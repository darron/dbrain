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
	appendSection := func(role, heading, text string, derived bool) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := role + "\x00" + text
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		parent.Sections = append(parent.Sections, Section{
			Role: role, Heading: strings.TrimSpace(heading), Text: text, Derived: derived,
		})
	}

	appendSection("raw", parent.Title, item.Text, false)
	appendSection("raw", parent.Title, item.XPostText, false)
	appendSection("ocr", "OCR", item.OCRText, false)
	if strings.TrimSpace(item.ArticleTitle) == model.XMediaTranscriptArticleTitle {
		appendSection("transcript", model.XMediaTranscriptArticleTitle, item.ArticleText, false)
	} else {
		appendSection("raw", item.ArticleTitle, item.ArticleText, false)
	}
	appendSection("summary", "Summary", item.SummaryText, true)
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
			Role: "raw", Heading: parent.Title, Text: text,
		})
	}
	if text := strings.TrimSpace(source.SummaryText); text != "" {
		parent.Sections = append(parent.Sections, Section{
			Role: "summary", Heading: "Summary", Text: text, Derived: true,
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
