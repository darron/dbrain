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

// SemanticRuntimeReadinessSnapshotAt acquires admission facts without the
// maintenance/status path's repeated full-corpus joins. Projection facts are
// ledger aggregates, embedding state comes from transactionally maintained
// profile counters, and exact row validation is entered only when both the
// current chunk set and the whole profile fit the immutable exact-search cap.
// Every read and bounded validation belongs to one read transaction.
func (s *Store) SemanticRuntimeReadinessSnapshotAt(ctx context.Context, profile embedding.Profile, exactMaxChunks int, now time.Time) (semanticreadiness.Snapshot, error) {
	profileID, err := profile.ID()
	if err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("semantic runtime readiness profile: %w", err)
	}
	exactMaxChunks = semanticreadiness.EffectiveExactMaxChunks(exactMaxChunks)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("begin semantic runtime readiness snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshot := semanticreadiness.Snapshot{Available: true, ProfileID: profileID, ExactMaxChunks: exactMaxChunks, Now: now.UTC()}
	var schemaTables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN (
		'retrieval_state','retrieval_parent_projections','retrieval_chunks','retrieval_embeddings',
		'retrieval_embedding_profiles','retrieval_index_generations')`).Scan(&schemaTables); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("inspect semantic runtime readiness schema: %w", err)
	}
	if schemaTables != 6 {
		return semanticreadiness.Snapshot{ProfileID: profileID, ExactMaxChunks: exactMaxChunks, Now: now.UTC()}, ErrRetrievalUnavailable
	}
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch,projection_parent_count,current_parent_count,empty_parent_count,
		pending_parent_count,blocked_parent_count,error_parent_count,dirty_parent_count,current_chunk_count
		FROM retrieval_state WHERE singleton=1`).Scan(
		&snapshot.GlobalPurgeEpoch, &snapshot.ExpectedParents, &snapshot.CurrentParents, &snapshot.EmptyParents,
		&snapshot.PendingParents, &snapshot.BlockedParents, &snapshot.ErrorParents, &snapshot.DirtyParents,
		&snapshot.ChunkCount,
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic runtime purge epoch: %w", err)
	}
	if !semanticRuntimeProjectionCountersPlausible(snapshot) {
		snapshot.AggregateCountersCorrupt = true
	}
	oldestDirtyExists := true
	if err := tx.QueryRowContext(ctx, `SELECT dirty_at FROM retrieval_parent_projections INDEXED BY idx_retrieval_parent_projections_dirty_age WHERE projected_revision<dirty_revision ORDER BY dirty_at LIMIT 1`).Scan(newOptionalRFC3339Scanner(&snapshot.OldestDirtyAt)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			oldestDirtyExists = false
		} else {
			return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic runtime oldest projection debt: %w", err)
		}
	}
	dirtyIdentities, dirtyOverflow, err := observeSemanticRuntimeDirtyIdentities(ctx, tx)
	if err != nil {
		return semanticreadiness.Snapshot{}, err
	}
	if oldestDirtyExists != (len(dirtyIdentities) > 0) ||
		(dirtyOverflow && snapshot.DirtyParents <= semanticreadiness.MaxDirtyParents) ||
		(!dirtyOverflow && snapshot.DirtyParents != len(dirtyIdentities)) {
		snapshot.AggregateCountersCorrupt = true
	}
	snapshot.ChunkableParents = snapshot.CurrentParents
	if snapshot.AggregateCountersCorrupt || dirtyOverflow || snapshot.PendingParents > semanticreadiness.MaxDirtyParents {
		snapshot.EstimatedNotReadyChunks = semanticreadiness.MaxNotReadyChunks + 1
	} else {
		snapshot.EstimatedNotReadyChunks, err = estimateSemanticRuntimeDirtyIdentities(ctx, tx, dirtyIdentities)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return semanticreadiness.Snapshot{}, err
			}
			snapshot.EstimatedNotReadyChunks = semanticreadiness.MaxNotReadyChunks + 1
			snapshot.PlanningError = err.Error()
		}
	}

	var stored embedding.Profile
	var activeSnapshotRevision int64
	var readyCount, pendingCount, blockedCount, errorCount, corruptCount int
	err = tx.QueryRowContext(ctx, `
		SELECT latest_revision,purge_epoch,active_generation_id,active_snapshot_revision,
			active_indexed_count,l0_ready_count,active_tombstone_count,
			provider,model,dimensions,projection_version,chunker_version,representation,normalization,
			ready_embedding_count,pending_embedding_count,blocked_embedding_count,error_embedding_count,corrupt_embedding_count
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&snapshot.LatestRevision, &snapshot.ProfilePurgeEpoch, &snapshot.ActiveGenerationID, &activeSnapshotRevision,
		&snapshot.ActiveIndexedCount, &snapshot.L0ReadyCount, &snapshot.ActiveTombstones,
		&stored.Provider, &stored.Model, &stored.Dimensions, &stored.ProjectionVersion,
		&stored.ChunkerVersion, &stored.Representation, &stored.Normalization,
		&readyCount, &pendingCount, &blockedCount, &errorCount, &corruptCount,
	)
	if err == nil {
		snapshot.ProfileExists = true
		snapshot.ProfileProvenanceValid = stored == profile
	} else if !errors.Is(err, sql.ErrNoRows) {
		return semanticreadiness.Snapshot{}, fmt.Errorf("read semantic runtime profile: %w", err)
	}
	if min(readyCount, pendingCount, blockedCount, errorCount, corruptCount) < 0 || corruptCount > blockedCount {
		snapshot.AggregateCountersCorrupt = true
	}

	if snapshot.ProfileExists {
		totalProfileRows := readyCount + pendingCount + blockedCount + errorCount
		if snapshot.BlockedParents == 0 && snapshot.ErrorParents == 0 && snapshot.ChunkCount <= exactMaxChunks && totalProfileRows <= exactMaxChunks {
			if err := validateExactSmallRuntimeProfile(ctx, tx, profileID, profile, activeSnapshotRevision, readyCount, pendingCount, blockedCount, errorCount, corruptCount, &snapshot); err != nil {
				return semanticreadiness.Snapshot{}, err
			}
		} else {
			snapshot.ReadyEmbeddings = readyCount
			snapshot.PendingEmbeddings = pendingCount + max(0, snapshot.ChunkCount-totalProfileRows)
			snapshot.BlockedEmbeddings = blockedCount
			snapshot.ErrorEmbeddings = errorCount
			snapshot.CorruptEmbeddings = corruptCount
			snapshot.ObservedLatestRevision = snapshot.LatestRevision
			snapshot.ObservedL0ReadyCount = snapshot.L0ReadyCount
			if projectionCompleteForRuntime(snapshot) && snapshot.PendingEmbeddings == 0 && blockedCount == 0 && errorCount == 0 && readyCount == snapshot.ChunkCount {
				snapshot.ParentsWithReadyChunk = snapshot.ChunkableParents
			}
		}
	} else {
		snapshot.PendingEmbeddings = snapshot.ChunkCount
	}
	embeddingDebt := snapshot.PendingEmbeddings + snapshot.ErrorEmbeddings
	if embeddingDebt > semanticreadiness.MaxNotReadyChunks-snapshot.EstimatedNotReadyChunks {
		snapshot.EstimatedNotReadyChunks = semanticreadiness.MaxNotReadyChunks + 1
	} else {
		snapshot.EstimatedNotReadyChunks += embeddingDebt
	}
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM retrieval_index_generations INDEXED BY idx_retrieval_generations_profile_status WHERE profile_id=? AND build_status='building' LIMIT 1),
		EXISTS(SELECT 1 FROM retrieval_index_generations INDEXED BY idx_retrieval_generations_profile_status WHERE profile_id=? AND build_status='stale' LIMIT 1),
		EXISTS(SELECT 1 FROM retrieval_index_generations INDEXED BY idx_retrieval_generations_profile_status WHERE profile_id=? AND build_status='error' LIMIT 1)`, profileID, profileID, profileID).Scan(
		&snapshot.BuildingGenerations, &snapshot.StaleGenerations, &snapshot.ErrorGenerations,
	); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("inspect semantic runtime generation presence: %w", err)
	}
	snapshot.ActiveGenerationValid = snapshot.ActiveGenerationID == ""
	if err := tx.Commit(); err != nil {
		return semanticreadiness.Snapshot{}, fmt.Errorf("commit semantic runtime readiness snapshot: %w", err)
	}
	return snapshot, nil
}

