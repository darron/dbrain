package store

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/semanticreadiness"
)

func TestSemanticReadinessSnapshotUsesExactDirtyPlanningAndObservedCounters(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	profile := readinessTestProfile()

	seedRetrievalSource(t, st, "source:readiness")
	pending, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.Available || pending.ExpectedParents != 1 || pending.PendingParents != 1 || pending.DirtyParents != 1 || pending.EstimatedNotReadyChunks <= 0 || pending.ProfileExists {
		t.Fatalf("pending snapshot=%+v", pending)
	}

	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]RetrievalEmbeddingRow, 0, len(projection.Chunks))
	for _, chunk := range projection.Chunks {
		row := testEmbedding(chunk.ID, profileID, chunk.TextHash)
		row.Provider, row.Model = profile.Provider, profile.Model
		rows = append(rows, row)
	}
	epoch, err := st.RetrievalPurgeEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: epoch}); err != nil {
		t.Fatal(err)
	}

	ready, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if ready.ExpectedParents != 1 || ready.CurrentParents != 1 || ready.DirtyParents != 0 || ready.ChunkCount != len(projection.Chunks) || ready.ReadyEmbeddings != len(projection.Chunks) || ready.ParentsWithReadyChunk != 1 || !ready.ProfileExists || !ready.ProfileProvenanceValid {
		t.Fatalf("ready snapshot=%+v", ready)
	}
	if ready.LatestRevision <= 0 || ready.LatestRevision != ready.ObservedLatestRevision || ready.L0ReadyCount != ready.ObservedL0ReadyCount || ready.RevisionZeroEmbeddings != 0 {
		t.Fatalf("counter snapshot=%+v", ready)
	}
	ready.Configured, ready.Enabled = true, true
	if decision := semanticreadiness.Evaluate(ready); decision.State != semanticreadiness.StateReady || !decision.Searchable {
		t.Fatalf("decision=%+v snapshot=%+v", decision, ready)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embedding_profiles SET l0_ready_count=0 WHERE profile_id=?`, profileID); err != nil {
		t.Fatal(err)
	}
	drifted, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil || drifted.L0ReadyCount == drifted.ObservedL0ReadyCount || semanticreadiness.Evaluate(withReadinessMode(drifted)).State != semanticreadiness.StateCorrupt {
		t.Fatalf("drifted snapshot=%+v err=%v", drifted, err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embedding_profiles SET l0_ready_count=? WHERE profile_id=?`, ready.L0ReadyCount, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_chunks SET projection_version='wrong' WHERE chunk_id=?`, projection.Chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	mismatched, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil || mismatched.ProfileProvenanceValid || semanticreadiness.Evaluate(withReadinessMode(mismatched)).State != semanticreadiness.StateCorrupt {
		t.Fatalf("mismatched snapshot=%+v err=%v", mismatched, err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_chunks SET projection_version=? WHERE chunk_id=?`, profile.ProjectionVersion, projection.Chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET revision=0 WHERE profile_id=?`, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embedding_profiles SET latest_revision=0 WHERE profile_id=?`, profileID); err != nil {
		t.Fatal(err)
	}
	zeroRevision, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil || zeroRevision.RevisionZeroEmbeddings == 0 || semanticreadiness.Evaluate(withReadinessMode(zeroRevision)).State != semanticreadiness.StateCorrupt {
		t.Fatalf("zero revision snapshot=%+v err=%v", zeroRevision, err)
	}
}

