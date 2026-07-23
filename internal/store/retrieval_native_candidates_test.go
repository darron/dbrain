package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
)

func TestReadRetrievalNativeCandidatesReturnsCurrentExactActiveMembersInRequestOrder(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
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
}

func TestReadRetrievalNativeCandidatesRejectsChangedActiveRoot(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
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
