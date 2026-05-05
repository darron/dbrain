package mcpserver

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func itemAvailableSections(item model.Item) []getSection {
	var sections []getSection
	appendUniqueSection(&sections, makeGetSection("summary_text", "derived", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryText, 0))
	appendUniqueSection(&sections, makeGetSection("x_post_text", "raw", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostText, 0))
	appendUniqueSection(&sections, makeGetSection("x_media_transcript", "raw_transcript", item.XMediaTranscriptStatus, "", "", item.XMediaTranscriptAt, itemMediaTranscriptText(item), 0))
	appendUniqueSection(&sections, makeGetSection("article_text", "raw", "", "", "", time.Time{}, nonTranscriptArticleText(item), 0))
	appendUniqueSection(&sections, makeGetSection("text", "raw", "", "", "", time.Time{}, item.Text, 0))
	appendUniqueSection(&sections, makeGetSection("ocr_text", "raw_ocr", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRText, 0))
	appendUniqueSection(&sections, makeGetSection("x_post_json", "raw_json", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostJSON, 0))
	appendUniqueSection(&sections, makeGetSection("ocr_json", "raw_json", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRJSON, 0))
	appendUniqueSection(&sections, makeGetSection("summary_json", "derived_json", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryJSON, 0))
	appendUniqueSection(&sections, makeGetSection("raw_json", "raw_json", "", "", "", time.Time{}, item.RawJSON, 0))
	return sections
}

func sourceAvailableSections(source model.SourceDocument) []getSection {
	var sections []getSection
	appendUniqueSection(&sections, makeGetSection("user_tags", "metadata", "", "", "", time.Time{}, source.UserTags, 0))
	appendUniqueSection(&sections, makeGetSection("summary_text", "derived", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryText, 0))
	appendUniqueSection(&sections, makeGetSection("extracted_text", "raw", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, source.ExtractedText, 0))
	appendUniqueSection(&sections, makeGetSection("description", "metadata", "", "", "", time.Time{}, source.Description, 0))
	appendUniqueSection(&sections, makeGetSection("extract_json", "raw_json", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, source.ExtractJSON, 0))
	appendUniqueSection(&sections, makeGetSection("summary_json", "derived_json", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryJSON, 0))
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
			section.Text, section.Truncated = truncateWithFlag(section.Text, maxChars)
		}
		appendUniqueSection(&sections, section)
	}
	return sections
}

func evidenceSectionText(section getSection, maxChars int, query string) (string, bool) {
	if strings.TrimSpace(query) == "" {
		return truncateWithFlag(section.Text, maxChars)
	}
	text, truncated, matched := queryWindowWithFlag(section.Text, query, maxChars)
	if matched {
		return text, truncated
	}
	return truncateWithFlag(section.Text, maxChars)
}

func makeGetSection(name, role, status, modelName, tool string, at time.Time, text string, maxChars int) getSection {
	trimmed := strings.TrimSpace(text)
	section := getSection{
		Name:  name,
		Role:  role,
		Chars: len([]rune(trimmed)),
	}
	if status = strings.TrimSpace(status); status != "" {
		section.Status = status
	}
	if modelName = strings.TrimSpace(modelName); modelName != "" {
		section.Model = modelName
	}
	if tool = strings.TrimSpace(tool); tool != "" {
		section.Tool = tool
	}
	if !at.IsZero() {
		section.At = at.UTC().Format(time.RFC3339)
	}
	if maxChars > 0 {
		section.Text, section.Truncated = truncateWithFlag(trimmed, maxChars)
	} else {
		section.Text = trimmed
	}
	return section
}

func appendUniqueSection(sections *[]getSection, section getSection) {
	if strings.TrimSpace(section.Text) == "" {
		return
	}
	for _, existing := range *sections {
		if existing.Text == section.Text {
			return
		}
	}
	*sections = append(*sections, section)
}

func sectionCatalog(sections []getSection) []getSection {
	catalog := make([]getSection, 0, len(sections))
	for _, section := range sections {
		section.Text = ""
		section.Truncated = false
		catalog = append(catalog, section)
	}
	return catalog
}

func truncateWithFlag(value string, maxChars int) (string, bool) {
	value = strings.TrimSpace(value)
	if maxChars <= 0 {
		return value, false
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value, false
	}
	return strings.TrimSpace(string(runes[:maxChars])), true
}
