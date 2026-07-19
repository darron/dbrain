package brainresearch

import (
	"strings"
	"unicode"
)

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
		role := sanitizeMergedConceptRole(key, concept.Role)
		seen[key] = struct{}{}
		out = append(out, QueryConcept{
			Key:       key,
			Preferred: terms[0],
			Terms:     terms,
			Required:  required,
			Role:      role,
		})
		if len(out) >= maxPlannerConcepts {
			break
		}
	}
	return applyConceptRolePolicy(out)
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

func looksLikeSourceKey(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(value, "src:") || strings.HasPrefix(value, "x:") || strings.HasPrefix(value, "apple-note:") || strings.HasPrefix(value, "yt:")
}
