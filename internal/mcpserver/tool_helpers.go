package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
	"github.com/darron/dbrain/internal/store"
)

func toolOKResult(text string, payload interface{}) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
		"structuredContent": payload,
		"isError":           false,
	}
}

func toolErrorResult(err error) map[string]interface{} {
	message := err.Error()
	return map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": message},
		},
		"structuredContent": map[string]interface{}{
			"error": map[string]interface{}{
				"message":     message,
				"suggestions": toolErrorSuggestions(message),
			},
		},
		"isError": true,
	}
}

func toolErrorSuggestions(message string) []string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unknown tool"):
		return []string{
			"Call tools/list and choose one of the advertised dbrain_* tools.",
			"Use dbrain_research_pack for broad research, dbrain_search for keyword lookup, dbrain_get for a known lookup, or dbrain_related for graph expansion.",
		}
	case strings.Contains(lower, "lookup is required"):
		return []string{
			"Pass a source key, external id, URL, or note path as lookup.",
			"If you do not know the lookup yet, call dbrain_research_pack or dbrain_search first.",
		}
	case strings.Contains(lower, "lookup not found"):
		return []string{
			"Verify the source key, external id, URL, or note path and retry.",
			"Use dbrain_search or dbrain_research_pack to find a current lookup before calling dbrain_get or dbrain_related.",
		}
	case strings.Contains(lower, "lookups is required"):
		return []string{
			"Pass one or more source keys, external ids, URLs, or note paths in lookups.",
			"Use dbrain_research_pack next_steps or dbrain_search results as inputs to dbrain_get_many.",
		}
	case strings.Contains(lower, "too many lookups"):
		return []string{
			fmt.Sprintf("Split the request into batches of %d or fewer lookups.", maxGetManyLookups),
		}
	case strings.Contains(lower, "unsupported content_mode"):
		return []string{
			"Use content_mode=brief, evidence, raw, or rendered.",
			"Prefer content_mode=evidence for agent research and content_mode=rendered only when note shape matters.",
		}
	default:
		return []string{
			"Inspect the error message, adjust the tool arguments, and retry.",
			"Call dbrain://mcp/overview or tools/list for the supported workflow and tool surface.",
		}
	}
}

