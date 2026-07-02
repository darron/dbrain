package modelbakeoff

import (
	"time"

	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/model"
)

const SchemaVersion = "model_bakeoff.v2"

type Options struct {
	Mode          string
	Lookups       []string
	Models        []string
	Timeout       time.Duration
	Length        string
	Language      string
	IncludeImages bool
	// ParityPreset enables an explicit sampler/prompt-parity comparison
	// preset. Empty or "none" disables it; "dbrain-modelfile" applies the
	// repo Modelfile sampler values where the provider path supports them.
	ParityPreset string
}

type Result struct {
	SchemaVersion string       `json:"schema_version"`
	Mode          string       `json:"mode"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	DurationMS    int64        `json:"duration_ms"`
	Models        []string     `json:"models"`
	Lookups       []string     `json:"lookups"`
	Targets       []TargetRun  `json:"targets"`
	Summary       []ModelStats `json:"summary"`
	Errors        int          `json:"errors"`
}

type TargetRun struct {
	Lookup       string     `json:"lookup"`
	SourceKey    string     `json:"source_key,omitempty"`
	Title        string     `json:"title,omitempty"`
	SourceType   string     `json:"source_type,omitempty"`
	CanonicalURL string     `json:"canonical_url,omitempty"`
	Runs         []ModelRun `json:"runs"`
}

// RuntimeContext captures optional read-only runtime metadata for a run, such
// as the live model id and context length reported by the provider. The Status
// field describes whether collection was attempted, succeeded, or skipped.
type RuntimeContext struct {
	Status        string `json:"status,omitempty"`
	Provider      string `json:"provider,omitempty"`
	APIModel      string `json:"api_model,omitempty"`
	ContextLength int    `json:"context_length,omitempty"`
	Notes         string `json:"notes,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ModelRun struct {
	Model               string                 `json:"model"`
	Provider            string                 `json:"provider,omitempty"`
	APIModel            string                 `json:"api_model,omitempty"`
	Transport           string                 `json:"transport,omitempty"`
	Local               *bool                  `json:"local,omitempty"`
	Status              string                 `json:"status"`
	Error               string                 `json:"error,omitempty"`
	DurationMS          int64                  `json:"duration_ms"`
	RequestedParams     map[string]any         `json:"requested_params,omitempty"`
	SentParams          map[string]any         `json:"sent_params,omitempty"`
	OmittedParams       map[string]string      `json:"omitted_params,omitempty"`
	ParamStrictness     string                 `json:"param_strictness,omitempty"`
	PromptParityStatus  string                 `json:"prompt_parity_status,omitempty"`
	ReasoningModeStatus string                 `json:"reasoning_mode_status,omitempty"`
	RuntimeContext      RuntimeContext         `json:"runtime_context"`
	Summary             *model.SummaryResult   `json:"summary,omitempty"`
	Categorize          *itemcategorize.Result `json:"categorize,omitempty"`
	OutputChars         int                    `json:"output_chars"`
	WordOverlap         float64                `json:"baseline_word_overlap,omitempty"`
	ExistingTags        string                 `json:"existing_tags,omitempty"`
}

type ModelStats struct {
	Model                      string  `json:"model"`
	OK                         int     `json:"ok"`
	Errors                     int     `json:"errors"`
	AverageDurationMS          int64   `json:"average_duration_ms"`
	AverageOutputChars         int     `json:"average_output_chars"`
	AverageBaselineWordOverlap float64 `json:"average_baseline_word_overlap,omitempty"`
}
