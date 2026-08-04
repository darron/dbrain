package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/semanticsegment"
)

// RetrievalDanglingGenerationRecovery describes a profile root that is still
// the authoritative profile pointer but was incorrectly marked stale and
// inactive by the pre-v29 projection dirty triggers.
type RetrievalDanglingGenerationRecovery struct {
	ProfileID, GenerationID, DescriptorSHA256, BackendVersion string
	SnapshotRevision, PurgeEpoch                              int64
	Dimensions, IndexedChunkCount                             int
}

// RetrievalDanglingGenerationRecovery returns a recovery candidate only for
// the narrow legacy corruption shape: a non-empty profile pointer to the same
// profile's stale, inactive, fully catalogued segmented generation. Active
// completed roots and profiles without a root need no recovery.
func (s *Store) RetrievalDanglingGenerationRecovery(
	ctx context.Context,
	profileID string,
) (*RetrievalDanglingGenerationRecovery, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("retrieval embedding profile is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin dangling retrieval generation inspection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	candidate, needed, err := retrievalDanglingGenerationRecoveryTx(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dangling retrieval generation inspection: %w", err)
	}
	if !needed {
		return nil, nil
	}
	return &candidate, nil
}

// ReactivateRetrievalDanglingGeneration restores a candidate only after the
// caller has independently verified the immutable native root. Every profile,
// generation, and catalog fact is re-proved in the same write transaction so a
// stale inspection cannot activate a changed root.
func (s *Store) ReactivateRetrievalDanglingGeneration(
	ctx context.Context,
	expected RetrievalDanglingGenerationRecovery,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dangling retrieval generation recovery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	actual, needed, err := retrievalDanglingGenerationRecoveryTx(ctx, tx, expected.ProfileID)
	if err != nil {
		return err
	}
	if !needed || actual != expected {
		return fmt.Errorf("%w: dangling retrieval generation changed before recovery", ErrRetrievalGenerationActivationStale)
	}
	var conflictingActive int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM retrieval_index_generations
		WHERE profile_id=? AND active=1 AND generation_id!=?`, expected.ProfileID, expected.GenerationID).Scan(&conflictingActive); err != nil {
		return fmt.Errorf("count conflicting active retrieval generations: %w", err)
	}
	if conflictingActive != 0 {
		return fmt.Errorf("%w: profile %s acquired another active generation", ErrRetrievalGenerationActivationStale, expected.ProfileID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `
		UPDATE retrieval_index_generations
		SET build_status=?,build_error='',active=1,activated_at=?,updated_at=?
		WHERE generation_id=? AND profile_id=? AND build_status=? AND active=0
			AND source_manifest_hash=? AND backend_version=? AND dimensions=? AND indexed_chunk_count=?`,
		RetrievalGenerationCompleted, now, now, expected.GenerationID, expected.ProfileID,
		RetrievalGenerationStale, expected.DescriptorSHA256, expected.BackendVersion,
		expected.Dimensions, expected.IndexedChunkCount)
	if err != nil {
		return fmt.Errorf("reactivate dangling retrieval generation %s: %w", expected.GenerationID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count dangling retrieval generation recovery: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: dangling retrieval generation changed during recovery", ErrRetrievalGenerationActivationStale)
	}
	var l0Ready, tombstones int
	if err := countRetrievalGenerationCountersTx(ctx, tx, expected.ProfileID, expected.GenerationID, &l0Ready, &tombstones); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE retrieval_embedding_profiles
		SET l0_ready_count=?,active_tombstone_count=?,updated_at=?
		WHERE profile_id=? AND active_generation_id=? AND active_snapshot_revision=?
			AND purge_epoch=? AND active_indexed_count=?`,
		l0Ready, tombstones, now, expected.ProfileID, expected.GenerationID,
		expected.SnapshotRevision, expected.PurgeEpoch, expected.IndexedChunkCount)
	if err != nil {
		return fmt.Errorf("refresh recovered retrieval profile counters: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count recovered retrieval profile update: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: retrieval profile changed during recovery", ErrRetrievalGenerationActivationStale)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dangling retrieval generation recovery: %w", err)
	}
	return nil
}

