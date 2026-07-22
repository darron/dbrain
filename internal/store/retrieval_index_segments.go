package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	RetrievalSegmentTarget    = 5_000
	RetrievalSegmentHardLimit = 10_000
)

type RetrievalIndexSegmentRow struct {
	SegmentHash, ProfileID, Backend, BackendVersion, DistanceMetric, RelativeCachePath string
	Dimensions, IndexedChunkCount                                                      int
	MembershipHash, PayloadHash, ManifestHash                                          string
}

type RetrievalIndexSegmentMember struct {
	SegmentHash, ChunkID, VectorHash string
	Ordinal                          uint64
	Revision                         int64
}

type RetrievalFlushWindow struct {
	Profile          RetrievalEmbeddingProfileRow
	Rows             []RetrievalEmbeddingRow
	SnapshotRevision int64
}

type CompleteRetrievalIndexGenerationInput struct {
	Generation       RetrievalIndexGenerationRow
	Segments         []RetrievalIndexSegmentRow
	Members          []RetrievalIndexSegmentMember
	SnapshotRevision int64
}

func (s *Store) RetrievalDatabaseID(ctx context.Context) (string, error) {
	var databaseID string
	if err := s.queryer().QueryRowContext(ctx, `SELECT database_id FROM retrieval_state WHERE singleton=1`).Scan(&databaseID); err != nil {
		return "", fmt.Errorf("read retrieval database ID: %w", err)
	}
	if strings.TrimSpace(databaseID) == "" {
		return "", fmt.Errorf("retrieval database ID is empty")
	}
	return databaseID, nil
}

func (s *Store) RetrievalIndexGenerationSegments(ctx context.Context, generationID string) ([]RetrievalIndexSegmentRow, error) {
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		return nil, fmt.Errorf("retrieval generation ID is required")
	}
	rows, err := s.queryer().QueryContext(ctx, `
		SELECT segment.segment_hash,segment.profile_id,segment.backend,segment.backend_version,segment.dimensions,
			segment.distance_metric,segment.indexed_chunk_count,segment.relative_cache_path,segment.membership_hash,
			segment.payload_hash,segment.manifest_hash
		FROM retrieval_generation_segments generation
		JOIN retrieval_index_segments segment ON segment.segment_hash=generation.segment_hash
		WHERE generation.generation_id=? ORDER BY segment.segment_hash`, generationID)
	if err != nil {
		return nil, fmt.Errorf("list retrieval generation segments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	segments := make([]RetrievalIndexSegmentRow, 0)
	for rows.Next() {
		var segment RetrievalIndexSegmentRow
		if err := rows.Scan(&segment.SegmentHash, &segment.ProfileID, &segment.Backend, &segment.BackendVersion,
			&segment.Dimensions, &segment.DistanceMetric, &segment.IndexedChunkCount, &segment.RelativeCachePath,
			&segment.MembershipHash, &segment.PayloadHash, &segment.ManifestHash); err != nil {
			return nil, fmt.Errorf("scan retrieval generation segment: %w", err)
		}
		segments = append(segments, segment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval generation segments: %w", err)
	}
	return segments, nil
}

// NextRetrievalFlushWindow returns a contiguous revision prefix of the exact
// L0 tail. Revision order is essential: advancing past an older unindexed row
// would make it disappear from both the root and the exact tail.
func (s *Store) NextRetrievalFlushWindow(ctx context.Context, profileID string, limit int) (RetrievalFlushWindow, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return RetrievalFlushWindow{}, fmt.Errorf("retrieval embedding profile is required")
	}
	if limit <= 0 || limit > RetrievalSegmentHardLimit {
		return RetrievalFlushWindow{}, fmt.Errorf("retrieval segment limit must be between 1 and %d", RetrievalSegmentHardLimit)
	}
	profile, err := s.RetrievalEmbeddingProfile(ctx, profileID)
	if err != nil {
		return RetrievalFlushWindow{}, err
	}
	if limit > RetrievalSegmentTarget {
		limit = RetrievalSegmentTarget
	}
	rows, err := s.queryer().QueryContext(ctx, `
		SELECT e.chunk_id,e.profile_id,e.provider,e.model,e.dimensions,e.representation,e.normalization,
			e.vector_bytes,e.vector_hash,e.chunk_text_hash,e.revision,e.status,e.attempt_count,e.last_error,
			e.next_attempt_at,e.embedded_at,c.parent_kind,c.parent_source_key,c.evidence_role,
			CASE WHEN c.parent_kind='source' THEN COALESCE(source.source_type,'') ELSE COALESCE(item.source_type,'') END,
			c.section_ordinal,c.text,c.projection_version,c.chunker_version,c.chunk_text_hash
		FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		JOIN retrieval_parent_projections parent ON parent.parent_kind=c.parent_kind
			AND parent.parent_source_key=c.parent_source_key AND parent.status='current'
		LEFT JOIN items item ON c.parent_kind='item' AND item.source_key=c.parent_source_key
		LEFT JOIN sources source ON c.parent_kind='source' AND source.source_key=c.parent_source_key
		WHERE e.profile_id=? AND e.status='ready' AND e.revision>?
		ORDER BY e.revision,e.chunk_id
		LIMIT ?`, profileID, profile.ActiveSnapshotRevision, limit)
	if err != nil {
		return RetrievalFlushWindow{}, fmt.Errorf("list retrieval L0 flush window: %w", err)
	}
	defer func() { _ = rows.Close() }()
	window := RetrievalFlushWindow{Profile: profile, Rows: make([]RetrievalEmbeddingRow, 0, limit)}
	for rows.Next() {
		var row RetrievalEmbeddingRow
		var nextAttemptAt, embeddedAt, currentChunkTextHash string
		if err := rows.Scan(&row.ChunkID, &row.ProfileID, &row.Provider, &row.Model, &row.Dimensions,
			&row.Representation, &row.Normalization, &row.VectorBytes, &row.VectorHash, &row.ChunkTextHash,
			&row.Revision, &row.Status, &row.AttemptCount, &row.LastError, &nextAttemptAt, &embeddedAt,
			&row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole, &row.SourceType, &row.SectionOrdinal,
			&row.Text, &row.ProjectionVersion, &row.ChunkerVersion, &currentChunkTextHash); err != nil {
			return RetrievalFlushWindow{}, fmt.Errorf("scan retrieval L0 flush window: %w", err)
		}
		if reason := retrievalEmbeddingCorruptionReason(row, currentChunkTextHash); reason != "" {
			return RetrievalFlushWindow{}, retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, reason)
		}
		row.NextAttemptAt = parseStoredTime(nextAttemptAt)
		row.EmbeddedAt = parseStoredTime(embeddedAt)
		window.Rows = append(window.Rows, row)
		window.SnapshotRevision = row.Revision
	}
	if err := rows.Err(); err != nil {
		return RetrievalFlushWindow{}, fmt.Errorf("iterate retrieval L0 flush window: %w", err)
	}
	return window, nil
}

