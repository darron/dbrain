package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

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
