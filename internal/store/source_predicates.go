package store

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const sourceExtractErrorRetryCooldown = 12 * time.Hour

type sourceEnrichmentPolicy struct {
	now           time.Time
	promptVersion string
	toolName      string
	toolVersion   string
}

func newSourceEnrichmentPolicy(now time.Time, promptVersion string, toolName string, toolVersion string) sourceEnrichmentPolicy {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return sourceEnrichmentPolicy{
		now:           now.UTC(),
		promptVersion: strings.TrimSpace(promptVersion),
		toolName:      strings.TrimSpace(toolName),
		toolVersion:   strings.TrimSpace(toolVersion),
	}
}

func isExtractFailureStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case model.SourceExtractStatusError, model.SourceExtractStatusDead, model.SourceExtractStatusGone:
		return true
	default:
		return false
	}
}

func (p sourceEnrichmentPolicy) extractBacklogWhere() (string, []any) {
	return `(
		extract_status = ''
		OR ` + sourceExtractCoverageRepairWhere() + `
		OR (
			extract_status = '` + model.SourceExtractStatusError + `'
			AND (
				extract_last_failed_at = ''
				OR extract_last_failed_at <= ?
				OR ` + sourceExtractFinalAttemptWhere() + `
			)
		)
	)`, []any{
			p.now.Add(-sourceExtractErrorRetryCooldown).Format(time.RFC3339),
		}
}

func (p sourceEnrichmentPolicy) summaryBacklogWhere() (string, []any) {
	staleWhere, args := sourceSummaryStaleWhere(p.promptVersion, p.toolName, p.toolVersion)
	return `extract_status IN ('` + model.SourceExtractStatusOK + `', '` + model.SourceExtractStatusEmpty + `') AND ` + staleWhere, args
}

func (p sourceEnrichmentPolicy) candidateWhere(summarize bool) (string, []any) {
	extractWhere, extractArgs := p.extractBacklogWhere()
	if !summarize {
		return extractWhere, extractArgs
	}
	summaryWhere, summaryArgs := p.summaryBacklogWhere()
	args := append([]any{}, extractArgs...)
	args = append(args, summaryArgs...)
	return `(
		` + extractWhere + `
		OR ` + summaryWhere + `
	)`, args
}

func sourceExtractFinalAttemptWhere() string {
	return `(
		extract_failure_count > 0
		AND (
			(
				COALESCE(NULLIF(extract_failure_kind, ''), '` + model.SourceFailureKindUnknown + `') IN ('` + model.SourceFailureKindUnknown + `', '` + model.SourceFailureKindFetchFailed + `', '` + model.SourceFailureKindHTTP5xx + `')
				AND extract_failure_count >= 4
			)
			OR (
				extract_failure_kind IN ('` + model.SourceFailureKindTLSCertificate + `', '` + model.SourceFailureKindCloudflareEdge + `', '` + model.SourceFailureKindConnectivity + `', '` + model.SourceFailureKindXArticleShell + `', '` + model.SourceFailureKindHTTPAccessDenied + `', '` + model.SourceFailureKindTimeout + `')
				AND extract_failure_count >= 2
			)
			OR (
				extract_failure_kind IN ('` + model.SourceFailureKindDNSNXDomain + `', '` + model.SourceFailureKindUnsupportedFile + `')
				AND extract_failure_count >= 1
			)
		)
	)`
}

func sourceExtractCoverageRepairWhere() string {
	return `(
		source_type = 'x_article'
		AND extract_status = '` + model.SourceExtractStatusOK + `'
		AND extract_tool = 'x-hydration'
		AND extract_tool_version = 'local-article-preview-cache'
		AND length(trim(extracted_text)) > 0
		AND length(trim(extracted_text)) < 300
	)`
}

func sourceSummaryCoverageRepairWhere() string {
	return `(
		(extract_status = '` + model.SourceExtractStatusEmpty + `' AND summary_status = '` + model.SourceSummaryStatusOK + `')
		OR (
			extract_status = '` + model.SourceExtractStatusOK + `'
			AND summary_status = '` + model.SourceSummaryStatusOK + `'
			AND extract_tool = 'wayback'
			AND length(trim(extracted_text)) < 500
		)
		OR (
			extract_status = '` + model.SourceExtractStatusOK + `'
			AND summary_status = '` + model.SourceSummaryStatusOK + `'
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
		"(summary_status = '' OR summary_status = '" + model.SourceSummaryStatusError + "' OR summary_content_hash != content_hash OR " + sourceSummaryCoverageRepairWhere(),
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
