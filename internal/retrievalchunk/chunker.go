package retrievalchunk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const preparedStreamPlanVersion = "retrieval-stream-plan-v1"

type PreparedStreamPlan struct {
	data preparedStreamPlanData
}

type preparedStreamPlanData struct {
	Version           string                  `json:"version"`
	ProjectionVersion string                  `json:"projection_version"`
	ChunkerVersion    string                  `json:"chunker_version"`
	ParentHash        string                  `json:"parent_hash"`
	TargetRunes       int                     `json:"target_runes"`
	MaxRunes          int                     `json:"max_runes"`
	OverlapRunes      int                     `json:"overlap_runes"`
	OccurrenceCount   int                     `json:"occurrence_count"`
	Sections          []preparedStreamSection `json:"sections"`
}

type preparedStreamSection struct {
	Key       string                 `json:"key"`
	RuneCount int                    `json:"rune_count"`
	Windows   []preparedStreamWindow `json:"windows"`
}

type preparedStreamWindow struct {
	StartBoundary int  `json:"start_boundary"`
	NextBoundary  int  `json:"next_boundary"`
	StartChar     int  `json:"start_char"`
	EndChar       int  `json:"end_char"`
	StartByte     int  `json:"start_byte"`
	EndByte       int  `json:"end_byte"`
	Emit          bool `json:"emit"`
}

type PreparedStreamOccurrenceLimitError struct {
	OccurrenceCount int
	Limit           int
}

var prepareStreamSectionWindows = chunkSectionRunesV3

func (e *PreparedStreamOccurrenceLimitError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("retrieval stream plan contains at least %d occurrences, limit %d", e.OccurrenceCount, e.Limit)
}

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
	projection := Projection{Chunks: make([]Chunk, 0), Occurrences: make([]Occurrence, 0)}
	seenChunks := make(map[string]struct{})
	_, done, err := Stream(parent, opts, Cursor{}, func(chunk Chunk, occurrence Occurrence) error {
		if _, found := seenChunks[chunk.ID]; !found {
			projection.Chunks = append(projection.Chunks, chunk)
			seenChunks[chunk.ID] = struct{}{}
		}
		projection.Occurrences = append(projection.Occurrences, occurrence)
		return nil
	})
	if err != nil {
		return Projection{}, err
	}
	if !done {
		return Projection{}, fmt.Errorf("retrieval chunk stream ended without completion")
	}
	projection.ParentHash = parentHash(parent)
	return projection, nil
}

// Cursor is a durable v3 streaming position. SectionKey is the normalized,
// parent-bound section identity and NextBoundary is the next untrimmed rune
// boundary to emit within that section.
type Cursor struct {
	SectionKey   string
	NextBoundary int
}

// ParentProjectionHash returns the exact projection-v2 parent identity without
// materializing its chunks. It validates the same parent/section contract as
// Stream and BuildProjection.
func ParentProjectionHash(parent Parent) (string, error) {
	if _, err := normalizedStreamingSections(parent, DefaultOptions(), false); err != nil {
		return "", err
	}
	return parentHash(parent), nil
}

// Stream emits v3 windows from cursor without retaining the complete
// projection. When emit returns an error, the returned cursor is positioned
// after the successfully delivered window so callers can durably checkpoint
// the batch before resuming.
func Stream(parent Parent, opts Options, cursor Cursor, emit func(Chunk, Occurrence) error) (Cursor, bool, error) {
	plan, err := PrepareStream(parent, opts, 0)
	if err != nil {
		return cursor, false, err
	}
	return StreamPrepared(parent, opts, plan, cursor, emit)
}

// PrepareStream computes the content-defined boundary plan once. The opaque
// result can be persisted and reused across batches without rescanning section
// text from the beginning. maxOccurrences <= 0 means no caller-imposed limit.
func PrepareStream(parent Parent, opts Options, maxOccurrences int) (PreparedStreamPlan, error) {
	return prepareStreamWithPlanner(context.Background(), parent, opts, maxOccurrences, 0, func(text []rune) ([]window, error) {
		return prepareStreamSectionWindows(text, opts), nil
	})
}

