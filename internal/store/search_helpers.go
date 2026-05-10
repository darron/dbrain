package store

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"github.com/darron/dbrain/internal/model"
)

func scanSearchResults(rows *sql.Rows) ([]model.SearchResult, error) {
	var results []model.SearchResult
	for rows.Next() {
		var result model.SearchResult
		if err := rows.Scan(&result.SourceKey, &result.SourceType, &result.ExternalID, &result.Title, &result.AuthorHandle, &result.AuthorName, &result.CanonicalURL, &result.PrimaryDomain, &result.NotePath, &result.UserTags, &result.Snippet); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return results, nil
}

func buildFTSQuery(query string) string {
	return buildFTSQueryWithOperator(query, "AND")
}

func buildRelaxedFTSQuery(query string) string {
	return buildFTSQueryWithOperator(query, "OR")
}

func buildFTSQueryWithOperator(query, operator string) string {
	parts := strings.Fields(strings.TrimSpace(query))
	if len(parts) == 0 {
		return `""`
	}

	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(part, `"`, `""`)
		terms = append(terms, fmt.Sprintf(`"%s"*`, part))
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " "+operator+" ")
}

func relaxedMatchTerms(query string) []string {
	parts := strings.Fields(strings.TrimSpace(query))
	terms := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimFunc(strings.ToLower(part), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-'
		})
		if len(part) < 3 {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	return terms
}

func matchesRelaxedTerms(result model.SearchResult, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	minMatches := 1
	if len(terms) > 1 {
		minMatches = 2
	}
	text := strings.ToLower(strings.Join([]string{
		result.SourceKey,
		result.Title,
		result.AuthorHandle,
		result.AuthorName,
		result.CanonicalURL,
		result.PrimaryDomain,
		result.NotePath,
		result.UserTags,
		result.Snippet,
	}, " "))
	matches := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			matches++
			if matches >= minMatches {
				return true
			}
		}
	}
	return false
}
