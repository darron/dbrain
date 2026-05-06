package queryterms

import (
	"strings"
	"unicode"
)

// Terms normalizes a natural-language question into stable retrieval terms.
func Terms(question string) []string {
	parts := strings.Fields(normalizeQuestionText(SearchText(question)))
	if len(parts) == 0 {
		return nil
	}

	terms := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		part = canonicalTerm(part)
		if _, skip := stopwords[part]; skip {
			continue
		}
		if looksLikeSourceKeyFragment(part) {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	if len(terms) == 0 {
		return strings.Fields(strings.ToLower(strings.TrimSpace(question)))
	}
	return terms
}
