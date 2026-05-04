package brainresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

const (
	researchPlannerPromptVersion = "brain-research-planner-v1"
	defaultPlannerTimeout        = 20 * time.Second
	maxPlannerConcepts           = 8
	maxPlannerConceptTerms       = 8
	maxPlannerVariants           = 8
	maxPlannerQueryChars         = 120
)

const researchPlannerPrompt = `You are a query planner for a private local second-brain search index.
Return compact JSON only. Do not answer the user's question.

Build a retrieval plan that helps find relevant saved notes, links, titles, summaries, transcripts, and OCR text.
Use general language knowledge to expand abbreviations, synonyms, title-like phrasings, and likely alternate wording.
Do not invent facts about the user's corpus.
Keep variants short enough for keyword search.

JSON schema:
{
  "concepts": [
    {"key":"canonical_concept", "preferred":"best_search_term", "terms":["alias", "alternate phrase"], "required":true}
  ],
  "query_variants": [
    {"query":"short keyword query", "reason":"why this variant helps"}
  ]
}

Rules:
- concepts must be semantic constraints from the question, not filler words.
- required=false only for numeric counts, weak modifiers, or optional context.
- include abbreviations and expansions when useful, e.g. k8s/kubernetes.
- include title-like variants for news/event questions.
- include product/project/repository names exactly when present.
- return at most 8 concepts and 8 query_variants.`

type modelResearchPlan struct {
	Concepts      []QueryConcept `json:"concepts"`
	QueryVariants []QueryVariant `json:"query_variants"`
}

func (p modelResearchPlan) Empty() bool {
	return len(p.Concepts) == 0 && len(p.QueryVariants) == 0
}

func (b *Builder) buildModelResearchPlan(ctx context.Context, question string, hints ask.QueryHints, deterministic researchStrategy, opts Options) (modelResearchPlan, string, error) {
	modelName := summaryconfig.Model(b.cfg.RootDir, opts.PlannerModel)
	if strings.TrimSpace(modelName) == "" {
		return modelResearchPlan{}, "", nil
	}
	timeout := opts.PlannerTimeout
	if timeout <= 0 {
		timeout = defaultPlannerTimeout
	}

	inputFile, err := b.cfg.CreateTemp("dbrain-research-planner-*.md")
	if err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("create planner input: %w", err)
	}
	inputPath := inputFile.Name()
	defer func() {
		_ = os.Remove(inputPath)
	}()
	if _, err := inputFile.WriteString(plannerInput(question, hints, deterministic)); err != nil {
		_ = inputFile.Close()
		return modelResearchPlan{}, modelName, fmt.Errorf("write planner input: %w", err)
	}
	if err := inputFile.Close(); err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("close planner input: %w", err)
	}

	result, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.PlannerBinary,
		Input:     inputPath,
		Summarize: true,
		Model:     modelName,
		Prompt:    researchPlannerPrompt,
		Length:    "short",
		Language:  "auto",
		Timeout:   timeout,
		RootDir:   b.cfg.RootDir,
	})
	if err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("model planner %s: %w", researchPlannerPromptVersion, err)
	}
	if result.Summary.Status != "ok" || strings.TrimSpace(result.Summary.Text) == "" {
		return modelResearchPlan{}, modelName, fmt.Errorf("model planner %s returned no plan", researchPlannerPromptVersion)
	}
	plan, err := parseModelResearchPlan(result.Summary.Text)
	if err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("model planner %s: %w", researchPlannerPromptVersion, err)
	}
	return plan, modelName, nil
}

