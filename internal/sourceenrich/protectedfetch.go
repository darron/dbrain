package sourceenrich

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dbrain/internal/model"
)

const (
	protectedFetchToolName    = "http-fallback"
	protectedFetchToolVersion = "sucuri-js-v1"
	protectedFetchUserAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
	maxProtectedBodyBytes     = 8 << 20
)

var (
	sucuriEncodedScriptPattern = regexp.MustCompile(`\bS\s*=\s*(?:'([^']+)'|"([^"]+)")`)
	htmlMetaTagPattern         = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	htmlAttrPattern            = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	htmlTitlePattern           = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	htmlArticlePattern         = regexp.MustCompile(`(?is)<article\b[^>]*>(.*?)</article>`)
	htmlMainPattern            = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main>`)
	htmlBodyPattern            = regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)</body>`)
	htmlStripPattern           = regexp.MustCompile(`(?is)<[^>]+>`)
	htmlBreakPattern           = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>`)
	htmlBlockClosePattern      = regexp.MustCompile(`(?i)</\s*(p|div|li|ul|ol|article|section|main|h[1-6]|tr|blockquote)\s*>`)
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

func fallbackExtractForFetchError(ctx context.Context, source model.SourceDocument, opts Options, fetchErr error) (model.ExtractResult, bool, error) {
	if !isRedirectFetchError(fetchErr) {
		return model.ExtractResult{}, false, nil
	}

	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	if strings.TrimSpace(sourceURL) == "" {
		return model.ExtractResult{}, false, nil
	}

	timeout := 30 * time.Second
	if opts.Timeout > 0 && opts.Timeout < timeout {
		timeout = opts.Timeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return extractProtectedSource(fetchCtx, sourceURL)
}

func extractProtectedSource(ctx context.Context, rawURL string) (model.ExtractResult, bool, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("create cookie jar: %w", err)
	}

	client := &http.Client{Jar: jar}

	challengeResp, challengeBody, err := fetchHTTPText(ctx, client, rawURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("fetch protected source challenge: %w", err)
	}
	if !looksLikeSucuriChallenge(challengeResp, challengeBody) {
		return model.ExtractResult{}, false, nil
	}

	cookie, err := solveSucuriChallengeCookie(challengeBody)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("solve sucuri challenge: %w", err)
	}
	if challengeResp.Request == nil || challengeResp.Request.URL == nil {
		return model.ExtractResult{}, false, fmt.Errorf("sucuri challenge response missing request url")
	}
	jar.SetCookies(challengeResp.Request.URL, []*http.Cookie{cookie})

	protectedResp, protectedBody, err := fetchHTTPText(ctx, client, rawURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("refetch protected source: %w", err)
	}
	if protectedResp.StatusCode < 200 || protectedResp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("refetch protected source: unexpected status %d", protectedResp.StatusCode)
	}

	if wpJSONURL := wordpressJSONURL(protectedResp); wpJSONURL != "" {
		if extract, ok, err := fetchWordPressJSONExtract(ctx, client, rawURL, wpJSONURL); err == nil && ok {
			return extract, true, nil
		}
	}

	return extractHTMLProtectedSource(rawURL, protectedResp, protectedBody), true, nil
}

func fetchHTTPText(ctx context.Context, client *http.Client, rawURL string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("user-agent", protectedFetchUserAgent)
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("accept-language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProtectedBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read response body: %w", err)
	}
	return resp, string(body), nil
}

func looksLikeSucuriChallenge(resp *http.Response, body string) bool {
	if resp == nil {
		return false
	}
	value := strings.ToLower(body)
	server := strings.ToLower(resp.Header.Get("server"))
	return strings.Contains(server, "sucuri") &&
		strings.Contains(value, "javascript is required") &&
		strings.Contains(value, "sucuri_cloudproxy_js") &&
		strings.Contains(value, "e(r);")
}

func solveSucuriChallengeCookie(challengeHTML string) (*http.Cookie, error) {
	match := sucuriEncodedScriptPattern.FindStringSubmatch(challengeHTML)
	if len(match) != 3 {
		return nil, fmt.Errorf("sucuri challenge payload not found")
	}
	encoded := firstNonEmpty(match[1], match[2])
	if encoded == "" {
		return nil, fmt.Errorf("sucuri challenge payload empty")
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode sucuri payload: %w", err)
	}

	vars := map[string]string{}
	for _, stmt := range splitJSStatements(string(decoded)) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if strings.HasPrefix(stmt, "document.cookie") {
			index := strings.Index(stmt, "=")
			if index < 0 {
				continue
			}
			value, err := evalJSConcatExpression(strings.TrimSpace(stmt[index+1:]), vars)
			if err != nil {
				return nil, fmt.Errorf("evaluate sucuri cookie expression: %w", err)
			}
			cookie, err := parseDocumentCookie(value)
			if err != nil {
				return nil, err
			}
			return cookie, nil
		}

		index := strings.Index(stmt, "=")
		if index < 0 {
			continue
		}
		name := strings.TrimSpace(stmt[:index])
		name = strings.TrimPrefix(name, "var ")
		if name == "" || strings.Contains(name, ".") {
			continue
		}
		value, err := evalJSConcatExpression(strings.TrimSpace(stmt[index+1:]), vars)
		if err != nil {
			return nil, fmt.Errorf("evaluate sucuri variable %s: %w", name, err)
		}
		vars[name] = value
	}

	return nil, fmt.Errorf("sucuri cookie assignment not found")
}

func splitJSStatements(script string) []string {
	var (
		out     []string
		start   int
		quote   rune
		escaped bool
	)

	for i, r := range script {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ';' {
			out = append(out, script[start:i])
			start = i + 1
		}
	}
	if start <= len(script) {
		out = append(out, script[start:])
	}
	return out
}

func evalJSConcatExpression(expr string, vars map[string]string) (string, error) {
	var builder strings.Builder
	for _, token := range splitJSConcatTokens(expr) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		switch {
		case isQuotedJSToken(token):
			value, err := unquoteJSLiteral(token)
			if err != nil {
				return "", fmt.Errorf("unquote %s: %w", token, err)
			}
			builder.WriteString(value)
		case strings.HasPrefix(token, "String.fromCharCode(") && strings.HasSuffix(token, ")"):
			value, err := evalFromCharCode(token)
			if err != nil {
				return "", err
			}
			builder.WriteRune(rune(value))
		default:
			value, ok := vars[token]
			if !ok {
				return "", fmt.Errorf("unsupported token %q", token)
			}
			builder.WriteString(value)
		}
	}
	return builder.String(), nil
}

func splitJSConcatTokens(expr string) []string {
	var (
		out     []string
		start   int
		depth   int
		quote   rune
		escaped bool
	)

	for i, r := range expr {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}

		switch r {
		case '\'', '"':
			quote = r
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '+':
			if depth == 0 {
				out = append(out, expr[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, expr[start:])
	return out
}

func isQuotedJSToken(token string) bool {
	return len(token) >= 2 && ((token[0] == '\'' && token[len(token)-1] == '\'') || (token[0] == '"' && token[len(token)-1] == '"'))
}

func unquoteJSLiteral(token string) (string, error) {
	if !isQuotedJSToken(token) {
		return "", fmt.Errorf("not a quoted js literal")
	}

	quote := token[0]
	inner := token[1 : len(token)-1]
	var builder strings.Builder
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if ch != '\\' {
			builder.WriteByte(ch)
			continue
		}
		if i+1 >= len(inner) {
			return "", fmt.Errorf("unterminated escape")
		}
		i++
		switch inner[i] {
		case '\\', '"', '\'':
			builder.WriteByte(inner[i])
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case quote:
			builder.WriteByte(inner[i])
		default:
			builder.WriteByte(inner[i])
		}
	}
	return builder.String(), nil
}

func evalFromCharCode(token string) (int64, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(token, "String.fromCharCode("), ")")
	value = strings.TrimSpace(value)
	decoded, err := strconv.ParseInt(value, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("parse fromCharCode %q: %w", value, err)
	}
	return decoded, nil
}

func parseDocumentCookie(value string) (*http.Cookie, error) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Add("Set-Cookie", value)
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil, fmt.Errorf("parse sucuri cookie")
	}
	return cookies[0], nil
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
		RawJSON:      buildProtectedRawJSON("wordpress-json", sourceURL, finalURL, title, description, siteName, content),
		Status:       extractStatusForContent(content),
		FetchedAt:    time.Now().UTC(),
		Tool:         protectedFetchToolName,
		ToolVersion:  protectedFetchToolVersion,
	}, true, nil
}

func extractHTMLProtectedSource(sourceURL string, resp *http.Response, body string) model.ExtractResult {
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
		RawJSON:      buildProtectedRawJSON("sucuri-html", sourceURL, finalURL, title, description, siteName, content),
		Status:       extractStatusForContent(content),
		FetchedAt:    time.Now().UTC(),
		Tool:         protectedFetchToolName,
		ToolVersion:  protectedFetchToolVersion,
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
		return "empty"
	}
	return "ok"
}

func buildProtectedRawJSON(method string, sourceURL string, finalURL string, title string, description string, siteName string, content string) string {
	payload := protectedExtractEnvelope{
		Method:    method,
		URL:       sourceURL,
		FinalURL:  finalURL,
		Title:     title,
		SiteName:  siteName,
		Content:   content,
		Summary:   description,
		Challenge: "sucuri",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}
