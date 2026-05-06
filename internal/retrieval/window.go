package retrieval

import "strings"

type QueryWindowOptions struct {
	MaxChars        int
	LeadDivisor     int
	EllipsisReserve int
	CountText       string
	TermCounts      map[string]int
}

type QueryWindowResult struct {
	Text      string
	Score     int
	Matched   bool
	Truncated bool
}

func QueryWindow(value string, candidates []string, coverageTerms []string, opts QueryWindowOptions) QueryWindowResult {
	if value == "" || len(candidates) == 0 {
		return QueryWindowResult{}
	}

	lower := strings.ToLower(value)
	countText := strings.ToLower(opts.CountText)
	if countText == "" {
		countText = lower
	}

	type candidateWindow struct {
		start int
		end   int
		score int
	}
	best := candidateWindow{start: -1}
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], candidate)
			if idx < 0 {
				break
			}
			idx += searchFrom
			window := queryWindowBounds(lower, idx, opts)
			windowText := lower[window.start:window.end]
			score := queryWindowScore(windowText, countText, coverageTerms, candidate, opts.TermCounts)
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
		return QueryWindowResult{}
	}

	runes := []rune(value)
	if opts.MaxChars <= 0 || len(runes) <= opts.MaxChars {
		return QueryWindowResult{Text: value, Score: best.score, Matched: true}
	}

	startRune := len([]rune(lower[:best.start]))
	endRune := len([]rune(lower[:best.end]))
	text := strings.TrimSpace(string(runes[startRune:endRune]))
	if startRune > 0 {
		text = "..." + text
	}
	if endRune < len(runes) {
		text += "..."
	}
	return QueryWindowResult{
		Text:      text,
		Score:     best.score,
		Matched:   true,
		Truncated: startRune > 0 || endRune < len(runes),
	}
}

func queryWindowBounds(value string, matchByteIndex int, opts QueryWindowOptions) struct{ start, end int } {
	runes := []rune(value)
	if opts.MaxChars <= 0 || len(runes) <= opts.MaxChars {
		return struct{ start, end int }{start: 0, end: len(value)}
	}

	bodyMax := opts.MaxChars
	if opts.EllipsisReserve > 0 {
		bodyMax = opts.MaxChars - opts.EllipsisReserve
		if bodyMax < 40 {
			bodyMax = opts.MaxChars
		}
		if bodyMax <= 0 {
			bodyMax = opts.MaxChars
		}
	}

	leadDivisor := opts.LeadDivisor
	if leadDivisor <= 0 {
		leadDivisor = 4
	}
	matchRune := len([]rune(value[:matchByteIndex]))
	startRune := matchRune - bodyMax/leadDivisor
	if startRune < 0 {
		startRune = 0
	}
	endRune := startRune + bodyMax
	if endRune > len(runes) {
		endRune = len(runes)
		startRune = endRune - bodyMax
		if startRune < 0 {
			startRune = 0
		}
	}
	startByte := len(string(runes[:startRune]))
	endByte := len(string(runes[:endRune]))
	return struct{ start, end int }{start: startByte, end: endByte}
}

func queryWindowScore(window string, fullText string, coverageTerms []string, matchedCandidate string, termCounts map[string]int) int {
	score := len([]rune(matchedCandidate))
	for _, term := range uniqueQueryWindowCoverageTerms(coverageTerms) {
		if strings.Contains(window, term) {
			occurrences := 0
			if termCounts != nil {
				occurrences = termCounts[term]
			} else {
				occurrences = strings.Count(fullText, term)
			}
			if occurrences <= 0 {
				occurrences = 1
			}
			score += 100 + 1000/occurrences + len([]rune(term))
		}
	}
	return score
}

func uniqueQueryWindowCoverageTerms(terms []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
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
