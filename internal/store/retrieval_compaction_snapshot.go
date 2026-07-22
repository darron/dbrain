package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// RetrievalActiveSegmentCompactionSnapshot is the immutable active-root input
// for compaction planning. It contains no vector payloads and makes no change
// to SQLite or cache state.
type RetrievalActiveSegmentCompactionSnapshot struct {
	Profile  RetrievalEmbeddingProfileRow
	Segments []RetrievalActiveSegmentCompactionSegment
}

// RetrievalActiveSegmentCompactionSegment combines an immutable segment
// catalog entry with its current authoritative membership state. CreatedOrder
// is one-based and stable for a given root: segment creation timestamp first,
// then content-addressed segment hash.
type RetrievalActiveSegmentCompactionSegment struct {
	RetrievalIndexSegmentRow
	CreatedOrder              int64
	LiveCount, TombstoneCount int
}

// RetrievalActiveSegmentCompactionSnapshot reads one active root in a single
// read-only transaction. A member is live only when its exact stored identity
// still joins a ready embedding and current parent projection; every other
// catalogued member is a tombstone. It fails closed on active-root or catalog
// corruption, leaving planning and root replacement to later layers.
func (s *Store) RetrievalActiveSegmentCompactionSnapshot(ctx context.Context, profileID string) (RetrievalActiveSegmentCompactionSnapshot, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("retrieval embedding profile is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("begin retrieval active segment compaction snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var snapshot RetrievalActiveSegmentCompactionSnapshot
	err = tx.QueryRowContext(ctx, `
		SELECT profile_id,latest_revision,purge_epoch,active_generation_id,active_snapshot_revision,
			active_indexed_count,l0_ready_count,active_tombstone_count
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&snapshot.Profile.ProfileID, &snapshot.Profile.LatestRevision, &snapshot.Profile.PurgeEpoch,
		&snapshot.Profile.ActiveGenerationID, &snapshot.Profile.ActiveSnapshotRevision,
		&snapshot.Profile.ActiveIndexedCount, &snapshot.Profile.L0ReadyCount,
		&snapshot.Profile.ActiveTombstoneCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("retrieval embedding profile %s: %w", profileID, sql.ErrNoRows)
		}
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("load retrieval active segment compaction profile %s: %w", profileID, err)
	}
	snapshot.Segments = make([]RetrievalActiveSegmentCompactionSegment, 0)
	if snapshot.Profile.ActiveGenerationID == "" {
		if err := tx.Commit(); err != nil {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("commit empty retrieval active segment compaction snapshot: %w", err)
		}
		return snapshot, nil
	}

	var generationProfileID string
	err = tx.QueryRowContext(ctx, `
		SELECT profile_id FROM retrieval_index_generations
		WHERE generation_id=? AND active=1 AND build_status=?`,
		snapshot.Profile.ActiveGenerationID, RetrievalGenerationCompleted,
	).Scan(&generationProfileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval generation %s is unavailable", snapshot.Profile.ActiveGenerationID)
		}
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("load active retrieval generation %s: %w", snapshot.Profile.ActiveGenerationID, err)
	}
	if generationProfileID != profileID {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval generation %s belongs to profile %s, want %s", snapshot.Profile.ActiveGenerationID, generationProfileID, profileID)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT segment.segment_hash,segment.profile_id,segment.backend,segment.backend_version,segment.dimensions,
			segment.distance_metric,segment.indexed_chunk_count,segment.relative_cache_path,segment.membership_hash,
			segment.payload_hash,segment.manifest_hash,COUNT(member.ordinal),
			COALESCE(SUM(CASE WHEN embedding.chunk_id IS NOT NULL AND parent.parent_kind IS NOT NULL THEN 1 ELSE 0 END),0)
		FROM retrieval_generation_segments generation
		JOIN retrieval_index_segments segment ON segment.segment_hash=generation.segment_hash
		LEFT JOIN retrieval_index_segment_members member ON member.segment_hash=segment.segment_hash
		LEFT JOIN retrieval_embeddings embedding ON embedding.chunk_id=member.chunk_id AND embedding.profile_id=?
			AND embedding.status='ready' AND embedding.revision=member.revision AND embedding.vector_hash=member.vector_hash
		LEFT JOIN retrieval_chunks chunk ON chunk.chunk_id=member.chunk_id
		LEFT JOIN retrieval_parent_projections parent ON parent.parent_kind=chunk.parent_kind
			AND parent.parent_source_key=chunk.parent_source_key AND parent.status='current'
		WHERE generation.generation_id=?
		GROUP BY segment.segment_hash,segment.profile_id,segment.backend,segment.backend_version,segment.dimensions,
			segment.distance_metric,segment.indexed_chunk_count,segment.relative_cache_path,segment.membership_hash,
			segment.payload_hash,segment.manifest_hash,segment.created_at
		ORDER BY segment.created_at,segment.segment_hash`, profileID, snapshot.Profile.ActiveGenerationID)
	if err != nil {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("list retrieval active segment compaction snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var segment RetrievalActiveSegmentCompactionSegment
		var memberCount int
		if err := rows.Scan(
			&segment.SegmentHash, &segment.ProfileID, &segment.Backend, &segment.BackendVersion, &segment.Dimensions,
			&segment.DistanceMetric, &segment.IndexedChunkCount, &segment.RelativeCachePath, &segment.MembershipHash,
			&segment.PayloadHash, &segment.ManifestHash, &memberCount, &segment.LiveCount,
		); err != nil {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("scan retrieval active segment compaction snapshot: %w", err)
		}
		if segment.ProfileID != profileID {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval segment %s belongs to profile %s, want %s", segment.SegmentHash, segment.ProfileID, profileID)
		}
		if segment.IndexedChunkCount <= 0 {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval segment %s has invalid catalogued count %d", segment.SegmentHash, segment.IndexedChunkCount)
		}
		if memberCount != segment.IndexedChunkCount {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval segment %s catalogued count %d does not equal membership count %d", segment.SegmentHash, segment.IndexedChunkCount, memberCount)
		}
		if segment.LiveCount < 0 || segment.LiveCount > memberCount {
			return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval segment %s has invalid live membership count %d of %d", segment.SegmentHash, segment.LiveCount, memberCount)
		}
		segment.TombstoneCount = memberCount - segment.LiveCount
		segment.CreatedOrder = int64(len(snapshot.Segments) + 1)
		snapshot.Segments = append(snapshot.Segments, segment)
	}
	if err := rows.Err(); err != nil {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("iterate retrieval active segment compaction snapshot: %w", err)
	}
	if err := rows.Close(); err != nil {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("close retrieval active segment compaction snapshot: %w", err)
	}
	if len(snapshot.Segments) == 0 {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("active retrieval generation %s has no segments", snapshot.Profile.ActiveGenerationID)
	}
	if err := tx.Commit(); err != nil {
		return RetrievalActiveSegmentCompactionSnapshot{}, fmt.Errorf("commit retrieval active segment compaction snapshot: %w", err)
	}
	return snapshot, nil
}
