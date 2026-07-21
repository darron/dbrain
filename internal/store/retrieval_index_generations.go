package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RetrievalGenerationStatus string

var ErrRetrievalGenerationMembershipUnproven = errors.New("retrieval generation membership provenance is unavailable")

const (
	RetrievalGenerationBuilding  RetrievalGenerationStatus = "building"
	RetrievalGenerationCompleted RetrievalGenerationStatus = "completed"
	RetrievalGenerationStale     RetrievalGenerationStatus = "stale"
	RetrievalGenerationError     RetrievalGenerationStatus = "error"
)

type RetrievalIndexGenerationRow struct {
	GenerationID       string
	ProfileID          string
	Backend            string
	BackendVersion     string
	Dimensions         int
	DistanceMetric     string
	IndexedChunkCount  int
	SourceManifestHash string
	BuildStatus        RetrievalGenerationStatus
	BuildError         string
	RelativeCachePath  string
	BuildStartedAt     time.Time
	BuildCompletedAt   time.Time
	ActivatedAt        time.Time
	Active             bool
}

func (s *Store) PutRetrievalIndexGeneration(ctx context.Context, row RetrievalIndexGenerationRow) error {
	if strings.TrimSpace(row.GenerationID) == "" || strings.TrimSpace(row.ProfileID) == "" {
		return fmt.Errorf("retrieval generation and profile IDs are required")
	}
	if row.Dimensions <= 0 || strings.TrimSpace(row.DistanceMetric) == "" {
		return fmt.Errorf("retrieval generation dimensions and distance metric are required")
	}
	if row.Active && row.BuildStatus != RetrievalGenerationCompleted {
		return fmt.Errorf("only completed retrieval generations can be active")
	}
	if row.Active {
		return fmt.Errorf("%w: generation %s profile %s cannot be stored active without a source revision and membership manifest", ErrRetrievalGenerationMembershipUnproven, row.GenerationID, row.ProfileID)
	}
	if !validRetrievalGenerationStatus(row.BuildStatus) {
		return fmt.Errorf("invalid retrieval generation status %q", row.BuildStatus)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO retrieval_index_generations (
			generation_id, profile_id, backend, backend_version, dimensions,
			distance_metric, indexed_chunk_count, source_manifest_hash, build_status,
			build_error, relative_cache_path, build_started_at, build_completed_at,
			activated_at, active, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(generation_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			backend = excluded.backend,
			backend_version = excluded.backend_version,
			dimensions = excluded.dimensions,
			distance_metric = excluded.distance_metric,
			indexed_chunk_count = excluded.indexed_chunk_count,
			source_manifest_hash = excluded.source_manifest_hash,
			build_status = excluded.build_status,
			build_error = excluded.build_error,
			relative_cache_path = excluded.relative_cache_path,
			build_started_at = excluded.build_started_at,
			build_completed_at = excluded.build_completed_at,
			activated_at = excluded.activated_at,
			active = excluded.active,
			updated_at = excluded.updated_at`,
		row.GenerationID, row.ProfileID, row.Backend, row.BackendVersion, row.Dimensions,
		row.DistanceMetric, row.IndexedChunkCount, row.SourceManifestHash, row.BuildStatus,
		row.BuildError, row.RelativeCachePath, formatOptionalTime(row.BuildStartedAt),
		formatOptionalTime(row.BuildCompletedAt), formatOptionalTime(row.ActivatedAt), boolInt(row.Active), now, now)
	if err != nil {
		return fmt.Errorf("put retrieval index generation %s: %w", row.GenerationID, err)
	}
	return nil
}

func (s *Store) ActivateRetrievalIndexGeneration(ctx context.Context, generationID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retrieval generation activation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var profileID string
	var status RetrievalGenerationStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT profile_id, build_status
		FROM retrieval_index_generations WHERE generation_id = ?`, generationID).Scan(
		&profileID, &status,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("retrieval generation not found: %s", generationID)
		}
		return fmt.Errorf("load retrieval generation %s: %w", generationID, err)
	}
	if status != RetrievalGenerationCompleted {
		return fmt.Errorf("retrieval generation %s is %s, not completed", generationID, status)
	}
	return fmt.Errorf("%w: generation %s profile %s has no stored source revision and membership manifest", ErrRetrievalGenerationMembershipUnproven, generationID, profileID)
}

func validRetrievalGenerationStatus(status RetrievalGenerationStatus) bool {
	switch status {
	case RetrievalGenerationBuilding, RetrievalGenerationCompleted, RetrievalGenerationStale, RetrievalGenerationError:
		return true
	default:
		return false
	}
}

func markRetrievalProfileGenerationsStaleTx(ctx context.Context, tx *sql.Tx, profileID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		UPDATE retrieval_index_generations
		SET build_status = ?, active = 0, activated_at = '', updated_at = ?
		WHERE profile_id = ? AND build_status != ?`,
		RetrievalGenerationStale, now, profileID, RetrievalGenerationStale); err != nil {
		return fmt.Errorf("mark retrieval generations stale for profile %s: %w", profileID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embedding_profiles SET
			active_generation_id='', active_snapshot_revision=0, active_indexed_count=0,
			l0_ready_count=(SELECT COUNT(*) FROM retrieval_embeddings
				WHERE profile_id=? AND status='ready'),
			active_tombstone_count=0, updated_at=?
		WHERE profile_id=?`, profileID, now, profileID); err != nil {
		return fmt.Errorf("clear active retrieval profile root %s: %w", profileID, err)
	}
	return nil
}
