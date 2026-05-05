package mcpserver

import (
	"sort"
	"strings"
	"unicode"

	"github.com/darron/dbrain/internal/retrieval"
)

func queryWindowWithFlag(value string, query string, maxChars int) (string, bool, bool) {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if text == "" {
		return "", false, false
	}
	terms := queryWindowTerms(query)
	if len(terms) == 0 {
		return "", false, false
	}
	result := retrieval.QueryWindow(text, terms, queryWindowCoverageTerms(terms), retrieval.QueryWindowOptions{
		MaxChars:        maxChars,
		LeadDivisor:     3,
		EllipsisReserve: 6,
	})
	if !result.Matched {
		return "", false, false
	}
	return result.Text, result.Truncated, true
}

func queryWindowCoverageTerms(terms []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, term := range terms {
		if strings.ContainsAny(term, " -") {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func queryWindowTerms(query string) []string {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil
	}
	stopwords := map[string]struct{}{
		"a": {}, "about": {}, "an": {}, "and": {}, "are": {}, "brain": {}, "can": {}, "dbrain": {}, "do": {}, "does": {}, "for": {},
		"evidence": {}, "find": {}, "give": {}, "have": {}, "how": {}, "i": {}, "if": {}, "in": {}, "include": {}, "is": {}, "know": {}, "me": {}, "my": {}, "of": {}, "on": {},
		"overview": {}, "present": {}, "related": {}, "saved": {}, "show": {}, "tag": {}, "tags": {}, "tell": {}, "the": {}, "to": {}, "use": {}, "using": {}, "we": {}, "what": {}, "why": {}, "you": {}, "your": {},
	}
	parts := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(part)) < 2 {
			continue
		}
		if _, skip := stopwords[part]; skip {
			continue
		}
		filtered = append(filtered, part)
	}
	candidates := make([]string, 0, len(filtered)+3)
	if len(filtered) > 1 {
		candidates = append(candidates, strings.Join(filtered, " "), strings.Join(filtered, "-"))
	}
	if len([]rune(query)) <= 120 {
		candidates = append(candidates, query)
	}
	candidates = append(candidates, filtered...)

	seen := map[string]struct{}{}
	terms := make([]string, 0, len(candidates))
	for _, term := range candidates {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		return len([]rune(terms[i])) > len([]rune(terms[j]))
	})
	return terms
}
