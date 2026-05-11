package store

import "github.com/darron/dbrain/internal/model"

const sourceSelectColumns = `
	id, source_key, canonical_url, normalized_url, source_type, domain, title, description, site_name,
	extracted_text, extract_json, extract_status, extract_error, extract_failure_kind, extract_failure_count,
	extract_first_failed_at, extract_last_failed_at, extracted_at,
	extract_tool, extract_tool_version,
	summary_text, summary_json, summary_status, summary_error, summary_model, summary_content_hash, summary_prompt_version,
	summary_tool, summary_tool_version, summarized_at, summary_failed_at,
	content_hash, note_path, user_tags, created_at, updated_at`

func scanSource(scanner interface{ Scan(dest ...any) error }, source *model.SourceDocument) error {
	var extractedAt, summarizedAt, summaryFailedAt, createdAt, updatedAt string
	var extractFirstFailedAt, extractLastFailedAt string
	if err := scanner.Scan(
		&source.ID,
		&source.SourceKey,
		&source.CanonicalURL,
		&source.NormalizedURL,
		&source.SourceType,
		&source.Domain,
		&source.Title,
		&source.Description,
		&source.SiteName,
		&source.ExtractedText,
		&source.ExtractJSON,
		&source.ExtractStatus,
		&source.ExtractError,
		&source.ExtractFailureKind,
		&source.ExtractFailureCount,
		&extractFirstFailedAt,
		&extractLastFailedAt,
		&extractedAt,
		&source.ExtractTool,
		&source.ExtractToolVersion,
		&source.SummaryText,
		&source.SummaryJSON,
		&source.SummaryStatus,
		&source.SummaryError,
		&source.SummaryModel,
		&source.SummaryContentHash,
		&source.SummaryPromptVersion,
		&source.SummaryTool,
		&source.SummaryToolVersion,
		&summarizedAt,
		&summaryFailedAt,
		&source.ContentHash,
		&source.NotePath,
		&source.UserTags,
		&createdAt,
		&updatedAt,
	); err != nil {
		return err
	}

	source.ExtractedAt = parseStoredTime(extractedAt)
	source.ExtractFirstFailedAt = parseStoredTime(extractFirstFailedAt)
	source.ExtractLastFailedAt = parseStoredTime(extractLastFailedAt)
	source.SummarizedAt = parseStoredTime(summarizedAt)
	source.SummaryFailedAt = parseStoredTime(summaryFailedAt)
	source.CreatedAt = parseStoredTime(createdAt)
	source.UpdatedAt = parseStoredTime(updatedAt)
	return nil
}
