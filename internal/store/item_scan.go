package store

import (
	"time"

	"github.com/darron/dbrain/internal/model"
)

const itemSelectColumns = `
	id, source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
	published_at, saved_at, synced_at, language, text, article_title, article_text,
	primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
	like_count, repost_count, reply_count, quote_count, bookmark_count,
	content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
	x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error,
	link_extract_synced_at,
	summary_text, summary_json, summary_status, summary_error, summary_model,
	summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
	ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at,
	x_media_transcript_status, x_media_transcript_error, x_media_transcript_at,
	user_tags`

func scanItem(scanner interface{ Scan(dest ...any) error }, item *model.Item) error {
	var importedAt, updatedAt, lastSeenAt string
	var xPostFetchedAt string
	var linkExtractSyncedAt string
	var summarizedAt string
	var ocrAt string
	var xMediaTranscriptAt string
	if err := scanner.Scan(
		&item.ID, &item.SourceKey, &item.SourceType, &item.ExternalID, &item.CanonicalURL, &item.Title, &item.AuthorHandle, &item.AuthorName,
		&item.PublishedAt, &item.SavedAt, &item.SyncedAt, &item.Language, &item.Text, &item.ArticleTitle, &item.ArticleText,
		&item.PrimaryCategory, &item.PrimaryDomain, &item.LinksJSON, &item.Categories, &item.Domains, &item.GitHubURLs, &item.FolderNames,
		&item.LikeCount, &item.RepostCount, &item.ReplyCount, &item.QuoteCount, &item.BookmarkCount,
		&item.ContentHash, &item.NotePath, &item.RawJSON, &importedAt, &updatedAt, &lastSeenAt,
		&item.XPostText, &item.XPostLang, &item.XPostJSON, &xPostFetchedAt, &item.XPostStatus, &item.XPostError,
		&linkExtractSyncedAt,
		&item.SummaryText, &item.SummaryJSON, &item.SummaryStatus, &item.SummaryError, &item.SummaryModel,
		&item.SummaryPromptVersion, &item.SummaryTool, &item.SummaryToolVersion, &item.SummaryInputHash, &summarizedAt,
		&item.OCRText, &item.OCRJSON, &item.OCRStatus, &item.OCRError, &item.OCRModel, &item.OCRTool, &item.OCRToolVersion, &item.OCRInputHash, &ocrAt,
		&item.XMediaTranscriptStatus, &item.XMediaTranscriptError, &xMediaTranscriptAt,
		&item.UserTags,
	); err != nil {
		return err
	}

	item.ImportedAt = parseStoredTime(importedAt)
	item.UpdatedAt = parseStoredTime(updatedAt)
	item.LastSeenAt = parseStoredTime(lastSeenAt)
	item.XPostFetchedAt = parseStoredTime(xPostFetchedAt)
	item.LinkExtractSyncedAt = parseStoredTime(linkExtractSyncedAt)
	item.SummarizedAt = parseStoredTime(summarizedAt)
	item.OCRAt = parseStoredTime(ocrAt)
	item.XMediaTranscriptAt = parseStoredTime(xMediaTranscriptAt)
	return nil
}

func parseStoredTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
