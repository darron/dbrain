package brainresearch

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
)

const (
	SchemaVersion       = "research_pack.v1"
	maxExactTagEvidence = 3
	maxQueryVariants    = 8
)

type Builder struct {
	cfg                          config.Config
	st                           *store.Store
	semanticRetriever            SemanticRetriever
	semanticOptions              researchsemantic.Options
	strategyRunner               func(context.Context, string, ask.Options) (ask.Response, error)
	strategyPoolRunner           func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error)
	semanticMode                 semanticconfig.Mode
	semanticReadiness            semanticreadiness.Decision
	semanticReadinessDiagnostics *SemanticReadinessDiagnostics
	semanticStatus               *semanticindex.Status
	shadowComparison             *ShadowComparison
}

// SemanticReadinessDiagnostics exposes bounded, content-free catch-up debt.
// Parent keys and source content are deliberately excluded from this value.
type SemanticReadinessDiagnostics struct {
	OmittedParentCount      int        `json:"omitted_parent_count"`
	EstimatedNotReadyChunks int        `json:"estimated_not_ready_chunks"`
	OldestDebtAt            *time.Time `json:"oldest_debt_at,omitempty"`
}

// WithSemanticReadinessDiagnostics attaches aggregate debt only while semantic
// retrieval is admitted in catching-up mode.
func (b *Builder) WithSemanticReadinessDiagnostics(diagnostics SemanticReadinessDiagnostics) *Builder {
	if b == nil {
		return b
	}
	b.semanticReadinessDiagnostics = nil
	if b.semanticReadiness.State != semanticreadiness.StateCatchingUp {
		return b
	}
	copy := diagnostics
	if diagnostics.OldestDebtAt != nil {
		oldest := *diagnostics.OldestDebtAt
		copy.OldestDebtAt = &oldest
	}
	b.semanticReadinessDiagnostics = &copy
	return b
}

func (b *Builder) WithSemanticMode(mode semanticconfig.Mode) *Builder {
	if b != nil {
		b.semanticMode = mode
	}
	return b
}

type SemanticRetriever interface {
	Retrieve(context.Context, string, researchsemantic.Options) ([]retrieval.EvidenceDocument, semanticindex.Status, error)
}

// Close releases optional request-scoped semantic retrieval resources. Ordinary
// exact retrieval has no close operation and remains unchanged.
func (b *Builder) Close() error {
	if b == nil {
		return nil
	}
	if closer, ok := b.semanticRetriever.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// WithSemanticRetriever returns the builder with an explicitly injected local
// semantic lane. New and top-level Build remain lexical unless callers opt in.
func (b *Builder) WithSemanticRetriever(retriever SemanticRetriever, opts researchsemantic.Options) *Builder {
	if b == nil {
		return b
	}
	opts.Filters.AllowedParentKeys = append([]string(nil), opts.Filters.AllowedParentKeys...)
	opts.Filters.AllowedParentKinds = append([]string(nil), opts.Filters.AllowedParentKinds...)
	opts.Filters.AllowedEvidenceRoles = append([]string(nil), opts.Filters.AllowedEvidenceRoles...)
	opts.Filters.AllowedSourceTypes = append([]string(nil), opts.Filters.AllowedSourceTypes...)
	b.semanticRetriever, b.semanticOptions = retriever, opts
	if retriever != nil && b.semanticReadiness.State == "" {
		b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateReady, Reason: "explicit semantic retriever", Searchable: true}
	}
	return b
}

type Options struct {
	Question              string
	RawQuestion           string
	Topic                 string
	Limit                 int
	SourceTypes           []string
	IncludeRelated        bool
	RelatedLimit          int
	SeedLimit             int
	IncludeTopic          *bool
	MaxCharsPerDoc        int
	PlannerModel          string
	PlannerTimeout        time.Duration
	PlannerBinary         string
	UseModelPlanner       bool
	DisablePlanner        bool
	UseSemantic           bool
	DisableSemantic       bool
	EffectiveSemanticMode semanticconfig.Mode
	ContinuityAnchors     []ProtectedAnchor
	Attempt               string
	AnchorResolver        AnchorResolver
	Observer              Observer
}

