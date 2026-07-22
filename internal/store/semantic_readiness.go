package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticreadiness"
)

var retrievalRuntimeReadinessCounterTriggers = []retrievalConstraintTrigger{
	{
		name: "trg_retrieval_embeddings_readiness_count_insert", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_readiness_count_insert
			AFTER INSERT ON retrieval_embeddings
			BEGIN
				UPDATE retrieval_embedding_profiles SET
					ready_embedding_count=ready_embedding_count+(NEW.status='ready'),
					pending_embedding_count=pending_embedding_count+(NEW.status='pending'),
					blocked_embedding_count=blocked_embedding_count+(NEW.status='blocked'),
					error_embedding_count=error_embedding_count+(NEW.status='error'),
					corrupt_embedding_count=corrupt_embedding_count+(NEW.status='blocked' AND NEW.last_error LIKE 'corrupt:%')
				WHERE profile_id=NEW.profile_id;
			END`,
	},
	{
		name: "trg_retrieval_embeddings_readiness_count_delete_guard", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_readiness_count_delete_guard
			BEFORE DELETE ON retrieval_embeddings
			BEGIN
				SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM retrieval_embedding_profiles WHERE profile_id=OLD.profile_id)
					THEN RAISE(ABORT, 'retrieval embedding profile readiness aggregate row is missing') END;
				SELECT CASE WHEN EXISTS (
					SELECT 1 FROM retrieval_embedding_profiles WHERE profile_id=OLD.profile_id AND (
						(OLD.status='ready' AND ready_embedding_count<=0) OR
						(OLD.status='pending' AND pending_embedding_count<=0) OR
						(OLD.status='blocked' AND blocked_embedding_count<=0) OR
						(OLD.status='error' AND error_embedding_count<=0) OR
						(OLD.status='blocked' AND OLD.last_error LIKE 'corrupt:%' AND corrupt_embedding_count<=0)
					)) THEN RAISE(ABORT, 'retrieval embedding profile readiness aggregate drift') END;
			END`,
	},
	{
		name: "trg_retrieval_embeddings_readiness_count_delete", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_readiness_count_delete
			AFTER DELETE ON retrieval_embeddings
			BEGIN
				UPDATE retrieval_embedding_profiles SET
					ready_embedding_count=ready_embedding_count-(OLD.status='ready'),
					pending_embedding_count=pending_embedding_count-(OLD.status='pending'),
					blocked_embedding_count=blocked_embedding_count-(OLD.status='blocked'),
					error_embedding_count=error_embedding_count-(OLD.status='error'),
					corrupt_embedding_count=corrupt_embedding_count-(OLD.status='blocked' AND OLD.last_error LIKE 'corrupt:%')
				WHERE profile_id=OLD.profile_id;
			END`,
	},
	{
		name: "trg_retrieval_embeddings_readiness_count_update", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_readiness_count_update
			AFTER UPDATE OF profile_id,status,last_error ON retrieval_embeddings
			BEGIN
				UPDATE retrieval_embedding_profiles SET
					ready_embedding_count=ready_embedding_count-(OLD.status='ready'),
					pending_embedding_count=pending_embedding_count-(OLD.status='pending'),
					blocked_embedding_count=blocked_embedding_count-(OLD.status='blocked'),
					error_embedding_count=error_embedding_count-(OLD.status='error'),
					corrupt_embedding_count=corrupt_embedding_count-(OLD.status='blocked' AND OLD.last_error LIKE 'corrupt:%')
				WHERE profile_id=OLD.profile_id;
				UPDATE retrieval_embedding_profiles SET
					ready_embedding_count=ready_embedding_count+(NEW.status='ready'),
					pending_embedding_count=pending_embedding_count+(NEW.status='pending'),
					blocked_embedding_count=blocked_embedding_count+(NEW.status='blocked'),
					error_embedding_count=error_embedding_count+(NEW.status='error'),
					corrupt_embedding_count=corrupt_embedding_count+(NEW.status='blocked' AND NEW.last_error LIKE 'corrupt:%')
				WHERE profile_id=NEW.profile_id;
			END`,
	},
}

