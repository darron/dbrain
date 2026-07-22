package semanticbuild

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

func TestCompactReturnsNoopWithoutEligibleSegments(t *testing.T) {
	result, err := Compact(context.Background(), nil, nil, CompactionOptions{})
	if err == nil || result.Plan.Kind != SegmentCompactionNone {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCompactRejectsChangedLiveStreamBeforePublication(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	st := compactionFakeStore{snapshot: store.RetrievalActiveSegmentCompactionSnapshot{Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, ActiveGenerationID: "root", ActiveSnapshotRevision: 1}, Segments: []store.RetrievalActiveSegmentCompactionSegment{{RetrievalIndexSegmentRow: store.RetrievalIndexSegmentRow{SegmentHash: "segment-a"}, CreatedOrder: 1, LiveCount: 5_000, TombstoneCount: 51}}}}
	_, err = Compact(context.Background(), &st, testStreamingBuilder{}, CompactionOptions{Profile: profile, Backend: "test", BackendVersion: "v1", DistanceMetric: "cosine", CacheDir: t.TempDir()})
	if err == nil || st.completeCalls != 0 {
		t.Fatalf("err=%v complete=%d", err, st.completeCalls)
	}
}

func TestCompactRewritesRootAndRetainsUnselectedSegment(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	retained, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{DatabaseID: "db", ProfileID: profileID, Backend: "test", BackendVersion: "v1", DistanceMetric: "cosine", Dimensions: 2, Members: []semanticsegment.Member{{Ordinal: 0, ChunkID: "retained", Revision: 1, VectorHash: "retained-vector"}}, Payload: func(w io.Writer) error { _, err := io.WriteString(w, "retained"); return err }})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.RetrievalActiveSegmentMember, 0, 5_000)
	for i := 0; i < 5_000; i++ {
		rows = append(rows, store.RetrievalActiveSegmentMember{SegmentHash: "selected", Ordinal: uint64(i), Embedding: store.RetrievalEmbeddingRow{ChunkID: fmt.Sprintf("chunk-%d", i), ProfileID: profileID, Revision: 1, VectorHash: fmt.Sprintf("vector-%d", i), Dimensions: 2}})
	}
	retainedRow := store.RetrievalIndexSegmentRow{SegmentHash: retained.Hash, ProfileID: profileID, Backend: "test", BackendVersion: "v1", Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 1, RelativeCachePath: retained.RelativePath, MembershipHash: retained.Manifest.MembersSHA256, PayloadHash: retained.Manifest.PayloadSHA256, ManifestHash: retained.Manifest.DescriptorSHA256}
	st := compactionFakeStore{
		snapshot:   store.RetrievalActiveSegmentCompactionSnapshot{Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, ActiveGenerationID: "root", PurgeEpoch: 1, ActiveSnapshotRevision: 1}, Segments: []store.RetrievalActiveSegmentCompactionSegment{{RetrievalIndexSegmentRow: store.RetrievalIndexSegmentRow{SegmentHash: "selected"}, CreatedOrder: 1, LiveCount: 5_000, TombstoneCount: 51}, {RetrievalIndexSegmentRow: retainedRow, CreatedOrder: 2, LiveCount: 1}}},
		streamRows: rows, existing: []store.RetrievalIndexSegmentRow{{SegmentHash: "selected", ProfileID: profileID, Backend: "test", BackendVersion: "v1", Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 5_051, RelativeCachePath: "old"}, retainedRow},
	}
	result, err := Compact(context.Background(), &st, testStreamingBuilder{}, CompactionOptions{Profile: profile, Backend: "test", BackendVersion: "v1", DistanceMetric: "cosine", CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if st.completeCalls != 1 || st.completed.ActivationMode != store.RetrievalGenerationRewriteSnapshot || len(st.completed.Segments) != 2 || len(st.completed.Members) != 5_000 || result.StreamedLiveMembers != 5_000 || len(result.ReplacementSegmentHashes) != 1 {
		t.Fatalf("completion=%+v result=%+v", st.completed, result)
	}
	if _, err := semanticsegment.OpenRoot(cache, "db", profileID, result.GenerationID); err != nil {
		t.Fatal(err)
	}
}

func TestCompactRejectsLastSegmentToExactL0(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.RetrievalActiveSegmentMember, 0, 4_999)
	for i := 0; i < 4_999; i++ {
		rows = append(rows, store.RetrievalActiveSegmentMember{Embedding: store.RetrievalEmbeddingRow{ChunkID: fmt.Sprintf("l0-%d", i), ProfileID: profileID, Revision: 1, VectorHash: fmt.Sprintf("l0-vector-%d", i), Dimensions: 2}})
	}
	st := compactionFakeStore{snapshot: store.RetrievalActiveSegmentCompactionSnapshot{Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, ActiveGenerationID: "root", PurgeEpoch: 1, ActiveSnapshotRevision: 1}, Segments: []store.RetrievalActiveSegmentCompactionSegment{{RetrievalIndexSegmentRow: store.RetrievalIndexSegmentRow{SegmentHash: "selected"}, CreatedOrder: 1, LiveCount: 4_999, TombstoneCount: 51}}}, streamRows: rows, existing: []store.RetrievalIndexSegmentRow{{SegmentHash: "selected", ProfileID: profileID, IndexedChunkCount: 5_050}}}
	_, err = Compact(context.Background(), &st, nil, CompactionOptions{Profile: profile, Backend: "test", BackendVersion: "v1", DistanceMetric: "cosine", CacheDir: t.TempDir()})
	if err == nil || st.completeCalls != 0 {
		t.Fatalf("err=%v complete=%d", err, st.completeCalls)
	}
}

