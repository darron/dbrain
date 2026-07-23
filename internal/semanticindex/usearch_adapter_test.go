//go:build usearch && cgo

package semanticindex

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

func TestUSearchAdapterSearchAndReopenPreservesNearestOrdinals(t *testing.T) {
	index, err := NewUSearch(USearchOptions{Dimensions: 2, Connectivity: 16, ExpansionAdd: 128, ExpansionSearch: 128})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	if err := index.Reserve(3); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(
		HNSWNode{Ordinal: 11, Vector: []float32{1, 0}},
		HNSWNode{Ordinal: 22, Vector: []float32{0.8, 0.6}},
		HNSWNode{Ordinal: 33, Vector: []float32{0, 1}},
	); err != nil {
		t.Fatal(err)
	}
	assertUSearchOrdinals(t, index, []uint64{11, 22})

	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Import(&payload); err != nil {
		t.Fatal(err)
	}
	assertUSearchOrdinals(t, reopened, []uint64{11, 22})
}

func TestOpenUSearchRootImportsVerifiedSegments(t *testing.T) {
	cache := t.TempDir()
	profile := "profile"
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Reserve(1); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(HNSWNode{Ordinal: 0, Vector: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	segment, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{DatabaseID: "db", ProfileID: profile, Backend: BackendUSearch, BackendVersion: "test", DistanceMetric: "cosine", Dimensions: 2, Members: []semanticsegment.Member{{Ordinal: 0, ChunkID: "chunk", Revision: 1, VectorHash: "hash"}}, Payload: func(w io.Writer) error { _, err := w.Write(payload.Bytes()); return err }})
	if err != nil {
		t.Fatal(err)
	}
	root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{DatabaseID: "db", ProfileID: profile, GenerationID: "root", SnapshotRevision: 1, Segments: []semanticsegment.RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenUSearchRoot(cache, "db", profile, root.Manifest.GenerationID, USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loaded.Close() }()
	if len(loaded.Segments) != 1 {
		t.Fatalf("segments=%d", len(loaded.Segments))
	}
	assertUSearchOrdinals(t, loaded.Segments[0].Index, []uint64{0})
}

func TestUSearchRootCandidatesResolveImmutableMembers(t *testing.T) {
	cache := t.TempDir()
	profile := "profile"
	segment := publishUSearchTestSegment(t, cache, profile, []HNSWNode{
		{Ordinal: 0, Vector: []float32{1, 0}},
		{Ordinal: 1, Vector: []float32{0, 1}},
	}, []semanticsegment.Member{
		{Ordinal: 0, ChunkID: "first", Revision: 3, VectorHash: "hash-first"},
		{Ordinal: 1, ChunkID: "second", Revision: 4, VectorHash: "hash-second"},
	})
	root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{DatabaseID: "db", ProfileID: profile, GenerationID: "root", SnapshotRevision: 4, Segments: []semanticsegment.RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenUSearchRoot(cache, "db", profile, root.Manifest.GenerationID, USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loaded.Close() })

	candidates, err := loaded.Candidates([]float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []USearchRootCandidate{
		{SegmentHash: segment.Hash, Member: semanticsegment.Member{Ordinal: 0, ChunkID: "first", Revision: 3, VectorHash: "hash-first"}, ApproximateDistance: 0},
		{SegmentHash: segment.Hash, Member: semanticsegment.Member{Ordinal: 1, ChunkID: "second", Revision: 4, VectorHash: "hash-second"}, ApproximateDistance: 1},
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("candidates=%+v want=%+v", candidates, want)
	}
}

func TestUSearchRootCandidatesGloballyOrderApproximateHitsBeforeExactRerank(t *testing.T) {
	far := newUSearchRootTestIndex(t, HNSWNode{Ordinal: 0, Vector: []float32{0, 1}})
	near := newUSearchRootTestIndex(t, HNSWNode{Ordinal: 0, Vector: []float32{1, 0}})
	root := &USearchRoot{Segments: []USearchRootSegment{
		{SegmentHash: "segment-z", Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{{Ordinal: 0, ChunkID: "far", Revision: 1, VectorHash: "far-hash"}}}, Index: far},
		{SegmentHash: "segment-a", Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{{Ordinal: 0, ChunkID: "near", Revision: 1, VectorHash: "near-hash"}}}, Index: near},
	}}
	t.Cleanup(func() { _ = root.Close() })

	candidates, err := root.Candidates([]float32{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{candidates[0].Member.ChunkID, candidates[1].Member.ChunkID}; !reflect.DeepEqual(got, []string{"near", "far"}) {
		t.Fatalf("candidate chunks=%v", got)
	}
}

func TestUSearchRootCandidatesRejectOrdinalOutsideImmutableManifest(t *testing.T) {
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	if err := index.Reserve(1); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(HNSWNode{Ordinal: 1, Vector: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	root := &USearchRoot{Segments: []USearchRootSegment{{
		SegmentHash: "segment",
		Manifest:    semanticsegment.Manifest{Members: []semanticsegment.Member{{Ordinal: 0, ChunkID: "only", Revision: 1, VectorHash: "hash"}}},
		Index:       index,
	}}}
	_, err = root.Candidates([]float32{1, 0}, 1)
	if err == nil || !strings.Contains(err.Error(), "outside immutable manifest") {
		t.Fatalf("err=%v", err)
	}
}

func TestUSearchCandidateSearcherExactlyReranksCurrentValidatedCandidates(t *testing.T) {
	profile := embedding.Profile{Provider: "fake", Model: "model", Dimensions: 2, ProjectionVersion: "projection", ChunkerVersion: "chunker", Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	index := newUSearchRootTestIndex(t,
		HNSWNode{Ordinal: 0, Vector: []float32{1, 0}},
		HNSWNode{Ordinal: 1, Vector: []float32{0, 1}},
	)
	root := &USearchRoot{
		Root: semanticsegment.Root{Manifest: semanticsegment.RootManifest{ProfileID: profileID, GenerationID: "generation", SnapshotRevision: 4, PurgeEpoch: 2}},
		Segments: []USearchRootSegment{{
			SegmentHash: "segment",
			Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{
				{Ordinal: 0, ChunkID: "near", Revision: 3, VectorHash: "near-hash"},
				{Ordinal: 1, ChunkID: "far", Revision: 4, VectorHash: "far-hash"},
			}},
			Index: index,
		}},
	}
	t.Cleanup(func() { _ = root.Close() })
	st := &fakeUSearchCandidateStore{rows: []store.RetrievalEmbeddingRow{
		{ChunkID: "far", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), VectorHash: "far-hash", Revision: 4, ParentKind: "source", ParentSourceKey: "source:far", EvidenceRole: "raw", SourceType: "article", SectionOrdinal: 4, ProjectionVersion: "projection", ChunkerVersion: "chunker"},
		{ChunkID: "near", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), VectorHash: "near-hash", Revision: 3, ParentKind: "source", ParentSourceKey: "source:near", EvidenceRole: "raw", SourceType: "article", SectionOrdinal: 2, ProjectionVersion: "projection", ChunkerVersion: "chunker"},
	}}

	hits, status, err := NewUSearchCandidateSearcher(root, st).Search(context.Background(), []float32{1, 0}, SearchOptions{Profile: profile, Limit: 2, MaxChunks: 999})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateSearched || status.Backend != BackendUSearch || status.GenerationID != "generation" || !reflect.DeepEqual([]string{hits[0].ChunkID, hits[1].ChunkID}, []string{"near", "far"}) {
		t.Fatalf("hits=%+v status=%+v", hits, status)
	}
	if st.request.ExpectedActiveGenerationID != "generation" || st.request.ExpectedPurgeEpoch != 2 || st.request.ExpectedActiveSnapshotRevision != 4 || len(st.request.Candidates) != 2 {
		t.Fatalf("candidate request=%+v", st.request)
	}
}

func TestUSearchAdapterRejectsInvalidState(t *testing.T) {
	if _, err := NewUSearch(USearchOptions{Dimensions: 0}); err == nil {
		t.Fatal("expected zero dimensions to be rejected")
	}
	if _, err := NewUSearch(USearchOptions{Dimensions: 2, Connectivity: -1}); err == nil {
		t.Fatal("expected negative connectivity to be rejected")
	}
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(HNSWNode{Ordinal: 1, Vector: []float32{1, 0}}); err == nil {
		t.Fatal("expected closed index add to be rejected")
	}
	if err := index.Import(bytes.NewReader([]byte("not a usearch payload"))); err == nil {
		t.Fatal("expected closed index import to be rejected")
	}
	fresh, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if err := fresh.Import(bytes.NewReader([]byte("not a usearch payload"))); err == nil {
		t.Fatal("expected malformed payload to be rejected")
	}
}

func TestUSearchAdapterRejectsShortExportWrite(t *testing.T) {
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	if err := index.Reserve(1); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(HNSWNode{Ordinal: 1, Vector: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := index.Export(shortWriter{}); err == nil {
		t.Fatal("expected short export write to be rejected")
	}
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

func assertUSearchOrdinals(t *testing.T, index *USearch, want []uint64) {
	t.Helper()
	hits, err := index.Search([]float32{1, 0}, len(want))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]uint64, len(hits))
	for i, hit := range hits {
		got[i] = hit.Ordinal
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordinals=%v want=%v", got, want)
	}
}

func publishUSearchTestSegment(t *testing.T, cache, profile string, nodes []HNSWNode, members []semanticsegment.Member) semanticsegment.Segment {
	t.Helper()
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()
	if err := index.Reserve(len(nodes)); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(nodes...); err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		t.Fatal(err)
	}
	segment, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{DatabaseID: "db", ProfileID: profile, Backend: BackendUSearch, BackendVersion: "test", DistanceMetric: "cosine", Dimensions: 2, Members: members, Payload: func(w io.Writer) error { _, err := w.Write(payload.Bytes()); return err }})
	if err != nil {
		t.Fatal(err)
	}
	return segment
}

func newUSearchRootTestIndex(t *testing.T, nodes ...HNSWNode) *USearch {
	t.Helper()
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Reserve(len(nodes)); err != nil {
		_ = index.Close()
		t.Fatal(err)
	}
	if err := index.Add(nodes...); err != nil {
		_ = index.Close()
		t.Fatal(err)
	}
	return index
}

type fakeUSearchCandidateStore struct {
	request store.RetrievalNativeCandidateRequest
	rows    []store.RetrievalEmbeddingRow
	err     error
}

func (f *fakeUSearchCandidateStore) ReadRetrievalNativeCandidates(_ context.Context, request store.RetrievalNativeCandidateRequest) ([]store.RetrievalEmbeddingRow, error) {
	f.request = request
	return append([]store.RetrievalEmbeddingRow(nil), f.rows...), f.err
}
