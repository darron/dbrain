package store

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func nextExtractFailureState(current model.SourceDocument, status string, errorText string, now time.Time) (string, int, string, string) {
	if !isExtractFailureStatus(status) {
		return "", 0, "", ""
	}

	kind := classifyStoredExtractFailureKind(status, errorText)
	if kind == "" {
		kind = "unknown"
	}

	count := 1
	firstFailedAt := now.UTC().Format(time.RFC3339)
	if isExtractFailureStatus(current.ExtractStatus) && current.ExtractFailureCount > 0 {
		count = current.ExtractFailureCount + 1
		if !current.ExtractFirstFailedAt.IsZero() {
			firstFailedAt = current.ExtractFirstFailedAt.UTC().Format(time.RFC3339)
		}
	}

	return kind, count, firstFailedAt, now.UTC().Format(time.RFC3339)
}

func classifyStoredExtractFailureKind(status string, errorText string) string {
	value := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case strings.TrimSpace(status) == "gone":
		return "http_gone"
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

func storedTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
