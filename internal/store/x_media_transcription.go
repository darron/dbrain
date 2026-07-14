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

func (s *Store) ListItemsForXMediaTranscription(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	pendingWhere, pendingArgs := xMediaTranscriptionPendingWhere(time.Now().UTC())

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
			AND external_id != ''
			AND ` + xMediaTranscriptionRunnableMediaExistsWhere
	if !force {
		query += ` AND ` + pendingWhere
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	args := append([]any{}, pendingArgs...)
	if force {
		args = nil
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
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
	if err := s.applyItemEnrichmentMirrorToItems(ctx, items); err != nil {
		return nil, err
	}

	return items, nil
}

type XMediaTranscriptionState struct {
	Status, Error, RawJSON, Model, Tool, ToolVersion, InputHash string
	CompletedAt                                                 time.Time
}

func (s *Store) SaveXMediaTranscriptionState(ctx context.Context, itemID int64, status string, errorText string, at time.Time) error {
	return s.SaveXMediaTranscription(ctx, itemID, XMediaTranscriptionState{Status: status, Error: errorText, CompletedAt: at})
}

func (s *Store) SaveXMediaTranscription(ctx context.Context, itemID int64, state XMediaTranscriptionState) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return struct{}{}, fmt.Errorf("begin x media transcription state tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		atText := ""
		if !state.CompletedAt.IsZero() {
			atText = state.CompletedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE items
			SET x_media_transcript_status = ?,
				x_media_transcript_error = ?,
				x_media_transcript_at = ?,
				updated_at = ?
			WHERE id = ?`,
			strings.TrimSpace(state.Status),
			strings.TrimSpace(state.Error),
			atText,
			time.Now().UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return struct{}{}, fmt.Errorf("save x media transcription state %d: %w", itemID, err)
		}

		var articleTitle string
		var articleText string
		if err := tx.QueryRowContext(ctx, `SELECT article_title, article_text FROM items WHERE id = ?`, itemID).Scan(&articleTitle, &articleText); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return struct{}{}, fmt.Errorf("item not found for x media transcription state: %d", itemID)
			}
			return struct{}{}, fmt.Errorf("load x media transcript text %d: %w", itemID, err)
		}
		text := ""
		if strings.TrimSpace(articleTitle) == model.XMediaTranscriptArticleTitle {
			text = articleText
		}
		if err := s.upsertItemEnrichmentTx(ctx, tx, model.ItemEnrichment{
			ItemID:      itemID,
			Role:        model.ItemEnrichmentRoleXMediaTranscript,
			Status:      strings.TrimSpace(state.Status),
			Text:        text,
			RawJSON:     state.RawJSON,
			Error:       strings.TrimSpace(state.Error),
			Model:       state.Model,
			Tool:        state.Tool,
			ToolVersion: state.ToolVersion,
			InputHash:   state.InputHash,
			CompletedAt: state.CompletedAt,
		}); err != nil {
			return struct{}{}, err
		}
		if err := tx.Commit(); err != nil {
			return struct{}{}, fmt.Errorf("commit x media transcription state %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}
