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

const itemEnrichmentSelectColumns = `
	id, item_id, role, status, text, raw_json, error, model, prompt_version,
	tool, tool_version, input_hash, completed_at, created_at, updated_at`

func (s *Store) GetItemEnrichment(ctx context.Context, itemID int64, role string) (model.ItemEnrichment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+itemEnrichmentSelectColumns+`
		FROM item_enrichments
		WHERE item_id = ? AND role = ?`,
		itemID,
		strings.TrimSpace(role),
	)
	return scanItemEnrichment(row)
}

func (s *Store) applyItemEnrichmentMirror(ctx context.Context, item *model.Item) error {
	return applyItemEnrichmentMirrorFrom(ctx, s.db, item)
}

func (s *Store) applyItemEnrichmentMirrorToItems(ctx context.Context, items []model.Item) error {
	for i := range items {
		if err := s.applyItemEnrichmentMirror(ctx, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

type itemEnrichmentQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func applyItemEnrichmentMirrorFrom(ctx context.Context, queryer itemEnrichmentQueryer, item *model.Item) error {
	if item == nil || item.ID == 0 {
		return nil
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT `+itemEnrichmentSelectColumns+`
		FROM item_enrichments
		WHERE item_id = ?`,
		item.ID,
	)
	if err != nil {
		return fmt.Errorf("load item enrichments %d: %w", item.ID, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		enrichment, err := scanItemEnrichment(rows)
		if err != nil {
			return fmt.Errorf("scan item enrichment %d: %w", item.ID, err)
		}
		applyItemEnrichmentToItem(item, enrichment)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate item enrichments %d: %w", item.ID, err)
	}
	return nil
}

func applyItemEnrichmentToItem(item *model.Item, enrichment model.ItemEnrichment) {
	switch strings.TrimSpace(enrichment.Role) {
	case model.ItemEnrichmentRoleSummary:
		item.SummaryText = enrichment.Text
		item.SummaryJSON = enrichment.RawJSON
		item.SummaryStatus = enrichment.Status
		item.SummaryError = enrichment.Error
		item.SummaryModel = enrichment.Model
		item.SummaryPromptVersion = enrichment.PromptVersion
		item.SummaryTool = enrichment.Tool
		item.SummaryToolVersion = enrichment.ToolVersion
		item.SummaryInputHash = enrichment.InputHash
		item.SummarizedAt = enrichment.CompletedAt
	case model.ItemEnrichmentRoleOCR:
		item.OCRText = enrichment.Text
		item.OCRJSON = enrichment.RawJSON
		item.OCRStatus = enrichment.Status
		item.OCRError = enrichment.Error
		item.OCRModel = enrichment.Model
		item.OCRTool = enrichment.Tool
		item.OCRToolVersion = enrichment.ToolVersion
		item.OCRInputHash = enrichment.InputHash
		item.OCRAt = enrichment.CompletedAt
	case model.ItemEnrichmentRoleXMediaTranscript:
		item.XMediaTranscriptStatus = enrichment.Status
		item.XMediaTranscriptError = enrichment.Error
		item.XMediaTranscriptAt = enrichment.CompletedAt
		if strings.TrimSpace(enrichment.Text) != "" || strings.TrimSpace(item.ArticleTitle) == model.XMediaTranscriptArticleTitle {
			item.ArticleTitle = model.XMediaTranscriptArticleTitle
			item.ArticleText = enrichment.Text
		}
	}
}

func scanItemEnrichment(row interface {
	Scan(dest ...any) error
}) (model.ItemEnrichment, error) {
	var enrichment model.ItemEnrichment
	var completedAt string
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&enrichment.ID,
		&enrichment.ItemID,
		&enrichment.Role,
		&enrichment.Status,
		&enrichment.Text,
		&enrichment.RawJSON,
		&enrichment.Error,
		&enrichment.Model,
		&enrichment.PromptVersion,
		&enrichment.Tool,
		&enrichment.ToolVersion,
		&enrichment.InputHash,
		&completedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.ItemEnrichment{}, err
	}
	enrichment.CompletedAt = parseStoredTime(completedAt)
	enrichment.CreatedAt = parseStoredTime(createdAt)
	enrichment.UpdatedAt = parseStoredTime(updatedAt)
	return enrichment, nil
}

func (s *Store) upsertItemEnrichmentTx(ctx context.Context, tx *sql.Tx, enrichment model.ItemEnrichment) error {
	role := strings.TrimSpace(enrichment.Role)
	if enrichment.ItemID == 0 || role == "" {
		return fmt.Errorf("item enrichment requires item_id and role")
	}
	enrichment.Role = role
	enrichment.Status = strings.TrimSpace(enrichment.Status)
	enrichment.InputHash = strings.TrimSpace(enrichment.InputHash)
	completedAt := formatTimeForDB(enrichment.CompletedAt)
	existing, err := getItemEnrichmentTx(ctx, tx, enrichment.ItemID, role)
	if err == nil && itemEnrichmentSemanticallyEqual(existing, enrichment) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load item enrichment %d/%s before upsert: %w", enrichment.ItemID, role, err)
	}

	nowText := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `
		UPDATE item_enrichments
		SET status = ?,
			text = ?,
			raw_json = ?,
			error = ?,
			model = ?,
			prompt_version = ?,
			tool = ?,
			tool_version = ?,
			input_hash = ?,
			completed_at = ?,
			updated_at = ?
		WHERE item_id = ? AND role = ?`,
		enrichment.Status,
		enrichment.Text,
		enrichment.RawJSON,
		enrichment.Error,
		enrichment.Model,
		enrichment.PromptVersion,
		enrichment.Tool,
		enrichment.ToolVersion,
		enrichment.InputHash,
		completedAt,
		nowText,
		enrichment.ItemID,
		role,
	)
	if err != nil {
		return fmt.Errorf("update item enrichment %d/%s: %w", enrichment.ItemID, role, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check item enrichment update %d/%s: %w", enrichment.ItemID, role, err)
	}
	if rows > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO item_enrichments (
			item_id, role, status, text, raw_json, error, model, prompt_version,
			tool, tool_version, input_hash, completed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		enrichment.ItemID,
		role,
		enrichment.Status,
		enrichment.Text,
		enrichment.RawJSON,
		enrichment.Error,
		enrichment.Model,
		enrichment.PromptVersion,
		enrichment.Tool,
		enrichment.ToolVersion,
		enrichment.InputHash,
		completedAt,
		nowText,
		nowText,
	); err != nil {
		return fmt.Errorf("insert item enrichment %d/%s: %w", enrichment.ItemID, role, err)
	}
	return nil
}

func getItemEnrichmentTx(ctx context.Context, tx *sql.Tx, itemID int64, role string) (model.ItemEnrichment, error) {
	return scanItemEnrichment(tx.QueryRowContext(ctx, `
		SELECT `+itemEnrichmentSelectColumns+`
		FROM item_enrichments
		WHERE item_id = ? AND role = ?`, itemID, strings.TrimSpace(role)))
}

func itemEnrichmentSemanticallyEqual(existing model.ItemEnrichment, next model.ItemEnrichment) bool {
	return existing.ItemID == next.ItemID &&
		strings.TrimSpace(existing.Role) == strings.TrimSpace(next.Role) &&
		existing.Status == next.Status &&
		existing.Text == next.Text &&
		existing.RawJSON == next.RawJSON &&
		existing.Error == next.Error &&
		existing.Model == next.Model &&
		existing.PromptVersion == next.PromptVersion &&
		existing.Tool == next.Tool &&
		existing.ToolVersion == next.ToolVersion &&
		existing.InputHash == next.InputHash &&
		formatTimeForDB(existing.CompletedAt) == formatTimeForDB(next.CompletedAt)
}

func (s *Store) mergeCompatibilityItemEnrichmentTx(ctx context.Context, tx *sql.Tx, incoming model.ItemEnrichment) error {
	existing, err := getItemEnrichmentTx(ctx, tx, incoming.ItemID, incoming.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return s.upsertItemEnrichmentTx(ctx, tx, incoming)
	}
	if err != nil {
		return fmt.Errorf("load compatibility item enrichment %d/%s: %w", incoming.ItemID, strings.TrimSpace(incoming.Role), err)
	}
	preserve := func(next *string, current string) {
		if strings.TrimSpace(*next) == "" && strings.TrimSpace(current) != "" {
			*next = current
		}
	}
	preserve(&incoming.Status, existing.Status)
	preserve(&incoming.Text, existing.Text)
	preserve(&incoming.RawJSON, existing.RawJSON)
	preserve(&incoming.Error, existing.Error)
	preserve(&incoming.Model, existing.Model)
	preserve(&incoming.PromptVersion, existing.PromptVersion)
	preserve(&incoming.Tool, existing.Tool)
	preserve(&incoming.ToolVersion, existing.ToolVersion)
	preserve(&incoming.InputHash, existing.InputHash)
	if incoming.CompletedAt.IsZero() && !existing.CompletedAt.IsZero() {
		incoming.CompletedAt = existing.CompletedAt
	}
	return s.upsertItemEnrichmentTx(ctx, tx, incoming)
}

func (s *Store) deleteItemEnrichmentTx(ctx context.Context, tx *sql.Tx, itemID int64, role string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_enrichments WHERE item_id = ? AND role = ?`, itemID, strings.TrimSpace(role)); err != nil {
		return fmt.Errorf("delete item enrichment %d/%s: %w", itemID, strings.TrimSpace(role), err)
	}
	return nil
}

func (s *Store) syncItemEnrichmentMirrorTx(ctx context.Context, tx *sql.Tx, itemID int64, item model.Item) error {
	if itemHasSummaryEnrichment(item) {
		if err := s.mergeCompatibilityItemEnrichmentTx(ctx, tx, model.ItemEnrichment{
			ItemID:        itemID,
			Role:          model.ItemEnrichmentRoleSummary,
			Status:        item.SummaryStatus,
			Text:          item.SummaryText,
			RawJSON:       item.SummaryJSON,
			Error:         item.SummaryError,
			Model:         item.SummaryModel,
			PromptVersion: item.SummaryPromptVersion,
			Tool:          item.SummaryTool,
			ToolVersion:   item.SummaryToolVersion,
			InputHash:     item.SummaryInputHash,
			CompletedAt:   item.SummarizedAt,
		}); err != nil {
			return err
		}
	} else if err := s.deleteItemEnrichmentTx(ctx, tx, itemID, model.ItemEnrichmentRoleSummary); err != nil {
		return err
	}

	if itemHasOCREnrichment(item) {
		if err := s.mergeCompatibilityItemEnrichmentTx(ctx, tx, model.ItemEnrichment{
			ItemID:      itemID,
			Role:        model.ItemEnrichmentRoleOCR,
			Status:      item.OCRStatus,
			Text:        item.OCRText,
			RawJSON:     item.OCRJSON,
			Error:       item.OCRError,
			Model:       item.OCRModel,
			Tool:        item.OCRTool,
			ToolVersion: item.OCRToolVersion,
			InputHash:   item.OCRInputHash,
			CompletedAt: item.OCRAt,
		}); err != nil {
			return err
		}
	} else if err := s.deleteItemEnrichmentTx(ctx, tx, itemID, model.ItemEnrichmentRoleOCR); err != nil {
		return err
	}

	if itemHasXMediaTranscriptEnrichment(item) {
		status, errorText, completedAt, err := s.loadXMediaTranscriptStateTx(ctx, tx, itemID)
		if err != nil {
			return err
		}
		text := ""
		if strings.TrimSpace(item.ArticleTitle) == model.XMediaTranscriptArticleTitle {
			text = item.ArticleText
		}
		if err := s.mergeCompatibilityItemEnrichmentTx(ctx, tx, model.ItemEnrichment{
			ItemID:      itemID,
			Role:        model.ItemEnrichmentRoleXMediaTranscript,
			Status:      status,
			Text:        text,
			Error:       errorText,
			CompletedAt: completedAt,
		}); err != nil {
			return err
		}
	} else if err := s.deleteItemEnrichmentTx(ctx, tx, itemID, model.ItemEnrichmentRoleXMediaTranscript); err != nil {
		return err
	}
	return nil
}

func (s *Store) loadXMediaTranscriptStateTx(ctx context.Context, tx *sql.Tx, itemID int64) (string, string, time.Time, error) {
	var status string
	var errorText string
	var atText string
	if err := tx.QueryRowContext(ctx, `
		SELECT x_media_transcript_status, x_media_transcript_error, x_media_transcript_at
		FROM items
		WHERE id = ?`, itemID).Scan(&status, &errorText, &atText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", time.Time{}, fmt.Errorf("item not found for x media transcript enrichment: %d", itemID)
		}
		return "", "", time.Time{}, fmt.Errorf("load x media transcript enrichment state %d: %w", itemID, err)
	}
	return status, errorText, parseStoredTime(atText), nil
}

func itemHasSummaryEnrichment(item model.Item) bool {
	return strings.TrimSpace(item.SummaryText) != "" ||
		strings.TrimSpace(item.SummaryJSON) != "" ||
		strings.TrimSpace(item.SummaryStatus) != "" ||
		strings.TrimSpace(item.SummaryError) != "" ||
		strings.TrimSpace(item.SummaryModel) != "" ||
		strings.TrimSpace(item.SummaryTool) != ""
}

func itemHasOCREnrichment(item model.Item) bool {
	return strings.TrimSpace(item.OCRText) != "" ||
		strings.TrimSpace(item.OCRJSON) != "" ||
		strings.TrimSpace(item.OCRStatus) != "" ||
		strings.TrimSpace(item.OCRError) != "" ||
		strings.TrimSpace(item.OCRModel) != "" ||
		strings.TrimSpace(item.OCRTool) != ""
}

func itemHasXMediaTranscriptEnrichment(item model.Item) bool {
	return strings.TrimSpace(item.ArticleTitle) == model.XMediaTranscriptArticleTitle ||
		strings.TrimSpace(item.XMediaTranscriptStatus) != "" ||
		strings.TrimSpace(item.XMediaTranscriptError) != ""
}
