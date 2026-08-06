package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestReadRetrievalNativeCandidatesReturnsCurrentExactActiveMembersInRequestOrder(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListReadyEmbeddings(ctx, "flush-profile", 10)
	if err != nil {
		t.Fatal(err)
	}
	byChunk := make(map[string]RetrievalEmbeddingRow, len(rows))
	for _, row := range rows {
		byChunk[row.ChunkID] = row
	}
	changed := testEmbedding("chunk-b", "flush-profile", "hash-b")
	changed.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := st.PutRetrievalEmbedding(ctx, changed); err != nil {
		t.Fatal(err)
	}

	got, err := st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		Candidates: []RetrievalNativeCandidate{
			{SegmentHash: "segment-bravo", ChunkID: "chunk-c", Revision: byChunk["chunk-c"].Revision, VectorHash: byChunk["chunk-c"].VectorHash},
			{SegmentHash: "segment-alpha", ChunkID: "chunk-b", Revision: byChunk["chunk-b"].Revision, VectorHash: byChunk["chunk-b"].VectorHash},
			{SegmentHash: "segment-alpha", ChunkID: "chunk-a", Revision: byChunk["chunk-a"].Revision, VectorHash: byChunk["chunk-a"].VectorHash},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !reflect.DeepEqual([]string{got[0].ChunkID, got[1].ChunkID}, []string{"chunk-c", "chunk-a"}) {
		t.Fatalf("current candidates=%+v", got)
	}
	if got[0].Text != "" || got[1].Text != "" {
		t.Fatalf("native candidate read materialized text: %+v", got)
	}
}

func TestReadRetrievalNativeCandidatesRejectsChangedActiveRoot(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision + 1,
		Candidates: []RetrievalNativeCandidate{{SegmentHash: "segment-alpha", ChunkID: "chunk-a", Revision: 1, VectorHash: "ignored"}},
	})
	if err == nil {
		t.Fatal("changed active root candidate read succeeded")
	}
}

func TestReadRetrievalNativeCandidatesRejectsListsAboveSQLiteBindBudget(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	candidates := make([]RetrievalNativeCandidate, MaxRetrievalNativeCandidates+1)
	for index := range candidates {
		candidates[index] = RetrievalNativeCandidate{SegmentHash: "segment", ChunkID: "chunk-" + strings.Repeat("x", index+1), Revision: 1, VectorHash: "hash"}
	}
	_, err := st.ReadRetrievalNativeCandidates(context.Background(), RetrievalNativeCandidateRequest{
		ProfileID: "profile", ExpectedActiveGenerationID: "generation", ExpectedActiveSnapshotRevision: 1,
		Candidates: candidates,
	})
	if err == nil || !strings.Contains(err.Error(), "between 1 and") {
		t.Fatalf("err=%v", err)
	}
}

