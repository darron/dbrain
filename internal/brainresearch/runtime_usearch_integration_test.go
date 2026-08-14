//go:build usearch && cgo

package brainresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticgc"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticruntime"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

type taggedRuntimeProvider struct{ info embedding.Info }

func (p *taggedRuntimeProvider) Info() embedding.Info { return p.info }
func (p *taggedRuntimeProvider) Embed(_ context.Context, _ embedding.Request) (embedding.Response, error) {
	return embedding.Response{Vectors: [][]float32{{1, 0}}, Provider: p.info.Provider, Model: p.info.Model, Dimensions: p.info.Dimensions}, nil
}

type blockingTaggedSearcher struct {
	started chan struct{}
	unblock chan struct{}
	closed  chan struct{}
}

func (s *blockingTaggedSearcher) Search(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	close(s.started)
	<-s.unblock
	return []semanticindex.Hit{}, semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendUSearch}, nil
}

type taggedSnapshotSource struct {
	mu       sync.Mutex
	snapshot semanticreadiness.Snapshot
	err      error
}

func (s *taggedSnapshotSource) read(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot, s.err
}

func (s *taggedSnapshotSource) update(fn func(*semanticreadiness.Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.snapshot)
}

func (s *taggedSnapshotSource) fail(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

type taggedRuntimeFixture struct {
	runtime *Runtime
	builder *Builder
	store   *store.Store
	source  *taggedSnapshotSource
	cache   string
}

type nonRetainableTaggedLease struct{ closes atomic.Int32 }

func (l *nonRetainableTaggedLease) Close() error {
	l.closes.Add(1)
	return nil
}

type taggedSemanticGCCatalog struct {
	plan store.RetrievalSemanticGCPlan
}

func (c taggedSemanticGCCatalog) PlanRetrievalSemanticGC(context.Context, store.RetrievalSemanticGCOptions) (store.RetrievalSemanticGCPlan, error) {
	return c.plan, nil
}

func (c taggedSemanticGCCatalog) PruneRetrievalSemanticCatalog(context.Context, store.RetrievalSemanticGCOptions) (store.RetrievalSemanticGCPlan, error) {
	return c.plan, nil
}

func (taggedSemanticGCCatalog) VacuumRetrievalDatabase(context.Context) error { return nil }

func newTaggedRuntimeFixture(t *testing.T, mode semanticconfig.Mode, configure func(*runtimeDeps)) taggedRuntimeFixture {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, "private-semantic-cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeSemanticConfig(t, root, string(mode), "embed-model", 2)
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	profile := semanticbuild.Profile(embedding.Info{Provider: "ollama", Model: "embed-model", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runtimeReadySnapshot(true)
	snapshot.ProfileID = profileID
	snapshot.ActiveGenerationRootDescriptorSHA256 = strings.Repeat("a", 64)
	source := &taggedSnapshotSource{snapshot: snapshot}
	deps := runtimeDeps{
		readiness: source.read,
		capability: func() semanticindex.Capability {
			return semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion}
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return &taggedRuntimeProvider{info: embedding.Info{Provider: "ollama", Model: "embed-model", Dimensions: 2}}, nil
		},
	}
	if configure != nil {
		configure(&deps)
	}
	runtime := newRuntimeWithDeps(config.Config{RootDir: root, CacheDir: cache}, st, deps)
	builder, err := runtime.NewBuilderContext(t.Context(), mode, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = builder.Close()
		_ = runtime.Close()
	})
	return taggedRuntimeFixture{runtime: runtime, builder: builder, store: st, source: source, cache: cache}
}

func stubRuntimeRootOpen(t *testing.T, open func(context.Context) (*semanticindex.USearchRoot, error)) {
	t.Helper()
	original := openRuntimeUSearchRoot
	openRuntimeUSearchRoot = func(ctx context.Context, _, _, _, _ string, _ semanticindex.USearchRootExpectations) (*semanticindex.USearchRoot, error) {
		return open(ctx)
	}
	t.Cleanup(func() { openRuntimeUSearchRoot = original })
}

func retrieveTagged(t *testing.T, fixture taggedRuntimeFixture) semanticindex.Status {
	t.Helper()
	_, status, err := fixture.builder.semanticRetriever.Retrieve(t.Context(), "query", fixture.builder.semanticOptions)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestRuntimeLazySearcherLoadsRootOnFirstSearch(t *testing.T) {
	var loads atomic.Int32
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		loads.Add(1)
		return &semanticindex.USearchRoot{}, nil
	})
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, nil)
	if loads.Load() != 0 {
		t.Fatalf("root loads during admission=%d want=0", loads.Load())
	}
	_ = retrieveTagged(t, fixture)
	if loads.Load() != 1 {
		t.Fatalf("root loads after first search=%d want=1", loads.Load())
	}
}

