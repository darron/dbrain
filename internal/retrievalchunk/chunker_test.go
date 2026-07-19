package retrievalchunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildKeepsShortSectionAsOneChunk(t *testing.T) {
	parent := Parent{
		Kind:        "source",
		SourceKey:   "src:test",
		ContentHash: "input-v1",
		Sections: []Section{{
			Role:    "raw",
			Heading: "Architecture",
			Text:    "First paragraph.\n\nSecond paragraph.",
		}},
	}

	chunks, err := Build(parent, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	got := chunks[0]
	if got.Text != parent.Sections[0].Text || got.Heading != "Architecture" {
		t.Fatalf("chunk did not preserve text and heading: %+v", got)
	}
	if got.StartChar != 0 || got.EndChar != utf8.RuneCountInString(parent.Sections[0].Text) {
		t.Fatalf("got rune offsets [%d,%d)", got.StartChar, got.EndChar)
	}
}

func TestBuildUsesTargetHardMaximumAndBoundedOverlap(t *testing.T) {
	paragraph := func(label string) string { return label + " " + strings.Repeat("word ", 195) }
	text := strings.Join([]string{
		paragraph("one"), paragraph("two"), paragraph("three"), paragraph("four"),
		paragraph("five"), paragraph("six"), paragraph("seven"),
	}, "\n\n")

	chunks, err := Build(Parent{
		Kind: "source", SourceKey: "src:long", ContentHash: "long-v1",
		Sections: []Section{{Role: "raw", Heading: "Details", Text: text}},
	}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want several target-sized chunks", len(chunks))
	}
	runes := []rune(text)
	for i, chunk := range chunks {
		length := chunk.EndChar - chunk.StartChar
		if length > DefaultOptions().MaxRunes {
			t.Fatalf("chunk %d has %d runes, hard maximum is %d", i, length, DefaultOptions().MaxRunes)
		}
		if chunk.Text != string(runes[chunk.StartChar:chunk.EndChar]) {
			t.Fatalf("chunk %d text does not match its offsets", i)
		}
		if i > 0 {
			overlap := chunks[i-1].EndChar - chunk.StartChar
			if overlap < 0 || overlap > DefaultOptions().OverlapRunes {
				t.Fatalf("chunk %d overlap is %d, want 0..%d", i, overlap, DefaultOptions().OverlapRunes)
			}
		}
	}
	if got := chunks[0].EndChar - chunks[0].StartChar; got > DefaultOptions().TargetRunes {
		t.Fatalf("first chunk has %d runes, expected paragraph boundary at or before target %d", got, DefaultOptions().TargetRunes)
	}
}

func TestBuildUsesRuneOffsetsForUTF8(t *testing.T) {
	text := strings.Repeat("é🧠漢字 ", 900)
	opts := Options{TargetRunes: 120, MaxRunes: 160, OverlapRunes: 20}
	chunks, err := Build(Parent{
		Kind: "item", SourceKey: "x:utf8", ContentHash: "utf8-v1",
		Sections: []Section{{Role: "raw", Text: text}},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(text)
	for i, chunk := range chunks {
		if chunk.Text != string(runes[chunk.StartChar:chunk.EndChar]) {
			t.Fatalf("chunk %d corrupts UTF-8 or uses byte offsets: %+v", i, chunk)
		}
		if !utf8.ValidString(chunk.Text) {
			t.Fatalf("chunk %d contains invalid UTF-8", i)
		}
	}
}

func TestBuildSplitsOversizedParagraphDeterministically(t *testing.T) {
	text := strings.Repeat("This sentence has a deterministic boundary. ", 180)
	parent := Parent{
		Kind: "source", SourceKey: "src:oversized", ContentHash: "oversized-v1",
		Sections: []Section{{Role: "raw", Text: text}},
	}

	first, err := Build(parent, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(parent, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 2 {
		t.Fatalf("oversized paragraph produced %d chunks", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("chunk %d is not deterministic", i)
		}
		if first[i].EndChar-first[i].StartChar > DefaultOptions().MaxRunes {
			t.Fatalf("chunk %d exceeds hard maximum", i)
		}
	}
}

func TestBuildNeverMergesEvidenceRoles(t *testing.T) {
	sections := []Section{
		{Role: "raw", Text: "raw evidence"},
		{Role: "ocr", Text: "ocr evidence"},
		{Role: "transcript", Text: "transcript evidence"},
		{Role: "summary", Text: "derived evidence", Derived: true},
	}
	chunks, err := Build(Parent{
		Kind: "item", SourceKey: "item:roles", ContentHash: "roles-v1", Sections: sections,
	}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != len(sections) {
		t.Fatalf("got %d chunks, want one per role", len(chunks))
	}
	for i, chunk := range chunks {
		if chunk.EvidenceRole != sections[i].Role || chunk.Text != sections[i].Text {
			t.Fatalf("chunk %d merged or relabeled evidence: %+v", i, chunk)
		}
	}
}

func TestBuildIdentifiesSameRoleSameHeadingSectionsByOrdinal(t *testing.T) {
	chunks, err := Build(Parent{
		Kind: "item", SourceKey: "item:ambiguous", ContentHash: "ambiguous-v1",
		Sections: []Section{
			{Role: "raw", Heading: "", Text: "first raw field"},
			{Role: "raw", Heading: "", Text: "second raw field"},
		},
	}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].StartChar != 0 || chunks[1].StartChar != 0 {
		t.Fatalf("expected section-local offsets to both start at zero: %+v", chunks)
	}
	if chunks[0].EvidenceRole != chunks[1].EvidenceRole || chunks[0].Heading != chunks[1].Heading {
		t.Fatalf("fixture does not exercise ambiguous role/heading: %+v", chunks)
	}
	if chunks[0].SectionOrdinal != 0 || chunks[1].SectionOrdinal != 1 {
		t.Fatalf("section ordinals = [%d,%d], want [0,1]", chunks[0].SectionOrdinal, chunks[1].SectionOrdinal)
	}
	if chunks[0].ID == chunks[1].ID {
		t.Fatal("global chunk ordinal should keep IDs distinct without changing identity encoding")
	}
}

func TestBuildPreservesCRLFParagraphBoundaryPastTarget(t *testing.T) {
	firstParagraph := strings.Repeat("a", 120)
	text := firstParagraph + "\r\n\r\n" + strings.Repeat("b", 200)
	opts := Options{TargetRunes: 100, MaxRunes: 150, OverlapRunes: 0}
	chunks, err := Build(Parent{
		Kind: "source", SourceKey: "src:crlf", ContentHash: "crlf-v1",
		Sections: []Section{{Role: "raw", Text: text}},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := len([]rune(firstParagraph + "\r\n\r\n"))
	if chunks[0].EndChar != wantEnd {
		t.Fatalf("first chunk ends at %d, want CRLF paragraph boundary %d", chunks[0].EndChar, wantEnd)
	}
}

func TestBuildSkipsTinyIntroForCloserForwardParagraphBoundary(t *testing.T) {
	t.Parallel()

	intro := strings.Repeat("i", 20) + "\n\n"
	middle := strings.Repeat("m", 105) + "\n\n"
	text := intro + middle + strings.Repeat("tail ", 80)
	opts := Options{TargetRunes: 100, MaxRunes: 150, OverlapRunes: 0}
	chunks, err := Build(Parent{
		Kind: "source", SourceKey: "src:intro", ContentHash: "intro-v1",
		Sections: []Section{{Role: "raw", Text: text}},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := len([]rune(intro + middle))
	if chunks[0].EndChar != wantEnd {
		t.Fatalf("first chunk ends at %d, want closer forward paragraph boundary %d", chunks[0].EndChar, wantEnd)
	}
	if chunks[0].ChunkerVersion != "retrieval-chunker-v2" {
		t.Fatalf("boundary behavior changed without a new chunk identity version: %q", chunks[0].ChunkerVersion)
	}
}

func TestBuildFallsBackToWhitespaceBeforeRuneBoundary(t *testing.T) {
	opts := Options{TargetRunes: 20, MaxRunes: 30, OverlapRunes: 0}
	text := strings.Repeat("word ", 20)
	chunks, err := Build(Parent{
		Kind: "source", SourceKey: "src:whitespace", ContentHash: "whitespace-v1",
		Sections: []Section{{Role: "raw", Text: text}},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if chunks[0].EndChar != 20 || chunks[0].Text != "word word word word " {
		t.Fatalf("whitespace fallback chose %+v", chunks[0])
	}
}

func TestBuildFallsBackToRuneBoundaryWithoutWhitespace(t *testing.T) {
	opts := Options{TargetRunes: 20, MaxRunes: 30, OverlapRunes: 0}
	text := strings.Repeat("界", 75)
	chunks, err := Build(Parent{
		Kind: "source", SourceKey: "src:runes", ContentHash: "runes-v1",
		Sections: []Section{{Role: "raw", Text: text}},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if chunks[0].EndChar != 20 || chunks[0].Text != strings.Repeat("界", 20) {
		t.Fatalf("rune fallback chose %+v", chunks[0])
	}
}

func TestBuildStableIDsUseAllIdentityFields(t *testing.T) {
	base := Parent{
		Kind: "source", SourceKey: "src:id", ContentHash: "input-v1",
		Sections: []Section{{Role: "raw", Text: "stable evidence"}},
	}
	one, err := Build(base, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	two, err := Build(base, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if one[0].ID != two[0].ID || one[0].TextHash != two[0].TextHash {
		t.Fatalf("stable input produced unstable hashes: %+v %+v", one[0], two[0])
	}
	if len(one[0].ID) != 64 || len(one[0].TextHash) != 64 {
		t.Fatalf("expected hex SHA-256 identities, got id=%q text=%q", one[0].ID, one[0].TextHash)
	}

	changed := base
	changed.ContentHash = "input-v2"
	three, err := Build(changed, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if one[0].ID == three[0].ID {
		t.Fatal("input content hash did not affect chunk ID")
	}

	changed = base
	changed.Sections = []Section{{Role: "summary", Text: "stable evidence", Derived: true}}
	four, err := Build(changed, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if one[0].ID == four[0].ID {
		t.Fatal("evidence role did not affect chunk ID")
	}
}

func TestBuildRejectsOverlapAboveVersionMaximum(t *testing.T) {
	opts := DefaultOptions()
	opts.OverlapRunes = 301
	_, err := Build(Parent{
		Kind: "source", SourceKey: "src:overlap", ContentHash: "overlap-v1",
		Sections: []Section{{Role: "raw", Text: "evidence"}},
	}, opts)
	if err == nil {
		t.Fatal("expected overlap above 300 runes to be rejected")
	}
}
