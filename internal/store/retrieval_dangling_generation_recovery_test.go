package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/semanticsegment"
)

func TestRetrievalDanglingGenerationRecoveryReactivatesProvenSegmentedRoot(t *testing.T) {
	st, expected := seedDanglingGenerationRecoveryFixture(t)
	ctx := context.Background()

	candidate, err := st.RetrievalDanglingGenerationRecovery(ctx, "dangling-root-profile")
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil || candidate.GenerationID != "dangling-root-generation" ||
		candidate.DescriptorSHA256 != expected.DescriptorSHA256 ||
		candidate.BackendVersion != "v1" || candidate.Dimensions != 2 ||
		candidate.SnapshotRevision != 1 || candidate.IndexedChunkCount != 1 {
		t.Fatalf("recovery candidate=%+v", candidate)
	}
	if err := st.ReactivateRetrievalDanglingGeneration(ctx, *candidate); err != nil {
		t.Fatal(err)
	}
	assertGenerationActiveForTest(t, st, "dangling-root-generation")
	assertProfileAggregatesForTest(t, st, "dangling-root-profile", "dangling-root-generation", 1, 0, 0)
	if candidate, err := st.RetrievalDanglingGenerationRecovery(ctx, "dangling-root-profile"); err != nil || candidate != nil {
		t.Fatalf("candidate after recovery=%+v err=%v", candidate, err)
	}
}

func TestRetrievalDanglingGenerationRecoveryRejectsDescriptorCatalogMismatch(t *testing.T) {
	st, _ := seedDanglingGenerationRecoveryFixture(t)
	if _, err := st.db.Exec(`
		UPDATE retrieval_index_generations SET source_manifest_hash=?
		WHERE generation_id='dangling-root-generation'`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	candidate, err := st.RetrievalDanglingGenerationRecovery(t.Context(), "dangling-root-profile")
	if err == nil || candidate != nil || !strings.Contains(err.Error(), "descriptor does not match") {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	assertRetrievalGenerationStale(t, st, "dangling-root-generation")
}

func TestRetrievalDanglingGenerationRecoveryRejectsNonStaleStatus(t *testing.T) {
	st, _ := seedDanglingGenerationRecoveryFixture(t)
	if _, err := st.db.Exec(`
		UPDATE retrieval_index_generations SET build_status='error'
		WHERE generation_id='dangling-root-generation'`); err != nil {
		t.Fatal(err)
	}
	candidate, err := st.RetrievalDanglingGenerationRecovery(t.Context(), "dangling-root-profile")
	if err == nil || candidate != nil || !strings.Contains(err.Error(), "non-recoverable") {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
}

func TestReactivateRetrievalDanglingGenerationRejectsStaleCandidate(t *testing.T) {
	st, candidate := seedDanglingGenerationRecoveryFixture(t)
	candidate.SnapshotRevision++
	err := st.ReactivateRetrievalDanglingGeneration(t.Context(), candidate)
	if !errors.Is(err, ErrRetrievalGenerationActivationStale) {
		t.Fatalf("reactivation error=%v", err)
	}
	assertRetrievalGenerationStale(t, st, "dangling-root-generation")
}

func seedDanglingGenerationRecoveryFixture(t *testing.T) (*Store, RetrievalDanglingGenerationRecovery) {
	t.Helper()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	chunk := testRetrievalChunk("dangling-root-chunk", "item", "item:dangling-root", 0, "dangling-root-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:dangling-root", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "dangling-root-profile", chunk.TextHash)); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "dangling-root-generation", ProfileID: "dangling-root-profile",
		Backend: "test", BackendVersion: "v1", Dimensions: 2,
		DistanceMetric: "cosine", SourceManifestHash: "temporary-descriptor",
		RelativeCachePath: "semantic/test/dangling-root", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "dangling-root-generation")
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := st.RetrievalIndexGenerationSegments(ctx, "dangling-root-generation")
	if err != nil || len(segments) != 1 {
		t.Fatalf("segments=%+v err=%v", segments, err)
	}
	segments[0].RelativeCachePath = filepath.ToSlash(filepath.Join(
		"semantic", databaseID, "dangling-root-profile", "segments", segments[0].SegmentHash,
	))
	if _, err := st.db.Exec(`UPDATE retrieval_index_segments SET relative_cache_path=? WHERE segment_hash=?`, segments[0].RelativeCachePath, segments[0].SegmentHash); err != nil {
		t.Fatal(err)
	}
	descriptor, err := semanticsegment.RootDescriptorSHA256(semanticsegment.RootInput{
		DatabaseID: databaseID, ProfileID: "dangling-root-profile", GenerationID: "dangling-root-generation",
		SnapshotRevision: 1, Segments: []semanticsegment.RootSegment{{
			Hash: segments[0].SegmentHash, RelativePath: segments[0].RelativeCachePath,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`
		UPDATE retrieval_index_generations
		SET source_manifest_hash=?,build_status='stale',active=0,activated_at=''
		WHERE generation_id='dangling-root-generation'`, descriptor); err != nil {
		t.Fatal(err)
	}
	return st, RetrievalDanglingGenerationRecovery{
		ProfileID: "dangling-root-profile", GenerationID: "dangling-root-generation",
		DescriptorSHA256: descriptor, BackendVersion: "v1", SnapshotRevision: 1,
		Dimensions: 2, IndexedChunkCount: 1,
	}
}