func TestSemanticReadinessSnapshotSeparatesDueAndScheduledRetries(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	profile := readinessTestProfile()
	profileID, _ := profile.ID()
	seedRetrievalSource(t, st, "source:retry")
	work, _ := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	projection, _ := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	epoch, _ := st.RetrievalPurgeEpoch(ctx)
	row := testEmbedding(projection.Chunks[0].ID, profileID, projection.Chunks[0].TextHash)
	row.Provider, row.Model, row.Status, row.LastError, row.NextAttemptAt = profile.Provider, profile.Model, RetrievalEmbeddingError, "retryable: down", now.Add(time.Hour)
	row.VectorBytes = nil
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: []RetrievalEmbeddingRow{row}, ExpectedPurgeEpoch: epoch}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET updated_at=? WHERE profile_id=?`, now.Add(-30*time.Minute).Format(time.RFC3339), profileID); err != nil {
		t.Fatal(err)
	}
	scheduled, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil || scheduled.ScheduledRetries != 1 || scheduled.DueRetries != 0 || scheduled.EstimatedNotReadyChunks != 1 || !scheduled.OldestDirtyAt.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("scheduled=%+v err=%v", scheduled, err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET next_attempt_at=? WHERE profile_id=?`, now.Add(-time.Second).Format(time.RFC3339), profileID); err != nil {
		t.Fatal(err)
	}
	due, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
	if err != nil || due.DueRetries != 1 || due.ScheduledRetries != 0 || due.EstimatedNotReadyChunks != 1 {
		t.Fatalf("due=%+v err=%v", due, err)
	}
}

