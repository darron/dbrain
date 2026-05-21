package mcpserver

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
)

func formatSearchResults(results []searchResultWithMedia) string {
	if len(results) == 0 {
		return "No results."
	}
	var b strings.Builder
	for _, result := range results {
		b.WriteString("- [")
		b.WriteString(result.SourceKey)
		b.WriteString("] ")
		b.WriteString(result.Title)
		b.WriteString("\n")
		b.WriteString("  URL: ")
		b.WriteString(result.CanonicalURL)
		b.WriteString("\n")
		b.WriteString("  Note: ")
		b.WriteString(result.NotePath)
		b.WriteString("\n")
		if strings.TrimSpace(result.UserTags) != "" {
			b.WriteString("  User tags: ")
			b.WriteString(strings.TrimSpace(result.UserTags))
			b.WriteString("\n")
		}
		if strings.TrimSpace(result.Snippet) != "" {
			b.WriteString("  Snippet: ")
			b.WriteString(strings.TrimSpace(result.Snippet))
			b.WriteString("\n")
		}
		if len(result.Media) > 0 {
			b.WriteString("  Media: ")
			parts := make([]string, 0, len(result.Media))
			for _, ref := range result.Media {
				label := strings.TrimSpace(ref.MediaType)
				if label == "" {
					label = "media"
				}
				if strings.TrimSpace(ref.ArchiveURL) != "" {
					label += " " + strings.TrimSpace(ref.ArchiveURL)
				} else if strings.TrimSpace(ref.RemoteURL) != "" {
					label += " " + strings.TrimSpace(ref.RemoteURL)
				}
				parts = append(parts, label)
			}
			b.WriteString(strings.Join(parts, "; "))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func searchTagAliases(query string) []string {
	aliases := []string{}
	aliases = append(aliases, queryterms.TagQueries(queryterms.Terms(query))...)
	trimmed := strings.TrimSpace(strings.ToLower(query))
	if strings.Contains(trimmed, "-") && !strings.ContainsAny(trimmed, " \t\n\r") {
		aliases = append(aliases, trimmed)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(strings.ToLower(alias))
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func dedupeSearchResults(results []model.SearchResult, limit int) []model.SearchResult {
	if limit <= 0 {
		limit = len(results)
	}
	seen := map[string]struct{}{}
	out := make([]model.SearchResult, 0, min(len(results), limit))
	for _, result := range results {
		if strings.TrimSpace(result.SourceKey) == "" {
			continue
		}
		if _, exists := seen[result.SourceKey]; exists {
			continue
		}
		seen[result.SourceKey] = struct{}{}
		out = append(out, result)
		if len(out) >= limit {
			break
		}
	}
	return out
}
