package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// RetrievalActiveRootReadRequest pins a read to the active root facts observed
// before opening a derived native root.
type RetrievalActiveRootReadRequest struct {
	ProfileID, ExpectedActiveGenerationID              string
	ExpectedPurgeEpoch, ExpectedActiveSnapshotRevision int64
}

// ReadRetrievalExactL0 returns only current ready embeddings absent from the
// CAS-checked active root. It bounds the exact delta explicitly; callers must
// fail open rather than silently search a truncated L0 tail.
func (s *Store) ReadRetrievalExactL0(ctx context.Context, input RetrievalActiveRootReadRequest, limit int) ([]RetrievalEmbeddingRow, error) {
	input.ProfileID, input.ExpectedActiveGenerationID = strings.TrimSpace(input.ProfileID), strings.TrimSpace(input.ExpectedActiveGenerationID)
	if input.ProfileID == "" || input.ExpectedActiveGenerationID == "" || input.ExpectedPurgeEpoch < 0 || input.ExpectedActiveSnapshotRevision <= 0 {
		return nil, fmt.Errorf("retrieval exact L0 active-root request is invalid")
	}
	if limit <= 0 || limit > RetrievalSegmentHardLimit {
		return nil, fmt.Errorf("retrieval exact L0 limit must be between 1 and %d", RetrievalSegmentHardLimit)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin retrieval exact L0 read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var generation string
	var epoch, snapshot, globalEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT active_generation_id,purge_epoch,active_snapshot_revision FROM retrieval_embedding_profiles WHERE profile_id=?`, input.ProfileID).Scan(&generation, &epoch, &snapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("retrieval embedding profile %s: %w", input.ProfileID, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("load retrieval exact L0 profile: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&globalEpoch); err != nil {
		return nil, fmt.Errorf("load retrieval exact L0 purge epoch: %w", err)
	}
	if generation != input.ExpectedActiveGenerationID || epoch != input.ExpectedPurgeEpoch || globalEpoch != input.ExpectedPurgeEpoch || snapshot != input.ExpectedActiveSnapshotRevision {
		return nil, fmt.Errorf("%w: active root changed", ErrRetrievalGenerationActivationStale)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT e.chunk_id,e.profile_id,e.provider,e.model,e.dimensions,e.representation,e.normalization,e.vector_bytes,e.vector_hash,e.chunk_text_hash,e.revision,e.status,
			c.parent_kind,c.parent_source_key,c.evidence_role,CASE WHEN c.parent_kind='source' THEN COALESCE(source.source_type,'') ELSE COALESCE(item.source_type,'') END,c.section_ordinal,c.projection_version,c.chunker_version,c.chunk_text_hash
		FROM retrieval_embeddings e JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		JOIN retrieval_parent_projections parent ON parent.parent_kind=c.parent_kind AND parent.parent_source_key=c.parent_source_key AND parent.status='current'
		LEFT JOIN items item ON c.parent_kind='item' AND item.source_key=c.parent_source_key LEFT JOIN sources source ON c.parent_kind='source' AND source.source_key=c.parent_source_key
		WHERE e.profile_id=? AND e.status='ready' AND NOT EXISTS (SELECT 1 FROM retrieval_generation_segments generation JOIN retrieval_index_segment_members member ON member.segment_hash=generation.segment_hash WHERE generation.generation_id=? AND member.chunk_id=e.chunk_id AND member.revision=e.revision AND member.vector_hash=e.vector_hash)
		ORDER BY e.revision,e.chunk_id LIMIT ?`, input.ProfileID, generation, limit+1)
	if err != nil {
		return nil, fmt.Errorf("read retrieval exact L0: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalEmbeddingRow, 0, limit)
	for rows.Next() {
		var row RetrievalEmbeddingRow
		var current string
		if err := rows.Scan(&row.ChunkID, &row.ProfileID, &row.Provider, &row.Model, &row.Dimensions, &row.Representation, &row.Normalization, &row.VectorBytes, &row.VectorHash, &row.ChunkTextHash, &row.Revision, &row.Status, &row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole, &row.SourceType, &row.SectionOrdinal, &row.ProjectionVersion, &row.ChunkerVersion, &current); err != nil {
			return nil, fmt.Errorf("scan retrieval exact L0: %w", err)
		}
		if len(result) == limit {
			return nil, fmt.Errorf("retrieval exact L0 exceeds limit %d", limit)
		}
		if reason := retrievalEmbeddingCorruptionReason(row, current); reason != "" {
			return nil, retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, reason)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval exact L0: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit retrieval exact L0: %w", err)
	}
	return result, nil
}
