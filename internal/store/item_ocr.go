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

func (s *Store) InvalidateItemOCR(ctx context.Context, itemID int64) error {
	return s.clearItemOCR(ctx, itemID)
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
