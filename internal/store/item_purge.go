package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) PurgeItemIndexedContent(ctx context.Context, sourceKey string, rawJSON string) (bool, error) {
	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		return false, fmt.Errorf("source key is required for purge")
	}
	return withBusyRetry(ctx, func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin item purge tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		row := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE source_key = ?`, sourceKey)
		var itemID int64
		if err := row.Scan(&itemID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, fmt.Errorf("load item for purge %s: %w", sourceKey, err)
		}

		affectedProfiles := make([]string, 0)
		profileRows, err := tx.QueryContext(ctx, `
			SELECT DISTINCT e.profile_id
			FROM retrieval_chunks c
			JOIN retrieval_embeddings e ON e.chunk_id = c.chunk_id
			WHERE c.parent_kind = 'item' AND c.parent_source_key = ?`, sourceKey)
		if err != nil {
			return false, fmt.Errorf("load retrieval profiles for item purge %s: %w", sourceKey, err)
		}
		for profileRows.Next() {
			var profileID string
			if err := profileRows.Scan(&profileID); err != nil {
				_ = profileRows.Close()
				return false, fmt.Errorf("scan retrieval profile for item purge %s: %w", sourceKey, err)
			}
			affectedProfiles = append(affectedProfiles, profileID)
		}
		if err := profileRows.Err(); err != nil {
			_ = profileRows.Close()
			return false, fmt.Errorf("iterate retrieval profiles for item purge %s: %w", sourceKey, err)
		}
		if err := profileRows.Close(); err != nil {
			return false, fmt.Errorf("close retrieval profiles for item purge %s: %w", sourceKey, err)
		}
		for _, profileID := range affectedProfiles {
			if err := markRetrievalProfileGenerationsStaleTx(ctx, tx, profileID); err != nil {
				return false, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM retrieval_chunks
			WHERE parent_kind = 'item' AND parent_source_key = ?`, sourceKey); err != nil {
			return false, fmt.Errorf("delete retrieval chunks for item purge %s: %w", sourceKey, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_enrichments WHERE item_id = ?`, itemID); err != nil {
			return false, fmt.Errorf("delete authoritative item enrichments for purge %s: %w", sourceKey, err)
		}

		nowText := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `
			UPDATE items
			SET title = '[excluded Apple Note]',
				canonical_url = '',
				text = '',
				article_title = '',
				article_text = '',
				links_json = '[]',
				categories = '',
				domains = '',
				github_urls = '',
				folder_names = '',
				content_hash = ?,
				raw_json = ?,
				summary_text = '',
				summary_json = '',
				summary_status = '',
				summary_error = '',
				summary_model = '',
				summary_prompt_version = '',
				summary_tool = '',
				summary_tool_version = '',
				summary_input_hash = '',
				summarized_at = '',
				ocr_text = '',
				ocr_json = '',
				ocr_status = '',
				ocr_error = '',
				ocr_model = '',
				ocr_tool = '',
				ocr_tool_version = '',
				ocr_input_hash = '',
				ocr_at = '',
				updated_at = ?
			WHERE id = ?`,
			"purged:"+sourceKey+":"+nowText,
			rawJSON,
			nowText,
			itemID,
		); err != nil {
			return false, fmt.Errorf("purge item indexed content %s: %w", sourceKey, err)
		}
		if s.hasFTS {
			if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, itemID); err != nil {
				return false, fmt.Errorf("delete purged item fts %s: %w", sourceKey, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit item purge %s: %w", sourceKey, err)
		}
		return true, nil
	})
}
