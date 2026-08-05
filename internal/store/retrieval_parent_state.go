package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

type RetrievalParentWork struct {
	Parent        retrievalchunk.Parent
	DirtyRevision int64
}

type ApplyRetrievalProjectionInput struct {
	ParentKind         string
	ParentSourceKey    string
	DirtyRevision      int64
	ExpectedPurgeEpoch int64
	Projection         retrievalchunk.Projection
	Status             RetrievalProjectionStatus
	Reason             string
}

// RetrievalProjectionStaleWorkError reports a projection result that no longer
// matches the durable parent work item. Callers may discard it; the current
// dirty state is deliberately left untouched.
type RetrievalProjectionStaleWorkError struct {
	ParentKind       string
	ParentSourceKey  string
	SelectedRevision int64
	CurrentRevision  int64
	Reason           string
}

func (e *RetrievalProjectionStaleWorkError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("stale retrieval projection for %s %s at revision %d (current %d): %s", e.ParentKind, e.ParentSourceKey, e.SelectedRevision, e.CurrentRevision, e.Reason)
}

func (s *Store) ProjectionWorkRevision(ctx context.Context) (int64, error) {
	var revision int64
	if err := s.queryer().QueryRowContext(ctx, `SELECT projection_work_revision FROM retrieval_state WHERE singleton = 1`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read retrieval projection work revision: %w", err)
	}
	return revision, nil
}