type AnchorResolver interface {
	ResolveAnchors(ctx context.Context, anchors []ProtectedAnchor) ([]ProtectedAnchor, error)
}

type Observer interface {
	Event(name string, data map[string]interface{})
	PlannerInput(input string)
	PlannerOutput(output string)
}

type AttemptPlannerInputObserver interface {
	PlannerInputAttempt(attempt string, input string)
}

type AttemptPlannerOutputObserver interface {
	PlannerOutputAttempt(attempt string, output string)
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
	Inspection       *InspectionResult `json:"inspection,omitempty"`
	TopicBrief       *TopicBrief       `json:"topic_brief,omitempty"`
	NextSteps        []SuggestedAction `json:"next_steps,omitempty"`
}

type QueryPlan struct {
	TextQuery                    string                        `json:"text_query"`
	QueryFamily                  string                        `json:"query_family,omitempty"`
	QueryTerms                   []string                      `json:"query_terms"`
	TagQueries                   []string                      `json:"tag_queries"`
	QueryVariants                []QueryVariant                `json:"query_variants,omitempty"`
	Concepts                     []QueryConcept                `json:"concepts,omitempty"`
	ProtectedAnchors             []ProtectedAnchor             `json:"protected_anchors,omitempty"`
	Planner                      string                        `json:"planner,omitempty"`
	PlannerModel                 string                        `json:"planner_model,omitempty"`
	PlannerError                 string                        `json:"planner_error,omitempty"`
	SourceTypes                  []string                      `json:"source_types,omitempty"`
	RetrievalLanes               []retrieval.RetrievalLane     `json:"retrieval_lanes,omitempty"`
	Limit                        int                           `json:"limit"`
	MaxCharsPerDoc               int                           `json:"max_chars_per_doc"`
	IncludeRelated               bool                          `json:"include_related"`
	RelatedLimit                 int                           `json:"related_limit,omitempty"`
	Topic                        string                        `json:"topic,omitempty"`
	TopicSource                  string                        `json:"topic_source,omitempty"`
	IncludeTopicBrief            bool                          `json:"include_topic_brief"`
	SemanticMode                 semanticconfig.Mode           `json:"semantic_mode"`
	SemanticReadiness            semanticreadiness.State       `json:"semantic_readiness,omitempty"`
	SemanticReadinessDiagnostics *SemanticReadinessDiagnostics `json:"semantic_readiness_diagnostics,omitempty"`
	ShadowComparison             *ShadowComparison             `json:"shadow_comparison,omitempty"`
	RetryShadowComparison        *ShadowComparison             `json:"retry_shadow_comparison,omitempty"`
}

type ShadowRankedReference struct {
	SourceKey string `json:"source_key"`
	ChunkID   string `json:"chunk_id,omitempty"`
	Rank      int    `json:"rank"`
}

type ShadowComparison struct {
	Status       semanticindex.StatusState  `json:"status"`
	Reason       semanticindex.StatusReason `json:"reason,omitempty"`
	LexicalCount int                        `json:"lexical_count"`
	HybridCount  int                        `json:"hybrid_count"`
	Lexical      []ShadowRankedReference    `json:"lexical"`
	Hybrid       []ShadowRankedReference    `json:"hybrid"`
	Added        []ShadowRankedReference    `json:"added"`
	Removed      []ShadowRankedReference    `json:"removed"`
	Reordered    []ShadowRankedReference    `json:"reordered"`
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
	Role      string   `json:"role,omitempty"`
}

type ProtectedAnchor struct {
	Kind           string   `json:"kind"`
	Relation       string   `json:"relation,omitempty"`
	Raw            string   `json:"raw"`
	Canonical      string   `json:"canonical,omitempty"`
	ResolvedID     string   `json:"resolved_id,omitempty"`
	Source         string   `json:"source,omitempty"`
	Confidence     string   `json:"confidence,omitempty"`
	ExactTerms     []string `json:"exact_terms,omitempty"`
	PhraseTerms    []string `json:"phrase_terms,omitempty"`
	ExpansionTerms []string `json:"expansion_terms,omitempty"`
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
