package sourceenrich

import (
	"fmt"
	neturl "net/url"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

const minXArticlePreviewExtractChars = 300

func isShortXArticlePreviewExtract(extract model.ExtractResult) bool {
	return extract.Tool == "x-hydration" &&
		extract.ToolVersion == "local-article-preview-cache" &&
		len(strings.TrimSpace(extract.Content)) < minXArticlePreviewExtractChars
}

func shouldRetryRemoteAfterLocalExtractReject(source model.SourceDocument, extract model.ExtractResult, failure model.ExtractResult) bool {
	if !isXArticleURL(firstNonEmpty(source.CanonicalURL, extract.CanonicalURL, extract.FinalURL)) {
		return false
	}
	if failure.Status != model.SourceExtractStatusError {
		return false
	}
	if looksLikeXArticleErrorShell(extract.Content) {
		return false
	}
	return isShortXArticlePreviewExtract(extract)
}

func rejectExtractFailure(source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool) {
	if !isXArticleURL(firstNonEmpty(source.CanonicalURL, extract.CanonicalURL, extract.FinalURL)) {
		if looksLikeSubstackSubscriptionShell(extract.Content) {
			return model.ExtractResult{
				Status:      model.SourceExtractStatusEmpty,
				Error:       "substack returned subscription boilerplate instead of article content",
				Tool:        extract.Tool,
				ToolVersion: extract.ToolVersion,
			}, true
		}
		if looksLikeSubstackInboxNavigationShell(extract.Content) {
			return model.ExtractResult{
				Status:      model.SourceExtractStatusEmpty,
				Error:       "substack returned inbox/navigation chrome instead of article content",
				Tool:        extract.Tool,
				ToolVersion: extract.ToolVersion,
			}, true
		}
		return model.ExtractResult{}, false
	}
	if looksLikeXArticleErrorShell(extract.Content) {
		return model.ExtractResult{
			Status:      model.SourceExtractStatusError,
			Error:       "x article returned an X error shell instead of article content",
			Tool:        extract.Tool,
			ToolVersion: extract.ToolVersion,
		}, true
	}
	if isShortXArticlePreviewExtract(extract) {
		return model.ExtractResult{
			Status:      model.SourceExtractStatusError,
			Error:       fmt.Sprintf("x article hydration only exposed a short preview snippet (%d chars) instead of article content", len(strings.TrimSpace(extract.Content))),
			Tool:        extract.Tool,
			ToolVersion: extract.ToolVersion,
		}, true
	}
	return model.ExtractResult{}, false
}

func normalizeExtract(source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool) {
	cleaned := stripKnownPaywallNoise(extract.Content)
	if strings.TrimSpace(cleaned) == strings.TrimSpace(extract.Content) {
		return extract, false
	}
	normalized := extract
	normalized.Content = cleaned
	normalized.Status = extractStatusForContent(cleaned)
	return normalized, true
}

func stripKnownPaywallNoise(content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isKnownPaywallNoiseLine(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isKnownPaywallNoiseLine(line string) bool {
	value := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(value, "continue reading this post for free"):
		return true
	case strings.HasPrefix(value, "continue reading this post"):
		return true
	case strings.HasPrefix(value, "or purchase a paid subscription"):
		return true
	default:
		return false
	}
}

func looksLikeSubstackSubscriptionShell(content string) bool {
	value := strings.ToLower(strings.TrimSpace(content))
	if value == "" || len(value) > 600 {
		return false
	}
	return strings.Contains(value, "by subscribing, you agree substack's terms of use") &&
		strings.Contains(value, "information collection notice") &&
		strings.Contains(value, "privacy policy")
}

func looksLikeSubstackInboxNavigationShell(content string) bool {
	value := strings.ToLower(strings.TrimSpace(content))
	if value == "" || len(value) > 400 {
		return false
	}
	compact := compactAlphaNumeric(value)
	return strings.Contains(compact, "homesubscriptionschatactivityexploreprofilecreatealllistenpaidsavedhistorysortbypriorityrecentgetapp")
}

func compactAlphaNumeric(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func extractFromSource(source model.SourceDocument) model.ExtractResult {
	return model.ExtractResult{
		CanonicalURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        source.Title,
		Description:  source.Description,
		SiteName:     source.SiteName,
		Content:      source.ExtractedText,
		RawJSON:      source.ExtractJSON,
		Status:       source.ExtractStatus,
		Error:        source.ExtractError,
		FetchedAt:    source.ExtractedAt,
		Tool:         source.ExtractTool,
		ToolVersion:  source.ExtractToolVersion,
	}
}

func isXArticleURL(rawURL string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "x.com" && host != "www.x.com" && host != "twitter.com" && host != "www.twitter.com" {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(parsed.EscapedPath()))
	return strings.Contains(path, "/i/article/") || strings.Contains(path, "/article/")
}

func looksLikeXArticleErrorShell(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "something went wrong") &&
		strings.Contains(normalized, "privacy related extensions may cause issues on x.com")
}
