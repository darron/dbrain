package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

// invalidateItemMediaDerivedTx clears enrichment that was computed from the
// previous ordered media set. Keeping this in the replacement transaction
// makes the contract apply equally to X hydration and direct Bluesky import.
func (s *Store) invalidateItemMediaDerivedTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) error {
	if _, err := s.invalidateItemOCRTx(ctx, tx, itemID, nowText); err != nil {
		return err
	}
	if _, err := s.invalidateItemSummaryTx(ctx, tx, itemID, nowText); err != nil {
		return err
	}
	if _, err := s.invalidateItemMediaTranscriptTx(ctx, tx, itemID, nowText); err != nil {
		return err
	}
	if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
		return fmt.Errorf("sync item FTS after media invalidation %d: %w", itemID, err)
	}
	return nil
}

func (s *Store) invalidateItemMediaTranscriptTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) (bool, error) {
	var status, errorText, atText, articleTitle string
	if err := tx.QueryRowContext(ctx, `
		SELECT x_media_transcript_status, x_media_transcript_error, x_media_transcript_at, article_title
		FROM items
		WHERE id = ?`, itemID).Scan(&status, &errorText, &atText, &articleTitle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("item not found for clear media transcript: %d", itemID)
		}
		return false, fmt.Errorf("load item media transcript %d: %w", itemID, err)
	}

	changed := strings.TrimSpace(status) != "" ||
		strings.TrimSpace(errorText) != "" ||
		strings.TrimSpace(atText) != "" ||
		strings.TrimSpace(articleTitle) == model.XMediaTranscriptArticleTitle
	if changed {
		if _, err := tx.ExecContext(ctx, `
			UPDATE items
			SET x_media_transcript_status = '',
				x_media_transcript_error = '',
				x_media_transcript_at = '',
				article_title = CASE WHEN article_title = ? THEN '' ELSE article_title END,
				article_text = CASE WHEN article_title = ? THEN '' ELSE article_text END,
				updated_at = ?
			WHERE id = ?`, model.XMediaTranscriptArticleTitle, model.XMediaTranscriptArticleTitle, nowText, itemID); err != nil {
			return false, fmt.Errorf("clear item media transcript %d: %w", itemID, err)
		}
	}
	if err := s.deleteItemEnrichmentTx(ctx, tx, itemID, model.ItemEnrichmentRoleXMediaTranscript); err != nil {
		return false, err
	}
	return changed, nil
}
