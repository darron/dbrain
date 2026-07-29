package retrievalchunk

import (
	"context"
	"encoding/json"
	"errors"
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

// PreparedStreamSession owns the normalized parent and its validated plan for
// one projection invocation. Reusing it across batches avoids re-normalizing
// and re-hashing the complete parent at every durable checkpoint.
type PreparedStreamSession struct {
	parent   Parent
	sections []Section
	plan     PreparedStreamPlan
	planHash string
}

func (s PreparedStreamSession) Plan() PreparedStreamPlan { return s.plan }
func (s PreparedStreamSession) PlanDigest() string       { return s.planHash }

func (s *PreparedStreamSession) MarshalPlanBinary() ([]byte, string, error) {
	encoded, err := s.plan.MarshalBinary()
	if err != nil {
		return nil, "", err
	}
	s.planHash = PreparedStreamPlanDigest(encoded)
	return encoded, s.planHash, nil
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

// ParentProjectionHashContext computes the same projection identity while
// bounding metadata and input scanning and honoring command cancellation. Hash
// construction is streaming, so it uses the 128 MiB command-planning ceiling
// rather than readiness's separate 8 MiB exact-window allocation ceiling.
func ParentProjectionHashContext(ctx context.Context, parent Parent) (string, error) {
	return parentProjectionHashContext(ctx, parent, V3MaximumPlanningSections, V3MaximumPlanningInputUTF8Bytes)
}

func parentProjectionHashContext(ctx context.Context, parent Parent, maxSections, maxBytes int) (string, error) {
	if err := validateStreamingMetadataContext(ctx, parent, DefaultOptions(), maxSections); err != nil {
		return "", err
	}
	if err := validatePlanningInputBytes(ctx, parent, maxBytes); err != nil {
		return "", err
	}
	return parentHashContext(ctx, parent)
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

// PrepareStreamCappedContext is the readiness planner. It performs the
// allocation-free v3 preflight before materializing an exact boundary plan.
// Dense inputs can stop at the maxOccurrences+1 sentinel while sparse inputs
// fail closed above readiness's separate exact-planning allocation ceiling.
func PrepareStreamCappedContext(ctx context.Context, parent Parent, opts Options, maxOccurrences int) (PreparedStreamPlan, error) {
	if maxOccurrences < 0 {
		return PreparedStreamPlan{}, fmt.Errorf("retrieval occurrence limit must not be negative")
	}
	if err := validateStreamingMetadataContext(ctx, parent, opts, V3MaximumPlanningSections); err != nil {
		return PreparedStreamPlan{}, err
	}
	if maxOccurrences == 0 {
		if err := validatePlanningInputBytes(ctx, parent, V3MaximumExactPlanningInputUTF8Bytes); err != nil {
			return PreparedStreamPlan{}, err
		}
		return PrepareStreamContext(ctx, parent, opts, 0)
	}
	over, normalizedBytes, err := occurrenceLimitPreflight(ctx, parent, maxOccurrences)
	if err != nil {
		return PreparedStreamPlan{}, err
	}
	if over {
		return PreparedStreamPlan{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: maxOccurrences + 1, Limit: maxOccurrences}
	}
	if normalizedBytes > V3MaximumExactPlanningInputUTF8Bytes {
		return PreparedStreamPlan{}, fmt.Errorf("retrieval parent %s %s normalized input %d exceeds exact planning allocation ceiling %d", parent.Kind, parent.SourceKey, normalizedBytes, V3MaximumExactPlanningInputUTF8Bytes)
	}
	return PrepareStreamContext(ctx, parent, opts, maxOccurrences)
}

// PrepareStreamCommandContext is the projection-worker planner. It shares the
// allocation-free occurrence preflight, cancellation checks, 4,096-section
// metadata cap, and 128 MiB input ceiling, but intentionally does not apply
// readiness's narrower 8 MiB exact-planning cap. Projection execution is
// independently bounded by its 50k chunk, 200k occurrence, and 128 MiB staged
// state contract.
func PrepareStreamCommandContext(ctx context.Context, parent Parent, opts Options, maxOccurrences int) (PreparedStreamPlan, error) {
	session, err := PrepareStreamCommandSessionContext(ctx, parent, opts, maxOccurrences)
	if err != nil {
		return PreparedStreamPlan{}, err
	}
	return session.plan, nil
}

func PrepareStreamCommandSessionContext(ctx context.Context, parent Parent, opts Options, maxOccurrences int) (PreparedStreamSession, error) {
	if maxOccurrences < 0 {
		return PreparedStreamSession{}, fmt.Errorf("retrieval occurrence limit must not be negative")
	}
	if err := validateStreamingMetadataContext(ctx, parent, opts, V3MaximumPlanningSections); err != nil {
		return PreparedStreamSession{}, err
	}
	if maxOccurrences == 0 {
		if err := validatePlanningInputBytes(ctx, parent, V3MaximumPlanningInputUTF8Bytes); err != nil {
			return PreparedStreamSession{}, err
		}
		return prepareStreamSessionWithPlanner(ctx, parent, opts, 0, V3MaximumPlanningInputUTF8Bytes, func(text []rune) ([]window, error) {
			return chunkSectionRunesV3Context(ctx, text, opts)
		})
	}
	over, _, err := occurrenceLimitPreflight(ctx, parent, maxOccurrences)
	if err != nil {
		return PreparedStreamSession{}, err
	}
	if over {
		return PreparedStreamSession{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: maxOccurrences + 1, Limit: maxOccurrences}
	}
	return prepareStreamSessionWithPlanner(ctx, parent, opts, maxOccurrences, V3MaximumPlanningInputUTF8Bytes, func(text []rune) ([]window, error) {
		return chunkSectionRunesV3Context(ctx, text, opts)
	})
}

// CountOccurrencesCappedContext returns the exact chunker-v3 occurrence count
// when it is at most limit, and limit+1 as a stable over-budget sentinel. The
// preflight is deliberately only a proof of overage, never an estimator: v3
// windows are a zero-overlap partition and every untrimmed window is at most
// MaxUTF8Bytes. Greedily covering every non-whitespace rune start with the
// furthest possible interval of that size is a lower bound on final emitted
// windows, regardless of natural anchors.
// This lets readiness reject giant parents without cloning the unread tail into
// []rune, anchor candidates, or prepared windows. Inputs not proven over budget
// use the ordinary exact planner so all counts through limit remain identical.
func CountOccurrencesCappedContext(ctx context.Context, parent Parent, opts Options, limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("retrieval occurrence limit must not be negative")
	}
	if limit == 0 {
		if err := validateStreamingMetadataContext(ctx, parent, opts, V3MaximumPlanningSections); err != nil {
			return 0, err
		}
		over, _, err := occurrenceLimitPreflight(ctx, parent, 0)
		if err != nil {
			return 0, err
		}
		if over {
			return 1, nil
		}
		return 0, nil
	}
	plan, err := PrepareStreamCappedContext(ctx, parent, opts, limit)
	if err != nil {
		var exceeded *PreparedStreamOccurrenceLimitError
		if errors.As(err, &exceeded) {
			return limit + 1, nil
		}
		return 0, err
	}
	return plan.OccurrenceCount(), nil
}

func validateStreamingMetadataContext(ctx context.Context, parent Parent, opts Options, maxSections int) error {
	if strings.TrimSpace(parent.Kind) == "" {
		return fmt.Errorf("parent kind is required")
	}
	if strings.TrimSpace(parent.SourceKey) == "" {
		return fmt.Errorf("parent source key is required")
	}
	if opts.TargetRunes <= 0 || opts.MaxRunes < opts.TargetRunes {
		return fmt.Errorf("invalid chunk sizes: target=%d max=%d", opts.TargetRunes, opts.MaxRunes)
	}
	if opts.OverlapRunes != 0 {
		return fmt.Errorf("retrieval chunker v3 requires zero overlap, got %d", opts.OverlapRunes)
	}
	if maxSections > 0 && len(parent.Sections) > maxSections {
		return fmt.Errorf("retrieval parent %s %s section count %d exceeds planning section ceiling %d", parent.Kind, parent.SourceKey, len(parent.Sections), maxSections)
	}
	seen := make(map[string]struct{}, len(parent.Sections))
	for i, section := range parent.Sections {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if strings.TrimSpace(section.Role) == "" {
			return fmt.Errorf("section evidence role is required")
		}
		key := sectionKey(parent, section)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate section key %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func occurrenceLimitPreflight(ctx context.Context, parent Parent, limit int) (bool, int, error) {
	// len is a constant-time raw-byte guard. Malformed UTF-8 can expand during
	// normalization, so the incremental normalized-byte counter below retains
	// the authoritative ceiling for every prefix that must actually be read.
	rawBytes := 0
	for i, section := range parent.Sections {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return false, 0, err
			}
		}
		if len(section.Text) > V3MaximumPlanningInputUTF8Bytes-rawBytes {
			return false, 0, fmt.Errorf("retrieval parent %s %s section %d exceeds planning byte ceiling %d", parent.Kind, parent.SourceKey, i, V3MaximumPlanningInputUTF8Bytes)
		}
		rawBytes += len(section.Text)
	}
	normalizedBytes := 0
	lowerBound := 0
	for i, section := range parent.Sections {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return false, 0, err
			}
		}
		sectionByteAt := 0
		coveredThrough := 0
		checkedBytes := 0
		for _, value := range section.Text {
			width := utf8.RuneLen(value)
			if width < 0 || normalizedBytes > V3MaximumPlanningInputUTF8Bytes-width {
				return false, 0, fmt.Errorf("retrieval parent %s %s section %d exceeds planning byte ceiling %d", parent.Kind, parent.SourceKey, i, V3MaximumPlanningInputUTF8Bytes)
			}
			normalizedBytes += width
			checkedBytes += width
			if checkedBytes >= 4<<10 {
				if err := ctx.Err(); err != nil {
					return false, 0, err
				}
				checkedBytes = 0
			}
			if !unicode.IsSpace(value) && sectionByteAt >= coveredThrough {
				// Greedily cover the first uncovered non-whitespace rune start
				// with the furthest possible <=1,800-byte interval. Any final
				// zero-overlap v3 windows form another such cover, so this is a
				// safe lower bound on emitted occurrences, not an estimate.
				lowerBound++
				if lowerBound > limit {
					return true, normalizedBytes, nil
				}
				coveredThrough = sectionByteAt + MaxUTF8Bytes
			}
			sectionByteAt += width
		}
	}
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	return false, normalizedBytes, nil
}

