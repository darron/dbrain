package retrievalchunk

import (
	"fmt"
	"strings"
	"unicode"
)

// Build is retained for v1 callers until occurrence persistence lands. New
// callers must use BuildProjection so they do not mistake a content window for
// one parent-local location.
func Build(parent Parent, opts Options) ([]Chunk, error) {
	projection, err := BuildProjection(parent, opts)
	if err != nil {
		return nil, err
	}
	chunks := append([]Chunk(nil), projection.Chunks...)
	// Populate legacy fields from each chunk's first occurrence. They are not
	// chunk identity and persistence must use Projection.Occurrences instead.
	byID := make(map[string]Occurrence, len(projection.Occurrences))
	for _, occurrence := range projection.Occurrences {
		if _, ok := byID[occurrence.ChunkID]; !ok {
			byID[occurrence.ChunkID] = occurrence
		}
	}
	for ordinal := range chunks {
		if occurrence, ok := byID[chunks[ordinal].ID]; ok {
			chunks[ordinal].Ordinal = ordinal
			chunks[ordinal].StartChar = occurrence.StartChar
			chunks[ordinal].EndChar = occurrence.EndChar
		}
	}
	return chunks, nil
}

func BuildProjection(parent Parent, opts Options) (Projection, error) {
	if strings.TrimSpace(parent.Kind) == "" {
		return Projection{}, fmt.Errorf("parent kind is required")
	}
	if strings.TrimSpace(parent.SourceKey) == "" {
		return Projection{}, fmt.Errorf("parent source key is required")
	}
	if opts.TargetRunes <= 0 || opts.MaxRunes < opts.TargetRunes {
		return Projection{}, fmt.Errorf("invalid chunk sizes: target=%d max=%d", opts.TargetRunes, opts.MaxRunes)
	}
	if opts.OverlapRunes != 0 {
		return Projection{}, fmt.Errorf("retrieval chunker v3 requires zero overlap, got %d", opts.OverlapRunes)
	}

	projection := Projection{ParentHash: parentHash(parent), Chunks: make([]Chunk, 0), Occurrences: make([]Occurrence, 0)}
	seenSections := make(map[string]struct{}, len(parent.Sections))
	seenChunks := make(map[string]struct{})
	for sectionOrdinal, section := range parent.Sections {
		section.Role = strings.TrimSpace(section.Role)
		section.Heading = strings.TrimSpace(section.Heading)
		section.Key = sectionKey(parent, section)
		if section.Role == "" {
			return Projection{}, fmt.Errorf("section evidence role is required")
		}
		if _, duplicate := seenSections[section.Key]; duplicate {
			return Projection{}, fmt.Errorf("duplicate section key %q", section.Key)
		}
		seenSections[section.Key] = struct{}{}
		if strings.TrimSpace(section.Text) == "" {
			continue
		}
		windows := chunkSectionV3(section.Text, opts)
		for _, window := range windows {
			text := strings.TrimSpace(string([]rune(section.Text)[window.start:window.end]))
			if text == "" {
				continue
			}
			start, end := trimRuneOffsets(section.Text, window.start, window.end)
			hash := textHash(text)
			headHash := headingHash(section.Heading)
			id := chunkID(section.Key, section.Role, section.Derived, headHash, hash)
			if _, found := seenChunks[id]; !found {
				projection.Chunks = append(projection.Chunks, Chunk{ID: id, ParentKind: parent.Kind, ParentSourceKey: parent.SourceKey, SectionKey: section.Key, EvidenceRole: section.Role, SectionOrdinal: sectionOrdinal, Heading: section.Heading, HeadingHash: headHash, Derived: section.Derived, ProjectionVersion: ProjectionVersion, ChunkerVersion: Version, InputContentHash: parent.ContentHash, TextHash: hash, Text: text})
				seenChunks[id] = struct{}{}
			}
			projection.Occurrences = append(projection.Occurrences, Occurrence{ChunkID: id, SectionKey: section.Key, StartChar: start, EndChar: end})
		}
	}
	return projection, nil
}

type window struct{ start, end int }

