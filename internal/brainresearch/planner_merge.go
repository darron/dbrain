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
		out = trimConceptsPreservingAnchors(out, maxPlannerConcepts)
	}
	return applyConceptRolePolicy(out)
}

func mergeQueryConcept(current QueryConcept, next QueryConcept) QueryConcept {
	current.Terms = uniqueStrings(append(current.Terms, next.Terms...))
	if current.Preferred == "" {
		current.Preferred = next.Preferred
	}
	current.Required = current.Required || next.Required
	current.Role = mergeConceptRole(current.Role, next.Role)
	return current
}

func mergeConceptRole(current string, next string) string {
	rank := map[string]int{
		conceptRoleFrame:   1,
		conceptRoleIntent:  2,
		conceptRoleContent: 3,
		conceptRoleAnchor:  4,
	}
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		current = conceptRoleContent
	}
	if next == "" {
		next = conceptRoleContent
	}
	if rank[next] > rank[current] {
		return next
	}
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
	role := sanitizeMergedConceptRole(key, concept.Role)
	return QueryConcept{
		Key:       key,
		Preferred: terms[0],
		Terms:     terms,
		Required:  concept.Required,
		Role:      role,
	}
}

func sanitizeMergedConceptRole(key string, incoming string) string {
	classified := classifyConceptRole(key)
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return classified
	}
	if incoming == conceptRoleAnchor {
		return conceptRoleAnchor
	}
	if classified != conceptRoleContent {
		return classified
	}
	switch incoming {
	case conceptRoleFrame, conceptRoleIntent, conceptRoleContent:
		return incoming
	default:
		return classified
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
	add := func(variant QueryVariant, sanitize bool) {
		query := strings.TrimSpace(strings.Join(strings.Fields(variant.Query), " "))
		if sanitize {
			query = sanitizePlannerQuery(query)
		}
		if query == "" {
			return
		}
		key := strings.ToLower(query)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, QueryVariant{
			Query:  query,
			Reason: sanitizePlannerReason(variant.Reason),
		})
	}
	for _, variant := range base {
		add(variant, false)
	}
	for _, variant := range extra {
		add(variant, true)
	}
	return limitQueryVariants(out)
}

func limitQueryVariants(variants []QueryVariant) []QueryVariant {
	if len(variants) > maxQueryVariants {
		return variants[:maxQueryVariants]
	}
	return variants
}

func trimConceptsPreservingAnchors(concepts []QueryConcept, limit int) []QueryConcept {
	if limit <= 0 || len(concepts) <= limit {
		return concepts
	}
	out := make([]QueryConcept, 0, limit)
	for _, concept := range concepts {
		if concept.Role == conceptRoleAnchor {
			out = append(out, concept)
		}
	}
	for _, concept := range concepts {
		if len(out) >= limit {
			break
		}
		if concept.Role == conceptRoleAnchor {
			continue
		}
		out = append(out, concept)
	}
	return out
}
