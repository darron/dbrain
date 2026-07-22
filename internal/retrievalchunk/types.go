package retrievalchunk

const (
	Version           = "retrieval-chunker-v2"
	ProjectionVersion = "retrieval-projection-v1"
)

const (
	defaultTargetRunes  = 2400
	defaultMaxRunes     = 3600
	defaultOverlapRunes = 300
)

type Section struct {
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
	Ordinal           int
	SectionOrdinal    int
	StartChar         int
	EndChar           int
	Heading           string
	ProjectionVersion string
	ChunkerVersion    string
	InputContentHash  string
	TextHash          string
	Text              string
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