func TestSemanticReadinessSnapshotStopsDirtyIdentityScanAfterSingleParentSentinel(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	profile := readinessTestProfile()
	seedRetrievalSource(t, st, "source:sentinel-first")
	if _, err := st.db.Exec(`UPDATE sources SET extracted_text=? WHERE source_key=?`, strings.Repeat("x ", 3_000_000), "source:sentinel-first"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_parent_projections (
			parent_kind,parent_source_key,status,chunk_count,dirty_at,dirty_revision,projected_revision,updated_at
		) VALUES ('source','source:tail-must-not-scan','pending',X'FF','2026-07-21T12:00:00Z',999999,0,'2026-07-21T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	snapshot, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("snapshot scanned identity after sentinel: %v", err)
	}
	if snapshot.EstimatedNotReadyChunks != semanticreadiness.MaxNotReadyChunks+1 {
		t.Fatalf("estimated_not_ready_chunks=%d", snapshot.EstimatedNotReadyChunks)
	}
}

func TestSemanticReadinessSnapshotUsesParentCountGateBeforeLoadingDirtyEvidence(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	if _, err := st.db.Exec(`
		WITH RECURSIVE n(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM n WHERE value<501)
		INSERT INTO retrieval_parent_projections (
			parent_kind,parent_source_key,status,chunk_count,dirty_at,dirty_revision,projected_revision,updated_at
		)
		SELECT 'invalid',printf('tail:%04d',value),'pending',1,'2026-07-21T12:00:00Z',value,0,'2026-07-21T12:00:00Z' FROM n`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.SemanticReadinessSnapshotAt(context.Background(), readinessTestProfile(), 25_000, time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PlanningError != "" || snapshot.EstimatedNotReadyChunks != semanticreadiness.MaxNotReadyChunks+1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSemanticReadinessProfileCountersTrackTransitionsIdempotenceAndCascade(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	profile := readinessTestProfile()
	profileID, _ := profile.ID()
	seedRetrievalSource(t, st, "source:profile-counters")
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	epoch, _ := st.RetrievalPurgeEpoch(ctx)
	ready := testEmbedding(projection.Chunks[0].ID, profileID, projection.Chunks[0].TextHash)
	ready.Provider, ready.Model = profile.Provider, profile.Model
	put := func(row RetrievalEmbeddingRow) {
		t.Helper()
		if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: []RetrievalEmbeddingRow{row}, ExpectedPurgeEpoch: epoch}); err != nil {
			t.Fatal(err)
		}
	}
	assertCounters := func(ready, pending, blocked, failed, corrupt int) {
		t.Helper()
		var gotReady, gotPending, gotBlocked, gotFailed, gotCorrupt int
		if err := st.db.QueryRow(`SELECT ready_embedding_count,pending_embedding_count,blocked_embedding_count,error_embedding_count,corrupt_embedding_count FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&gotReady, &gotPending, &gotBlocked, &gotFailed, &gotCorrupt); err != nil {
			t.Fatal(err)
		}
		if gotReady != ready || gotPending != pending || gotBlocked != blocked || gotFailed != failed || gotCorrupt != corrupt {
			t.Fatalf("counters=(%d,%d,%d,%d,%d) want=(%d,%d,%d,%d,%d)", gotReady, gotPending, gotBlocked, gotFailed, gotCorrupt, ready, pending, blocked, failed, corrupt)
		}
	}
	put(ready)
	assertCounters(1, 0, 0, 0, 0)
	put(ready)
	assertCounters(1, 0, 0, 0, 0)
	pending := ready
	pending.Status, pending.VectorBytes, pending.LastError = RetrievalEmbeddingPending, nil, ""
	put(pending)
	assertCounters(0, 1, 0, 0, 0)
	blocked := pending
	blocked.Status, blocked.LastError = RetrievalEmbeddingBlocked, "corrupt: test"
	put(blocked)
	assertCounters(0, 0, 1, 0, 1)
	corruptSnapshot, err := st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, 25_000, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	corruptSnapshot.Configured, corruptSnapshot.Enabled = true, true
	if corruptSnapshot.CorruptEmbeddings == 0 || semanticreadiness.Evaluate(corruptSnapshot).State != semanticreadiness.StateCorrupt {
		t.Fatalf("blocked corruption snapshot=%+v", corruptSnapshot)
	}
	if _, err := st.db.Exec(`DELETE FROM retrieval_chunks WHERE chunk_id=?`, projection.Chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	assertCounters(0, 0, 0, 0, 0)
}

func TestSemanticRuntimeAdmissionRejectsExactSmallCorruptionBeforeProviderConstruction(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store, string, string)
	}{
		{"short bytes", func(st *Store, chunkID, profileID string) {
			_, _ = st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes=X'00000000',vector_hash=? WHERE chunk_id=? AND profile_id=?`, retrievalVectorHash([]byte{0, 0, 0, 0}), chunkID, profileID)
		}},
		{"empty hash", func(st *Store, chunkID, profileID string) {
			_, _ = st.db.Exec(`UPDATE retrieval_embeddings SET vector_hash='' WHERE chunk_id=? AND profile_id=?`, chunkID, profileID)
		}},
		{"wrong hash", func(st *Store, chunkID, profileID string) {
			_, _ = st.db.Exec(`UPDATE retrieval_embeddings SET vector_hash='wrong' WHERE chunk_id=? AND profile_id=?`, chunkID, profileID)
		}},
		{"nan", func(st *Store, chunkID, profileID string) {
			value := embedding.EncodeDenseF32([]float32{float32(math.NaN()), 1})
			_, _ = st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes=?,vector_hash=? WHERE chunk_id=? AND profile_id=?`, value, retrievalVectorHash(value), chunkID, profileID)
		}},
		{"zero", func(st *Store, chunkID, profileID string) {
			value := embedding.EncodeDenseF32([]float32{0, 0})
			_, _ = st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes=?,vector_hash=? WHERE chunk_id=? AND profile_id=?`, value, retrievalVectorHash(value), chunkID, profileID)
		}},
		{"not l2", func(st *Store, chunkID, profileID string) {
			value := embedding.EncodeDenseF32([]float32{1, 1})
			_, _ = st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes=?,vector_hash=? WHERE chunk_id=? AND profile_id=?`, value, retrievalVectorHash(value), chunkID, profileID)
		}},
		{"profile counter drift", func(st *Store, _, profileID string) {
			_, _ = st.db.Exec(`UPDATE retrieval_embedding_profiles SET ready_embedding_count=0 WHERE profile_id=?`, profileID)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
			defer func() { _ = st.Close() }()
			ctx := context.Background()
			profile := readinessTestProfile()
			profileID, _ := profile.ID()
			seedRetrievalSource(t, st, "source:runtime-corrupt")
			work, _ := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
			projection, _ := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
			if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
				t.Fatal(err)
			}
			epoch, _ := st.RetrievalPurgeEpoch(ctx)
			row := testEmbedding(projection.Chunks[0].ID, profileID, projection.Chunks[0].TextHash)
			row.Provider, row.Model = profile.Provider, profile.Model
			if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: []RetrievalEmbeddingRow{row}, ExpectedPurgeEpoch: epoch}); err != nil {
				t.Fatal(err)
			}
			tc.mutate(st, row.ChunkID, profileID)
			snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, 25_000, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Configured, snapshot.Enabled = true, true
			if snapshot.CorruptEmbeddings == 0 || semanticreadiness.Evaluate(snapshot).State != semanticreadiness.StateCorrupt {
				t.Fatalf("snapshot=%+v decision=%+v", snapshot, semanticreadiness.Evaluate(snapshot))
			}
		})
	}
}

func TestSemanticRuntimeAdmissionAllowsHistoricalLatestRevisionAfterNewestRowDeleted(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	profile := readinessTestProfile()
	profileID, _ := profile.ID()
	seedRetrievalSource(t, st, "source:delete-newest")
	if _, err := st.db.Exec(`UPDATE sources SET extracted_text=? WHERE source_key=?`, strings.Repeat("evidence ", 2_000), "source:delete-newest"); err != nil {
		t.Fatal(err)
	}
	work, _ := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil || len(projection.Chunks) < 2 {
		t.Fatalf("chunks=%d err=%v", len(projection.Chunks), err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	epoch, _ := st.RetrievalPurgeEpoch(ctx)
	makeRow := func(chunk retrievalchunk.Chunk) RetrievalEmbeddingRow {
		row := testEmbedding(chunk.ID, profileID, chunk.TextHash)
		row.Provider, row.Model = profile.Provider, profile.Model
		return row
	}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: []RetrievalEmbeddingRow{makeRow(projection.Chunks[0])}, ExpectedPurgeEpoch: epoch}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: []RetrievalEmbeddingRow{makeRow(projection.Chunks[1])}, ExpectedPurgeEpoch: epoch}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM retrieval_embeddings WHERE chunk_id=? AND profile_id=?`, projection.Chunks[1].ID, profileID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, 25_000, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Configured, snapshot.Enabled = true, true
	if got := semanticreadiness.Evaluate(snapshot); got.State == semanticreadiness.StateCorrupt {
		t.Fatalf("deleting newest live revision falsely corrupted profile: snapshot=%+v decision=%+v", snapshot, got)
	}
}

func TestSemanticReadinessSnapshotDetectsRuntimeAggregateCounterDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store, string)
	}{
		{
			name: "plausible projection status counters",
			mutate: func(st *Store, _ string) {
				_, _ = st.db.Exec(`UPDATE retrieval_state SET current_parent_count=0,empty_parent_count=1 WHERE singleton=1`)
			},
		},
		{
			name: "plausible embedding status counters",
			mutate: func(st *Store, profileID string) {
				_, _ = st.db.Exec(`UPDATE retrieval_embedding_profiles SET ready_embedding_count=0,pending_embedding_count=1 WHERE profile_id=?`, profileID)
			},
		},
		{
			name: "ledger chunk count disagrees with rows",
			mutate: func(st *Store, _ string) {
				_, _ = st.db.Exec(`UPDATE retrieval_parent_projections SET chunk_count=chunk_count+1`)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, _, profile := seedReadySemanticReadinessStore(t, "source:full-drift")
			defer func() { _ = st.Close() }()
			profileID, _ := profile.ID()
			baseline, err := st.SemanticReadinessSnapshotAt(context.Background(), profile, 25_000, time.Now())
			if err != nil || baseline.AggregateCountersCorrupt {
				t.Fatalf("baseline=%+v err=%v", baseline, err)
			}
			tc.mutate(st, profileID)
			snapshot, err := st.SemanticReadinessSnapshotAt(context.Background(), profile, 25_000, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !snapshot.AggregateCountersCorrupt {
				t.Fatalf("snapshot did not expose aggregate drift: %+v", snapshot)
			}
			if decision := semanticreadiness.Evaluate(withReadinessMode(snapshot)); decision.State != semanticreadiness.StateCorrupt || decision.Searchable {
				t.Fatalf("decision=%+v snapshot=%+v", decision, snapshot)
			}
		})
	}
}

