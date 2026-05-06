package model

const (
	SourceExtractStatusOK    = "ok"
	SourceExtractStatusEmpty = "empty"
	SourceExtractStatusError = "error"
	SourceExtractStatusDead  = "dead"
	SourceExtractStatusGone  = "gone"
)

const (
	SourceSummaryStatusOK      = "ok"
	SourceSummaryStatusError   = "error"
	SourceSummaryStatusBlocked = "blocked"
	SourceSummaryStatusSkipped = "skipped"
)

const (
	SourceFailureKindUnknown          = "unknown"
	SourceFailureKindFetchFailed      = "fetch_failed"
	SourceFailureKindRateLimited      = "rate_limited"
	SourceFailureKindHTTP5xx          = "http_5xx"
	SourceFailureKindTLSCertificate   = "tls_certificate"
	SourceFailureKindCloudflareEdge   = "cloudflare_edge"
	SourceFailureKindConnectivity     = "connectivity"
	SourceFailureKindXArticleShell    = "x_article_shell"
	SourceFailureKindHTTPAccessDenied = "http_access_denied"
	SourceFailureKindTimeout          = "timeout"
	SourceFailureKindDNSNXDomain      = "dns_nxdomain"
	SourceFailureKindUnsupportedFile  = "unsupported_file"
	SourceFailureKindHTTPGone         = "http_gone"
)