func TestCompactPlansFromActiveSnapshot(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	st := compactionFakeStore{snapshot: store.RetrievalActiveSegmentCompactionSnapshot{
		Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID},
		Segments: []store.RetrievalActiveSegmentCompactionSegment{{
			RetrievalIndexSegmentRow: store.RetrievalIndexSegmentRow{SegmentHash: "segment-a"},
			CreatedOrder:             1, LiveCount: 5_000, TombstoneCount: 51,
		}},
	}}
	result, err := Compact(context.Background(), &st, nil, CompactionOptions{Profile: profile})
	if err == nil {
		t.Fatal("eligible compaction accepted a nil streaming builder")
	}
	if result.Plan.Kind != SegmentCompactionSingleton || len(result.Plan.Inputs) != 1 || result.Plan.Inputs[0].SegmentHash != "segment-a" {
		t.Fatalf("plan=%+v", result.Plan)
	}
}

type compactionFakeStore struct {
	snapshot      store.RetrievalActiveSegmentCompactionSnapshot
	streamRows    []store.RetrievalActiveSegmentMember
	existing      []store.RetrievalIndexSegmentRow
	completed     store.CompleteRetrievalIndexGenerationInput
	completeErr   error
	completeCalls int
}

func (f *compactionFakeStore) RetrievalActiveSegmentCompactionSnapshot(context.Context, string) (store.RetrievalActiveSegmentCompactionSnapshot, error) {
	return f.snapshot, nil
}
func (*compactionFakeStore) RetrievalDatabaseID(context.Context) (string, error) { return "db", nil }
func (f *compactionFakeStore) StreamRetrievalActiveSegmentMembers(_ context.Context, _ store.RetrievalActiveSegmentMemberStreamRequest, visit store.RetrievalActiveSegmentMemberVisitor) (int, error) {
	for index, row := range f.streamRows {
		if err := visit(row); err != nil {
			return index, err
		}
	}
	return len(f.streamRows), nil
}
func (f *compactionFakeStore) RetrievalIndexGenerationSegments(context.Context, string) ([]store.RetrievalIndexSegmentRow, error) {
	return append([]store.RetrievalIndexSegmentRow(nil), f.existing...), nil
}
func (f *compactionFakeStore) CompleteRetrievalIndexGeneration(_ context.Context, input store.CompleteRetrievalIndexGenerationInput) error {
	f.completeCalls++
	f.completed = input
	return f.completeErr
}

type testStreamingBuilder struct{}

func (testStreamingBuilder) Begin(context.Context, int) (StreamingSegmentPayloadSession, error) {
	return testStreamingSession{}, nil
}

type testStreamingSession struct{}

func (testStreamingSession) Add(context.Context, store.RetrievalEmbeddingRow) error { return nil }
func (testStreamingSession) Finish(context.Context) (func(io.Writer) error, error) {
	return func(w io.Writer) error { _, err := io.WriteString(w, "payload"); return err }, nil
}
func (testStreamingSession) Close() error { return nil }
