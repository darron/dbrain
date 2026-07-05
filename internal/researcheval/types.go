package researcheval

import "github.com/darron/dbrain/internal/brainresearch"

const ProposalSchemaVersion = "research_eval_proposal.v1"

type Case struct {
	Name                       string                          `json:"name"`
	Question                   string                          `json:"question"`
	RawQuestion                string                          `json:"raw_question,omitempty"`
	Limit                      int                             `json:"limit,omitempty"`
	MaxCharsPerDoc             int                             `json:"max_chars_per_doc,omitempty"`
	SourceTypes                []string                        `json:"source_types,omitempty"`
	IncludeRelated             bool                            `json:"include_related,omitempty"`
	RelatedLimit               int                             `json:"related_limit,omitempty"`
	SeedLimit                  int                             `json:"seed_limit,omitempty"`
	IncludeTopicBrief          *bool                           `json:"include_topic_brief,omitempty"`
	DisablePlanner             bool                            `json:"disable_planner,omitempty"`
	UseSemantic                bool                            `json:"use_semantic,omitempty"`
	DisableSemantic            bool                            `json:"disable_semantic,omitempty"`
	PlannerModel               string                          `json:"planner_model,omitempty"`
	PlannerTimeoutMS           int64                           `json:"planner_timeout_ms,omitempty"`
	PlannerBinary              string                          `json:"planner_binary,omitempty"`
	SynthesisModel             string                          `json:"synthesis_model,omitempty"`
	ContinuityAnchors          []brainresearch.ProtectedAnchor `json:"continuity_anchors,omitempty"`
	RunWithRunner              bool                            `json:"run_with_runner,omitempty"`
	StopAfterJudge             bool                            `json:"stop_after_judge,omitempty"`
	MinEvidence                int                             `json:"min_evidence,omitempty"`
	ExpectSourceKeys           []string                        `json:"expect_source_keys,omitempty"`
	ExpectAnySourceKeys        []string                        `json:"expect_any_source_keys,omitempty"`
	ExpectTopSourceKeys        []string                        `json:"expect_top_source_keys,omitempty"`
	ForbidSourceKeys           []string                        `json:"forbid_source_keys,omitempty"`
	ExpectPlanner              string                          `json:"expect_planner,omitempty"`
	ExpectSemanticStatus       string                          `json:"expect_semantic_status,omitempty"`
	ExpectQueryFamily          string                          `json:"expect_query_family,omitempty"`
	ExpectQueryTerms           []string                        `json:"expect_query_terms,omitempty"`
	ExpectQueryVariants        []string                        `json:"expect_query_variants,omitempty"`
	ForbidQueryVariants        []string                        `json:"forbid_query_variants,omitempty"`
	ExpectConcepts             []string                        `json:"expect_concepts,omitempty"`
	ForbidConcepts             []string                        `json:"forbid_concepts,omitempty"`
	ExpectPlannerErrorContains string                          `json:"expect_planner_error_contains,omitempty"`
	MinRetrievalSignals        int                             `json:"min_retrieval_signals,omitempty"`
	ExpectJudgeVerdict         string                          `json:"expect_judge_verdict,omitempty"`
	ForbidRetryAction          string                          `json:"forbid_retry_action,omitempty"`
	ExpectAnswerStatus         string                          `json:"expect_answer_status,omitempty"`
	ExpectCitationSourceKeys   []string                        `json:"expect_citation_source_keys,omitempty"`
	ForbidCitationSourceKeys   []string                        `json:"forbid_citation_source_keys,omitempty"`
	ExpectAnswerText           []string                        `json:"expect_answer_text,omitempty"`
	MaxLatencyMS               int64                           `json:"max_latency_ms,omitempty"`
}

type Options struct {
	Cases []Case
}

type Report struct {
	StartedAt  string       `json:"started_at"`
	DurationMS int64        `json:"duration_ms"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
	Cases      []CaseResult `json:"cases"`
}

type CaseResult struct {
	Name                 string            `json:"name"`
	Question             string            `json:"question"`
	Passed               bool              `json:"passed"`
	DurationMS           int64             `json:"duration_ms"`
	EvidenceCount        int               `json:"evidence_count"`
	RetrievalSignalCount int               `json:"retrieval_signal_count"`
	SourceKeys           []string          `json:"source_keys"`
	Planner              string            `json:"planner,omitempty"`
	PlannerModel         string            `json:"planner_model,omitempty"`
	PlannerError         string            `json:"planner_error,omitempty"`
	RetrievalLanes       map[string]string `json:"retrieval_lanes,omitempty"`
	QueryFamily          string            `json:"query_family,omitempty"`
	QueryTerms           []string          `json:"query_terms,omitempty"`
	QueryVariants        []string          `json:"query_variants,omitempty"`
	Concepts             []string          `json:"concepts,omitempty"`
	TopEvidence          []EvidenceSummary `json:"top_evidence,omitempty"`
	StopReason           string            `json:"stop_reason,omitempty"`
	JudgeVerdict         string            `json:"judge_verdict,omitempty"`
	RetryAction          string            `json:"retry_action,omitempty"`
	AnswerStatus         string            `json:"answer_status,omitempty"`
	CitationSourceKeys   []string          `json:"citation_source_keys,omitempty"`
	Failures             []string          `json:"failures,omitempty"`
}

type EvidenceSummary struct {
	SourceKey    string   `json:"source_key"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Score        int      `json:"score,omitempty"`
	Signals      []string `json:"signals,omitempty"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
	MissingTerms []string `json:"missing_terms,omitempty"`
}

type Proposal struct {
	SchemaVersion string   `json:"schema_version"`
	Source        string   `json:"source"`
	SourceType    string   `json:"source_type"`
	GeneratedAt   string   `json:"generated_at"`
	Cases         []Case   `json:"cases"`
	Notes         []string `json:"notes,omitempty"`
}

type ProposalOptions struct {
	IncludeAnswerText bool
}

type TraceDiff struct {
	TracePath       string            `json:"trace_path"`
	Question        string            `json:"question"`
	OldSourceKeys   []string          `json:"old_source_keys"`
	NewSourceKeys   []string          `json:"new_source_keys"`
	Added           []string          `json:"added"`
	Removed         []string          `json:"removed"`
	Reordered       []ReorderedSource `json:"reordered,omitempty"`
	TopEvidence     []EvidenceSummary `json:"top_evidence,omitempty"`
	ProposalCommand string            `json:"proposal_command"`
}

type ReorderedSource struct {
	SourceKey string `json:"source_key"`
	OldIndex  int    `json:"old_index"`
	NewIndex  int    `json:"new_index"`
}

func queryVariantStrings(variants []brainresearch.QueryVariant) []string {
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		if variant.Query != "" {
			out = append(out, variant.Query)
		}
	}
	return out
}
