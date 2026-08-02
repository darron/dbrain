package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestRetrievalActiveSegmentCompactionSnapshotReadsActiveRootInStableOrder(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)

	snapshot, err := st.RetrievalActiveSegmentCompactionSnapshot(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile.ActiveGenerationID != "compaction-root" || snapshot.Profile.ActiveSnapshotRevision != 3 {
		t.Fatalf("profile = %+v", snapshot.Profile)
	}
	if len(snapshot.Segments) != 2 {
		t.Fatalf("segments = %+v", snapshot.Segments)
	}
	first, second := snapshot.Segments[0], snapshot.Segments[1]
	if first.SegmentHash != "segment-alpha" || first.CreatedOrder != 1 || first.LiveCount != 2 || first.TombstoneCount != 0 {
		t.Fatalf("first segment = %+v", first)
	}
	if second.SegmentHash != "segment-bravo" || second.CreatedOrder != 2 || second.LiveCount != 1 || second.TombstoneCount != 0 {
		t.Fatalf("second segment = %+v", second)
	}
	if first.Backend != "usearch" || first.IndexedChunkCount != 2 || first.RelativeCachePath == "" || first.ManifestHash == "" {
		t.Fatalf("first immutable metadata = %+v", first.RetrievalIndexSegmentRow)
	}
}

func TestRetrievalActiveSegmentCompactionSnapshotCountsOnlyCurrentReadyMembership(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)

	changed := testEmbedding("chunk-b", "flush-profile", "hash-b")
	changed.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := st.PutRetrievalEmbedding(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_parent_projections SET status='pending' WHERE parent_kind='item' AND parent_source_key='item:two'`); err != nil {
		t.Fatal(err)
	}

	snapshot, err := st.RetrievalActiveSegmentCompactionSnapshot(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Segments) != 2 {
		t.Fatalf("segments = %+v", snapshot.Segments)
	}
	if got := snapshot.Segments[0]; got.SegmentHash != "segment-alpha" || got.LiveCount != 1 || got.TombstoneCount != 1 {
		t.Fatalf("alpha segment = %+v", got)
	}
	if got := snapshot.Segments[1]; got.SegmentHash != "segment-bravo" || got.LiveCount != 0 || got.TombstoneCount != 1 {
		t.Fatalf("bravo segment = %+v", got)
	}
}

func TestRetrievalActiveSegmentCompactionSnapshotRejectsCatalogMembershipDrift(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	if _, err := st.db.Exec(`UPDATE retrieval_index_segments SET indexed_chunk_count=99 WHERE segment_hash='segment-alpha'`); err != nil {
		t.Fatal(err)
	}

	if _, err := st.RetrievalActiveSegmentCompactionSnapshot(context.Background(), "flush-profile"); err == nil {
		t.Fatal("compaction snapshot succeeded with catalog membership drift")
	}
}

func TestRetrievalActiveSegmentCompactionSnapshotDoesNotBorrowInactiveHistory(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 1)

	snapshot, err := st.RetrievalActiveSegmentCompactionSnapshot(context.Background(), "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile.ActiveGenerationID != "" || len(snapshot.Segments) != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func seedActiveCompactionRoot(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha"),
		testRetrievalChunk("chunk-b", "item", "item:one", 1, "hash-b", "bravo"),
		testRetrievalChunk("chunk-c", "item", "item:two", 0, "hash-c", "charlie"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", chunks[:2]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:two", chunks[2:]); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:one")
	markProjectionCurrentForTest(t, st, "item", "item:two")
	for _, chunk := range chunks {
		if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "flush-profile", chunk.TextHash)); err != nil {
			t.Fatal(err)
		}
	}
	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 3)
	if err != nil {
		t.Fatal(err)
	}
	alpha := testRetrievalSegment("segment-alpha", 2)
	bravo := testRetrievalSegment("segment-bravo", 1)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation:                     testCompletedGeneration("compaction-root", 3),
		Segments:                       []RetrievalIndexSegmentRow{alpha, bravo},
		Members:                        append(retrievalSegmentMembers(window.Rows[:2], alpha.SegmentHash), retrievalSegmentMembers(window.Rows[2:], bravo.SegmentHash)...),
		SnapshotRevision:               window.SnapshotRevision,
		ExpectedActiveGenerationID:     window.Profile.ActiveGenerationID,
		ExpectedPurgeEpoch:             window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision,
		ActivationMode:                 RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_index_segments SET created_at=? WHERE segment_hash='segment-alpha'`, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_index_segments SET created_at=? WHERE segment_hash='segment-bravo'`, time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}
