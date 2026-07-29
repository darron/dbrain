package retrievalchunk

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func ProjectItem(item model.Item) Parent {
	parent := Parent{
		Kind:        "item",
		SourceKey:   strings.TrimSpace(item.SourceKey),
		ContentHash: strings.TrimSpace(item.ContentHash),
		Title:       strings.TrimSpace(item.Title),
		SourceType:  strings.TrimSpace(item.SourceType),
		Author:      firstNonBlank(item.AuthorName, item.AuthorHandle),
	}

	seen := make(map[string]struct{})
	appendSection := func(key, role, heading, text string, derived bool) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		dedupeKey := role + "\x00" + text
		if _, ok := seen[dedupeKey]; ok {
			return
		}
		seen[dedupeKey] = struct{}{}
		parent.Sections = append(parent.Sections, Section{
			Key: key, Role: role, Heading: strings.TrimSpace(heading), Text: text, Derived: derived,
		})
	}

	appendSection("item:text", "raw", parent.Title, item.Text, false)
	appendSection("item:x_post_text", "raw", parent.Title, item.XPostText, false)
	appendSection("item:ocr", "ocr", "OCR", item.OCRText, false)
	if strings.TrimSpace(item.ArticleTitle) == model.XMediaTranscriptArticleTitle {
		appendSection("item:transcript", "transcript", model.XMediaTranscriptArticleTitle, item.ArticleText, false)
	} else {
		appendSection("item:article", "raw", item.ArticleTitle, item.ArticleText, false)
	}
	appendSection("item:summary", "summary", "Summary", item.SummaryText, true)
	return parent
}

func ProjectSource(source model.SourceDocument) Parent {
	parent := Parent{
		Kind:        "source",
		SourceKey:   strings.TrimSpace(source.SourceKey),
		ContentHash: strings.TrimSpace(source.ContentHash),
		Title:       strings.TrimSpace(source.Title),
		SourceType:  strings.TrimSpace(source.SourceType),
		Author:      strings.TrimSpace(source.Domain),
	}
	if text := strings.TrimSpace(source.ExtractedText); text != "" {
		parent.Sections = append(parent.Sections, Section{
			Key: "source:extract", Role: "raw", Heading: parent.Title, Text: text,
		})
	}
	if text := strings.TrimSpace(source.SummaryText); text != "" {
		parent.Sections = append(parent.Sections, Section{
			Key: "source:summary", Role: "summary", Heading: "Summary", Text: text, Derived: true,
		})
	}
	return parent
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
