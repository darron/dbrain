package brainresearch

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

func buildResearchStrategy(question string, hints ask.QueryHints) researchStrategy {
	return buildDeterministicResearchStrategy(question, hints)
}

func (b *Builder) buildResearchStrategy(ctx context.Context, question string, hints ask.QueryHints, opts Options) researchStrategy {
	strategy := buildDeterministicResearchStrategy(question, hints)
	strategy.Planner = "deterministic"
	if opts.DisablePlanner {
		return strategy
	}
	if !opts.UseModelPlanner && strings.TrimSpace(opts.PlannerModel) == "" {
		return strategy
	}

	planned, modelName, err := b.buildModelResearchPlan(ctx, question, hints, strategy, opts)
	if modelName != "" {
		strategy.PlannerModel = modelName
	}
	if err != nil {
		strategy.PlannerError = err.Error()
		return strategy
	}
	if planned.Empty() {
		return strategy
	}

	strategy.Planner = "model_assisted"
	strategy.Concepts = mergeQueryConcepts(strategy.Concepts, planned.Concepts)
	strategy.Variants = mergeQueryVariants(strategy.Variants, planned.QueryVariants)
	if preferred := preferredConceptQuery(strategy.Concepts); preferred != "" {
		strategy.Variants = mergeQueryVariants(strategy.Variants, []QueryVariant{{Query: preferred, Reason: "model_concept_terms"}})
	}
	strategy.Variants = limitQueryVariants(strategy.Variants)
	return strategy
}

func buildDeterministicResearchStrategy(question string, hints ask.QueryHints) researchStrategy {
	terms := uniqueStrings(hints.Terms)
	concepts := buildQueryConcepts(terms)
	variants := buildQueryVariants(question, hints.TextQuery, terms, concepts)
	return researchStrategy{Variants: variants, Concepts: concepts, Planner: "deterministic"}
}

func buildQueryConcepts(terms []string) []QueryConcept {
	concepts := make([]QueryConcept, 0, len(terms))
	seen := map[string]struct{}{}
	for _, term := range terms {
		concept := conceptForTerm(term)
		if concept.Key == "" {
			continue
		}
		if _, ok := seen[concept.Key]; ok {
			continue
		}
		seen[concept.Key] = struct{}{}
		concepts = append(concepts, concept)
	}
	return concepts
}

func conceptForTerm(term string) QueryConcept {
	term = strings.TrimSpace(strings.ToLower(term))
	switch term {
	case "father", "dad", "parent", "man":
		return QueryConcept{Key: "father", Preferred: "father", Terms: []string{"father", "dad", "parent", "man"}, Required: true}
	case "mother", "mom", "woman":
		return QueryConcept{Key: "mother", Preferred: "mother", Terms: []string{"mother", "mom", "parent", "woman"}, Required: true}
	case "children", "child", "kid", "kids", "son", "daughter":
		return QueryConcept{Key: "children", Preferred: "children", Terms: []string{"children", "child", "kid", "kids", "son", "daughter"}, Required: true}
	case "kill", "killed", "killing", "murder", "murdered", "murdering", "death", "deaths":
		return QueryConcept{Key: "kill", Preferred: "killing", Terms: []string{"kill", "killed", "killing", "kills", "murder", "murdered", "murdering", "death", "deaths", "dead"}, Required: true}
	case "charge", "charged", "charges", "charging":
		return QueryConcept{Key: "charge", Preferred: "charged", Terms: []string{"charge", "charged", "charges", "charging"}, Required: true}
	case "vehicle", "suv", "car":
		return QueryConcept{Key: "vehicle", Preferred: "vehicle", Terms: []string{"vehicle", "suv", "car", "automobile"}, Required: true}
	case "two", "2":
		return QueryConcept{Key: "two", Preferred: "two", Terms: []string{"two", "2"}, Required: false}
	case "k8s", "kubernetes":
		return QueryConcept{Key: "kubernetes", Preferred: "kubernetes", Terms: []string{"kubernetes", "k8s"}, Required: true}
	case "alternative", "alternatives", "replacement", "replacements":
		return QueryConcept{Key: "alternative", Preferred: "alternative", Terms: []string{"alternative", "alternatives", "replacement", "replacements", "instead of"}, Required: true}
	case "model", "models", "llm", "llms":
		return QueryConcept{Key: "model", Preferred: "model", Terms: []string{"model", "models", "llm", "llms", "qwen", "gpt", "claude", "gemini", "minimax", "deepseek", "ollama", "openrouter"}, Required: true}
	default:
		if len([]rune(term)) < 3 {
			return QueryConcept{}
		}
		return QueryConcept{Key: term, Preferred: term, Terms: conceptTermAliases(term), Required: true}
	}
}

