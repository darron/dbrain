//go:build usearch && cgo

package semanticindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	fixture := publishUSearchRootFixture(t, cache, profile, usearchRootFixtureOptions{})
	loaded, err := OpenUSearchRoot(context.Background(), cache, "db", profile, fixture.root.Manifest.GenerationID, USearchRootExpectations{
		Index:            USearchOptions{Dimensions: 2},
		SnapshotRevision: 7,
		PurgeEpoch:       3,
		BackendVersion:   USearchVersion,
		DescriptorSHA256: fixture.root.Manifest.DescriptorSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loaded.Close() }()
	if len(loaded.Segments) != 1 {
		t.Fatalf("segments=%d", len(loaded.Segments))
	}
	assertUSearchOrdinals(t, loaded.Segments[0].Index, []uint64{0})
}

func TestOpenUSearchRootReadsEachPayloadOnce(t *testing.T) {
	cache := t.TempDir()
	fixture := publishUSearchRootFixture(t, cache, "profile", usearchRootFixtureOptions{})
	payloadOpens := 0
	loaded, err := openUSearchRoot(context.Background(), cache, "db", "profile", fixture.root.Manifest.GenerationID, USearchRootExpectations{
		Index:            USearchOptions{Dimensions: 2},
		SnapshotRevision: 7,
		PurgeEpoch:       3,
		BackendVersion:   USearchVersion,
		DescriptorSHA256: fixture.root.Manifest.DescriptorSHA256,
	}, func(path string) (io.ReadCloser, error) {
		payloadOpens++
		return os.Open(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loaded.Close() })
	if payloadOpens != 1 {
		t.Fatalf("payload opens=%d want=1", payloadOpens)
	}
}

func TestOpenUSearchRootRejectsNativeVectorCountManifestCardinalityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		nodes   []HNSWNode
		members []semanticsegment.Member
		want    string
	}{
		{
			name:  "one native vector for two manifest members",
			nodes: []HNSWNode{{Ordinal: 0, Vector: []float32{1, 0}}},
			members: []semanticsegment.Member{
				{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"},
				{Ordinal: 1, ChunkID: "second", Revision: 1, VectorHash: "second-hash"},
			},
			want: "native vector count 1 want manifest members 2",
		},
		{
			name: "two native vectors for one manifest member",
			nodes: []HNSWNode{
				{Ordinal: 0, Vector: []float32{1, 0}},
				{Ordinal: 1, Vector: []float32{0, 1}},
			},
			members: []semanticsegment.Member{
				{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"},
			},
			want: "native vector count 2 want manifest members 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			segment := publishUSearchTestSegment(t, cache, "profile", tc.nodes, tc.members)
			root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{
				DatabaseID: "db", ProfileID: "profile", GenerationID: "root", SnapshotRevision: 1,
				Segments: []semanticsegment.RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}},
			})
			if err != nil {
				t.Fatal(err)
			}

			loaded, err := openUSearchRoot(context.Background(), cache, "db", "profile", root.Manifest.GenerationID, USearchRootExpectations{
				Index:            USearchOptions{Dimensions: 2},
				SnapshotRevision: 1,
				BackendVersion:   USearchVersion,
				DescriptorSHA256: root.Manifest.DescriptorSHA256,
			}, func(path string) (io.ReadCloser, error) {
				return os.Open(path)
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("open root error=%v want %q", err, tc.want)
			}
			if loaded == nil || len(loaded.Segments) != 0 {
				t.Fatalf("loaded=%#v want no admitted native segments", loaded)
			}
			if err := loaded.Close(); err != nil {
				t.Fatalf("close rejected root: %v", err)
			}
		})
	}
}

func TestOpenUSearchRootRejectsCanceledContextBeforeFilesystemWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loaded, err := OpenUSearchRoot(ctx, filepath.Join(t.TempDir(), "missing"), "db", "profile", "root", USearchRootExpectations{})
	if loaded != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("loaded=%#v error=%v want context cancellation", loaded, err)
	}
}