func prepareStreamWithPlanner(ctx context.Context, parent Parent, opts Options, maxOccurrences, planningByteLimit int, planSection func([]rune) ([]window, error)) (PreparedStreamPlan, error) {
	session, err := prepareStreamSessionWithPlanner(ctx, parent, opts, maxOccurrences, planningByteLimit, planSection)
	if err != nil {
		return PreparedStreamPlan{}, err
	}
	return session.plan, nil
}

func prepareStreamSessionWithPlanner(ctx context.Context, parent Parent, opts Options, maxOccurrences, planningByteLimit int, planSection func([]rune) ([]window, error)) (PreparedStreamSession, error) {
	if err := ctx.Err(); err != nil {
		return PreparedStreamSession{}, err
	}
	if planningByteLimit > 0 {
		if err := validateStreamingMetadataContext(ctx, parent, opts, V3MaximumPlanningSections); err != nil {
			return PreparedStreamSession{}, err
		}
		if err := validatePlanningInputBytes(ctx, parent, planningByteLimit); err != nil {
			return PreparedStreamSession{}, err
		}
	}
	sections, err := normalizedStreamingSectionsContext(ctx, parent, opts, true)
	if err != nil {
		return PreparedStreamSession{}, err
	}
	projectionHash, err := parentHashContext(ctx, parent)
	if err != nil {
		return PreparedStreamSession{}, err
	}
	data := preparedStreamPlanData{
		Version: preparedStreamPlanVersion, ProjectionVersion: ProjectionVersion,
		ChunkerVersion: Version, ParentHash: projectionHash,
		TargetRunes: opts.TargetRunes, MaxRunes: opts.MaxRunes, OverlapRunes: opts.OverlapRunes,
		Sections: make([]preparedStreamSection, 0, len(sections)),
	}
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return PreparedStreamSession{}, err
		}
		runes, err := stringRunesContext(ctx, section.Text)
		if err != nil {
			return PreparedStreamSession{}, err
		}
		byteOffsets := make([]int, len(runes)+1)
		for i, value := range runes {
			if i%1_024 == 0 {
				if err := ctx.Err(); err != nil {
					return PreparedStreamSession{}, err
				}
			}
			byteOffsets[i+1] = byteOffsets[i] + utf8.RuneLen(value)
		}
		prepared := preparedStreamSection{Key: section.Key, RuneCount: len(runes)}
		windows, err := planSection(runes)
		if err != nil {
			return PreparedStreamSession{}, err
		}
		for windowIndex, current := range windows {
			if windowIndex%64 == 0 {
				if err := ctx.Err(); err != nil {
					return PreparedStreamSession{}, err
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
					return PreparedStreamSession{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: data.OccurrenceCount, Limit: maxOccurrences}
				}
			}
		}
		data.Sections = append(data.Sections, prepared)
	}
	plan := PreparedStreamPlan{data: data}
	return PreparedStreamSession{parent: parent, sections: sections, plan: plan}, nil
}

