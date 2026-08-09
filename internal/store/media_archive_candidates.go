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
		WHERE ` + mediaArchiveCandidateWhere("a", force) + `
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
		WHERE download_status = '`+model.MediaDownloadStatusDownloaded+`'
			AND local_path != ''
			AND archive_status = '`+model.MediaArchiveStatusArchived+`'
			AND local_pruned_at = ''
			AND `+mediaArchiveSupportedOwnerExistsWhere("media_assets")+`
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
