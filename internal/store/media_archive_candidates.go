package store

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListMediaAssetsForArchive(ctx context.Context, limit int, force bool) ([]model.MediaAsset, error) {
	if limit <= 0 {
		limit = 5000
	}

	query := `
		SELECT ` + mediaSelectColumns + `
		FROM media_assets a
		WHERE a.download_status = 'downloaded'
			AND a.local_path != ''
			AND a.local_pruned_at = ''`
	if !force {
		query += `
			AND (a.archive_status = '' OR a.archive_status = 'error')`
	}
	query += `
			AND EXISTS (
				SELECT 1
				FROM item_media_links l
				WHERE l.media_asset_id = a.id
			)
			AND (
				(
					a.media_type = 'photo'
					AND NOT EXISTS (
						SELECT 1
						FROM item_media_links l
						JOIN items i ON i.id = l.item_id
						WHERE l.media_asset_id = a.id
							AND i.ocr_status != '` + model.ItemOCRStatusOK + `'
					)
				)
				OR
				(
					a.media_type IN ('video', 'animated_gif')
					AND NOT EXISTS (
						SELECT 1
						FROM item_media_links l
						JOIN items i ON i.id = l.item_id
						WHERE l.media_asset_id = a.id
							AND i.x_media_transcript_status NOT IN ('` + model.XMediaTranscriptStatusOK + `', '` + model.XMediaTranscriptStatusNoAudio + `', '` + model.XMediaTranscriptStatusNoise + `', '` + model.XMediaTranscriptStatusTooShort + `', '` + model.XMediaTranscriptStatusEmpty + `')
					)
				)
			)
		ORDER BY a.media_type ASC, a.downloaded_at ASC, a.id ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list media assets for archive: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var assets []model.MediaAsset
	for rows.Next() {
		var asset model.MediaAsset
		if err := scanMediaAsset(rows.Scan, &asset); err != nil {
			return nil, fmt.Errorf("scan media asset for archive: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media assets for archive: %w", err)
	}
	return assets, nil
}

func (s *Store) ListMediaAssetsForPrune(ctx context.Context, limit int) ([]model.MediaAsset, error) {
	if limit <= 0 {
		limit = 5000
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+mediaSelectColumns+`
		FROM media_assets
		WHERE download_status = 'downloaded'
			AND local_path != ''
			AND archive_status = 'archived'
			AND local_pruned_at = ''
		ORDER BY local_path ASC, id ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list media assets for prune: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var assets []model.MediaAsset
	for rows.Next() {
		var asset model.MediaAsset
		if err := scanMediaAsset(rows.Scan, &asset); err != nil {
			return nil, fmt.Errorf("scan media asset for prune: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media assets for prune: %w", err)
	}
	return assets, nil
}
