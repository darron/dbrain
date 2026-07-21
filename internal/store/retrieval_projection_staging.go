package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

const (
	projectionTooLargeReason          = "projection_too_large_for_flat_retrieval"
	MaxRetrievalProjectionChunks      = 50_000
	MaxRetrievalProjectionOccurrences = 200_000
	MaxRetrievalProjectionStagedBytes = 128 << 20
	retrievalProjectionPlanRowID      = "__dbrain_projection_plan_v1__"
)

type RetrievalProjectionTooLargeError struct {
	ChunkCount      int
	OccurrenceCount int
	ByteCount       int64
	Limit           int
}

func (e *RetrievalProjectionTooLargeError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.OccurrenceCount > 0 {
		return fmt.Sprintf("retrieval projection contains at least %d occurrences, limit %d", e.OccurrenceCount, e.Limit)
	}
	if e.ByteCount > 0 {
		return fmt.Sprintf("retrieval projection staging contains at least %d bytes, limit %d", e.ByteCount, e.Limit)
	}
	return fmt.Sprintf("retrieval projection contains at least %d unique chunks, limit %d", e.ChunkCount, e.Limit)
}

type RetrievalProjectionStageRow struct {
	Chunk      retrievalchunk.Chunk
	Occurrence retrievalchunk.Occurrence
}

type StageRetrievalProjectionInput struct {
	WorkID          string
	DirtyRevision   int64
	ParentKind      string
	ParentSourceKey string
	ProjectionHash  string
	Cursor          retrievalchunk.Cursor
	Rows            []RetrievalProjectionStageRow
	PreparedPlan    []byte
}

type RetrievalProjectionCheckpoint struct {
	WorkID            string `json:"work_id"`
	DirtyRevision     int64  `json:"dirty_revision"`
	ParentKind        string `json:"parent_kind"`
	ParentSourceKey   string `json:"parent_source_key"`
	ProjectionHash    string `json:"projection_hash"`
	SectionKey        string `json:"section_key"`
	NextBoundary      int    `json:"next_boundary"`
	StagedChunks      int    `json:"staged_chunks"`
	StagedOccurrences int    `json:"staged_occurrences"`
	StagedBytes       int64  `json:"staged_bytes"`
	PreparedPlan      string `json:"-"`
}

