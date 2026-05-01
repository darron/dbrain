package queryterms

import (
	"strings"
	"unicode"
)

// Terms normalizes a natural-language question into stable retrieval terms.
func Terms(question string) []string {
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "can": {}, "did": {}, "do": {}, "does": {},
		"about": {}, "brain": {}, "dbrain": {}, "evidence": {}, "find": {}, "for": {}, "from": {}, "github": {}, "have": {}, "how": {}, "i": {}, "if": {}, "in": {}, "include": {}, "is": {}, "know": {}, "local": {}, "me": {}, "my": {}, "of": {}, "on": {}, "or": {}, "present": {}, "related": {}, "saved": {}, "the": {},
		"repo": {}, "repos": {}, "repository": {}, "repositories": {},
		"show": {}, "source": {}, "sources": {}, "tag": {}, "tags": {}, "tell": {}, "tweet": {}, "tweets": {},
		"to": {}, "use": {}, "using": {}, "we": {}, "what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {}, "you": {}, "your": {},
	}

	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimSpace(question)))
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
		if _, skip := stopwords[part]; skip {
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
