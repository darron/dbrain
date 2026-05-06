package sourceenrich

import (
	"encoding/json"
	"html"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

var (
	htmlMetaTagPattern    = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	htmlAttrPattern       = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	htmlTitlePattern      = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	htmlArticlePattern    = regexp.MustCompile(`(?is)<article\b[^>]*>(.*?)</article>`)
	htmlMainPattern       = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main>`)
	htmlBodyPattern       = regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)</body>`)
	htmlStripPattern      = regexp.MustCompile(`(?is)<[^>]+>`)
	htmlBreakPattern      = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>`)
	htmlBlockClosePattern = regexp.MustCompile(`(?i)</\s*(p|div|li|ul|ol|article|section|main|h[1-6]|tr|blockquote)\s*>`)
)

type protectedExtractEnvelope struct {
	Method    string `json:"method"`
	URL       string `json:"url"`
	FinalURL  string `json:"final_url"`
	Title     string `json:"title"`
	SiteName  string `json:"site_name"`
	Content   string `json:"content"`
	Summary   string `json:"summary,omitempty"`
	Challenge string `json:"challenge,omitempty"`
}

func extractHTMLSource(sourceURL string, resp *http.Response, body string, method string, toolVersion string, challenge string) model.ExtractResult {
	finalURL := sourceURL
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	region := firstNonEmpty(
		extractRegion(body, htmlArticlePattern),
		extractRegion(body, htmlMainPattern),
		extractRegion(body, htmlBodyPattern),
		body,
	)

	title := firstNonEmpty(
		extractMetaContent(body, "property", "og:title"),
		extractMetaContent(body, "name", "twitter:title"),
		normalizeExtractedText(stripHTMLText(extractTagText(body, htmlTitlePattern))),
	)
	description := firstNonEmpty(
		extractMetaContent(body, "name", "description"),
		extractMetaContent(body, "property", "og:description"),
	)
	siteName := firstNonEmpty(
		extractMetaContent(body, "property", "og:site_name"),
		siteNameFromURL(finalURL),
	)
	content := normalizeExtractedText(stripHTMLText(region))
	if content == "" {
		content = normalizeExtractedText(stripHTMLText(body))
	}

	return model.ExtractResult{
		CanonicalURL: sourceURL,
		FinalURL:     finalURL,
		Title:        title,
		Description:  description,
		SiteName:     siteName,
		Content:      content,
		RawJSON:      buildProtectedRawJSON(method, sourceURL, finalURL, title, description, siteName, content, challenge),
		Status:       extractStatusForContent(content),
		FetchedAt:    time.Now().UTC(),
		Tool:         protectedFetchToolName,
		ToolVersion:  toolVersion,
	}
}

func extractRegion(body string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func extractTagText(body string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func extractMetaContent(body string, attrName string, attrValue string) string {
	for _, tag := range htmlMetaTagPattern.FindAllString(body, -1) {
		attrs := parseHTMLAttributes(tag)
		if !strings.EqualFold(strings.TrimSpace(attrs[attrName]), attrValue) {
			continue
		}
		content := strings.TrimSpace(attrs["content"])
		if content != "" {
			return normalizeExtractedText(stripHTMLText(content))
		}
	}
	return ""
}

func parseHTMLAttributes(tag string) map[string]string {
	attrs := map[string]string{}
	for _, match := range htmlAttrPattern.FindAllStringSubmatch(tag, -1) {
		if len(match) != 4 {
			continue
		}
		attrs[strings.ToLower(strings.TrimSpace(match[1]))] = html.UnescapeString(firstNonEmpty(match[2], match[3]))
	}
	return attrs
}

func stripHTMLText(value string) string {
	trimmed := value
	for _, tag := range []string{"script", "style", "noscript", "svg", "header", "footer", "nav", "aside", "form"} {
		pattern := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `>`)
		trimmed = pattern.ReplaceAllString(trimmed, " ")
	}
	trimmed = htmlBreakPattern.ReplaceAllString(trimmed, "\n")
	trimmed = htmlBlockClosePattern.ReplaceAllString(trimmed, "$0\n")
	trimmed = htmlStripPattern.ReplaceAllString(trimmed, " ")
	return html.UnescapeString(trimmed)
}

func normalizeExtractedText(value string) string {
	lines := strings.Split(value, "\n")
	cleaned := make([]string, 0, len(lines))
	lastBlank := true
	for _, line := range lines {
		line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		if line == "" {
			if !lastBlank {
				cleaned = append(cleaned, "")
			}
			lastBlank = true
			continue
		}
		cleaned = append(cleaned, line)
		lastBlank = false
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func siteNameFromURL(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host
}

func extractStatusForContent(content string) string {
	if strings.TrimSpace(content) == "" {
		return model.SourceExtractStatusEmpty
	}
	return model.SourceExtractStatusOK
}

func buildProtectedRawJSON(method string, sourceURL string, finalURL string, title string, description string, siteName string, content string, challenge string) string {
	payload := protectedExtractEnvelope{
		Method:    method,
		URL:       sourceURL,
		FinalURL:  finalURL,
		Title:     title,
		SiteName:  siteName,
		Content:   content,
		Summary:   description,
		Challenge: challenge,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}
