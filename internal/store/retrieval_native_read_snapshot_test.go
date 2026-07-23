package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
)

func TestRetrievalNativeReadSnapshotRetainsOneConsistentView(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	writer := openStoreAtPath(t, path)
	defer func() { _ = writer.Close() }()
	seedActiveCompactionRoot(t, writer)
	ctx := context.Background()
	profile, err := writer.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	ready, err := writer.ListReadyEmbeddings(ctx, profile.ProfileID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var prior RetrievalEmbeddingRow
	for _, row := range ready {
		if row.ChunkID == "chunk-b" {
			prior = row
			break
		}
	}
	if prior.ChunkID == "" {
		t.Fatal("missing seeded chunk-b embedding")
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = reader.Close() }()
	snapshot, err := reader.BeginRetrievalNativeReadSnapshot(ctx)
	if err != nil {
		t.Fatalf("BeginRetrievalNativeReadSnapshot: %v", err)
	}
	defer func() { _ = snapshot.Close() }()
	request := RetrievalActiveRootReadRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
	}
	initialL0, err := snapshot.ReadRetrievalExactL0(ctx, request, RetrievalSegmentHardLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(initialL0) != 0 {
		t.Fatalf("initial L0=%+v", initialL0)
	}

	changed := testEmbedding("chunk-b", profile.ProfileID, "hash-b")
	changed.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := writer.PutRetrievalEmbedding(ctx, changed); err != nil {
		t.Fatal(err)
	}

	validated, err := snapshot.ReadRetrievalNativeCandidates(ctx, RetrievalNativeCandidateRequest{
		ProfileID: request.ProfileID, ExpectedActiveGenerationID: request.ExpectedActiveGenerationID,
		ExpectedPurgeEpoch: request.ExpectedPurgeEpoch, ExpectedActiveSnapshotRevision: request.ExpectedActiveSnapshotRevision,
		Candidates: []RetrievalNativeCandidate{{SegmentHash: "segment-alpha", ChunkID: prior.ChunkID, Revision: prior.Revision, VectorHash: prior.VectorHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(validated) != 1 || validated[0].ChunkID != "chunk-b" || validated[0].Revision != prior.Revision {
		t.Fatalf("snapshot candidates=%+v", validated)
	}
	stableL0, err := snapshot.ReadRetrievalExactL0(ctx, request, RetrievalSegmentHardLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(stableL0) != 0 {
		t.Fatalf("snapshot L0 changed after writer update: %+v", stableL0)
	}

	freshL0, err := reader.ReadRetrievalExactL0(ctx, request, RetrievalSegmentHardLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(freshL0) != 1 || freshL0[0].ChunkID != "chunk-b" || freshL0[0].Revision <= prior.Revision {
		t.Fatalf("fresh L0=%+v prior=%+v", freshL0, prior)
	}
}
