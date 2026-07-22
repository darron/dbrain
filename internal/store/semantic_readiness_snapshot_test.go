package store

import (
	"context"
	"path/filepath"
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

func readinessTestProfile() embedding.Profile {
	return embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
}

func withReadinessMode(snapshot semanticreadiness.Snapshot) semanticreadiness.Snapshot {
	snapshot.Configured, snapshot.Enabled = true, true
	return snapshot
}