func TestReadVerifiedUSearchPayloadStopsDuringStreamedRead(t *testing.T) {
	cache := t.TempDir()
	fixture := publishUSearchRootFixture(t, cache, "profile", usearchRootFixtureOptions{})
	payloadPath := filepath.Join(cache, filepath.FromSlash(fixture.segment.RelativePath), semanticsegment.PayloadFileName)
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstChunkReader{payload: payload, cancel: cancel}

	_, err = readVerifiedUSearchPayload(ctx, reader, fixture.segment.Manifest.PayloadSHA256)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read payload error=%v want context cancellation", err)
	}
}

func TestOpenUSearchRootCancellationBetweenSegmentsClosesLoadedIndexes(t *testing.T) {
	cache := t.TempDir()
	profile := "profile"
	first := publishUSearchTestSegment(t, cache, profile,
		[]HNSWNode{{Ordinal: 0, Vector: []float32{1, 0}}},
		[]semanticsegment.Member{{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"}})
	second := publishUSearchTestSegment(t, cache, profile,
		[]HNSWNode{{Ordinal: 0, Vector: []float32{0, 1}}},
		[]semanticsegment.Member{{Ordinal: 0, ChunkID: "second", Revision: 2, VectorHash: "second-hash"}})
	segments := []semanticsegment.RootSegment{
		{Hash: first.Hash, RelativePath: first.RelativePath},
		{Hash: second.Hash, RelativePath: second.RelativePath},
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Hash < segments[j].Hash })
	root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{
		DatabaseID: "db", ProfileID: profile, GenerationID: "root", SnapshotRevision: 2,
		Segments: segments,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	openCalls := 0
	openPayload := func(path string) (io.ReadCloser, error) {
		openCalls++
		if openCalls == 2 {
			cancel()
			return nil, ctx.Err()
		}
		return os.Open(path)
	}

	loaded, err := openUSearchRoot(ctx, cache, "db", profile, root.Manifest.GenerationID, USearchRootExpectations{
		Index: USearchOptions{Dimensions: 2}, SnapshotRevision: 2,
		BackendVersion: USearchVersion, DescriptorSHA256: root.Manifest.DescriptorSHA256,
	}, openPayload)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("open root error=%v want context cancellation", err)
	}
	if openCalls != 2 || loaded == nil || len(loaded.Segments) != 1 || loaded.Segments[0].Index != nil {
		t.Fatalf("open_calls=%d loaded=%#v want one cleaned-up index", openCalls, loaded)
	}
}

func TestOpenUSearchRootLaterPayloadErrorClosesLoadedIndexes(t *testing.T) {
	cache := t.TempDir()
	profile := "profile"
	first := publishUSearchTestSegment(t, cache, profile,
		[]HNSWNode{{Ordinal: 0, Vector: []float32{1, 0}}},
		[]semanticsegment.Member{{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"}})
	second := publishUSearchTestSegment(t, cache, profile,
		[]HNSWNode{{Ordinal: 0, Vector: []float32{0, 1}}},
		[]semanticsegment.Member{{Ordinal: 0, ChunkID: "second", Revision: 2, VectorHash: "second-hash"}})
	segments := []semanticsegment.RootSegment{
		{Hash: first.Hash, RelativePath: first.RelativePath},
		{Hash: second.Hash, RelativePath: second.RelativePath},
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Hash < segments[j].Hash })
	root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{
		DatabaseID: "db", ProfileID: profile, GenerationID: "root", SnapshotRevision: 2,
		Segments: segments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cache, filepath.FromSlash(segments[1].RelativePath), semanticsegment.PayloadFileName),
		[]byte("tampered"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	loaded, err := openUSearchRoot(context.Background(), cache, "db", profile, root.Manifest.GenerationID, USearchRootExpectations{
		Index: USearchOptions{Dimensions: 2}, SnapshotRevision: 2,
		BackendVersion: USearchVersion, DescriptorSHA256: root.Manifest.DescriptorSHA256,
	}, func(path string) (io.ReadCloser, error) {
		return os.Open(path)
	})
	if err == nil || !strings.Contains(err.Error(), "payload checksum mismatch") {
		t.Fatalf("open root error=%v want payload checksum mismatch", err)
	}
	if loaded == nil || len(loaded.Segments) != 1 || loaded.Segments[0].Index != nil {
		t.Fatalf("loaded=%#v want one cleaned-up index", loaded)
	}
}

func TestOpenUSearchRootRejectsChecksumValidSubsetBeforePayloadImport(t *testing.T) {
	cache := t.TempDir()
	profile := "profile"
	first := publishUSearchTestSegment(t, cache, profile,
		[]HNSWNode{{Ordinal: 0, Vector: []float32{1, 0}}},
		[]semanticsegment.Member{{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"}})
	second := publishUSearchTestSegment(t, cache, profile,
		[]HNSWNode{{Ordinal: 0, Vector: []float32{0, 1}}},
		[]semanticsegment.Member{{Ordinal: 0, ChunkID: "second", Revision: 2, VectorHash: "second-hash"}})
	segments := []semanticsegment.RootSegment{
		{Hash: first.Hash, RelativePath: first.RelativePath},
		{Hash: second.Hash, RelativePath: second.RelativePath},
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Hash < segments[j].Hash })
	root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{
		DatabaseID: "db", ProfileID: profile, GenerationID: "root", SnapshotRevision: 2,
		Segments: segments,
	})
	if err != nil {
		t.Fatal(err)
	}
	rewriteUSearchRootManifest(t, cache, root, func(manifest *semanticsegment.RootManifest) {
		manifest.Segments = append([]semanticsegment.RootSegment(nil), manifest.Segments[:1]...)
		hash, err := semanticsegment.RootDescriptorSHA256(semanticsegment.RootInput{
			DatabaseID: manifest.DatabaseID, ProfileID: manifest.ProfileID, GenerationID: manifest.GenerationID,
			SnapshotRevision: manifest.SnapshotRevision, PurgeEpoch: manifest.PurgeEpoch, Segments: manifest.Segments,
		})
		if err != nil {
			t.Fatal(err)
		}
		manifest.DescriptorSHA256 = hash
	})
	payloadOpens := 0
	loaded, err := openUSearchRoot(context.Background(), cache, "db", profile, root.Manifest.GenerationID, USearchRootExpectations{
		Index: USearchOptions{Dimensions: 2}, SnapshotRevision: 2,
		BackendVersion: USearchVersion, DescriptorSHA256: root.Manifest.DescriptorSHA256,
	}, func(path string) (io.ReadCloser, error) {
		payloadOpens++
		return os.Open(path)
	})
	if loaded != nil || err == nil || !strings.Contains(err.Error(), "descriptor hash mismatch") || payloadOpens != 0 {
		t.Fatalf("loaded=%#v error=%v payload_opens=%d", loaded, err, payloadOpens)
	}
}

func TestOpenUSearchRootRejectsMismatchedProvenance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*usearchRootFixtureOptions, *USearchRootExpectations)
		tamper func(*testing.T, string, usearchRootFixture)
		want   string
	}{
		{
			name: "root snapshot revision",
			mutate: func(_ *usearchRootFixtureOptions, expect *USearchRootExpectations) {
				expect.SnapshotRevision++
			},
			want: "snapshot revision mismatch",
		},
		{
			name: "root purge epoch",
			mutate: func(_ *usearchRootFixtureOptions, expect *USearchRootExpectations) {
				expect.PurgeEpoch++
			},
			want: "purge epoch mismatch",
		},
		{
			name: "segment backend",
			mutate: func(options *usearchRootFixtureOptions, _ *USearchRootExpectations) {
				options.backend = "exact"
			},
			want: "backend mismatch",
		},
		{
			name: "segment backend version",
			mutate: func(options *usearchRootFixtureOptions, _ *USearchRootExpectations) {
				options.backendVersion = "2.25.0"
			},
			want: "backend version mismatch",
		},
		{
			name: "segment metric",
			mutate: func(options *usearchRootFixtureOptions, _ *USearchRootExpectations) {
				options.distanceMetric = "dot"
			},
			want: "distance metric mismatch",
		},
		{
			name: "segment dimensions",
			mutate: func(options *usearchRootFixtureOptions, _ *USearchRootExpectations) {
				options.dimensions = 3
			},
			want: "dimensions mismatch",
		},
		{
			name: "membership checksum",
			tamper: func(t *testing.T, cache string, fixture usearchRootFixture) {
				rewriteUSearchSegmentManifest(t, cache, fixture.segment, func(manifest *semanticsegment.Manifest) {
					manifest.Members[0].ChunkID = "tampered"
				})
			},
			want: "membership checksum mismatch",
		},
		{
			name: "payload checksum",
			tamper: func(t *testing.T, cache string, fixture usearchRootFixture) {
				path := filepath.Join(cache, filepath.FromSlash(fixture.segment.RelativePath), semanticsegment.PayloadFileName)
				if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "payload checksum mismatch",
		},
		{
			name: "descriptor checksum",
			tamper: func(t *testing.T, cache string, fixture usearchRootFixture) {
				rewriteUSearchSegmentManifest(t, cache, fixture.segment, func(manifest *semanticsegment.Manifest) {
					manifest.DescriptorSHA256 = strings.Repeat("0", 64)
				})
			},
			want: "descriptor checksum mismatch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			options := usearchRootFixtureOptions{}
			expect := USearchRootExpectations{
				Index:            USearchOptions{Dimensions: 2},
				SnapshotRevision: 7,
				PurgeEpoch:       3,
				BackendVersion:   USearchVersion,
			}
			if tc.mutate != nil {
				tc.mutate(&options, &expect)
			}
			fixture := publishUSearchRootFixture(t, cache, "profile", options)
			expect.DescriptorSHA256 = fixture.root.Manifest.DescriptorSHA256
			if tc.tamper != nil {
				tc.tamper(t, cache, fixture)
			}

			loaded, err := OpenUSearchRoot(context.Background(), cache, "db", "profile", fixture.root.Manifest.GenerationID, expect)
			if loaded != nil {
				_ = loaded.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("OpenUSearchRoot error = %v, want %q", err, tc.want)
			}
		})
	}
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
	loaded, err := OpenUSearchRoot(context.Background(), cache, "db", profile, root.Manifest.GenerationID, USearchRootExpectations{
		Index:            USearchOptions{Dimensions: 2},
		SnapshotRevision: 4,
		PurgeEpoch:       0,
		BackendVersion:   USearchVersion,
		DescriptorSHA256: root.Manifest.DescriptorSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loaded.Close() })

	candidates, err := loaded.Candidates(context.Background(), []float32{1, 0}, 2)
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

	candidates, err := root.Candidates(context.Background(), []float32{1, 0}, 1)
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
	_, err = root.Candidates(context.Background(), []float32{1, 0}, 1)
	if err == nil || !strings.Contains(err.Error(), "outside immutable manifest") {
		t.Fatalf("err=%v", err)
	}
}

func TestUSearchRootCandidatesChecksCancellationImmediatelyAfterNativeSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root := &USearchRoot{
		Segments: []USearchRootSegment{{
			SegmentHash: "first",
			Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{
				{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"},
			}},
		}},
		searchSegment: func(_ *USearch, _ []float32, _ int) ([]HNSWHit, error) {
			cancel()
			return []HNSWHit{{Ordinal: 99}}, nil
		},
	}

	candidates, err := root.Candidates(ctx, []float32{1, 0}, 1)
	if len(candidates) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("candidates=%+v error=%v want immediate context cancellation", candidates, err)
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
		HNSWNode{Ordinal: 2, Vector: []float32{0.6, 0.8}},
	)
	root := &USearchRoot{
		Root: semanticsegment.Root{Manifest: semanticsegment.RootManifest{ProfileID: profileID, GenerationID: "generation", SnapshotRevision: 4, PurgeEpoch: 2}},
		Segments: []USearchRootSegment{{
			SegmentHash: "segment",
			Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{
				{Ordinal: 0, ChunkID: "near", Revision: 3, VectorHash: "near-hash"},
				{Ordinal: 1, ChunkID: "far", Revision: 4, VectorHash: "far-hash"},
				{Ordinal: 2, ChunkID: "deleted", Revision: 4, VectorHash: "deleted-hash"},
			}},
			Index: index,
		}},
	}
	t.Cleanup(func() { _ = root.Close() })
	st := &fakeUSearchCandidateStore{rows: []store.RetrievalEmbeddingRow{
		{ChunkID: "far", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), VectorHash: "far-hash", Revision: 4, ParentKind: "source", ParentSourceKey: "source:far", EvidenceRole: "raw", SourceType: "article", SectionOrdinal: 4, ProjectionVersion: "projection", ChunkerVersion: "chunker"},
		{ChunkID: "near", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), VectorHash: "near-hash", Revision: 3, ParentKind: "source", ParentSourceKey: "source:near", EvidenceRole: "raw", SourceType: "article", SectionOrdinal: 2, ProjectionVersion: "projection", ChunkerVersion: "chunker"},
	}, l0Rows: []store.RetrievalEmbeddingRow{{ChunkID: "l0", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{0.8, 0.6}), VectorHash: "l0-hash", Revision: 5, ParentKind: "source", ParentSourceKey: "source:l0", EvidenceRole: "raw", SourceType: "article", SectionOrdinal: 3, ProjectionVersion: "projection", ChunkerVersion: "chunker"}}}

	hits, status, err := NewUSearchCandidateSearcher(root, st).Search(context.Background(), []float32{1, 0}, SearchOptions{Profile: profile, Limit: 3, MaxChunks: 999})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateSearched || status.Backend != BackendUSearch || status.GenerationID != "generation" || !reflect.DeepEqual([]string{hits[0].ChunkID, hits[1].ChunkID, hits[2].ChunkID}, []string{"near", "l0", "far"}) {
		t.Fatalf("hits=%+v status=%+v", hits, status)
	}
	for _, hit := range hits {
		if hit.ChunkID == "deleted" {
			t.Fatalf("stale/deleted SQLite candidate survived exact validation: hits=%+v", hits)
		}
	}
	if st.request.ExpectedActiveGenerationID != "generation" || st.request.ExpectedPurgeEpoch != 2 || st.request.ExpectedActiveSnapshotRevision != 4 || len(st.request.Candidates) != 3 {
		t.Fatalf("candidate request=%+v", st.request)
	}
	if st.l0Request.ExpectedActiveGenerationID != "generation" || st.l0Request.ExpectedPurgeEpoch != 2 || st.l0Request.ExpectedActiveSnapshotRevision != 4 || st.l0Limit != store.RetrievalSegmentHardLimit {
		t.Fatalf("L0 request=%+v limit=%d", st.l0Request, st.l0Limit)
	}
	if st.snapshotStarts != 1 || st.snapshotCloses != 1 {
		t.Fatalf("snapshot starts=%d closes=%d", st.snapshotStarts, st.snapshotCloses)
	}
}

