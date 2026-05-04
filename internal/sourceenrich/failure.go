package sourceenrich

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

func saveSourceFailure(ctx context.Context, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, extractToolVersion string, summaryToolVersion string) error {
	if extract.Status == "error" && isTerminalExtractStatus(source.ExtractStatus) {
		extract.Status = source.ExtractStatus
	}
	if strings.TrimSpace(extract.Tool) == "" {
		extract.Tool = summarizecli.ToolName
	}
	if strings.TrimSpace(extract.ToolVersion) == "" {
		extract.ToolVersion = extractToolVersion
	}
	if _, err := st.SaveSourceExtraction(ctx, source.ID, extract, source.ContentHash); err != nil {
		return err
	}
	if !opts.Summarize {
		return nil
	}

	summaryStatus := "error"
	if extract.Status != "error" {
		summaryStatus = "skipped"
	}
	_, err := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
		Status:        summaryStatus,
		Error:         extract.Error,
		Model:         opts.Model,
		PromptVersion: SummaryPromptVersion,
		Tool:          summarizecli.SummaryToolName(opts.Model),
		ToolVersion:   summaryToolVersion,
	})
	return err
}

func isTerminalExtractStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "dead", "gone":
		return true
	default:
		return false
	}
}

func preflightTerminalSourceFailure(ctx context.Context, source model.SourceDocument, opts Options, toolVersion string) (model.ExtractResult, bool) {
	host, ok := sourceHost(source.CanonicalURL)
	if !ok {
		return model.ExtractResult{}, false
	}

	resolveHost := opts.ResolveHost
	if resolveHost == nil {
		resolveHost = defaultResolveHost
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := resolveHost(lookupCtx, host); err != nil {
		if isHostNotFoundError(err) {
			return model.ExtractResult{
				Status:      "dead",
				Error:       fmt.Sprintf("host does not resolve: %s", host),
				Tool:        summarizecli.ToolName,
				ToolVersion: toolVersion,
			}, true
		}
	}

	return model.ExtractResult{}, false
}

func sourceHost(rawURL string) (string, bool) {
	if strings.TrimSpace(rawURL) == "" {
		return "", false
	}
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", false
	}
	return host, true
}

func defaultResolveHost(ctx context.Context, host string) error {
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err
}

func defaultResolveRedirectURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create redirect resolution request: %w", err)
	}
	req.Header.Set("user-agent", "dbrain/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve redirect: %w", err)
	}
	defer func() {
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		_ = resp.Body.Close()
	}()

	if resp.Request == nil || resp.Request.URL == nil {
		return rawURL, nil
	}
	return resp.Request.URL.String(), nil
}

func isHostNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "no such host") || strings.Contains(value, "nxdomain")
}

func isRedirectFetchError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, status := range []string{"status 301", "status 302", "status 303", "status 307", "status 308"} {
		if strings.Contains(value, status) {
			return true
		}
	}
	return false
}

func classifyTerminalExtractError(source model.SourceDocument, err error) (string, string, bool) {
	if err == nil {
		return "", "", false
	}
	errorText := strings.TrimSpace(err.Error())
	value := strings.ToLower(errorText)
	switch {
	case strings.Contains(value, "status 404"),
		strings.Contains(value, "404 not found"),
		strings.Contains(value, "status 410"),
		strings.Contains(value, "410 gone"):
		return "gone", "", true
	default:
		kind := classifyExtractFailureKind(errorText)
		threshold := deadThresholdForFailureKind(kind)
		if threshold <= 0 {
			return "", "", false
		}
		nextCount := nextFailureCount(source, kind)
		if nextCount < threshold {
			return "", "", false
		}
		return "dead", fmt.Sprintf("marking source dead after %d consecutive %s failures: %s", nextCount, failureKindLabel(kind), errorText), true
	}
}

func classifyExtractFailureKind(errorText string) string {
	value := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case strings.Contains(value, "host does not resolve"),
		strings.Contains(value, "no such host"),
		strings.Contains(value, "nxdomain"):
		return "dns_nxdomain"
	case strings.Contains(value, "self signed certificate"),
		strings.Contains(value, "unable to verify the first certificate"),
		strings.Contains(value, "err_tls_cert_altname_invalid"),
		strings.Contains(value, "altname invalid"),
		strings.Contains(value, "x509"),
		strings.Contains(value, "certificate"):
		return "tls_certificate"
	case strings.Contains(value, "status 522"),
		strings.Contains(value, "status 523"),
		strings.Contains(value, "status 524"),
		strings.Contains(value, "status 525"),
		strings.Contains(value, "status 526"):
		return "cloudflare_edge"
	case strings.Contains(value, "x article returned an x error shell"):
		return "x_article_shell"
	case strings.Contains(value, "status 401"),
		strings.Contains(value, "401 unauthorized"),
		strings.Contains(value, "status 403"),
		strings.Contains(value, "403 forbidden"),
		strings.Contains(value, "status 451"),
		strings.Contains(value, "451 unavailable"):
		return "http_access_denied"
	case strings.Contains(value, "unsupported file type"):
		return "unsupported_file"
	case strings.Contains(value, "signal: killed"),
		strings.Contains(value, "context deadline exceeded"),
		strings.Contains(value, "timeout"),
		strings.Contains(value, "timed out"):
		return "timeout"
	case strings.Contains(value, "fetch failed"):
		return "fetch_failed"
	case strings.Contains(value, "unable to connect"),
		strings.Contains(value, "connection refused"),
		strings.Contains(value, "network is unreachable"),
		strings.Contains(value, "no route to host"):
		return "connectivity"
	case strings.Contains(value, "status 502"),
		strings.Contains(value, "status 503"),
		strings.Contains(value, "status 504"):
		return "http_5xx"
	default:
		return ""
	}
}

func deadThresholdForFailureKind(kind string) int {
	switch kind {
	case "", "unknown":
		return 5
	case "dns_nxdomain":
		return 1
	case "tls_certificate", "cloudflare_edge", "connectivity":
		return 3
	case "x_article_shell":
		return 3
	case "http_access_denied":
		return 3
	case "unsupported_file":
		return 1
	case "timeout":
		return 3
	case "fetch_failed":
		return 5
	case "http_5xx":
		return 5
	default:
		return 0
	}
}

func nextFailureCount(source model.SourceDocument, kind string) int {
	if kind == "" {
		kind = "unknown"
	}
	storedKind := strings.TrimSpace(source.ExtractFailureKind)
	if source.ExtractFailureCount <= 0 {
		return 1
	}
	if storedKind == kind {
		return source.ExtractFailureCount + 1
	}
	if storedKind == "" || storedKind == "unknown" {
		return source.ExtractFailureCount + 1
	}
	return 1
}

func failureKindLabel(kind string) string {
	switch kind {
	case "dns_nxdomain":
		return "dns resolution"
	case "tls_certificate":
		return "tls certificate"
	case "cloudflare_edge":
		return "cloudflare edge"
	case "connectivity":
		return "connectivity"
	case "x_article_shell":
		return "x article shell"
	case "http_access_denied":
		return "http access denied"
	case "unsupported_file":
		return "unsupported file"
	case "timeout":
		return "timeout"
	case "fetch_failed":
		return "fetch"
	case "http_5xx":
		return "http 5xx"
	case "", "unknown":
		return "unclassified"
	default:
		return "terminal"
	}
}
