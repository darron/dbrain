package retrievalchunk

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
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
		sectionRunes := []rune(section.Text)
		windows := chunkSectionRunesV3(sectionRunes, opts)
		for _, window := range windows {
			text := strings.TrimSpace(string(sectionRunes[window.start:window.end]))
			if text == "" {
				continue
			}
			start, end := trimRuneOffsetsRunes(sectionRunes, window.start, window.end)
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
	return chunkSectionRunesV3([]rune(text), opts)
}

func chunkSectionRunesV3(text []rune, opts Options) []window {
	if len(text) == 0 {
		return nil
	}
	anchors := globalAnchors(text, opts)
	result := make([]window, 0, len(anchors)+1)
	start := 0
	for _, end := range anchors {
		if end > start && end < len(text) {
			result = append(result, window{start: start, end: end})
			start = end
		}
	}
	if start < len(text) {
		result = append(result, window{start: start, end: len(text)})
	}
	return result
}

type anchorCandidate struct {
	runePosition int
	bytePosition int
	rank         uint8
	fingerprint  uint64
}

// globalAnchors computes all section boundaries before emitting any window.
// Two deterministic winnowing passes enforce the independent rune and UTF-8
// byte ceilings. Their union remains content-local: a prior emitted boundary
// is never an input to selecting a later boundary.
func globalAnchors(text []rune, opts Options) []int {
	const fingerprintRunes = 32
	candidates := make([]anchorCandidate, 0, max(0, len(text)-1))
	bytePosition := 0
	for at, r := range text {
		bytePosition += utf8.RuneLen(r)
		runePosition := at + 1
		if runePosition == len(text) {
			break
		}
		candidates = append(candidates, anchorCandidate{
			runePosition: runePosition,
			bytePosition: bytePosition,
			rank:         boundaryRank(text, runePosition),
			fingerprint:  fixedWindowFingerprint(text, runePosition, fingerprintRunes),
		})
	}
	totalBytes := bytePosition
	selected := make(map[int]struct{})
	// Reserve the local fingerprint radius inside each hard limit so an edit's
	// influence plus the minimizer window remains bounded by that limit.
	winnowAnchors(candidates, len(text), max(1, opts.MaxRunes-fingerprintRunes), false, selected)
	winnowAnchors(candidates, totalBytes, MaxUTF8Bytes-fingerprintRunes*utf8.UTFMax, true, selected)
	// The section edge is itself a stable global boundary. Honor the historical
	// target-sized natural prefix from that edge, then let global minimizers own
	// every later cut. This avoids arbitrary prefix fingerprints defeating an
	// immediately available paragraph or sentence without coupling later cuts
	// to the emitted prefix.
	if prefix := prefixPreferredBoundary(text, opts); prefix > 0 {
		for at := range selected {
			if at < prefix {
				delete(selected, at)
			}
		}
		selected[prefix] = struct{}{}
	}
	anchors := make([]int, 0, len(selected))
	for at := range selected {
		anchors = append(anchors, at)
	}
	sort.Ints(anchors)
	return anchors
}

func prefixPreferredBoundary(text []rune, opts Options) int {
	hard := min(opts.MaxRunes, len(text))
	bytes := 0
	for at := 0; at < hard; at++ {
		width := utf8.RuneLen(text[at])
		if bytes+width > MaxUTF8Bytes {
			hard = at
			break
		}
		bytes += width
	}
	if hard == len(text) {
		return 0
	}
	target := min(opts.TargetRunes, hard)
	if end := preferredBoundary(text, 0, target, hard, paragraphBoundary); end > 0 {
		return end
	}
	if end := preferredBoundary(text, 0, target, hard, sentenceAt); end > 0 {
		return end
	}
	return preferredBoundary(text, 0, target, hard, func(value []rune, at int) bool {
		return whitespaceAt(value, 0, at)
	})
}

func preferredBoundary(text []rune, start, target, hard int, predicate func([]rune, int) bool) int {
	best, distance := 0, int(^uint(0)>>1)
	for at := start + 1; at <= hard; at++ {
		if !predicate(text, at) {
			continue
		}
		delta := at - target
		if delta < 0 {
			delta = -delta
		}
		if delta < distance || (delta == distance && at > best) {
			best, distance = at, delta
		}
	}
	return best
}

func boundaryRank(text []rune, at int) uint8 {
	switch {
	case paragraphBoundary(text, at):
		return 0
	case sentenceAt(text, at):
		return 1
	case whitespaceAt(text, 0, at):
		return 2
	default:
		return 3
	}
}

// winnowAnchors selects the rightmost lexicographic minimum in every fixed
// coordinate window. The deque makes the pass linear. Equal fingerprints on
// adversarial periodic input intentionally select every rightmost minimum;
// translationally invariant content cannot otherwise provide sparse anchors
// while also guaranteeing a hard maximum gap.
func winnowAnchors(candidates []anchorCandidate, total, span int, bytes bool, selected map[int]struct{}) {
	if span <= 0 || total <= span || len(candidates) == 0 {
		return
	}
	position := func(candidate anchorCandidate) int {
		if bytes {
			return candidate.bytePosition
		}
		return candidate.runePosition
	}
	deque := make([]int, 0, span)
	next := 0
	for at := 1; at < total; at++ {
		for next < len(candidates) && position(candidates[next]) == at {
			candidate := candidates[next]
			for len(deque) > 0 && anchorLessOrEqual(candidate, candidates[deque[len(deque)-1]]) {
				deque = deque[:len(deque)-1]
			}
			deque = append(deque, next)
			next++
		}
		windowStart := at - span + 1
		for len(deque) > 0 && position(candidates[deque[0]]) < windowStart {
			deque = deque[1:]
		}
		if at >= span && len(deque) > 0 {
			selected[candidates[deque[0]].runePosition] = struct{}{}
		}
	}
}

func anchorLessOrEqual(left, right anchorCandidate) bool {
	return left.rank < right.rank || (left.rank == right.rank && left.fingerprint <= right.fingerprint)
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

// fixedWindowFingerprint is deliberately calculated from the preceding fixed
// window only. Once that window is beyond an edit, its state is independent of
// the prior chunk start and enables content-defined re-synchronization.
func fixedWindowFingerprint(text []rune, at, window int) uint64 {
	var value uint64 = 1469598103934665603
	from := max(0, at-window)
	value = (value ^ uint64(at-from)) * 1099511628211
	for _, r := range text[from:at] {
		value = (value ^ uint64(r)) * 1099511628211
	}
	return value
}
func trimRuneOffsetsRunes(text []rune, start, end int) (int, int) {
	for start < end && unicode.IsSpace(text[start]) {
		start++
	}
	for end > start && unicode.IsSpace(text[end-1]) {
		end--
	}
	return start, end
}