func TestRuntimeLazySearcherReusesWarmRoot(t *testing.T) {
	var loads atomic.Int32
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		loads.Add(1)
		return &semanticindex.USearchRoot{}, nil
	})
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, nil)
	_ = retrieveTagged(t, fixture)
	_ = retrieveTagged(t, fixture)
	if loads.Load() != 1 {
		t.Fatalf("warm root loads=%d want=1", loads.Load())
	}
}

func TestRuntimeLazySearcherRejectsMismatchedRoot(t *testing.T) {
	var fixture taggedRuntimeFixture
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		fixture.source.update(func(snapshot *semanticreadiness.Snapshot) {
			snapshot.ActiveGenerationID = "replacement-root"
			snapshot.ActiveGenerationRootDescriptorSHA256 = strings.Repeat("b", 64)
		})
		return &semanticindex.USearchRoot{}, nil
	})
	fixture = newTaggedRuntimeFixture(t, semanticconfig.ModeOn, nil)
	status := retrieveTagged(t, fixture)
	if status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonNativeRootArtifactsUnavailable {
		t.Fatalf("status=%+v", status)
	}
}

func TestRuntimeMismatchedRootCloseErrorIsSurfacedByShutdown(t *testing.T) {
	closeFailure := errors.New("synthetic discarded root close failure")
	originalClose := closeRuntimeUSearchSearcher
	closeRuntimeUSearchSearcher = func(*semanticindex.USearchCandidateSearcher) error {
		return closeFailure
	}
	t.Cleanup(func() { closeRuntimeUSearchSearcher = originalClose })

	var fixture taggedRuntimeFixture
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		fixture.source.update(func(snapshot *semanticreadiness.Snapshot) {
			snapshot.ActiveGenerationID = "replacement-root"
			snapshot.ActiveGenerationRootDescriptorSHA256 = strings.Repeat("b", 64)
		})
		return &semanticindex.USearchRoot{}, nil
	})
	fixture = newTaggedRuntimeFixture(t, semanticconfig.ModeOn, nil)
	status := retrieveTagged(t, fixture)
	if status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonNativeRootArtifactsUnavailable {
		t.Fatalf("status=%+v", status)
	}
	if err := fixture.builder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.Shutdown(t.Context()); !errors.Is(err, closeFailure) {
		t.Fatalf("shutdown error=%v want discarded-root close failure", err)
	}
}

func TestRuntimeColdLoadRequiresRetainableGenerationLease(t *testing.T) {
	var loads atomic.Int32
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, func(deps *runtimeDeps) {
		deps.rootLoader = func(context.Context, *Runtime, semanticruntime.RootKey) (semanticruntime.LoadedSearcher, error) {
			loads.Add(1)
			return semanticruntime.LoadedSearcher{}, errors.New("loader must not run")
		}
	})
	profile := fixture.builder.semanticOptions.Profile
	searcher, err := runtimeSemanticSearcher(
		t.Context(), fixture.runtime, profile, fixture.source.snapshot,
		fixture.builder.semanticOptions.MaxChunks,
	)
	if err != nil {
		t.Fatal(err)
	}
	lease := &nonRetainableTaggedLease{}
	retriever := researchsemantic.NewWithGenerationLease(
		&taggedRuntimeProvider{info: embedding.Info{Provider: profile.Provider, Model: profile.Model, Dimensions: profile.Dimensions}},
		searcher,
		fixture.store,
		func(context.Context) (researchsemantic.GenerationLease, error) { return lease, nil },
	)
	docs, status, err := retriever.Retrieve(t.Context(), "query", fixture.builder.semanticOptions)
	if err != nil || len(docs) != 0 || status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonNativeRootArtifactsUnavailable {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
	if loads.Load() != 0 {
		t.Fatalf("loader calls=%d want=0", loads.Load())
	}
	if lease.closes.Load() != 1 {
		t.Fatalf("generation lease closes=%d want=1", lease.closes.Load())
	}
}

