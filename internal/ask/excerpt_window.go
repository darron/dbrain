package ask

import (
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/retrieval"
)

func queryWindowCandidate(value string, terms []string, maxChars int, termCounts map[string]int) queryWindowResult {
	value = collapseWhitespace(value)
	if value == "" || maxChars <= 0 {
		return queryWindowResult{Text: value}
	}

	candidates := queryWindowTerms(terms)
	if len(candidates) == 0 {
		return queryWindowResult{}
	}

	lower := strings.ToLower(value)
	if len(termCounts) == 0 {
		termCounts = queryTermCounts(lower, terms)
	}
	result := retrieval.QueryWindow(value, candidates, uniqueTerms(terms), retrieval.QueryWindowOptions{
		MaxChars:    maxChars,
		LeadDivisor: 4,
		TermCounts:  termCounts,
	})
	if !result.Matched {
		return queryWindowResult{}
	}
	return queryWindowResult{Text: result.Text, Score: result.Score, Matched: true}
}

func queryTermCounts(fullText string, terms []string) map[string]int {
	counts := make(map[string]int, len(terms))
	for _, term := range uniqueTerms(terms) {
		if term == "" {
			continue
		}
		counts[term] = strings.Count(fullText, term)
	}
	return counts
}

func queryWindowTerms(terms []string) []string {
	seen := map[string]struct{}{}
	candidates := make([]string, 0, len(terms)+1)
	add := func(value string) {
		value = strings.ToLower(collapseWhitespace(value))
		if value == "" || len([]rune(value)) < 2 {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}
	add(strings.Join(terms, " "))
	for _, term := range terms {
		add(term)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return len([]rune(candidates[i])) > len([]rune(candidates[j]))
	})
	return candidates
}