// PrepareStreamContext computes the same deterministic plan as PrepareStream
// while allowing readiness/status callers to cancel bounded planning before a
// large dirty parent monopolizes a read transaction.
func PrepareStreamContext(ctx context.Context, parent Parent, opts Options, maxOccurrences int) (PreparedStreamPlan, error) {
	return prepareStreamWithPlanner(ctx, parent, opts, maxOccurrences, V3MaximumPlanningInputUTF8Bytes, func(text []rune) ([]window, error) {
		return chunkSectionRunesV3Context(ctx, text, opts)
	})
}

func prepareStreamWithPlanner(ctx context.Context, parent Parent, opts Options, maxOccurrences, planningByteLimit int, planSection func([]rune) ([]window, error)) (PreparedStreamPlan, error) {
	if err := ctx.Err(); err != nil {
		return PreparedStreamPlan{}, err
	}
	if planningByteLimit > 0 {
		if err := validatePlanningInputBytes(parent, planningByteLimit); err != nil {
			return PreparedStreamPlan{}, err
		}
	}
	sections, err := normalizedStreamingSections(parent, opts, true)
	if err != nil {
		return PreparedStreamPlan{}, err
	}
	data := preparedStreamPlanData{
		Version: preparedStreamPlanVersion, ProjectionVersion: ProjectionVersion,
		ChunkerVersion: Version, ParentHash: parentHash(parent),
		TargetRunes: opts.TargetRunes, MaxRunes: opts.MaxRunes, OverlapRunes: opts.OverlapRunes,
		Sections: make([]preparedStreamSection, 0, len(sections)),
	}
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return PreparedStreamPlan{}, err
		}
		runes := []rune(section.Text)
		byteOffsets := make([]int, len(runes)+1)
		for i, value := range runes {
			byteOffsets[i+1] = byteOffsets[i] + utf8.RuneLen(value)
		}
		prepared := preparedStreamSection{Key: section.Key, RuneCount: len(runes)}
		windows, err := planSection(runes)
		if err != nil {
			return PreparedStreamPlan{}, err
		}
		for windowIndex, current := range windows {
			if windowIndex%64 == 0 {
				if err := ctx.Err(); err != nil {
					return PreparedStreamPlan{}, err
				}
			}
			start, end := trimRuneOffsetsRunes(runes, current.start, current.end)
			emit := start < end
			prepared.Windows = append(prepared.Windows, preparedStreamWindow{
				StartBoundary: current.start, NextBoundary: current.end,
				StartChar: start, EndChar: end, StartByte: byteOffsets[start], EndByte: byteOffsets[end], Emit: emit,
			})
			if emit {
				data.OccurrenceCount++
				if maxOccurrences > 0 && data.OccurrenceCount > maxOccurrences {
					return PreparedStreamPlan{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: data.OccurrenceCount, Limit: maxOccurrences}
				}
			}
		}
		data.Sections = append(data.Sections, prepared)
	}
	return PreparedStreamPlan{data: data}, nil
}

func validatePlanningInputBytes(parent Parent, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("retrieval planning byte ceiling must be positive")
	}
	total := 0
	for i, section := range parent.Sections {
		sectionBytes := 0
		for _, value := range section.Text {
			width := utf8.RuneLen(value)
			if width < 0 || sectionBytes > limit-width {
				return fmt.Errorf("retrieval parent %s %s section %d exceeds planning byte ceiling %d", parent.Kind, parent.SourceKey, i, limit)
			}
			sectionBytes += width
		}
		if total > limit-sectionBytes {
			return fmt.Errorf("retrieval parent %s %s section %d exceeds planning byte ceiling %d", parent.Kind, parent.SourceKey, i, limit)
		}
		total += sectionBytes
	}
	return nil
}

func (p PreparedStreamPlan) MarshalBinary() ([]byte, error) {
	if p.data.Version != preparedStreamPlanVersion {
		return nil, fmt.Errorf("invalid retrieval prepared stream plan")
	}
	return json.Marshal(p.data)
}

