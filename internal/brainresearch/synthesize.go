package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	Applied            bool                         `json:"applied,omitempty"`
	Reason             string                       `json:"reason,omitempty"`
	SelectedSourceKeys []string                     `json:"selected_source_keys,omitempty"`
	ExcludedSourceKeys []string                     `json:"excluded_source_keys,omitempty"`
	TopicBriefExcluded bool                         `json:"topic_brief_excluded,omitempty"`
	Decisions          []SynthesisRelevanceDecision `json:"decisions,omitempty"`
}

type SynthesisRelevanceDecision struct {
	SourceKey       string   `json:"source_key"`
	Decision        string   `json:"decision"`
	Reason          string   `json:"reason"`
	MatchedConcepts []string `json:"matched_concepts,omitempty"`
	MissingConcepts []string `json:"missing_concepts,omitempty"`
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

	topicBriefExcluded := opts.Pack.TopicBrief != nil
	synthesisPack, relevance := selectSynthesisPack(opts.Pack)
	synthesisPack.TopicBrief = nil
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
	if topicBriefExcluded {
		warnings = appendUnique(warnings, "uncited_topic_brief_excluded")
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
	reason := ""
	if hasRequiredShortPhraseConcept(pack.QueryPlan.Concepts) {
		reason = "required_short_phrase"
	} else if supportsRequiredConceptIntersection(pack.QueryPlan.QueryFamily, pack.QueryPlan.Concepts) {
		reason = "required_concept_intersection"
	} else {
		return pack, nil
	}
	decisions, selectedKeys, excludedKeys, hasPartial := classifyRowsByRequiredConcepts(pack, pack.QueryPlan.Concepts, reason == "required_short_phrase")
	if reason == "required_concept_intersection" && (hasPartial || isCompoundSelectionQuestion(pack.Question) || hasUncertainExcludedEvidence(pack, excludedKeys)) {
		return pack, nil
	}
	if len(selectedKeys) < 2 || len(excludedKeys) == 0 || !hasSelectedDirectEvidence(pack.Evidence, selectedKeys) {
		return pack, nil
	}
	if hasSelectionDependency(pack, selectedKeys, excludedKeys) {
		return pack, nil
	}
	selectedEvidence := filterEvidenceBySourceKeys(pack.Evidence, selectedKeys)
	selectedExact := filterEvidenceBySourceKeys(pack.ExactTagEvidence, selectedKeys)
	selection := &SynthesisRelevanceSelection{
		Applied:            true,
		Reason:             reason,
		SelectedSourceKeys: sortedStringSetKeys(selectedKeys),
		ExcludedSourceKeys: sortedStringSetKeys(excludedKeys),
		TopicBriefExcluded: pack.TopicBrief != nil,
		Decisions:          decisions,
	}
	pack.Evidence = selectedEvidence
	pack.ExactTagEvidence = selectedExact
	pack.TopicBrief = nil
	return pack, selection
}

func supportsRequiredConceptIntersection(family string, concepts []QueryConcept) bool {
	switch strings.TrimSpace(family) {
	case queryFamilyEntityTopicOverview, queryFamilyPeopleEventLookup, queryFamilySoftwareProject, queryFamilyMediaEvidence:
	default:
		return false
	}
	count := 0
	for _, concept := range concepts {
		if concept.Required && (concept.Role == conceptRoleContent || concept.Role == conceptRoleAnchor || concept.Role == "") {
			count++
		}
	}
	return count >= 2
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

func classifyRowsByRequiredConcepts(pack Pack, concepts []QueryConcept, excludePartial bool) ([]SynthesisRelevanceDecision, map[string]struct{}, map[string]struct{}, bool) {
	rows := append(append([]ask.Evidence{}, pack.Evidence...), pack.ExactTagEvidence...)
	byKey := map[string]SynthesisRelevanceDecision{}
	for _, row := range rows {
		key := strings.TrimSpace(row.SourceKey)
		if key == "" {
			continue
		}
		decision := SynthesisRelevanceDecision{SourceKey: key, Decision: "selected", Reason: "all_required_concepts_matched"}
		text := synthesisEvidenceText(row)
		for _, concept := range concepts {
			if !concept.Required || (concept.Role != "" && concept.Role != conceptRoleContent && concept.Role != conceptRoleAnchor) {
				continue
			}
			if conceptMatchesText(concept, text) {
				decision.MatchedConcepts = appendUnique(decision.MatchedConcepts, concept.Key)
			} else {
				decision.MissingConcepts = appendUnique(decision.MissingConcepts, concept.Key)
			}
		}
		if len(decision.MissingConcepts) > 0 {
			decision.Decision = "excluded"
			decision.Reason = "missing_required_concepts"
		}
		if existing, ok := byKey[key]; ok {
			decision.MatchedConcepts = uniqueStrings(append(existing.MatchedConcepts, decision.MatchedConcepts...))
			decision.MissingConcepts = requiredConceptKeysMissing(concepts, decision.MatchedConcepts)
			if len(decision.MissingConcepts) == 0 {
				decision.Decision = "selected"
				decision.Reason = "all_required_concepts_matched"
			}
		}
		byKey[key] = decision
	}
	selected := map[string]struct{}{}
	excluded := map[string]struct{}{}
	hasPartial := false
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	decisions := make([]SynthesisRelevanceDecision, 0, len(keys))
	for _, key := range keys {
		decision := byKey[key]
		if decision.Decision == "selected" {
			selected[key] = struct{}{}
		} else if len(decision.MatchedConcepts) > 0 && !excludePartial {
			hasPartial = true
			decision.Decision = "retained_fail_open"
			decision.Reason = "partial_required_concept_coverage"
		} else {
			excluded[key] = struct{}{}
		}
		decisions = append(decisions, decision)
	}
	return decisions, selected, excluded, hasPartial
}

func isCompoundSelectionQuestion(question string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(question), " "))
	return strings.Count(lower, "?") > 1 ||
		strings.Contains(lower, " and also ") ||
		strings.Contains(lower, " what about ") ||
		strings.Contains(lower, " as well as ") ||
		strings.Contains(lower, " additionally ") ||
		strings.Contains(lower, " plus ") ||
		strings.Contains(lower, ";")
}