type semanticRuntimeDirtyIdentity struct {
	revision  int64
	kind      string
	sourceKey string
}

func semanticRuntimeProjectionCountersPlausible(snapshot semanticreadiness.Snapshot) bool {
	if min(snapshot.ExpectedParents, snapshot.CurrentParents, snapshot.EmptyParents, snapshot.PendingParents,
		snapshot.BlockedParents, snapshot.ErrorParents, snapshot.DirtyParents, snapshot.ChunkCount) < 0 ||
		snapshot.PendingParents < snapshot.DirtyParents || snapshot.PendingParents > snapshot.ExpectedParents ||
		snapshot.DirtyParents > snapshot.ExpectedParents ||
		(snapshot.CurrentParents == 0 && snapshot.ChunkCount != 0) || snapshot.ChunkCount < snapshot.CurrentParents {
		return false
	}
	// Clean current/empty and all blocked/error rows are disjoint categories.
	// Subtracting them from the ledger total avoids trusting overflow-prone
	// aggregate arithmetic when the durable counters themselves are corrupt.
	unclassified := snapshot.ExpectedParents
	for _, classified := range []int{snapshot.CurrentParents, snapshot.EmptyParents, snapshot.BlockedParents, snapshot.ErrorParents} {
		if classified > unclassified {
			return false
		}
		unclassified -= classified
	}
	// pending_parent_count covers every otherwise-unclassified row. It may also
	// overlap a blocked/error category, but only when that same identity is dirty.
	return snapshot.PendingParents >= unclassified && snapshot.PendingParents-unclassified <= snapshot.DirtyParents
}

