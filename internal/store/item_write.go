package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
)

func (s *Store) UpsertItem(ctx context.Context, item model.Item) (model.UpsertResult, error) {
	return withBusyRetry(ctx, func() (model.UpsertResult, error) {
		return s.upsertItem(ctx, item)
	})
}

func (s *Store) upsertItem(ctx context.Context, item model.Item) (model.UpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.UpsertResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var existingID int64
	var existingHash string
	var existingArticleTitle string
	var existingArticleText string
	var existingSummary itemSummaryFields
	var existingOCR itemOCRFields
	row := tx.QueryRowContext(ctx, `SELECT
		id, content_hash, article_title, article_text,
		summary_text, summary_json, summary_status, summary_error, summary_model, summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
		ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at
		FROM items
		WHERE source_key = ?`, item.SourceKey)
	switch scanErr := row.Scan(
		&existingID, &existingHash, &existingArticleTitle, &existingArticleText,
		&existingSummary.Text, &existingSummary.JSON, &existingSummary.Status, &existingSummary.Error, &existingSummary.Model, &existingSummary.PromptVersion, &existingSummary.Tool, &existingSummary.ToolVersion, &existingSummary.InputHash, &existingSummary.At,
		&existingOCR.Text, &existingOCR.JSON, &existingOCR.Status, &existingOCR.Error, &existingOCR.Model, &existingOCR.Tool, &existingOCR.ToolVersion, &existingOCR.InputHash, &existingOCR.At,
	); {
	case errors.Is(scanErr, sql.ErrNoRows):
		now := item.UpdatedAt.Format(time.RFC3339)
		result, execErr := tx.ExecContext(ctx, `INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			summary_text, summary_json, summary_status, summary_error, summary_model, summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
			ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.SourceKey, item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
			item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
			item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
			item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
			item.ContentHash, item.NotePath, item.RawJSON, now, now, item.LastSeenAt.Format(time.RFC3339),
			item.SummaryText, item.SummaryJSON, item.SummaryStatus, item.SummaryError, item.SummaryModel, item.SummaryPromptVersion, item.SummaryTool, item.SummaryToolVersion, item.SummaryInputHash, formatTimeForDB(item.SummarizedAt),
			item.OCRText, item.OCRJSON, item.OCRStatus, item.OCRError, item.OCRModel, item.OCRTool, item.OCRToolVersion, item.OCRInputHash, formatTimeForDB(item.OCRAt))
		if execErr != nil {
			err = fmt.Errorf("insert item %s: %w", item.SourceKey, execErr)
			return model.UpsertResult{}, err
		}
		itemID, execErr := result.LastInsertId()
		if execErr != nil {
			err = fmt.Errorf("fetch inserted row id %s: %w", item.SourceKey, execErr)
			return model.UpsertResult{}, err
		}
		if syncErr := s.syncFTSTx(ctx, tx, itemID, item); syncErr != nil {
			err = syncErr
			return model.UpsertResult{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return model.UpsertResult{}, fmt.Errorf("commit insert: %w", commitErr)
		}
		return model.UpsertResult{Status: model.UpsertCreated, ItemID: itemID, NotePath: item.NotePath}, nil
	case scanErr != nil:
		err = fmt.Errorf("load existing item %s: %w", item.SourceKey, scanErr)
		return model.UpsertResult{}, err
	default:
	}

	if preserveExistingItemEnrichmentFields(&item, existingArticleTitle, existingArticleText, existingSummary, existingOCR) {
		item.ContentHash = itemhash.Compute(item)
	}

	if existingHash == item.ContentHash {
		if _, execErr := tx.ExecContext(ctx, `UPDATE items SET last_seen_at = ?, synced_at = ?, raw_json = ? WHERE id = ?`,
			item.LastSeenAt.Format(time.RFC3339), item.SyncedAt, item.RawJSON, existingID); execErr != nil {
			err = fmt.Errorf("touch unchanged item %s: %w", item.SourceKey, execErr)
			return model.UpsertResult{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return model.UpsertResult{}, fmt.Errorf("commit unchanged item: %w", commitErr)
		}
		return model.UpsertResult{Status: model.UpsertUnchanged, ItemID: existingID, NotePath: item.NotePath}, nil
	}

	if _, execErr := tx.ExecContext(ctx, `UPDATE items SET
		source_type = ?, external_id = ?, canonical_url = ?, title = ?, author_handle = ?, author_name = ?,
		published_at = ?, saved_at = ?, synced_at = ?, language = ?, text = ?, article_title = ?, article_text = ?,
		primary_category = ?, primary_domain = ?, links_json = ?, categories = ?, domains = ?, github_urls = ?, folder_names = ?,
		like_count = ?, repost_count = ?, reply_count = ?, quote_count = ?, bookmark_count = ?,
		content_hash = ?, note_path = ?, raw_json = ?, updated_at = ?, last_seen_at = ?,
		summary_text = ?, summary_json = ?, summary_status = ?, summary_error = ?, summary_model = ?, summary_prompt_version = ?, summary_tool = ?, summary_tool_version = ?, summary_input_hash = ?, summarized_at = ?,
		ocr_text = ?, ocr_json = ?, ocr_status = ?, ocr_error = ?, ocr_model = ?, ocr_tool = ?, ocr_tool_version = ?, ocr_input_hash = ?, ocr_at = ?
		WHERE id = ?`,
		item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
		item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
		item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
		item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
		item.ContentHash, item.NotePath, item.RawJSON, item.UpdatedAt.Format(time.RFC3339), item.LastSeenAt.Format(time.RFC3339),
		item.SummaryText, item.SummaryJSON, item.SummaryStatus, item.SummaryError, item.SummaryModel, item.SummaryPromptVersion, item.SummaryTool, item.SummaryToolVersion, item.SummaryInputHash, formatTimeForDB(item.SummarizedAt),
		item.OCRText, item.OCRJSON, item.OCRStatus, item.OCRError, item.OCRModel, item.OCRTool, item.OCRToolVersion, item.OCRInputHash, formatTimeForDB(item.OCRAt),
		existingID); execErr != nil {
		err = fmt.Errorf("update item %s: %w", item.SourceKey, execErr)
		return model.UpsertResult{}, err
	}

	if syncErr := s.syncFTSTx(ctx, tx, existingID, item); syncErr != nil {
		err = syncErr
		return model.UpsertResult{}, err
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return model.UpsertResult{}, fmt.Errorf("commit update: %w", commitErr)
	}

	return model.UpsertResult{Status: model.UpsertUpdated, ItemID: existingID, NotePath: item.NotePath}, nil
}

type itemSummaryFields struct {
	Text          string
	JSON          string
	Status        string
	Error         string
	Model         string
	PromptVersion string
	Tool          string
	ToolVersion   string
	InputHash     string
	At            string
}

type itemOCRFields struct {
	Text        string
	JSON        string
	Status      string
	Error       string
	Model       string
	Tool        string
	ToolVersion string
	InputHash   string
	At          string
}

func preserveExistingItemEnrichmentFields(item *model.Item, existingArticleTitle string, existingArticleText string, existingSummary itemSummaryFields, existingOCR itemOCRFields) bool {
	if item == nil {
		return false
	}

	changed := false
	if strings.TrimSpace(item.ArticleText) == "" && strings.TrimSpace(existingArticleText) != "" {
		item.ArticleText = existingArticleText
		changed = true
	}
	if strings.TrimSpace(item.ArticleTitle) == "" && strings.TrimSpace(existingArticleTitle) != "" {
		item.ArticleTitle = existingArticleTitle
		changed = true
	}
	if strings.TrimSpace(item.SummaryText) == "" && strings.TrimSpace(existingSummary.Text) != "" {
		item.SummaryText = existingSummary.Text
		changed = true
	}
	if strings.TrimSpace(item.SummaryJSON) == "" && strings.TrimSpace(existingSummary.JSON) != "" {
		item.SummaryJSON = existingSummary.JSON
		changed = true
	}
	if strings.TrimSpace(item.SummaryStatus) == "" && strings.TrimSpace(existingSummary.Status) != "" {
		item.SummaryStatus = existingSummary.Status
		changed = true
	}
	if strings.TrimSpace(item.SummaryError) == "" && strings.TrimSpace(existingSummary.Error) != "" {
		item.SummaryError = existingSummary.Error
		changed = true
	}
	if strings.TrimSpace(item.SummaryModel) == "" && strings.TrimSpace(existingSummary.Model) != "" {
		item.SummaryModel = existingSummary.Model
		changed = true
	}
	if strings.TrimSpace(item.SummaryPromptVersion) == "" && strings.TrimSpace(existingSummary.PromptVersion) != "" {
		item.SummaryPromptVersion = existingSummary.PromptVersion
		changed = true
	}
	if strings.TrimSpace(item.SummaryTool) == "" && strings.TrimSpace(existingSummary.Tool) != "" {
		item.SummaryTool = existingSummary.Tool
		changed = true
	}
	if strings.TrimSpace(item.SummaryToolVersion) == "" && strings.TrimSpace(existingSummary.ToolVersion) != "" {
		item.SummaryToolVersion = existingSummary.ToolVersion
		changed = true
	}
	if strings.TrimSpace(item.SummaryInputHash) == "" && strings.TrimSpace(existingSummary.InputHash) != "" {
		item.SummaryInputHash = existingSummary.InputHash
		changed = true
	}
	if item.SummarizedAt.IsZero() && strings.TrimSpace(existingSummary.At) != "" {
		item.SummarizedAt = parseStoredTime(existingSummary.At)
		changed = true
	}
	if strings.TrimSpace(item.OCRText) == "" && strings.TrimSpace(existingOCR.Text) != "" {
		item.OCRText = existingOCR.Text
		changed = true
	}
	if strings.TrimSpace(item.OCRJSON) == "" && strings.TrimSpace(existingOCR.JSON) != "" {
		item.OCRJSON = existingOCR.JSON
		changed = true
	}
	if strings.TrimSpace(item.OCRStatus) == "" && strings.TrimSpace(existingOCR.Status) != "" {
		item.OCRStatus = existingOCR.Status
		changed = true
	}
	if strings.TrimSpace(item.OCRError) == "" && strings.TrimSpace(existingOCR.Error) != "" {
		item.OCRError = existingOCR.Error
		changed = true
	}
	if strings.TrimSpace(item.OCRModel) == "" && strings.TrimSpace(existingOCR.Model) != "" {
		item.OCRModel = existingOCR.Model
		changed = true
	}
	if strings.TrimSpace(item.OCRTool) == "" && strings.TrimSpace(existingOCR.Tool) != "" {
		item.OCRTool = existingOCR.Tool
		changed = true
	}
	if strings.TrimSpace(item.OCRToolVersion) == "" && strings.TrimSpace(existingOCR.ToolVersion) != "" {
		item.OCRToolVersion = existingOCR.ToolVersion
		changed = true
	}
	if strings.TrimSpace(item.OCRInputHash) == "" && strings.TrimSpace(existingOCR.InputHash) != "" {
		item.OCRInputHash = existingOCR.InputHash
		changed = true
	}
	if item.OCRAt.IsZero() && strings.TrimSpace(existingOCR.At) != "" {
		item.OCRAt = parseStoredTime(existingOCR.At)
		changed = true
	}
	return changed
}

func formatTimeForDB(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
