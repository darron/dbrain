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
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
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

func TestRuntimeSemanticSearcherReportsGenerationBusyBeforeRootOpen(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	databaseID, err := st.RetrievalDatabaseID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := semanticlock.NewScope(cache, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := scope.AcquireMaintenanceExclusive(t.Context(), "owner=activation\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = maintenance.Close() }()
	generation, err := maintenance.AcquireGenerationExclusive(t.Context(), "owner=activation\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = generation.Close() }()

	snapshot := runtimeNativeSnapshot()
	searcher, err := runtimeSemanticSearcher(
		t.Context(),
		st,
		config.Config{CacheDir: cache},
		embedding.Profile{Dimensions: 2},
		snapshot,
		semanticreadiness.DefaultExactMaxChunks,
	)
	if searcher != nil || !errors.Is(err, errSemanticGenerationBusy) {
		t.Fatalf("searcher=%#v err=%v want generation busy", searcher, err)
	}
}

func TestRuntimeSemanticSearcherDoesNotHoldGenerationLeaseWhileOpeningRoot(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	databaseID, err := st.RetrievalDatabaseID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := semanticlock.NewScope(cache, databaseID)
	if err != nil {
		t.Fatal(err)
	}

	openStarted := make(chan struct{})
	allowOpenReturn := make(chan struct{})
	openErr := errors.New("synthetic blocked root open")
	originalOpen := openRuntimeUSearchRoot
	openRuntimeUSearchRoot = func(
		context.Context,
		string,
		string,
		string,
		string,
		semanticindex.USearchRootExpectations,
	) (*semanticindex.USearchRoot, error) {
		close(openStarted)
		<-allowOpenReturn
		return nil, openErr
	}
	t.Cleanup(func() { openRuntimeUSearchRoot = originalOpen })

	type result struct {
		searcher semanticindex.Searcher
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		searcher, err := runtimeSemanticSearcher(
			t.Context(),
			st,
			config.Config{CacheDir: cache},
			embedding.Profile{Dimensions: 2},
			runtimeNativeSnapshot(),
			semanticreadiness.DefaultExactMaxChunks,
		)
		resultCh <- result{searcher: searcher, err: err}
	}()

	select {
	case <-openStarted:
	case <-time.After(time.Second):
		close(allowOpenReturn)
		t.Fatal("runtime root opener did not start")
	}

	writerCtx, cancelWriter := context.WithTimeout(t.Context(), time.Second)
	defer cancelWriter()
	maintenance, err := scope.AcquireMaintenanceExclusive(writerCtx, "owner=runtime-open-regression\n")
	if err != nil {
		close(allowOpenReturn)
		t.Fatalf("acquire maintenance exclusive: %v", err)
	}
	generation, err := maintenance.AcquireGenerationExclusive(writerCtx, "owner=runtime-open-regression\n")
	if err != nil {
		close(allowOpenReturn)
		_ = maintenance.Close()
		t.Fatalf("slow runtime root open blocked generation publication: %v", err)
	}
	if err := generation.Close(); err != nil {
		close(allowOpenReturn)
		_ = maintenance.Close()
		t.Fatalf("release generation exclusive: %v", err)
	}
	if err := maintenance.Close(); err != nil {
		close(allowOpenReturn)
		t.Fatalf("release maintenance exclusive: %v", err)
	}
	close(allowOpenReturn)

	select {
	case got := <-resultCh:
		if got.searcher != nil || !errors.Is(got.err, openErr) {
			t.Fatalf("searcher=%#v error=%v want root-open failure", got.searcher, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime root opener did not return")
	}
}

func TestRuntimeSemanticSearcherBoundsCooperativeRootOpen(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	originalOpen := openRuntimeUSearchRoot
	openRuntimeUSearchRoot = func(
		ctx context.Context,
		_ string,
		_ string,
		_ string,
		_ string,
		_ semanticindex.USearchRootExpectations,
	) (*semanticindex.USearchRoot, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { openRuntimeUSearchRoot = originalOpen })

	parentCtx, cancelParent := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelParent()
	started := time.Now()
	searcher, err := runtimeSemanticSearcher(
		parentCtx,
		st,
		config.Config{CacheDir: cache},
		embedding.Profile{Dimensions: 2},
		runtimeNativeSnapshot(),
		semanticreadiness.DefaultExactMaxChunks,
	)
	if searcher != nil || !errors.Is(err, errSemanticGenerationBusy) {
		t.Fatalf("searcher=%#v error=%v want generation busy", searcher, err)
	}
	if parentCtx.Err() != nil {
		t.Fatalf("local admission timeout canceled parent: %v", parentCtx.Err())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runtime root admission took %s, want bounded fail-open latency", elapsed)
	}
}

func TestRuntimeSemanticSearcherPreservesLateRootArtifactFailure(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rootErr := errors.New("synthetic payload checksum mismatch")
	originalOpen := openRuntimeUSearchRoot
	openRuntimeUSearchRoot = func(
		context.Context,
		string,
		string,
		string,
		string,
		semanticindex.USearchRootExpectations,
	) (*semanticindex.USearchRoot, error) {
		time.Sleep(semanticRuntimeAdmissionTimeout + 50*time.Millisecond)
		return nil, rootErr
	}
	t.Cleanup(func() { openRuntimeUSearchRoot = originalOpen })

	parentCtx, cancelParent := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelParent()
	searcher, err := runtimeSemanticSearcher(
		parentCtx,
		st,
		config.Config{CacheDir: cache},
		embedding.Profile{Dimensions: 2},
		runtimeNativeSnapshot(),
		semanticreadiness.DefaultExactMaxChunks,
	)
	if searcher != nil || !errors.Is(err, errNativeRootArtifactsUnavailable) || !errors.Is(err, rootErr) {
		t.Fatalf("searcher=%#v error=%v want preserved native root artifact failure", searcher, err)
	}
	if errors.Is(err, errSemanticGenerationBusy) {
		t.Fatalf("late native root artifact failure was misclassified as generation busy: %v", err)
	}
	if parentCtx.Err() != nil {
		t.Fatalf("late native root artifact failure canceled parent: %v", parentCtx.Err())
	}
}

func runtimeNativeSnapshot() semanticreadiness.Snapshot {
	return semanticreadiness.Snapshot{
		ActiveGenerationID:                   "root",
		ProfileID:                            "profile",
		ActiveSnapshotRevision:               1,
		ProfilePurgeEpoch:                    0,
		ActiveGenerationBackendVersion:       semanticindex.USearchVersion,
		ActiveGenerationRootDescriptorSHA256: strings.Repeat("a", 64),
	}
}

func TestRuntimeNativeRootOpenFailureFailsClosedWhenGenerationLeaseReleaseFails(t *testing.T) {
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
	snapshot := runtimeReadySnapshot(true)
	snapshot.ProfileID = profileID
	snapshot.ActiveGenerationID = "missing-native-root"
	snapshot.ActiveGenerationRootDescriptorSHA256 = strings.Repeat("a", 64)
	closeErr := errors.New("synthetic generation lease release failure")
	originalClose := closeRuntimeGenerationLease
	closeRuntimeGenerationLease = func(lease *semanticlock.Lease) error {
		return errors.Join(lease.Close(), closeErr)
	}
	t.Cleanup(func() { closeRuntimeGenerationLease = originalClose })
	openCalls := 0
	originalOpen := openRuntimeUSearchRoot
	openRuntimeUSearchRoot = func(
		context.Context,
		string,
		string,
		string,
		string,
		semanticindex.USearchRootExpectations,
	) (*semanticindex.USearchRoot, error) {
		openCalls++
		return nil, errors.New("root opener must not be called")
	}
	t.Cleanup(func() { openRuntimeUSearchRoot = originalOpen })

	providerCalls := 0
	deps := defaultRuntimeDeps()
	deps.readiness = func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return snapshot, nil
	}
	deps.capability = func() semanticindex.Capability {
		return semanticindex.Capability{
			State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion,
		}
	}
	deps.provider = func(semanticconfig.Config) (embedding.Provider, error) {
		providerCalls++
		return nil, errors.New("provider must not be constructed")
	}

	builder, err := newRuntimeBuilderWithDeps(
		t.Context(), config.Config{RootDir: root, CacheDir: cache}, st, "", false, false, deps,
	)
	if builder != nil || !errors.Is(err, closeErr) {
		t.Fatalf("builder=%#v error=%v want lease release failure", builder, err)
	}
	if errors.Is(err, errNativeRootArtifactsUnavailable) {
		t.Fatalf("lease release failure retained fail-open artifact classification: %v", err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d want=0", providerCalls)
	}
	if openCalls != 0 {
		t.Fatalf("root open calls=%d want=0 after lease release failure", openCalls)
	}
}

func TestRuntimeNativeRootOpenFailureFailsOpenAfterGenerationLeaseRelease(t *testing.T) {
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
	snapshot := runtimeReadySnapshot(true)
	snapshot.ProfileID = profileID
	snapshot.ActiveGenerationID = "missing-native-root"
	snapshot.ActiveGenerationRootDescriptorSHA256 = strings.Repeat("a", 64)
	providerCalls := 0
	deps := defaultRuntimeDeps()
	deps.readiness = func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return snapshot, nil
	}
	deps.capability = func() semanticindex.Capability {
		return semanticindex.Capability{
			State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion,
		}
	}
	deps.provider = func(semanticconfig.Config) (embedding.Provider, error) {
		providerCalls++
		return nil, errors.New("provider must not be constructed")
	}

	builder, err := newRuntimeBuilderWithDeps(
		t.Context(), config.Config{RootDir: root, CacheDir: cache}, st, "", false, false, deps,
	)
	if err != nil {
		t.Fatal(err)
	}
	if builder.semanticReadiness.Reason != errNativeRootArtifactsUnavailable.Error() {
		t.Fatalf("runtime readiness reason=%q", builder.semanticReadiness.Reason)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d want=0", providerCalls)
	}
}

func TestRuntimeMissingNativeRootUsesPathFreeReasonInResearchJSON(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	missingCache := filepath.Join(root, "private-native-cache")
	writeRuntimeSemanticConfig(t, root, "shadow", "embed-model", 2)
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
	deps := defaultRuntimeDeps()
	deps.readiness = func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return snapshot, nil
	}
	deps.capability = func() semanticindex.Capability {
		return semanticindex.Capability{
			State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion,
		}
	}
	deps.provider = func(semanticconfig.Config) (embedding.Provider, error) {
		return nil, errors.New("provider must not be constructed")
	}

	builder, err := newRuntimeBuilderWithDeps(ctx, config.Config{RootDir: root, CacheDir: missingCache}, st, "", false, false, deps)
	if err != nil {
		t.Fatal(err)
	}
	if builder.semanticReadiness.Reason != "native_root_artifacts_unavailable" {
		t.Fatalf("runtime readiness reason=%q", builder.semanticReadiness.Reason)
	}
	builder.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
		return ask.Response{}, ask.Response{}, nil
	}
	pack, err := builder.Build(ctx, Options{Question: "path leak check", Limit: 1, DisablePlanner: true})
	if err != nil {
		t.Fatal(err)
	}
	if pack.QueryPlan.ShadowComparison == nil || pack.QueryPlan.ShadowComparison.Reason != "native_root_artifacts_unavailable" {
		t.Fatalf("shadow comparison=%+v", pack.QueryPlan.ShadowComparison)
	}
	encoded, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{missingCache, semanticsegment.RootFileName} {
		if strings.Contains(builder.semanticReadiness.Reason, leaked) ||
			strings.Contains(string(pack.QueryPlan.ShadowComparison.Reason), leaked) ||
			strings.Contains(string(encoded), leaked) {
			t.Fatalf("runtime research output leaked %q: readiness=%q comparison=%+v json=%s",
				leaked, builder.semanticReadiness.Reason, pack.QueryPlan.ShadowComparison, encoded)
		}
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