func (p PreparedStreamPlan) OccurrenceCount() int { return p.data.OccurrenceCount }

func ParsePreparedStreamPlan(parent Parent, opts Options, encoded []byte, maxOccurrences int) (PreparedStreamPlan, error) {
	var data preparedStreamPlanData
	if err := json.Unmarshal(encoded, &data); err != nil {
		return PreparedStreamPlan{}, fmt.Errorf("decode retrieval prepared stream plan: %w", err)
	}
	sections, err := normalizedStreamingSections(parent, opts, true)
	if err != nil {
		return PreparedStreamPlan{}, err
	}
	if data.Version != preparedStreamPlanVersion || data.ProjectionVersion != ProjectionVersion || data.ChunkerVersion != Version ||
		data.ParentHash != parentHash(parent) || data.TargetRunes != opts.TargetRunes || data.MaxRunes != opts.MaxRunes || data.OverlapRunes != opts.OverlapRunes ||
		len(data.Sections) != len(sections) {
		return PreparedStreamPlan{}, fmt.Errorf("retrieval prepared stream plan provenance does not match parent and chunker")
	}
	count := 0
	for i, prepared := range data.Sections {
		if prepared.Key != sections[i].Key || prepared.RuneCount != utf8.RuneCountInString(sections[i].Text) {
			return PreparedStreamPlan{}, fmt.Errorf("retrieval prepared stream plan section %d does not match parent", i)
		}
		previous := 0
		for _, window := range prepared.Windows {
			if window.StartBoundary != previous || window.NextBoundary <= window.StartBoundary || window.NextBoundary > prepared.RuneCount ||
				window.StartChar < window.StartBoundary || window.EndChar > window.NextBoundary || window.EndChar < window.StartChar ||
				window.StartByte < 0 || window.EndByte < window.StartByte || window.EndByte > len(sections[i].Text) {
				return PreparedStreamPlan{}, fmt.Errorf("retrieval prepared stream plan has invalid section %d boundary", i)
			}
			if window.Emit {
				count++
			}
			previous = window.NextBoundary
		}
		if len(prepared.Windows) > 0 && previous != prepared.RuneCount {
			return PreparedStreamPlan{}, fmt.Errorf("retrieval prepared stream plan does not cover section %d", i)
		}
	}
	if count != data.OccurrenceCount {
		return PreparedStreamPlan{}, fmt.Errorf("retrieval prepared stream plan occurrence count mismatch")
	}
	if maxOccurrences > 0 && count > maxOccurrences {
		return PreparedStreamPlan{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: maxOccurrences + 1, Limit: maxOccurrences}
	}
	return PreparedStreamPlan{data: data}, nil
}

