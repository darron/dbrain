package okf

import (
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func renderItemDocument(item itemDoc, snapshot store.OKFExportSnapshot, opts ExportOptions, pathByConceptID map[string]string, sourceConceptByID map[int64]string, itemConceptByID map[int64]string) (Document, []OmittedLink, error) {
	doc := Document{
		Path:        item.Path,
		Type:        "Item",
		Title:       itemTitle(item.Item),
		Description: itemDescription(item.Item),
		Resource:    firstNonEmpty(item.Item.CanonicalURL, "dbrain://"+item.ConceptID),
		Tags:        itemTags(item.Item),
		Timestamp:   timestampForItem(item.Item),
		Fields: []Field{
			{Name: "dbrain_concept_id", Value: item.ConceptID},
			{Name: "dbrain_kind", Value: "item"},
			{Name: "dbrain_source_key", Value: item.Item.SourceKey},
			{Name: "dbrain_source_type", Value: item.Item.SourceType},
			{Name: "dbrain_external_id", Value: item.Item.ExternalID},
			{Name: "dbrain_note_path", Value: item.Item.NotePath},
			{Name: "author_handle", Value: item.Item.AuthorHandle},
			{Name: "author_name", Value: item.Item.AuthorName},
			{Name: "published_at", Value: normalizeTimestamp(item.Item.PublishedAt)},
			{Name: "saved_at", Value: normalizeTimestamp(item.Item.SavedAt)},
			{Name: "summary_model", Value: item.Item.SummaryModel},
			{Name: "summary_prompt_version", Value: item.Item.SummaryPromptVersion},
		},
	}

	var body strings.Builder
	writeSection(&body, "Overview", doc.Description)
	writeItemSource(&body, item.Item)
	writeItemSummary(&body, item.Item)
	writeItemRawEvidence(&body, item.Item, opts)
	writeItemMedia(&body, item.Item, snapshot.ItemMedia[item.Item.ID])
	omitted, err := writeItemRelated(&body, item, snapshot, pathByConceptID, sourceConceptByID, itemConceptByID)
	if err != nil {
		return Document{}, nil, err
	}
	writeItemCitations(&body, item.Item)
	doc.Body = strings.TrimSpace(body.String()) + "\n"
	return doc, omitted, nil
}

func itemTags(item model.Item) []string {
	var tags []string
	if value := normalizeTag(item.SourceType); value != "" {
		tags = append(tags, "source/"+value)
	}
	if value := normalizeTag(item.PrimaryDomain); value != "" {
		tags = append(tags, "domain/"+value)
	}
	if value := normalizeTag(item.PrimaryCategory); value != "" {
		tags = append(tags, "category/"+value)
	}
	for _, tag := range splitTags(item.UserTags) {
		if normalized := normalizeTag(tag); normalized != "" {
			tags = append(tags, normalized)
		}
	}
	return sortedUnique(tags)
}

func writeItemSource(b *strings.Builder, item model.Item) {
	b.WriteString("\n# Source\n\n")
	writeBullet(b, "Source key", code(item.SourceKey))
	writeBullet(b, "Source type", code(item.SourceType))
	writeBullet(b, "URL", item.CanonicalURL)
	if item.AuthorName != "" || item.AuthorHandle != "" {
		author := strings.TrimSpace(item.AuthorName)
		if item.AuthorHandle != "" {
			if author != "" {
				author += " "
			}
			author += "@" + strings.TrimPrefix(strings.TrimSpace(item.AuthorHandle), "@")
		}
		writeBullet(b, "Author", author)
	}
	writeBullet(b, "Published", item.PublishedAt)
	writeBullet(b, "Saved", item.SavedAt)
}

func writeItemSummary(b *strings.Builder, item model.Item) {
	if text := strings.TrimSpace(item.SummaryText); text != "" {
		writeSection(b, "Derived Summary", text)
		return
	}
	if strings.TrimSpace(item.SummaryStatus) == "blocked" || strings.TrimSpace(item.SummaryError) != "" {
		var status strings.Builder
		writeBullet(&status, "Summary status", code(item.SummaryStatus))
		writeBullet(&status, "Summary error", item.SummaryError)
		writeSection(b, "Derived Summary", status.String())
	}
}

func writeItemRawEvidence(b *strings.Builder, item model.Item, opts ExportOptions) {
	if !opts.IncludeRaw {
		return
	}
	var raw strings.Builder
	if text := truncateRaw(item.XPostText, opts.MaxRawChars); text != "" {
		raw.WriteString("## Canonical X Post\n\n")
		raw.WriteString(text)
		raw.WriteString("\n\n")
	}
	if text := strings.TrimSpace(item.Text); text != "" && text != strings.TrimSpace(item.XPostText) {
		raw.WriteString("## Imported Text\n\n")
		raw.WriteString(truncateRaw(text, opts.MaxRawChars))
		raw.WriteString("\n\n")
	}
	if item.ArticleTitle != "" || item.ArticleText != "" {
		if strings.TrimSpace(item.ArticleTitle) == model.XMediaTranscriptArticleTitle {
			raw.WriteString("## Media Transcript\n\n")
			raw.WriteString(truncateRaw(item.ArticleText, opts.MaxRawChars))
			raw.WriteString("\n\n")
		} else {
			raw.WriteString("## Cached Source Extract\n\n")
			if item.ArticleTitle != "" {
				raw.WriteString("### Title\n\n")
				raw.WriteString(strings.TrimSpace(item.ArticleTitle))
				raw.WriteString("\n\n")
			}
			if item.ArticleText != "" {
				raw.WriteString("### Text\n\n")
				raw.WriteString(truncateRaw(item.ArticleText, opts.MaxRawChars))
				raw.WriteString("\n\n")
			}
		}
	}
	if text := truncateRaw(item.OCRText, opts.MaxRawChars); text != "" {
		raw.WriteString("## OCR / Vision Extract\n\n")
		raw.WriteString(text)
		raw.WriteString("\n\n")
	}
	writeSection(b, "Raw Evidence", raw.String())
}

func writeItemMedia(b *strings.Builder, item model.Item, media []model.ItemMediaRef) {
	if len(media) == 0 {
		return
	}
	var body strings.Builder
	for _, ref := range media {
		label := fmt.Sprintf("Media %d", ref.Ordinal+1)
		if strings.TrimSpace(ref.MediaType) != "" {
			label = titleCase(strings.ReplaceAll(ref.MediaType, "_", " ")) + fmt.Sprintf(" %d", ref.Ordinal+1)
		}
		body.WriteString("## ")
		body.WriteString(label)
		body.WriteString("\n\n")
		writeBullet(&body, "Original item", item.CanonicalURL)
		writeBullet(&body, "Media source", ref.RemoteURL)
		writeBullet(&body, "Expanded media URL", ref.ExpandedURL)
		if strings.TrimSpace(ref.ArchiveStatus) == model.MediaArchiveStatusArchived {
			writeBullet(&body, "Archived media", ref.ArchiveURL)
		}
		writeBullet(&body, "Archive status", code(ref.ArchiveStatus))
		writeBullet(&body, "Download status", code(ref.DownloadStatus))
		if ref.Width > 0 || ref.Height > 0 {
			writeBullet(&body, "Dimensions", fmt.Sprintf("%dx%d", ref.Width, ref.Height))
		}
		body.WriteString("\n")
	}
	writeSection(b, "Media", body.String())
}

func titleCase(value string) string {
	parts := strings.Fields(value)
	for i, part := range parts {
		if len(part) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func writeItemRelated(b *strings.Builder, item itemDoc, snapshot store.OKFExportSnapshot, pathByConceptID map[string]string, sourceConceptByID map[int64]string, itemConceptByID map[int64]string) ([]OmittedLink, error) {
	var body strings.Builder
	var omitted []OmittedLink
	for _, ref := range snapshot.ItemSources[item.Item.ID] {
		conceptID := sourceConceptByID[ref.SourceID]
		targetPath := pathByConceptID[conceptID]
		if targetPath == "" {
			omitted = append(omitted, omittedByFilter(item.Path, ref.NotePath, conceptID))
			continue
		}
		rel, err := RelativeLink(item.Path, targetPath)
		if err != nil {
			return nil, err
		}
		body.WriteString("- ")
		body.WriteString(MarkdownLink(firstNonEmpty(ref.Title, ref.CanonicalURL, ref.SourceKey), rel))
		body.WriteString(" - linked source\n")
	}
	for _, ref := range snapshot.ItemChildren[item.Item.ID] {
		conceptID := itemConceptByID[ref.ItemID]
		targetPath := pathByConceptID[conceptID]
		if targetPath == "" {
			omitted = append(omitted, omittedByFilter(item.Path, ref.NotePath, conceptID))
			continue
		}
		rel, err := RelativeLink(item.Path, targetPath)
		if err != nil {
			return nil, err
		}
		body.WriteString("- ")
		body.WriteString(MarkdownLink(firstNonEmpty(ref.Title, ref.SourceKey), rel))
		body.WriteString(" - related item\n")
	}
	writeSection(b, "Related Concepts", body.String())
	return omitted, nil
}

func writeItemCitations(b *strings.Builder, item model.Item) {
	if strings.TrimSpace(item.CanonicalURL) == "" {
		return
	}
	writeSection(b, "Citations", "[1] "+MarkdownLink("Original URL", item.CanonicalURL))
}

func sortedUnique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
