package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
)

func TestReadRetrievalExactL0ReturnsOnlyCurrentNonMembers(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedActiveCompactionRoot(t, st)
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	changed := testEmbedding("chunk-b", "flush-profile", "hash-b")
	changed.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := st.PutRetrievalEmbedding(ctx, changed); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ReadRetrievalExactL0(ctx, RetrievalActiveRootReadRequest{
		ProfileID: profile.ProfileID, ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
	}, 5_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChunkID != "chunk-b" || rows[0].Revision <= 3 {
		t.Fatalf("l0 rows=%+v", rows)
	}
	if rows[0].Text != "" {
		t.Fatalf("exact L0 read materialized text: %+v", rows[0])
	}
}
