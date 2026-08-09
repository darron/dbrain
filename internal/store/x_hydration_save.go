package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) SaveXHydration(ctx context.Context, itemID int64, hydration model.XHydration) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		return withAuthoritativeWriteTx(ctx, s, "save-x-hydration", func(ctx context.Context, tx authoritativeWriteTx) (bool, error) {
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
			return hydrationChanged || mediaChanged || linksChanged, nil
		})
	})
}

func shouldPreserveDirectQuotedHydration(sourceType, currentStatus, currentJSON, newStatus, newJSON string) bool {
	return sourceType == "x_quote" &&
		currentStatus == "ok_graphql" &&
		newStatus == "ok_graphql" &&
		strings.Contains(currentJSON, `"tweetResult"`) &&
		!strings.Contains(newJSON, `"tweetResult"`)
}
