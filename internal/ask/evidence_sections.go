package ask

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
)

const rawEvidenceChunkSize = 2000

func itemEvidenceSections(item model.Item, excerpt string, maxChars int, terms []string) ([]retrieval.ContentSection, *retrieval.EvidenceChunk, string) {
	var sections []retrieval.ContentSection
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("summary_text", "derived", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryText, maxChars))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("x_post_text", "raw", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostText, maxChars))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("article_text", "raw", "", "", "", item.UpdatedAt, item.ArticleText, maxChars))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("text", "raw", "", "", "", item.UpdatedAt, item.Text, maxChars))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("ocr_text", "raw_ocr", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRText, maxChars))
	if !hasEnoughQueryCoverage([]string{item.SummaryText}, terms) {
		if chunk := evidenceChunkFromRaw(item.SourceKey, "raw_item_window", firstMatchingRawWindow(maxChars, terms, item.XPostText, item.ArticleText, item.Text, item.OCRText)); chunk != nil {
			return sections, chunk, chunk.Role
		}
	}
	if strings.TrimSpace(item.SummaryText) != "" {
		return sections, nil, "derived_summary"
	}
	if strings.TrimSpace(excerpt) != "" {
		return sections, nil, "excerpt"
	}
	return sections, nil, ""
}

func sourceEvidenceSections(source model.SourceDocument, excerpt string, maxChars int, terms []string) ([]retrieval.ContentSection, *retrieval.EvidenceChunk, string) {
	var sections []retrieval.ContentSection
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("summary_text", "derived", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryText, maxChars))
	retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("description", "metadata", "", "", "", source.UpdatedAt, source.Description, maxChars))
	if !hasEnoughQueryCoverage([]string{source.SummaryText, source.Description}, terms) {
		window := firstMatchingRawWindow(maxChars, terms, source.ExtractedText)
		if strings.TrimSpace(window.Text) != "" {
			retrieval.AppendUniqueContentSection(&sections, retrieval.NewContentSection("extracted_text_window", "raw", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, window.Text, maxChars))
			if chunk := evidenceChunkFromRaw(source.SourceKey, "raw_extract_window", window); chunk != nil {
				return sections, chunk, chunk.Role
			}
		}
	}
	if strings.TrimSpace(source.SummaryText) != "" {
		return sections, nil, "derived_summary"
	}
	if strings.TrimSpace(excerpt) != "" {
		return sections, nil, "excerpt"
	}
	return sections, nil, ""
}

type rawWindow struct {
	Text      string
	StartChar int
	EndChar   int
	Index     int
}

func firstMatchingRawWindow(maxChars int, terms []string, values ...string) rawWindow {
	for _, value := range values {
		window := rawEvidenceWindow(value, terms, maxChars)
		if strings.TrimSpace(window.Text) != "" && hasEnoughQueryCoverage([]string{window.Text}, terms) {
			return window
		}
	}
	for _, value := range values {
		window := rawEvidenceWindow(value, terms, maxChars)
		if strings.TrimSpace(window.Text) != "" {
			return window
		}
	}
	return rawWindow{}
}

func rawEvidenceWindow(raw string, terms []string, maxChars int) rawWindow {
	collapsed := collapseWhitespace(raw)
	if collapsed == "" {
		return rawWindow{}
	}
	text := evidenceExcerpt(maxChars, terms, collapsed)
	if text == "" {
		return rawWindow{}
	}
	needle := strings.TrimSpace(strings.Trim(text, ". "))
	start := strings.Index(collapsed, needle)
	if start < 0 {
		start = 0
	}
	end := start + len([]rune(needle))
	if end < start {
		end = start
	}
	return rawWindow{
		Text:      text,
		StartChar: start,
		EndChar:   end,
		Index:     start/rawEvidenceChunkSize + 1,
	}
}

func evidenceChunkFromRaw(parentSourceKey string, role string, window rawWindow) *retrieval.EvidenceChunk {
	text := strings.TrimSpace(window.Text)
	if text == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(text))
	return &retrieval.EvidenceChunk{
		ParentSourceKey: parentSourceKey,
		Index:           window.Index,
		StartChar:       window.StartChar,
		EndChar:         window.EndChar,
		Role:            role,
		Hash:            hex.EncodeToString(sum[:8]),
		Heading:         chunkHeading(text),
	}
}

func chunkHeading(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if i := strings.IndexByte(text, '.'); i > 0 && i <= 100 {
		return strings.TrimSpace(text[:i])
	}
	runes := []rune(text)
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return strings.TrimSpace(string(runes))
}
