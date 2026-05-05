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

func (s *Store) ListMediaAssetsForDownload(ctx context.Context, limit int, force bool) ([]model.MediaAsset, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + mediaSelectColumns + `
		FROM media_assets
		WHERE remote_url != ''`
	if !force {
		query += `
			AND ` + mediaDownloadRetryableWhere("")
	}
	query += `
		ORDER BY
			CASE download_status
				WHEN '` + model.MediaDownloadStatusPending + `' THEN 0
				WHEN '' THEN 1
				WHEN '` + model.MediaDownloadStatusError + `' THEN 2
				ELSE 3
			END,
			discovered_at ASC,
			id ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list media assets for download: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var assets []model.MediaAsset
	for rows.Next() {
		var asset model.MediaAsset
		if err := scanMediaAsset(rows.Scan, &asset); err != nil {
			return nil, fmt.Errorf("scan media asset: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media assets: %w", err)
	}

	return assets, nil
}

func (s *Store) SaveMediaDownload(ctx context.Context, assetID int64, result model.MediaDownloadResult) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		row := s.db.QueryRowContext(ctx, `
			SELECT mime_type, byte_size, content_hash, local_path, download_status, download_error, download_error_count, last_download_attempt_at, downloaded_at, local_pruned_at
			FROM media_assets
			WHERE id = ?`, assetID)

		var currentMIME string
		var currentByteSize int64
		var currentHash string
		var currentPath string
		var currentStatus string
		var currentError string
		var currentErrorCount int
		var currentLastAttemptAt string
		var currentDownloadedAt string
		var currentLocalPrunedAt string
		if err := row.Scan(&currentMIME, &currentByteSize, &currentHash, &currentPath, &currentStatus, &currentError, &currentErrorCount, &currentLastAttemptAt, &currentDownloadedAt, &currentLocalPrunedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("media asset not found: %d", assetID)
			}
			return false, fmt.Errorf("load media asset %d: %w", assetID, err)
		}

		attemptedAt := result.AttemptedAt
		if attemptedAt.IsZero() {
			attemptedAt = time.Now().UTC()
		} else {
			attemptedAt = attemptedAt.UTC()
		}
		lastAttemptAt := attemptedAt.Format(time.RFC3339)

		nextStatus := result.Status
		nextError := result.Error
		nextErrorCount := currentErrorCount
		nextLastAttemptAt := currentLastAttemptAt
		switch nextStatus {
		case model.MediaDownloadStatusError:
			nextErrorCount = currentErrorCount + 1
			nextLastAttemptAt = lastAttemptAt
			if nextErrorCount >= model.MediaDownloadMaxConsecutiveErrors {
				nextStatus = model.MediaDownloadStatusBlocked
				nextError = terminalMediaDownloadError(nextErrorCount, nextError)
			}
		case model.MediaDownloadStatusBlocked:
			nextLastAttemptAt = lastAttemptAt
			if nextErrorCount < model.MediaDownloadMaxConsecutiveErrors {
				nextErrorCount = model.MediaDownloadMaxConsecutiveErrors
			}
			nextError = terminalMediaDownloadError(nextErrorCount, nextError)
		case model.MediaDownloadStatusDownloaded, model.MediaDownloadStatusGone:
			nextErrorCount = 0
			nextLastAttemptAt = lastAttemptAt
		}

		downloadedAt := ""
		if !result.DownloadedAt.IsZero() {
			downloadedAt = result.DownloadedAt.UTC().Format(time.RFC3339)
		}
		nextLocalPrunedAt := currentLocalPrunedAt
		if result.Status == model.MediaDownloadStatusDownloaded && strings.TrimSpace(result.LocalPath) != "" {
			nextLocalPrunedAt = ""
		}

		changed := currentMIME != result.MIMEType ||
			currentByteSize != result.ByteSize ||
			currentHash != result.ContentHash ||
			currentPath != result.LocalPath ||
			currentStatus != nextStatus ||
			currentError != nextError ||
			currentErrorCount != nextErrorCount ||
			currentLastAttemptAt != nextLastAttemptAt ||
			currentDownloadedAt != downloadedAt ||
			currentLocalPrunedAt != nextLocalPrunedAt
		if !changed {
			return false, nil
		}

		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_assets
			SET mime_type = ?,
				byte_size = ?,
				content_hash = ?,
				local_path = ?,
				download_status = ?,
				download_error = ?,
				download_error_count = ?,
				last_download_attempt_at = ?,
				downloaded_at = ?,
				local_pruned_at = ?,
				updated_at = ?
			WHERE id = ?`,
			result.MIMEType,
			result.ByteSize,
			result.ContentHash,
			result.LocalPath,
			nextStatus,
			nextError,
			nextErrorCount,
			nextLastAttemptAt,
			downloadedAt,
			nextLocalPrunedAt,
			time.Now().UTC().Format(time.RFC3339),
			assetID,
		); err != nil {
			return false, fmt.Errorf("save media download %d: %w", assetID, err)
		}

		return true, nil
	})
}

func scanMediaAsset(scan func(dest ...any) error, asset *model.MediaAsset) error {
	var discoveredAt string
	var downloadedAt string
	var archivedAt string
	var localPrunedAt string
	var lastDownloadAt string
	var updatedAt string
	if err := scan(
		&asset.ID,
		&asset.RemoteURL,
		&asset.MediaType,
		&asset.MIMEType,
		&asset.Width,
		&asset.Height,
		&asset.ByteSize,
		&asset.ContentHash,
		&asset.DownloadStatus,
		&asset.DownloadError,
		&asset.DownloadErrors,
		&lastDownloadAt,
		&asset.LocalPath,
		&asset.ArchiveProvider,
		&asset.ArchiveBucket,
		&asset.ArchiveKey,
		&asset.ArchiveURL,
		&asset.ArchiveETag,
		&asset.ArchiveStatus,
		&asset.ArchiveError,
		&discoveredAt,
		&downloadedAt,
		&archivedAt,
		&localPrunedAt,
		&updatedAt,
	); err != nil {
		return err
	}
	asset.DiscoveredAt = parseStoredTime(discoveredAt)
	asset.LastDownloadAt = parseStoredTime(lastDownloadAt)
	asset.DownloadedAt = parseStoredTime(downloadedAt)
	asset.ArchivedAt = parseStoredTime(archivedAt)
	asset.LocalPrunedAt = parseStoredTime(localPrunedAt)
	asset.UpdatedAt = parseStoredTime(updatedAt)
	return nil
}

func terminalMediaDownloadError(count int, errText string) string {
	errText = strings.TrimSpace(errText)
	prefix := fmt.Sprintf("blocked after %d failed media download attempts", count)
	if errText == "" {
		return prefix
	}
	if strings.HasPrefix(errText, "blocked after ") {
		return errText
	}
	return prefix + ": " + errText
}
