package retrieval

import "github.com/darron/dbrain/internal/model"

func ItemMetadata(item model.Item) map[string]interface{} {
	return map[string]interface{}{
		"id":                        item.ID,
		"source_key":                item.SourceKey,
		"source_type":               item.SourceType,
		"external_id":               item.ExternalID,
		"canonical_url":             item.CanonicalURL,
		"title":                     item.Title,
		"author_handle":             item.AuthorHandle,
		"author_name":               item.AuthorName,
		"published_at":              item.PublishedAt,
		"saved_at":                  item.SavedAt,
		"language":                  item.Language,
		"primary_category":          item.PrimaryCategory,
		"primary_domain":            item.PrimaryDomain,
		"note_path":                 item.NotePath,
		"user_tags":                 item.UserTags,
		"x_post_status":             item.XPostStatus,
		"summary_status":            item.SummaryStatus,
		"summary_model":             item.SummaryModel,
		"summary_tool":              item.SummaryTool,
		"ocr_status":                item.OCRStatus,
		"ocr_model":                 item.OCRModel,
		"ocr_tool":                  item.OCRTool,
		"x_media_transcript_status": item.XMediaTranscriptStatus,
		"imported_at":               FormatTime(item.ImportedAt),
		"updated_at":                FormatTime(item.UpdatedAt),
		"last_seen_at":              FormatTime(item.LastSeenAt),
	}
}

func SourceMetadata(source model.SourceDocument) map[string]interface{} {
	return map[string]interface{}{
		"id":                    source.ID,
		"source_key":            source.SourceKey,
		"canonical_url":         source.CanonicalURL,
		"normalized_url":        source.NormalizedURL,
		"source_type":           source.SourceType,
		"domain":                source.Domain,
		"title":                 source.Title,
		"description":           source.Description,
		"site_name":             source.SiteName,
		"note_path":             source.NotePath,
		"user_tags":             source.UserTags,
		"extract_status":        source.ExtractStatus,
		"extract_error":         source.ExtractError,
		"extract_failure_kind":  source.ExtractFailureKind,
		"extract_failure_count": source.ExtractFailureCount,
		"extracted_at":          FormatTime(source.ExtractedAt),
		"extract_tool":          source.ExtractTool,
		"extract_tool_version":  source.ExtractToolVersion,
		"summary_status":        source.SummaryStatus,
		"summary_error":         source.SummaryError,
		"summary_model":         source.SummaryModel,
		"summary_tool":          source.SummaryTool,
		"summary_tool_version":  source.SummaryToolVersion,
		"summarized_at":         FormatTime(source.SummarizedAt),
		"content_hash":          source.ContentHash,
		"created_at":            FormatTime(source.CreatedAt),
		"updated_at":            FormatTime(source.UpdatedAt),
	}
}
