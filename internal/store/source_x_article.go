package store

import (
	"encoding/json"
	"net/url"
	"strings"
)

type xArticlePreview struct {
	Title       string
	Content     string
	PreviewText string
	SummaryText string
	PlainText   string
	RestID      string
	HasFullText bool
}

func parseXArticlePreview(rawJSON string, canonicalURL string) (xArticlePreview, bool) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return xArticlePreview{}, false
	}

	expectedRestID := xArticleRestIDFromURL(canonicalURL)
	if expectedRestID == "" {
		return xArticlePreview{}, false
	}

	var payload any
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		return xArticlePreview{}, false
	}

	return findXArticlePreview(payload, expectedRestID)
}

func findXArticlePreview(value any, expectedRestID string) (xArticlePreview, bool) {
	best := xArticlePreview{}
	bestScore := 0

	switch current := value.(type) {
	case map[string]any:
		restID, _ := current["rest_id"].(string)
		if strings.TrimSpace(restID) == expectedRestID {
			title, _ := current["title"].(string)
			previewText, _ := current["preview_text"].(string)
			summaryText, _ := current["summary_text"].(string)
			plainText, _ := current["plain_text"].(string)
			contentStateText := extractXArticleContentState(current["content_state"])
			content := firstNonEmpty(
				strings.TrimSpace(plainText),
				contentStateText,
				strings.TrimSpace(summaryText),
				strings.TrimSpace(previewText),
			)
			candidate := xArticlePreview{
				Title:       strings.TrimSpace(title),
				Content:     content,
				PreviewText: strings.TrimSpace(previewText),
				SummaryText: strings.TrimSpace(summaryText),
				PlainText:   strings.TrimSpace(plainText),
				RestID:      strings.TrimSpace(restID),
				HasFullText: strings.TrimSpace(plainText) != "" || contentStateText != "",
			}
			if score := xArticlePreviewScore(candidate); score > bestScore {
				best = candidate
				bestScore = score
			}
		}
		for _, child := range current {
			if preview, ok := findXArticlePreview(child, expectedRestID); ok {
				if score := xArticlePreviewScore(preview); score > bestScore {
					best = preview
					bestScore = score
				}
			}
		}
	case []any:
		for _, child := range current {
			if preview, ok := findXArticlePreview(child, expectedRestID); ok {
				if score := xArticlePreviewScore(preview); score > bestScore {
					best = preview
					bestScore = score
				}
			}
		}
	}

	if bestScore == 0 || strings.TrimSpace(best.Content) == "" {
		return xArticlePreview{}, false
	}
	return best, true
}

func xArticlePreviewScore(preview xArticlePreview) int {
	contentLen := len(strings.TrimSpace(preview.Content))
	switch {
	case preview.HasFullText:
		return 100000 + contentLen
	case strings.TrimSpace(preview.SummaryText) != "":
		return 10000 + contentLen
	case strings.TrimSpace(preview.PreviewText) != "":
		return 1000 + contentLen
	default:
		return 0
	}
}

func extractXArticleContentState(value any) string {
	state, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	blocks, ok := state["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		return ""
	}

	parts := make([]string, 0, len(blocks))
	for _, blockValue := range blocks {
		block, ok := blockValue.(map[string]any)
		if !ok {
			continue
		}
		text, _ := block["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func xArticleRestIDFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "article" {
			return strings.TrimSpace(parts[i+1])
		}
	}
	return ""
}

func buildXArticlePublicURL(authorHandle string, restID string) string {
	authorHandle = strings.TrimSpace(strings.TrimPrefix(authorHandle, "@"))
	restID = strings.TrimSpace(restID)
	if authorHandle == "" || restID == "" {
		return ""
	}
	return "https://x.com/" + authorHandle + "/article/" + restID
}
