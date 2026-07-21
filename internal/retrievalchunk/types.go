package retrievalchunk

const (
	Version           = "retrieval-chunker-v3"
	ProjectionVersion = "retrieval-projection-v2"
	MaxUTF8Bytes      = 1800
)

const (
	defaultTargetRunes  = 2400
	defaultMaxRunes     = 3600
	defaultOverlapRunes = 300
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