func validatePlanningInputBytes(ctx context.Context, parent Parent, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("retrieval planning byte ceiling must be positive")
	}
	total := 0
	for i, section := range parent.Sections {
		sectionBytes := 0
		checkedBytes := 0
		for _, value := range section.Text {
			width := utf8.RuneLen(value)
			if width < 0 || sectionBytes > limit-width {
				return fmt.Errorf("retrieval parent %s %s section %d exceeds planning byte ceiling %d", parent.Kind, parent.SourceKey, i, limit)
			}
			sectionBytes += width
			checkedBytes += width
			if checkedBytes >= 4<<10 {
				if err := ctx.Err(); err != nil {
					return err
				}
				checkedBytes = 0
			}
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
	return ParsePreparedStreamPlanContext(context.Background(), parent, opts, encoded, maxOccurrences)
}

// ParsePreparedStreamPlanContext validates persisted seek state under the same
// section, input, occurrence, and cancellation bounds as fresh command
// planning. A checkpoint created by an older unbounded command cannot bypass
// the current v3 planner ceilings on resume.
func ParsePreparedStreamPlanContext(ctx context.Context, parent Parent, opts Options, encoded []byte, maxOccurrences int) (PreparedStreamPlan, error) {
	session, err := ParsePreparedStreamSessionContext(ctx, parent, opts, encoded, maxOccurrences)
	if err != nil {
		return PreparedStreamPlan{}, err
	}
	return session.plan, nil
}

func ParsePreparedStreamSessionContext(ctx context.Context, parent Parent, opts Options, encoded []byte, maxOccurrences int) (PreparedStreamSession, error) {
	if maxOccurrences < 0 {
		return PreparedStreamSession{}, fmt.Errorf("retrieval occurrence limit must not be negative")
	}
	if len(encoded) > V3MaximumPlanningInputUTF8Bytes {
		return PreparedStreamSession{}, fmt.Errorf("retrieval prepared stream plan %d exceeds plan byte ceiling %d", len(encoded), V3MaximumPlanningInputUTF8Bytes)
	}
	if err := validateStreamingMetadataContext(ctx, parent, opts, V3MaximumPlanningSections); err != nil {
		return PreparedStreamSession{}, err
	}
	if maxOccurrences == 0 {
		if err := validatePlanningInputBytes(ctx, parent, V3MaximumPlanningInputUTF8Bytes); err != nil {
			return PreparedStreamSession{}, err
		}
	} else {
		over, _, err := occurrenceLimitPreflight(ctx, parent, maxOccurrences)
		if err != nil {
			return PreparedStreamSession{}, err
		}
		if over {
			return PreparedStreamSession{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: maxOccurrences + 1, Limit: maxOccurrences}
		}
	}
	if err := ctx.Err(); err != nil {
		return PreparedStreamSession{}, err
	}
	var data preparedStreamPlanData
	if err := json.Unmarshal(encoded, &data); err != nil {
		return PreparedStreamSession{}, fmt.Errorf("decode retrieval prepared stream plan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return PreparedStreamSession{}, err
	}
	sections, err := normalizedStreamingSectionsContext(ctx, parent, opts, true)
	if err != nil {
		return PreparedStreamSession{}, err
	}
	projectionHash, err := parentHashContext(ctx, parent)
	if err != nil {
		return PreparedStreamSession{}, err
	}
	if data.Version != preparedStreamPlanVersion || data.ProjectionVersion != ProjectionVersion || data.ChunkerVersion != Version ||
		data.ParentHash != projectionHash || data.TargetRunes != opts.TargetRunes || data.MaxRunes != opts.MaxRunes || data.OverlapRunes != opts.OverlapRunes ||
		len(data.Sections) != len(sections) {
		return PreparedStreamSession{}, fmt.Errorf("retrieval prepared stream plan provenance does not match parent and chunker")
	}
	count := 0
	for i, prepared := range data.Sections {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return PreparedStreamSession{}, err
			}
		}
		runeCount, err := runeCountContext(ctx, sections[i].Text)
		if err != nil {
			return PreparedStreamSession{}, err
		}
		if prepared.Key != sections[i].Key || prepared.RuneCount != runeCount {
			return PreparedStreamSession{}, fmt.Errorf("retrieval prepared stream plan section %d does not match parent", i)
		}
		previous := 0
		for windowIndex, window := range prepared.Windows {
			if windowIndex%64 == 0 {
				if err := ctx.Err(); err != nil {
					return PreparedStreamSession{}, err
				}
			}
			if window.StartBoundary != previous || window.NextBoundary <= window.StartBoundary || window.NextBoundary > prepared.RuneCount ||
				window.StartChar < window.StartBoundary || window.EndChar > window.NextBoundary || window.EndChar < window.StartChar ||
				window.StartByte < 0 || window.EndByte < window.StartByte || window.EndByte > len(sections[i].Text) {
				return PreparedStreamSession{}, fmt.Errorf("retrieval prepared stream plan has invalid section %d boundary", i)
			}
			if window.Emit {
				count++
			}
			previous = window.NextBoundary
		}
		if len(prepared.Windows) > 0 && previous != prepared.RuneCount {
			return PreparedStreamSession{}, fmt.Errorf("retrieval prepared stream plan does not cover section %d", i)
		}
	}
	if count != data.OccurrenceCount {
		return PreparedStreamSession{}, fmt.Errorf("retrieval prepared stream plan occurrence count mismatch")
	}
	if maxOccurrences > 0 && count > maxOccurrences {
		return PreparedStreamSession{}, &PreparedStreamOccurrenceLimitError{OccurrenceCount: maxOccurrences + 1, Limit: maxOccurrences}
	}
	if err := ctx.Err(); err != nil {
		return PreparedStreamSession{}, err
	}
	plan := PreparedStreamPlan{data: data}
	return PreparedStreamSession{parent: parent, sections: sections, plan: plan, planHash: PreparedStreamPlanDigest(encoded)}, nil
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
	return streamPreparedSession(context.Background(), PreparedStreamSession{parent: parent, sections: sections, plan: plan}, cursor, emit)
}

