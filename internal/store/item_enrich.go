package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbrain/internal/model"
)

func (s *Store) ListItemsForXMediaSummary(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_bookmark'
			AND article_title = 'X Media Transcript'
			AND article_text != ''
			AND x_media_transcript_status = 'ok'`
	if !force {
		query += `
			AND (summary_status = '' OR summary_status = 'error')`
	}
	query += `
		ORDER BY x_media_transcript_at DESC, last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x media summary items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x media summary item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x media summary items: %w", err)
	}
	return items, nil
}

func (s *Store) ListItemsForXPhotoOCR(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_bookmark'
			AND external_id != ''
			AND EXISTS (
				SELECT 1
				FROM item_media_links l
				JOIN media_assets a ON a.id = l.media_asset_id
				WHERE l.item_id = items.id
					AND a.download_status = 'downloaded'
					AND a.local_path != ''
					AND a.local_pruned_at = ''
					AND a.media_type = 'photo'
			)`
	if !force {
		query += `
			AND (ocr_status = '' OR ocr_status = 'error')`
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x photo ocr items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x photo ocr item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x photo ocr items: %w", err)
	}
	return items, nil
}

func (s *Store) SaveItemSummary(ctx context.Context, itemID int64, summary model.SummaryResult, inputHash string) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin item summary tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		row := tx.QueryRowContext(ctx, `
			SELECT summary_text, summary_json, summary_status, summary_error, summary_model,
				summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at
			FROM items
			WHERE id = ?`, itemID)

		var current itemSummaryFields
		if err := row.Scan(
			&current.Text,
			&current.JSON,
			&current.Status,
			&current.Error,
			&current.Model,
			&current.PromptVersion,
			&current.Tool,
			&current.ToolVersion,
			&current.InputHash,
			&current.At,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("item not found for summary: %d", itemID)
			}
			return false, fmt.Errorf("load current item summary %d: %w", itemID, err)
		}

		summarizedAt := formatTimeForDB(summary.FetchedAt)
		if current == (itemSummaryFields{
			Text:          summary.Text,
			JSON:          summary.RawJSON,
			Status:        summary.Status,
			Error:         summary.Error,
			Model:         summary.Model,
			PromptVersion: summary.PromptVersion,
			Tool:          summary.Tool,
			ToolVersion:   summary.ToolVersion,
			InputHash:     strings.TrimSpace(inputHash),
			At:            summarizedAt,
		}) {
			if commitErr := tx.Commit(); commitErr != nil {
				return false, fmt.Errorf("commit unchanged item summary: %w", commitErr)
			}
			return false, nil
		}

		nowText := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `
			UPDATE items
			SET summary_text = ?,
				summary_json = ?,
				summary_status = ?,
				summary_error = ?,
				summary_model = ?,
				summary_prompt_version = ?,
				summary_tool = ?,
				summary_tool_version = ?,
				summary_input_hash = ?,
				summarized_at = ?,
				updated_at = ?
			WHERE id = ?`,
			summary.Text,
			summary.RawJSON,
			summary.Status,
			summary.Error,
			summary.Model,
			summary.PromptVersion,
			summary.Tool,
			summary.ToolVersion,
			strings.TrimSpace(inputHash),
			summarizedAt,
			nowText,
			itemID,
		); err != nil {
			return false, fmt.Errorf("save item summary %d: %w", itemID, err)
		}

		if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit item summary %d: %w", itemID, err)
		}
		return true, nil
	})
}

func (s *Store) SaveItemOCR(ctx context.Context, itemID int64, result model.OCRResult, inputHash string) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin item ocr tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		row := tx.QueryRowContext(ctx, `
			SELECT ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at
			FROM items
			WHERE id = ?`, itemID)

		var current itemOCRFields
		if err := row.Scan(
			&current.Text,
			&current.JSON,
			&current.Status,
			&current.Error,
			&current.Model,
			&current.Tool,
			&current.ToolVersion,
			&current.InputHash,
			&current.At,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("item not found for ocr: %d", itemID)
			}
			return false, fmt.Errorf("load current item ocr %d: %w", itemID, err)
		}

		ocrAt := formatTimeForDB(result.FetchedAt)
		if current == (itemOCRFields{
			Text:        result.Text,
			JSON:        result.RawJSON,
			Status:      result.Status,
			Error:       result.Error,
			Model:       result.Model,
			Tool:        result.Tool,
			ToolVersion: result.ToolVersion,
			InputHash:   strings.TrimSpace(inputHash),
			At:          ocrAt,
		}) {
			if commitErr := tx.Commit(); commitErr != nil {
				return false, fmt.Errorf("commit unchanged item ocr: %w", commitErr)
			}
			return false, nil
		}

		nowText := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `
			UPDATE items
			SET ocr_text = ?,
				ocr_json = ?,
				ocr_status = ?,
				ocr_error = ?,
				ocr_model = ?,
				ocr_tool = ?,
				ocr_tool_version = ?,
				ocr_input_hash = ?,
				ocr_at = ?,
				updated_at = ?
			WHERE id = ?`,
			result.Text,
			result.RawJSON,
			result.Status,
			result.Error,
			result.Model,
			result.Tool,
			result.ToolVersion,
			strings.TrimSpace(inputHash),
			ocrAt,
			nowText,
			itemID,
		); err != nil {
			return false, fmt.Errorf("save item ocr %d: %w", itemID, err)
		}

		if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit item ocr %d: %w", itemID, err)
		}
		return true, nil
	})
}

func (s *Store) InvalidateItemSummary(ctx context.Context, itemID int64) error {
	return s.clearItemSummary(ctx, itemID)
}

func (s *Store) InvalidateItemOCR(ctx context.Context, itemID int64) error {
	return s.clearItemOCR(ctx, itemID)
}

func (s *Store) clearItemSummary(ctx context.Context, itemID int64) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return struct{}{}, fmt.Errorf("begin clear item summary tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		cleared, err := s.invalidateItemSummaryTx(ctx, tx, itemID, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return struct{}{}, err
		}
		if !cleared {
			if err := tx.Commit(); err != nil {
				return struct{}{}, fmt.Errorf("commit unchanged clear item summary: %w", err)
			}
			return struct{}{}, nil
		}
		if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
			return struct{}{}, err
		}
		if err := tx.Commit(); err != nil {
			return struct{}{}, fmt.Errorf("commit clear item summary %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) clearItemOCR(ctx context.Context, itemID int64) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return struct{}{}, fmt.Errorf("begin clear item ocr tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		cleared, err := s.invalidateItemOCRTx(ctx, tx, itemID, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return struct{}{}, err
		}
		if !cleared {
			if err := tx.Commit(); err != nil {
				return struct{}{}, fmt.Errorf("commit unchanged clear item ocr: %w", err)
			}
			return struct{}{}, nil
		}
		if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
			return struct{}{}, err
		}
		if err := tx.Commit(); err != nil {
			return struct{}{}, fmt.Errorf("commit clear item ocr %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) invalidateItemSummaryTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) (bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT summary_text, summary_json, summary_status, summary_error, summary_model, summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at FROM items WHERE id = ?`, itemID)
	var current itemSummaryFields
	if err := row.Scan(&current.Text, &current.JSON, &current.Status, &current.Error, &current.Model, &current.PromptVersion, &current.Tool, &current.ToolVersion, &current.InputHash, &current.At); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("item not found for clear summary: %d", itemID)
		}
		return false, fmt.Errorf("load item summary %d: %w", itemID, err)
	}
	if current == (itemSummaryFields{}) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE items
		SET summary_text = '',
			summary_json = '',
			summary_status = '',
			summary_error = '',
			summary_model = '',
			summary_prompt_version = '',
			summary_tool = '',
			summary_tool_version = '',
			summary_input_hash = '',
			summarized_at = '',
			updated_at = ?
		WHERE id = ?`, nowText, itemID); err != nil {
		return false, fmt.Errorf("clear item summary %d: %w", itemID, err)
	}
	return true, nil
}

func (s *Store) invalidateItemOCRTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) (bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at FROM items WHERE id = ?`, itemID)
	var current itemOCRFields
	if err := row.Scan(&current.Text, &current.JSON, &current.Status, &current.Error, &current.Model, &current.Tool, &current.ToolVersion, &current.InputHash, &current.At); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("item not found for clear ocr: %d", itemID)
		}
		return false, fmt.Errorf("load item ocr %d: %w", itemID, err)
	}
	if current == (itemOCRFields{}) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE items
		SET ocr_text = '',
			ocr_json = '',
			ocr_status = '',
			ocr_error = '',
			ocr_model = '',
			ocr_tool = '',
			ocr_tool_version = '',
			ocr_input_hash = '',
			ocr_at = '',
			updated_at = ?
		WHERE id = ?`, nowText, itemID); err != nil {
		return false, fmt.Errorf("clear item ocr %d: %w", itemID, err)
	}
	return true, nil
}

func (s *Store) syncItemFTSByIDTx(ctx context.Context, tx *sql.Tx, itemID int64) error {
	row := tx.QueryRowContext(ctx, `SELECT `+itemSelectColumns+` FROM items WHERE id = ?`, itemID)
	var item model.Item
	if err := scanItem(row, &item); err != nil {
		return fmt.Errorf("load item %d for fts sync: %w", itemID, err)
	}
	return s.syncFTSTx(ctx, tx, itemID, item)
}
