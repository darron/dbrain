package mcpserver

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
)

func (s *Server) itemRelatedItemSections(ctx context.Context, item model.Item) ([]retrieval.RelatedDocument, []getSection, error) {
	childIDs, err := s.st.ListItemChildLinks(ctx, item.ID, "quoted_post")
	if err != nil {
		return nil, nil, err
	}
	if len(childIDs) == 0 {
		return nil, nil, nil
	}
	related := make([]retrieval.RelatedDocument, 0, min(len(childIDs), maxGetRelatedSections))
	sections := make([]getSection, 0, min(len(childIDs), maxGetRelatedSections))
	for _, childID := range childIDs {
		if len(sections) >= maxGetRelatedSections {
			break
		}
		child, err := s.st.GetItemByID(ctx, childID)
		if err != nil {
			continue
		}
		related = append(related, retrieval.RelatedDocumentFromItem(child))
		text := relatedItemText(child)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sections = append(sections, retrieval.NewContentSection("quoted_post:"+child.SourceKey, "related_item", child.XPostStatus, child.SummaryModel, child.SummaryTool, child.XPostFetchedAt, text, 0))
	}
	return related, sections, nil
}

func (s *Server) itemRelatedSourceSections(ctx context.Context, item model.Item) ([]model.ItemSourceRef, []getSection, error) {
	refs, err := s.st.ListSourcesForItem(ctx, item.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if len(refs) > maxGetRelatedSections {
		refs = refs[:maxGetRelatedSections]
	}
	sections := make([]getSection, 0, len(refs))
	for _, ref := range refs {
		source, err := s.st.GetSource(ctx, ref.SourceKey)
		if err != nil {
			continue
		}
		text := relatedSourceText(source)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sections = append(sections, retrieval.NewContentSection("linked_source:"+source.SourceKey, "related_source", firstNonEmpty(source.SummaryStatus, source.ExtractStatus), source.SummaryModel, firstNonEmpty(source.SummaryTool, source.ExtractTool), retrieval.FirstNonZeroTime(source.SummarizedAt, source.ExtractedAt), text, 0))
	}
	return refs, sections, nil
}

func (s *Server) sourceBacklinkSections(ctx context.Context, source model.SourceDocument) ([]model.SourceBacklink, []getSection, error) {
	refs, err := s.st.ListBacklinksForSource(ctx, source.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if len(refs) > maxGetRelatedSections {
		refs = refs[:maxGetRelatedSections]
	}
	sections := make([]getSection, 0, len(refs))
	for _, ref := range refs {
		item, err := s.st.GetItem(ctx, ref.SourceKey)
		if err != nil {
			continue
		}
		text := relatedItemText(item)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sections = append(sections, retrieval.NewContentSection("referencing_item:"+item.SourceKey, "related_item", item.XPostStatus, item.SummaryModel, item.SummaryTool, item.XPostFetchedAt, text, 0))
	}
	return refs, sections, nil
}

func relatedItemText(item model.Item) string {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(item.SourceKey)
	b.WriteString("] ")
	b.WriteString(firstNonEmpty(item.Title, item.CanonicalURL))
	if item.CanonicalURL != "" {
		b.WriteString("\nURL: ")
		b.WriteString(item.CanonicalURL)
	}
	if item.AuthorName != "" || item.AuthorHandle != "" {
		b.WriteString("\nAuthor: ")
		b.WriteString(firstNonEmpty(item.AuthorName, item.AuthorHandle))
		if item.AuthorHandle != "" && item.AuthorName != "" {
			b.WriteString(" (@")
			b.WriteString(strings.TrimPrefix(item.AuthorHandle, "@"))
			b.WriteString(")")
		}
	}
	if strings.TrimSpace(item.UserTags) != "" {
		b.WriteString("\nUser tags: ")
		b.WriteString(strings.TrimSpace(item.UserTags))
	}
	appendDistinctTextBlock(&b, "Post text", firstNonEmpty(item.XPostText, item.Text))
	appendDistinctTextBlock(&b, "Media transcript", itemMediaTranscriptText(item))
	appendDistinctTextBlock(&b, "Image OCR", item.OCRText)
	appendDistinctTextBlock(&b, "Summary", item.SummaryText)
	appendDistinctTextBlock(&b, "Article text", nonTranscriptArticleText(item))
	return b.String()
}

func relatedSourceText(source model.SourceDocument) string {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(source.SourceKey)
	b.WriteString("] ")
	b.WriteString(firstNonEmpty(source.Title, source.CanonicalURL))
	if source.CanonicalURL != "" {
		b.WriteString("\nURL: ")
		b.WriteString(source.CanonicalURL)
	}
	if strings.TrimSpace(source.UserTags) != "" {
		b.WriteString("\nUser tags: ")
		b.WriteString(strings.TrimSpace(source.UserTags))
	}
	body := firstNonEmpty(source.SummaryText, source.ExtractedText, source.Description)
	if body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String()
}

func itemMediaTranscriptText(item model.Item) string {
	if strings.EqualFold(strings.TrimSpace(item.ArticleTitle), "X Media Transcript") {
		return strings.TrimSpace(item.ArticleText)
	}
	if strings.TrimSpace(item.XMediaTranscriptStatus) == "ok" {
		return strings.TrimSpace(item.ArticleText)
	}
	return ""
}

func nonTranscriptArticleText(item model.Item) string {
	if itemMediaTranscriptText(item) != "" {
		return ""
	}
	return strings.TrimSpace(item.ArticleText)
}

func appendDistinctTextBlock(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if strings.Contains(b.String(), value) {
		return
	}
	b.WriteString("\n\n")
	b.WriteString(label)
	b.WriteString(":\n")
	b.WriteString(value)
}