func TestUSearchCandidateSearcherCancellationDuringFirstNativeSegmentStopsTraversalAndExpansion(t *testing.T) {
	profile := embedding.Profile{
		Provider: "fake", Model: "model", Dimensions: 2,
		ProjectionVersion: "projection", ChunkerVersion: "chunker",
		Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2,
	}
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	nativeSearchCalls := 0
	root := &USearchRoot{
		Root: semanticsegment.Root{Manifest: semanticsegment.RootManifest{
			ProfileID: profileID, GenerationID: "generation", SnapshotRevision: 4, PurgeEpoch: 2,
		}},
		Segments: []USearchRootSegment{
			{
				SegmentHash: "first",
				Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{
					{Ordinal: 0, ChunkID: "first", Revision: 1, VectorHash: "first-hash"},
				}},
			},
			{
				SegmentHash: "second",
				Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{
					{Ordinal: 0, ChunkID: "second", Revision: 1, VectorHash: "second-hash"},
				}},
			},
		},
		searchSegment: func(_ *USearch, _ []float32, _ int) ([]HNSWHit, error) {
			nativeSearchCalls++
			cancel()
			return []HNSWHit{{Ordinal: 99}}, nil
		},
	}
	st := &fakeUSearchCandidateStore{}

	hits, status, err := NewUSearchCandidateSearcher(root, st).Search(ctx, []float32{1, 0}, SearchOptions{
		Profile: profile, Limit: 1, MaxChunks: 999,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("search error=%v want context cancellation", err)
	}
	if len(hits) != 0 || status.Reason != ReasonCanceled {
		t.Fatalf("hits=%+v status=%+v want canceled with no partial hits", hits, status)
	}
	if nativeSearchCalls != 1 {
		t.Fatalf("native searches=%d want only the first segment and no expansion", nativeSearchCalls)
	}
	if st.candidateReads != 0 {
		t.Fatalf("SQLite native candidate reads=%d want none after native cancellation", st.candidateReads)
	}
	if st.snapshotStarts != 1 || st.snapshotCloses != 1 {
		t.Fatalf("snapshot starts=%d closes=%d", st.snapshotStarts, st.snapshotCloses)
	}
}

