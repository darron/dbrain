package sourceenrich

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
)

func classifyTerminalExtractError(source model.SourceDocument, err error) (string, string, bool) {
	if err == nil {
		return "", "", false
	}
	errorText := strings.TrimSpace(err.Error())
	if safehttp.IsPolicyError(err) || strings.Contains(strings.ToLower(errorText), "safe http policy:") {
		return model.SourceExtractStatusDead, errorText, true
	}
	value := strings.ToLower(errorText)
	switch {
	case strings.Contains(value, "status 404"),
		strings.Contains(value, "404 not found"),
		strings.Contains(value, "status 410"),
		strings.Contains(value, "410 gone"):
		return model.SourceExtractStatusGone, "", true
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
		return model.SourceExtractStatusDead, fmt.Sprintf("marking source dead after %d consecutive %s failures: %s", nextCount, failureKindLabel(kind), errorText), true
	}
}

func classifyExtractFailureKind(errorText string) string {
	value := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case strings.Contains(value, "host does not resolve"),
		strings.Contains(value, "no such host"),
		strings.Contains(value, "nxdomain"):
		return model.SourceFailureKindDNSNXDomain
	case strings.Contains(value, "self signed certificate"),
		strings.Contains(value, "unable to verify the first certificate"),
		strings.Contains(value, "err_tls_cert_altname_invalid"),
		strings.Contains(value, "altname invalid"),
		strings.Contains(value, "x509"),
		strings.Contains(value, "certificate"):
		return model.SourceFailureKindTLSCertificate
	case strings.Contains(value, "status 522"),
		strings.Contains(value, "status 523"),
		strings.Contains(value, "status 524"),
		strings.Contains(value, "status 525"),
		strings.Contains(value, "status 526"):
		return model.SourceFailureKindCloudflareEdge
	case strings.Contains(value, "x article returned an x error shell"):
		return model.SourceFailureKindXArticleShell
	case strings.Contains(value, "status 401"),
		strings.Contains(value, "401 unauthorized"),
		strings.Contains(value, "status 403"),
		strings.Contains(value, "403 forbidden"),
		strings.Contains(value, "status 451"),
		strings.Contains(value, "451 unavailable"):
		return model.SourceFailureKindHTTPAccessDenied
	case strings.Contains(value, "unsupported file type"):
		return model.SourceFailureKindUnsupportedFile
	case strings.Contains(value, "signal: killed"),
		strings.Contains(value, "context deadline exceeded"),
		strings.Contains(value, "timeout"),
		strings.Contains(value, "timed out"):
		return model.SourceFailureKindTimeout
	case strings.Contains(value, "fetch failed"):
		return model.SourceFailureKindFetchFailed
	case strings.Contains(value, "status 429"),
		strings.Contains(value, "429 too many requests"),
		strings.Contains(value, "too many requests"):
		return model.SourceFailureKindRateLimited
	case strings.Contains(value, "unable to connect"),
		strings.Contains(value, "connection refused"),
		strings.Contains(value, "network is unreachable"),
		strings.Contains(value, "no route to host"):
		return model.SourceFailureKindConnectivity
	case strings.Contains(value, "status 502"),
		strings.Contains(value, "status 503"),
		strings.Contains(value, "status 504"):
		return model.SourceFailureKindHTTP5xx
	default:
		return ""
	}
}

func deadThresholdForFailureKind(kind string) int {
	switch kind {
	case "", model.SourceFailureKindUnknown:
		return 5
	case model.SourceFailureKindDNSNXDomain:
		return 1
	case model.SourceFailureKindTLSCertificate, model.SourceFailureKindCloudflareEdge, model.SourceFailureKindConnectivity:
		return 3
	case model.SourceFailureKindXArticleShell:
		return 3
	case model.SourceFailureKindHTTPAccessDenied:
		return 3
	case model.SourceFailureKindUnsupportedFile:
		return 1
	case model.SourceFailureKindTimeout:
		return 3
	case model.SourceFailureKindFetchFailed:
		return 5
	case model.SourceFailureKindRateLimited:
		return 0
	case model.SourceFailureKindHTTP5xx:
		return 5
	default:
		return 0
	}
}

func nextFailureCount(source model.SourceDocument, kind string) int {
	if kind == "" {
		kind = model.SourceFailureKindUnknown
	}
	storedKind := strings.TrimSpace(source.ExtractFailureKind)
	if source.ExtractFailureCount <= 0 {
		return 1
	}
	if storedKind == kind {
		return source.ExtractFailureCount + 1
	}
	if storedKind == "" || storedKind == model.SourceFailureKindUnknown {
		return source.ExtractFailureCount + 1
	}
	return 1
}

func failureKindLabel(kind string) string {
	switch kind {
	case model.SourceFailureKindDNSNXDomain:
		return "dns resolution"
	case model.SourceFailureKindTLSCertificate:
		return "tls certificate"
	case model.SourceFailureKindCloudflareEdge:
		return "cloudflare edge"
	case model.SourceFailureKindConnectivity:
		return "connectivity"
	case model.SourceFailureKindXArticleShell:
		return "x article shell"
	case model.SourceFailureKindHTTPAccessDenied:
		return "http access denied"
	case model.SourceFailureKindUnsupportedFile:
		return "unsupported file"
	case model.SourceFailureKindTimeout:
		return "timeout"
	case model.SourceFailureKindFetchFailed:
		return "fetch"
	case model.SourceFailureKindRateLimited:
		return "rate limited"
	case model.SourceFailureKindHTTP5xx:
		return "http 5xx"
	case "", model.SourceFailureKindUnknown:
		return "unclassified"
	default:
		return "terminal"
	}
}