func (s *Store) LoadRetrievalProjectionStaging(ctx context.Context, parent retrievalchunk.Parent, dirtyRevision int64) (RetrievalProjectionCheckpoint, bool, error) {
	projectionHash, err := retrievalchunk.ParentProjectionHash(parent)
	if err != nil {
		return RetrievalProjectionCheckpoint{}, false, err
	}
	workID := retrievalProjectionWorkID(parent.Kind, parent.SourceKey, dirtyRevision, projectionHash)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("begin retrieval projection staging load: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reserveRetrievalProjectionApplyTx(ctx, tx); err != nil {
		return RetrievalProjectionCheckpoint{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM retrieval_projection_staging
		WHERE parent_kind=? AND parent_source_key=?
			AND (dirty_revision != ? OR projection_hash != ? OR work_id != ?)`,
		parent.Kind, parent.SourceKey, dirtyRevision, projectionHash, workID); err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("discard stale retrieval projection staging: %w", err)
	}
	cp := RetrievalProjectionCheckpoint{
		WorkID: workID, DirtyRevision: dirtyRevision, ParentKind: parent.Kind,
		ParentSourceKey: parent.SourceKey, ProjectionHash: projectionHash,
	}
	err = tx.QueryRowContext(ctx, `
		SELECT section_key, next_boundary
		FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id=''
		ORDER BY updated_at DESC LIMIT 1`, workID, dirtyRevision).Scan(&cp.SectionKey, &cp.NextBoundary)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("commit retrieval projection staging cleanup: %w", err)
		}
		return RetrievalProjectionCheckpoint{}, false, nil
	}
	if err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("load retrieval projection checkpoint: %w", err)
	}
	if _, err := validateRetrievalProjectionStagingWorkTx(ctx, tx, parent.Kind, parent.SourceKey, dirtyRevision, projectionHash); err != nil {
		return RetrievalProjectionCheckpoint{}, false, err
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT chunk_id), COUNT(*), COALESCE(SUM(LENGTH(chunk_json) + LENGTH(occurrence_json)), 0)
		FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id!='' AND chunk_id!=?`, workID, dirtyRevision, retrievalProjectionPlanRowID).Scan(&cp.StagedChunks, &cp.StagedOccurrences, &cp.StagedBytes); err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("count staged retrieval projection chunks: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT chunk_json FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id=?`, workID, dirtyRevision, retrievalProjectionPlanRowID).Scan(&cp.PreparedPlan); err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("load retrieval prepared stream plan: %w", err)
	}
	if _, err := retrievalchunk.ParsePreparedStreamPlan(parent, retrievalchunk.DefaultOptions(), []byte(cp.PreparedPlan), MaxRetrievalProjectionOccurrences); err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("validate retrieval prepared stream plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RetrievalProjectionCheckpoint{}, false, fmt.Errorf("commit retrieval projection staging load: %w", err)
	}
	return cp, true, nil
}

func (s *Store) StageRetrievalProjectionBatch(ctx context.Context, input StageRetrievalProjectionInput) (RetrievalProjectionCheckpoint, error) {
	input.ParentKind = strings.TrimSpace(input.ParentKind)
	input.ParentSourceKey = strings.TrimSpace(input.ParentSourceKey)
	input.ProjectionHash = strings.TrimSpace(input.ProjectionHash)
	if input.ParentKind != "item" && input.ParentKind != "source" {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("invalid retrieval parent kind %q", input.ParentKind)
	}
	if input.ParentSourceKey == "" || input.DirtyRevision <= 0 || input.ProjectionHash == "" {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("retrieval projection staging requires parent, dirty revision, and projection hash")
	}
	wantWorkID := retrievalProjectionWorkID(input.ParentKind, input.ParentSourceKey, input.DirtyRevision, input.ProjectionHash)
	if input.WorkID == "" {
		input.WorkID = wantWorkID
	} else if input.WorkID != wantWorkID {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("retrieval projection staging work ID does not match selected work")
	}
	for _, row := range input.Rows {
		if row.Chunk.ID == "" || row.Chunk.ParentKind != input.ParentKind || row.Chunk.ParentSourceKey != input.ParentSourceKey {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("staged retrieval chunk %q does not belong to parent %s %s", row.Chunk.ID, input.ParentKind, input.ParentSourceKey)
		}
		if row.Occurrence.ChunkID != row.Chunk.ID || row.Occurrence.SectionKey != row.Chunk.SectionKey || row.Occurrence.StartChar < 0 || row.Occurrence.EndChar <= row.Occurrence.StartChar {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("invalid staged retrieval occurrence for chunk %q", row.Chunk.ID)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("begin retrieval projection staging batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reserveRetrievalProjectionApplyTx(ctx, tx); err != nil {
		return RetrievalProjectionCheckpoint{}, err
	}
	parent, err := validateRetrievalProjectionStagingWorkTx(ctx, tx, input.ParentKind, input.ParentSourceKey, input.DirtyRevision, input.ProjectionHash)
	if err != nil {
		return RetrievalProjectionCheckpoint{}, err
	}
	var storedPlan []byte
	err = tx.QueryRowContext(ctx, `
		SELECT chunk_json FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id=?`, input.WorkID, input.DirtyRevision, retrievalProjectionPlanRowID).Scan(&storedPlan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("load stored retrieval prepared stream plan: %w", err)
	}
	if len(input.PreparedPlan) == 0 && len(storedPlan) == 0 {
		plan, err := retrievalchunk.PrepareStream(parent, retrievalchunk.DefaultOptions(), MaxRetrievalProjectionOccurrences)
		if err != nil {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("prepare retrieval stream plan: %w", err)
		}
		input.PreparedPlan, err = plan.MarshalBinary()
		if err != nil {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("encode retrieval prepared stream plan: %w", err)
		}
	} else if len(input.PreparedPlan) == 0 {
		input.PreparedPlan = storedPlan
	}
	if _, err := retrievalchunk.ParsePreparedStreamPlan(parent, retrievalchunk.DefaultOptions(), input.PreparedPlan, MaxRetrievalProjectionOccurrences); err != nil {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("validate retrieval prepared stream plan: %w", err)
	}
	if len(storedPlan) != 0 && !bytes.Equal(storedPlan, input.PreparedPlan) {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("retrieval prepared stream plan changed within work item")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_projection_staging WHERE work_id=? AND dirty_revision=? AND chunk_id=''`, input.WorkID, input.DirtyRevision); err != nil {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("replace retrieval projection checkpoint: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if len(storedPlan) == 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_projection_staging (
				work_id, dirty_revision, parent_kind, parent_source_key, projection_hash,
				section_key, next_boundary, chunk_id, chunk_json, occurrence_json, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, '', -1, ?, ?, '', ?, ?)`,
			input.WorkID, input.DirtyRevision, input.ParentKind, input.ParentSourceKey, input.ProjectionHash,
			retrievalProjectionPlanRowID, input.PreparedPlan, now, now); err != nil {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("write retrieval prepared stream plan: %w", err)
		}
	}
	for _, row := range input.Rows {
		chunkJSON, err := json.Marshal(row.Chunk)
		if err != nil {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("encode staged retrieval chunk %s: %w", row.Chunk.ID, err)
		}
		occurrenceJSON, err := json.Marshal(row.Occurrence)
		if err != nil {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("encode staged retrieval occurrence %s: %w", row.Chunk.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_projection_staging (
				work_id, dirty_revision, parent_kind, parent_source_key, projection_hash,
				section_key, next_boundary, chunk_id, chunk_json, occurrence_json, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(work_id, dirty_revision, section_key, next_boundary, chunk_id) DO UPDATE SET
				chunk_json=excluded.chunk_json, occurrence_json=excluded.occurrence_json, updated_at=excluded.updated_at`,
			input.WorkID, input.DirtyRevision, input.ParentKind, input.ParentSourceKey, input.ProjectionHash,
			row.Occurrence.SectionKey, row.Occurrence.EndChar, row.Chunk.ID, string(chunkJSON), string(occurrenceJSON), now, now); err != nil {
			return RetrievalProjectionCheckpoint{}, fmt.Errorf("write staged retrieval chunk %s: %w", row.Chunk.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_projection_staging (
			work_id, dirty_revision, parent_kind, parent_source_key, projection_hash,
			section_key, next_boundary, chunk_id, chunk_json, occurrence_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, '', '', '', ?, ?)`,
		input.WorkID, input.DirtyRevision, input.ParentKind, input.ParentSourceKey, input.ProjectionHash,
		input.Cursor.SectionKey, input.Cursor.NextBoundary, now, now); err != nil {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("write retrieval projection checkpoint: %w", err)
	}
	cp := RetrievalProjectionCheckpoint{
		WorkID: input.WorkID, DirtyRevision: input.DirtyRevision, ParentKind: input.ParentKind,
		ParentSourceKey: input.ParentSourceKey, ProjectionHash: input.ProjectionHash,
		SectionKey: input.Cursor.SectionKey, NextBoundary: input.Cursor.NextBoundary,
		PreparedPlan: string(input.PreparedPlan),
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT chunk_id), COUNT(*), COALESCE(SUM(LENGTH(chunk_json) + LENGTH(occurrence_json)), 0)
		FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id!='' AND chunk_id!=?`, input.WorkID, input.DirtyRevision, retrievalProjectionPlanRowID).Scan(&cp.StagedChunks, &cp.StagedOccurrences, &cp.StagedBytes); err != nil {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("count staged retrieval chunks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RetrievalProjectionCheckpoint{}, fmt.Errorf("commit retrieval projection staging batch: %w", err)
	}
	return cp, nil
}

func (s *Store) PromoteRetrievalProjectionStaging(ctx context.Context, checkpoint RetrievalProjectionCheckpoint) (ChunkReplaceResult, error) {
	if checkpoint.WorkID == "" || checkpoint.DirtyRevision <= 0 || checkpoint.ProjectionHash == "" {
		return ChunkReplaceResult{}, fmt.Errorf("complete retrieval projection staging checkpoint is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("begin retrieval projection staging promotion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reserveRetrievalProjectionApplyTx(ctx, tx); err != nil {
		return ChunkReplaceResult{}, err
	}
	parent, err := validateRetrievalProjectionStagingWorkTx(ctx, tx, checkpoint.ParentKind, checkpoint.ParentSourceKey, checkpoint.DirtyRevision, checkpoint.ProjectionHash)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	var stagedSection string
	var stagedBoundary int
	if err := tx.QueryRowContext(ctx, `
		SELECT section_key, next_boundary
		FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND parent_kind=? AND parent_source_key=?
			AND projection_hash=? AND chunk_id=''`,
		checkpoint.WorkID, checkpoint.DirtyRevision, checkpoint.ParentKind,
		checkpoint.ParentSourceKey, checkpoint.ProjectionHash).Scan(&stagedSection, &stagedBoundary); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval projection staging work %s has no checkpoint", checkpoint.WorkID)
		}
		return ChunkReplaceResult{}, fmt.Errorf("load retrieval projection promotion checkpoint: %w", err)
	}
	if stagedSection != "" || stagedBoundary != 0 || checkpoint.SectionKey != "" || checkpoint.NextBoundary != 0 {
		return ChunkReplaceResult{}, fmt.Errorf("retrieval projection staging work %s is incomplete", checkpoint.WorkID)
	}
	var storedPlan []byte
	if err := tx.QueryRowContext(ctx, `
		SELECT chunk_json FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id=?`, checkpoint.WorkID, checkpoint.DirtyRevision, retrievalProjectionPlanRowID).Scan(&storedPlan); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("load retrieval promotion stream plan: %w", err)
	}
	if checkpoint.PreparedPlan != "" && !bytes.Equal([]byte(checkpoint.PreparedPlan), storedPlan) {
		return ChunkReplaceResult{}, fmt.Errorf("retrieval promotion stream plan does not match durable plan")
	}
	if _, err := retrievalchunk.ParsePreparedStreamPlan(parent, retrievalchunk.DefaultOptions(), storedPlan, MaxRetrievalProjectionOccurrences); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("validate retrieval promotion stream plan: %w", err)
	}
	var stagedOccurrences int
	var stagedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(LENGTH(chunk_json) + LENGTH(occurrence_json)), 0) FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND chunk_id!='' AND chunk_id!=?`, checkpoint.WorkID, checkpoint.DirtyRevision, retrievalProjectionPlanRowID).Scan(&stagedOccurrences, &stagedBytes); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("count retrieval promotion occurrences: %w", err)
	}
	if stagedOccurrences > MaxRetrievalProjectionOccurrences {
		return ChunkReplaceResult{}, &RetrievalProjectionTooLargeError{OccurrenceCount: stagedOccurrences, Limit: MaxRetrievalProjectionOccurrences}
	}
	if stagedBytes > MaxRetrievalProjectionStagedBytes {
		return ChunkReplaceResult{}, &RetrievalProjectionTooLargeError{ByteCount: stagedBytes, Limit: MaxRetrievalProjectionStagedBytes}
	}
	rows, err := loadStagedRetrievalProjectionRows(ctx, tx, checkpoint)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	if len(rows) == 0 {
		return ChunkReplaceResult{}, fmt.Errorf("retrieval projection staging work %s has no chunks", checkpoint.WorkID)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Chunk.SectionOrdinal != rows[j].Chunk.SectionOrdinal {
			return rows[i].Chunk.SectionOrdinal < rows[j].Chunk.SectionOrdinal
		}
		if rows[i].Occurrence.StartChar != rows[j].Occurrence.StartChar {
			return rows[i].Occurrence.StartChar < rows[j].Occurrence.StartChar
		}
		if rows[i].Occurrence.EndChar != rows[j].Occurrence.EndChar {
			return rows[i].Occurrence.EndChar < rows[j].Occurrence.EndChar
		}
		return rows[i].Chunk.ID < rows[j].Chunk.ID
	})
	projection := retrievalchunk.Projection{ParentHash: checkpoint.ProjectionHash, Chunks: make([]retrievalchunk.Chunk, 0), Occurrences: make([]retrievalchunk.Occurrence, 0, len(rows))}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.Chunk.ID]; !ok {
			projection.Chunks = append(projection.Chunks, row.Chunk)
			seen[row.Chunk.ID] = struct{}{}
		}
		projection.Occurrences = append(projection.Occurrences, row.Occurrence)
	}
	authoritative, err := buildBoundedAuthoritativeProjection(parent, retrievalchunk.DefaultOptions(), MaxRetrievalProjectionChunks)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	if !reflect.DeepEqual(projection, authoritative) {
		return ChunkReplaceResult{}, fmt.Errorf("staged retrieval projection does not match authoritative projection")
	}
	result, err := replaceRetrievalProjectionTx(ctx, tx, checkpoint.ParentKind, checkpoint.ParentSourceKey, projection)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	write, err := tx.ExecContext(ctx, `
		UPDATE retrieval_parent_projections
		SET projection_hash=?, projection_version=?, chunker_version=?, status='current',
			chunk_count=?, reason='', projected_revision=?, projected_at=?, updated_at=?
		WHERE parent_kind=? AND parent_source_key=? AND dirty_revision=? AND projected_revision < dirty_revision`,
		checkpoint.ProjectionHash, retrievalchunk.ProjectionVersion, retrievalchunk.Version,
		len(projection.Chunks), checkpoint.DirtyRevision, now, now,
		checkpoint.ParentKind, checkpoint.ParentSourceKey, checkpoint.DirtyRevision)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("promote retrieval projection parent state: %w", err)
	}
	affected, err := write.RowsAffected()
	if err != nil || affected != 1 {
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("inspect retrieval projection promotion: %w", err)
		}
		return ChunkReplaceResult{}, &RetrievalProjectionStaleWorkError{ParentKind: checkpoint.ParentKind, ParentSourceKey: checkpoint.ParentSourceKey, SelectedRevision: checkpoint.DirtyRevision, CurrentRevision: checkpoint.DirtyRevision, Reason: "work item changed during promotion"}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_projection_staging WHERE work_id=? AND dirty_revision=?`, checkpoint.WorkID, checkpoint.DirtyRevision); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("remove promoted retrieval projection staging: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("commit retrieval projection staging promotion: %w", err)
	}
	return result, nil
}

func buildBoundedAuthoritativeProjection(parent retrievalchunk.Parent, opts retrievalchunk.Options, maxChunks int) (retrievalchunk.Projection, error) {
	if maxChunks <= 0 {
		return retrievalchunk.Projection{}, fmt.Errorf("retrieval projection chunk limit must be positive")
	}
	plan, err := retrievalchunk.PrepareStream(parent, opts, MaxRetrievalProjectionOccurrences)
	if err != nil {
		var occurrences *retrievalchunk.PreparedStreamOccurrenceLimitError
		if errors.As(err, &occurrences) {
			return retrievalchunk.Projection{}, &RetrievalProjectionTooLargeError{OccurrenceCount: occurrences.OccurrenceCount, Limit: occurrences.Limit}
		}
		return retrievalchunk.Projection{}, err
	}
	projectionHash, err := retrievalchunk.ParentProjectionHash(parent)
	if err != nil {
		return retrievalchunk.Projection{}, err
	}
	projection := retrievalchunk.Projection{
		ParentHash:  projectionHash,
		Chunks:      make([]retrievalchunk.Chunk, 0, min(maxChunks, 1_000)),
		Occurrences: make([]retrievalchunk.Occurrence, 0, min(maxChunks, 1_000)),
	}
	seen := make(map[string]struct{}, min(maxChunks, 1_000))
	_, done, err := retrievalchunk.StreamPrepared(parent, opts, plan, retrievalchunk.Cursor{}, func(chunk retrievalchunk.Chunk, occurrence retrievalchunk.Occurrence) error {
		if _, ok := seen[chunk.ID]; !ok {
			seen[chunk.ID] = struct{}{}
			projection.Chunks = append(projection.Chunks, chunk)
			if len(seen) > maxChunks {
				return &RetrievalProjectionTooLargeError{ChunkCount: len(seen), Limit: maxChunks}
			}
		}
		projection.Occurrences = append(projection.Occurrences, occurrence)
		return nil
	})
	if err != nil {
		return retrievalchunk.Projection{}, err
	}
	if !done {
		return retrievalchunk.Projection{}, fmt.Errorf("authoritative retrieval projection stream ended without completion")
	}
	return projection, nil
}

func (s *Store) BlockRetrievalProjectionTooLarge(ctx context.Context, parent retrievalchunk.Parent, dirtyRevision int64, projectionHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin oversized retrieval projection block: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reserveRetrievalProjectionApplyTx(ctx, tx); err != nil {
		return err
	}
	if _, err := validateRetrievalProjectionStagingWorkTx(ctx, tx, parent.Kind, parent.SourceKey, dirtyRevision, projectionHash); err != nil {
		return err
	}
	if _, err := replaceRetrievalProjectionTx(ctx, tx, parent.Kind, parent.SourceKey, retrievalchunk.Projection{ParentHash: projectionHash}); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `
		UPDATE retrieval_parent_projections
		SET projection_hash=?, projection_version=?, chunker_version=?, status='blocked',
			chunk_count=0, reason=?, projected_revision=?, projected_at=?, updated_at=?
		WHERE parent_kind=? AND parent_source_key=? AND dirty_revision=? AND projected_revision < dirty_revision`,
		projectionHash, retrievalchunk.ProjectionVersion, retrievalchunk.Version, projectionTooLargeReason,
		dirtyRevision, now, now, parent.Kind, parent.SourceKey, dirtyRevision)
	if err != nil {
		return fmt.Errorf("block oversized retrieval projection: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return fmt.Errorf("inspect oversized retrieval projection block: %w", err)
		}
		return &RetrievalProjectionStaleWorkError{ParentKind: parent.Kind, ParentSourceKey: parent.SourceKey, SelectedRevision: dirtyRevision, CurrentRevision: dirtyRevision, Reason: "work item changed during oversized block"}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_projection_staging WHERE parent_kind=? AND parent_source_key=?`, parent.Kind, parent.SourceKey); err != nil {
		return fmt.Errorf("discard oversized retrieval projection staging: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit oversized retrieval projection block: %w", err)
	}
	return nil
}