func TestRetrievalRuntimeCounterRepairIsAtomicAndRebuildsAuthoritativeState(t *testing.T) {
	st, _, profile := seedReadySemanticReadinessStore(t, "source:repair-atomic")
	defer func() { _ = st.Close() }()
	profileID, _ := profile.ID()

	if _, err := st.db.Exec(`DROP TRIGGER trg_retrieval_embeddings_readiness_count_insert`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embedding_profiles SET ready_embedding_count=0 WHERE profile_id=?`, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_runtime_counter_backfill BEFORE UPDATE ON retrieval_state BEGIN SELECT RAISE(ABORT,'forced runtime counter repair failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.ensureRetrievalRuntimeReadinessCounters(); err == nil || !strings.Contains(err.Error(), "forced runtime counter repair failure") {
		t.Fatalf("repair error=%v", err)
	}
	var triggerCount, readyCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND name='trg_retrieval_embeddings_readiness_count_insert'`).Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT ready_embedding_count FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&readyCount); err != nil {
		t.Fatal(err)
	}
	if triggerCount != 0 || readyCount != 0 {
		t.Fatalf("failed repair leaked partial state: trigger_count=%d ready_count=%d", triggerCount, readyCount)
	}

	if _, err := st.db.Exec(`DROP TRIGGER fail_runtime_counter_backfill`); err != nil {
		t.Fatal(err)
	}
	if err := st.ensureRetrievalRuntimeReadinessCounters(); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND name='trg_retrieval_embeddings_readiness_count_insert'`).Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT ready_embedding_count FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&readyCount); err != nil {
		t.Fatal(err)
	}
	if triggerCount != 1 || readyCount != 1 {
		t.Fatalf("successful repair did not restore state: trigger_count=%d ready_count=%d", triggerCount, readyCount)
	}
	snapshot, err := st.SemanticReadinessSnapshotAt(context.Background(), profile, 25_000, time.Now())
	if err != nil || snapshot.AggregateCountersCorrupt {
		t.Fatalf("repaired snapshot=%+v err=%v", snapshot, err)
	}
}

func TestRetrievalProjectionRuntimeCountersTrackLifecycleRollbackAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:projection-counters")
	assertProjectionRuntimeCounters(t, st, [8]int{1, 0, 0, 1, 0, 0, 1, 0})

	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	assertProjectionRuntimeCounters(t, st, [8]int{1, 1, 0, 0, 0, 0, 0, len(projection.Chunks)})

	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE retrieval_parent_projections SET status='error' WHERE parent_kind='source' AND parent_source_key='source:projection-counters'`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertProjectionRuntimeCounters(t, st, [8]int{1, 1, 0, 0, 0, 0, 0, len(projection.Chunks)})

	if _, err := st.db.Exec(`UPDATE sources SET title=title||' changed' WHERE source_key='source:projection-counters'`); err != nil {
		t.Fatal(err)
	}
	assertProjectionRuntimeCounters(t, st, [8]int{1, 0, 0, 1, 0, 0, 1, 0})

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	assertProjectionRuntimeCounters(t, reopened, [8]int{1, 0, 0, 1, 0, 0, 1, 0})
}

