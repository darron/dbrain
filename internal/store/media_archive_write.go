package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

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
