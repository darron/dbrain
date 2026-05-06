package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

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