func TestRetrievalProjectionRuntimeCountersTrackEmptyBlockedDeleteAndEmbeddingCascade(t *testing.T) {
	t.Run("empty and ledger delete", func(t *testing.T) {
		st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
		defer func() { _ = st.Close() }()
		ctx := context.Background()
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := st.db.Exec(`INSERT INTO items (source_key,source_type,external_id,canonical_url,title,text,content_hash,raw_json,imported_at,updated_at,last_seen_at,note_path) VALUES ('item:counter-empty','test','item:counter-empty','','','','hash','{}',?,?,?,'empty.md')`, now, now, now); err != nil {
			t.Fatal(err)
		}
		work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
		if err != nil || len(work) != 1 {
			t.Fatalf("work=%+v err=%v", work, err)
		}
		projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{
			ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey,
			DirtyRevision: work[0].DirtyRevision, Projection: projection,
			Status: RetrievalProjectionEmpty, Reason: "no_chunkable_content",
		}); err != nil {
			t.Fatal(err)
		}
		assertProjectionRuntimeCounters(t, st, [8]int{1, 0, 1, 0, 0, 0, 0, 0})
		if _, err := st.db.Exec(`DELETE FROM retrieval_parent_projections WHERE parent_kind='item' AND parent_source_key='item:counter-empty'`); err != nil {
			t.Fatal(err)
		}
		assertProjectionRuntimeCounters(t, st, [8]int{})
	})

	t.Run("blocked projection removes chunks and cascades embeddings", func(t *testing.T) {
		st, _, profile := seedReadySemanticReadinessStore(t, "source:counter-blocked")
		defer func() { _ = st.Close() }()
		profileID, _ := profile.ID()
		assertProjectionRuntimeCounters(t, st, [8]int{1, 1, 0, 0, 0, 0, 0, 1})
		if _, err := st.db.Exec(`UPDATE sources SET extracted_text=extracted_text||' changed' WHERE source_key='source:counter-blocked'`); err != nil {
			t.Fatal(err)
		}
		work, err := st.ListDirtyRetrievalParents(context.Background(), projectionRevisionForTest(t, st), 1)
		if err != nil || len(work) != 1 {
			t.Fatalf("work=%+v err=%v", work, err)
		}
		projectionHash, err := retrievalchunk.ParentProjectionHash(work[0].Parent)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.BlockRetrievalProjectionTooLarge(context.Background(), work[0].Parent, work[0].DirtyRevision, projectionHash); err != nil {
			t.Fatal(err)
		}
		assertProjectionRuntimeCounters(t, st, [8]int{1, 0, 0, 0, 1, 0, 0, 0})
		var readyEmbeddings int
		if err := st.db.QueryRow(`SELECT ready_embedding_count FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&readyEmbeddings); err != nil {
			t.Fatal(err)
		}
		if readyEmbeddings != 0 {
			t.Fatalf("chunk cascade left ready_embedding_count=%d", readyEmbeddings)
		}
		if _, err := st.db.Exec(`DELETE FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:counter-blocked'`); err != nil {
			t.Fatal(err)
		}
		assertProjectionRuntimeCounters(t, st, [8]int{})
	})
}