// observeSemanticRuntimeDirtyIdentities proves the runtime identity budget
// independently of the projection counters. The 501st row is a stable
// over-budget sentinel; no caller plans or loads parent content after seeing it.
func observeSemanticRuntimeDirtyIdentities(ctx context.Context, tx *sql.Tx) ([]semanticRuntimeDirtyIdentity, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT dirty_revision,parent_kind,parent_source_key
		FROM retrieval_parent_projections INDEXED BY idx_retrieval_parent_projections_dirty_keyset
		WHERE projected_revision<dirty_revision
		ORDER BY dirty_revision,parent_kind,parent_source_key
		LIMIT ?`, semanticreadiness.MaxDirtyParents+1)
	if err != nil {
		return nil, false, fmt.Errorf("observe semantic runtime dirty identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	identities := make([]semanticRuntimeDirtyIdentity, 0, semanticreadiness.MaxDirtyParents+1)
	for rows.Next() {
		var identity semanticRuntimeDirtyIdentity
		if err := rows.Scan(&identity.revision, &identity.kind, &identity.sourceKey); err != nil {
			return nil, false, fmt.Errorf("scan semantic runtime dirty identity: %w", err)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate semantic runtime dirty identities: %w", err)
	}
	if len(identities) > semanticreadiness.MaxDirtyParents {
		return identities[:semanticreadiness.MaxDirtyParents], true, nil
	}
	return identities, false, nil
}

func estimateSemanticRuntimeDirtyIdentities(ctx context.Context, tx *sql.Tx, identities []semanticRuntimeDirtyIdentity) (int, error) {
	next := 0
	return semanticreadiness.EstimateDirtyParentStream(ctx, semanticreadiness.MaxNotReadyChunks, func() (semanticreadiness.DirtyParent, bool, error) {
		for next < len(identities) {
			identity := identities[next]
			next++
			var chunkCount int
			if err := tx.QueryRowContext(ctx, `SELECT chunk_count FROM retrieval_parent_projections WHERE parent_kind=? AND parent_source_key=? AND projected_revision<dirty_revision`, identity.kind, identity.sourceKey).Scan(&chunkCount); err != nil {
				return semanticreadiness.DirtyParent{}, false, fmt.Errorf("load semantic runtime dirty identity %s %s: %w", identity.kind, identity.sourceKey, err)
			}
			parent, exists, eligible, err := loadCurrentRetrievalParent(ctx, tx, identity.kind, identity.sourceKey)
			if err != nil {
				return semanticreadiness.DirtyParent{}, false, err
			}
			if !exists || !eligible {
				continue
			}
			return semanticreadiness.DirtyParent{Parent: parent, LastCurrentChunkCount: chunkCount}, true, nil
		}
		return semanticreadiness.DirtyParent{}, false, nil
	})
}

func validateExactSmallRuntimeProfile(ctx context.Context, tx *sql.Tx, profileID string, profile embedding.Profile, activeSnapshotRevision int64, counterReady, counterPending, counterBlocked, counterError, counterCorrupt int, snapshot *semanticreadiness.Snapshot) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.provider,e.model,e.dimensions,e.representation,e.normalization,e.vector_bytes,e.vector_hash,
			e.chunk_text_hash,e.status,e.revision,e.last_error,e.next_attempt_at,e.updated_at,c.chunk_text_hash,
			c.projection_version,c.chunker_version
		FROM retrieval_embeddings e INDEXED BY idx_retrieval_embeddings_profile_status
		JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		WHERE e.profile_id=? LIMIT ?`, profileID, snapshot.ExactMaxChunks+1)
	if err != nil {
		return fmt.Errorf("validate exact-small semantic runtime profile: %w", err)
	}
	defer func(openRows *sql.Rows) { _ = openRows.Close() }(rows)
	actualReady, actualPending, actualBlocked, actualError, actualCorrupt := 0, 0, 0, 0, 0
	for rows.Next() {
		var row RetrievalEmbeddingRow
		var nextAttemptAt, updatedAt, currentHash string
		if err := rows.Scan(&row.Provider, &row.Model, &row.Dimensions, &row.Representation, &row.Normalization,
			&row.VectorBytes, &row.VectorHash, &row.ChunkTextHash, &row.Status, &row.Revision, &row.LastError,
			&nextAttemptAt, &updatedAt, &currentHash, &row.ProjectionVersion, &row.ChunkerVersion); err != nil {
			return fmt.Errorf("scan exact-small semantic runtime profile: %w", err)
		}
		if row.Provider != profile.Provider || row.Model != profile.Model || row.Dimensions != profile.Dimensions ||
			row.Representation != profile.Representation || row.Normalization != profile.Normalization ||
			row.ProjectionVersion != profile.ProjectionVersion || row.ChunkerVersion != profile.ChunkerVersion {
			snapshot.ProfileProvenanceValid = false
		}
		if row.Revision > snapshot.ObservedLatestRevision {
			snapshot.ObservedLatestRevision = row.Revision
		}
		switch row.Status {
		case RetrievalEmbeddingReady:
			actualReady++
			if row.Revision <= 0 {
				snapshot.RevisionZeroEmbeddings++
			}
			if snapshot.ActiveGenerationID == "" || row.Revision > activeSnapshotRevision {
				snapshot.ObservedL0ReadyCount++
			}
			if reason := retrievalEmbeddingCorruptionReason(row, currentHash); reason != "" {
				snapshot.CorruptEmbeddings++
			}
		case RetrievalEmbeddingPending:
			actualPending++
		case RetrievalEmbeddingBlocked:
			actualBlocked++
			if strings.HasPrefix(row.LastError, "corrupt:") {
				actualCorrupt++
			}
		case RetrievalEmbeddingError:
			actualError++
			if strings.TrimSpace(row.LastError) == "" {
				snapshot.UnclassifiedErrors++
			}
			when := parseStoredTime(nextAttemptAt)
			if when.IsZero() || !when.After(snapshot.Now) {
				snapshot.DueRetries++
			} else {
				snapshot.ScheduledRetries++
			}
		}
		updated := parseStoredTime(updatedAt)
		if row.Status == RetrievalEmbeddingPending || row.Status == RetrievalEmbeddingError {
			if !updated.IsZero() && (snapshot.OldestDirtyAt.IsZero() || updated.Before(snapshot.OldestDirtyAt)) {
				snapshot.OldestDirtyAt = updated
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate exact-small semantic runtime profile: %w", err)
	}
	if actualReady != counterReady || actualPending != counterPending || actualBlocked != counterBlocked || actualError != counterError || actualCorrupt != counterCorrupt {
		snapshot.CorruptEmbeddings++
	}
	snapshot.CorruptEmbeddings += actualCorrupt
	if snapshot.ObservedLatestRevision == 0 && counterReady+counterPending+counterBlocked+counterError == 0 {
		snapshot.ObservedLatestRevision = snapshot.LatestRevision
	}
	if snapshot.ActiveGenerationID == "" && actualReady == 0 {
		snapshot.ObservedL0ReadyCount = snapshot.L0ReadyCount
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT c.parent_kind,c.parent_source_key,c.updated_at,e.status,e.chunk_text_hash,e.last_error,e.next_attempt_at,e.updated_at
		FROM retrieval_chunks c INDEXED BY idx_retrieval_chunks_parent
		JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key
		LEFT JOIN retrieval_embeddings e ON e.chunk_id=c.chunk_id AND e.profile_id=?
		WHERE p.status='current' AND p.projected_revision>=p.dirty_revision
		LIMIT ?`, profileID, snapshot.ExactMaxChunks+1)
	if err != nil {
		return fmt.Errorf("validate exact-small current semantic coverage: %w", err)
	}
	defer func(openRows *sql.Rows) { _ = openRows.Close() }(rows)
	parentsReady := make(map[string]struct{})
	currentRows := 0
	snapshot.ReadyEmbeddings, snapshot.PendingEmbeddings = 0, 0
	snapshot.BlockedEmbeddings, snapshot.ErrorEmbeddings = 0, 0
	for rows.Next() {
		var parentKind, parentSourceKey, chunkUpdated string
		var status, embeddedHash, lastError, nextAttempt, embeddingUpdated sql.NullString
		if err := rows.Scan(&parentKind, &parentSourceKey, &chunkUpdated, &status, &embeddedHash, &lastError, &nextAttempt, &embeddingUpdated); err != nil {
			return fmt.Errorf("scan exact-small current semantic coverage: %w", err)
		}
		currentRows++
		switch RetrievalEmbeddingStatus(status.String) {
		case RetrievalEmbeddingReady:
			snapshot.ReadyEmbeddings++
			parentsReady[parentKind+"\x00"+parentSourceKey] = struct{}{}
		case RetrievalEmbeddingBlocked:
			snapshot.BlockedEmbeddings++
		case RetrievalEmbeddingError:
			snapshot.ErrorEmbeddings++
		default:
			snapshot.PendingEmbeddings++
		}
		debtAt := parseStoredTime(chunkUpdated)
		if embeddingUpdated.Valid && embeddingUpdated.String != "" {
			debtAt = parseStoredTime(embeddingUpdated.String)
		}
		if RetrievalEmbeddingStatus(status.String) != RetrievalEmbeddingReady && !debtAt.IsZero() && (snapshot.OldestDirtyAt.IsZero() || debtAt.Before(snapshot.OldestDirtyAt)) {
			snapshot.OldestDirtyAt = debtAt
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate exact-small current semantic coverage: %w", err)
	}
	if currentRows != snapshot.ChunkCount {
		snapshot.CorruptEmbeddings++
	}
	snapshot.ParentsWithReadyChunk = len(parentsReady)
	return nil
}

func projectionCompleteForRuntime(s semanticreadiness.Snapshot) bool {
	return s.ExpectedParents == s.CurrentParents+s.EmptyParents && s.PendingParents == 0 && s.DirtyParents == 0 && s.BlockedParents == 0 && s.ErrorParents == 0
}
