package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListItemsForXMediaTranscription(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
			AND external_id != ''
			AND ` + xMediaTranscriptionRunnableMediaExistsWhere
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