func TestUSearchCandidateSearcherAcceptsStaleRootMemberReplacedInExactL0(t *testing.T) {
	profile := embedding.Profile{Provider: "fake", Model: "model", Dimensions: 2, ProjectionVersion: "projection", ChunkerVersion: "chunker", Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	root := &USearchRoot{Root: semanticsegment.Root{Manifest: semanticsegment.RootManifest{ProfileID: profileID, GenerationID: "generation", SnapshotRevision: 4, PurgeEpoch: 2}}, Segments: []USearchRootSegment{{
		SegmentHash: "segment",
		Manifest: semanticsegment.Manifest{Members: []semanticsegment.Member{
			{Ordinal: 0, ChunkID: "stale", Revision: 3, VectorHash: "old-hash"},
			{Ordinal: 1, ChunkID: "current", Revision: 4, VectorHash: "current-hash"},
		}},
		Index: newUSearchRootTestIndex(t, HNSWNode{Ordinal: 0, Vector: []float32{1, 0}}, HNSWNode{Ordinal: 1, Vector: []float32{0, 1}}),
	}}}
	t.Cleanup(func() { _ = root.Close() })
	st := &fakeUSearchCandidateStore{rows: []store.RetrievalEmbeddingRow{{ChunkID: "current", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), VectorHash: "current-hash", Revision: 4, ParentKind: "source", ParentSourceKey: "source:current", EvidenceRole: "raw", SourceType: "article", ProjectionVersion: "projection", ChunkerVersion: "chunker"}}, l0Rows: []store.RetrievalEmbeddingRow{{ChunkID: "stale", ProfileID: profileID, Provider: "fake", Model: "model", Dimensions: 2, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), VectorHash: "new-hash", Revision: 5, ParentKind: "source", ParentSourceKey: "source:stale", EvidenceRole: "raw", SourceType: "article", ProjectionVersion: "projection", ChunkerVersion: "chunker"}}}
	hits, status, err := NewUSearchCandidateSearcher(root, st).Search(context.Background(), []float32{1, 0}, SearchOptions{Profile: profile, Limit: 2, MaxChunks: 999})
	if err != nil || status.State != StateSearched || !reflect.DeepEqual([]string{hits[0].ChunkID, hits[1].ChunkID}, []string{"stale", "current"}) {
		t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
	}
}

