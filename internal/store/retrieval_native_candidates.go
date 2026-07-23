package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MaxRetrievalNativeCandidates keeps the five-column request CTE below the
// conservative SQLite 999-bind-variable floor (190 * 5 + two root facts).
const MaxRetrievalNativeCandidates = 190

// RetrievalNativeCandidate is an immutable ANN-manifest member selected by a
// native index. It is not evidence until ReadRetrievalNativeCandidates proves
// that it still belongs to the active SQLite root and current ready embedding.
type RetrievalNativeCandidate struct {
	SegmentHash, ChunkID, VectorHash string
	Revision                         int64
}

// RetrievalNativeCandidateRequest binds approximate candidates to the active
// root facts observed while opening the native root. The read fails closed when
// those facts changed; stale members are simply omitted from its result.
type RetrievalNativeCandidateRequest struct {
	ProfileID, ExpectedActiveGenerationID              string
	ExpectedPurgeEpoch, ExpectedActiveSnapshotRevision int64
	Candidates                                         []RetrievalNativeCandidate
}

// ReadRetrievalNativeCandidates validates a bounded native candidate set in a
// single read-only transaction. It preserves request order for the current
// members that remain, so a later exact reranker can use deterministic
// tie-breaking without trusting native result ordering.
func (s *Store) ReadRetrievalNativeCandidates(ctx context.Context, input RetrievalNativeCandidateRequest) ([]RetrievalEmbeddingRow, error) {
	input.ProfileID = strings.TrimSpace(input.ProfileID)
	input.ExpectedActiveGenerationID = strings.TrimSpace(input.ExpectedActiveGenerationID)
	input.Candidates = append([]RetrievalNativeCandidate(nil), input.Candidates...)
	for index := range input.Candidates {
		input.Candidates[index].SegmentHash = strings.TrimSpace(input.Candidates[index].SegmentHash)
		input.Candidates[index].ChunkID = strings.TrimSpace(input.Candidates[index].ChunkID)
		input.Candidates[index].VectorHash = strings.TrimSpace(input.Candidates[index].VectorHash)
	}
	if err := validateRetrievalNativeCandidateRequest(input); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin retrieval native candidate read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var activeGenerationID string
	var purgeEpoch, snapshotRevision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT active_generation_id,purge_epoch,active_snapshot_revision
		FROM retrieval_embedding_profiles WHERE profile_id=?`, input.ProfileID,
	).Scan(&activeGenerationID, &purgeEpoch, &snapshotRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("retrieval embedding profile %s: %w", input.ProfileID, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("load retrieval native candidate profile %s: %w", input.ProfileID, err)
	}
	var globalPurgeEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&globalPurgeEpoch); err != nil {
		return nil, fmt.Errorf("load retrieval native candidate purge epoch: %w", err)
	}
	if activeGenerationID != input.ExpectedActiveGenerationID || purgeEpoch != input.ExpectedPurgeEpoch ||
		globalPurgeEpoch != input.ExpectedPurgeEpoch || snapshotRevision != input.ExpectedActiveSnapshotRevision {
		return nil, fmt.Errorf("%w: expected root %q epoch %d snapshot %d, found root %q profile epoch %d global epoch %d snapshot %d",
			ErrRetrievalGenerationActivationStale, input.ExpectedActiveGenerationID, input.ExpectedPurgeEpoch,
			input.ExpectedActiveSnapshotRevision, activeGenerationID, purgeEpoch, globalPurgeEpoch, snapshotRevision)
	}
	var generationProfileID string
	if err := tx.QueryRowContext(ctx, `
		SELECT profile_id FROM retrieval_index_generations
		WHERE generation_id=? AND active=1 AND build_status=?`, activeGenerationID, RetrievalGenerationCompleted,
	).Scan(&generationProfileID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("active retrieval generation %s is unavailable", activeGenerationID)
		}
		return nil, fmt.Errorf("load retrieval native candidate generation %s: %w", activeGenerationID, err)
	}
	if generationProfileID != input.ProfileID {
		return nil, fmt.Errorf("active retrieval generation %s belongs to profile %s, want %s", activeGenerationID, generationProfileID, input.ProfileID)
	}

	values := make([]string, 0, len(input.Candidates))
	args := make([]any, 0, len(input.Candidates)*5+2)
	for position, candidate := range input.Candidates {
		values = append(values, "(?,?,?,?,?)")
		args = append(args, position, candidate.SegmentHash, candidate.ChunkID, candidate.Revision, candidate.VectorHash)
	}
	args = append(args, activeGenerationID, input.ProfileID)
	rows, err := tx.QueryContext(ctx, `
		WITH requested(position,segment_hash,chunk_id,revision,vector_hash) AS (VALUES `+strings.Join(values, ",")+`)
		SELECT e.chunk_id,e.profile_id,e.provider,e.model,e.dimensions,e.representation,e.normalization,
			e.vector_bytes,e.vector_hash,e.chunk_text_hash,e.revision,e.status,
			c.parent_kind,c.parent_source_key,c.evidence_role,
			CASE WHEN c.parent_kind='source' THEN COALESCE(source.source_type,'') ELSE COALESCE(item.source_type,'') END,
			c.section_ordinal,c.text,c.projection_version,c.chunker_version,c.chunk_text_hash
		FROM requested
		JOIN retrieval_generation_segments generation
			ON generation.generation_id=? AND generation.segment_hash=requested.segment_hash
		JOIN retrieval_index_segment_members member
			ON member.segment_hash=requested.segment_hash AND member.chunk_id=requested.chunk_id
			AND member.revision=requested.revision AND member.vector_hash=requested.vector_hash
		JOIN retrieval_embeddings e
			ON e.chunk_id=member.chunk_id AND e.profile_id=? AND e.status='ready'
			AND e.revision=member.revision AND e.vector_hash=member.vector_hash
		JOIN retrieval_chunks c ON c.chunk_id=member.chunk_id
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind=c.parent_kind AND parent.parent_source_key=c.parent_source_key AND parent.status='current'
		LEFT JOIN items item ON c.parent_kind='item' AND item.source_key=c.parent_source_key
		LEFT JOIN sources source ON c.parent_kind='source' AND source.source_key=c.parent_source_key
		ORDER BY requested.position`, args...)
	if err != nil {
		return nil, fmt.Errorf("read retrieval native candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalEmbeddingRow, 0, len(input.Candidates))
	for rows.Next() {
		var row RetrievalEmbeddingRow
		var currentChunkTextHash string
		if err := rows.Scan(&row.ChunkID, &row.ProfileID, &row.Provider, &row.Model, &row.Dimensions,
			&row.Representation, &row.Normalization, &row.VectorBytes, &row.VectorHash, &row.ChunkTextHash,
			&row.Revision, &row.Status, &row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole,
			&row.SourceType, &row.SectionOrdinal, &row.Text, &row.ProjectionVersion, &row.ChunkerVersion,
			&currentChunkTextHash); err != nil {
			return nil, fmt.Errorf("scan retrieval native candidate: %w", err)
		}
		if reason := retrievalEmbeddingCorruptionReason(row, currentChunkTextHash); reason != "" {
			return nil, retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, reason)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval native candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close retrieval native candidates: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit retrieval native candidate read: %w", err)
	}
	return result, nil
}

func validateRetrievalNativeCandidateRequest(input RetrievalNativeCandidateRequest) error {
	if input.ProfileID == "" || input.ExpectedActiveGenerationID == "" {
		return fmt.Errorf("retrieval native candidate profile and active generation IDs are required")
	}
	if input.ExpectedPurgeEpoch < 0 || input.ExpectedActiveSnapshotRevision <= 0 {
		return fmt.Errorf("retrieval native candidate purge epoch and snapshot revision are invalid")
	}
	if len(input.Candidates) == 0 || len(input.Candidates) > MaxRetrievalNativeCandidates {
		return fmt.Errorf("retrieval native candidate count must be between 1 and %d", MaxRetrievalNativeCandidates)
	}
	seen := make(map[string]struct{}, len(input.Candidates))
	for _, candidate := range input.Candidates {
		if candidate.SegmentHash == "" || candidate.ChunkID == "" || candidate.VectorHash == "" || candidate.Revision <= 0 {
			return fmt.Errorf("retrieval native candidate segment, chunk, revision, and vector hash are required")
		}
		key := candidate.SegmentHash + "\x00" + candidate.ChunkID
		if _, exists := seen[key]; exists {
			return fmt.Errorf("retrieval native candidate %s/%s is duplicated", candidate.SegmentHash, candidate.ChunkID)
		}
		seen[key] = struct{}{}
	}
	return nil
}
