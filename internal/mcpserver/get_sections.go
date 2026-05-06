package mcpserver

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
)

func itemAvailableSections(item model.Item) []getSection {
	var sections []getSection
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("summary_text", "derived", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryText, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("x_post_text", "raw", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostText, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("x_media_transcript", "raw_transcript", item.XMediaTranscriptStatus, "", "", item.XMediaTranscriptAt, itemMediaTranscriptText(item), 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("article_text", "raw", "", "", "", time.Time{}, nonTranscriptArticleText(item), 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("text", "raw", "", "", "", time.Time{}, item.Text, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("ocr_text", "raw_ocr", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRText, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("x_post_json", "raw_json", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostJSON, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("ocr_json", "raw_json", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRJSON, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("summary_json", "derived_json", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryJSON, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("raw_json", "raw_json", "", "", "", time.Time{}, item.RawJSON, 0))
	return sections
}

func sourceAvailableSections(source model.SourceDocument) []getSection {
	var sections []getSection
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("user_tags", "metadata", "", "", "", time.Time{}, source.UserTags, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("summary_text", "derived", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryText, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("extracted_text", "raw", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, source.ExtractedText, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("description", "metadata", "", "", "", time.Time{}, source.Description, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("extract_json", "raw_json", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, source.ExtractJSON, 0))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("summary_json", "derived_json", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryJSON, 0))
	return sections
}

func sectionsForMode(available []getSection, mode string, maxChars int, query string) []getSection {
	if mode == getModeBrief {
		return nil
	}
	var sections []getSection
	for _, section := range available {
		if mode == getModeEvidence && strings.Contains(section.Role, "json") {
			continue
		}
		if mode == getModeEvidence && section.Name == "raw_json" {
			continue
		}
		if mode == getModeRaw && strings.HasPrefix(section.Role, "derived") {
			continue
		}
		if mode == getModeRaw && strings.HasPrefix(section.Role, "related_") {
			continue
		}
		if mode == getModeEvidence {
			section.Text, section.Truncated = evidenceSectionText(section, maxChars, query)
		} else {
			section.Text, section.Truncated = retrieval.TruncateText(section.Text, maxChars)
		}
		retrieval.AppendUniqueContentSection(&sections, section)
	}
	return sections
}

func evidenceSectionText(section getSection, maxChars int, query string) (string, bool) {
	if strings.TrimSpace(query) == "" {
		return retrieval.TruncateText(section.Text, maxChars)
	}
	text, truncated, matched := queryWindowWithFlag(section.Text, query, maxChars)
	if matched {
		return text, truncated
	}
	return retrieval.TruncateText(section.Text, maxChars)
}
