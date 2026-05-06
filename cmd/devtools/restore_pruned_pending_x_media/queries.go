package main

import (
	"database/sql"
	"fmt"
)

const pendingTranscriptQuery = `
SELECT DISTINCT items.id
FROM items
WHERE items.source_type IN ('x_bookmark', 'x_quote')
	AND items.external_id != ''
	AND EXISTS (
		SELECT 1
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = items.id
			AND a.download_status = 'downloaded'
			AND a.media_type IN ('video', 'animated_gif')
	)
	AND (
		items.article_text = ''
		OR items.article_title = 'X Media Transcript'
		OR items.x_media_transcript_status != ''
	)
	AND NOT (items.article_title = 'X Media Transcript' AND items.article_text != '')
	AND items.x_media_transcript_status = ''
	AND NOT EXISTS (
		SELECT 1
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = items.id
			AND a.download_status = 'downloaded'
			AND a.local_path != ''
			AND a.local_pruned_at = ''
			AND a.media_type IN ('video', 'animated_gif')
	)
ORDER BY items.id
LIMIT ?`

const pendingOCRQuery = `
SELECT DISTINCT items.id
FROM items
WHERE items.source_type IN ('x_bookmark', 'x_quote')
	AND items.external_id != ''
	AND EXISTS (
		SELECT 1
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = items.id
			AND a.download_status = 'downloaded'
			AND a.media_type = 'photo'
	)
	AND (items.ocr_status = '' OR items.ocr_status = 'error')
	AND NOT EXISTS (
		SELECT 1
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = items.id
			AND a.download_status = 'downloaded'
			AND a.local_path != ''
			AND a.local_pruned_at = ''
			AND a.media_type = 'photo'
	)
ORDER BY items.id
LIMIT ?`

func loadIDs(db *sql.DB, query string, limit int) ([]int64, error) {
	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending ids: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan pending id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending ids: %w", err)
	}
	return ids, nil
}
