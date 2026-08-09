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

func (s *Store) syncXHydrationMediaTx(ctx context.Context, tx *sql.Tx, itemID int64, hydration model.XHydration, now time.Time) (bool, error) {
	snapshot, hasSnapshot, err := decodeXHydrationSnapshot(hydration.APIJSON)
	if err != nil {
		return false, fmt.Errorf("decode media snapshot for item %d: %w", itemID, err)
	}

	switch {
	case hasSnapshot:
		candidates := make([]model.MediaCandidate, 0, len(snapshot.MediaObjects))
		for _, media := range snapshot.MediaObjects {
			candidates = append(candidates, model.MediaCandidate{
				RemoteURL:   media.URL,
				ExpandedURL: media.ExpandedURL,
				MediaType:   media.Type,
				Width:       media.Width,
				Height:      media.Height,
			})
		}
		return s.replaceItemMediaLinksTx(ctx, tx, itemID, candidates, now)
	case hydration.Status == "not_found":
		current, err := s.listItemMediaRefsTx(ctx, tx, itemID)
		if err != nil {
			return false, err
		}
		if len(current) == 0 {
			return false, nil
		}
		if err := s.clearItemMediaLinksTx(ctx, tx, itemID); err != nil {
			return false, err
		}
		if err := s.invalidateItemMediaDerivedTx(ctx, tx, itemID, now.UTC().Format(time.RFC3339)); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (s *Store) replaceItemMediaLinksTx(ctx context.Context, tx *sql.Tx, itemID int64, media []model.MediaCandidate, now time.Time) (bool, error) {
	current, err := s.listItemMediaRefsTx(ctx, tx, itemID)
	if err != nil {
		return false, err
	}

	desired := desiredItemMediaRefs(itemID, media)
	nowText := now.UTC().Format(time.RFC3339)
	linkRows := make([]itemMediaLinkRow, 0, len(desired))
	assetChanged := false

	for _, candidate := range desired {
		assetID, changed, err := s.upsertMediaAssetTx(ctx, tx, model.MediaCandidate{
			MediaType:   candidate.MediaType,
			RemoteURL:   candidate.RemoteURL,
			ExpandedURL: candidate.ExpandedURL,
			Width:       candidate.Width,
			Height:      candidate.Height,
		}, nowText)
		if err != nil {
			return false, err
		}
		if changed {
			assetChanged = true
		}
		linkRows = append(linkRows, itemMediaLinkRow{
			ItemID:       itemID,
			MediaAssetID: assetID,
			Ordinal:      candidate.Ordinal,
			ExpandedURL:  candidate.ExpandedURL,
		})
	}

	linkChanged := !sameItemMediaRefs(current, desired)
	if !linkChanged {
		return assetChanged, nil
	}

	if err := s.clearItemMediaLinksTx(ctx, tx, itemID); err != nil {
		return false, err
	}
	for _, link := range linkRows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item_media_links (
				item_id, media_asset_id, ordinal, expanded_url, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			link.ItemID,
			link.MediaAssetID,
			link.Ordinal,
			link.ExpandedURL,
			nowText,
			nowText,
		); err != nil {
			return false, fmt.Errorf("insert item media link item=%d asset=%d: %w", link.ItemID, link.MediaAssetID, err)
		}
	}
	if err := s.invalidateItemMediaDerivedTx(ctx, tx, itemID, nowText); err != nil {
		return false, err
	}

	return true, nil
}

func (s *Store) clearItemMediaLinksTx(ctx context.Context, tx *sql.Tx, itemID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_media_links WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear item media links %d: %w", itemID, err)
	}
	return nil
}

func (s *Store) upsertMediaAssetTx(ctx context.Context, tx *sql.Tx, media model.MediaCandidate, nowText string) (int64, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, media_type, width, height, download_status, discovered_at
		FROM media_assets
		WHERE remote_url = ?`, media.RemoteURL)

	var (
		assetID       int64
		currentType   string
		currentWidth  int
		currentHeight int
		currentStatus string
		currentSeenAt string
	)
	switch err := row.Scan(&assetID, &currentType, &currentWidth, &currentHeight, &currentStatus, &currentSeenAt); {
	case errors.Is(err, sql.ErrNoRows):
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO media_assets (
				remote_url, media_type, mime_type, width, height, byte_size, content_hash,
				download_status, download_error, local_path, discovered_at, downloaded_at, updated_at
			) VALUES (?, ?, '', ?, ?, 0, '', '`+model.MediaDownloadStatusPending+`', '', '', ?, '', ?)`,
			media.RemoteURL,
			media.MediaType,
			media.Width,
			media.Height,
			nowText,
			nowText,
		)
		if execErr != nil {
			return 0, false, fmt.Errorf("insert media asset %q: %w", media.RemoteURL, execErr)
		}
		insertedID, execErr := result.LastInsertId()
		if execErr != nil {
			return 0, false, fmt.Errorf("fetch inserted media asset id %q: %w", media.RemoteURL, execErr)
		}
		return insertedID, true, nil
	case err != nil:
		return 0, false, fmt.Errorf("load media asset %q: %w", media.RemoteURL, err)
	default:
	}

	nextType := firstNonEmpty(media.MediaType, currentType)
	nextWidth := maxInt(media.Width, currentWidth)
	nextHeight := maxInt(media.Height, currentHeight)
	nextStatus := currentStatus
	if strings.TrimSpace(nextStatus) == "" {
		nextStatus = model.MediaDownloadStatusPending
	}
	nextSeenAt := currentSeenAt
	if strings.TrimSpace(nextSeenAt) == "" {
		nextSeenAt = nowText
	}

	changed := nextType != currentType ||
		nextWidth != currentWidth ||
		nextHeight != currentHeight ||
		nextStatus != currentStatus ||
		nextSeenAt != currentSeenAt
	if !changed {
		return assetID, false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET media_type = ?,
			width = ?,
			height = ?,
			download_status = CASE WHEN download_status = '' THEN '`+model.MediaDownloadStatusPending+`' ELSE download_status END,
			discovered_at = CASE WHEN discovered_at = '' THEN ? ELSE discovered_at END,
			updated_at = ?
		WHERE id = ?`,
		nextType,
		nextWidth,
		nextHeight,
		nowText,
		nowText,
		assetID,
	); err != nil {
		return 0, false, fmt.Errorf("update media asset %d: %w", assetID, err)
	}

	_ = currentStatus
	_ = currentSeenAt
	return assetID, changed, nil
}

// SaveItemMediaCandidates replaces the media references for an item through
// the same transaction path used by X hydration. A valid empty candidate list
// clears stale links; callers with an invalid or incomplete decoder result
// should not call this method.
func (s *Store) SaveItemMediaCandidates(ctx context.Context, itemID int64, candidates []model.MediaCandidate) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		return withAuthoritativeWriteTx(ctx, s, "save-item-media", func(ctx context.Context, tx authoritativeWriteTx) (bool, error) {
			now := time.Now().UTC()
			changed, err := s.replaceItemMediaLinksTx(ctx, tx, itemID, candidates, now)
			if err != nil {
				return false, err
			}
			if changed {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET updated_at = ? WHERE id = ?`, now.Format(time.RFC3339), itemID); err != nil {
					return false, fmt.Errorf("touch item after media sync %d: %w", itemID, err)
				}
			}
			return changed, nil
		})
	})
}
