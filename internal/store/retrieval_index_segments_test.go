package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestNextRetrievalFlushWindowUsesReadyRevisionPrefix(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 3)

	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Rows) != 2 || window.Rows[0].Revision != 1 || window.Rows[1].Revision != 2 || window.SnapshotRevision != 2 {
		t.Fatalf("window = %+v", window)
	}
}

func TestCompleteRetrievalIndexGenerationActivatesProvenMemberRoot(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 3)
	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 2)
	if err != nil {
		t.Fatal(err)
	}
	members := retrievalSegmentMembers(window.Rows, "segment-hash")
	err = st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: RetrievalIndexGenerationRow{
			GenerationID: "generation-1", ProfileID: "flush-profile", Backend: "usearch", BackendVersion: "2.26.0",
			Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 2, SourceManifestHash: "root-hash",
			BuildStatus: RetrievalGenerationCompleted, RelativeCachePath: "semantic/db/profile/generations/generation-1",
			BuildStartedAt: time.Now().UTC(), BuildCompletedAt: time.Now().UTC(),
		},
		Segments: []RetrievalIndexSegmentRow{{
			SegmentHash: "segment-hash", ProfileID: "flush-profile", Backend: "usearch", BackendVersion: "2.26.0",
			Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 2, RelativeCachePath: "semantic/db/profile/segments/segment-hash",
			MembershipHash: "members-hash", PayloadHash: "payload-hash", ManifestHash: "segment-hash",
		}},
		Members: members, SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID: window.Profile.ActiveGenerationID, ExpectedPurgeEpoch: window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ActiveGenerationID != "generation-1" || profile.ActiveSnapshotRevision != 2 || profile.ActiveIndexedCount != 2 || profile.L0ReadyCount != 1 {
		t.Fatalf("profile = %+v", profile)
	}
	var active int
	if err := st.db.QueryRow(`SELECT active FROM retrieval_index_generations WHERE generation_id='generation-1'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("generation active = %d", active)
	}
}

func TestCompleteRetrievalIndexGenerationRejectsChangedMember(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 1)
	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	changed := testEmbedding(window.Rows[0].ChunkID, "flush-profile", window.Rows[0].ChunkTextHash)
	changed.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := st.PutRetrievalEmbedding(ctx, changed); err != nil {
		t.Fatal(err)
	}
	err = st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: RetrievalIndexGenerationRow{GenerationID: "generation-1", ProfileID: "flush-profile", Backend: "usearch", BackendVersion: "2.26.0", Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 1, SourceManifestHash: "root-hash", BuildStatus: RetrievalGenerationCompleted},
		Segments:   []RetrievalIndexSegmentRow{{SegmentHash: "segment-hash", ProfileID: "flush-profile", Backend: "usearch", BackendVersion: "2.26.0", Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 1, RelativeCachePath: "semantic/db/profile/segments/segment-hash", MembershipHash: "members-hash", PayloadHash: "payload-hash", ManifestHash: "segment-hash"}},
		Members:    retrievalSegmentMembers(window.Rows, "segment-hash"), SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID: window.Profile.ActiveGenerationID, ExpectedPurgeEpoch: window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	})
	if err == nil {
		t.Fatal("CompleteRetrievalIndexGeneration succeeded after member changed")
	}
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ActiveGenerationID != "" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestCompleteRetrievalIndexGenerationRetainsPriorImmutableSegment(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 3)
	first, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 2)
	if err != nil {
		t.Fatal(err)
	}
	firstSegment := testRetrievalSegment("segment-one", 2)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-1", 2), Segments: []RetrievalIndexSegmentRow{firstSegment},
		Members: retrievalSegmentMembers(first.Rows, firstSegment.SegmentHash), SnapshotRevision: first.SnapshotRevision,
		ExpectedActiveGenerationID: first.Profile.ActiveGenerationID, ExpectedPurgeEpoch: first.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: first.Profile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 2)
	if err != nil {
		t.Fatal(err)
	}
	secondSegment := testRetrievalSegment("segment-two", 1)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-2", 3), Segments: []RetrievalIndexSegmentRow{firstSegment, secondSegment},
		Members: retrievalSegmentMembers(second.Rows, secondSegment.SegmentHash), SnapshotRevision: second.SnapshotRevision,
		ExpectedActiveGenerationID: second.Profile.ActiveGenerationID, ExpectedPurgeEpoch: second.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: second.Profile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	segments, err := st.RetrievalIndexGenerationSegments(ctx, "generation-2")
	if err != nil || len(segments) != 2 || segments[0].SegmentHash != "segment-one" || segments[1].SegmentHash != "segment-two" {
		t.Fatalf("segments=%+v err=%v", segments, err)
	}
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil || profile.ActiveGenerationID != "generation-2" || profile.ActiveIndexedCount != 3 || profile.L0ReadyCount != 0 {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
}

func TestCompleteRetrievalIndexGenerationAllowsSameSnapshotRewrite(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 2)

	first, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 2)
	if err != nil {
		t.Fatal(err)
	}
	initialProfile := first.Profile
	initialSegment := testRetrievalSegment("segment-initial", 2)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-initial", 2), Segments: []RetrievalIndexSegmentRow{initialSegment},
		Members: retrievalSegmentMembers(first.Rows, initialSegment.SegmentHash), SnapshotRevision: first.SnapshotRevision,
		ExpectedActiveGenerationID: initialProfile.ActiveGenerationID, ExpectedPurgeEpoch: initialProfile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: initialProfile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	active, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	replacement := testRetrievalSegment("segment-replacement", 1)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-replacement", 1), Segments: []RetrievalIndexSegmentRow{replacement},
		Members: retrievalSegmentMembers(first.Rows[:1], replacement.SegmentHash), SnapshotRevision: active.ActiveSnapshotRevision,
		ExpectedActiveGenerationID: active.ActiveGenerationID, ExpectedPurgeEpoch: active.PurgeEpoch,
		ExpectedActiveSnapshotRevision: active.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationRewriteSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ActiveGenerationID != "generation-replacement" || profile.ActiveSnapshotRevision != 2 || profile.ActiveIndexedCount != 1 || profile.L0ReadyCount != 1 {
		t.Fatalf("profile = %+v", profile)
	}
	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Rows) != 1 || window.Rows[0].ChunkID != first.Rows[1].ChunkID || window.SnapshotRevision != first.Rows[1].Revision {
		t.Fatalf("window = %+v", window)
	}
}

func TestCompleteRetrievalIndexGenerationRejectsChangedActiveRoot(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 2)

	first, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	initial := first.Profile
	segmentOne := testRetrievalSegment("segment-one", 1)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-one", 1), Segments: []RetrievalIndexSegmentRow{segmentOne},
		Members: retrievalSegmentMembers(first.Rows, segmentOne.SegmentHash), SnapshotRevision: first.SnapshotRevision,
		ExpectedActiveGenerationID: initial.ActiveGenerationID, ExpectedPurgeEpoch: initial.PurgeEpoch,
		ExpectedActiveSnapshotRevision: initial.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	secondProfile := second.Profile
	segmentTwo := testRetrievalSegment("segment-two", 1)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-two", 2), Segments: []RetrievalIndexSegmentRow{segmentOne, segmentTwo},
		Members: retrievalSegmentMembers(second.Rows, segmentTwo.SegmentHash), SnapshotRevision: second.SnapshotRevision,
		ExpectedActiveGenerationID: secondProfile.ActiveGenerationID, ExpectedPurgeEpoch: secondProfile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: secondProfile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	staleSegment := testRetrievalSegment("segment-stale", 1)
	err = st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-stale", 1), Segments: []RetrievalIndexSegmentRow{staleSegment},
		Members: retrievalSegmentMembers(first.Rows, staleSegment.SegmentHash), SnapshotRevision: second.SnapshotRevision,
		ExpectedActiveGenerationID: "generation-one", ExpectedPurgeEpoch: secondProfile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: second.SnapshotRevision, ActivationMode: RetrievalGenerationRewriteSnapshot,
	})
	if err == nil {
		t.Fatal("stale generation activation succeeded")
	}
	if !errors.Is(err, ErrRetrievalGenerationActivationStale) {
		t.Fatalf("err = %v, want stale activation error", err)
	}
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ActiveGenerationID != "generation-two" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestCompleteRetrievalIndexGenerationRejectsChangedPurgeEpoch(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 1)
	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_state SET purge_epoch=purge_epoch+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	segment := testRetrievalSegment("segment-epoch", 1)
	err = st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-epoch", 1), Segments: []RetrievalIndexSegmentRow{segment},
		Members: retrievalSegmentMembers(window.Rows, segment.SegmentHash), SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID: window.Profile.ActiveGenerationID, ExpectedPurgeEpoch: window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	})
	if err == nil {
		t.Fatal("activation succeeded after purge epoch changed")
	}
	if !errors.Is(err, ErrRetrievalGenerationActivationStale) {
		t.Fatalf("err = %v, want stale activation error", err)
	}
}

func TestCompleteRetrievalIndexGenerationRejectsDuplicateUsableMembership(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 1)
	window, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	one := testRetrievalSegment("segment-duplicate-one", 1)
	two := testRetrievalSegment("segment-duplicate-two", 1)
	err = st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("generation-duplicate", 2), Segments: []RetrievalIndexSegmentRow{one, two},
		Members:          append(retrievalSegmentMembers(window.Rows, one.SegmentHash), retrievalSegmentMembers(window.Rows, two.SegmentHash)...),
		SnapshotRevision: window.SnapshotRevision, ExpectedActiveGenerationID: window.Profile.ActiveGenerationID,
		ExpectedPurgeEpoch: window.Profile.PurgeEpoch, ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision,
		ActivationMode: RetrievalGenerationAdvanceSnapshot,
	})
	if err == nil {
		t.Fatal("duplicate usable membership activation succeeded")
	}
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ActiveGenerationID != "" {
		t.Fatalf("profile = %+v", profile)
	}
}

func seedReadyRetrievalEmbeddings(t *testing.T, st *Store, profileID string, count int) {
	t.Helper()
	ctx := context.Background()
	chunks := make([]retrievalchunk.Chunk, 0, count)
	for index := 0; index < count; index++ {
		id := string(rune('a' + index))
		chunks = append(chunks, testRetrievalChunk("chunk-"+id, "item", "item:one", index, "hash-"+id, "text "+id))
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", chunks); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:one")
	for _, chunk := range chunks {
		if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, profileID, chunk.TextHash)); err != nil {
			t.Fatal(err)
		}
	}
}

func retrievalSegmentMembers(rows []RetrievalEmbeddingRow, segmentHash string) []RetrievalIndexSegmentMember {
	members := make([]RetrievalIndexSegmentMember, 0, len(rows))
	for ordinal, row := range rows {
		members = append(members, RetrievalIndexSegmentMember{SegmentHash: segmentHash, Ordinal: uint64(ordinal), ChunkID: row.ChunkID, Revision: row.Revision, VectorHash: row.VectorHash})
	}
	return members
}

func testCompletedGeneration(generationID string, indexed int) RetrievalIndexGenerationRow {
	return RetrievalIndexGenerationRow{
		GenerationID: generationID, ProfileID: "flush-profile", Backend: "usearch", BackendVersion: "2.26.0",
		Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: indexed, SourceManifestHash: generationID + "-root",
		BuildStatus: RetrievalGenerationCompleted,
	}
}

func testRetrievalSegment(hash string, indexed int) RetrievalIndexSegmentRow {
	return RetrievalIndexSegmentRow{
		SegmentHash: hash, ProfileID: "flush-profile", Backend: "usearch", BackendVersion: "2.26.0", Dimensions: 2,
		DistanceMetric: "cosine", IndexedChunkCount: indexed, RelativeCachePath: "semantic/db/profile/segments/" + hash,
		MembershipHash: hash + "-members", PayloadHash: hash + "-payload", ManifestHash: hash,
	}
}
