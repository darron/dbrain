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
	SynthesisPromptVersion           = "brain-research-synthesis-v4"
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
	Relevance     *SynthesisRelevanceSelection `json:"relevance_selection,omitempty"`
	AnchorContext AnchorSynthesisContextStatus `json:"anchor_context,omitempty"`
	Warnings      []string                     `json:"answer_warnings,omitempty"`
	Status        string                       `json:"answer_status"`
}

type SynthesisRelevanceSelection struct {
	Applied            bool     `json:"applied,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	SelectedSourceKeys []string `json:"selected_source_keys,omitempty"`
	ExcludedSourceKeys []string `json:"excluded_source_keys,omitempty"`
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

	synthesisPack, relevance := selectSynthesisPack(opts.Pack)
	builder := synthesisInputBuilder{
		pack:      synthesisPack,
		question:  question,
		budget:    budget,
		citations: make([]Citation, 0, len(synthesisPack.Evidence)+len(synthesisPack.ExactTagEvidence)),
		seen:      map[string]struct{}{},
	}
	input := builder.build()
	status := "ok"
	warnings := builder.warnings()
	if relevance != nil {
		warnings = appendUnique(warnings, "evidence_relevance_filtered")
	}
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
		Relevance:     relevance,
		AnchorContext: anchorContext,
		Warnings:      warnings,
		Status:        status,
	}, nil
}

func selectSynthesisPack(pack Pack) (Pack, *SynthesisRelevanceSelection) {
	if !hasRequiredShortPhraseConcept(pack.QueryPlan.Concepts) {
		return pack, nil
	}
	selectedEvidence, excludedEvidence := rowsMatchingAllRequiredConcepts(pack.Evidence, pack.QueryPlan.Concepts)
	selectedExact, excludedExact := rowsMatchingAllRequiredConcepts(pack.ExactTagEvidence, pack.QueryPlan.Concepts)
	if len(selectedEvidence)+len(selectedExact) < 2 {
		return pack, nil
	}
	selection := &SynthesisRelevanceSelection{
		Applied:            true,
		Reason:             "required_short_phrase",
		SelectedSourceKeys: evidenceSourceKeys(append(append([]ask.Evidence{}, selectedEvidence...), selectedExact...)),
		ExcludedSourceKeys: evidenceSourceKeys(append(append([]ask.Evidence{}, excludedEvidence...), excludedExact...)),
	}
	pack.Evidence = selectedEvidence
	pack.ExactTagEvidence = selectedExact
	return pack, selection
}

func hasRequiredShortPhraseConcept(concepts []QueryConcept) bool {
	for _, concept := range concepts {
		parts := strings.Fields(concept.Preferred)
		if concept.Required && len(parts) == 2 && len([]rune(parts[0])) == 1 {
			return true
		}
	}
	return false
}

func rowsMatchingAllRequiredConcepts(rows []ask.Evidence, concepts []QueryConcept) ([]ask.Evidence, []ask.Evidence) {
	selected := make([]ask.Evidence, 0, len(rows))
	excluded := make([]ask.Evidence, 0, len(rows))
	for _, row := range rows {
		text := researchEvidenceText(row)
		matched := true
		for _, concept := range concepts {
			if concept.Required && !conceptMatchesText(concept, text) {
				matched = false
				break
			}
		}
		if matched {
			selected = append(selected, row)
		} else {
			excluded = append(excluded, row)
		}
	}
	return selected, excluded
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