func TestRuntimeRootLoadWaitTimeoutFailsOpenWithExplicitReason(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		close(started)
		<-unblock
		return &semanticindex.USearchRoot{}, nil
	})
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, func(deps *runtimeDeps) {
		deps.rootCache = func(loader semanticruntime.Loader, _ time.Duration) *semanticruntime.Manager {
			return semanticruntime.New(loader, 20*time.Millisecond)
		}
	})
	status := retrieveTagged(t, fixture)
	if status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonRootLoadTimeout {
		t.Fatalf("status=%+v", status)
	}
	select {
	case <-started:
	default:
		t.Fatal("detached root load did not start")
	}
	close(unblock)
}

func TestRuntimeReadinessFailureFailsOpenWithPathFreeReason(t *testing.T) {
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, nil)
	fixture.source.fail(fmt.Errorf("read %s: synthetic failure", fixture.cache))
	status := retrieveTagged(t, fixture)
	if status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonRuntimeReadinessUnavailable || strings.Contains(string(status.Reason), fixture.cache) {
		t.Fatalf("status=%+v", status)
	}
}

func TestRuntimeDetachedLoadRetainsGenerationLease(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		close(started)
		<-unblock
		return &semanticindex.USearchRoot{}, nil
	})
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, func(deps *runtimeDeps) {
		deps.rootCache = func(loader semanticruntime.Loader, _ time.Duration) *semanticruntime.Manager {
			return semanticruntime.New(loader, 20*time.Millisecond)
		}
	})
	status := retrieveTagged(t, fixture)
	if status.Reason != semanticindex.ReasonRootLoadTimeout {
		t.Fatalf("status=%+v", status)
	}
	<-started
	databaseID, err := fixture.store.RetrievalDatabaseID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := semanticlock.NewScope(fixture.cache, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	profileID, err := fixture.builder.semanticOptions.Profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	const artifactID = "detached-load-artifact"
	relativeArtifact := filepath.ToSlash(filepath.Join("semantic", databaseID, profileID, "generations", artifactID))
	artifactPath := filepath.Join(fixture.cache, filepath.FromSlash(relativeArtifact))
	if err := os.MkdirAll(artifactPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactPath, "root.usearch"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	maintenance, err := scope.AcquireMaintenanceExclusive(t.Context(), "owner=test-refresh\n")
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancelBlocked := context.WithTimeout(t.Context(), 25*time.Millisecond)
	if generation, err := maintenance.AcquireGenerationExclusive(blockedCtx, "owner=test-refresh\n"); generation != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("generation=%#v error=%v want retained load guard", generation, err)
	}
	cancelBlocked()
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}

	gcDone := make(chan error, 1)
	go func() {
		_, gcErr := semanticgc.Run(t.Context(), taggedSemanticGCCatalog{plan: store.RetrievalSemanticGCPlan{
			CatalogProfiles: []string{profileID},
			PrunableGenerations: []store.RetrievalSemanticGCArtifact{{
				ID: artifactID, ProfileID: profileID, RelativeCachePath: relativeArtifact,
			}},
		}}, fixture.cache, databaseID, semanticgc.Options{
			Now: time.Now().UTC(), Apply: true, LockTimeout: time.Second,
		})
		gcDone <- gcErr
	}()
	select {
	case gcErr := <-gcDone:
		t.Fatalf("semantic GC completed while detached load retained artifacts: %v", gcErr)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("retained artifact unavailable before load completion: %v", err)
	}

	close(unblock)
	select {
	case gcErr := <-gcDone:
		if gcErr != nil {
			t.Fatal(gcErr)
		}
	case <-time.After(time.Second):
		t.Fatal("semantic GC did not become eligible after detached load released artifacts")
	}
	if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact stat error=%v want deleted after retained load completed", err)
	}
}

