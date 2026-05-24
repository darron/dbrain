package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/summaryconfig"
)

const (
	SynthesisSchemaVersion           = "research_synthesis.v1"
	SynthesisPromptVersion           = "brain-research-synthesis-v3"
	DefaultMaxEvidenceChars          = 24000
	defaultExactTagReservedChars     = 2000
	defaultTopicBriefMinRemaining    = 2000
	defaultTopicBriefSummaryMaxChars = 2000
)

var ErrSynthesisUnavailable = errors.New("synthesis model is not configured")

type SynthesisOptions struct {
	Question         string
	Pack             Pack
	Model            string
	CLI              string
	Length           string
	Timeout          time.Duration
	MaxEvidenceChars int
	Binary           string
}

type PreparedSynthesis struct {
	SchemaVersion string             `json:"schema_version"`
	Question      string             `json:"question"`
	Model         string             `json:"model"`
	PromptVersion string             `json:"prompt_version"`
	Input         string             `json:"-"`
	Truncation    TruncationMetadata `json:"truncation"`
	Citations     []Citation         `json:"citations,omitempty"`
	Warnings      []string           `json:"answer_warnings,omitempty"`
	Status        string             `json:"answer_status"`
}

type SynthesisResult struct {
	SchemaVersion string             `json:"schema_version"`
	Question      string             `json:"question"`
	Answer        string             `json:"answer"`
	AnswerStatus  string             `json:"answer_status"`
	Warnings      []string           `json:"answer_warnings,omitempty"`
	Truncation    TruncationMetadata `json:"truncation"`
	Citations     []Citation         `json:"citations,omitempty"`
	PromptVersion string             `json:"prompt_version"`
	Model         string             `json:"model"`
	Tool          string             `json:"tool"`
	ToolVersion   string             `json:"tool_version"`
}

type TruncationMetadata struct {
	EvidenceBudgetChars       int      `json:"evidence_budget_chars"`
	EvidenceCharsUsed         int      `json:"evidence_chars_used"`
	DroppedSourceKeys         []string `json:"dropped_source_keys,omitempty"`
	PartiallyTrimmedSourceKey string   `json:"partially_trimmed_source_key,omitempty"`
}

type Citation struct {
	SourceKey string `json:"source_key"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	NotePath  string `json:"note_path,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

func PrepareSynthesis(cfg config.Config, opts SynthesisOptions) (PreparedSynthesis, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		question = strings.TrimSpace(opts.Pack.Question)
	}
	if question == "" {
		return PreparedSynthesis{}, fmt.Errorf("question is required")
	}
	if strings.TrimSpace(opts.Pack.SchemaVersion) != SchemaVersion {
		return PreparedSynthesis{}, fmt.Errorf("research_pack.schema_version must be %q", SchemaVersion)
	}

	modelName := summaryconfig.Model(cfg.RootDir, opts.Model)
	hasEvidence := len(opts.Pack.Evidence) > 0 || len(opts.Pack.ExactTagEvidence) > 0
	if hasEvidence && strings.TrimSpace(modelName) == "" {
		return PreparedSynthesis{}, fmt.Errorf("%w: set DBRAIN_SUMMARY_MODEL or pass --model", ErrSynthesisUnavailable)
	}
	budget := opts.MaxEvidenceChars
	if budget <= 0 {
		budget = DefaultMaxEvidenceChars
	}

	builder := synthesisInputBuilder{
		pack:      opts.Pack,
		question:  question,
		budget:    budget,
		citations: make([]Citation, 0, len(opts.Pack.Evidence)+len(opts.Pack.ExactTagEvidence)),
		seen:      map[string]struct{}{},
	}
	input := builder.build()
	status := "ok"
	warnings := builder.warnings()
	if len(opts.Pack.Evidence) == 0 && len(opts.Pack.ExactTagEvidence) == 0 {
		status = "no_evidence"
		warnings = appendUnique(warnings, "no_evidence")
	}

	return PreparedSynthesis{
		SchemaVersion: SynthesisSchemaVersion,
		Question:      question,
		Model:         modelName,
		PromptVersion: SynthesisPromptVersion,
		Input:         input,
		Truncation:    builder.truncation,
		Citations:     builder.citations,
		Warnings:      warnings,
		Status:        status,
	}, nil
}

func Synthesize(ctx context.Context, cfg config.Config, opts SynthesisOptions) (SynthesisResult, error) {
	prepared, err := PrepareSynthesis(cfg, opts)
	if err != nil {
		return SynthesisResult{}, err
	}
	return RunPreparedSynthesis(ctx, cfg, prepared, opts)
}
