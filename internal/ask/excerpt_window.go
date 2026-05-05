package ask

import (
	"sort"
	"strings"
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
	type candidateWindow struct {
		start int
		end   int
		score int
	}
	best := candidateWindow{start: -1}
	for _, candidate := range candidates {
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], candidate)
			if idx < 0 {
				break
			}
			idx += searchFrom
			window := queryWindowBounds(lower, idx, maxChars)
			windowText := lower[window.start:window.end]
			score := queryWindowScore(windowText, terms, candidate, termCounts)
			if best.start < 0 || score > best.score || (score == best.score && window.start < best.start) {
				best = candidateWindow{start: window.start, end: window.end, score: score}
			}
			searchFrom = idx + len(candidate)
			if searchFrom >= len(lower) {
				break
			}
		}
	}
	if best.start < 0 {
		return queryWindowResult{}
	}

	runes := []rune(value)
	if len(runes) <= maxChars {
		return queryWindowResult{Text: value, Score: best.score, Matched: true}
	}

	startRune := len([]rune(lower[:best.start]))
	endRune := len([]rune(lower[:best.end]))
	excerpt := strings.TrimSpace(string(runes[startRune:endRune]))
	if startRune > 0 {
		excerpt = "..." + excerpt
	}
	if endRune < len(runes) {
		excerpt += "..."
	}
	return queryWindowResult{Text: excerpt, Score: best.score, Matched: true}
}

func queryWindowBounds(value string, matchByteIndex int, maxChars int) struct{ start, end int } {
	runes := []rune(value)
	if len(runes) <= maxChars {
		return struct{ start, end int }{start: 0, end: len(value)}
	}
	matchRune := len([]rune(value[:matchByteIndex]))
	startRune := matchRune - maxChars/4
	if startRune < 0 {
		startRune = 0
	}
	endRune := startRune + maxChars
	if endRune > len(runes) {
		endRune = len(runes)
		startRune = endRune - maxChars
		if startRune < 0 {
			startRune = 0
		}
	}
	startByte := len(string(runes[:startRune]))
	endByte := len(string(runes[:endRune]))
	return struct{ start, end int }{start: startByte, end: endByte}
}

func queryWindowScore(window string, terms []string, matchedCandidate string, termCounts map[string]int) int {
	score := len([]rune(matchedCandidate))
	for _, term := range uniqueTerms(terms) {
		if strings.Contains(window, term) {
			occurrences := termCounts[term]
			if occurrences <= 0 {
				occurrences = 1
			}
			score += 100 + 1000/occurrences + len([]rune(term))
		}
	}
	return score
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