func validateRetrievalProjectionStagingWorkTx(ctx context.Context, tx *sql.Tx, kind, sourceKey string, dirtyRevision int64, projectionHash string) (retrievalchunk.Parent, error) {
	var currentRevision, projectedRevision int64
	err := tx.QueryRowContext(ctx, `
		SELECT dirty_revision, projected_revision FROM retrieval_parent_projections
		WHERE parent_kind=? AND parent_source_key=?`, kind, sourceKey).Scan(&currentRevision, &projectedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return retrievalchunk.Parent{}, &RetrievalProjectionStaleWorkError{ParentKind: kind, ParentSourceKey: sourceKey, SelectedRevision: dirtyRevision, Reason: "work item no longer exists"}
	}
	if err != nil {
		return retrievalchunk.Parent{}, fmt.Errorf("load staged retrieval projection work item: %w", err)
	}
	if currentRevision != dirtyRevision || projectedRevision >= dirtyRevision {
		return retrievalchunk.Parent{}, &RetrievalProjectionStaleWorkError{ParentKind: kind, ParentSourceKey: sourceKey, SelectedRevision: dirtyRevision, CurrentRevision: currentRevision, Reason: "dirty revision no longer matches"}
	}
	parent, exists, eligible, err := loadCurrentRetrievalParent(ctx, tx, kind, sourceKey)
	if err != nil {
		return retrievalchunk.Parent{}, err
	}
	if !exists || !eligible {
		return retrievalchunk.Parent{}, &RetrievalProjectionStaleWorkError{ParentKind: kind, ParentSourceKey: sourceKey, SelectedRevision: dirtyRevision, CurrentRevision: currentRevision, Reason: "parent is no longer eligible"}
	}
	currentHash, err := retrievalchunk.ParentProjectionHash(parent)
	if err != nil {
		return retrievalchunk.Parent{}, err
	}
	if currentHash != projectionHash {
		return retrievalchunk.Parent{}, &RetrievalProjectionStaleWorkError{ParentKind: kind, ParentSourceKey: sourceKey, SelectedRevision: dirtyRevision, CurrentRevision: currentRevision, Reason: "parent projection hash changed"}
	}
	return parent, nil
}

