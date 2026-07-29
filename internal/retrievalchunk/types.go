package retrievalchunk

const (
	Version           = "retrieval-chunker-v3"
	ProjectionVersion = "retrieval-projection-v2"
	MaxUTF8Bytes      = 1800

	// V3ByteCeiling, V3MaximumOverlapUTF8Bytes, and
	// V3MinimumForwardUTF8Bytes are the published deterministic planning
	// bounds for chunker v3. Natural boundaries may be dense, so the only
	// guaranteed forward byte advance is one. Readiness therefore uses exact
	// capped v3 occurrence planning rather than a byte-ratio approximation.
	V3ByteCeiling                   = MaxUTF8Bytes
	V3MaximumOverlapUTF8Bytes       = 0
	V3MinimumForwardUTF8Bytes       = 1
	V3MaximumPlanningInputUTF8Bytes = 128 << 20
	// Bound section metadata independently from text bytes. Empty sections
	// still consume normalized-section and duplicate-key map entries.
	V3MaximumPlanningSections = 4_096
	// Exact capped readiness planning may allocate rune/anchor/window state
	// only below this normalized-input ceiling. The allocation-free preflight
	// still scans up to V3MaximumPlanningInputUTF8Bytes and proves ordinary
	// oversized content over budget; sparse giant input fails closed.
	V3MaximumExactPlanningInputUTF8Bytes = 8 << 20
)

const (
	defaultTargetRunes  = 2400
	defaultMaxRunes     = 3600
	defaultOverlapRunes = 0
)

type Section struct {
	// Key is a durable content-section identity. It intentionally excludes the
	// section text so changing a word does not invalidate all later windows.
	Key     string
	Role    string
	Heading string
	Text    string
	Derived bool
}

type Parent struct {
	Kind        string
	SourceKey   string
	ContentHash string
	Title       string
	SourceType  string
	Author      string
	Sections    []Section
}

type Chunk struct {
	ID                string
	ParentKind        string
	ParentSourceKey   string
	EvidenceRole      string
	SectionKey        string
	Ordinal           int
	SectionOrdinal    int
	StartChar         int
	EndChar           int
	Heading           string
	HeadingHash       string
	Derived           bool
	ProjectionVersion string
	ChunkerVersion    string
	InputContentHash  string
	TextHash          string
	Text              string
}

// Occurrence records where a reusable content-local chunk occurs in one
// projected section. Offsets are rune offsets in the untrimmed Section.Text.
type Occurrence struct {
	ChunkID    string
	SectionKey string
	StartChar  int
	EndChar    int
}

// Projection is the v2 parent projection. Chunks are unique content windows;
// occurrences retain the parent-local locations without polluting chunk IDs.
type Projection struct {
	ParentHash  string
	Chunks      []Chunk
	Occurrences []Occurrence
}

type Options struct {
	TargetRunes  int
	MaxRunes     int
	OverlapRunes int
}

func DefaultOptions() Options {
	return Options{
		TargetRunes:  defaultTargetRunes,
		MaxRunes:     defaultMaxRunes,
		OverlapRunes: defaultOverlapRunes,
	}
}