func plannerInput(question string, hints ask.QueryHints, deterministic researchStrategy) string {
	var b strings.Builder
	b.WriteString("# Research Question\n")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\n# Deterministic Retrieval Seed\n")
	b.WriteString("- text_query: ")
	b.WriteString(hints.TextQuery)
	b.WriteString("\n- terms: ")
	b.WriteString(strings.Join(hints.Terms, ", "))
	if len(deterministic.Concepts) > 0 {
		b.WriteString("\n- existing_concepts:")
		for _, concept := range deterministic.Concepts {
			b.WriteString("\n  - ")
			b.WriteString(concept.Key)
			b.WriteString(": ")
			b.WriteString(strings.Join(concept.Terms, ", "))
		}
	}
	if len(deterministic.Variants) > 0 {
		b.WriteString("\n- existing_variants:")
		for _, variant := range deterministic.Variants {
			b.WriteString("\n  - ")
			b.WriteString(variant.Query)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func parseModelResearchPlan(text string) (modelResearchPlan, error) {
	payload := extractJSONObject(text)
	if payload == "" {
		return modelResearchPlan{}, fmt.Errorf("planner response did not contain a JSON object")
	}
	var raw modelResearchPlan
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return modelResearchPlan{}, fmt.Errorf("parse planner JSON: %w", err)
	}
	return sanitizeModelResearchPlan(raw), nil
}

func sanitizeModelResearchPlan(raw modelResearchPlan) modelResearchPlan {
	var out modelResearchPlan
	out.Concepts = sanitizeModelConcepts(raw.Concepts)
	out.QueryVariants = sanitizeModelQueryVariants(raw.QueryVariants)
	return out
}

func sanitizeModelConcepts(concepts []QueryConcept) []QueryConcept {
	out := make([]QueryConcept, 0, min(len(concepts), maxPlannerConcepts))
	seen := map[string]struct{}{}
	for _, concept := range concepts {
		key := sanitizeConceptKey(firstNonEmpty(concept.Key, concept.Preferred))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		terms := sanitizeConceptTerms(plannerConceptSearchTerms(concept))
		if len(terms) == 0 {
			continue
		}
		required := concept.Required
		if !required && !optionalPlannerConcept(key, terms) {
			required = true
		}
		seen[key] = struct{}{}
		out = append(out, QueryConcept{
			Key:       key,
			Preferred: terms[0],
			Terms:     terms,
			Required:  required,
		})
		if len(out) >= maxPlannerConcepts {
			break
		}
	}
	return out
}

func optionalPlannerConcept(key string, terms []string) bool {
	if key == "" {
		return false
	}
	if key == "two" || key == "count" || key == "number" || key == "recent" || key == "old" || key == "older" {
		return true
	}
	for _, term := range terms {
		if term == "two" || term == "2" || term == "recent" || term == "old" || term == "older" {
			return true
		}
	}
	return false
}

func sanitizeModelQueryVariants(variants []QueryVariant) []QueryVariant {
	out := make([]QueryVariant, 0, min(len(variants), maxPlannerVariants))
	seen := map[string]struct{}{}
	for _, variant := range variants {
		query := sanitizePlannerQuery(variant.Query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, QueryVariant{
			Query:  query,
			Reason: sanitizePlannerReason(firstNonEmpty(variant.Reason, "model_planner")),
		})
		if len(out) >= maxPlannerVariants {
			break
		}
	}
	return out
}

func sanitizeConceptTerms(terms []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, min(len(terms), maxPlannerConceptTerms))
	for _, term := range terms {
		term = sanitizePlannerTerm(term)
		if term == "" {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
		if len(out) >= maxPlannerConceptTerms {
			break
		}
	}
	return out
}

func sanitizeConceptKey(value string) string {
	value = sanitizePlannerTerm(value)
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.Trim(value, "_")
	if len(value) < 2 || len(value) > 40 {
		return ""
	}
	return value
}

func sanitizePlannerTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || looksLikeSourceKey(value) {
		return ""
	}
	value = strings.Join(strings.Fields(value), " ")
	var b strings.Builder
	b.Grow(len(value))
	lastSpace := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
			lastSpace = false
		case r == '-' || r == '/' || r == '.' || r == '_':
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	value = strings.TrimSpace(b.String())
	if len([]rune(value)) < 2 || len([]rune(value)) > 60 {
		return ""
	}
	return value
}

func sanitizePlannerQuery(value string) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if value == "" || looksLikeSourceKey(value) {
		return ""
	}
	terms := sanitizeConceptTerms(strings.Fields(value))
	if len(terms) == 0 {
		return ""
	}
	query := strings.Join(terms, " ")
	if len([]rune(query)) > maxPlannerQueryChars {
		query = string([]rune(query)[:maxPlannerQueryChars])
		query = strings.TrimSpace(query)
	}
	return query
}