func formatSearchResults(cfg config.Config, results []model.SearchResult) string {
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
		b.WriteString(filepath.Join(cfg.VaultDir, filepath.FromSlash(result.NotePath)))
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

func formatRelatedSources(lookup string, refs []model.ItemSourceRef) string {
	if len(refs) == 0 {
		return fmt.Sprintf("No linked sources found for %s.", lookup)
	}
	var b strings.Builder
	b.WriteString("Linked sources:\n")
	for _, ref := range refs {
		b.WriteString("- [")
		b.WriteString(ref.SourceKey)
		b.WriteString("] ")
		b.WriteString(firstNonEmpty(ref.Title, ref.CanonicalURL))
		b.WriteString("\n")
		b.WriteString("  Type: ")
		b.WriteString(ref.SourceType)
		b.WriteString("\n")
		b.WriteString("  URL: ")
		b.WriteString(ref.CanonicalURL)
		b.WriteString("\n")
		b.WriteString("  Note: ")
		b.WriteString(ref.NotePath)
		b.WriteString("\n")
		if strings.TrimSpace(ref.UserTags) != "" {
			b.WriteString("  User tags: ")
			b.WriteString(strings.TrimSpace(ref.UserTags))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatRelatedItemGraph(lookup string, refs []model.ItemSourceRef, childItems []map[string]interface{}) string {
	if len(refs) == 0 && len(childItems) == 0 {
		return fmt.Sprintf("No linked sources or child items found for %s.", lookup)
	}
	var b strings.Builder
	if len(childItems) > 0 {
		b.WriteString("Related child items:\n")
		for _, child := range childItems {
			sourceKey, _ := child["source_key"].(string)
			title, _ := child["title"].(string)
			url, _ := child["canonical_url"].(string)
			notePath, _ := child["note_path"].(string)
			sourceType, _ := child["source_type"].(string)
			b.WriteString("- [")
			b.WriteString(sourceKey)
			b.WriteString("] ")
			b.WriteString(firstNonEmpty(title, url))
			b.WriteString("\n")
			b.WriteString("  Type: ")
			b.WriteString(sourceType)
			b.WriteString("\n")
			b.WriteString("  URL: ")
			b.WriteString(url)
			b.WriteString("\n")
			b.WriteString("  Note: ")
			b.WriteString(notePath)
			b.WriteString("\n")
		}
	}
	if len(refs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatRelatedSources(lookup, refs))
	}
	return strings.TrimSpace(b.String())
}

func formatBacklinks(lookup string, refs []model.SourceBacklink) string {
	if len(refs) == 0 {
		return fmt.Sprintf("No backlinks found for %s.", lookup)
	}
	var b strings.Builder
	b.WriteString("Backlinks:\n")
	for _, ref := range refs {
		b.WriteString("- [")
		b.WriteString(ref.SourceKey)
		b.WriteString("] ")
		b.WriteString(firstNonEmpty(ref.Title, ref.CanonicalURL))
		b.WriteString("\n")
		if ref.AuthorHandle != "" || ref.AuthorName != "" {
			b.WriteString("  Author: ")
			b.WriteString(firstNonEmpty(ref.AuthorName, ref.AuthorHandle))
			b.WriteString("\n")
		}
		if strings.TrimSpace(ref.UserTags) != "" {
			b.WriteString("  User tags: ")
			b.WriteString(strings.TrimSpace(ref.UserTags))
			b.WriteString("\n")
		}
		b.WriteString("  URL: ")
		b.WriteString(ref.CanonicalURL)
		b.WriteString("\n")
		b.WriteString("  Note: ")
		b.WriteString(ref.NotePath)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func formatCountBuckets(groupBy string, buckets []store.CountBucket) string {
	if len(buckets) == 0 {
		if strings.TrimSpace(groupBy) == "none" {
			return "Count: 0"
		}
		return "Total: 0"
	}
	var b strings.Builder
	total := 0
	grouped := strings.TrimSpace(groupBy) != "none"
	for _, bucket := range buckets {
		total += bucket.Count
		if !grouped {
			continue
		}
		b.WriteString(displayBucketKey(groupBy, bucket.Key))
		b.WriteString(": ")
		_, _ = fmt.Fprintf(&b, "%d", bucket.Count)
		b.WriteString("\n")
	}
	if grouped {
		b.WriteString("Total: ")
		_, _ = fmt.Fprintf(&b, "%d", total)
		return strings.TrimSpace(b.String())
	}
	return fmt.Sprintf("Count: %d", total)
}

func displayBucketKey(groupBy string, key string) string {
	value := strings.TrimSpace(key)
	if value != "" {
		return value
	}
	switch strings.TrimSpace(groupBy) {
	case "summary-status", "extract-status":
		return "pending"
	default:
		return "(empty)"
	}
}

func countBucketTotal(buckets []store.CountBucket) int {
	total := 0
	for _, bucket := range buckets {
		total += bucket.Count
	}
	return total
}

func filterSearchResults(ctx context.Context, st *store.Store, results []model.SearchResult, sourceTypes []string) []model.SearchResult {
	if len(sourceTypes) == 0 {
		return results
	}
	filtered := make([]model.SearchResult, 0, len(results))
	for _, result := range results {
		if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, item.SourceType) {
				filtered = append(filtered, result)
			}
			continue
		}
		if source, err := st.GetSource(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, source.SourceType) {
				filtered = append(filtered, result)
			}
		}
	}
	return filtered
}

func matchesSourceTypes(filters []string, sourceType string) bool {
	if len(filters) == 0 {
		return true
	}
	sourceType = strings.TrimSpace(strings.ToLower(sourceType))
	family := sourceTypeFamily(sourceType)
	for _, filter := range filters {
		filter = strings.TrimSpace(strings.ToLower(filter))
		if filter == "" {
			continue
		}
		if filter == sourceType || filter == family {
			return true
		}
	}
	return false
}

func sourceTypeFamily(value string) string {
	if idx := strings.IndexByte(value, '_'); idx > 0 {
		return value[:idx]
	}
	return value
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func secondsTimeout(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}

func readNote(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
