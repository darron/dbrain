package itemcategorize

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

const maxLinkedSourcesForItemCategorization = 3

func buildContentBundle(item model.Item) string {
	var sb strings.Builder

	// Metadata header
	sb.WriteString("source_type: " + item.SourceType + "\n")
	if item.AuthorHandle != "" {
		handle := "@" + item.AuthorHandle
		if item.AuthorName != "" {
			handle += " (" + item.AuthorName + ")"
		}
		sb.WriteString("author: " + handle + "\n")
	}
	if item.PublishedAt != "" {
		sb.WriteString("published: " + item.PublishedAt + "\n")
	}
	if lang := strings.TrimSpace(coalesce(item.Language, item.XPostLang)); lang != "" {
		sb.WriteString("language: " + lang + "\n")
	}
	if item.Title != "" {
		sb.WriteString("title: " + item.Title + "\n")
	}
	sb.WriteString("\n")

	// Primary text
	if postText := strings.TrimSpace(item.XPostText); postText != "" {
		sb.WriteString("Post:\n" + postText + "\n\n")
	} else if text := strings.TrimSpace(item.Text); text != "" {
		sb.WriteString("Text:\n" + text + "\n\n")
	}

	// Summary
	if summary := strings.TrimSpace(item.SummaryText); summary != "" {
		sb.WriteString("Summary:\n" + summary + "\n\n")
	}

	// Transcript or article body
	if articleText := strings.TrimSpace(item.ArticleText); articleText != "" {
		if item.ArticleTitle == "X Media Transcript" {
			if t := extractTranscriptText(articleText); t != "" {
				if len(t) > maxTranscriptChars {
					t = t[:maxTranscriptChars] + "…"
				}
				sb.WriteString("Transcript:\n" + t + "\n\n")
			}
		} else {
			body := articleText
			if len(body) > maxArticleChars {
				body = body[:maxArticleChars] + "…"
			}
			label := "Article"
			if item.ArticleTitle != "" {
				label = item.ArticleTitle
			}
			sb.WriteString(label + ":\n" + body + "\n\n")
		}
	}

	// OCR text (already extracted from images)
	if ocrText := strings.TrimSpace(item.OCRText); ocrText != "" {
		sb.WriteString("Image text (OCR):\n" + ocrText + "\n\n")
	}

	return strings.TrimSpace(sb.String())
}

func buildContentBundleWithSources(item model.Item, sources []model.SourceDocument) string {
	bundle := buildContentBundle(item)
	if len(sources) == 0 {
		return bundle
	}

	var linked strings.Builder
	included := 0
	for _, source := range sources {
		if included >= maxLinkedSourcesForItemCategorization {
			break
		}
		if !sourceHasCategorizationEvidence(source) {
			continue
		}
		sourceBundle := buildSourceContentBundle(source)
		if sourceBundle == "" {
			continue
		}
		if linked.Len() == 0 {
			linked.WriteString("Linked source evidence:")
		}
		linked.WriteString("\n\n---\n")
		linked.WriteString(sourceBundle)
		included++
	}
	if linked.Len() == 0 {
		return bundle
	}
	if bundle == "" {
		return linked.String()
	}
	return bundle + "\n\n" + linked.String()
}

func sourceHasCategorizationEvidence(source model.SourceDocument) bool {
	return strings.TrimSpace(source.SummaryText) != "" ||
		strings.TrimSpace(source.ExtractedText) != "" ||
		strings.TrimSpace(source.Description) != ""
}

func buildSourceContentBundle(source model.SourceDocument) string {
	var sb strings.Builder

	sb.WriteString("record_kind: source\n")
	sb.WriteString("source_type: " + source.SourceType + "\n")
	if source.Domain != "" {
		sb.WriteString("domain: " + source.Domain + "\n")
	}
	if source.SiteName != "" {
		sb.WriteString("site_name: " + source.SiteName + "\n")
	}
	if source.CanonicalURL != "" {
		sb.WriteString("url: " + source.CanonicalURL + "\n")
	}
	if source.Title != "" {
		sb.WriteString("title: " + source.Title + "\n")
	}
	sb.WriteString("\n")

	if description := strings.TrimSpace(source.Description); description != "" {
		sb.WriteString("Description:\n" + description + "\n\n")
	}
	if summary := strings.TrimSpace(source.SummaryText); summary != "" {
		sb.WriteString("Summary:\n" + summary + "\n\n")
	}
	if extracted := strings.TrimSpace(source.ExtractedText); extracted != "" {
		if len(extracted) > maxArticleChars {
			extracted = extracted[:maxArticleChars] + "…"
		}
		sb.WriteString("Extracted text:\n" + extracted + "\n\n")
	}

	return strings.TrimSpace(sb.String())
}

func extractTranscriptText(raw string) string {
	parts := strings.Split(raw, "\nTranscript:\n")
	if len(parts) <= 1 {
		return strings.TrimSpace(raw)
	}
	segments := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		if t := strings.TrimSpace(p); t != "" {
			segments = append(segments, t)
		}
	}
	return strings.Join(segments, "\n\n")
}
