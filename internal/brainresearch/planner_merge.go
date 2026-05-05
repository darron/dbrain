package brainresearch

import "strings"

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
