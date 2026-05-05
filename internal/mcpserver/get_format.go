package mcpserver

import (
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
)

func slimItem(item model.Item) map[string]interface{} {
	return map[string]interface{}{
		"id":                        item.ID,
		"source_key":                item.SourceKey,
		"source_type":               item.SourceType,
		"external_id":               item.ExternalID,
		"canonical_url":             item.CanonicalURL,
		"title":                     item.Title,
		"author_handle":             item.AuthorHandle,
		"author_name":               item.AuthorName,
		"published_at":              item.PublishedAt,
		"saved_at":                  item.SavedAt,
		"language":                  item.Language,
		"primary_category":          item.PrimaryCategory,
		"primary_domain":            item.PrimaryDomain,
		"note_path":                 item.NotePath,
		"user_tags":                 item.UserTags,
		"x_post_status":             item.XPostStatus,
		"summary_status":            item.SummaryStatus,
		"summary_model":             item.SummaryModel,
		"summary_tool":              item.SummaryTool,
		"ocr_status":                item.OCRStatus,
		"ocr_model":                 item.OCRModel,
		"ocr_tool":                  item.OCRTool,
		"x_media_transcript_status": item.XMediaTranscriptStatus,
		"imported_at":               formatGetTime(item.ImportedAt),
		"updated_at":                formatGetTime(item.UpdatedAt),
		"last_seen_at":              formatGetTime(item.LastSeenAt),
	}
}

func relatedDocument(item model.Item) retrieval.RelatedDocument {
	return retrieval.RelatedDocument{
		ID:                     item.ID,
		SourceKey:              item.SourceKey,
		SourceType:             item.SourceType,
		ExternalID:             item.ExternalID,
		CanonicalURL:           item.CanonicalURL,
		Title:                  item.Title,
		AuthorHandle:           item.AuthorHandle,
		AuthorName:             item.AuthorName,
		PublishedAt:            item.PublishedAt,
		SavedAt:                item.SavedAt,
		Language:               item.Language,
		PrimaryCategory:        item.PrimaryCategory,
		PrimaryDomain:          item.PrimaryDomain,
		NotePath:               item.NotePath,
		UserTags:               item.UserTags,
		XPostStatus:            item.XPostStatus,
		SummaryStatus:          item.SummaryStatus,
		SummaryModel:           item.SummaryModel,
		SummaryTool:            item.SummaryTool,
		OCRStatus:              item.OCRStatus,
		OCRModel:               item.OCRModel,
		OCRTool:                item.OCRTool,
		XMediaTranscriptStatus: item.XMediaTranscriptStatus,
		ImportedAt:             formatGetTime(item.ImportedAt),
		UpdatedAt:              formatGetTime(item.UpdatedAt),
		LastSeenAt:             formatGetTime(item.LastSeenAt),
	}
}

func slimSource(source model.SourceDocument) map[string]interface{} {
	return map[string]interface{}{
		"id":                    source.ID,
		"source_key":            source.SourceKey,
		"canonical_url":         source.CanonicalURL,
		"normalized_url":        source.NormalizedURL,
		"source_type":           source.SourceType,
		"domain":                source.Domain,
		"title":                 source.Title,
		"description":           source.Description,
		"site_name":             source.SiteName,
		"note_path":             source.NotePath,
		"user_tags":             source.UserTags,
		"extract_status":        source.ExtractStatus,
		"extract_error":         source.ExtractError,
		"extract_failure_kind":  source.ExtractFailureKind,
		"extract_failure_count": source.ExtractFailureCount,
		"extracted_at":          formatGetTime(source.ExtractedAt),
		"extract_tool":          source.ExtractTool,
		"extract_tool_version":  source.ExtractToolVersion,
		"summary_status":        source.SummaryStatus,
		"summary_error":         source.SummaryError,
		"summary_model":         source.SummaryModel,
		"summary_tool":          source.SummaryTool,
		"summary_tool_version":  source.SummaryToolVersion,
		"summarized_at":         formatGetTime(source.SummarizedAt),
		"content_hash":          source.ContentHash,
		"created_at":            formatGetTime(source.CreatedAt),
		"updated_at":            formatGetTime(source.UpdatedAt),
	}
}

func formatGetTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func formatGetPayload(payload map[string]interface{}) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "[%s] %s\n", payload["source_key"], payload["title"])
	if url, _ := payload["url"].(string); strings.TrimSpace(url) != "" {
		b.WriteString("URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if note, _ := payload["note"].(string); strings.TrimSpace(note) != "" {
		b.WriteString("Note: ")
		b.WriteString(note)
		b.WriteString("\n")
	}
	b.WriteString("Content mode: ")
	b.WriteString(payload["content_mode"].(string))
	b.WriteString("\n")
	if query, _ := payload["query"].(string); strings.TrimSpace(query) != "" {
		b.WriteString("Query: ")
		b.WriteString(strings.TrimSpace(query))
		b.WriteString("\n")
	}

	if item, ok := payload["item"].(map[string]interface{}); ok {
		if tags, _ := item["user_tags"].(string); strings.TrimSpace(tags) != "" {
			b.WriteString("User tags: ")
			b.WriteString(strings.TrimSpace(tags))
			b.WriteString("\n")
		}
	}
	if source, ok := payload["source"].(map[string]interface{}); ok {
		if tags, _ := source["user_tags"].(string); strings.TrimSpace(tags) != "" {
			b.WriteString("User tags: ")
			b.WriteString(strings.TrimSpace(tags))
			b.WriteString("\n")
		}
	}

	sections, _ := payload["content_sections"].([]getSection)
	if len(sections) == 0 {
		b.WriteString("\nNo content sections returned. Use content_mode=\"evidence\", \"raw\", or \"rendered\" for content.\n")
		return b.String()
	}
	for _, section := range sections {
		b.WriteString("\n## ")
		b.WriteString(section.Name)
		b.WriteString(" (")
		b.WriteString(section.Role)
		_, _ = fmt.Fprintf(&b, ", chars=%d", section.Chars)
		if section.Truncated {
			b.WriteString(", truncated")
		}
		b.WriteString(")\n")
		b.WriteString(section.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func formatGetManyPayload(payload map[string]interface{}, texts []string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Fetched %d of %d lookups", payload["count"], len(payload["lookups"].([]string)))
	if errors, ok := payload["errors"].([]getManyError); ok && len(errors) > 0 {
		_, _ = fmt.Fprintf(&b, " (%d errors)", len(errors))
	}
	b.WriteString(".\n")
	if query, _ := payload["query"].(string); strings.TrimSpace(query) != "" {
		b.WriteString("Query: ")
		b.WriteString(strings.TrimSpace(query))
		b.WriteString("\n")
	}
	if len(texts) > 0 {
		for i, text := range texts {
			if i > 0 {
				b.WriteString("\n---\n")
			}
			b.WriteString(strings.TrimSpace(text))
			b.WriteString("\n")
		}
	}
	if errors, ok := payload["errors"].([]getManyError); ok && len(errors) > 0 {
		b.WriteString("\nErrors:\n")
		for _, entry := range errors {
			b.WriteString("- ")
			b.WriteString(entry.Lookup)
			b.WriteString(": ")
			b.WriteString(entry.Error)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func uniqueGetLookups(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
