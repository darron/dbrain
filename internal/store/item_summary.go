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

func (s *Store) SaveItemSummary(ctx context.Context, itemID int64, summary model.SummaryResult, inputHash string) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		return withAuthoritativeWriteTx(ctx, s, "save-item-summary", func(ctx context.Context, tx authoritativeWriteTx) (bool, error) {
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

			if err := s.upsertItemEnrichmentTx(ctx, tx, model.ItemEnrichment{
				ItemID:        itemID,
				Role:          model.ItemEnrichmentRoleSummary,
				Status:        summary.Status,
				Text:          summary.Text,
				RawJSON:       summary.RawJSON,
				Error:         summary.Error,
				Model:         summary.Model,
				PromptVersion: summary.PromptVersion,
				Tool:          summary.Tool,
				ToolVersion:   summary.ToolVersion,
				InputHash:     strings.TrimSpace(inputHash),
				CompletedAt:   summary.FetchedAt,
			}); err != nil {
				return false, err
			}
			if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
				return false, err
			}
			return true, nil
		})
	})
}

func (s *Store) InvalidateItemSummary(ctx context.Context, itemID int64) error {
	return s.clearItemSummary(ctx, itemID)
}

func (s *Store) clearItemSummary(ctx context.Context, itemID int64) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		return withAuthoritativeWriteTx(ctx, s, "clear-item-summary", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
			cleared, err := s.invalidateItemSummaryTx(ctx, tx, itemID, time.Now().UTC().Format(time.RFC3339))
			if err != nil {
				return struct{}{}, err
			}
			if !cleared {
				return struct{}{}, nil
			}
			if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, nil
		})
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
	if err := s.deleteItemEnrichmentTx(ctx, tx, itemID, model.ItemEnrichmentRoleSummary); err != nil {
		return false, err
	}
	return true, nil
}
