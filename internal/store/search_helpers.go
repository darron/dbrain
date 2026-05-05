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
	return strings.Join(terms, " AND ")
}
