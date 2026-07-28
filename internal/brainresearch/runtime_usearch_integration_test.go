//go:build usearch && cgo

package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

func TestRuntimeSemanticSearcherRejectsPostReadinessCancellationBeforeRootWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snapshot := semanticreadiness.Snapshot{
		ActiveGenerationID:                   "root",
		ProfileID:                            "profile",
		ActiveSnapshotRevision:               1,
		ProfilePurgeEpoch:                    0,
		ActiveGenerationBackendVersion:       semanticindex.USearchVersion,
		ActiveGenerationRootDescriptorSHA256: "unused-after-cancellation",
	}

	searcher, err := runtimeSemanticSearcher(
		ctx, nil, config.Config{CacheDir: filepath.Join(t.TempDir(), "missing")},
		embedding.Profile{Dimensions: 2}, snapshot, semanticreadiness.DefaultExactMaxChunks,
	)
	if searcher != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("searcher=%#v error=%v want context cancellation", searcher, err)
	}
}

func TestRuntimeUSearchIntegration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	profile := semanticbuild.Profile(embedding.Info{
		Provider: "fake", Model: "fake-v1", Dimensions: 2,
	})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	for index, text := range []string{"nearest semantic evidence", "distant semantic evidence"} {
		sourceKey := fmt.Sprintf("source:runtime-%d", index)
		url := fmt.Sprintf("https://example.com/runtime-%d", index)
		upserted, err := st.UpsertSource(ctx, model.SourceCandidate{
			OriginalURL: url, CanonicalURL: url, NormalizedURL: url,
			SourceType: "article", Domain: "example.com",
			SourceKey: sourceKey, NotePath: sourceKey + ".md",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveSourceExtraction(ctx, upserted.SourceID, model.ExtractResult{
			CanonicalURL: url, FinalURL: url, Title: sourceKey,
			Content: text, Status: "ok", FetchedAt: time.Now().UTC(),
			Tool: "test", ToolVersion: "1",
		}, "content-"+sourceKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := semanticbuild.RunChunk(ctx, st, semanticbuild.ChunkOptions{
		Limit: 10, UntilIdle: true,
	}); err != nil {
		t.Fatal(err)
	}
	chunks, err := st.ListChunksNeedingEmbeddingForProfileAt(
		ctx, profile, "", 10, time.Now().UTC(),
	)
	if err != nil || len(chunks) != 2 {
		t.Fatalf("chunks=%+v err=%v", chunks, err)
	}
	chunksBySource := make(map[string]store.RetrievalChunkRow, len(chunks))
	for _, chunk := range chunks {
		chunksBySource[chunk.ParentSourceKey] = chunk
	}
	initialVectors := map[string][]float32{
		"source:runtime-0": {0.8, 0.6},
		"source:runtime-1": {0, 1},
	}
	for sourceKey, vector := range initialVectors {
		chunk, ok := chunksBySource[sourceKey]
		if !ok {
			t.Fatalf("missing chunk for %s: %+v", sourceKey, chunks)
		}
		if err := st.PutRetrievalEmbedding(ctx, store.RetrievalEmbeddingRow{
			ChunkID: chunk.ChunkID, ProfileID: profileID,
			Provider: profile.Provider, Model: profile.Model,
			Dimensions:     profile.Dimensions,
			Representation: profile.Representation,
			Normalization:  profile.Normalization,
			VectorBytes:    embedding.EncodeDenseF32(vector),
			ChunkTextHash:  chunk.ChunkTextHash,
			Status:         store.RetrievalEmbeddingReady,
			AttemptCount:   1, EmbeddedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	builder, err := semanticbuild.NewUSearchSegmentBuilder(
		semanticbuild.USearchSegmentBuilderOptions{Dimensions: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	window, err := st.NextRetrievalFlushWindow(ctx, profileID, 10)
	if err != nil || len(window.Rows) != 2 {
		t.Fatalf("flush window=%+v err=%v", window, err)
	}
	payload, err := builder.Build(ctx, window.Rows)
	if err != nil {
		t.Fatal(err)
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members := make([]semanticsegment.Member, 0, len(window.Rows))
	storeMembers := make([]store.RetrievalIndexSegmentMember, 0, len(window.Rows))
	for ordinal, row := range window.Rows {
		member := semanticsegment.Member{
			Ordinal: uint64(ordinal), ChunkID: row.ChunkID,
			Revision: row.Revision, VectorHash: row.VectorHash,
		}
		members = append(members, member)
	}
	segment, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{
		DatabaseID: databaseID, ProfileID: profileID,
		Backend: semanticindex.BackendUSearch, BackendVersion: semanticindex.USearchVersion,
		DistanceMetric: "cosine", Dimensions: profile.Dimensions,
		Members: members, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		storeMembers = append(storeMembers, store.RetrievalIndexSegmentMember{
			SegmentHash: segment.Hash, Ordinal: member.Ordinal, ChunkID: member.ChunkID,
			Revision: member.Revision, VectorHash: member.VectorHash,
		})
	}
	const generationID = "runtime-usearch-generation"
	publishedRoot, err := semanticsegment.PublishRoot(cache, semanticsegment.RootInput{
		DatabaseID: databaseID, ProfileID: profileID, GenerationID: generationID,
		SnapshotRevision: window.SnapshotRevision, PurgeEpoch: window.Profile.PurgeEpoch,
		Segments: []semanticsegment.RootSegment{{
			Hash: segment.Hash, RelativePath: segment.RelativePath,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CompleteRetrievalIndexGeneration(ctx, store.CompleteRetrievalIndexGenerationInput{
		Generation: store.RetrievalIndexGenerationRow{
			GenerationID: generationID, ProfileID: profileID,
			Backend: semanticindex.BackendUSearch, BackendVersion: semanticindex.USearchVersion,
			Dimensions: profile.Dimensions, DistanceMetric: "cosine",
			IndexedChunkCount: len(members), SourceManifestHash: publishedRoot.Manifest.DescriptorSHA256,
			BuildStatus: store.RetrievalGenerationCompleted, RelativeCachePath: publishedRoot.RelativePath,
			BuildStartedAt: now, BuildCompletedAt: now,
		},
		Segments: []store.RetrievalIndexSegmentRow{{
			SegmentHash: segment.Hash, ProfileID: profileID,
			Backend: semanticindex.BackendUSearch, BackendVersion: semanticindex.USearchVersion,
			Dimensions: profile.Dimensions, DistanceMetric: "cosine",
			IndexedChunkCount: len(members), RelativeCachePath: segment.RelativePath,
			MembershipHash: segment.Manifest.MembersSHA256, PayloadHash: segment.Manifest.PayloadSHA256,
			ManifestHash: segment.Manifest.DescriptorSHA256,
		}},
		Members: storeMembers, SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID:     window.Profile.ActiveGenerationID,
		ExpectedPurgeEpoch:             window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision,
		ActivationMode:                 store.RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}

	const expectedNearestSourceKey = "source:runtime-1"
	expectedNearestChunk := chunksBySource[expectedNearestSourceKey]
	snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(
		ctx, profile, semanticreadiness.DefaultExactMaxChunks, time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Configured, snapshot.Enabled = true, true
	if decision := semanticreadiness.Evaluate(snapshot); decision.State != semanticreadiness.StateReady {
		t.Fatalf("decision=%+v snapshot=%+v", decision, snapshot)
	}
	if !snapshot.ActiveGenerationValid ||
		snapshot.ActiveGenerationID != generationID ||
		snapshot.ActiveGenerationBackend != semanticindex.BackendUSearch ||
		snapshot.ActiveGenerationBackendVersion != semanticindex.USearchVersion ||
		snapshot.ActiveGenerationDistanceMetric != "cosine" ||
		snapshot.ActiveGenerationDimensions != profile.Dimensions ||
		snapshot.ActiveGenerationRootDescriptorSHA256 != publishedRoot.Manifest.DescriptorSHA256 {
		t.Fatalf("invalid active generation provenance: %+v", snapshot)
	}
	if ok, reason := semanticindex.RuntimeCapability().Admit(
		snapshot.ActiveGenerationBackend,
		snapshot.ActiveGenerationBackendVersion,
	); !ok {
		t.Fatalf("runtime capability rejected active root: %s", reason)
	}
	searcher, err := runtimeSemanticSearcher(
		ctx, st, config.Config{CacheDir: cache}, profile, snapshot,
		semanticreadiness.DefaultExactMaxChunks,
	)
	if err != nil {
		t.Fatal(err)
	}
	closeable, ok := searcher.(interface{ Close() error })
	if !ok {
		t.Fatalf("runtime searcher %T is not closeable", searcher)
	}
	t.Cleanup(func() {
		if err := closeable.Close(); err != nil {
			t.Errorf("close runtime searcher: %v", err)
		}
	})

	// Move the formerly distant vector into exact L0 after runtime admission,
	// as a concurrent refresh may do. Native candidates still supply the other
	// current root member, while SQLite validation and exact reranking must
	// discard the stale root member and put this current L0 row first.
	if err := st.PutRetrievalEmbedding(ctx, store.RetrievalEmbeddingRow{
		ChunkID: expectedNearestChunk.ChunkID, ProfileID: profileID,
		Provider: profile.Provider, Model: profile.Model,
		Dimensions:     profile.Dimensions,
		Representation: profile.Representation,
		Normalization:  profile.Normalization,
		VectorBytes:    embedding.EncodeDenseF32([]float32{1, 0}),
		ChunkTextHash:  expectedNearestChunk.ChunkTextHash,
		Status:         store.RetrievalEmbeddingReady,
		AttemptCount:   2, EmbeddedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	hits, status, err := searcher.Search(ctx, []float32{1, 0}, semanticindex.SearchOptions{
		Profile: profile, Limit: 2,
		MaxChunks: semanticreadiness.DefaultExactMaxChunks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != semanticindex.StateSearched ||
		status.Backend != semanticindex.BackendUSearch ||
		status.GenerationID != generationID ||
		len(hits) != 2 ||
		hits[0].ChunkID != expectedNearestChunk.ChunkID ||
		hits[0].ParentSourceKey != expectedNearestSourceKey ||
		hits[0].Distance != 0 ||
		hits[0].Distance >= hits[1].Distance {
		t.Fatalf("hits=%+v status=%+v", hits, status)
	}
}
