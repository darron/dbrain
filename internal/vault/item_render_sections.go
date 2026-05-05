package vault

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
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

func writeItemTitle(b *strings.Builder, item model.Item) {
	b.WriteString("# ")
	b.WriteString(item.Title)
	b.WriteString("\n\n")
}

func writeItemSourceSection(b *strings.Builder, item model.Item) {
	b.WriteString("## Source\n\n")
	b.WriteString("- Source key: `")
	b.WriteString(item.SourceKey)
	b.WriteString("`\n")
	b.WriteString("- URL: ")
	b.WriteString(item.CanonicalURL)
	b.WriteString("\n")
	if item.AuthorHandle != "" || item.AuthorName != "" {
		b.WriteString("- Author: ")
		if item.AuthorName != "" {
			b.WriteString(item.AuthorName)
			if item.AuthorHandle != "" {
				b.WriteString(" ")
			}
		}
		if item.AuthorHandle != "" {
			b.WriteString("(@")
			b.WriteString(item.AuthorHandle)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if item.PublishedAt != "" {
		b.WriteString("- Published: ")
		b.WriteString(item.PublishedAt)
		b.WriteString("\n")
	}
	if item.SavedAt != "" {
		b.WriteString("- ")
		b.WriteString(itemSavedLabel(item))
		b.WriteString(": ")
		b.WriteString(item.SavedAt)
		b.WriteString("\n")
	}
	if item.PrimaryCategory != "" {
		b.WriteString("- ")
		b.WriteString(itemCategoryLabel(item))
		b.WriteString(": `")
		b.WriteString(item.PrimaryCategory)
		b.WriteString("`\n")
	}
	if item.PrimaryDomain != "" {
		b.WriteString("- Domain: `")
		b.WriteString(item.PrimaryDomain)
		b.WriteString("`\n")
	}
	if isXItem(item) && item.XPostStatus != "" {
		b.WriteString("- X API status: `")
		b.WriteString(item.XPostStatus)
		b.WriteString("`\n")
	}
	if isXItem(item) && !item.XPostFetchedAt.IsZero() {
		b.WriteString("- X API fetched: ")
		b.WriteString(formatTime(item.XPostFetchedAt))
		b.WriteString("\n")
	}
	if isXItem(item) && item.XPostLang != "" {
		b.WriteString("- X API language: `")
		b.WriteString(item.XPostLang)
		b.WriteString("`\n")
	}
}

func writeItemSummarySection(b *strings.Builder, item model.Item) {
	b.WriteString("\n## Summary\n\n")
	switch {
	case strings.TrimSpace(item.SummaryText) != "":
		b.WriteString(strings.TrimSpace(item.SummaryText))
		b.WriteString("\n")
	case isXItem(item) && item.XPostText != "":
		b.WriteString("Canonical X post text is cached below. No item summary stored yet.\n")
	case item.ArticleText != "":
		b.WriteString("Imported source text is available below. No item summary stored yet.\n")
	case isGitHubItem(item):
		b.WriteString("GitHub star imported. Canonical repo and homepage enrichment are stored on linked source notes.\n")
	case isYouTubeItem(item):
		b.WriteString("YouTube signal imported. Canonical video extraction and summarization are stored on linked source notes.\n")
	case isAppleNoteItem(item):
		b.WriteString("Apple Note imported. Local note body is cached below. No item summary stored yet.\n")
	default:
		b.WriteString("Bookmark imported. No expanded source text is stored yet.\n")
	}
}

func writeItemEvidenceSections(b *strings.Builder, item model.Item, snapshot *xpost.Snapshot) {
	if text := strings.TrimSpace(item.XPostText); text != "" {
		b.WriteString("\n## Canonical X Post\n\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	if isXItem(item) && snapshot != nil && snapshot.QuotedPost != nil {
		writeQuotedPostSection(b, snapshot.QuotedPost)
	}

	if text := strings.TrimSpace(item.Text); text != "" && !sameNormalizedText(text, item.XPostText) {
		b.WriteString("\n## ")
		b.WriteString(itemTextHeading(item))
		b.WriteString("\n\n")
		b.WriteString(text)
		b.WriteString("\n")
	}

	if item.ArticleTitle != "" || item.ArticleText != "" {
		b.WriteString("\n## Cached Source Extract\n\n")
		if item.ArticleTitle != "" {
			b.WriteString("### Title\n\n")
			b.WriteString(item.ArticleTitle)
			b.WriteString("\n\n")
		}
		if item.ArticleText != "" {
			b.WriteString("### Text\n\n")
			b.WriteString(strings.TrimSpace(item.ArticleText))
			b.WriteString("\n")
		}
	}

	if text := strings.TrimSpace(item.OCRText); text != "" {
		b.WriteString("\n## OCR / Vision Extract\n\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
}

func writeItemLinksSection(b *strings.Builder, links []string) {
	if len(links) == 0 {
		return
	}
	b.WriteString("\n## Outbound Links\n\n")
	for _, link := range links {
		b.WriteString("- ")
		b.WriteString(link)
		b.WriteString("\n")
	}
}

func writeItemMetadataSection(b *strings.Builder, item model.Item) {
	metadata := itemMetadataLines(item)
	if len(metadata) == 0 {
		return
	}
	b.WriteString("\n## Metadata\n\n")
	for _, line := range metadata {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
}