var retrievalRuntimeProjectionCounterTriggers = []retrievalConstraintTrigger{
	{
		name: "trg_retrieval_parent_readiness_count_insert", table: "retrieval_parent_projections",
		sql: `CREATE TRIGGER trg_retrieval_parent_readiness_count_insert
			AFTER INSERT ON retrieval_parent_projections BEGIN
				UPDATE retrieval_state SET
					projection_parent_count=projection_parent_count+1,
					current_parent_count=current_parent_count+(NEW.status='current' AND NEW.projected_revision>=NEW.dirty_revision),
					empty_parent_count=empty_parent_count+(NEW.status='empty' AND NEW.projected_revision>=NEW.dirty_revision),
					pending_parent_count=pending_parent_count+(NEW.status='pending' OR NEW.projected_revision<NEW.dirty_revision),
					blocked_parent_count=blocked_parent_count+(NEW.status='blocked'),
					error_parent_count=error_parent_count+(NEW.status='error'),
					dirty_parent_count=dirty_parent_count+(NEW.projected_revision<NEW.dirty_revision),
					current_chunk_count=current_chunk_count+(CASE WHEN NEW.status='current' AND NEW.projected_revision>=NEW.dirty_revision THEN NEW.chunk_count ELSE 0 END)
				WHERE singleton=1;
			END`,
	},
	{
		name: "trg_retrieval_parent_readiness_count_delete", table: "retrieval_parent_projections",
		sql: `CREATE TRIGGER trg_retrieval_parent_readiness_count_delete
			AFTER DELETE ON retrieval_parent_projections BEGIN
				UPDATE retrieval_state SET
					projection_parent_count=projection_parent_count-1,
					current_parent_count=current_parent_count-(OLD.status='current' AND OLD.projected_revision>=OLD.dirty_revision),
					empty_parent_count=empty_parent_count-(OLD.status='empty' AND OLD.projected_revision>=OLD.dirty_revision),
					pending_parent_count=pending_parent_count-(OLD.status='pending' OR OLD.projected_revision<OLD.dirty_revision),
					blocked_parent_count=blocked_parent_count-(OLD.status='blocked'),
					error_parent_count=error_parent_count-(OLD.status='error'),
					dirty_parent_count=dirty_parent_count-(OLD.projected_revision<OLD.dirty_revision),
					current_chunk_count=current_chunk_count-(CASE WHEN OLD.status='current' AND OLD.projected_revision>=OLD.dirty_revision THEN OLD.chunk_count ELSE 0 END)
				WHERE singleton=1;
			END`,
	},
	{
		name: "trg_retrieval_parent_readiness_count_update", table: "retrieval_parent_projections",
		sql: `CREATE TRIGGER trg_retrieval_parent_readiness_count_update
			AFTER UPDATE OF status,chunk_count,dirty_revision,projected_revision ON retrieval_parent_projections BEGIN
				UPDATE retrieval_state SET
					current_parent_count=current_parent_count-(OLD.status='current' AND OLD.projected_revision>=OLD.dirty_revision)+(NEW.status='current' AND NEW.projected_revision>=NEW.dirty_revision),
					empty_parent_count=empty_parent_count-(OLD.status='empty' AND OLD.projected_revision>=OLD.dirty_revision)+(NEW.status='empty' AND NEW.projected_revision>=NEW.dirty_revision),
					pending_parent_count=pending_parent_count-(OLD.status='pending' OR OLD.projected_revision<OLD.dirty_revision)+(NEW.status='pending' OR NEW.projected_revision<NEW.dirty_revision),
					blocked_parent_count=blocked_parent_count-(OLD.status='blocked')+(NEW.status='blocked'),
					error_parent_count=error_parent_count-(OLD.status='error')+(NEW.status='error'),
					dirty_parent_count=dirty_parent_count-(OLD.projected_revision<OLD.dirty_revision)+(NEW.projected_revision<NEW.dirty_revision),
					current_chunk_count=current_chunk_count-(CASE WHEN OLD.status='current' AND OLD.projected_revision>=OLD.dirty_revision THEN OLD.chunk_count ELSE 0 END)+(CASE WHEN NEW.status='current' AND NEW.projected_revision>=NEW.dirty_revision THEN NEW.chunk_count ELSE 0 END)
				WHERE singleton=1;
			END`,
	},
}

