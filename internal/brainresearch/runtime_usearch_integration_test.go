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
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticruntime"
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
	maintenance, err := scope.AcquireMaintenanceExclusive(t.Context(), "owner=test-refresh\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = maintenance.Close() }()
	blockedCtx, cancelBlocked := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancelBlocked()
	if generation, err := maintenance.AcquireGenerationExclusive(blockedCtx, "owner=test-refresh\n"); generation != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("generation=%#v error=%v want retained load guard", generation, err)
	}
	close(unblock)
	deadline := time.Now().Add(time.Second)
	for {
		acquireCtx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
		generation, acquireErr := maintenance.AcquireGenerationExclusive(acquireCtx, "owner=test-refresh\n")
		cancel()
		if acquireErr == nil {
			_ = generation.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retained generation guard did not release: %v", acquireErr)
		}
		time.Sleep(5 * time.Millisecond)
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
