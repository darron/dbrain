package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrRetrievalPurgeEpochChanged = errors.New("retrieval purge epoch changed")

func (s *Store) RetrievalPurgeEpoch(ctx context.Context) (int64, error) {
	var epoch int64
	if err := s.queryer().QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("read retrieval purge epoch: %w", err)
	}
	return epoch, nil
}

func (s *Store) RetrievalEmbeddingProfile(ctx context.Context, profileID string) (RetrievalEmbeddingProfileRow, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("retrieval embedding profile is required")
	}
	var row RetrievalEmbeddingProfileRow
	err := s.queryer().QueryRowContext(ctx, `
		SELECT profile_id, latest_revision, purge_epoch, active_generation_id,
			active_snapshot_revision, active_indexed_count, l0_ready_count,
			active_tombstone_count
		FROM retrieval_embedding_profiles
		WHERE profile_id=?`, profileID).Scan(
		&row.ProfileID, &row.LatestRevision, &row.PurgeEpoch, &row.ActiveGenerationID,
		&row.ActiveSnapshotRevision, &row.ActiveIndexedCount, &row.L0ReadyCount,
		&row.ActiveTombstoneCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RetrievalEmbeddingProfileRow{}, fmt.Errorf("retrieval embedding profile %s: %w", profileID, sql.ErrNoRows)
		}
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("read retrieval embedding profile %s: %w", profileID, err)
	}
	return row, nil
}

func ensureRetrievalEmbeddingProfileTx(ctx context.Context, tx *sql.Tx, profileID string, purgeEpoch int64) (RetrievalEmbeddingProfileRow, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_embedding_profiles (
			profile_id, latest_revision, purge_epoch, active_generation_id,
			active_snapshot_revision, active_indexed_count, l0_ready_count,
			active_tombstone_count, updated_at
		) VALUES (?, 0, ?, '', 0, 0, 0, 0, ?)
		ON CONFLICT(profile_id) DO NOTHING`, profileID, purgeEpoch, now); err != nil {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("ensure retrieval embedding profile %s: %w", profileID, err)
	}
	var row RetrievalEmbeddingProfileRow
	if err := tx.QueryRowContext(ctx, `
		SELECT profile_id, latest_revision, purge_epoch, active_generation_id,
			active_snapshot_revision, active_indexed_count, l0_ready_count,
			active_tombstone_count
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&row.ProfileID, &row.LatestRevision, &row.PurgeEpoch, &row.ActiveGenerationID,
		&row.ActiveSnapshotRevision, &row.ActiveIndexedCount, &row.L0ReadyCount,
		&row.ActiveTombstoneCount,
	); err != nil {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("load retrieval embedding profile %s: %w", profileID, err)
	}
	if row.PurgeEpoch != purgeEpoch {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("%w: profile %s is at epoch %d, expected %d", ErrRetrievalPurgeEpochChanged, profileID, row.PurgeEpoch, purgeEpoch)
	}
	return row, nil
}