func TestRuntimeBlockedNativeSearchShutdownWaitsForSearch(t *testing.T) {
	searcher := &blockingTaggedSearcher{started: make(chan struct{}), unblock: make(chan struct{}), closed: make(chan struct{})}
	fixture := newTaggedRuntimeFixture(t, semanticconfig.ModeOn, func(deps *runtimeDeps) {
		deps.rootLoader = func(context.Context, *Runtime, semanticruntime.RootKey) (semanticruntime.LoadedSearcher, error) {
			return semanticruntime.LoadedSearcher{Searcher: searcher, Close: func() error { close(searcher.closed); return nil }}, nil
		}
	})
	retrieveDone := make(chan error, 1)
	go func() {
		_, _, err := fixture.builder.semanticRetriever.Retrieve(t.Context(), "query", fixture.builder.semanticOptions)
		retrieveDone <- err
	}()
	select {
	case <-searcher.started:
	case <-time.After(time.Second):
		t.Fatal("native search did not start")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancelShutdown()
	if err := fixture.runtime.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v want deadline", err)
	}
	select {
	case <-searcher.closed:
		t.Fatal("shutdown closed native searcher while search was active")
	default:
	}
	close(searcher.unblock)
	if err := <-retrieveDone; err != nil {
		t.Fatal(err)
	}
	if err := fixture.builder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-searcher.closed:
	default:
		t.Fatal("native searcher did not close after search and builder drained")
	}
}

