package sourceenrich

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

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