func (s PreparedStreamSession) Stream(cursor Cursor, emit func(Chunk, Occurrence) error) (Cursor, bool, error) {
	return s.StreamContext(context.Background(), cursor, emit)
}

func (s PreparedStreamSession) StreamContext(ctx context.Context, cursor Cursor, emit func(Chunk, Occurrence) error) (Cursor, bool, error) {
	if emit == nil {
		return cursor, false, fmt.Errorf("retrieval chunk stream emit callback is required")
	}
	if err := ctx.Err(); err != nil {
		return cursor, false, err
	}
	if s.plan.data.Version != preparedStreamPlanVersion || len(s.sections) != len(s.plan.data.Sections) {
		return cursor, false, fmt.Errorf("invalid retrieval prepared stream session")
	}
	return streamPreparedSession(ctx, s, cursor, emit)
}

func streamPreparedSession(ctx context.Context, session PreparedStreamSession, cursor Cursor, emit func(Chunk, Occurrence) error) (Cursor, bool, error) {
	parent := session.parent
	sections := session.sections
	plan := session.plan
	resumeCursor := cursor
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
		if err := ctx.Err(); err != nil {
			return resumeCursor, false, err
		}
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
		for windowIndex, current := range prepared.Windows {
			if windowIndex%64 == 0 {
				if err := ctx.Err(); err != nil {
					return resumeCursor, false, err
				}
			}
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
			resumeCursor = next
		}
	}
	if err := ctx.Err(); err != nil {
		return resumeCursor, false, err
	}
	return Cursor{}, true, nil
}

