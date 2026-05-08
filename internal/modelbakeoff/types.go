package modelbakeoff

import (
	"time"

	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/model"
)

const SchemaVersion = "model_bakeoff.v1"

type Options struct {
	Mode          string
	Lookups       []string
	Models        []string
	Timeout       time.Duration
	Length        string
	Language      string
	IncludeImages bool
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

type ModelRun struct {
	Model        string                 `json:"model"`
	Status       string                 `json:"status"`
	Error        string                 `json:"error,omitempty"`
	DurationMS   int64                  `json:"duration_ms"`
	Summary      *model.SummaryResult   `json:"summary,omitempty"`
	Categorize   *itemcategorize.Result `json:"categorize,omitempty"`
	OutputChars  int                    `json:"output_chars"`
	WordOverlap  float64                `json:"baseline_word_overlap,omitempty"`
	ExistingTags string                 `json:"existing_tags,omitempty"`
}

type ModelStats struct {
	Model                      string  `json:"model"`
	OK                         int     `json:"ok"`
	Errors                     int     `json:"errors"`
	AverageDurationMS          int64   `json:"average_duration_ms"`
	AverageOutputChars         int     `json:"average_output_chars"`
	AverageBaselineWordOverlap float64 `json:"average_baseline_word_overlap,omitempty"`
}
