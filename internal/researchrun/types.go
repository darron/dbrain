package researchrun

import (
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/researchtrace"
)

const (
	StopEnoughEvidence                 = "enough_evidence"
	StopNoEvidence                     = "no_evidence"
	StopWeakEvidenceBudgetExhausted    = "weak_evidence_budget_exhausted"
	StopPlannerFailedDeterministicUsed = "planner_failed_deterministic_used"
	StopMaxStepsReached                = "max_steps_reached"
	StopTimeoutExceeded                = "timeout_exceeded"
	StopSynthesisUnavailable           = "synthesis_unavailable"
	StopSynthesisFailed                = "synthesis_failed"
	StopVerificationFailed             = "verification_failed"
	StopTraceFailed                    = "trace_failed"
)

type JudgeVerdict string
type RetryAction string

const (
	JudgeEnoughEvidence JudgeVerdict = "enough_evidence"
	JudgeNoEvidence     JudgeVerdict = "no_evidence"
	JudgeWeakEvidence   JudgeVerdict = "weak_evidence"

	RetryNone             RetryAction = "none"
	RetryFocusedVariant   RetryAction = "focused_retry"
	RetryRelatedExpansion RetryAction = "related_expansion"
)

type JudgeResult struct {
	Verdict         JudgeVerdict      `json:"verdict"`
	Reason          string            `json:"reason,omitempty"`
	MissingConcepts []string          `json:"missing_concepts,omitempty"`
	WeakRows        []WeakEvidenceRow `json:"weak_rows,omitempty"`
	RetryAction     RetryAction       `json:"retry_action"`
	RetryVariant    string            `json:"retry_variant,omitempty"`
	ExpansionLookup string            `json:"expansion_lookup,omitempty"`
}

type WeakEvidenceRow struct {
	SourceKey       string   `json:"source_key"`
	Reason          string   `json:"reason"`
	Relationship    string   `json:"relationship,omitempty"`
	MissingConcepts []string `json:"missing_concepts,omitempty"`
}

type VerificationResult struct {
	Passed   bool     `json:"passed"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type AnswerReviewVerdict string

const (
	AnswerReviewPass        AnswerReviewVerdict = "pass"
	AnswerReviewWarn        AnswerReviewVerdict = "warn"
	AnswerReviewFail        AnswerReviewVerdict = "fail"
	AnswerReviewUnavailable AnswerReviewVerdict = "unavailable"
	AnswerReviewError       AnswerReviewVerdict = "error"
)

type AnswerReviewResult struct {
	Enabled  bool                `json:"enabled"`
	Verdict  AnswerReviewVerdict `json:"verdict,omitempty"`
	Model    string              `json:"model,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
	Errors   []string            `json:"errors,omitempty"`
	Raw      string              `json:"raw,omitempty"`
}

type ProgressEvent struct {
	Stage      string                 `json:"stage"`
	Status     string                 `json:"status"`
	Message    string                 `json:"message,omitempty"`
	StopReason string                 `json:"stop_reason,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`
}

type Options struct {
	Question             string
	SynthesisQuestion    string
	Topic                string
	Limit                int
	SourceTypes          []string
	RelatedLimit         int
	SeedLimit            int
	IncludeTopic         *bool
	MaxCharsPerDoc       int
	PlannerModel         string
	PlannerTimeout       time.Duration
	PlannerBinary        string
	UseModelPlanner      bool
	DisablePlanner       bool
	UseSemantic          bool
	DisableSemantic      bool
	Model                string
	CLI                  string
	Binary               string
	MaxEvidenceChars     int
	SynthesisTimeout     time.Duration
	EnableAnswerReview   bool
	AnswerReviewModel    string
	AnswerReviewBinary   string
	AnswerReviewTimeout  time.Duration
	RunnerTimeout        time.Duration
	StageTimeout         time.Duration
	MaxSteps             int
	MinEvidenceForEnough int
	TraceEnabled         *bool
	Surface              string
	ChatContinuity       *researchtrace.ChatContinuity
	KeepAllTraces        bool
	Progress             func(ProgressEvent)
}

type Result struct {
	Pack              brainresearch.Pack               `json:"research_pack"`
	PreparedSynthesis *brainresearch.PreparedSynthesis `json:"prepared_synthesis,omitempty"`
	Synthesis         *brainresearch.SynthesisResult   `json:"synthesis,omitempty"`
	Judge             JudgeResult                      `json:"judge"`
	Verification      VerificationResult               `json:"verification"`
	AnswerReview      AnswerReviewResult               `json:"answer_review,omitempty"`
	TracePath         string                           `json:"trace_path,omitempty"`
	StopReason        string                           `json:"stop_reason"`
	Warnings          []string                         `json:"warnings,omitempty"`
}