// RepairRetrievalRuntimeReadinessCounters rebuilds the migration-21 projection
// and embedding-profile aggregates from authoritative rows. Trigger replacement,
// both backfills, and supporting indexes commit atomically in one cancellable
// write transaction.
func (s *Store) RepairRetrievalRuntimeReadinessCounters(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retrieval runtime readiness counter repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureColumnsTxContext(ctx, tx, "retrieval_state", []columnDefinition{
		{Name: "projection_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "current_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "empty_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "pending_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "blocked_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "error_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "dirty_parent_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "current_chunk_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return fmt.Errorf("ensure retrieval runtime projection counters: %w", err)
	}
	if err := ensureColumnsTxContext(ctx, tx, "retrieval_embedding_profiles", []columnDefinition{
		{Name: "ready_embedding_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "pending_embedding_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "blocked_embedding_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "error_embedding_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "corrupt_embedding_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return fmt.Errorf("ensure retrieval runtime readiness counters: %w", err)
	}
	// Install the counter-maintenance triggers before the authoritative
	// backfill. The first schema write takes SQLite's writer lock; keeping the
	// trigger replacement, backfill, and indexes in this transaction prevents a
	// concurrent writer from landing in an uncounted gap.
	for _, trigger := range retrievalRuntimeReadinessCounterTriggers {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+trigger.name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, trigger.sql); err != nil {
			return fmt.Errorf("create retrieval runtime readiness trigger %s: %w", trigger.name, err)
		}
	}
	for _, trigger := range retrievalRuntimeProjectionCounterTriggers {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+trigger.name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, trigger.sql); err != nil {
			return fmt.Errorf("create retrieval runtime projection trigger %s: %w", trigger.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embedding_profiles SET
			ready_embedding_count=(SELECT COUNT(*) FROM retrieval_embeddings e WHERE e.profile_id=retrieval_embedding_profiles.profile_id AND e.status='ready'),
			pending_embedding_count=(SELECT COUNT(*) FROM retrieval_embeddings e WHERE e.profile_id=retrieval_embedding_profiles.profile_id AND e.status='pending'),
			blocked_embedding_count=(SELECT COUNT(*) FROM retrieval_embeddings e WHERE e.profile_id=retrieval_embedding_profiles.profile_id AND e.status='blocked'),
			error_embedding_count=(SELECT COUNT(*) FROM retrieval_embeddings e WHERE e.profile_id=retrieval_embedding_profiles.profile_id AND e.status='error'),
			corrupt_embedding_count=(SELECT COUNT(*) FROM retrieval_embeddings e WHERE e.profile_id=retrieval_embedding_profiles.profile_id AND e.status='blocked' AND e.last_error LIKE 'corrupt:%')`); err != nil {
		return fmt.Errorf("backfill retrieval runtime readiness counters: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_state SET
		projection_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections),
		current_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections WHERE status='current' AND projected_revision>=dirty_revision),
		empty_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections WHERE status='empty' AND projected_revision>=dirty_revision),
		pending_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections WHERE status='pending' OR projected_revision<dirty_revision),
		blocked_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections WHERE status='blocked'),
		error_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections WHERE status='error'),
		dirty_parent_count=(SELECT COUNT(*) FROM retrieval_parent_projections WHERE projected_revision<dirty_revision),
		current_chunk_count=(SELECT COALESCE(SUM(chunk_count),0) FROM retrieval_parent_projections WHERE status='current' AND projected_revision>=dirty_revision)
		WHERE singleton=1`); err != nil {
		return fmt.Errorf("backfill retrieval runtime projection counters: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_retrieval_parent_projections_dirty_age ON retrieval_parent_projections(dirty_at) WHERE projected_revision<dirty_revision`); err != nil {
		return fmt.Errorf("create retrieval runtime dirty age index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_retrieval_parent_projections_dirty_keyset ON retrieval_parent_projections(dirty_revision,parent_kind,parent_source_key) WHERE projected_revision<dirty_revision`); err != nil {
		return fmt.Errorf("create retrieval runtime dirty keyset index: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval runtime readiness counter repair: %w", err)
	}
	return nil
}

func ensureColumnsTxContext(ctx context.Context, tx *sql.Tx, table string, required []columnDefinition) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("load %s table info: %w", table, err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan %s table info: %w", table, err)
		}
		existing[name] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s table info: %w", table, err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s table info: %w", table, err)
	}
	for _, column := range required {
		if existing[column.Name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column.Name, column.Definition)); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, column.Name, err)
		}
	}
	return nil
}