func StreamPrepared(parent Parent, opts Options, plan PreparedStreamPlan, cursor Cursor, emit func(Chunk, Occurrence) error) (Cursor, bool, error) {
	if emit == nil {
		return cursor, false, fmt.Errorf("retrieval chunk stream emit callback is required")
	}
	sections, err := normalizedStreamingSections(parent, opts, true)
	if err != nil {
		return cursor, false, err
	}
	if plan.data.Version != preparedStreamPlanVersion || plan.data.ParentHash != parentHash(parent) || plan.data.ProjectionVersion != ProjectionVersion || plan.data.ChunkerVersion != Version ||
		plan.data.TargetRunes != opts.TargetRunes || plan.data.MaxRunes != opts.MaxRunes || plan.data.OverlapRunes != opts.OverlapRunes || len(plan.data.Sections) != len(sections) {
		return cursor, false, fmt.Errorf("retrieval prepared stream plan provenance does not match parent and chunker")
	}
	startSection := 0
	if cursor.SectionKey != "" {
		startSection = -1
		for i := range plan.data.Sections {
			if plan.data.Sections[i].Key == cursor.SectionKey {
				startSection = i
				break
			}
		}
		if startSection < 0 {
			return cursor, false, fmt.Errorf("retrieval chunk cursor section %q is not in parent", cursor.SectionKey)
		}
	}
	for sectionOrdinal := startSection; sectionOrdinal < len(sections); sectionOrdinal++ {
		section := sections[sectionOrdinal]
		prepared := plan.data.Sections[sectionOrdinal]
		boundary := 0
		if sectionOrdinal == startSection && cursor.SectionKey != "" {
			boundary = cursor.NextBoundary
			if boundary < 0 || boundary > prepared.RuneCount {
				return cursor, false, fmt.Errorf("retrieval chunk cursor boundary %d is outside section %q", boundary, section.Key)
			}
		}
		if boundary != 0 {
			found := boundary == prepared.RuneCount
			for _, candidate := range prepared.Windows {
				if candidate.StartBoundary == boundary {
					found = true
					break
				}
			}
			if !found {
				return cursor, false, fmt.Errorf("retrieval chunk cursor boundary %d is not a v3 boundary in section %q", boundary, section.Key)
			}
		}
		for _, current := range prepared.Windows {
			if current.StartBoundary < boundary {
				continue
			}
			if !current.Emit {
				continue
			}
			text := section.Text[current.StartByte:current.EndByte]
			textHash := textHash(text)
			headingHash := headingHash(section.Heading)
			id := chunkID(section.Key, section.Role, section.Derived, headingHash, textHash)
			chunk := Chunk{ID: id, ParentKind: parent.Kind, ParentSourceKey: parent.SourceKey, SectionKey: section.Key, EvidenceRole: section.Role, SectionOrdinal: sectionOrdinal, Heading: section.Heading, HeadingHash: headingHash, Derived: section.Derived, ProjectionVersion: ProjectionVersion, ChunkerVersion: Version, InputContentHash: parent.ContentHash, TextHash: textHash, Text: text}
			occurrence := Occurrence{ChunkID: id, SectionKey: section.Key, StartChar: current.StartChar, EndChar: current.EndChar}
			next := Cursor{SectionKey: section.Key, NextBoundary: current.NextBoundary}
			if current.NextBoundary == prepared.RuneCount && sectionOrdinal+1 < len(sections) {
				next = Cursor{SectionKey: sections[sectionOrdinal+1].Key}
			}
			if err := emit(chunk, occurrence); err != nil {
				return next, false, err
			}
		}
	}
	return Cursor{}, true, nil
}

func normalizedStreamingSections(parent Parent, opts Options, validateOptions bool) ([]Section, error) {
	parent.Kind = strings.TrimSpace(parent.Kind)
	parent.SourceKey = strings.TrimSpace(parent.SourceKey)
	if parent.Kind == "" {
		return nil, fmt.Errorf("parent kind is required")
	}
	if parent.SourceKey == "" {
		return nil, fmt.Errorf("parent source key is required")
	}
	if validateOptions {
		if opts.TargetRunes <= 0 || opts.MaxRunes < opts.TargetRunes {
			return nil, fmt.Errorf("invalid chunk sizes: target=%d max=%d", opts.TargetRunes, opts.MaxRunes)
		}
		if opts.OverlapRunes != 0 {
			return nil, fmt.Errorf("retrieval chunker v3 requires zero overlap, got %d", opts.OverlapRunes)
		}
	}
	sections := append([]Section(nil), parent.Sections...)
	seen := make(map[string]struct{}, len(sections))
	for i := range sections {
		// Imported evidence can contain malformed UTF-8. Chunker v3 has always
		// operated on Go runes, which replaces malformed byte sequences with
		// U+FFFD. Normalize once here so the prepared byte offsets address the
		// exact string later sliced by StreamPrepared instead of the shorter raw
		// byte string.
		sections[i].Text = string([]rune(sections[i].Text))
		sections[i].Role = strings.TrimSpace(sections[i].Role)
		sections[i].Heading = strings.TrimSpace(sections[i].Heading)
		sections[i].Key = sectionKey(parent, sections[i])
		if sections[i].Role == "" {
			return nil, fmt.Errorf("section evidence role is required")
		}
		if _, duplicate := seen[sections[i].Key]; duplicate {
			return nil, fmt.Errorf("duplicate section key %q", sections[i].Key)
		}
		seen[sections[i].Key] = struct{}{}
	}
	return sections, nil
}

