package store

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

type itemSummaryFields struct {
	Text          string
	JSON          string
	Status        string
	Error         string
	Model         string
	PromptVersion string
	Tool          string
	ToolVersion   string
	InputHash     string
	At            string
}

type itemOCRFields struct {
	Text        string
	JSON        string
	Status      string
	Error       string
	Model       string
	Tool        string
	ToolVersion string
	InputHash   string
	At          string
}

func preserveExistingItemEnrichmentFields(item *model.Item, existingArticleTitle string, existingArticleText string, existingSummary itemSummaryFields, existingOCR itemOCRFields) bool {
	if item == nil {
		return false
	}

	changed := false
	if strings.TrimSpace(item.ArticleText) == "" && strings.TrimSpace(existingArticleText) != "" {
		item.ArticleText = existingArticleText
		changed = true
	}
	if strings.TrimSpace(item.ArticleTitle) == "" && strings.TrimSpace(existingArticleTitle) != "" {
		item.ArticleTitle = existingArticleTitle
		changed = true
	}
	if strings.TrimSpace(item.SummaryText) == "" && strings.TrimSpace(existingSummary.Text) != "" {
		item.SummaryText = existingSummary.Text
		changed = true
	}
	if strings.TrimSpace(item.SummaryJSON) == "" && strings.TrimSpace(existingSummary.JSON) != "" {
		item.SummaryJSON = existingSummary.JSON
		changed = true
	}
	if strings.TrimSpace(item.SummaryStatus) == "" && strings.TrimSpace(existingSummary.Status) != "" {
		item.SummaryStatus = existingSummary.Status
		changed = true
	}
	if strings.TrimSpace(item.SummaryError) == "" && strings.TrimSpace(existingSummary.Error) != "" {
		item.SummaryError = existingSummary.Error
		changed = true
	}
	if strings.TrimSpace(item.SummaryModel) == "" && strings.TrimSpace(existingSummary.Model) != "" {
		item.SummaryModel = existingSummary.Model
		changed = true
	}
	if strings.TrimSpace(item.SummaryPromptVersion) == "" && strings.TrimSpace(existingSummary.PromptVersion) != "" {
		item.SummaryPromptVersion = existingSummary.PromptVersion
		changed = true
	}
	if strings.TrimSpace(item.SummaryTool) == "" && strings.TrimSpace(existingSummary.Tool) != "" {
		item.SummaryTool = existingSummary.Tool
		changed = true
	}
	if strings.TrimSpace(item.SummaryToolVersion) == "" && strings.TrimSpace(existingSummary.ToolVersion) != "" {
		item.SummaryToolVersion = existingSummary.ToolVersion
		changed = true
	}
	if strings.TrimSpace(item.SummaryInputHash) == "" && strings.TrimSpace(existingSummary.InputHash) != "" {
		item.SummaryInputHash = existingSummary.InputHash
		changed = true
	}
	if item.SummarizedAt.IsZero() && strings.TrimSpace(existingSummary.At) != "" {
		item.SummarizedAt = parseStoredTime(existingSummary.At)
		changed = true
	}
	if strings.TrimSpace(item.OCRText) == "" && strings.TrimSpace(existingOCR.Text) != "" {
		item.OCRText = existingOCR.Text
		changed = true
	}
	if strings.TrimSpace(item.OCRJSON) == "" && strings.TrimSpace(existingOCR.JSON) != "" {
		item.OCRJSON = existingOCR.JSON
		changed = true
	}
	if strings.TrimSpace(item.OCRStatus) == "" && strings.TrimSpace(existingOCR.Status) != "" {
		item.OCRStatus = existingOCR.Status
		changed = true
	}
	if strings.TrimSpace(item.OCRError) == "" && strings.TrimSpace(existingOCR.Error) != "" {
		item.OCRError = existingOCR.Error
		changed = true
	}
	if strings.TrimSpace(item.OCRModel) == "" && strings.TrimSpace(existingOCR.Model) != "" {
		item.OCRModel = existingOCR.Model
		changed = true
	}
	if strings.TrimSpace(item.OCRTool) == "" && strings.TrimSpace(existingOCR.Tool) != "" {
		item.OCRTool = existingOCR.Tool
		changed = true
	}
	if strings.TrimSpace(item.OCRToolVersion) == "" && strings.TrimSpace(existingOCR.ToolVersion) != "" {
		item.OCRToolVersion = existingOCR.ToolVersion
		changed = true
	}
	if strings.TrimSpace(item.OCRInputHash) == "" && strings.TrimSpace(existingOCR.InputHash) != "" {
		item.OCRInputHash = existingOCR.InputHash
		changed = true
	}
	if item.OCRAt.IsZero() && strings.TrimSpace(existingOCR.At) != "" {
		item.OCRAt = parseStoredTime(existingOCR.At)
		changed = true
	}
	return changed
}