// SemanticReadinessSnapshotAt reads every readiness fact and every dirty
// parent used by exact v3 planning through one immutable SQLite read
// transaction. No status/runtime caller may compose readiness from independent
// autocommit reads.
func (s *Store) SemanticReadinessSnapshotAt(ctx context.Context, profile embedding.Profile, exactMaxChunks int, now time.Time) (semanticreadiness.Snapshot, error) {
	profileID, err := profile.ID()
	if err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("semantic readiness profile: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("begin semantic readiness snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshot := semanticreadiness.Snapshot{Available: true, ProfileID: profileID, ExactMaxChunks: exactMaxChunks, Now: now.UTC()}
	var schemaTables int
	var malformedReadyEmbeddings int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_schema
		WHERE type='table' AND name IN (
			'retrieval_state','retrieval_parent_projections','retrieval_chunks',
			'retrieval_embeddings','retrieval_embedding_profiles','retrieval_index_generations'
		)`).Scan(&schemaTables); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("inspect semantic readiness schema: %w", err)
	}
	if schemaTables != 6 {
		return semanticreadiness.Snapshot{ProfileID: profileID, ExactMaxChunks: exactMaxChunks, Now: now.UTC()}, ErrRetrievalUnavailable
	}
	var storedProjectionParentCount, storedCurrentParents, storedEmptyParents int
	var storedPendingParents, storedBlockedParents, storedErrorParents int
	var storedDirtyParents, storedCurrentChunkCount int
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch,projection_parent_count,current_parent_count,empty_parent_count,
		pending_parent_count,blocked_parent_count,error_parent_count,dirty_parent_count,current_chunk_count
		FROM retrieval_state WHERE singleton=1`).Scan(
		&snapshot.GlobalPurgeEpoch, &storedProjectionParentCount, &storedCurrentParents, &storedEmptyParents,
		&storedPendingParents, &storedBlockedParents, &storedErrorParents, &storedDirtyParents,
		&storedCurrentChunkCount,
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic readiness purge epoch: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT source_key FROM items WHERE note_path!=''
			UNION ALL SELECT source_key FROM sources WHERE note_path!=''
		)`).Scan(&snapshot.ExpectedParents); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness parents: %w", err)
	}
	var observedProjectionParentCount, observedCurrentChunkCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(status='current' AND projected_revision>=dirty_revision),0),
			COALESCE(SUM(status='empty' AND projected_revision>=dirty_revision),0),
			COALESCE(SUM(status='pending' OR projected_revision<dirty_revision),0),
			COALESCE(SUM(status='blocked'),0), COALESCE(SUM(status='error'),0),
			COALESCE(SUM(projected_revision<dirty_revision),0),
			COALESCE(SUM(CASE WHEN status='current' AND projected_revision>=dirty_revision THEN chunk_count ELSE 0 END),0),
			COALESCE(MIN(CASE WHEN projected_revision<dirty_revision THEN dirty_at END),'')
		FROM retrieval_parent_projections`).Scan(
		&observedProjectionParentCount,
		&snapshot.CurrentParents, &snapshot.EmptyParents, &snapshot.PendingParents,
		&snapshot.BlockedParents, &snapshot.ErrorParents, &snapshot.DirtyParents,
		&observedCurrentChunkCount,
		newOptionalRFC3339Scanner(&snapshot.OldestDirtyAt),
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness projection states: %w", err)
	}
	if min(storedProjectionParentCount, storedCurrentParents, storedEmptyParents, storedPendingParents,
		storedBlockedParents, storedErrorParents, storedDirtyParents, storedCurrentChunkCount) < 0 ||
		storedProjectionParentCount != observedProjectionParentCount ||
		storedCurrentParents != snapshot.CurrentParents || storedEmptyParents != snapshot.EmptyParents ||
		storedPendingParents != snapshot.PendingParents || storedBlockedParents != snapshot.BlockedParents ||
		storedErrorParents != snapshot.ErrorParents || storedDirtyParents != snapshot.DirtyParents ||
		storedCurrentChunkCount != observedCurrentChunkCount {
		snapshot.AggregateCountersCorrupt = true
	}
	snapshot.ChunkableParents = snapshot.CurrentParents

	if max(snapshot.DirtyParents, snapshot.PendingParents) > semanticreadiness.MaxDirtyParents {
		snapshot.EstimatedNotReadyChunks = semanticreadiness.MaxNotReadyChunks + 1
	} else {
		snapshot.EstimatedNotReadyChunks, err = estimateSemanticReadinessDirtyParents(ctx, tx)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return semanticreadiness.Snapshot{}, err
		}
		snapshot.EstimatedNotReadyChunks = semanticreadiness.MaxNotReadyChunks + 1
		snapshot.PlanningError = err.Error()
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM retrieval_chunks c
		JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key
		WHERE p.status='current' AND p.projected_revision>=p.dirty_revision`).Scan(&snapshot.ChunkCount); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness chunks: %w", err)
	}
	if snapshot.ChunkCount != observedCurrentChunkCount {
		snapshot.AggregateCountersCorrupt = true
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(e.status='ready' AND e.chunk_text_hash=c.chunk_text_hash),0),
			COALESCE(SUM(e.chunk_id IS NULL OR e.chunk_text_hash!=c.chunk_text_hash OR e.status='pending'),0),
			COALESCE(SUM(e.status='blocked' AND e.chunk_text_hash=c.chunk_text_hash),0),
			COALESCE(SUM(e.status='error' AND e.chunk_text_hash=c.chunk_text_hash),0),
			COALESCE(SUM(e.status='error' AND e.chunk_text_hash=c.chunk_text_hash AND (e.next_attempt_at='' OR e.next_attempt_at<=?)),0),
			COALESCE(SUM(e.status='error' AND e.chunk_text_hash=c.chunk_text_hash AND e.next_attempt_at>?),0),
			COALESCE(SUM((e.status='error' OR e.status='blocked') AND e.chunk_text_hash=c.chunk_text_hash AND e.last_error=''),0),
			COALESCE(SUM(e.status='blocked' AND e.chunk_text_hash=c.chunk_text_hash AND e.last_error LIKE 'corrupt:%'),0),
			COALESCE(SUM(e.status='ready' AND e.chunk_text_hash=c.chunk_text_hash AND (e.revision<=0 OR e.vector_hash='' OR length(e.vector_bytes)!=e.dimensions*4)),0)
		FROM retrieval_chunks c
		JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key
		LEFT JOIN retrieval_embeddings e ON e.chunk_id=c.chunk_id AND e.profile_id=?
		WHERE p.status='current' AND p.projected_revision>=p.dirty_revision`,
		now.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339), profileID).Scan(
		&snapshot.ReadyEmbeddings, &snapshot.PendingEmbeddings, &snapshot.BlockedEmbeddings,
		&snapshot.ErrorEmbeddings, &snapshot.DueRetries, &snapshot.ScheduledRetries,
		&snapshot.UnclassifiedErrors, &snapshot.CorruptEmbeddings, &malformedReadyEmbeddings,
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness embeddings: %w", err)
	}
	embeddingDebt := snapshot.PendingEmbeddings + snapshot.ErrorEmbeddings
	if embeddingDebt > semanticreadiness.MaxNotReadyChunks-snapshot.EstimatedNotReadyChunks {
		snapshot.EstimatedNotReadyChunks = semanticreadiness.MaxNotReadyChunks + 1
	} else {
		snapshot.EstimatedNotReadyChunks += embeddingDebt
	}
	var oldestEmbeddingDebt time.Time
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(CASE
			WHEN e.status='error' AND e.chunk_text_hash=c.chunk_text_hash THEN e.updated_at
			WHEN e.chunk_id IS NULL OR e.chunk_text_hash!=c.chunk_text_hash OR e.status='pending' THEN c.updated_at
		END),'')
		FROM retrieval_chunks c
		JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key
		LEFT JOIN retrieval_embeddings e ON e.chunk_id=c.chunk_id AND e.profile_id=?
		WHERE p.status='current' AND p.projected_revision>=p.dirty_revision`, profileID).Scan(newOptionalRFC3339Scanner(&oldestEmbeddingDebt)); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("read oldest semantic embedding debt: %w", err)
	}
	if !oldestEmbeddingDebt.IsZero() && (snapshot.OldestDirtyAt.IsZero() || oldestEmbeddingDebt.Before(snapshot.OldestDirtyAt)) {
		snapshot.OldestDirtyAt = oldestEmbeddingDebt
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT c.parent_kind,c.parent_source_key
			FROM retrieval_chunks c
			JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key
			JOIN retrieval_embeddings e ON e.chunk_id=c.chunk_id AND e.profile_id=?
			WHERE p.status='current' AND p.projected_revision>=p.dirty_revision
				AND e.status='ready' AND e.chunk_text_hash=c.chunk_text_hash
			GROUP BY c.parent_kind,c.parent_source_key
		)`, profileID).Scan(&snapshot.ParentsWithReadyChunk); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness parent coverage: %w", err)
	}

	var stored embedding.Profile
	var storedReadyCount, storedPendingCount, storedBlockedCount, storedErrorCount, storedCorruptCount int
	err = tx.QueryRowContext(ctx, `
		SELECT latest_revision,purge_epoch,active_generation_id,active_snapshot_revision,
			active_indexed_count,l0_ready_count,active_tombstone_count,
			provider,model,dimensions,projection_version,chunker_version,representation,normalization,
			ready_embedding_count,pending_embedding_count,blocked_embedding_count,error_embedding_count,corrupt_embedding_count
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&snapshot.LatestRevision, &snapshot.ProfilePurgeEpoch, &snapshot.ActiveGenerationID,
		new(int64), &snapshot.ActiveIndexedCount, &snapshot.L0ReadyCount, &snapshot.ActiveTombstones,
		&stored.Provider, &stored.Model, &stored.Dimensions, &stored.ProjectionVersion,
		&stored.ChunkerVersion, &stored.Representation, &stored.Normalization,
		&storedReadyCount, &storedPendingCount, &storedBlockedCount, &storedErrorCount, &storedCorruptCount,
	)
	if err == nil {
		snapshot.ProfileExists = true
		snapshot.ProfileProvenanceValid = stored == profile
	} else if !errors.Is(err, sql.ErrNoRows) {
		return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic readiness profile: %w", err)
	}
	if snapshot.ProfileExists && snapshot.ProfileProvenanceValid {
		var mismatched int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM retrieval_chunks c
			JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key
			LEFT JOIN retrieval_embeddings e ON e.chunk_id=c.chunk_id AND e.profile_id=?
			WHERE p.status='current' AND p.projected_revision>=p.dirty_revision AND (
				c.projection_version!=? OR c.chunker_version!=? OR
				(e.chunk_id IS NOT NULL AND (e.provider!=? OR e.model!=? OR e.dimensions!=? OR e.representation!=? OR e.normalization!=?))
			)`, profileID, profile.ProjectionVersion, profile.ChunkerVersion, profile.Provider,
			profile.Model, profile.Dimensions, profile.Representation, profile.Normalization).Scan(&mismatched); err != nil {
			return semanticreadiness.Snapshot{}, fmt.Errorf("validate semantic readiness profile provenance: %w", err)
		}
		snapshot.ProfileProvenanceValid = mismatched == 0
	}
	var activeSnapshotRevision int64
	if snapshot.ProfileExists {
		var observedReadyCount, observedPendingCount, observedBlockedCount, observedErrorCount, observedCorruptCount int
		if err := tx.QueryRowContext(ctx, `SELECT active_snapshot_revision FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&activeSnapshotRevision); err != nil {
			return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic readiness profile watermark: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(revision),0),
				COALESCE(SUM(status='ready' AND revision<=0),0),
				COALESCE(SUM(status='ready' AND (?='' OR revision>?)),0),
				COALESCE(SUM(status='ready'),0),COALESCE(SUM(status='pending'),0),
				COALESCE(SUM(status='blocked'),0),COALESCE(SUM(status='error'),0),
				COALESCE(SUM(status='blocked' AND last_error LIKE 'corrupt:%'),0)
			FROM retrieval_embeddings WHERE profile_id=?`,
			snapshot.ActiveGenerationID, activeSnapshotRevision, profileID).Scan(
			&snapshot.ObservedLatestRevision, &snapshot.RevisionZeroEmbeddings, &snapshot.ObservedL0ReadyCount,
			&observedReadyCount, &observedPendingCount, &observedBlockedCount, &observedErrorCount, &observedCorruptCount,
		); err != nil {
			return semanticreadiness.Snapshot{}, fmt.Errorf("observe semantic readiness profile counters: %w", err)
		}
		if min(storedReadyCount, storedPendingCount, storedBlockedCount, storedErrorCount, storedCorruptCount) < 0 ||
			storedCorruptCount > storedBlockedCount || storedReadyCount != observedReadyCount ||
			storedPendingCount != observedPendingCount || storedBlockedCount != observedBlockedCount ||
			storedErrorCount != observedErrorCount || storedCorruptCount != observedCorruptCount {
			snapshot.AggregateCountersCorrupt = true
		}
		snapshot.CorruptEmbeddings += malformedReadyEmbeddings
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(build_status='building'),0),COALESCE(SUM(build_status='stale'),0),COALESCE(SUM(build_status='error'),0)
		FROM retrieval_index_generations WHERE profile_id=?`, profileID).Scan(
		&snapshot.BuildingGenerations, &snapshot.StaleGenerations, &snapshot.ErrorGenerations,
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness generations: %w", err)
	}
	// This foundation schema cannot persist the source revision, purge epoch,
	// membership hash, or segment manifest needed to prove an active ANN root.
	// A claimed active row therefore remains fail-closed until the segmented
	// index lifecycle lands.
	snapshot.ActiveGenerationValid = snapshot.ActiveGenerationID == ""
	if err := tx.Commit(); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("commit semantic readiness read snapshot: %w", err)
	}
	return snapshot, nil
}

func estimateSemanticReadinessDirtyParents(ctx context.Context, tx *sql.Tx) (int, error) {
	var lastRevision int64 = -1
	lastKind, lastSourceKey := "", ""
	return semanticreadiness.EstimateDirtyParentStream(ctx, semanticreadiness.MaxNotReadyChunks, func() (semanticreadiness.DirtyParent, bool, error) {
		for {
			var revision int64
			var kind, sourceKey string
			var chunkCount int
			err := tx.QueryRowContext(ctx, `
				SELECT dirty_revision,parent_kind,parent_source_key,chunk_count
				FROM retrieval_parent_projections INDEXED BY idx_retrieval_parent_projections_dirty_keyset
				WHERE projected_revision<dirty_revision AND (
					dirty_revision>? OR
					(dirty_revision=? AND parent_kind>?) OR
					(dirty_revision=? AND parent_kind=? AND parent_source_key>?)
				)
				ORDER BY dirty_revision,parent_kind,parent_source_key
				LIMIT 1`, lastRevision, lastRevision, lastKind, lastRevision, lastKind, lastSourceKey).Scan(
				&revision, &kind, &sourceKey, &chunkCount,
			)
			if errors.Is(err, sql.ErrNoRows) {
				return semanticreadiness.DirtyParent{}, false, nil
			}
			if err != nil {
				return semanticreadiness.DirtyParent{}, false, err
			}
			lastRevision, lastKind, lastSourceKey = revision, kind, sourceKey
			parent, exists, eligible, err := loadCurrentRetrievalParent(ctx, tx, kind, sourceKey)
			if err != nil {
				return semanticreadiness.DirtyParent{}, false, err
			}
			if !exists || !eligible {
				continue
			}
			return semanticreadiness.DirtyParent{Parent: parent, LastCurrentChunkCount: chunkCount}, true, nil
		}
	})
}

type optionalRFC3339Scanner struct{ target *time.Time }

func newOptionalRFC3339Scanner(target *time.Time) *optionalRFC3339Scanner {
	return &optionalRFC3339Scanner{target: target}
}

func (s *optionalRFC3339Scanner) Scan(src any) error {
	value, _ := src.(string)
	value = strings.TrimSpace(value)
	if value == "" {
		*s.target = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return err
	}
	*s.target = parsed
	return nil
}