type window struct{ start, end int }

func chunkSectionV3(text string, opts Options) []window {
	return chunkSectionRunesV3([]rune(text), opts)
}

func chunkSectionRunesV3(text []rune, opts Options) []window {
	result, _ := chunkSectionRunesV3Context(context.Background(), text, opts)
	return result
}

func chunkSectionRunesV3Context(ctx context.Context, text []rune, opts Options) ([]window, error) {
	if len(text) == 0 {
		return nil, nil
	}
	anchors, err := globalAnchorsContext(ctx, text, opts)
	if err != nil {
		return nil, err
	}
	result := make([]window, 0, len(anchors)+1)
	start := 0
	for i, end := range anchors {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if end > start && end < len(text) {
			result = append(result, window{start: start, end: end})
			start = end
		}
	}
	if start < len(text) {
		result = append(result, window{start: start, end: len(text)})
	}
	return result, nil
}

type anchorCandidate struct {
	runePosition int
	bytePosition int
	rank         uint8
	fingerprint  uint64
}

func globalAnchorsContext(ctx context.Context, text []rune, opts Options) ([]int, error) {
	fingerprintRunes := min(32, max(1, min(opts.TargetRunes, opts.MaxRunes)/4))
	candidates := make([]anchorCandidate, 0, max(0, len(text)-1))
	byteOffsets := make([]int, len(text)+1)
	for at, r := range text {
		if at%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		byteOffsets[at+1] = byteOffsets[at] + utf8.RuneLen(r)
		runePosition := at + 1
		if runePosition == len(text) {
			break
		}
		candidates = append(candidates, anchorCandidate{
			runePosition: runePosition,
			bytePosition: byteOffsets[runePosition],
			rank:         boundaryRank(text, runePosition),
			fingerprint:  fixedWindowFingerprint(text, runePosition, fingerprintRunes),
		})
	}
	totalBytes := byteOffsets[len(text)]
	selected := make(map[int]struct{})
	// Reserve the local fingerprint radius inside each hard limit so an edit's
	// influence plus the minimizer window remains bounded by that limit.
	if err := winnowAnchorsContext(ctx, candidates, len(text), max(1, opts.MaxRunes-fingerprintRunes), false, selected); err != nil {
		return nil, err
	}
	if err := winnowAnchorsContext(ctx, candidates, totalBytes, MaxUTF8Bytes-fingerprintRunes*utf8.UTFMax, true, selected); err != nil {
		return nil, err
	}
	// The section edge is itself a stable global boundary. Honor the historical
	// target-sized natural prefix from that edge, then let global minimizers own
	// every later cut. This avoids arbitrary prefix fingerprints defeating an
	// immediately available paragraph or sentence without coupling later cuts
	// to the emitted prefix.
	protected := -1
	if prefix, natural := prefixBoundary(text, byteOffsets, opts); prefix > 0 {
		for at := range selected {
			if at < prefix || (!natural && at <= hardEnd(byteOffsets, prefix, opts)) {
				delete(selected, at)
			}
		}
		selected[prefix] = struct{}{}
		protected = prefix
	}
	anchors := make([]int, 0, len(selected))
	for at := range selected {
		anchors = append(anchors, at)
	}
	sort.Ints(anchors)
	anchors, err := compactDenseAnchorsContext(ctx, text, anchors, fingerprintRunes, protected)
	if err != nil {
		return nil, err
	}
	return boundAnchorGapsContext(ctx, byteOffsets, anchors, opts)
}

