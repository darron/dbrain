package store

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const sourceExtractErrorRetryCooldown = 12 * time.Hour

func isExtractFailureStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "error", "dead", "gone":
		return true
	default:
		return false
	}
}

func sourceExtractBacklogWhere(now time.Time) (string, []any) {
	return `(
		extract_status = ''
		OR ` + sourceExtractCoverageRepairWhere() + `
		OR (
			extract_status = 'error'
			AND (
				extract_last_failed_at = ''
				OR extract_last_failed_at <= ?
				OR ` + sourceExtractFinalAttemptWhere() + `
			)
		)
	)`, []any{
			now.UTC().Add(-sourceExtractErrorRetryCooldown).Format(time.RFC3339),
		}
}

func sourceExtractFinalAttemptWhere() string {
	return `(
		extract_failure_count > 0
		AND (
			(
				COALESCE(NULLIF(extract_failure_kind, ''), 'unknown') IN ('unknown', 'fetch_failed', 'http_5xx')
				AND extract_failure_count >= 4
			)
			OR (
				extract_failure_kind IN ('tls_certificate', 'cloudflare_edge', 'connectivity', 'x_article_shell', 'http_access_denied', 'timeout')
				AND extract_failure_count >= 2
			)
			OR (
				extract_failure_kind IN ('dns_nxdomain', 'unsupported_file')
				AND extract_failure_count >= 1
			)
		)
	)`
}

func sourceExtractCoverageRepairWhere() string {
	return `(
		source_type = 'x_article'
		AND extract_status = 'ok'
		AND extract_tool = 'x-hydration'
		AND extract_tool_version = 'local-article-preview-cache'
		AND length(trim(extracted_text)) > 0
		AND length(trim(extracted_text)) < 300
	)`
}

func sourceSummaryCoverageRepairWhere() string {
	return `(
		(extract_status = 'empty' AND summary_status = 'ok')
		OR (
			extract_status = 'ok'
			AND summary_status = 'ok'
			AND extract_tool = 'wayback'
			AND length(trim(extracted_text)) < 500
		)
		OR (
			extract_status = 'ok'
			AND summary_status = 'ok'
			AND length(trim(extracted_text)) <= 300
			AND (
				lower(trim(extracted_text)) LIKE '%redirecting%'
				OR lower(trim(extracted_text)) LIKE '%you will be redirected%'
				OR lower(trim(extracted_text)) LIKE '%if you are not redirected automatically%'
				OR lower(trim(extracted_text)) LIKE '%loading...%'
				OR lower(trim(extracted_text)) LIKE '%coming soon%'
				OR lower(trim(extracted_text)) LIKE '%<div></div>%'
				OR lower(trim(extracted_text)) LIKE '%we use cookies to improve user experience%'
				OR lower(trim(extracted_text)) LIKE '%nothing to see here%'
				OR lower(trim(extracted_text)) LIKE '%google drive%'
				OR lower(trim(extracted_text)) LIKE '%your browser does not support frames%'
				OR lower(trim(extracted_text)) LIKE '%click here to enter the site%'
				OR lower(trim(extracted_text)) LIKE '%sign in or sign up%'
				OR lower(trim(extracted_text)) LIKE '%you are not logged in%'
				OR lower(trim(extracted_text)) LIKE '%manage account%'
				OR lower(trim(extracted_text)) LIKE '%your profile%'
				OR lower(trim(extracted_text)) LIKE '%continue with google%'
				OR lower(trim(extracted_text)) LIKE '%continue with github%'
				OR lower(trim(extracted_text)) LIKE '%open full screen to view more%'
				OR lower(trim(extracted_text)) LIKE '%google apps%'
			)
		)
	)`
}

func sourceSummaryStaleWhere(promptVersion string, toolName string, toolVersion string) (string, []any) {
	parts := []string{
		"(summary_status = '' OR summary_status = 'error' OR summary_content_hash != content_hash OR " + sourceSummaryCoverageRepairWhere(),
	}
	args := []any{}
	if strings.TrimSpace(promptVersion) != "" {
		parts[0] += " OR summary_prompt_version != ?"
		args = append(args, promptVersion)
	}
	if strings.TrimSpace(toolName) != "" {
		parts[0] += " OR summary_tool != ?"
		args = append(args, toolName)
	}
	if strings.TrimSpace(toolVersion) != "" {
		parts[0] += " OR summary_tool_version != ?"
		args = append(args, toolVersion)
	}
	parts[0] += ")"
	return strings.Join(parts, " AND "), args
}

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