func synthesisEvidenceText(row ask.Evidence) string {
	parts := []string{researchEvidenceText(row)}
	for _, section := range row.ContentSections {
		if text := strings.TrimSpace(section.Text); text != "" {
			parts = append(parts, strings.ToLower(text))
		}
	}
	return strings.Join(parts, "\n")
}

func hasUncertainExcludedEvidence(pack Pack, excluded map[string]struct{}) bool {
	for _, row := range append(append([]ask.Evidence{}, pack.Evidence...), pack.ExactTagEvidence...) {
		if _, ok := excluded[strings.TrimSpace(row.SourceKey)]; !ok {
			continue
		}
		if row.Chunk != nil || len(row.Media) > 0 || strings.HasPrefix(row.EvidenceRole, "raw_") {
			return true
		}
		text := strings.ToLower(strings.Join([]string{row.Title, row.Summary, row.Excerpt}, " "))
		if containsAnySelectionTerm(text, "contradict", "debunk", "correction", "misattributed", "alternative", "however") {
			return true
		}
		for _, section := range row.ContentSections {
			if strings.HasPrefix(section.Role, "raw") && strings.TrimSpace(section.Text) == "" {
				return true
			}
		}
	}
	return false
}

func containsAnySelectionTerm(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func requiredConceptKeysMissing(concepts []QueryConcept, matched []string) []string {
	matchedSet := stringSliceSet(matched)
	var missing []string
	for _, concept := range concepts {
		if !concept.Required || (concept.Role != "" && concept.Role != conceptRoleContent && concept.Role != conceptRoleAnchor) {
			continue
		}
		if _, ok := matchedSet[concept.Key]; !ok {
			missing = appendUnique(missing, concept.Key)
		}
	}
	return missing
}

func hasSelectedDirectEvidence(rows []ask.Evidence, selected map[string]struct{}) bool {
	for _, row := range rows {
		if _, ok := selected[strings.TrimSpace(row.SourceKey)]; ok && strings.TrimSpace(row.RelatedTo) == "" && strings.TrimSpace(row.Relationship) == "" {
			return true
		}
	}
	return false
}

func hasSelectionDependency(pack Pack, selected map[string]struct{}, excluded map[string]struct{}) bool {
	for _, row := range append(append([]ask.Evidence{}, pack.Evidence...), pack.ExactTagEvidence...) {
		key := strings.TrimSpace(row.SourceKey)
		parents := []string{strings.TrimSpace(row.RelatedTo)}
		if row.Chunk != nil {
			parents = append(parents, strings.TrimSpace(row.Chunk.ParentSourceKey))
		}
		for _, parent := range parents {
			if parent == "" || parent == key {
				continue
			}
			_, keySelected := selected[key]
			_, parentSelected := selected[parent]
			_, keyExcluded := excluded[key]
			_, parentExcluded := excluded[parent]
			if (keySelected && parentExcluded) || (keyExcluded && parentSelected) {
				return true
			}
		}
	}
	return false
}

func filterEvidenceBySourceKeys(rows []ask.Evidence, selected map[string]struct{}) []ask.Evidence {
	out := make([]ask.Evidence, 0, len(rows))
	for _, row := range rows {
		if _, ok := selected[strings.TrimSpace(row.SourceKey)]; ok {
			out = append(out, row)
		}
	}
	return out
}

func sortedStringSetKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
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
