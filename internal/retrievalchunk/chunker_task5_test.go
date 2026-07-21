package retrievalchunk

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTask5PreparedStreamNormalizesMalformedUTF8BeforeByteSlicing(t *testing.T) {
	parent := Parent{
		Kind: "source", SourceKey: "source:malformed-utf8",
		Sections: []Section{{Key: "body", Role: "raw", Text: "a\xff\xfeb"}},
	}
	projection, err := BuildProjection(parent, Options{TargetRunes: 100, MaxRunes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Chunks) == 0 {
		t.Fatal("malformed UTF-8 projection emitted no chunks")
	}
	for _, chunk := range projection.Chunks {
		if !utf8.ValidString(chunk.Text) {
			t.Fatalf("chunk contains malformed UTF-8: %q", chunk.Text)
		}
	}
	if got, want := projection.Chunks[0].Text, "a\uFFFD\uFFFDb"; got != want {
		t.Fatalf("projected malformed text=%q want rune-decoder parity %q", got, want)
	}
	if len(projection.Occurrences) != 1 || projection.Occurrences[0].StartChar != 0 || projection.Occurrences[0].EndChar != 4 {
		t.Fatalf("malformed UTF-8 occurrence=%+v want exact sanitized rune offsets [0,4)", projection.Occurrences)
	}
}

func TestTask5PreparedGiantStreamPlansBoundariesOnceAcrossBatchesAndRestart(t *testing.T) {
	originalPlanner := prepareStreamSectionWindows
	planningCalls := 0
	prepareStreamSectionWindows = func(text []rune, opts Options) []window {
		planningCalls++
		return originalPlanner(text, opts)
	}
	t.Cleanup(func() { prepareStreamSectionWindows = originalPlanner })
	var text strings.Builder
	for i := 0; i < 4_000; i++ {
		_, _ = fmt.Fprintf(&text, "unique-%05d evidence. ", i)
	}
	parent := Parent{Kind: "source", SourceKey: "source:prepared-giant", ContentHash: "v1", Sections: []Section{{Key: "body", Role: "raw", Text: text.String()}}}
	opts := Options{TargetRunes: 24, MaxRunes: 32}
	plan, err := PrepareStream(parent, opts, 20_000)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := plan.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	plan, err = ParsePreparedStreamPlan(parent, opts, encoded, 20_000)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("batch full")
	cursor := Cursor{}
	emitted := 0
	batches := 0
	for {
		batch := 0
		next, done, err := StreamPrepared(parent, opts, plan, cursor, func(Chunk, Occurrence) error {
			batch++
			emitted++
			if batch == 37 {
				return stop
			}
			return nil
		})
		batches++
		if done {
			break
		}
		if !errors.Is(err, stop) {
			t.Fatalf("batch %d err=%v", batches, err)
		}
		cursor = next
		// Simulate a process restart by decoding the same durable opaque plan.
		plan, err = ParsePreparedStreamPlan(parent, opts, encoded, 20_000)
		if err != nil {
			t.Fatal(err)
		}
	}
	if batches < 20 || emitted != plan.OccurrenceCount() || planningCalls != 1 {
		t.Fatalf("batches=%d emitted=%d planning_calls=%d occurrences=%d", batches, emitted, planningCalls, plan.OccurrenceCount())
	}
}
