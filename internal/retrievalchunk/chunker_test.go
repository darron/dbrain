package retrievalchunk

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildProjectionV2UsesContentLocalChunksAndOccurrences(t *testing.T) {
	parent := Parent{Kind: "source", SourceKey: "src:projection", ContentHash: "parent-v1", Sections: []Section{
		{Key: "body", Role: "raw", Heading: "Body", Text: "  same window  "},
		{Key: "body-copy", Role: "raw", Heading: "Body", Text: "same window"},
	}}
	projection, err := BuildProjection(parent, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if projection.ParentHash == "" || len(projection.Chunks) != 2 || len(projection.Occurrences) != 2 {
		t.Fatalf("projection = %+v", projection)
	}
	if projection.Chunks[0].Text != "same window" || projection.Occurrences[0].StartChar != 2 || projection.Occurrences[0].EndChar != 13 {
		t.Fatalf("trimmed occurrence = %+v chunk=%+v", projection.Occurrences[0], projection.Chunks[0])
	}
	if projection.Chunks[0].ID == projection.Chunks[1].ID {
		t.Fatal("section keys must keep equal text from distinct sections distinct")
	}
}

func TestOccurrenceDeduplicatesDuplicateWindowsDeterministically(t *testing.T) {
	repeated := strings.Repeat("repeat ", 200)
	parent := Parent{Kind: "item", SourceKey: "item:dupes", Sections: []Section{{Key: "body", Role: "raw", Heading: "Body", Text: repeated + "\n\n" + repeated}}}
	first, err := BuildProjection(parent, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildProjection(parent, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Chunks) >= len(first.Occurrences) {
		t.Fatalf("projection = %+v", first)
	}
	if first.Occurrences[0].ChunkID != first.Occurrences[1].ChunkID {
		t.Fatal("duplicate window got distinct identities")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("projection output is not deterministic")
	}
}

func TestBuildProjectionV2RejectsDuplicateSectionKeys(t *testing.T) {
	_, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:duplicate", Sections: []Section{
		{Key: "same", Role: "raw", Text: "one"}, {Key: "same", Role: "raw", Text: "two"},
	}}, DefaultOptions())
	if err == nil || !strings.Contains(err.Error(), "duplicate section key") {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectionV2NamespacesChunksByStableParentIdentity(t *testing.T) {
	build := func(source string) Projection {
		p, err := BuildProjection(Parent{Kind: "source", SourceKey: source, Sections: []Section{{Key: "body", Role: "raw", Heading: "Body", Text: "identical evidence"}}}, DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if build("src:one").Chunks[0].ID == build("src:two").Chunks[0].ID {
		t.Fatal("cross-parent chunk identities collided")
	}
}

func TestProjectionV2ParentHashIncludesEveryProjectedSectionField(t *testing.T) {
	base := Parent{Kind: "source", SourceKey: "src:hash", ContentHash: "unchanged", Sections: []Section{{Key: "body", Role: "raw", Heading: "One", Text: "evidence"}}}
	one, err := BuildProjection(base, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.Sections[0].Derived = true
	two, err := BuildProjection(changed, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if one.ParentHash == two.ParentHash {
		t.Fatal("derived change did not affect parent hash")
	}
}

func TestChunkerV3EnforcesUTF8ByteCeilingAndRuneOffsets(t *testing.T) {
	text := "  " + strings.Repeat("🧠漢字é ", 800) + "  "
	projection, err := BuildProjection(Parent{Kind: "item", SourceKey: "item:utf8-v3", Sections: []Section{{Key: "body", Role: "raw", Text: text}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(text)
	for _, occurrence := range projection.Occurrences {
		chunk := projection.Chunks[chunkIndex(t, projection.Chunks, occurrence.ChunkID)]
		if len([]byte(chunk.Text)) > MaxUTF8Bytes || !utf8.ValidString(chunk.Text) {
			t.Fatalf("invalid chunk: bytes=%d text=%q", len([]byte(chunk.Text)), chunk.Text)
		}
		if chunk.Text != string(runes[occurrence.StartChar:occurrence.EndChar]) {
			t.Fatalf("offsets [%d,%d) do not select chunk", occurrence.StartChar, occurrence.EndChar)
		}
		if strings.TrimSpace(chunk.Text) != chunk.Text {
			t.Fatalf("chunk has whitespace tail: %q", chunk.Text)
		}
	}
}

func TestChunkerV3ReusesMovedWindowsAndLimitsDistantEditChurn(t *testing.T) {
	sections := synthetic26512WindowFixture()
	fixture, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:fixture", Sections: sections}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.Chunks) != 26_512 || len(fixture.Occurrences) != 26_512 {
		t.Fatalf("fixture windows chunks=%d occurrences=%d, want 26512", len(fixture.Chunks), len(fixture.Occurrences))
	}
	base := syntheticUnstructuredFixture()
	before, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:local", Sections: []Section{{Key: "body", Role: "raw", Heading: "Fixture", Text: base}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	editAt := len(base) / 2
	afterText := base[:editAt] + "X" + base[editAt:]
	after, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:local", Sections: []Section{{Key: "body", Role: "raw", Heading: "Fixture", Text: afterText}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	changed := symmetricChunkIDs(before.Chunks, after.Chunks)
	if changed > 8 {
		t.Fatalf("unstructured insertion changed %d chunk identities, want <= 8", changed)
	}
	deleted := afterText[:editAt] + afterText[editAt+1:]
	afterDeletion, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:local", Sections: []Section{{Key: "body", Role: "raw", Heading: "Fixture", Text: deleted}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if changed := symmetricChunkIDs(before.Chunks, afterDeletion.Chunks); changed > 8 {
		t.Fatalf("unstructured deletion changed %d chunk identities, want <= 8", changed)
	}
	// Moving an unchanged paragraph changes its occurrence, not its chunk identity.
	movable := strings.Repeat("movable semantic evidence ", 60)
	filler := strings.Repeat("unstructured filler ", 80)
	movedBefore, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:moved", Sections: []Section{{Key: "body", Role: "raw", Heading: "Fixture", Text: movable + "\n\n" + filler + "\n\n" + filler}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	movedProjection, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:moved", Sections: []Section{{Key: "body", Role: "raw", Heading: "Fixture", Text: filler + "\n\n" + filler + "\n\n" + movable}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !sharesChunkText(movedBefore.Chunks, movedProjection.Chunks, strings.TrimSpace(movable)) {
		t.Fatal("moved window did not reuse content-local identity")
	}
}

func TestChunkerV3HeadingChangesIdentity(t *testing.T) {
	build := func(heading string) Projection {
		p, err := BuildProjection(Parent{Kind: "source", SourceKey: "src:heading", Sections: []Section{{Key: "body", Role: "raw", Heading: heading, Text: "same body"}}}, DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if build("One").Chunks[0].ID == build("Two").Chunks[0].ID {
		t.Fatal("heading did not affect content-local identity")
	}
}

func chunkIndex(t *testing.T, chunks []Chunk, id string) int {
	t.Helper()
	for i := range chunks {
		if chunks[i].ID == id {
			return i
		}
	}
	t.Fatalf("chunk %s missing", id)
	return 0
}
func symmetricChunkIDs(left, right []Chunk) int {
	a, b := map[string]bool{}, map[string]bool{}
	for _, c := range left {
		a[c.ID] = true
	}
	for _, c := range right {
		b[c.ID] = true
	}
	n := 0
	for id := range a {
		if !b[id] {
			n++
		}
	}
	for id := range b {
		if !a[id] {
			n++
		}
	}
	return n
}
func sharesChunkText(left, right []Chunk, text string) bool {
	var id string
	for _, c := range left {
		if c.Text == text {
			id = c.ID
			break
		}
	}
	if id == "" {
		return false
	}
	for _, c := range right {
		if c.ID == id {
			return true
		}
	}
	return false
}

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
		if i > 0 && chunks[i-1].EndChar > chunk.StartChar {
			t.Fatalf("v3 content windows must make forward progress: chunk %d starts at %d after prior end %d", i, chunk.StartChar, chunks[i-1].EndChar)
		}
	}
	if got := chunks[0].EndChar - chunks[0].StartChar; got > DefaultOptions().TargetRunes {
		t.Fatalf("first chunk has %d runes, expected paragraph boundary at or before target %d", got, DefaultOptions().TargetRunes)
	}
}

func TestBuildUsesRuneOffsetsForUTF8(t *testing.T) {
	text := strings.Repeat("é🧠漢字 ", 900)
	opts := Options{TargetRunes: 120, MaxRunes: 160, OverlapRunes: 0}
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
			{Key: "first", Role: "raw", Heading: "", Text: "first raw field"},
			{Key: "second", Role: "raw", Heading: "", Text: "second raw field"},
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
	wantEnd := len([]rune(firstParagraph))
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
	wantEnd := len([]rune(strings.TrimSpace(intro + middle)))
	if chunks[0].EndChar != wantEnd {
		t.Fatalf("first chunk ends at %d, want closer forward paragraph boundary %d", chunks[0].EndChar, wantEnd)
	}
	if chunks[0].ChunkerVersion != "retrieval-chunker-v3" {
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
	if chunks[0].EndChar != 19 || chunks[0].Text != "word word word word" {
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
	if chunks[0].EndChar <= 0 || chunks[0].EndChar > opts.MaxRunes || chunks[0].Text != strings.Repeat("界", chunks[0].EndChar) {
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
	if one[0].ID != three[0].ID {
		t.Fatal("parent content hash must not affect content-local chunk ID")
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
	opts.OverlapRunes = 1
	_, err := Build(Parent{
		Kind: "source", SourceKey: "src:overlap", ContentHash: "overlap-v1",
		Sections: []Section{{Role: "raw", Text: "evidence"}},
	}, opts)
	if err == nil {
		t.Fatal("expected nonzero overlap to be rejected by v3")
	}
}