func TestSemanticRuntimeReadinessRequiresBoundedAdmissionIndexes(t *testing.T) {
	tests := []struct {
		name      string
		indexName string
		ready     bool
	}{
		{name: "dirty age", indexName: "idx_retrieval_parent_projections_dirty_age"},
		{name: "dirty keyset", indexName: "idx_retrieval_parent_projections_dirty_keyset"},
		{name: "profile status", indexName: "idx_retrieval_embeddings_profile_status", ready: true},
		{name: "current chunks", indexName: "idx_retrieval_chunks_parent", ready: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var st *Store
			var profile embedding.Profile
			if tc.ready {
				st, _, profile = seedReadySemanticReadinessStore(t, "source:required-index")
			} else {
				st = openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
				profile = readinessTestProfile()
				seedRetrievalSource(t, st, "source:required-index")
			}
			defer func() { _ = st.Close() }()
			if _, err := st.db.Exec(`DROP INDEX ` + tc.indexName); err != nil {
				t.Fatal(err)
			}
			snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(context.Background(), profile, 25_000, time.Now())
			if err != nil {
				if !strings.Contains(err.Error(), "no such index: "+tc.indexName) {
					t.Fatalf("runtime admission returned wrong missing-index error: %v", err)
				}
				return
			}
			snapshot.Configured, snapshot.Enabled = true, true
			decision := semanticreadiness.Evaluate(snapshot)
			if !strings.Contains(snapshot.PlanningError, "no such index: "+tc.indexName) || decision.State != semanticreadiness.StateUnavailable || decision.Searchable {
				t.Fatalf("runtime admission did not fail closed for missing index: snapshot=%+v decision=%+v", snapshot, decision)
			}
		})
	}
}