func normalizedStreamingSections(parent Parent, opts Options, validateOptions bool) ([]Section, error) {
	return normalizedStreamingSectionsContext(context.Background(), parent, opts, validateOptions)
}

func normalizedStreamingSectionsContext(ctx context.Context, parent Parent, opts Options, validateOptions bool) ([]Section, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	sections := make([]Section, len(parent.Sections))
	seen := make(map[string]struct{}, len(sections))
	for i := range parent.Sections {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		sections[i] = parent.Sections[i]
		// Imported evidence can contain malformed UTF-8. Chunker v3 has always
		// operated on Go runes, which replaces malformed byte sequences with
		// U+FFFD. Normalize once here so the prepared byte offsets address the
		// exact string later sliced by StreamPrepared instead of the shorter raw
		// byte string.
		normalized, err := normalizeUTF8Context(ctx, sections[i].Text)
		if err != nil {
			return nil, err
		}
		sections[i].Text = normalized
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

func normalizeUTF8Context(ctx context.Context, value string) (string, error) {
	var normalized strings.Builder
	normalized.Grow(min(len(value), 4<<10))
	checkedBytes := 0
	for _, current := range value {
		if checkedBytes >= 4<<10 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			checkedBytes = 0
		}
		checkedBytes += utf8.RuneLen(current)
		_, _ = normalized.WriteRune(current)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return normalized.String(), nil
}

func stringRunesContext(ctx context.Context, value string) ([]rune, error) {
	result := make([]rune, 0, min(len(value), 4<<10))
	checkedBytes := 0
	for _, current := range value {
		if checkedBytes >= 4<<10 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			checkedBytes = 0
		}
		checkedBytes += utf8.RuneLen(current)
		result = append(result, current)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func runeCountContext(ctx context.Context, value string) (int, error) {
	count := 0
	checkedBytes := 0
	for _, current := range value {
		if checkedBytes >= 4<<10 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			checkedBytes = 0
		}
		checkedBytes += utf8.RuneLen(current)
		count++
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
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
