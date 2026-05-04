package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
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

func (s *Store) ListItemsForXHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	return s.listItemsForXHydration(ctx, limit, force, xItemSourceTypeWhere)
}

func (s *Store) ListItemsForXQuoteHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_quote'
			AND external_id != ''`
	if !force {
		query += `
			AND (
				x_post_status = ''
				OR x_post_status = 'api_error'
				OR x_post_status = 'error'
				OR x_post_status = 'rate_limited'
				OR ` + xHydrationRepairWhere + `
			)`
	}
	query += `
		ORDER BY
			CASE WHEN x_post_status = '' THEN 0 ELSE 1 END,
			last_seen_at DESC,
			x_post_fetched_at ASC,
			id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x quote hydration items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x quote hydration item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x quote hydration items: %w", err)
	}

	return items, nil
}

func (s *Store) listItemsForXHydration(ctx context.Context, limit int, force bool, sourceWhere string) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + sourceWhere + `
			AND external_id != ''`
	if !force {
		query += `
				AND ` + xHydrationCandidateWhere
	}
	query += `
		ORDER BY
			CASE WHEN x_post_status = '' THEN 0 ELSE 1 END,
			last_seen_at DESC,
			x_post_fetched_at ASC,
			id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x hydration items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x hydration item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x hydration items: %w", err)
	}

	return items, nil
}

func (s *Store) ListItemsForXMediaTranscription(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
			AND external_id != ''
			AND EXISTS (
				SELECT 1
				FROM item_media_links l
				JOIN media_assets a ON a.id = l.media_asset_id
				WHERE l.item_id = items.id
					AND a.download_status = 'downloaded'
					AND a.local_path != ''
					AND a.local_pruned_at = ''
					AND a.media_type IN ('video', 'animated_gif')
			)`
	if !force {
		query += `
			AND NOT (
				article_title = 'X Media Transcript'
				AND article_text != ''
			)`
		query += `
			AND x_media_transcript_status = ''`
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x media transcription items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x media transcription item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x media transcription items: %w", err)
	}

	return items, nil
}