// allocateRetrievalParentDirtyTx allocates the queue identity and writes the
// dirty state in the caller's transaction. Task 4's authoritative mutation
// coverage will call this primitive through its public trigger/helper seam.
func allocateRetrievalParentDirtyTx(ctx context.Context, tx *sql.Tx, kind, sourceKey string) (int64, error) {
	kind = strings.TrimSpace(kind)
	sourceKey = strings.TrimSpace(sourceKey)
	if kind != "item" && kind != "source" {
		return 0, fmt.Errorf("invalid retrieval parent kind %q", kind)
	}
	if sourceKey == "" {
		return 0, fmt.Errorf("retrieval parent source key is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `
		UPDATE retrieval_state
		SET projection_work_revision = projection_work_revision + 1, updated_at = ?
		WHERE singleton = 1`, now)
	if err != nil {
		return 0, fmt.Errorf("allocate retrieval projection work revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("inspect retrieval projection work allocation: %w", err)
	}
	if affected != 1 {
		return 0, fmt.Errorf("retrieval state singleton is missing")
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT projection_work_revision FROM retrieval_state WHERE singleton = 1`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read allocated retrieval projection work revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_parent_projections (
			parent_kind, parent_source_key, projection_version, chunker_version,
			status, reason, dirty_at, dirty_revision, updated_at
		) VALUES (?, ?, ?, ?, ?, '', ?, ?, ?)
		ON CONFLICT(parent_kind, parent_source_key) DO UPDATE SET
			projection_version = excluded.projection_version,
			chunker_version = excluded.chunker_version,
			status = excluded.status,
			reason = '',
			dirty_at = excluded.dirty_at,
			dirty_revision = excluded.dirty_revision,
			updated_at = excluded.updated_at`,
		kind, sourceKey, retrievalchunk.ProjectionVersion, retrievalchunk.Version,
		RetrievalProjectionPending, now, revision, now); err != nil {
		return 0, fmt.Errorf("mark retrieval parent %s %s dirty: %w", kind, sourceKey, err)
	}
	return revision, nil
}

func (s *Store) ListDirtyRetrievalParents(ctx context.Context, watermark int64, limit int) ([]RetrievalParentWork, error) {
	if watermark < 0 {
		return nil, fmt.Errorf("retrieval projection watermark must not be negative")
	}
	if limit <= 0 || watermark == 0 {
		return make([]RetrievalParentWork, 0), nil
	}
	rows, err := s.queryer().QueryContext(ctx, `
		SELECT parent_kind, parent_source_key, dirty_revision
		FROM retrieval_parent_projections
		WHERE dirty_revision <= ? AND projected_revision < dirty_revision
		ORDER BY dirty_revision, parent_kind, parent_source_key
		LIMIT ?`, watermark, limit)
	if err != nil {
		return nil, fmt.Errorf("list dirty retrieval parent identities: %w", err)
	}
	type identity struct {
		kind, sourceKey string
		revision        int64
	}
	identities := make([]identity, 0, limit)
	for rows.Next() {
		var value identity
		if err := rows.Scan(&value.kind, &value.sourceKey, &value.revision); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan dirty retrieval parent identity: %w", err)
		}
		identities = append(identities, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate dirty retrieval parent identities: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close dirty retrieval parent identities: %w", err)
	}

	work := make([]RetrievalParentWork, 0, len(identities))
	for _, selected := range identities {
		parent, _, _, err := loadCurrentRetrievalParent(ctx, s.queryer(), selected.kind, selected.sourceKey)
		if err != nil {
			return nil, err
		}
		work = append(work, RetrievalParentWork{Parent: parent, DirtyRevision: selected.revision})
	}
	return work, nil
}

func (s *Store) CountDirtyRetrievalParents(ctx context.Context, watermark int64) (int, error) {
	if watermark < 0 {
		return 0, fmt.Errorf("retrieval projection watermark must not be negative")
	}
	if watermark == 0 {
		return 0, nil
	}
	var count int
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM retrieval_parent_projections
		WHERE dirty_revision <= ? AND projected_revision < dirty_revision`, watermark).Scan(&count); err != nil {
		return 0, fmt.Errorf("count dirty retrieval parents: %w", err)
	}
	return count, nil
}

func (s *Store) ApplyRetrievalProjection(ctx context.Context, input ApplyRetrievalProjectionInput) (ChunkReplaceResult, error) {
	if err := validateRetrievalProjectionApplyInput(&input); err != nil {
		return ChunkReplaceResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("begin retrieval projection apply: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reserveRetrievalProjectionApplyTx(ctx, tx); err != nil {
		return ChunkReplaceResult{}, err
	}
	if err := validateRetrievalProjectionPurgeEpochTx(ctx, tx, input.ExpectedPurgeEpoch); err != nil {
		return ChunkReplaceResult{}, err
	}
	result, err := applyRetrievalProjectionReservedTx(ctx, tx, input)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	if err := validateRetrievalProjectionPurgeEpochTx(ctx, tx, input.ExpectedPurgeEpoch); err != nil {
		return ChunkReplaceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("commit retrieval projection apply: %w", err)
	}
	return result, nil
}

func validateRetrievalProjectionApplyInput(input *ApplyRetrievalProjectionInput) error {
	input.ParentKind = strings.TrimSpace(input.ParentKind)
	input.ParentSourceKey = strings.TrimSpace(input.ParentSourceKey)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ParentKind != "item" && input.ParentKind != "source" {
		return fmt.Errorf("invalid retrieval parent kind %q", input.ParentKind)
	}
	if input.ParentSourceKey == "" || input.DirtyRevision <= 0 {
		return fmt.Errorf("retrieval projection parent and positive dirty revision are required")
	}
	if input.ExpectedPurgeEpoch < 0 {
		return fmt.Errorf("retrieval projection expected purge epoch must not be negative")
	}
	if input.Status != RetrievalProjectionCurrent && input.Status != RetrievalProjectionEmpty {
		return fmt.Errorf("only current and empty retrieval projection applies are supported, got %q", input.Status)
	}
	if input.Status == RetrievalProjectionCurrent && len(input.Projection.Chunks) == 0 {
		return fmt.Errorf("current retrieval projection must contain chunks")
	}
	if input.Status == RetrievalProjectionCurrent && input.Reason != "" {
		return fmt.Errorf("current retrieval projection must not contain a reason")
	}
	if input.Status != RetrievalProjectionCurrent && len(input.Projection.Chunks) != 0 {
		return fmt.Errorf("%s retrieval projection must not contain chunks", input.Status)
	}
	if input.Status != RetrievalProjectionCurrent && len(input.Projection.Occurrences) != 0 {
		return fmt.Errorf("%s retrieval projection must not contain occurrences", input.Status)
	}
	if input.Status != RetrievalProjectionCurrent && input.Reason == "" {
		return fmt.Errorf("%s retrieval projection requires a reason", input.Status)
	}
	if input.Status == RetrievalProjectionEmpty && input.Reason != "no_chunkable_content" {
		return fmt.Errorf("empty retrieval projection reason must be no_chunkable_content")
	}
	return nil
}

func reserveRetrievalProjectionApplyTx(ctx context.Context, tx *sql.Tx) error {
	// Acquire SQLite's writer reservation before validating. A concurrent
	// dirtying commit therefore linearizes either before this validation (and is
	// rejected below) or after this complete apply transaction.
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_state SET updated_at = updated_at WHERE singleton = 1`); err != nil {
		return fmt.Errorf("reserve retrieval projection apply transaction: %w", err)
	}
	return nil
}

// applyRetrievalProjectionReservedTx applies an already validated input after
// reserveRetrievalProjectionApplyTx has acquired SQLite's writer reservation.
// Keeping this core separate lets concurrency tests pause at the actual
// linearization point used by the public API.
func applyRetrievalProjectionReservedTx(ctx context.Context, tx *sql.Tx, input ApplyRetrievalProjectionInput) (ChunkReplaceResult, error) {
	var currentRevision, projectedRevision int64
	err := tx.QueryRowContext(ctx, `
		SELECT dirty_revision, projected_revision
		FROM retrieval_parent_projections
		WHERE parent_kind = ? AND parent_source_key = ?`, input.ParentKind, input.ParentSourceKey).Scan(&currentRevision, &projectedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ChunkReplaceResult{}, staleRetrievalProjectionError(input, 0, "work item no longer exists")
	}
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("load retrieval projection work item: %w", err)
	}
	if currentRevision != input.DirtyRevision || projectedRevision >= input.DirtyRevision {
		return ChunkReplaceResult{}, staleRetrievalProjectionError(input, currentRevision, "dirty revision no longer matches")
	}

	parent, exists, _, err := loadCurrentRetrievalParent(ctx, tx, input.ParentKind, input.ParentSourceKey)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	fresh, err := retrievalchunk.BuildProjection(parent, retrievalchunk.DefaultOptions())
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("recompute current retrieval parent projection: %w", err)
	}
	if fresh.ParentHash != input.Projection.ParentHash {
		return ChunkReplaceResult{}, staleRetrievalProjectionError(input, currentRevision, "parent projection hash changed")
	}
	if !reflect.DeepEqual(fresh, input.Projection) {
		return ChunkReplaceResult{}, fmt.Errorf("retrieval projection payload does not match freshly computed parent projection")
	}
	if input.Status == RetrievalProjectionCurrent && len(fresh.Chunks) == 0 || input.Status == RetrievalProjectionEmpty && len(fresh.Chunks) != 0 {
		return ChunkReplaceResult{}, fmt.Errorf("retrieval projection status %q does not match chunk count %d", input.Status, len(fresh.Chunks))
	}

	result, err := replaceRetrievalProjectionTx(ctx, tx, input.ParentKind, input.ParentSourceKey, input.Projection)
	if err != nil {
		return ChunkReplaceResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if !exists {
		write, err := tx.ExecContext(ctx, `DELETE FROM retrieval_parent_projections WHERE parent_kind=? AND parent_source_key=? AND dirty_revision=? AND projected_revision < dirty_revision`, input.ParentKind, input.ParentSourceKey, input.DirtyRevision)
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("remove deleted retrieval parent projection state: %w", err)
		}
		if affected, err := write.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return ChunkReplaceResult{}, fmt.Errorf("inspect deleted retrieval parent projection state: %w", err)
			}
			return ChunkReplaceResult{}, staleRetrievalProjectionError(input, currentRevision, "work item changed during cleanup")
		}
	} else {
		write, err := tx.ExecContext(ctx, `
			UPDATE retrieval_parent_projections
			SET projection_hash=?, projection_version=?, chunker_version=?, status=?,
				chunk_count=?, reason=?, projected_revision=?, projected_at=?, updated_at=?
			WHERE parent_kind=? AND parent_source_key=? AND dirty_revision=? AND projected_revision < dirty_revision`,
			input.Projection.ParentHash, retrievalchunk.ProjectionVersion, retrievalchunk.Version, input.Status,
			len(input.Projection.Chunks), input.Reason, input.DirtyRevision, now, now,
			input.ParentKind, input.ParentSourceKey, input.DirtyRevision)
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("write retrieval parent projection state: %w", err)
		}
		if affected, err := write.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return ChunkReplaceResult{}, fmt.Errorf("inspect retrieval parent projection state: %w", err)
			}
			return ChunkReplaceResult{}, staleRetrievalProjectionError(input, currentRevision, "work item changed during apply")
		}
	}
	return result, nil
}

func staleRetrievalProjectionError(input ApplyRetrievalProjectionInput, current int64, reason string) error {
	return &RetrievalProjectionStaleWorkError{
		ParentKind: input.ParentKind, ParentSourceKey: input.ParentSourceKey,
		SelectedRevision: input.DirtyRevision, CurrentRevision: current, Reason: reason,
	}
}