// compactDenseAnchors collapses the short burst of distinct fingerprints that
// a local edit can create. It keeps the rightmost boundary in each non-natural
// cluster, so compaction depends only on the precomputed global anchors within
// one fingerprint radius. Paragraph/sentence anchors and the stable prefix are
// never removed.
func compactDenseAnchorsContext(ctx context.Context, text []rune, anchors []int, radius, protected int) ([]int, error) {
	result := make([]int, 0, len(anchors))
	for i := 0; i < len(anchors); {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		at := anchors[i]
		if at == protected || boundaryRank(text, at) <= 1 {
			result = append(result, at)
			i++
			continue
		}
		last := i
		for last+1 < len(anchors) && anchors[last+1] != protected && boundaryRank(text, anchors[last+1]) > 1 && anchors[last+1]-anchors[last] < radius {
			last++
		}
		result = append(result, anchors[last])
		i = last + 1
	}
	return result, nil
}

func prefixBoundary(text []rune, byteOffsets []int, opts Options) (int, bool) {
	hard := hardEnd(byteOffsets, 0, opts)
	if hard == len(text) {
		return 0, false
	}
	target := min(opts.TargetRunes, hard)
	if end := preferredBoundary(text, 0, target, hard, paragraphBoundary); end > 0 {
		return end, true
	}
	if end := preferredBoundary(text, 0, target, hard, sentenceAt); end > 0 {
		return end, true
	}
	if end := preferredBoundary(text, 0, target, hard, func(value []rune, at int) bool {
		return whitespaceAt(value, 0, at)
	}); end > 0 {
		return end, true
	}
	// A section edge is a stable exception to the global minimizer stream. It
	// restores a useful target-sized first window when no natural boundary is
	// available; later ordinary-content cuts still come from global anchors.
	return target, false
}

// boundAnchorGaps fills only gaps where indistinguishable minimizer scores were
// suppressed. Ordinary content retains its globally selected boundaries. Each
// inserted cut uses the useful target, capped by both independent hard limits.
func boundAnchorGapsContext(ctx context.Context, byteOffsets, anchors []int, opts Options) ([]int, error) {
	result := make([]int, 0, len(anchors)+1)
	start := 0
	for i, end := range append(append([]int(nil), anchors...), len(byteOffsets)-1) {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if end <= start {
			continue
		}
		for end > hardEnd(byteOffsets, start, opts) {
			cut := min(start+opts.TargetRunes, hardEnd(byteOffsets, start, opts))
			if cut <= start {
				cut = hardEnd(byteOffsets, start, opts)
			}
			result = append(result, cut)
			start = cut
		}
		if end < len(byteOffsets)-1 {
			result = append(result, end)
		}
		start = end
	}
	return result, nil
}

func hardEnd(byteOffsets []int, start int, opts Options) int {
	runeEnd := min(start+opts.MaxRunes, len(byteOffsets)-1)
	byteLimit := byteOffsets[start] + MaxUTF8Bytes
	byteEnd := sort.Search(len(byteOffsets), func(at int) bool { return byteOffsets[at] > byteLimit }) - 1
	return min(runeEnd, max(start+1, byteEnd))
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

func winnowAnchorsContext(ctx context.Context, candidates []anchorCandidate, total, span int, bytes bool, selected map[int]struct{}) error {
	if span <= 0 || total <= span || len(candidates) == 0 {
		return nil
	}
	position := func(candidate anchorCandidate) int {
		if bytes {
			return candidate.bytePosition
		}
		return candidate.runePosition
	}
	deque := make([]int, 0, span)
	next := 0
	lastSelected := -1
	for at := 1; at < total; at++ {
		if at%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
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
			minimum := deque[0]
			if lastSelected < 0 || !anchorSameScore(candidates[minimum], candidates[lastSelected]) {
				selected[candidates[minimum].runePosition] = struct{}{}
				lastSelected = minimum
			}
		}
	}
	return nil
}

func anchorLessOrEqual(left, right anchorCandidate) bool {
	return left.rank < right.rank || (left.rank == right.rank && left.fingerprint <= right.fingerprint)
}

func anchorSameScore(left, right anchorCandidate) bool {
	return left.rank == right.rank && left.fingerprint == right.fingerprint
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
