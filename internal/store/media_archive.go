package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
							AND i.ocr_status != 'ok'
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
							AND i.x_media_transcript_status NOT IN ('ok', 'no_audio', 'noise', 'too_short', 'empty')
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

func (s *Store) ListMediaAssetsByLocalPath(ctx context.Context, localPath string) ([]model.MediaAsset, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+mediaSelectColumns+`
		FROM media_assets
		WHERE local_path = ?
		ORDER BY id ASC`, localPath)
	if err != nil {
		return nil, fmt.Errorf("list media assets by local path %q: %w", localPath, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var assets []model.MediaAsset
	for rows.Next() {
		var asset model.MediaAsset
		if err := scanMediaAsset(rows.Scan, &asset); err != nil {
			return nil, fmt.Errorf("scan media asset by local path %q: %w", localPath, err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media assets by local path %q: %w", localPath, err)
	}
	return assets, nil
}

func (s *Store) GetMediaAsset(ctx context.Context, assetID int64) (model.MediaAsset, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mediaSelectColumns+`
		FROM media_assets
		WHERE id = ?`, assetID)

	var asset model.MediaAsset
	if err := scanMediaAsset(row.Scan, &asset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.MediaAsset{}, fmt.Errorf("media asset not found: %d", assetID)
		}
		return model.MediaAsset{}, fmt.Errorf("get media asset %d: %w", assetID, err)
	}
	return asset, nil
}

func (s *Store) SaveMediaArchive(ctx context.Context, assetID int64, result model.MediaArchiveResult) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		row := s.db.QueryRowContext(ctx, `
			SELECT archive_provider, archive_bucket, archive_key, archive_url, archive_etag, archive_status, archive_error, archived_at
			FROM media_assets
			WHERE id = ?`, assetID)

		var current model.MediaArchiveResult
		var currentArchivedAt string
		if err := row.Scan(
			&current.Provider,
			&current.Bucket,
			&current.Key,
			&current.URL,
			&current.ETag,
			&current.Status,
			&current.Error,
			&currentArchivedAt,
		); err != nil {
			return false, fmt.Errorf("load media archive %d: %w", assetID, err)
		}
		current.ArchivedAt = parseStoredTime(currentArchivedAt)

		changed := current.Provider != result.Provider ||
			current.Bucket != result.Bucket ||
			current.Key != result.Key ||
			current.URL != result.URL ||
			current.ETag != result.ETag ||
			current.Status != result.Status ||
			current.Error != result.Error ||
			!current.ArchivedAt.Equal(result.ArchivedAt)
		if !changed {
			return false, nil
		}

		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_assets
			SET archive_provider = ?,
				archive_bucket = ?,
				archive_key = ?,
				archive_url = ?,
				archive_etag = ?,
				archive_status = ?,
				archive_error = ?,
				archived_at = ?,
				updated_at = ?
			WHERE id = ?`,
			result.Provider,
			result.Bucket,
			result.Key,
			result.URL,
			result.ETag,
			result.Status,
			result.Error,
			formatTimeForDB(result.ArchivedAt),
			time.Now().UTC().Format(time.RFC3339),
			assetID,
		); err != nil {
			return false, fmt.Errorf("save media archive %d: %w", assetID, err)
		}

		return true, nil
	})
}

func (s *Store) MarkMediaLocalPrunedByPath(ctx context.Context, localPath string, prunedAt time.Time) (int64, error) {
	if localPath == "" {
		return 0, nil
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET local_pruned_at = ?,
			updated_at = ?
		WHERE local_path = ?`,
		formatTimeForDB(prunedAt),
		time.Now().UTC().Format(time.RFC3339),
		localPath,
	)
	if err != nil {
		return 0, fmt.Errorf("mark media local pruned %q: %w", localPath, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected media local pruned %q: %w", localPath, err)
	}
	return count, nil
}

func (s *Store) ListItemSourceKeysByMediaLocalPath(ctx context.Context, localPath string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT i.source_key
		FROM items i
		JOIN item_media_links l ON l.item_id = i.id
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE a.local_path = ?
		ORDER BY i.source_key ASC`, localPath)
	if err != nil {
		return nil, fmt.Errorf("list item source keys by media local path %q: %w", localPath, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan item source key by media local path %q: %w", localPath, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item source keys by media local path %q: %w", localPath, err)
	}
	return keys, nil
}