func buildQueryVariants(question string, textQuery string, terms []string, concepts []QueryConcept) []QueryVariant {
	variants := make([]QueryVariant, 0, maxQueryVariants)
	add := func(query string, reason string) {
		query = strings.TrimSpace(strings.Join(strings.Fields(query), " "))
		if query == "" {
			return
		}
		for _, existing := range variants {
			if strings.EqualFold(existing.Query, query) {
				return
			}
		}
		variants = append(variants, QueryVariant{Query: query, Reason: reason})
	}

	add(textQuery, "normalized_terms")
	if preferred := preferredConceptQuery(concepts); preferred != "" && !strings.EqualFold(preferred, textQuery) {
		add(preferred, "preferred_concept_terms")
	}

	location := firstConceptKey(concepts, "calgary")
	hasFather := hasConcept(concepts, "father")
	hasChildren := hasConcept(concepts, "children")
	hasKill := hasConcept(concepts, "kill")
	if hasFather && hasChildren && hasKill {
		prefix := strings.TrimSpace(location)
		add(strings.TrimSpace(prefix+" father charged killing children"), "people_event_title_variant")
		add(strings.TrimSpace(prefix+" man charged killing children"), "people_event_title_variant")
		add(strings.TrimSpace(prefix+" father son daughter"), "victim_role_variant")
	}
	if hasChildren && hasConcept(concepts, "vehicle") {
		prefix := strings.TrimSpace(location)
		add(strings.TrimSpace(prefix+" children found vehicle"), "vehicle_context_variant")
	}
	if hasConcept(concepts, "model") && hasConcept(concepts, "agent") {
		if subject := modelStrategySubject(concepts); subject != "" {
			add("llm model stack "+subject, "model_strategy_variant")
			add("qwen gpt "+subject, "model_name_variant")
		}
	}
	for _, variant := range focusedConceptVariants(concepts) {
		add(variant.Query, variant.Reason)
	}

	if len(variants) == 0 {
		add(question, "original_question")
	}
	return limitQueryVariants(variants)
}

func modelStrategySubject(concepts []QueryConcept) string {
	terms := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		if concept.Key == "model" {
			continue
		}
		if !concept.Required {
			continue
		}
		if concept.Preferred != "" {
			terms = append(terms, concept.Preferred)
			continue
		}
		terms = append(terms, concept.Key)
	}
	return strings.Join(uniqueStrings(terms), " ")
}

func preferredConceptQuery(concepts []QueryConcept) string {
	terms := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		if concept.Preferred != "" {
			terms = append(terms, concept.Preferred)
			continue
		}
		terms = append(terms, concept.Key)
	}
	return strings.Join(uniqueStrings(terms), " ")
}

func focusedConceptVariants(concepts []QueryConcept) []QueryVariant {
	required := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		if !concept.Required {
			continue
		}
		if concept.Preferred != "" {
			required = append(required, concept.Preferred)
			continue
		}
		required = append(required, concept.Key)
	}
	if len(required) <= 3 {
		return nil
	}
	var variants []QueryVariant
	for i := 0; i <= len(required)-3 && len(variants) < 3; i++ {
		variants = append(variants, QueryVariant{Query: strings.Join(required[i:i+3], " "), Reason: "focused_concept_window"})
	}
	return variants
}

func conceptTermAliases(term string) []string {
	terms := []string{term}
	switch {
	case strings.HasSuffix(term, "ies") && len(term) > 4:
		terms = append(terms, strings.TrimSuffix(term, "ies")+"y")
	case strings.HasSuffix(term, "s") && len(term) > 4:
		terms = append(terms, strings.TrimSuffix(term, "s"))
	default:
		terms = append(terms, term+"s")
	}
	return uniqueStrings(terms)
}

func hasConcept(concepts []QueryConcept, key string) bool {
	return firstConceptKey(concepts, key) != ""
}

func firstConceptKey(concepts []QueryConcept, key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	for _, concept := range concepts {
		if concept.Key == key {
			return concept.Key
		}
	}
	return ""
}
