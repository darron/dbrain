package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

const (
	SynthesisSchemaVersion           = "research_synthesis.v1"
	SynthesisPromptVersion           = "brain-research-synthesis-v2"
	DefaultMaxEvidenceChars          = 24000
	defaultExactTagReservedChars     = 2000
	defaultTopicBriefMinRemaining    = 2000
	defaultTopicBriefSummaryMaxChars = 2000
)

const synthesisPrompt = `Answer this question from the provided dbrain research pack only.
Do not use outside knowledge.
The corpus is intentionally selective: it reflects what the collector cared about or found noteworthy, not a neutral or comprehensive sample.
Do not criticize the corpus for not being unbiased or compensate by adding outside balance unless asked.
Accuracy matters more than appearing objective: separate supported facts, source claims, opinions, and uncertainty; flag weak or conflicting evidence.
Cite each material claim with exact source keys from the research pack in brackets, such as [src:...], [x:...], [apple-note:...], or [gh-star:...].
Do not add, remove, shorten, or rewrite source key prefixes.
Include a short Sources section with source keys and note paths.
If evidence is weak, partial, list-like, or missing, say so plainly.
Distinguish user-authored notes from linked third-party sources when the evidence marks that difference.
Distinguish summaries, excerpts, transcripts, OCR, raw notes, and archived web extracts when relevant.
Keep the answer concise and useful.`

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

func RunPreparedSynthesis(ctx context.Context, cfg config.Config, prepared PreparedSynthesis, opts SynthesisOptions) (SynthesisResult, error) {
	if prepared.Status == "no_evidence" {
		return SynthesisResult{
			SchemaVersion: SynthesisSchemaVersion,
			Question:      prepared.Question,
			AnswerStatus:  "no_evidence",
			Warnings:      prepared.Warnings,
			Truncation:    prepared.Truncation,
			Citations:     prepared.Citations,
			PromptVersion: SynthesisPromptVersion,
			Model:         prepared.Model,
		}, nil
	}

	inputFile, err := cfg.CreateTemp("dbrain-research-synthesis-*.md")
	if err != nil {
		return SynthesisResult{}, fmt.Errorf("create synthesis input: %w", err)
	}
	inputPath := inputFile.Name()
	defer func() {
		_ = os.Remove(inputPath)
	}()
	if _, err := inputFile.WriteString(prepared.Input); err != nil {
		_ = inputFile.Close()
		return SynthesisResult{}, fmt.Errorf("write synthesis input: %w", err)
	}
	if err := inputFile.Close(); err != nil {
		return SynthesisResult{}, fmt.Errorf("close synthesis input: %w", err)
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     inputPath,
		Summarize: true,
		Model:     prepared.Model,
		CLI:       opts.CLI,
		Prompt:    synthesisPrompt,
		Length:    defaultString(opts.Length, "medium"),
		Timeout:   defaultDuration(opts.Timeout, 2*time.Minute),
		RootDir:   cfg.RootDir,
	})
	if err != nil {
		return SynthesisResult{}, err
	}
	if runResult.Summary.Status != "ok" || strings.TrimSpace(runResult.Summary.Text) == "" {
		if runResult.Summary.Error != "" {
			return SynthesisResult{}, errors.New(runResult.Summary.Error)
		}
		return SynthesisResult{}, fmt.Errorf("synthesis returned no answer")
	}

	status := "ok"
	if hasString(prepared.Warnings, "evidence_truncated") {
		status = "ok_truncated"
	}
	return SynthesisResult{
		SchemaVersion: SynthesisSchemaVersion,
		Question:      prepared.Question,
		Answer:        strings.TrimSpace(runResult.Summary.Text),
		AnswerStatus:  status,
		Warnings:      prepared.Warnings,
		Truncation:    prepared.Truncation,
		Citations:     prepared.Citations,
		PromptVersion: SynthesisPromptVersion,
		Model:         firstNonEmpty(runResult.Summary.Model, prepared.Model),
		Tool:          runResult.Summary.Tool,
		ToolVersion:   runResult.Summary.ToolVersion,
	}, nil
}

func Synthesize(ctx context.Context, cfg config.Config, opts SynthesisOptions) (SynthesisResult, error) {
	prepared, err := PrepareSynthesis(cfg, opts)
	if err != nil {
		return SynthesisResult{}, err
	}
	return RunPreparedSynthesis(ctx, cfg, prepared, opts)
}
