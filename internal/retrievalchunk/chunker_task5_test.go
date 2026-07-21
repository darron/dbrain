package retrievalchunk

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

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
