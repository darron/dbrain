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
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&snapshot.GlobalPurgeEpoch); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic readiness purge epoch: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT source_key FROM items WHERE note_path!=''
			UNION ALL SELECT source_key FROM sources WHERE note_path!=''
		)`).Scan(&snapshot.ExpectedParents); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness parents: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(status='current' AND projected_revision>=dirty_revision),0),
			COALESCE(SUM(status='empty' AND projected_revision>=dirty_revision),0),
			COALESCE(SUM(status='pending' OR projected_revision<dirty_revision),0),
			COALESCE(SUM(status='blocked'),0), COALESCE(SUM(status='error'),0),
			COALESCE(SUM(projected_revision<dirty_revision),0),
			COALESCE(MIN(CASE WHEN projected_revision<dirty_revision THEN dirty_at END),'')
		FROM retrieval_parent_projections`).Scan(
		&snapshot.CurrentParents, &snapshot.EmptyParents, &snapshot.PendingParents,
		&snapshot.BlockedParents, &snapshot.ErrorParents, &snapshot.DirtyParents,
		newOptionalRFC3339Scanner(&snapshot.OldestDirtyAt),
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("count semantic readiness projection states: %w", err)
	}
	snapshot.ChunkableParents = snapshot.CurrentParents

	snapshot.EstimatedNotReadyChunks, err = estimateSemanticReadinessDirtyParents(ctx, tx)
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
		&snapshot.UnclassifiedErrors, &snapshot.CorruptEmbeddings, &snapshot.RevisionZeroEmbeddings,
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
	err = tx.QueryRowContext(ctx, `
		SELECT latest_revision,purge_epoch,active_generation_id,active_snapshot_revision,
			active_indexed_count,l0_ready_count,active_tombstone_count,
			provider,model,dimensions,projection_version,chunker_version,representation,normalization
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&snapshot.LatestRevision, &snapshot.ProfilePurgeEpoch, &snapshot.ActiveGenerationID,
		new(int64), &snapshot.ActiveIndexedCount, &snapshot.L0ReadyCount, &snapshot.ActiveTombstones,
		&stored.Provider, &stored.Model, &stored.Dimensions, &stored.ProjectionVersion,
		&stored.ChunkerVersion, &stored.Representation, &stored.Normalization,
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
		if err := tx.QueryRowContext(ctx, `SELECT active_snapshot_revision FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&activeSnapshotRevision); err != nil {
			return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic readiness profile watermark: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(revision),0),
				COALESCE(SUM(status='ready' AND revision<=0),0),
				COALESCE(SUM(status='ready' AND (?='' OR revision>?)),0)
			FROM retrieval_embeddings WHERE profile_id=?`,
			snapshot.ActiveGenerationID, activeSnapshotRevision, profileID).Scan(
			&snapshot.ObservedLatestRevision, &snapshot.RevisionZeroEmbeddings, &snapshot.ObservedL0ReadyCount,
		); err != nil {
			return semanticreadiness.Snapshot{}, fmt.Errorf("observe semantic readiness profile counters: %w", err)
		}
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
	rows, err := tx.QueryContext(ctx, `
		SELECT parent_kind,parent_source_key,chunk_count
		FROM retrieval_parent_projections
		WHERE projected_revision<dirty_revision
		ORDER BY dirty_revision,parent_kind,parent_source_key`)
	if err != nil {
		return 0, fmt.Errorf("list semantic readiness dirty parents: %w", err)
	}
	type identity struct {
		kind, sourceKey string
		chunkCount      int
	}
	identities := make([]identity, 0)
	for rows.Next() {
		var value identity
		if err := rows.Scan(&value.kind, &value.sourceKey, &value.chunkCount); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan semantic readiness dirty parent: %w", err)
		}
		identities = append(identities, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate semantic readiness dirty parents: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close semantic readiness dirty parents: %w", err)
	}
	next := 0
	return semanticreadiness.EstimateDirtyParentStream(ctx, semanticreadiness.MaxNotReadyChunks, func() (semanticreadiness.DirtyParent, bool, error) {
		for next < len(identities) {
			identity := identities[next]
			next++
			parent, exists, eligible, err := loadCurrentRetrievalParent(ctx, tx, identity.kind, identity.sourceKey)
			if err != nil {
				return semanticreadiness.DirtyParent{}, false, err
			}
			if !exists || !eligible {
				continue
			}
			return semanticreadiness.DirtyParent{Parent: parent, LastCurrentChunkCount: identity.chunkCount}, true, nil
		}
		return semanticreadiness.DirtyParent{}, false, nil
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
