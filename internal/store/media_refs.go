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
			a.download_error_count,
			a.last_download_attempt_at,
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

// ListMastodonMediaRefsForDownload returns retryable media linked from
// Mastodon records. Keeping the source filter in SQL prevents a Mastodon run
// from accidentally retrying unrelated X or Bluesky media.
func (s *Store) ListMastodonMediaRefsForDownload(ctx context.Context, limit int, force bool) ([]model.ItemMediaRef, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT
			l.item_id,
			l.media_asset_id,
			l.ordinal,
			l.expanded_url,
			a.remote_url,
			a.media_type,
			a.download_status,
			a.download_error_count,
			a.last_download_attempt_at,
			a.local_path,
			a.archive_provider,
			a.archive_bucket,
			a.archive_key,
			a.archive_url,
			a.archive_status,
			a.width,
			a.height,
			a.local_pruned_at
		FROM items i
		JOIN item_media_links l ON l.item_id = i.id
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE i.source_type IN ('mastodon_bookmark', 'mastodon_quote', 'mastodon_reblog')
			AND a.remote_url != ''
			AND NOT EXISTS (
				SELECT 1
				FROM item_media_links prior_link
				JOIN items prior_item ON prior_item.id = prior_link.item_id
				WHERE prior_link.media_asset_id = l.media_asset_id
					AND prior_item.source_type IN ('mastodon_bookmark', 'mastodon_quote', 'mastodon_reblog')
					AND (
						prior_item.id < i.id
						OR (prior_item.id = i.id AND prior_link.ordinal < l.ordinal)
					)
			)`
	if !force {
		query += `
			AND ` + mediaDownloadRetryableWhere("a")
	} else {
		query += `
			AND a.download_status IN ('', '` + model.MediaDownloadStatusPending + `', '` + model.MediaDownloadStatusError + `', '` + model.MediaDownloadStatusBlocked + `')`
	}
	if force {
		query += `
			ORDER BY
				CASE WHEN a.last_download_attempt_at = '' THEN 0 ELSE 1 END,
				a.last_download_attempt_at ASC,
				CASE a.download_status
					WHEN '` + model.MediaDownloadStatusPending + `' THEN 0
					WHEN '' THEN 1
					WHEN '` + model.MediaDownloadStatusError + `' THEN 2
					WHEN '` + model.MediaDownloadStatusBlocked + `' THEN 3
					ELSE 4
				END,
				a.discovered_at ASC, a.id ASC, i.id ASC, l.ordinal ASC
			LIMIT ?`
	} else {
		query += `
			ORDER BY a.discovered_at ASC, a.id ASC, i.id ASC, l.ordinal ASC
			LIMIT ?`
	}
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list Mastodon media refs for download: %w", err)
	}
	defer func() { _ = rows.Close() }()

	refs := make([]model.ItemMediaRef, 0, limit)
	for rows.Next() {
		var ref model.ItemMediaRef
		if err := scanItemMediaRef(rows.Scan, &ref); err != nil {
			return nil, fmt.Errorf("scan Mastodon media ref for download: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Mastodon media refs for download: %w", err)
	}
	return refs, nil
}

func (s *Store) ListItemMediaRefsForSourceKeys(ctx context.Context, sourceKeys []string) (map[string][]model.ItemMediaRef, error) {
	seen := make(map[string]struct{}, len(sourceKeys))
	keys := make([]string, 0, len(sourceKeys))
	for _, key := range sourceKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	out := make(map[string][]model.ItemMediaRef, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		placeholders = append(placeholders, "?")
		args = append(args, key)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			i.source_key,
			l.item_id,
			l.media_asset_id,
			l.ordinal,
			l.expanded_url,
			a.remote_url,
			a.media_type,
			a.download_status,
			a.download_error_count,
			a.last_download_attempt_at,
			a.local_path,
			a.archive_provider,
			a.archive_bucket,
			a.archive_key,
			a.archive_url,
			a.archive_status,
			a.width,
			a.height,
			a.local_pruned_at
		FROM items i
		JOIN item_media_links l ON l.item_id = i.id
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE i.source_key IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY i.source_key ASC, l.ordinal ASC, l.media_asset_id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list item media refs for source keys: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var sourceKey string
		var ref model.ItemMediaRef
		var localPrunedAt string
		var lastDownloadAt string
		if err := rows.Scan(
			&sourceKey,
			&ref.ItemID,
			&ref.MediaAssetID,
			&ref.Ordinal,
			&ref.ExpandedURL,
			&ref.RemoteURL,
			&ref.MediaType,
			&ref.DownloadStatus,
			&ref.DownloadErrors,
			&lastDownloadAt,
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
			return nil, fmt.Errorf("scan item media refs for source keys: %w", err)
		}
		ref.LastDownloadAt = parseStoredTime(lastDownloadAt)
		ref.LocalPrunedAt = parseStoredTime(localPrunedAt)
		out[sourceKey] = append(out[sourceKey], ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item media refs for source keys: %w", err)
	}
	return out, nil
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
			a.download_error_count,
			a.last_download_attempt_at,
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

func desiredItemMediaRefs(itemID int64, media []model.MediaCandidate) []model.ItemMediaRef {
	seen := make(map[string]struct{}, len(media))
	refs := make([]model.ItemMediaRef, 0, len(media))
	for _, candidate := range media {
		url := strings.TrimSpace(candidate.RemoteURL)
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
			MediaType:   strings.TrimSpace(candidate.MediaType),
			Width:       candidate.Width,
			Height:      candidate.Height,
		})
	}
	return refs
}

func scanItemMediaRef(scan func(dest ...any) error, ref *model.ItemMediaRef) error {
	var localPrunedAt string
	var lastDownloadAt string
	if err := scan(
		&ref.ItemID,
		&ref.MediaAssetID,
		&ref.Ordinal,
		&ref.ExpandedURL,
		&ref.RemoteURL,
		&ref.MediaType,
		&ref.DownloadStatus,
		&ref.DownloadErrors,
		&lastDownloadAt,
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
	ref.LastDownloadAt = parseStoredTime(lastDownloadAt)
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