func TestNativeCandidateStagesFollowBoundedExpansionContract(t *testing.T) {
	if got := nativeCandidateStages(1); !reflect.DeepEqual(got, []int{200, 500, 2000}) {
		t.Fatalf("stages=%v", got)
	}
	if got := nativeCandidatesPerSegment(500, 8); got != 63 {
		t.Fatalf("per-segment candidates=%d", got)
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
	segment, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{DatabaseID: "db", ProfileID: profile, Backend: BackendUSearch, BackendVersion: USearchVersion, DistanceMetric: "cosine", Dimensions: 2, Members: members, Payload: func(w io.Writer) error { _, err := w.Write(payload.Bytes()); return err }})
	if err != nil {
		t.Fatal(err)
	}
	return segment
}

type usearchRootFixtureOptions struct {
	backend        string
	backendVersion string
	distanceMetric string
	dimensions     int
}

type usearchRootFixture struct {
	root    semanticsegment.Root
	segment semanticsegment.Segment
}

func publishUSearchRootFixture(t *testing.T, cache, profile string, options usearchRootFixtureOptions) usearchRootFixture {
	t.Helper()
	if options.backend == "" {
		options.backend = BackendUSearch
	}
	if options.backendVersion == "" {
		options.backendVersion = USearchVersion
	}
	if options.distanceMetric == "" {
		options.distanceMetric = "cosine"
	}
	if options.dimensions == 0 {
		options.dimensions = 2
	}
	index := newUSearchRootTestIndex(t, HNSWNode{Ordinal: 0, Vector: []float32{1, 0}})
	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		_ = index.Close()
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	segment, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{
		DatabaseID: "db", ProfileID: profile, Backend: options.backend, BackendVersion: options.backendVersion,
		DistanceMetric: options.distanceMetric, Dimensions: options.dimensions,
		Members: []semanticsegment.Member{{Ordinal: 0, ChunkID: "chunk", Revision: 1, VectorHash: "hash"}},
		Payload: func(w io.Writer) error { _, err := w.Write(payload.Bytes()); return err },
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{
		DatabaseID: "db", ProfileID: profile, GenerationID: "root", SnapshotRevision: 7, PurgeEpoch: 3,
		Segments: []semanticsegment.RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return usearchRootFixture{root: root, segment: segment}
}

func rewriteUSearchSegmentManifest(t *testing.T, cache string, segment semanticsegment.Segment, mutate func(*semanticsegment.Manifest)) {
	t.Helper()
	path := filepath.Join(cache, filepath.FromSlash(segment.RelativePath), semanticsegment.ManifestFileName)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest semanticsegment.Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	contents, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rewriteUSearchRootManifest(t *testing.T, cache string, root semanticsegment.Root, mutate func(*semanticsegment.RootManifest)) {
	t.Helper()
	path := filepath.Join(cache, filepath.FromSlash(root.RelativePath), semanticsegment.RootFileName)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest semanticsegment.RootManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	contents, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

type cancelAfterFirstChunkReader struct {
	payload []byte
	cancel  context.CancelFunc
	read    bool
}

func (r *cancelAfterFirstChunkReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	count := min(len(buffer), min(len(r.payload), 64))
	copy(buffer, r.payload[:count])
	r.payload = r.payload[count:]
	r.cancel()
	return count, nil
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
	request        store.RetrievalNativeCandidateRequest
	l0Request      store.RetrievalActiveRootReadRequest
	l0Limit        int
	rows           []store.RetrievalEmbeddingRow
	l0Rows         []store.RetrievalEmbeddingRow
	err            error
	candidateReads int
	snapshotStarts int
	snapshotCloses int
}

func (f *fakeUSearchCandidateStore) BeginRetrievalNativeReadSnapshot(context.Context) (store.RetrievalNativeReadSession, error) {
	f.snapshotStarts++
	return &fakeUSearchCandidateReadSession{store: f}, nil
}

type fakeUSearchCandidateReadSession struct{ store *fakeUSearchCandidateStore }

func (s *fakeUSearchCandidateReadSession) Close() error {
	s.store.snapshotCloses++
	return nil
}

func (s *fakeUSearchCandidateReadSession) ReadRetrievalExactL0(ctx context.Context, request store.RetrievalActiveRootReadRequest, limit int) ([]store.RetrievalEmbeddingRow, error) {
	return s.store.ReadRetrievalExactL0(ctx, request, limit)
}

func (s *fakeUSearchCandidateReadSession) ReadRetrievalNativeCandidates(ctx context.Context, request store.RetrievalNativeCandidateRequest) ([]store.RetrievalEmbeddingRow, error) {
	return s.store.ReadRetrievalNativeCandidates(ctx, request)
}

func (f *fakeUSearchCandidateStore) ReadRetrievalExactL0(_ context.Context, request store.RetrievalActiveRootReadRequest, limit int) ([]store.RetrievalEmbeddingRow, error) {
	f.l0Request, f.l0Limit = request, limit
	return append([]store.RetrievalEmbeddingRow(nil), f.l0Rows...), nil
}

func (f *fakeUSearchCandidateStore) ReadRetrievalNativeCandidates(_ context.Context, request store.RetrievalNativeCandidateRequest) ([]store.RetrievalEmbeddingRow, error) {
	f.candidateReads++
	f.request = request
	return append([]store.RetrievalEmbeddingRow(nil), f.rows...), f.err
}