func sanitizePlannerReason(value string) string {
	value = sanitizePlannerTerm(value)
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "model_planner"
	}
	if len(value) > 40 {
		value = value[:40]
	}
	return value
}

func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}

func looksLikeSourceKey(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(value, "src:") || strings.HasPrefix(value, "x:") || strings.HasPrefix(value, "apple-note:") || strings.HasPrefix(value, "yt:")
}

func mergeQueryConcepts(base []QueryConcept, extra []QueryConcept) []QueryConcept {
	out := make([]QueryConcept, 0, len(base)+len(extra))
	index := map[string]int{}
	for _, concept := range base {
		concept = sanitizeMergedConcept(concept)
		if concept.Key == "" {
			continue
		}
		index[concept.Key] = len(out)
		out = append(out, concept)
	}
	for _, concept := range extra {
		concept = sanitizeMergedConcept(concept)
		if concept.Key == "" {
			continue
		}
		if pos, exists := index[concept.Key]; exists {
			out[pos] = mergeQueryConcept(out[pos], concept)
			continue
		}
		if pos := overlappingConceptIndex(out, concept); pos >= 0 {
			out[pos] = mergeQueryConcept(out[pos], concept)
			continue
		}
		// Model-only concepts are useful expansions, but they should not become
		// extra hard constraints that penalize exact matches lacking the synonym.
		concept.Required = false
		index[concept.Key] = len(out)
		out = append(out, concept)
	}
	if len(out) > maxPlannerConcepts {
		out = out[:maxPlannerConcepts]
	}
	return out
}

func mergeQueryConcept(current QueryConcept, next QueryConcept) QueryConcept {
	current.Terms = uniqueStrings(append(current.Terms, next.Terms...))
	if current.Preferred == "" {
		current.Preferred = next.Preferred
	}
	current.Required = current.Required || next.Required
	return current
}

func overlappingConceptIndex(concepts []QueryConcept, next QueryConcept) int {
	nextTerms := conceptTermSet(next)
	if len(nextTerms) == 0 {
		return -1
	}
	for i, current := range concepts {
		for term := range conceptTermSet(current) {
			if _, exists := nextTerms[term]; exists {
				return i
			}
		}
	}
	return -1
}

func conceptTermSet(concept QueryConcept) map[string]struct{} {
	terms := sanitizeConceptTerms(append([]string{concept.Key, concept.Preferred}, concept.Terms...))
	out := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		out[term] = struct{}{}
	}
	return out
}

func sanitizeMergedConcept(concept QueryConcept) QueryConcept {
	key := sanitizeConceptKey(firstNonEmpty(concept.Key, concept.Preferred))
	if key == "" {
		return QueryConcept{}
	}
	terms := sanitizeConceptTerms(plannerConceptSearchTerms(concept))
	if len(terms) == 0 {
		return QueryConcept{}
	}
	return QueryConcept{
		Key:       key,
		Preferred: terms[0],
		Terms:     terms,
		Required:  concept.Required,
	}
}

func plannerConceptSearchTerms(concept QueryConcept) []string {
	terms := make([]string, 0, len(concept.Terms)+1)
	if strings.TrimSpace(concept.Preferred) != "" {
		terms = append(terms, concept.Preferred)
	}
	terms = append(terms, concept.Terms...)
	if len(terms) == 0 {
		terms = append(terms, concept.Key)
	}
	return terms
}

func mergeQueryVariants(base []QueryVariant, extra []QueryVariant) []QueryVariant {
	out := make([]QueryVariant, 0, len(base)+len(extra))
	seen := map[string]struct{}{}
	for _, variant := range append(base, extra...) {
		query := sanitizePlannerQuery(variant.Query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, QueryVariant{
			Query:  query,
			Reason: sanitizePlannerReason(variant.Reason),
		})
	}
	return limitQueryVariants(out)
}

func limitQueryVariants(variants []QueryVariant) []QueryVariant {
	if len(variants) > maxQueryVariants {
		return variants[:maxQueryVariants]
	}
	return variants
}