func loadStagedRetrievalProjectionRows(ctx context.Context, tx *sql.Tx, checkpoint RetrievalProjectionCheckpoint) ([]RetrievalProjectionStageRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT chunk_json, occurrence_json
		FROM retrieval_projection_staging
		WHERE work_id=? AND dirty_revision=? AND parent_kind=? AND parent_source_key=?
			AND projection_hash=? AND chunk_id!='' AND chunk_id!=?`, checkpoint.WorkID, checkpoint.DirtyRevision,
		checkpoint.ParentKind, checkpoint.ParentSourceKey, checkpoint.ProjectionHash, retrievalProjectionPlanRowID)
	if err != nil {
		return nil, fmt.Errorf("list staged retrieval projection: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalProjectionStageRow, 0)
	for rows.Next() {
		var chunkJSON, occurrenceJSON string
		if err := rows.Scan(&chunkJSON, &occurrenceJSON); err != nil {
			return nil, fmt.Errorf("scan staged retrieval projection: %w", err)
		}
		var row RetrievalProjectionStageRow
		if err := json.Unmarshal([]byte(chunkJSON), &row.Chunk); err != nil {
			return nil, fmt.Errorf("decode staged retrieval chunk: %w", err)
		}
		if err := json.Unmarshal([]byte(occurrenceJSON), &row.Occurrence); err != nil {
			return nil, fmt.Errorf("decode staged retrieval occurrence: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate staged retrieval projection: %w", err)
	}
	return result, nil
}

func retrievalProjectionWorkID(kind, sourceKey string, dirtyRevision int64, projectionHash string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%d:%s%d:%d%d:%s", len(kind), kind, len(sourceKey), sourceKey, len(fmt.Sprint(dirtyRevision)), dirtyRevision, len(projectionHash), projectionHash)))
	return hex.EncodeToString(digest[:])
}