func chunkSectionV3(text string, opts Options) []window {
	runes := []rune(text)
	result := make([]window, 0, len(runes)/max(1, opts.TargetRunes)+1)
	for start := 0; start < len(runes); {
		end := chooseEndV3(runes, start, opts)
		if end <= start {
			end = nextByteSafe(runes, start, MaxUTF8Bytes)
		}
		result = append(result, window{start: start, end: end})
		if end == len(runes) {
			break
		}
		// V3 windows are non-overlapping. Their content-defined endpoints make a
		// later unchanged window reusable after an earlier edit.
		start = end
	}
	return result
}

func chooseEndV3(text []rune, start int, opts Options) int {
	if byteLenRunes(text[start:]) <= MaxUTF8Bytes && len(text)-start <= opts.MaxRunes {
		return len(text)
	}
	hard := min(nextByteSafe(text, start, MaxUTF8Bytes), min(start+opts.MaxRunes, len(text)))
	target := min(start+opts.TargetRunes, hard)
	if target <= start {
		target = hard
	}
	// Paragraph and sentence boundaries are content-defined and therefore let
	// the stream re-synchronize within one hard byte ceiling after a local edit.
	if end := preferredBoundary(text, start, target, hard, paragraphBoundary); end > start {
		return end
	}
	if end := preferredBoundary(text, start, target, hard, sentenceAt); end > start {
		return end
	}
	if end := rollingBoundary(text, start, target, hard); end > start {
		return end
	}
	if whitespaceAt(text, start, target) {
		return target
	}
	return hard
}

func preferredBoundary(text []rune, start, target, hard int, predicate func([]rune, int) bool) int {
	best, distance := 0, int(^uint(0)>>1)
	for i := start + 1; i <= hard; i++ {
		if !predicate(text, i) {
			continue
		}
		d := i - target
		if d < 0 {
			d = -d
		}
		if d < distance || (d == distance && i > best) {
			best, distance = i, d
		}
	}
	return best
}

func paragraphBoundary(text []rune, at int) bool {
	if at < 2 || at > len(text) || text[at-1] != '\n' {
		return false
	}
	previous := at - 2
	if text[previous] == '\r' {
		previous--
	}
	return previous >= 0 && text[previous] == '\n'
}
func sentenceAt(text []rune, at int) bool {
	if at < 1 || at > len(text) {
		return false
	}
	r := text[at-1]
	return (r == '.' || r == '!' || r == '?') && (at == len(text) || unicode.IsSpace(text[at]))
}
func whitespaceAt(text []rune, start, at int) bool {
	return at > start && at <= len(text) && unicode.IsSpace(text[at-1])
}

// rollingBoundary supplies a deterministic content-defined fall back for long
// unstructured input. The bounded scan is what limits re-synchronization to a
// single 1,800-byte ceiling.
func rollingBoundary(text []rune, start, target, hard int) int {
	best, bestFingerprint := 0, ^uint64(0)
	const window = 32
	from := start + window
	for at := from; at <= hard; at++ {
		fingerprint := fixedWindowFingerprint(text, at, window)
		// Rightmost-minimum winnowing always chooses an intrinsic anchor in
		// every hard interval, including adversarial all-equal input.
		if fingerprint <= bestFingerprint {
			best, bestFingerprint = at, fingerprint
		}
	}
	_ = target
	return best
}

// fixedWindowFingerprint is deliberately calculated from the preceding fixed
// window only. Once that window is beyond an edit, its state is independent of
// the prior chunk start and enables content-defined re-synchronization.
func fixedWindowFingerprint(text []rune, at, window int) uint64 {
	var value uint64 = 1469598103934665603
	for _, r := range text[at-window : at] {
		value = (value ^ uint64(r)) * 1099511628211
	}
	return value
}

func nextByteSafe(text []rune, start, limit int) int {
	used := 0
	for i := start; i < len(text); i++ {
		width := len(string(text[i]))
		if used+width > limit {
			return i
		}
		used += width
	}
	return len(text)
}
func byteLenRunes(text []rune) int {
	n := 0
	for _, r := range text {
		n += len(string(r))
	}
	return n
}
func trimRuneOffsets(text string, start, end int) (int, int) {
	runes := []rune(text)
	for start < end && unicode.IsSpace(runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	return start, end
}
