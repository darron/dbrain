package ask

import "strings"

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func compactMatchText(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

func hasAnyText(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func excerptValuesWithRawFallback(terms []string, compact []string, raw ...string) []string {
	values := make([]string, 0, len(compact)+len(raw))
	for _, value := range compact {
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 || !hasEnoughQueryCoverage(values, terms) {
		for _, value := range raw {
			if strings.TrimSpace(value) != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

func sourceExcerptValues(compact []string, terms []string, raw ...string) []string {
	values := make([]string, 0, len(compact)+len(raw))
	for _, value := range compact {
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 || !hasEnoughQueryCoverage(values, terms) {
		for _, value := range raw {
			if strings.TrimSpace(value) != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

func hasEnoughQueryCoverage(values []string, terms []string) bool {
	queryTerms := uniqueTerms(terms)
	if len(queryTerms) == 0 {
		return true
	}
	required := minInt(2, len(queryTerms))
	text := strings.ToLower(strings.Join(values, "\n"))
	matched := 0
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			matched++
			if matched >= required {
				return true
			}
		}
	}
	return false
}

func evidenceExcerpt(maxChars int, terms []string, values ...string) string {
	var best queryWindowResult
	collapsedValues := make([]string, 0, len(values))
	for _, value := range values {
		value = collapseWhitespace(value)
		if value == "" {
			continue
		}
		collapsedValues = append(collapsedValues, value)
	}
	scoreText := strings.ToLower(strings.Join(collapsedValues, "\n"))
	termCounts := queryTermCounts(scoreText, terms)
	for _, value := range collapsedValues {
		result := queryWindowCandidate(value, terms, maxChars, termCounts)
		if result.Matched && (!best.Matched || result.Score > best.Score) {
			best = result
		}
	}
	if best.Matched {
		return best.Text
	}
	for _, value := range collapsedValues {
		if excerpt := trimTo(value, maxChars); excerpt != "" {
			return excerpt
		}
	}
	return ""
}

type queryWindowResult struct {
	Text    string
	Score   int
	Matched bool
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func trimTo(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "..."
}
