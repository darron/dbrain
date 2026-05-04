package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListItemMediaRefs(ctx context.Context, itemID int64) ([]model.ItemMediaRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			l.item_id,
			l.media_asset_id,
			l.ordinal,
			l.expanded_url,
			a.remote_url,
			a.media_type,
			a.download_status,
			a.local_path,
			a.archive_provider,
			a.archive_bucket,
			a.archive_key,
			a.archive_url,
			a.archive_status,
			a.width,
			a.height,
			a.local_pruned_at
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = ?
		ORDER BY l.ordinal ASC, l.media_asset_id ASC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list item media refs for item %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.ItemMediaRef
	for rows.Next() {
		var ref model.ItemMediaRef
		if err := scanItemMediaRef(rows.Scan, &ref); err != nil {
			return nil, fmt.Errorf("scan item media ref for item %d: %w", itemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item media refs for item %d: %w", itemID, err)
	}
	return refs, nil
}

type itemMediaLinkRow struct {
	ItemID       int64
	MediaAssetID int64
	Ordinal      int
	ExpandedURL  string
}

func (s *Store) listItemMediaRefsTx(ctx context.Context, tx *sql.Tx, itemID int64) ([]model.ItemMediaRef, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			l.item_id,
			l.media_asset_id,
			l.ordinal,
			l.expanded_url,
			a.remote_url,
			a.media_type,
			a.download_status,
			a.local_path,
			a.archive_provider,
			a.archive_bucket,
			a.archive_key,
			a.archive_url,
			a.archive_status,
			a.width,
			a.height,
			a.local_pruned_at
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = ?
		ORDER BY l.ordinal ASC, l.media_asset_id ASC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list item media refs in tx for item %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.ItemMediaRef
	for rows.Next() {
		var ref model.ItemMediaRef
		if err := scanItemMediaRef(rows.Scan, &ref); err != nil {
			return nil, fmt.Errorf("scan item media ref in tx for item %d: %w", itemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item media refs in tx for item %d: %w", itemID, err)
	}
	return refs, nil
}

func desiredItemMediaRefs(itemID int64, media []xHydrationMedia) []model.ItemMediaRef {
	seen := make(map[string]struct{}, len(media))
	refs := make([]model.ItemMediaRef, 0, len(media))
	for _, candidate := range media {
		url := strings.TrimSpace(candidate.URL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		refs = append(refs, model.ItemMediaRef{
			ItemID:      itemID,
			Ordinal:     len(refs),
			ExpandedURL: strings.TrimSpace(candidate.ExpandedURL),
			RemoteURL:   url,
			MediaType:   strings.TrimSpace(candidate.Type),
			Width:       candidate.Width,
			Height:      candidate.Height,
		})
	}
	return refs
}

func scanItemMediaRef(scan func(dest ...any) error, ref *model.ItemMediaRef) error {
	var localPrunedAt string
	if err := scan(
		&ref.ItemID,
		&ref.MediaAssetID,
		&ref.Ordinal,
		&ref.ExpandedURL,
		&ref.RemoteURL,
		&ref.MediaType,
		&ref.DownloadStatus,
		&ref.LocalPath,
		&ref.ArchiveProvider,
		&ref.ArchiveBucket,
		&ref.ArchiveKey,
		&ref.ArchiveURL,
		&ref.ArchiveStatus,
		&ref.Width,
		&ref.Height,
		&localPrunedAt,
	); err != nil {
		return err
	}
	ref.LocalPrunedAt = parseStoredTime(localPrunedAt)
	return nil
}

func sameItemMediaRefs(current []model.ItemMediaRef, desired []model.ItemMediaRef) bool {
	if len(current) != len(desired) {
		return false
	}
	for i := range current {
		if current[i].Ordinal != desired[i].Ordinal ||
			current[i].ExpandedURL != desired[i].ExpandedURL ||
			current[i].RemoteURL != desired[i].RemoteURL ||
			current[i].MediaType != desired[i].MediaType ||
			current[i].Width != desired[i].Width ||
			current[i].Height != desired[i].Height {
			return false
		}
	}
	return true
}
