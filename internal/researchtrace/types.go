package researchtrace

import (
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
)

const (
	SchemaVersion   = "research_trace.v1"
	CompleteMarker  = ".complete"
	DefaultKeepRuns = 500
	DefaultMaxAge   = 180 * 24 * time.Hour
)

type ResearchTrace struct {
	SchemaVersion     string                           `json:"schema_version"`
	RunID             string                           `json:"run_id"`
	Surface           string                           `json:"surface"`
	Question          string                           `json:"question"`
	ChatContinuity    *ChatContinuity                  `json:"chat_continuity,omitempty"`
	StartedAt         time.Time                        `json:"started_at"`
	CompletedAt       time.Time                        `json:"completed_at"`
	Events            []ResearchTraceEvent             `json:"events,omitempty"`
	Metrics           TraceMetrics                     `json:"metrics"`
	Pack              *brainresearch.Pack              `json:"pack,omitempty"`
	PreparedSynthesis *brainresearch.PreparedSynthesis `json:"prepared_synthesis,omitempty"`
	Synthesis         *brainresearch.SynthesisResult   `json:"synthesis,omitempty"`
	EvidenceFlow      *brainresearch.EvidenceFlow      `json:"evidence_flow,omitempty"`
	Artifacts         TraceArtifacts                   `json:"artifacts,omitempty"`
	StopReason        string                           `json:"stop_reason"`
	Failure           *TraceFailure                    `json:"failure,omitempty"`
}

type ResearchTraceEvent struct {
	At   time.Time              `json:"at"`
	Name string                 `json:"name"`
	Data map[string]interface{} `json:"data,omitempty"`
}

type ChatContinuity struct {
	OriginalQuestion    string                          `json:"original_question,omitempty"`
	RetrievalQuestion   string                          `json:"retrieval_question,omitempty"`
	PriorQuestionIDs    []string                        `json:"prior_question_ids,omitempty"`
	PinnedEvidenceKeys  []string                        `json:"pinned_evidence_keys,omitempty"`
	MergedPriorEvidence []string                        `json:"merged_prior_evidence,omitempty"`
	ContinuityAnchors   []brainresearch.ProtectedAnchor `json:"continuity_anchors,omitempty"`
}

type TraceArtifacts struct {
	PlannerInputPath    string                         `json:"planner_input_path,omitempty"`
	PlannerOutputPath   string                         `json:"planner_output_path,omitempty"`
	PlannerAttemptPaths map[string]PlannerAttemptPaths `json:"planner_attempt_paths,omitempty"`
	SynthesisInputPath  string                         `json:"synthesis_input_path,omitempty"`
}

type TraceFailure struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type TraceMetrics struct {
	TotalDurationMS            int64            `json:"total_duration_ms,omitempty"`
	StageDurationsMS           map[string]int64 `json:"stage_durations_ms,omitempty"`
	QueryVariantCount          int              `json:"query_variant_count,omitempty"`
	CandidateCountBeforeDedupe int              `json:"candidate_count_before_dedupe,omitempty"`
	EvidenceCountAfterDedupe   int              `json:"evidence_count_after_dedupe,omitempty"`
	ModelCallCount             int              `json:"model_call_count,omitempty"`
	CharactersSentToPlanner    int              `json:"characters_sent_to_planner,omitempty"`
	CharactersSentToSynthesis  int              `json:"characters_sent_to_synthesis,omitempty"`
	TraceArtifactBytes         int64            `json:"trace_artifact_bytes,omitempty"`
	PlannerEnabled             bool             `json:"planner_enabled"`
	Planner                    string           `json:"planner,omitempty"`
	PlannerFallbackStatus      string           `json:"planner_fallback_status,omitempty"`
}

type ArtifactContents struct {
	PlannerInput    string
	PlannerOutput   string
	PlannerAttempts map[string]PlannerAttemptContents
	SynthesisInput  string
}

type PlannerAttemptPaths struct {
	InputPath  string `json:"input_path,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
}

type PlannerAttemptContents struct {
	Input  string
	Output string
}

type RetentionOptions struct {
	KeepLatest int
	MaxAge     time.Duration
	KeepAll    bool
}

type WriteOptions struct {
	Retention RetentionOptions
	Now       func() time.Time
}

type WriteResult struct {
	RunID        string
	RelativePath string
	Directory    string
	Bytes        int64
}

type PruneResult struct {
	Deleted int
	Kept    int
}