// CompleteRetrievalIndexGeneration records an already-published root and only
// activates it after every newly-built member still matches SQLite evidence.
func (s *Store) CompleteRetrievalIndexGeneration(ctx context.Context, input CompleteRetrievalIndexGenerationInput) error {
	if err := validateGenerationCompletionInput(input); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retrieval generation completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var profile RetrievalEmbeddingProfileRow
	if err := tx.QueryRowContext(ctx, `
		SELECT profile_id,latest_revision,purge_epoch,active_generation_id,active_snapshot_revision,
			active_indexed_count,l0_ready_count,active_tombstone_count
		FROM retrieval_embedding_profiles WHERE profile_id=?`, input.Generation.ProfileID).Scan(
		&profile.ProfileID, &profile.LatestRevision, &profile.PurgeEpoch, &profile.ActiveGenerationID,
		&profile.ActiveSnapshotRevision, &profile.ActiveIndexedCount, &profile.L0ReadyCount, &profile.ActiveTombstoneCount); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("retrieval embedding profile %s not found", input.Generation.ProfileID)
		}
		return fmt.Errorf("load retrieval generation profile: %w", err)
	}
	if input.SnapshotRevision <= profile.ActiveSnapshotRevision {
		return fmt.Errorf("retrieval generation snapshot revision %d does not advance active snapshot %d", input.SnapshotRevision, profile.ActiveSnapshotRevision)
	}
	for _, member := range input.Members {
		var status RetrievalEmbeddingStatus
		var revision int64
		var vectorHash string
		err := tx.QueryRowContext(ctx, `
			SELECT e.status,e.revision,e.vector_hash
			FROM retrieval_embeddings e JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
			JOIN retrieval_parent_projections parent ON parent.parent_kind=c.parent_kind
				AND parent.parent_source_key=c.parent_source_key AND parent.status='current'
			WHERE e.chunk_id=? AND e.profile_id=?`, member.ChunkID, input.Generation.ProfileID).Scan(&status, &revision, &vectorHash)
		if err != nil || status != RetrievalEmbeddingReady || revision != member.Revision || vectorHash != member.VectorHash {
			return fmt.Errorf("retrieval segment member %s no longer matches ready embedding evidence", member.ChunkID)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_index_generations (
			generation_id,profile_id,backend,backend_version,dimensions,distance_metric,indexed_chunk_count,
			source_manifest_hash,build_status,build_error,relative_cache_path,build_started_at,build_completed_at,
			activated_at,active,created_at,updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
		input.Generation.GenerationID, input.Generation.ProfileID, input.Generation.Backend, input.Generation.BackendVersion,
		input.Generation.Dimensions, input.Generation.DistanceMetric, input.Generation.IndexedChunkCount,
		input.Generation.SourceManifestHash, RetrievalGenerationCompleted, "", input.Generation.RelativeCachePath,
		formatOptionalTime(input.Generation.BuildStartedAt), formatOptionalTime(input.Generation.BuildCompletedAt), "", now, now); err != nil {
		return fmt.Errorf("insert retrieval generation %s: %w", input.Generation.GenerationID, err)
	}
	for _, segment := range input.Segments {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_index_segments (
				segment_hash,profile_id,backend,backend_version,dimensions,distance_metric,indexed_chunk_count,
				relative_cache_path,membership_hash,payload_hash,manifest_hash,created_at
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(segment_hash) DO NOTHING`, segment.SegmentHash, segment.ProfileID, segment.Backend,
			segment.BackendVersion, segment.Dimensions, segment.DistanceMetric, segment.IndexedChunkCount,
			segment.RelativeCachePath, segment.MembershipHash, segment.PayloadHash, segment.ManifestHash, now)
		if err != nil {
			return fmt.Errorf("insert retrieval index segment %s: %w", segment.SegmentHash, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count retrieval index segment %s insert: %w", segment.SegmentHash, err)
		}
		if affected == 0 {
			var stored RetrievalIndexSegmentRow
			if err := tx.QueryRowContext(ctx, `
				SELECT segment_hash,profile_id,backend,backend_version,dimensions,distance_metric,indexed_chunk_count,
					relative_cache_path,membership_hash,payload_hash,manifest_hash
				FROM retrieval_index_segments WHERE segment_hash=?`, segment.SegmentHash).Scan(
				&stored.SegmentHash, &stored.ProfileID, &stored.Backend, &stored.BackendVersion, &stored.Dimensions,
				&stored.DistanceMetric, &stored.IndexedChunkCount, &stored.RelativeCachePath, &stored.MembershipHash,
				&stored.PayloadHash, &stored.ManifestHash); err != nil {
				return fmt.Errorf("read existing retrieval index segment %s: %w", segment.SegmentHash, err)
			}
			if stored != segment {
				return fmt.Errorf("retrieval index segment %s conflicts with immutable stored metadata", segment.SegmentHash)
			}
		}
	}
	for _, member := range input.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO retrieval_index_segment_members (segment_hash,ordinal,chunk_id,revision,vector_hash) VALUES (?,?,?,?,?)`,
			member.SegmentHash, member.Ordinal, member.ChunkID, member.Revision, member.VectorHash); err != nil {
			return fmt.Errorf("insert retrieval index segment member %s: %w", member.ChunkID, err)
		}
	}
	indexed, err := proveGenerationSegmentsTx(ctx, tx, input.Generation.GenerationID, input.Segments)
	if err != nil {
		return err
	}
	if indexed != input.Generation.IndexedChunkCount {
		return fmt.Errorf("retrieval generation indexed count %d does not equal proved segment count %d", input.Generation.IndexedChunkCount, indexed)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_index_generations SET active=0,activated_at='' WHERE profile_id=? AND active=1`, input.Generation.ProfileID); err != nil {
		return fmt.Errorf("deactivate prior retrieval generation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_index_generations SET active=1,activated_at=?,updated_at=? WHERE generation_id=?`, now, now, input.Generation.GenerationID); err != nil {
		return fmt.Errorf("activate retrieval generation: %w", err)
	}
	var l0Ready, tombstones int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM retrieval_embeddings WHERE profile_id=? AND status='ready' AND revision>?`, input.Generation.ProfileID, input.SnapshotRevision).Scan(&l0Ready); err != nil {
		return fmt.Errorf("count retrieval L0 tail: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM retrieval_index_segment_members member
		JOIN retrieval_generation_segments generation ON generation.segment_hash=member.segment_hash
		LEFT JOIN retrieval_embeddings embedding ON embedding.chunk_id=member.chunk_id AND embedding.profile_id=?
			AND embedding.status='ready' AND embedding.revision=member.revision AND embedding.vector_hash=member.vector_hash
		WHERE generation.generation_id=? AND embedding.chunk_id IS NULL`, input.Generation.ProfileID, input.Generation.GenerationID).Scan(&tombstones); err != nil {
		return fmt.Errorf("count retrieval root tombstones: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embedding_profiles SET active_generation_id=?,active_snapshot_revision=?,active_indexed_count=?,
			l0_ready_count=?,active_tombstone_count=?,updated_at=? WHERE profile_id=?`,
		input.Generation.GenerationID, input.SnapshotRevision, indexed, l0Ready, tombstones, now, input.Generation.ProfileID); err != nil {
		return fmt.Errorf("activate retrieval embedding profile root: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval generation activation: %w", err)
	}
	return nil
}

func proveGenerationSegmentsTx(ctx context.Context, tx *sql.Tx, generationID string, segments []RetrievalIndexSegmentRow) (int, error) {
	indexed := 0
	for _, segment := range segments {
		var members int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM retrieval_index_segment_members WHERE segment_hash=?`, segment.SegmentHash).Scan(&members); err != nil {
			return 0, fmt.Errorf("count retrieval index segment %s members: %w", segment.SegmentHash, err)
		}
		if members != segment.IndexedChunkCount {
			return 0, fmt.Errorf("retrieval index segment %s has %d members, want %d", segment.SegmentHash, members, segment.IndexedChunkCount)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO retrieval_generation_segments (generation_id,segment_hash) VALUES (?,?)`, generationID, segment.SegmentHash); err != nil {
			return 0, fmt.Errorf("record retrieval generation segment %s: %w", segment.SegmentHash, err)
		}
		indexed += members
	}
	return indexed, nil
}

func validateGenerationCompletionInput(input CompleteRetrievalIndexGenerationInput) error {
	generation := input.Generation
	if strings.TrimSpace(generation.GenerationID) == "" || strings.TrimSpace(generation.ProfileID) == "" {
		return fmt.Errorf("retrieval generation and profile IDs are required")
	}
	if generation.BuildStatus != RetrievalGenerationCompleted || generation.Active {
		return fmt.Errorf("retrieval generation completion requires an inactive completed generation")
	}
	if generation.Dimensions <= 0 || strings.TrimSpace(generation.Backend) == "" || strings.TrimSpace(generation.BackendVersion) == "" || strings.TrimSpace(generation.DistanceMetric) == "" || strings.TrimSpace(generation.SourceManifestHash) == "" {
		return fmt.Errorf("retrieval generation provenance is incomplete")
	}
	if len(input.Segments) == 0 || len(input.Members) == 0 || input.SnapshotRevision <= 0 {
		return fmt.Errorf("retrieval generation segments, members, and snapshot revision are required")
	}
	segmentCounts := make(map[string]int, len(input.Segments))
	for _, segment := range input.Segments {
		if strings.TrimSpace(segment.SegmentHash) == "" || segment.ProfileID != generation.ProfileID || segment.Dimensions != generation.Dimensions || segment.IndexedChunkCount <= 0 || strings.TrimSpace(segment.RelativeCachePath) == "" || strings.TrimSpace(segment.MembershipHash) == "" || strings.TrimSpace(segment.PayloadHash) == "" || strings.TrimSpace(segment.ManifestHash) == "" {
			return fmt.Errorf("retrieval segment provenance is incomplete")
		}
		if _, exists := segmentCounts[segment.SegmentHash]; exists {
			return fmt.Errorf("retrieval segment %s is duplicated", segment.SegmentHash)
		}
		segmentCounts[segment.SegmentHash] = segment.IndexedChunkCount
	}
	seenOrdinals := make(map[string]map[uint64]struct{}, len(segmentCounts))
	seenChunks := make(map[string]map[string]struct{}, len(segmentCounts))
	maximumRevision := int64(0)
	for _, member := range input.Members {
		if _, exists := segmentCounts[member.SegmentHash]; !exists || strings.TrimSpace(member.ChunkID) == "" || strings.TrimSpace(member.VectorHash) == "" || member.Revision <= 0 {
			return fmt.Errorf("retrieval segment member provenance is incomplete")
		}
		if seenOrdinals[member.SegmentHash] == nil {
			seenOrdinals[member.SegmentHash] = map[uint64]struct{}{}
			seenChunks[member.SegmentHash] = map[string]struct{}{}
		}
		if _, exists := seenOrdinals[member.SegmentHash][member.Ordinal]; exists {
			return fmt.Errorf("retrieval segment member ordinal is duplicated")
		}
		if _, exists := seenChunks[member.SegmentHash][member.ChunkID]; exists {
			return fmt.Errorf("retrieval segment member chunk is duplicated")
		}
		seenOrdinals[member.SegmentHash][member.Ordinal] = struct{}{}
		seenChunks[member.SegmentHash][member.ChunkID] = struct{}{}
		if member.Revision > maximumRevision {
			maximumRevision = member.Revision
		}
	}
	if maximumRevision != input.SnapshotRevision {
		return fmt.Errorf("retrieval generation snapshot revision %d does not equal member maximum %d", input.SnapshotRevision, maximumRevision)
	}
	return nil
}