func TestRuntimeUSearchIntegrationLazyLoadHydratesAndReusesRoot(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile := semanticbuild.Profile(embedding.Info{Provider: "ollama", Model: "embed-model", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	const sourceCount = 50
	for index := 0; index < sourceCount; index++ {
		text := fmt.Sprintf("filler semantic evidence %d", index)
		switch index {
		case 0:
			text = "nearest semantic evidence"
		case 1:
			text = "distant semantic evidence"
		}
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
	if _, err := semanticbuild.RunChunk(ctx, st, semanticbuild.ChunkOptions{Limit: 10, UntilIdle: true}); err != nil {
		t.Fatal(err)
	}
	chunks, err := st.ListChunksNeedingEmbeddingForProfileAt(ctx, profile, "", sourceCount, time.Now().UTC())
	if err != nil || len(chunks) != sourceCount {
		t.Fatalf("chunks=%+v err=%v", chunks, err)
	}
	chunksBySource := make(map[string]store.RetrievalChunkRow, len(chunks))
	for _, chunk := range chunks {
		chunksBySource[chunk.ParentSourceKey] = chunk
	}
	for sourceKey, chunk := range chunksBySource {
		vector := []float32{-1, 0}
		if sourceKey == "source:runtime-0" {
			vector = []float32{0.8, 0.6}
		} else if sourceKey == "source:runtime-1" {
			vector = []float32{0, 1}
		}
		if err := st.PutRetrievalEmbedding(ctx, store.RetrievalEmbeddingRow{
			ChunkID: chunk.ChunkID, ProfileID: profileID,
			Provider: profile.Provider, Model: profile.Model,
			Dimensions: profile.Dimensions, Representation: profile.Representation, Normalization: profile.Normalization,
			VectorBytes: embedding.EncodeDenseF32(vector), ChunkTextHash: chunk.ChunkTextHash,
			Status: store.RetrievalEmbeddingReady, AttemptCount: 1, EmbeddedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	segmentBuilder, err := semanticbuild.NewUSearchSegmentBuilder(semanticbuild.USearchSegmentBuilderOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	window, err := st.NextRetrievalFlushWindow(ctx, profileID, sourceCount)
	if err != nil || len(window.Rows) != sourceCount {
		t.Fatalf("flush window=%+v err=%v", window, err)
	}
	payload, err := segmentBuilder.Build(ctx, window.Rows)
	if err != nil {
		t.Fatal(err)
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members := make([]semanticsegment.Member, 0, len(window.Rows))
	for ordinal, row := range window.Rows {
		members = append(members, semanticsegment.Member{
			Ordinal: uint64(ordinal), ChunkID: row.ChunkID, Revision: row.Revision, VectorHash: row.VectorHash,
		})
	}
	segment, err := semanticsegment.PublishSegment(cache, semanticsegment.SegmentInput{
		DatabaseID: databaseID, ProfileID: profileID,
		Backend: semanticindex.BackendUSearch, BackendVersion: semanticindex.USearchVersion,
		DistanceMetric: "cosine", Dimensions: profile.Dimensions, Members: members, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	storeMembers := make([]store.RetrievalIndexSegmentMember, 0, len(members))
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
		Segments: []semanticsegment.RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CompleteRetrievalIndexGeneration(ctx, store.CompleteRetrievalIndexGenerationInput{
		Generation: store.RetrievalIndexGenerationRow{
			GenerationID: generationID, ProfileID: profileID,
			Backend: semanticindex.BackendUSearch, BackendVersion: semanticindex.USearchVersion,
			Dimensions: profile.Dimensions, DistanceMetric: "cosine", IndexedChunkCount: len(members),
			SourceManifestHash: publishedRoot.Manifest.DescriptorSHA256,
			BuildStatus:        store.RetrievalGenerationCompleted, RelativeCachePath: publishedRoot.RelativePath,
			BuildStartedAt: now, BuildCompletedAt: now,
		},
		Segments: []store.RetrievalIndexSegmentRow{{
			SegmentHash: segment.Hash, ProfileID: profileID,
			Backend: semanticindex.BackendUSearch, BackendVersion: semanticindex.USearchVersion,
			Dimensions: profile.Dimensions, DistanceMetric: "cosine", IndexedChunkCount: len(members),
			RelativeCachePath: segment.RelativePath, MembershipHash: segment.Manifest.MembersSHA256,
			PayloadHash: segment.Manifest.PayloadSHA256, ManifestHash: segment.Manifest.DescriptorSHA256,
		}},
		Members: storeMembers, SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID:     window.Profile.ActiveGenerationID,
		ExpectedPurgeEpoch:             window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision,
		ActivationMode:                 store.RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}

	var opens atomic.Int32
	originalOpen := openRuntimeUSearchRoot
	openRuntimeUSearchRoot = func(
		openCtx context.Context,
		cacheDir, openDatabaseID, openProfileID, openGenerationID string,
		expectations semanticindex.USearchRootExpectations,
	) (*semanticindex.USearchRoot, error) {
		opens.Add(1)
		return originalOpen(openCtx, cacheDir, openDatabaseID, openProfileID, openGenerationID, expectations)
	}
	t.Cleanup(func() { openRuntimeUSearchRoot = originalOpen })
	deps := defaultRuntimeDeps()
	deps.provider = func(semanticconfig.Config) (embedding.Provider, error) {
		return &taggedRuntimeProvider{info: embedding.Info{Provider: profile.Provider, Model: profile.Model, Dimensions: profile.Dimensions}}, nil
	}
	runtime := newRuntimeWithDeps(config.Config{RootDir: root, CacheDir: cache}, st, deps)
	builder, err := runtime.NewBuilderContext(ctx, semanticconfig.ModeOn, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = builder.Close()
		_ = runtime.Close()
	})
	if opens.Load() != 0 {
		t.Fatalf("native root opens during builder admission=%d want=0", opens.Load())
	}

	// Move the formerly distant vector into exact L0 after admission. The
	// stale native candidate must be rejected, current SQLite evidence must
	// hydrate first, and the other current root member must remain available.
	expectedNearestSourceKey := "source:runtime-1"
	expectedNearestChunk := chunksBySource[expectedNearestSourceKey]
	if err := st.PutRetrievalEmbedding(ctx, store.RetrievalEmbeddingRow{
		ChunkID: expectedNearestChunk.ChunkID, ProfileID: profileID,
		Provider: profile.Provider, Model: profile.Model,
		Dimensions: profile.Dimensions, Representation: profile.Representation, Normalization: profile.Normalization,
		VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), ChunkTextHash: expectedNearestChunk.ChunkTextHash,
		Status: store.RetrievalEmbeddingReady, AttemptCount: 2, EmbeddedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	postUpdateSnapshot, err := st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, semanticreadiness.DefaultExactMaxChunks, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	postUpdateSnapshot.Configured, postUpdateSnapshot.Enabled = true, true
	if decision := semanticreadiness.Evaluate(postUpdateSnapshot); !decision.Searchable {
		t.Fatalf("post-update readiness is not searchable: decision=%+v snapshot=%+v", decision, postUpdateSnapshot)
	}
	opts := builder.semanticOptions
	opts.Limit = 2
	docs, status, err := builder.semanticRetriever.Retrieve(ctx, "query", opts)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != semanticindex.StateSearched || status.Backend != semanticindex.BackendUSearch || status.GenerationID != generationID {
		t.Fatalf("status=%+v", status)
	}
	if len(docs) != 2 || docs[0].SourceKey != expectedNearestSourceKey || docs[0].Excerpt != "distant semantic evidence" ||
		docs[0].Retrieval == nil || len(docs[0].Retrieval.Lanes) != 1 || docs[0].Retrieval.Lanes[0].RawDistance == nil ||
		*docs[0].Retrieval.Lanes[0].RawDistance != 0 || docs[1].SourceKey != "source:runtime-0" {
		t.Fatalf("hydrated docs=%+v", docs)
	}
	if opens.Load() != 1 {
		t.Fatalf("native root opens after first retrieval=%d want=1", opens.Load())
	}
	warmDocs, warmStatus, err := builder.semanticRetriever.Retrieve(ctx, "query", opts)
	if err != nil || len(warmDocs) != 2 || warmStatus.State != semanticindex.StateSearched {
		t.Fatalf("warm docs=%+v status=%+v err=%v", warmDocs, warmStatus, err)
	}
	if opens.Load() != 1 {
		t.Fatalf("warm retrieval imported native root again: opens=%d want=1", opens.Load())
	}
}

func TestRuntimeLazyRootFailureFailsOpenForShadowAndOn(t *testing.T) {
	privateDiagnostic := filepath.Join(t.TempDir(), "private-root", "payload.usearch")
	stubRuntimeRootOpen(t, func(context.Context) (*semanticindex.USearchRoot, error) {
		return nil, fmt.Errorf("open %s: checksum mismatch", privateDiagnostic)
	})
	for _, mode := range []semanticconfig.Mode{semanticconfig.ModeShadow, semanticconfig.ModeOn} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newTaggedRuntimeFixture(t, mode, nil)
			lexical := []ask.Evidence{{Kind: "source", SourceKey: "lexical:one", Excerpt: "lexical evidence", Retrieval: &retrieval.RetrievalInfo{Lanes: []retrieval.RetrievalLane{{Name: "lexical", Status: "used"}}}}}
			fixture.builder.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
				response := ask.Response{Evidence: append([]ask.Evidence(nil), lexical...)}
				return response, response, nil
			}
			pack, err := fixture.builder.Build(t.Context(), Options{Question: "one", Limit: 1, DisablePlanner: true})
			if err != nil || len(pack.Evidence) != 1 || pack.Evidence[0].SourceKey != "lexical:one" {
				t.Fatalf("pack=%+v err=%v", pack, err)
			}
			encoded, err := json.Marshal(pack)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), privateDiagnostic) || strings.Contains(string(encoded), "checksum mismatch") {
				t.Fatalf("research JSON leaked native diagnostic: %s", encoded)
			}
			if mode == semanticconfig.ModeShadow {
				if pack.QueryPlan.ShadowComparison == nil || pack.QueryPlan.ShadowComparison.Reason != semanticindex.ReasonNativeRootArtifactsUnavailable {
					t.Fatalf("shadow comparison=%+v", pack.QueryPlan.ShadowComparison)
				}
			} else {
				var reason string
				for _, lane := range pack.QueryPlan.RetrievalLanes {
					if lane.Name == "semantic" {
						reason = lane.Reason
					}
				}
				if reason != string(semanticindex.ReasonNativeRootArtifactsUnavailable) {
					t.Fatalf("semantic reason=%q lanes=%+v", reason, pack.QueryPlan.RetrievalLanes)
				}
			}
		})
	}
}