func TestOrdinaryChunkChangePreservesRootAndFiltersOldNativeMember(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, candidates := nativeCandidateFixture(t, st, "chunk-a", "chunk-b")
	replacement := []retrievalchunk.Chunk{
		testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha"),
		testRetrievalChunk("chunk-b", "item", "item:one", 1, "hash-b2", "bravo changed"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", replacement); err != nil {
		t.Fatal(err)
	}
	assertGenerationActiveForTest(t, st, profile.ActiveGenerationID)
	afterReplace, err := st.RetrievalEmbeddingProfile(ctx, profile.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReplace.ActiveSnapshotRevision != profile.ActiveSnapshotRevision || afterReplace.ActiveTombstoneCount != 1 {
		t.Fatalf("profile after replacement=%+v before=%+v", afterReplace, profile)
	}
	got, err := st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		Candidates: candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChunkID != "chunk-a" {
		t.Fatalf("native candidates after replacement=%+v", got)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-b", profile.ProfileID, "hash-b2")); err != nil {
		t.Fatal(err)
	}
	afterEmbed, err := st.RetrievalEmbeddingProfile(ctx, profile.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if afterEmbed.ActiveGenerationID != profile.ActiveGenerationID || afterEmbed.L0ReadyCount != 1 || afterEmbed.ActiveTombstoneCount != 1 {
		t.Fatalf("profile after replacement embedding=%+v", afterEmbed)
	}
	window, err := st.NextRetrievalFlushWindow(ctx, profile.ProfileID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Rows) != 1 || window.Rows[0].ChunkID != "chunk-b" {
		t.Fatalf("next flush window=%+v want only replacement chunk-b", window.Rows)
	}
	got, err = st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		Candidates: candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChunkID != "chunk-a" {
		t.Fatalf("stale native member returned after L0 replacement=%+v", got)
	}
}

func TestDirtyParentSuppressesUnchangedSiblingNativeRecallWithoutInvalidatingRoot(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, candidates := nativeCandidateFixture(t, st, "chunk-a", "chunk-b")
	if _, err := st.db.Exec(`UPDATE retrieval_parent_projections SET status='pending' WHERE parent_kind='item' AND parent_source_key='item:one'`); err != nil {
		t.Fatal(err)
	}
	assertGenerationActiveForTest(t, st, profile.ActiveGenerationID)
	got, err := st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		Candidates: candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("dirty parent native candidates=%+v want all siblings suppressed", got)
	}
}

func TestOrdinaryChunkDeletionPreservesRootAndFiltersDeletedNativeMember(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, candidates := nativeCandidateFixture(t, st, "chunk-a", "chunk-b")
	remaining := []retrievalchunk.Chunk{testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha")}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", remaining); err != nil {
		t.Fatal(err)
	}
	assertGenerationActiveForTest(t, st, profile.ActiveGenerationID)
	got, err := st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		Candidates: candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChunkID != "chunk-a" {
		t.Fatalf("native candidates after deletion=%+v", got)
	}
	row, err := st.RetrievalEmbeddingProfile(ctx, profile.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ActiveGenerationID != profile.ActiveGenerationID || row.ActiveTombstoneCount != 1 {
		t.Fatalf("profile after deletion=%+v", row)
	}
}

func TestPreservedRootKeepsNativeTextHashMismatchFailClosed(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, candidates := nativeCandidateFixture(t, st, "chunk-a")
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET chunk_text_hash='corrupt-text-hash' WHERE chunk_id='chunk-a' AND profile_id=?`, profile.ProfileID); err != nil {
		t.Fatal(err)
	}
	_, err := st.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		Candidates: candidates,
	})
	var corruption *RetrievalEmbeddingCorruptionError
	if !errors.As(err, &corruption) || corruption.ChunkID != "chunk-a" || corruption.ProfileID != profile.ProfileID {
		t.Fatalf("native corruption error=%v typed=%+v", err, corruption)
	}
}

func nativeCandidateFixture(t *testing.T, st *Store, chunkIDs ...string) (RetrievalEmbeddingProfileRow, []RetrievalNativeCandidate) {
	t.Helper()
	profile, err := st.RetrievalEmbeddingProfile(context.Background(), "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListReadyEmbeddings(context.Background(), profile.ProfileID, 10)
	if err != nil {
		t.Fatal(err)
	}
	byChunk := make(map[string]RetrievalEmbeddingRow, len(rows))
	for _, row := range rows {
		byChunk[row.ChunkID] = row
	}
	candidates := make([]RetrievalNativeCandidate, 0, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		row, ok := byChunk[chunkID]
		if !ok {
			t.Fatalf("ready embedding %s is missing", chunkID)
		}
		segment := "segment-alpha"
		if chunkID == "chunk-c" {
			segment = "segment-bravo"
		}
		candidates = append(candidates, RetrievalNativeCandidate{SegmentHash: segment, ChunkID: chunkID, Revision: row.Revision, VectorHash: row.VectorHash})
	}
	return profile, candidates
}
