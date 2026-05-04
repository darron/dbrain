package store

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListItemsForXMediaSummary(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
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
		WHERE ` + xItemSourceTypeWhere + `
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

func (s *Store) ListItemsForXPhotoOCRAudit(ctx context.Context, limit int, includePruned bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
			AND external_id != ''
			AND EXISTS (
				SELECT 1
				FROM item_media_links l
				JOIN media_assets a ON a.id = l.media_asset_id
				WHERE l.item_id = items.id
					AND a.download_status = 'downloaded'
					AND a.local_path != ''
					AND a.media_type = 'photo'`
	if !includePruned {
		query += `
					AND a.local_pruned_at = ''`
	}
	query += `
			)
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x photo ocr audit items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x photo ocr audit item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x photo ocr audit items: %w", err)
	}
	return items, nil
}
