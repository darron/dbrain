package mcpserver

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

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