func (s *Store) SaveXMediaTranscriptionState(ctx context.Context, itemID int64, status string, errorText string, at time.Time) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		atText := ""
		if !at.IsZero() {
			atText = at.UTC().Format(time.RFC3339)
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE items
			SET x_media_transcript_status = ?,
				x_media_transcript_error = ?,
				x_media_transcript_at = ?,
				updated_at = ?
			WHERE id = ?`,
			strings.TrimSpace(status),
			strings.TrimSpace(errorText),
			atText,
			time.Now().UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return struct{}{}, fmt.Errorf("save x media transcription state %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) SaveXHydration(ctx context.Context, itemID int64, hydration model.XHydration) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin hydration tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		row := tx.QueryRowContext(ctx, `
			SELECT source_type, x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, links_json
			FROM items
			WHERE id = ?`, itemID)

		var currentSourceType, currentText, currentLang, currentJSON, currentFetchedAt, currentStatus, currentError, currentLinksJSON string
		if err := row.Scan(&currentSourceType, &currentText, &currentLang, &currentJSON, &currentFetchedAt, &currentStatus, &currentError, &currentLinksJSON); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("item not found for hydration: %d", itemID)
			}
			return false, fmt.Errorf("load current hydration %d: %w", itemID, err)
		}

		if shouldPreserveDirectQuotedHydration(currentSourceType, currentStatus, currentJSON, hydration.Status, hydration.APIJSON) {
			hydration.FullText = currentText
			hydration.Language = currentLang
			hydration.APIJSON = currentJSON
			hydration.Status = currentStatus
			hydration.Error = currentError
		}

		newFetchedAt := ""
		if !hydration.FetchedAt.IsZero() {
			newFetchedAt = hydration.FetchedAt.UTC().Format(time.RFC3339)
		}

		hydrationChanged := currentText != hydration.FullText ||
			currentLang != hydration.Language ||
			currentJSON != hydration.APIJSON ||
			currentStatus != hydration.Status ||
			currentError != hydration.Error ||
			(currentFetchedAt == "" && newFetchedAt != "")

		nowText := time.Now().UTC().Format(time.RFC3339)
		if hydrationChanged {
			if _, err := tx.ExecContext(ctx, `
				UPDATE items
				SET x_post_text = ?,
					x_post_lang = ?,
					x_post_json = ?,
					x_post_fetched_at = ?,
					x_post_status = ?,
					x_post_error = ?,
					updated_at = ?
				WHERE id = ?`,
				hydration.FullText,
				hydration.Language,
				hydration.APIJSON,
				newFetchedAt,
				hydration.Status,
				hydration.Error,
				nowText,
				itemID,
			); err != nil {
				return false, fmt.Errorf("save hydration %d: %w", itemID, err)
			}
			if err := s.invalidateLinkedXArticleSourcesTx(ctx, tx, itemID, nowText); err != nil {
				return false, err
			}
			if _, err := s.invalidateItemSummaryTx(ctx, tx, itemID, nowText); err != nil {
				return false, err
			}
		}

		mediaChanged, err := s.syncXHydrationMediaTx(ctx, tx, itemID, hydration, time.Now().UTC())
		if err != nil {
			return false, err
		}
		if mediaChanged {
			if _, err := s.invalidateItemOCRTx(ctx, tx, itemID, nowText); err != nil {
				return false, err
			}
		}
		if mediaChanged && !hydrationChanged {
			if _, err := tx.ExecContext(ctx, `
				UPDATE items
				SET updated_at = ?
				WHERE id = ?`,
				nowText,
				itemID,
			); err != nil {
				return false, fmt.Errorf("touch item after media sync %d: %w", itemID, err)
			}
		}
		linksChanged, err := syncXHydrationLinksTx(ctx, tx, itemID, currentLinksJSON, hydration.APIJSON, nowText)
		if err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit hydration %d: %w", itemID, err)
		}

		return hydrationChanged || mediaChanged || linksChanged, nil
	})
}

func shouldPreserveDirectQuotedHydration(sourceType, currentStatus, currentJSON, newStatus, newJSON string) bool {
	return sourceType == "x_quote" &&
		currentStatus == "ok_graphql" &&
		newStatus == "ok_graphql" &&
		strings.Contains(currentJSON, `"tweetResult"`) &&
		!strings.Contains(newJSON, `"tweetResult"`)
}

func syncXHydrationLinksTx(ctx context.Context, tx *sql.Tx, itemID int64, currentLinksJSON string, apiJSON string, nowText string) (bool, error) {
	snapshot, ok, err := xpost.DecodeSnapshot(apiJSON)
	if err != nil {
		return false, fmt.Errorf("decode hydration snapshot links %d: %w", itemID, err)
	}
	if !ok || len(snapshot.Links) == 0 {
		return false, nil
	}

	links := uniqueNonEmptyStrings(snapshot.Links)
	if len(links) == 0 {
		return false, nil
	}
	linksJSONBytes, err := json.Marshal(links)
	if err != nil {
		return false, fmt.Errorf("marshal hydration links %d: %w", itemID, err)
	}
	linksJSON := string(linksJSONBytes)
	if currentLinksJSON == linksJSON {
		return false, nil
	}

	primaryDomain, domains, githubURLs := deriveItemLinkMetadata(links)
	if _, err := tx.ExecContext(ctx, `
		UPDATE items
		SET links_json = ?,
			primary_domain = ?,
			domains = ?,
			github_urls = ?,
			link_extract_synced_at = '',
			updated_at = ?
		WHERE id = ?`,
		linksJSON,
		primaryDomain,
		strings.Join(domains, ","),
		strings.Join(githubURLs, ","),
		nowText,
		itemID,
	); err != nil {
		return false, fmt.Errorf("sync hydration links %d: %w", itemID, err)
	}

	return true, nil
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func deriveItemLinkMetadata(links []string) (string, []string, []string) {
	domains := make([]string, 0, len(links))
	githubURLs := make([]string, 0)
	seenDomains := map[string]struct{}{}
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if _, ok := seenDomains[host]; !ok {
			seenDomains[host] = struct{}{}
			domains = append(domains, host)
		}
		if host == "github.com" {
			githubURLs = append(githubURLs, link)
		}
	}
	primary := ""
	if len(domains) > 0 {
		primary = domains[0]
	}
	return primary, domains, githubURLs
}

func (s *Store) invalidateLinkedXArticleSourcesTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources
		SET extracted_text = '',
			extract_json = '',
			extract_status = '',
			extract_error = '',
			extract_failure_kind = '',
			extract_failure_count = 0,
			extract_first_failed_at = '',
			extract_last_failed_at = '',
			extracted_at = '',
			extract_tool = '',
			extract_tool_version = '',
			summary_text = '',
			summary_json = '',
			summary_status = '',
			summary_error = '',
			summary_model = '',
			summary_content_hash = '',
			summary_prompt_version = '',
			summary_tool = '',
			summary_tool_version = '',
			summarized_at = '',
			content_hash = '',
			updated_at = ?
		WHERE id IN (
			SELECT l.source_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			WHERE l.item_id = ?
				AND s.source_type = 'x_article'
		)`,
		nowText,
		itemID,
	); err != nil {
		return fmt.Errorf("invalidate linked x article sources for item %d: %w", itemID, err)
	}
	return nil
}
