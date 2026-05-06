package sourceenrich

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type wordpressPostEnvelope struct {
	Link    string       `json:"link"`
	Title   renderedHTML `json:"title"`
	Excerpt renderedHTML `json:"excerpt"`
	Content renderedHTML `json:"content"`
	Yoast   struct {
		OGSiteName    string `json:"og_site_name"`
		OGDescription string `json:"og_description"`
	} `json:"yoast_head_json"`
}

type renderedHTML struct {
	Rendered string `json:"rendered"`
}

func wordpressJSONURL(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	for _, raw := range resp.Header.Values("Link") {
		for _, segment := range strings.Split(raw, ",") {
			trimmed := strings.TrimSpace(segment)
			lower := strings.ToLower(trimmed)
			if !strings.Contains(lower, "application/json") || !strings.Contains(lower, `rel="alternate"`) {
				continue
			}
			start := strings.Index(trimmed, "<")
			end := strings.Index(trimmed, ">")
			if start < 0 || end <= start {
				continue
			}
			resolved, err := resp.Request.URL.Parse(trimmed[start+1 : end])
			if err == nil {
				return resolved.String()
			}
		}
	}
	return ""
}

func fetchWordPressJSONExtract(ctx context.Context, client *http.Client, sourceURL string, jsonURL string) (model.ExtractResult, bool, error) {
	resp, body, err := fetchHTTPText(ctx, client, jsonURL)
	if err != nil {
		return model.ExtractResult{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("wordpress json status %d", resp.StatusCode)
	}

	var payload wordpressPostEnvelope
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("parse wordpress json: %w", err)
	}

	finalURL := firstNonEmpty(strings.TrimSpace(payload.Link), sourceURL)
	title := normalizeExtractedText(stripHTMLText(payload.Title.Rendered))
	description := firstNonEmpty(
		normalizeExtractedText(stripHTMLText(payload.Excerpt.Rendered)),
		strings.TrimSpace(payload.Yoast.OGDescription),
	)
	siteName := firstNonEmpty(strings.TrimSpace(payload.Yoast.OGSiteName), siteNameFromURL(finalURL))
	content := normalizeExtractedText(stripHTMLText(payload.Content.Rendered))

	return model.ExtractResult{
		CanonicalURL: sourceURL,
		FinalURL:     finalURL,
		Title:        title,
		Description:  description,
		SiteName:     siteName,
		Content:      content,
		RawJSON:      buildProtectedRawJSON("wordpress-json", sourceURL, finalURL, title, description, siteName, content, "sucuri"),
		Status:       extractStatusForContent(content),
		FetchedAt:    time.Now().UTC(),
		Tool:         protectedFetchToolName,
		ToolVersion:  protectedFetchToolVersion,
	}, true, nil
}