func TestSemanticRuntimeExactSmallQueryPlansStayIndexedAndSortFree(t *testing.T) {
	st, _, profile := seedReadySemanticReadinessStore(t, "source:runtime-query-plan")
	defer func() { _ = st.Close() }()
	profileID, _ := profile.ID()
	queries := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			name:  "profile validation",
			query: `EXPLAIN QUERY PLAN SELECT e.chunk_id FROM retrieval_embeddings e INDEXED BY idx_retrieval_embeddings_profile_status JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id WHERE e.profile_id=? LIMIT ?`,
			args:  []any{profileID, 25_001}, wantIndex: "idx_retrieval_embeddings_profile_status",
		},
		{
			name:  "current coverage",
			query: `EXPLAIN QUERY PLAN SELECT c.chunk_id FROM retrieval_chunks c INDEXED BY idx_retrieval_chunks_parent JOIN retrieval_parent_projections p ON p.parent_kind=c.parent_kind AND p.parent_source_key=c.parent_source_key LEFT JOIN retrieval_embeddings e ON e.chunk_id=c.chunk_id AND e.profile_id=? WHERE p.status='current' AND p.projected_revision>=p.dirty_revision LIMIT ?`,
			args:  []any{profileID, 25_001}, wantIndex: "idx_retrieval_chunks_parent",
		},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := st.db.Query(tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			joined := strings.Join(details, "\n")
			if !strings.Contains(joined, tc.wantIndex) || strings.Contains(joined, "USE TEMP B-TREE") {
				t.Fatalf("query plan does not preserve bounded index/no-sort contract:\n%s", joined)
			}
		})
	}
}

func seedReadySemanticReadinessStore(t *testing.T, sourceKey string) (*Store, retrievalchunk.Projection, embedding.Profile) {
	t.Helper()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	ctx := context.Background()
	profile := readinessTestProfile()
	profileID, _ := profile.ID()
	seedRetrievalSource(t, st, sourceKey)
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		_ = st.Close()
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey, DirtyRevision: work[0].DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	epoch, _ := st.RetrievalPurgeEpoch(ctx)
	rows := make([]RetrievalEmbeddingRow, 0, len(projection.Chunks))
	for _, chunk := range projection.Chunks {
		row := testEmbedding(chunk.ID, profileID, chunk.TextHash)
		row.Provider, row.Model = profile.Provider, profile.Model
		rows = append(rows, row)
	}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: epoch}); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	return st, projection, profile
}

func assertProjectionRuntimeCounters(t *testing.T, st *Store, want [8]int) {
	t.Helper()
	var got [8]int
	if err := st.db.QueryRow(`SELECT projection_parent_count,current_parent_count,empty_parent_count,pending_parent_count,
		blocked_parent_count,error_parent_count,dirty_parent_count,current_chunk_count FROM retrieval_state WHERE singleton=1`).Scan(
		&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6], &got[7],
	); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("projection runtime counters=%v want=%v", got, want)
	}
}

func readinessTestProfile() embedding.Profile {
	return embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
}

func withReadinessMode(snapshot semanticreadiness.Snapshot) semanticreadiness.Snapshot {
	snapshot.Configured, snapshot.Enabled = true, true
	return snapshot
}
