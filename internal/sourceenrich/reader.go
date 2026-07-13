package sourceenrich

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func extractHTTPReadableSource(ctx context.Context, rawURL string, options ...Options) (model.ExtractResult, bool, error) {
	client := newPublicHTTPClient(options...)
	resp, body, err := fetchHTTPText(ctx, client, rawURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("fetch readable source: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("fetch readable source: unexpected status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("content-type"))
	if !strings.Contains(contentType, "html") && !strings.Contains(strings.ToLower(body[:min(len(body), 512)]), "<html") {
		return extractPlainTextSource(rawURL, resp, body, "http-text", httpReaderToolVersion), true, nil
	}
	return extractHTMLSource(rawURL, resp, body, "http-html", httpReaderToolVersion, ""), true, nil
}

func extractConfiguredHTTPReaderSource(ctx context.Context, source model.SourceDocument, opts Options) (model.ExtractResult, bool, error) {
	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	readerURL := buildHTTPReaderURL(opts.HTTPReaderBaseURL, sourceURL)
	if readerURL == "" {
		return model.ExtractResult{}, false, nil
	}

	timeout := 30 * time.Second
	if opts.Timeout > 0 && opts.Timeout < timeout {
		timeout = opts.Timeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return extractHTTPReaderSource(fetchCtx, newConfiguredOriginHTTPClient(opts.HTTPReaderBaseURL, opts), sourceURL, readerURL)
}

func extractHTTPReaderSource(ctx context.Context, client *http.Client, sourceURL string, readerURL string) (model.ExtractResult, bool, error) {
	resp, body, err := fetchHTTPReaderText(ctx, client, readerURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("fetch reader source: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("fetch reader source: unexpected status %d", resp.StatusCode)
	}

	snippetLen := len(body)
	if snippetLen > 512 {
		snippetLen = 512
	}
	contentType := strings.ToLower(resp.Header.Get("content-type"))
	if strings.Contains(contentType, "html") || strings.Contains(strings.ToLower(body[:snippetLen]), "<html") {
		return extractHTMLSource(sourceURL, resp, body, "reader-html", httpReaderToolVersion, ""), true, nil
	}

	return extractPlainTextSource(sourceURL, resp, body, "reader-text", httpReaderToolVersion), true, nil
}

func extractPlainTextSource(sourceURL string, resp *http.Response, body string, method string, toolVersion string) model.ExtractResult {
	finalURL := sourceURL
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	content := normalizeExtractedText(body)
	title := firstMarkdownTitle(content)
	siteName := siteNameFromURL(finalURL)
	return model.ExtractResult{
		CanonicalURL: sourceURL,
		FinalURL:     finalURL,
		Title:        title,
		SiteName:     siteName,
		Content:      content,
		RawJSON:      buildProtectedRawJSON(method, sourceURL, finalURL, title, "", siteName, content, ""),
		Status:       extractStatusForContent(content),
		FetchedAt:    time.Now().UTC(),
		Tool:         protectedFetchToolName,
		ToolVersion:  toolVersion,
	}
}

func extractKnownReaderDomainSource(ctx context.Context, source model.SourceDocument, opts Options) (model.ExtractResult, bool, error) {
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

	readerExtract, recovered, readerErr := extractConfiguredHTTPReaderSource(fetchCtx, source, opts)
	if readerErr == nil && recovered {
		return readerExtract, true, nil
	}

	directExtract, directRecovered, directErr := extractHTTPReadableSource(fetchCtx, sourceURL, opts)
	if directErr == nil && directRecovered {
		return directExtract, true, readerErr
	}
	if readerErr != nil && directErr != nil {
		return model.ExtractResult{}, false, fmt.Errorf("reader fetch failed: %v; direct fetch failed: %w", readerErr, directErr)
	}
	if readerErr != nil {
		return model.ExtractResult{}, false, readerErr
	}
	if directErr != nil {
		return model.ExtractResult{}, false, directErr
	}
	return model.ExtractResult{}, false, nil
}

func firstMarkdownTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if title, ok := strings.CutPrefix(line, "Title:"); ok {
			return strings.TrimSpace(title)
		}
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
		if line != "" {
			return ""
		}
	}
	return ""
}

func isHTTPReaderFallbackCandidate(source model.SourceDocument, opts Options, fetchErr error) bool {
	if !isKilledOrTimeoutExtractError(fetchErr) {
		return false
	}
	return sourceMatchesHTTPReaderFallbackDomain(source, opts)
}

func sourceMatchesHTTPReaderFallbackDomain(source model.SourceDocument, opts Options) bool {
	host, ok := sourceHost(firstNonEmpty(source.CanonicalURL, source.NormalizedURL))
	if !ok {
		return false
	}
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	for _, domain := range opts.HTTPReaderFallbackDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, "www.")
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func buildHTTPReaderURL(baseURL string, sourceURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	sourceURL = strings.TrimSpace(sourceURL)
	if baseURL == "" || sourceURL == "" {
		return ""
	}
	if strings.Contains(baseURL, "{url}") {
		return strings.ReplaceAll(baseURL, "{url}", sourceURL)
	}
	if strings.Contains(baseURL, "{escaped_url}") {
		return strings.ReplaceAll(baseURL, "{escaped_url}", neturl.QueryEscape(sourceURL))
	}
	return strings.TrimRight(baseURL, "/") + "/" + sourceURL
}

func isKilledOrTimeoutExtractError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "signal: killed") ||
		strings.Contains(value, "context deadline exceeded") ||
		strings.Contains(value, "timeout") ||
		strings.Contains(value, "timed out")
}
