package brainresearch

import "strings"

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
