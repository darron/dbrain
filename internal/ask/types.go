package ask

import (
	"time"

	"github.com/darron/dbrain/internal/entities"
)

const defaultMaxCharsPerDoc = 1800

type Options struct {
	Limit          int
	RetrieveOnly   bool
	Model          string
	CLI            string
	Length         string
	Timeout        time.Duration
	Binary         string
	MaxCharsPerDoc int
	SourceTypes    []string
	IncludeRelated bool
	RelatedLimit   int
	EntityIndex    []entities.Entity
	SearchLimit    int
	// DisableEntityExpansion keeps broad research retrieval off the expensive
	// global entity index path. Direct ask calls keep the previous default.
	DisableEntityExpansion bool
	// DisableTagExpansion skips broad LIKE/INSTR tag scans. FTS still searches
	// indexed user tags; exact tag examples can be gathered by callers that need
	// them without paying this cost for every query variant.
	DisableTagExpansion bool
}

type Evidence struct {
	SourceKey     string         `json:"source_key"`
	Kind          string         `json:"kind"`
	Title         string         `json:"title"`
	URL           string         `json:"url"`
	NotePath      string         `json:"note_path"`
	Summary       string         `json:"summary"`
	Excerpt       string         `json:"excerpt"`
	Author        string         `json:"author,omitempty"`
	SourceType    string         `json:"source_type,omitempty"`
	PublishedAt   string         `json:"published_at,omitempty"`
	ExtractedAt   string         `json:"extracted_at,omitempty"`
	SummarizedAt  string         `json:"summarized_at,omitempty"`
	UserTags      string         `json:"user_tags,omitempty"`
	EntityMatches []string       `json:"entity_matches,omitempty"`
	RelatedTo     string         `json:"related_to,omitempty"`
	Relationship  string         `json:"relationship,omitempty"`
	Retrieval     *RetrievalInfo `json:"retrieval,omitempty"`
}

type RetrievalInfo struct {
	Score        int               `json:"score"`
	Signals      []RetrievalSignal `json:"signals,omitempty"`
	MatchedTerms []string          `json:"matched_terms,omitempty"`
	MissingTerms []string          `json:"missing_terms,omitempty"`
}

type RetrievalSignal struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Weight int    `json:"weight"`
}

type Response struct {
	Question string     `json:"question"`
	Answer   string     `json:"answer"`
	Evidence []Evidence `json:"evidence"`
}

type QueryHints struct {
	TextQuery  string   `json:"text_query"`
	Terms      []string `json:"terms"`
	TagQueries []string `json:"tag_queries"`
}

type evidenceCandidate struct {
	Evidence
	ItemID    int64
	SourceID  int64
	Score     int
	MatchText string
}

type entityMatch struct {
	Labels []string
	Boost  int
}

type weightedQuery struct {
	Value  string
	Weight int
}
