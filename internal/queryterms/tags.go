package queryterms

import "strings"

// TagQueries returns hyphenated tag aliases that match dbrain user_tags.
func TagQueries(terms []string) []string {
	if len(terms) < 2 {
		return nil
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || len([]rune(term)) < 3 {
			continue
		}
		parts = append(parts, term)
	}
	if len(parts) < 2 {
		return nil
	}
	seen := map[string]struct{}{}
	var queries []string
	add := func(query string) {
		if query == "" {
			return
		}
		if _, ok := seen[query]; ok {
			return
		}
		seen[query] = struct{}{}
		queries = append(queries, query)
	}
	add(strings.Join(parts, "-"))
	if len(parts) > 2 {
		for i := 0; i < len(parts)-1; i++ {
			add(parts[i] + "-" + parts[i+1])
		}
	}
	return queries
}
