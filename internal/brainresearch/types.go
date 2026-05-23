package brainresearch

import (
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
)

const (
	SchemaVersion       = "research_pack.v1"
	maxExactTagEvidence = 3
	maxQueryVariants    = 8
)

type Builder struct {
	cfg config.Config
	st  *store.Store
}

type Options struct {
	Question        string
	Topic           string
	Limit           int
	SourceTypes     []string
	IncludeRelated  bool
	RelatedLimit    int
	SeedLimit       int
	IncludeTopic    *bool
	MaxCharsPerDoc  int
	PlannerModel    string
	PlannerTimeout  time.Duration
	PlannerBinary   string
	UseModelPlanner bool
	DisablePlanner  bool
	UseSemantic     bool
	DisableSemantic bool
	Observer        Observer
}

type Observer interface {
	Event(name string, data map[string]interface{})
	PlannerInput(input string)
	PlannerOutput(output string)
}

type Pack struct {
	SchemaVersion    string            `json:"schema_version"`
	Question         string            `json:"question"`
	Mode             string            `json:"mode"`
	QueryPlan        QueryPlan         `json:"query_plan"`
	Coverage         Coverage          `json:"coverage"`
	Topic            string            `json:"topic,omitempty"`
	UsedTopicBrief   bool              `json:"used_topic_brief"`
	Evidence         []ask.Evidence    `json:"evidence"`
	ExactTagEvidence []ask.Evidence    `json:"exact_tag_evidence,omitempty"`
	TopicBrief       *TopicBrief       `json:"topic_brief,omitempty"`
	NextSteps        []SuggestedAction `json:"next_steps,omitempty"`
}

type QueryPlan struct {
	TextQuery         string                    `json:"text_query"`
	QueryFamily       string                    `json:"query_family,omitempty"`
	QueryTerms        []string                  `json:"query_terms"`
	TagQueries        []string                  `json:"tag_queries"`
	QueryVariants     []QueryVariant            `json:"query_variants,omitempty"`
	Concepts          []QueryConcept            `json:"concepts,omitempty"`
	Planner           string                    `json:"planner,omitempty"`
	PlannerModel      string                    `json:"planner_model,omitempty"`
	PlannerError      string                    `json:"planner_error,omitempty"`
	SourceTypes       []string                  `json:"source_types,omitempty"`
	RetrievalLanes    []retrieval.RetrievalLane `json:"retrieval_lanes,omitempty"`
	Limit             int                       `json:"limit"`
	MaxCharsPerDoc    int                       `json:"max_chars_per_doc"`
	IncludeRelated    bool                      `json:"include_related"`
	RelatedLimit      int                       `json:"related_limit,omitempty"`
	Topic             string                    `json:"topic,omitempty"`
	TopicSource       string                    `json:"topic_source,omitempty"`
	IncludeTopicBrief bool                      `json:"include_topic_brief"`
}

type QueryVariant struct {
	Query  string `json:"query"`
	Reason string `json:"reason,omitempty"`
}

type QueryConcept struct {
	Key       string   `json:"key"`
	Preferred string   `json:"preferred,omitempty"`
	Terms     []string `json:"terms"`
	Required  bool     `json:"required"`
}

type Coverage struct {
	EvidenceCount     int      `json:"evidence_count"`
	ByKind            []Bucket `json:"by_kind"`
	BySourceType      []Bucket `json:"by_source_type"`
	TopUserTags       []Bucket `json:"top_user_tags,omitempty"`
	ExactTagMatches   []Bucket `json:"exact_tag_matches,omitempty"`
	ItemTextMatches   int      `json:"item_text_matches,omitempty"`
	SourceTextMatches int      `json:"source_text_matches,omitempty"`
	TopicNodeCount    int      `json:"topic_node_count,omitempty"`
	TopicEdgeCount    int      `json:"topic_edge_count,omitempty"`
	DisplayedLimit    int      `json:"displayed_limit"`
	RelatedLimit      int      `json:"related_limit,omitempty"`
	RecallNote        string   `json:"recall_note,omitempty"`
}

type Bucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type TopicBrief struct {
	Topic        string                `json:"topic"`
	SourceTypes  []string              `json:"source_types,omitempty"`
	SeedLimit    int                   `json:"seed_limit"`
	RelatedLimit int                   `json:"related_limit"`
	Summary      string                `json:"summary"`
	Pivots       topics.TopicPivots    `json:"pivots,omitempty"`
	Entities     []topics.TopicEntity  `json:"entities,omitempty"`
	Nodes        []topics.TopicMapNode `json:"nodes"`
	Edges        []topics.TopicMapEdge `json:"edges"`
}

type SuggestedAction struct {
	Action string                 `json:"action"`
	Label  string                 `json:"label"`
	Reason string                 `json:"reason,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
}

type researchStrategy struct {
	Variants     []QueryVariant
	Concepts     []QueryConcept
	Family       string
	Planner      string
	PlannerModel string
	PlannerError string
}
