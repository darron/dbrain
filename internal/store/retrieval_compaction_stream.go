package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// RetrievalActiveSegmentMemberStreamRequest identifies the one or two active
// segments selected by a compaction plan, together with the root CAS facts
// observed while planning. The stream rechecks all of these facts before it
// reads vector bytes.
type RetrievalActiveSegmentMemberStreamRequest struct {
	ProfileID, ExpectedActiveGenerationID              string
	ExpectedPurgeEpoch, ExpectedActiveSnapshotRevision int64
	SegmentHashes                                      []string
}

// RetrievalActiveSegmentMember is one current, live immutable membership and
// its authoritative embedding. Source ordinal is retained so callers can
// reproduce deterministic input order while assigning new segment ordinals.
type RetrievalActiveSegmentMember struct {
	SegmentHash string
	Ordinal     uint64
	Embedding   RetrievalEmbeddingRow
}

// RetrievalActiveSegmentMemberVisitor consumes one stream row synchronously.
// It must not call this Store or mutate SQLite while the stream is active.
type RetrievalActiveSegmentMemberVisitor func(RetrievalActiveSegmentMember) error

// StreamRetrievalActiveSegmentMembers visits current live embeddings from one
// or two selected segments in one read-only SQLite transaction. It retains no
// vector collection; stale immutable memberships are omitted. Callers must
// still use activation CAS after building derived payloads, because the root or
// embedding state can change after this read transaction closes.
func (s *Store) StreamRetrievalActiveSegmentMembers(ctx context.Context, input RetrievalActiveSegmentMemberStreamRequest, visit RetrievalActiveSegmentMemberVisitor) (int, error) {
	input.SegmentHashes = append([]string(nil), input.SegmentHashes...)
	input.ProfileID = strings.TrimSpace(input.ProfileID)
	input.ExpectedActiveGenerationID = strings.TrimSpace(input.ExpectedActiveGenerationID)
	for index := range input.SegmentHashes {
		input.SegmentHashes[index] = strings.TrimSpace(input.SegmentHashes[index])
	}
	if err := validateRetrievalActiveSegmentMemberStreamRequest(input, visit); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("begin retrieval active segment member stream: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var activeGenerationID string
	var purgeEpoch, snapshotRevision int64
	err = tx.QueryRowContext(ctx, `
		SELECT active_generation_id,purge_epoch,active_snapshot_revision
		FROM retrieval_embedding_profiles WHERE profile_id=?`, input.ProfileID,
	).Scan(&activeGenerationID, &purgeEpoch, &snapshotRevision)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("retrieval embedding profile %s: %w", input.ProfileID, sql.ErrNoRows)
		}
		return 0, fmt.Errorf("load retrieval active segment stream profile %s: %w", input.ProfileID, err)
	}
	var globalPurgeEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&globalPurgeEpoch); err != nil {
		return 0, fmt.Errorf("load retrieval active segment stream purge epoch: %w", err)
	}
	if activeGenerationID != input.ExpectedActiveGenerationID || purgeEpoch != input.ExpectedPurgeEpoch ||
		globalPurgeEpoch != input.ExpectedPurgeEpoch || snapshotRevision != input.ExpectedActiveSnapshotRevision {
		return 0, fmt.Errorf("%w: expected root %q epoch %d snapshot %d, found root %q profile epoch %d global epoch %d snapshot %d",
			ErrRetrievalGenerationActivationStale, input.ExpectedActiveGenerationID, input.ExpectedPurgeEpoch,
			input.ExpectedActiveSnapshotRevision, activeGenerationID, purgeEpoch, globalPurgeEpoch, snapshotRevision)
	}
	var generationProfileID string
	err = tx.QueryRowContext(ctx, `
		SELECT profile_id FROM retrieval_index_generations
		WHERE generation_id=? AND active=1 AND build_status=?`, activeGenerationID, RetrievalGenerationCompleted,
	).Scan(&generationProfileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("active retrieval generation %s is unavailable", activeGenerationID)
		}
		return 0, fmt.Errorf("load retrieval active segment stream generation %s: %w", activeGenerationID, err)
	}
	if generationProfileID != input.ProfileID {
		return 0, fmt.Errorf("active retrieval generation %s belongs to profile %s, want %s", activeGenerationID, generationProfileID, input.ProfileID)
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(input.SegmentHashes)), ",")
	selectionArgs := make([]any, 0, len(input.SegmentHashes)+1)
	selectionArgs = append(selectionArgs, activeGenerationID)
	for _, hash := range input.SegmentHashes {
		selectionArgs = append(selectionArgs, hash)
	}
	selected, err := tx.QueryContext(ctx, `
		SELECT segment.segment_hash,segment.profile_id,segment.indexed_chunk_count,COUNT(member.ordinal)
		FROM retrieval_generation_segments generation
		JOIN retrieval_index_segments segment ON segment.segment_hash=generation.segment_hash
		LEFT JOIN retrieval_index_segment_members member ON member.segment_hash=segment.segment_hash
		WHERE generation.generation_id=? AND segment.segment_hash IN (`+placeholders+`)
		GROUP BY segment.segment_hash,segment.profile_id,segment.indexed_chunk_count,segment.created_at
		ORDER BY segment.created_at,segment.segment_hash`, selectionArgs...)
	if err != nil {
		return 0, fmt.Errorf("list retrieval active segment stream selection: %w", err)
	}
	selectedCount := 0
	for selected.Next() {
		var segmentHash, segmentProfileID string
		var indexedCount, memberCount int
		if err := selected.Scan(&segmentHash, &segmentProfileID, &indexedCount, &memberCount); err != nil {
			_ = selected.Close()
			return 0, fmt.Errorf("scan retrieval active segment stream selection: %w", err)
		}
		if segmentProfileID != input.ProfileID {
			_ = selected.Close()
			return 0, fmt.Errorf("active retrieval segment %s belongs to profile %s, want %s", segmentHash, segmentProfileID, input.ProfileID)
		}
		if indexedCount <= 0 || memberCount != indexedCount {
			_ = selected.Close()
			return 0, fmt.Errorf("active retrieval segment %s catalogued count %d does not equal membership count %d", segmentHash, indexedCount, memberCount)
		}
		selectedCount++
	}
	if err := selected.Err(); err != nil {
		_ = selected.Close()
		return 0, fmt.Errorf("iterate retrieval active segment stream selection: %w", err)
	}
	if err := selected.Close(); err != nil {
		return 0, fmt.Errorf("close retrieval active segment stream selection: %w", err)
	}
	if selectedCount != len(input.SegmentHashes) {
		return 0, fmt.Errorf("retrieval active segment stream selected %d active segments, want %d", selectedCount, len(input.SegmentHashes))
	}

	streamArgs := make([]any, 0, len(input.SegmentHashes)+2)
	streamArgs = append(streamArgs, input.ProfileID, activeGenerationID)
	for _, hash := range input.SegmentHashes {
		streamArgs = append(streamArgs, hash)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT member.segment_hash,member.ordinal,e.chunk_id,e.profile_id,e.provider,e.model,e.dimensions,
			e.representation,e.normalization,e.vector_bytes,e.vector_hash,e.chunk_text_hash,e.revision,
			e.status,c.chunk_text_hash
		FROM retrieval_generation_segments generation
		JOIN retrieval_index_segments segment ON segment.segment_hash=generation.segment_hash
		JOIN retrieval_index_segment_members member ON member.segment_hash=segment.segment_hash
		JOIN retrieval_embeddings e ON e.chunk_id=member.chunk_id AND e.profile_id=? AND e.status='ready'
			AND e.revision=member.revision AND e.vector_hash=member.vector_hash
		JOIN retrieval_chunks c ON c.chunk_id=member.chunk_id
		JOIN retrieval_parent_projections parent ON parent.parent_kind=c.parent_kind
			AND parent.parent_source_key=c.parent_source_key AND parent.status='current'
		WHERE generation.generation_id=? AND segment.segment_hash IN (`+placeholders+`)
		ORDER BY segment.created_at,segment.segment_hash,member.ordinal`, streamArgs...)
	if err != nil {
		return 0, fmt.Errorf("stream retrieval active segment members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var member RetrievalActiveSegmentMember
		var currentChunkTextHash string
		if err := rows.Scan(&member.SegmentHash, &member.Ordinal, &member.Embedding.ChunkID,
			&member.Embedding.ProfileID, &member.Embedding.Provider, &member.Embedding.Model, &member.Embedding.Dimensions,
			&member.Embedding.Representation, &member.Embedding.Normalization, &member.Embedding.VectorBytes,
			&member.Embedding.VectorHash, &member.Embedding.ChunkTextHash, &member.Embedding.Revision,
			&member.Embedding.Status, &currentChunkTextHash); err != nil {
			return count, fmt.Errorf("scan retrieval active segment member: %w", err)
		}
		if reason := retrievalEmbeddingCorruptionReason(member.Embedding, currentChunkTextHash); reason != "" {
			return count, retrievalEmbeddingCorruption(member.Embedding.ChunkID, member.Embedding.ProfileID, reason)
		}
		if err := visit(member); err != nil {
			return count, fmt.Errorf("visit retrieval active segment member %s: %w", member.Embedding.ChunkID, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("iterate retrieval active segment members: %w", err)
	}
	if err := rows.Close(); err != nil {
		return count, fmt.Errorf("close retrieval active segment members: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return count, fmt.Errorf("commit retrieval active segment member stream: %w", err)
	}
	return count, nil
}

func validateRetrievalActiveSegmentMemberStreamRequest(input RetrievalActiveSegmentMemberStreamRequest, visit RetrievalActiveSegmentMemberVisitor) error {
	if input.ProfileID == "" || input.ExpectedActiveGenerationID == "" {
		return fmt.Errorf("retrieval active segment stream profile and active generation IDs are required")
	}
	if input.ExpectedPurgeEpoch < 0 || input.ExpectedActiveSnapshotRevision <= 0 {
		return fmt.Errorf("retrieval active segment stream purge epoch and snapshot revision are invalid")
	}
	if len(input.SegmentHashes) == 0 || len(input.SegmentHashes) > 2 {
		return fmt.Errorf("retrieval active segment stream must select one or two segments")
	}
	if visit == nil {
		return fmt.Errorf("retrieval active segment stream visitor is required")
	}
	seen := make(map[string]struct{}, len(input.SegmentHashes))
	for _, hash := range input.SegmentHashes {
		if hash == "" {
			return fmt.Errorf("retrieval active segment stream segment hash is required")
		}
		if _, exists := seen[hash]; exists {
			return fmt.Errorf("retrieval active segment stream segment hash %s is duplicated", hash)
		}
		seen[hash] = struct{}{}
	}
	return nil
}