func retrievalDanglingGenerationRecoveryTx(
	ctx context.Context,
	tx *sql.Tx,
	profileID string,
) (RetrievalDanglingGenerationRecovery, bool, error) {
	var candidate RetrievalDanglingGenerationRecovery
	candidate.ProfileID = profileID
	var activeIndexed int
	if err := tx.QueryRowContext(ctx, `
		SELECT active_generation_id,active_snapshot_revision,purge_epoch,active_indexed_count
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&candidate.GenerationID, &candidate.SnapshotRevision, &candidate.PurgeEpoch, &activeIndexed,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, false, nil
		}
		return candidate, false, fmt.Errorf("load dangling retrieval generation profile %s: %w", profileID, err)
	}
	if strings.TrimSpace(candidate.GenerationID) == "" {
		return candidate, false, nil
	}
	var generationProfileID, backend, distanceMetric, relativeCachePath string
	var status RetrievalGenerationStatus
	var active bool
	if err := tx.QueryRowContext(ctx, `
		SELECT profile_id,backend,backend_version,dimensions,distance_metric,indexed_chunk_count,
			source_manifest_hash,relative_cache_path,build_status,active
		FROM retrieval_index_generations WHERE generation_id=?`, candidate.GenerationID).Scan(
		&generationProfileID, &backend, &candidate.BackendVersion, &candidate.Dimensions,
		&distanceMetric, &candidate.IndexedChunkCount, &candidate.DescriptorSHA256,
		&relativeCachePath, &status, &active,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, false, fmt.Errorf("profile %s points to missing retrieval generation %s", profileID, candidate.GenerationID)
		}
		return candidate, false, fmt.Errorf("load dangling retrieval generation %s: %w", candidate.GenerationID, err)
	}
	if generationProfileID != profileID {
		return candidate, false, fmt.Errorf("retrieval generation %s belongs to profile %s, want %s", candidate.GenerationID, generationProfileID, profileID)
	}
	if status == RetrievalGenerationCompleted && active {
		return candidate, false, nil
	}
	if status != RetrievalGenerationStale || active {
		return candidate, false, fmt.Errorf("profile %s points to non-recoverable retrieval generation %s with status %s active=%t", profileID, candidate.GenerationID, status, active)
	}
	if candidate.SnapshotRevision <= 0 || candidate.PurgeEpoch < 0 || candidate.Dimensions <= 0 ||
		candidate.IndexedChunkCount <= 0 || activeIndexed != candidate.IndexedChunkCount ||
		strings.TrimSpace(backend) == "" || strings.TrimSpace(candidate.BackendVersion) == "" ||
		strings.TrimSpace(distanceMetric) == "" || strings.TrimSpace(candidate.DescriptorSHA256) == "" ||
		strings.TrimSpace(relativeCachePath) == "" {
		return candidate, false, fmt.Errorf("dangling retrieval generation %s has incomplete root provenance", candidate.GenerationID)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT segment.segment_hash,segment.profile_id,segment.backend,segment.backend_version,
			segment.dimensions,segment.distance_metric,segment.indexed_chunk_count,segment.relative_cache_path,
			segment.membership_hash,segment.payload_hash,segment.manifest_hash,COUNT(member.ordinal)
		FROM retrieval_generation_segments generation
		JOIN retrieval_index_segments segment ON segment.segment_hash=generation.segment_hash
		LEFT JOIN retrieval_index_segment_members member ON member.segment_hash=segment.segment_hash
		WHERE generation.generation_id=?
		GROUP BY segment.segment_hash,segment.profile_id,segment.backend,segment.backend_version,
			segment.dimensions,segment.distance_metric,segment.indexed_chunk_count,segment.relative_cache_path,
			segment.membership_hash,segment.payload_hash,segment.manifest_hash
		ORDER BY segment.segment_hash`, candidate.GenerationID)
	if err != nil {
		return candidate, false, fmt.Errorf("list dangling retrieval generation segments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	segments := 0
	indexed := 0
	rootSegments := make([]semanticsegment.RootSegment, 0)
	for rows.Next() {
		var segment RetrievalIndexSegmentRow
		var members int
		if err := rows.Scan(
			&segment.SegmentHash, &segment.ProfileID, &segment.Backend, &segment.BackendVersion,
			&segment.Dimensions, &segment.DistanceMetric, &segment.IndexedChunkCount,
			&segment.RelativeCachePath, &segment.MembershipHash, &segment.PayloadHash,
			&segment.ManifestHash, &members,
		); err != nil {
			return candidate, false, fmt.Errorf("scan dangling retrieval generation segment: %w", err)
		}
		if segment.ProfileID != profileID || segment.Backend != backend ||
			segment.BackendVersion != candidate.BackendVersion || segment.Dimensions != candidate.Dimensions ||
			segment.DistanceMetric != distanceMetric || segment.IndexedChunkCount <= 0 ||
			members != segment.IndexedChunkCount || strings.TrimSpace(segment.RelativeCachePath) == "" ||
			strings.TrimSpace(segment.MembershipHash) == "" || strings.TrimSpace(segment.PayloadHash) == "" ||
			strings.TrimSpace(segment.ManifestHash) == "" {
			return candidate, false, fmt.Errorf("dangling retrieval generation %s has unproven segment %s", candidate.GenerationID, segment.SegmentHash)
		}
		segments++
		indexed += members
		rootSegments = append(rootSegments, semanticsegment.RootSegment{
			Hash: segment.SegmentHash, RelativePath: segment.RelativeCachePath,
		})
	}
	if err := rows.Err(); err != nil {
		return candidate, false, fmt.Errorf("iterate dangling retrieval generation segments: %w", err)
	}
	if segments == 0 || segments > activeSemanticGenerationSegmentCatalogCeiling || indexed != candidate.IndexedChunkCount {
		return candidate, false, fmt.Errorf("dangling retrieval generation %s catalogues %d vectors across %d segments, want %d", candidate.GenerationID, indexed, segments, candidate.IndexedChunkCount)
	}
	var databaseID string
	if err := tx.QueryRowContext(ctx, `SELECT database_id FROM retrieval_state WHERE singleton=1`).Scan(&databaseID); err != nil {
		return candidate, false, fmt.Errorf("read dangling retrieval generation database ID: %w", err)
	}
	expectedDescriptor, err := semanticsegment.RootDescriptorSHA256(semanticsegment.RootInput{
		DatabaseID: databaseID, ProfileID: profileID, GenerationID: candidate.GenerationID,
		SnapshotRevision: candidate.SnapshotRevision, PurgeEpoch: candidate.PurgeEpoch,
		Segments: rootSegments,
	})
	if err != nil {
		return candidate, false, fmt.Errorf("reconstruct dangling retrieval generation descriptor: %w", err)
	}
	if candidate.DescriptorSHA256 != expectedDescriptor {
		return candidate, false, fmt.Errorf("dangling retrieval generation %s descriptor does not match its SQLite segment catalog", candidate.GenerationID)
	}
	return candidate, true, nil
}
