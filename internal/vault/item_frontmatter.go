package vault

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func writeItemFrontmatter(b *strings.Builder, item model.Item, links []string) {
	sourceFamily := itemSourceFamily(item.SourceType)
	tags := []string{"source/" + sourceFamily}
	if item.PrimaryCategory != "" {
		tags = append(tags, "category/"+item.PrimaryCategory)
	}
	if item.PrimaryDomain != "" {
		tags = append(tags, "domain/"+item.PrimaryDomain)
	}

	b.WriteString("---\n")
	writeYAMLScalar(b, "brain_source_key", item.SourceKey)
	writeYAMLScalar(b, "source_type", item.SourceType)
	writeYAMLScalar(b, "external_id", item.ExternalID)
	writeYAMLScalar(b, "canonical_url", item.CanonicalURL)
	writeYAMLScalar(b, "title", item.Title)
	writeYAMLScalar(b, "author_handle", item.AuthorHandle)
	writeYAMLScalar(b, "author_name", item.AuthorName)
	writeYAMLScalar(b, "published_at", item.PublishedAt)
	writeYAMLScalar(b, "saved_at", item.SavedAt)
	writeYAMLScalar(b, "synced_at", item.SyncedAt)
	writeYAMLScalar(b, "primary_category", item.PrimaryCategory)
	writeYAMLScalar(b, "primary_domain", item.PrimaryDomain)
	if isXItem(item) {
		writeYAMLScalar(b, "x_post_status", item.XPostStatus)
		writeYAMLScalar(b, "x_post_fetched_at", formatTime(item.XPostFetchedAt))
		writeYAMLScalar(b, "x_post_lang", item.XPostLang)
		writeYAMLScalar(b, "summary_status", item.SummaryStatus)
		writeYAMLScalar(b, "summary_model", item.SummaryModel)
		writeYAMLScalar(b, "summary_tool", item.SummaryTool)
		writeYAMLScalar(b, "ocr_status", item.OCRStatus)
		writeYAMLScalar(b, "ocr_model", item.OCRModel)
		writeYAMLScalar(b, "ocr_tool", item.OCRTool)
	}
	writeYAMLArray(b, "tags", tags)
	writeYAMLArray(b, "links", links)
	b.WriteString("---\n\n")
}
