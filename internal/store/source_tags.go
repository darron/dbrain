package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) SaveSourceUserTags(ctx context.Context, sourceID int64, tags string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source tag tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `UPDATE sources SET user_tags = ?, updated_at = ? WHERE id = ?`, tags, time.Now().UTC().Format(time.RFC3339), sourceID); err != nil {
		return fmt.Errorf("update user_tags for source %d: %w", sourceID, err)
	}
	if err = s.syncSourceFTSByIDTx(ctx, tx, sourceID); err != nil {
		return fmt.Errorf("sync fts for source %d: %w", sourceID, err)
	}
	return tx.Commit()
}
