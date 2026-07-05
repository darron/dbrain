package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
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
	SchemaVersion string                       `json:"schema_version"`
	Question      string                       `json:"question"`
	Model         string                       `json:"model"`
	PromptVersion string                       `json:"prompt_version"`
	Input         string                       `json:"-"`
	Truncation    TruncationMetadata           `json:"truncation"`
	Citations     []Citation                   `json:"citations,omitempty"`
	AnchorContext AnchorSynthesisContextStatus `json:"anchor_context,omitempty"`
	Warnings      []string                     `json:"answer_warnings,omitempty"`
	Status        string                       `json:"answer_status"`
}

type SynthesisResult struct {
	SchemaVersion string                       `json:"schema_version"`
	Question      string                       `json:"question"`
	Answer        string                       `json:"answer"`
	AnswerStatus  string                       `json:"answer_status"`
	Warnings      []string                     `json:"answer_warnings,omitempty"`
	Truncation    TruncationMetadata           `json:"truncation"`
	Citations     []Citation                   `json:"citations,omitempty"`
	AnchorContext AnchorSynthesisContextStatus `json:"anchor_context,omitempty"`
	PromptVersion string                       `json:"prompt_version"`
	Model         string                       `json:"model"`
	Tool          string                       `json:"tool"`
	ToolVersion   string                       `json:"tool_version"`
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

type AnchorSynthesisContextStatus struct {
	Anchors []AnchorSynthesisAnchorStatus `json:"anchors,omitempty"`
}

type AnchorSynthesisAnchorStatus struct {
	Anchor                     string   `json:"anchor"`
	SupportedSourceKeys        []string `json:"supported_source_keys,omitempty"`
	CitationSourceKeys         []string `json:"citation_source_keys,omitempty"`
	DroppedSourceKeys          []string `json:"dropped_source_keys,omitempty"`
	PartiallyTrimmedSourceKeys []string `json:"partially_trimmed_source_keys,omitempty"`
	Reasons                    []string `json:"reasons,omitempty"`
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
	anchorContext := anchoredSynthesisContextStatus(opts.Pack, PreparedSynthesis{
		Truncation: builder.truncation,
		Citations:  builder.citations,
	})
	if anchorContextNeedsWarning(anchorContext) {
		warnings = appendUnique(warnings, "anchor_evidence_truncated")
	}
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
		AnchorContext: anchorContext,
		Warnings:      warnings,
		Status:        status,
	}, nil
}

func anchoredSynthesisContextStatus(pack Pack, prepared PreparedSynthesis) AnchorSynthesisContextStatus {
	if len(pack.QueryPlan.ProtectedAnchors) == 0 {
		return AnchorSynthesisContextStatus{}
	}
	rows := append([]ask.Evidence{}, pack.Evidence...)
	rows = append(rows, pack.ExactTagEvidence...)
	citationSet := citationSourceKeySet(prepared.Citations)
	droppedSet := stringSliceSet(prepared.Truncation.DroppedSourceKeys)
	partialKey := strings.TrimSpace(prepared.Truncation.PartiallyTrimmedSourceKey)
	status := AnchorSynthesisContextStatus{Anchors: make([]AnchorSynthesisAnchorStatus, 0, len(pack.QueryPlan.ProtectedAnchors))}
	for _, anchor := range pack.QueryPlan.ProtectedAnchors {
		anchorStatus := AnchorSynthesisAnchorStatus{Anchor: anchorStatusLabel(anchor)}
		for _, row := range rows {
			if !EvidenceMatchesProtectedAnchor(row, anchor) {
				continue
			}
			key := strings.TrimSpace(row.SourceKey)
			if key == "" {
				continue
			}
			anchorStatus.SupportedSourceKeys = appendUnique(anchorStatus.SupportedSourceKeys, key)
			if _, ok := citationSet[key]; ok {
				anchorStatus.CitationSourceKeys = appendUnique(anchorStatus.CitationSourceKeys, key)
			}
			if _, ok := droppedSet[key]; ok {
				anchorStatus.DroppedSourceKeys = appendUnique(anchorStatus.DroppedSourceKeys, key)
			}
			if partialKey == key {
				anchorStatus.PartiallyTrimmedSourceKeys = appendUnique(anchorStatus.PartiallyTrimmedSourceKeys, key)
			}
		}
		if len(anchorStatus.SupportedSourceKeys) == 0 {
			anchorStatus.Reasons = appendUnique(anchorStatus.Reasons, "not_supported")
		} else if len(anchorStatus.CitationSourceKeys) == 0 {
			if len(anchorStatus.DroppedSourceKeys) > 0 || len(anchorStatus.PartiallyTrimmedSourceKeys) > 0 {
				anchorStatus.Reasons = appendUnique(anchorStatus.Reasons, "token_budget")
			} else {
				anchorStatus.Reasons = appendUnique(anchorStatus.Reasons, "citation_limit")
			}
		}
		status.Anchors = append(status.Anchors, anchorStatus)
	}
	return status
}

func anchorContextNeedsWarning(status AnchorSynthesisContextStatus) bool {
	for _, anchor := range status.Anchors {
		if len(anchor.SupportedSourceKeys) > 0 && len(anchor.CitationSourceKeys) == 0 {
			return true
		}
	}
	return false
}

func anchorStatusLabel(anchor ProtectedAnchor) string {
	for _, value := range []string{anchor.ResolvedID, anchor.Canonical, anchor.Raw} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "protected_anchor"
}

func citationSourceKeySet(citations []Citation) map[string]struct{} {
	out := make(map[string]struct{}, len(citations))
	for _, citation := range citations {
		key := strings.TrimSpace(citation.SourceKey)
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func stringSliceSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}

func Synthesize(ctx context.Context, cfg config.Config, opts SynthesisOptions) (SynthesisResult, error) {
	prepared, err := PrepareSynthesis(cfg, opts)
	if err != nil {
		return SynthesisResult{}, err
	}
	return RunPreparedSynthesis(ctx, cfg, prepared, opts)
}
