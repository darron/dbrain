package ask

import (
	"time"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/retrieval"
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

type Evidence = retrieval.EvidenceDocument

type RetrievalInfo = retrieval.RetrievalInfo

type RetrievalLane = retrieval.RetrievalLane

type RetrievalSignal = retrieval.RetrievalSignal

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
