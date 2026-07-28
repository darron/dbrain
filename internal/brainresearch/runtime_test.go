package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

func TestNewRuntimeBuilderModeConstructionAndForceOff(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	b, err := NewRuntimeBuilder(cfg, nil, "", false, false)
	if err != nil || b.semanticMode != semanticconfig.ModeOff || b.semanticRetriever != nil {
		t.Fatalf("off builder = %#v, err=%v", b, err)
	}

	writeRuntimeSemanticConfig(t, root, "shadow", "embed-model", 2)
	_, st := inspectionTestStore(t)
	b, err = NewRuntimeBuilder(cfg, st, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if b.semanticMode != semanticconfig.ModeShadow || b.semanticRetriever != nil || b.semanticReadiness.State != semanticreadiness.StateNeedsEmbeddings {
		t.Fatalf("shadow builder = %#v", b)
	}

	writeRuntimeSemanticConfig(t, root, "on", "", 0)
	if _, err := NewRuntimeBuilder(cfg, st, "", false, false); err == nil {
		t.Fatal("effective on must reject incomplete profile")
	}
	b, err = NewRuntimeBuilder(cfg, st, "", false, true)
	if err != nil || b.semanticMode != semanticconfig.ModeOff || b.semanticRetriever != nil {
		t.Fatalf("force-off incomplete builder = %#v, err=%v", b, err)
	}
	if _, err := NewRuntimeBuilder(cfg, st, "", true, true); !errors.Is(err, semanticconfig.ErrConflictingOverrides) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestRuntimeAdmissionEvaluatesBeforeProviderConstructionAndForceOnCannotBypass(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfigWithExactCap(t, root, "off", "embed-model", 2, 300_000)
	cfg := config.Config{RootDir: root}
	_, st := inspectionTestStore(t)
	providerCalls := 0
	readinessExactCap := 0
	deps := runtimeDeps{
		readiness: func(_ context.Context, _ *store.Store, _ embedding.Profile, exactCap int, _ time.Time) (semanticreadiness.Snapshot, error) {
			readinessExactCap = exactCap
			return semanticreadiness.Snapshot{
				Available: true, ProfileExists: true, ProfileProvenanceValid: true,
				ExpectedParents: 1, CurrentParents: 1, ChunkableParents: 1, ParentsWithReadyChunk: 1,
				ChunkCount: 25_001, ReadyEmbeddings: 25_001, GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
				LatestRevision: 1, ObservedLatestRevision: 1, L0ReadyCount: 25_001, ObservedL0ReadyCount: 25_001,
			}, nil
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			providerCalls++
			return &runtimeProvider{info: embedding.Info{Provider: "ollama", Model: "embed-model", Dimensions: 2}}, nil
		},
	}
	b, err := newRuntimeBuilderWithDeps(context.Background(), cfg, st, "", true, false, deps)
	if err != nil {
		t.Fatal(err)
	}
	if b.semanticMode != semanticconfig.ModeOn || b.semanticReadiness.State != semanticreadiness.StateNeedsIndex || b.semanticRetriever != nil || providerCalls != 0 || readinessExactCap != semanticreadiness.DefaultExactMaxChunks {
		t.Fatalf("builder=%#v provider_calls=%d readiness_exact_cap=%d", b, providerCalls, readinessExactCap)
	}

	deps.readiness = func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return semanticreadiness.Snapshot{
			Available: true, ProfileExists: true, ProfileProvenanceValid: true,
			ExpectedParents: 1, CurrentParents: 1, ChunkableParents: 1, ParentsWithReadyChunk: 1,
			ChunkCount: 1, ReadyEmbeddings: 1, GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
			LatestRevision: 1, ObservedLatestRevision: 1, L0ReadyCount: 1, ObservedL0ReadyCount: 1,
		}, nil
	}
	b, err = newRuntimeBuilderWithDeps(context.Background(), cfg, st, "", true, false, deps)
	if err != nil || b.semanticReadiness.State != semanticreadiness.StateReady || b.semanticRetriever == nil || providerCalls != 1 {
		t.Fatalf("ready builder=%#v provider_calls=%d err=%v", b, providerCalls, err)
	}
	oldest := time.Now().UTC().Add(-time.Minute)
	deps.readiness = func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return semanticreadiness.Snapshot{
			Available: true, ProfileExists: true, ProfileProvenanceValid: true,
			ExpectedParents: 2, CurrentParents: 1, PendingParents: 1, DirtyParents: 1,
			ChunkableParents: 1, ParentsWithReadyChunk: 1, ChunkCount: 1, ReadyEmbeddings: 1,
			EstimatedNotReadyChunks: 1, OldestDirtyAt: oldest,
			GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1, LatestRevision: 1, ObservedLatestRevision: 1,
			L0ReadyCount: 1, ObservedL0ReadyCount: 1,
		}, nil
	}
	b, err = newRuntimeBuilderWithDeps(context.Background(), cfg, st, "", true, false, deps)
	if err != nil || b.semanticReadiness.State != semanticreadiness.StateCatchingUp || b.semanticReadinessDiagnostics == nil ||
		b.semanticReadinessDiagnostics.OmittedParentCount != 1 || b.semanticReadinessDiagnostics.EstimatedNotReadyChunks != 1 ||
		b.semanticReadinessDiagnostics.OldestDebtAt == nil || !b.semanticReadinessDiagnostics.OldestDebtAt.Equal(oldest) {
		t.Fatalf("catching-up builder=%#v diagnostics=%+v err=%v", b, b.semanticReadinessDiagnostics, err)
	}
}

func TestRuntimeAdmissionExactSmallSkipsNativeCapability(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	_, st := inspectionTestStore(t)
	capabilityCalls := 0
	providerCalls := 0
	searcherCalls := 0
	deps := runtimeDeps{
		readiness: func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
			return runtimeReadySnapshot(false), nil
		},
		capability: func() semanticindex.Capability {
			capabilityCalls++
			return semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			providerCalls++
			return &runtimeProvider{info: embedding.Info{Provider: "ollama", Model: "embed-model", Dimensions: 2}}, nil
		},
		searcher: func(_ context.Context, st *store.Store, _ config.Config, _ embedding.Profile, snapshot semanticreadiness.Snapshot, _ int) (semanticindex.Searcher, error) {
			searcherCalls++
			if snapshot.ActiveGenerationID != "" {
				t.Fatalf("exact-small search received active generation %q", snapshot.ActiveGenerationID)
			}
			return semanticindex.NewExact(st), nil
		},
	}
	b, err := newRuntimeBuilderWithDeps(context.Background(), config.Config{RootDir: root}, st, "", false, false, deps)
	if err != nil || b.semanticRetriever == nil || b.semanticReadiness.State != semanticreadiness.StateReady || !b.semanticReadiness.Searchable ||
		capabilityCalls != 0 || searcherCalls != 1 || providerCalls != 1 {
		t.Fatalf("builder=%#v capability_calls=%d searcher_calls=%d provider_calls=%d err=%v", b, capabilityCalls, searcherCalls, providerCalls, err)
	}
}

func TestRuntimeAdmissionCapability(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	_, st := inspectionTestStore(t)
	tests := []struct {
		name       string
		capability semanticindex.Capability
		wantReason string
		admitted   bool
	}{
		{
			name:       "unsupported",
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantReason: "native_backend_unsupported",
		},
		{
			name: "broken",
			capability: semanticindex.Capability{
				State: semanticindex.CapabilitySupportedBroken, Backend: semanticindex.BackendUSearch,
				Version: semanticindex.USearchVersion, Reason: "load /private/tmp/libusearch.dylib failed",
			},
			wantReason: "native_backend_broken: load [path] failed",
		},
		{
			name: "provenance mismatch",
			capability: semanticindex.Capability{
				State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: "2.25.0",
			},
			wantReason: "native_backend_provenance_mismatch",
		},
		{
			name: "matching native backend",
			capability: semanticindex.Capability{
				State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion,
			},
			admitted: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := make([]string, 0, 4)
			deps := runtimeDeps{
				readiness: func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
					calls = append(calls, "readiness")
					return runtimeReadySnapshot(true), nil
				},
				capability: func() semanticindex.Capability {
					calls = append(calls, "capability")
					return tc.capability
				},
				searcher: func(_ context.Context, st *store.Store, _ config.Config, _ embedding.Profile, snapshot semanticreadiness.Snapshot, _ int) (semanticindex.Searcher, error) {
					calls = append(calls, "searcher")
					if snapshot.ActiveGenerationBackend != semanticindex.BackendUSearch || snapshot.ActiveGenerationBackendVersion != semanticindex.USearchVersion {
						t.Fatalf("searcher snapshot provenance=%+v", snapshot)
					}
					return semanticindex.NewExact(st), nil
				},
				provider: func(semanticconfig.Config) (embedding.Provider, error) {
					calls = append(calls, "provider")
					return &runtimeProvider{info: embedding.Info{Provider: "ollama", Model: "embed-model", Dimensions: 2}}, nil
				},
			}
			b, err := newRuntimeBuilderWithDeps(context.Background(), config.Config{RootDir: root}, st, "", false, false, deps)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := []string{"readiness", "capability"}
			if tc.admitted {
				wantCalls = append(wantCalls, "searcher", "provider")
				if b.semanticRetriever == nil || b.semanticReadiness.State != semanticreadiness.StateReady || !b.semanticReadiness.Searchable {
					t.Fatalf("admitted builder=%#v", b)
				}
			} else if b.semanticRetriever != nil || b.semanticReadiness.State != semanticreadiness.StateUnavailable ||
				b.semanticReadiness.Searchable || b.semanticReadiness.Reason != tc.wantReason {
				t.Fatalf("rejected builder=%#v want_reason=%q", b, tc.wantReason)
			}
			if fmt.Sprint(calls) != fmt.Sprint(wantCalls) {
				t.Fatalf("calls=%v want=%v", calls, wantCalls)
			}
		})
	}
}

func TestRuntimeBuilderClosesConstructedSearcherWhenProviderAdmissionFails(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	_, st := inspectionTestStore(t)
	providerFailure := errors.New("provider construction failed")
	closeFailure := errors.New("searcher close failed")

	for _, tc := range []struct {
		name         string
		provider     func(semanticconfig.Config) (embedding.Provider, error)
		wantError    string
		wantSentinel error
	}{
		{
			name: "provider construction error",
			provider: func(semanticconfig.Config) (embedding.Provider, error) {
				return nil, providerFailure
			},
			wantError:    "construct semantic embedding provider: provider construction failed",
			wantSentinel: providerFailure,
		},
		{
			name: "provider provenance mismatch",
			provider: func(semanticconfig.Config) (embedding.Provider, error) {
				return &runtimeProvider{info: embedding.Info{Provider: "ollama", Model: "wrong-model", Dimensions: 2}}, nil
			},
			wantError: "constructed semantic provider provenance does not match admitted profile",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searcher := &closeableRuntimeSearcher{closeErr: closeFailure}
			deps := runtimeDeps{
				readiness: func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
					return runtimeReadySnapshot(false), nil
				},
				searcher: func(context.Context, *store.Store, config.Config, embedding.Profile, semanticreadiness.Snapshot, int) (semanticindex.Searcher, error) {
					return searcher, nil
				},
				provider: tc.provider,
			}

			builder, err := newRuntimeBuilderWithDeps(context.Background(), config.Config{RootDir: root}, st, "", false, false, deps)
			if builder != nil || err == nil || err.Error() != tc.wantError {
				t.Fatalf("builder=%#v error=%v want %q", builder, err, tc.wantError)
			}
			if tc.wantSentinel != nil && !errors.Is(err, tc.wantSentinel) {
				t.Fatalf("error=%v does not preserve provider failure", err)
			}
			if errors.Is(err, closeFailure) || strings.Contains(err.Error(), closeFailure.Error()) {
				t.Fatalf("close error replaced primary error: %v", err)
			}
			if searcher.closeCalls != 1 {
				t.Fatalf("searcher close calls=%d want=1", searcher.closeCalls)
			}
		})
	}
}

func runtimeReadySnapshot(active bool) semanticreadiness.Snapshot {
	snapshot := semanticreadiness.Snapshot{
		Available: true, ProfileExists: true, ProfileProvenanceValid: true,
		ExpectedParents: 1, CurrentParents: 1, ChunkableParents: 1, ParentsWithReadyChunk: 1,
		ChunkCount: 100, ReadyEmbeddings: 100,
		GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
		LatestRevision: 1, ObservedLatestRevision: 1,
		L0ReadyCount: 1, ObservedL0ReadyCount: 1,
	}
	if active {
		snapshot.ActiveGenerationID = "root"
		snapshot.ActiveGenerationValid = true
		snapshot.ActiveSnapshotRevision = 1
		snapshot.ActiveGenerationBackend = semanticindex.BackendUSearch
		snapshot.ActiveGenerationBackendVersion = semanticindex.USearchVersion
		snapshot.ActiveGenerationDistanceMetric = "cosine"
		snapshot.ActiveGenerationDimensions = 2
		snapshot.ActiveIndexedCount = 99
	}
	return snapshot
}

func TestRuntimeAdmissionPropagatesCallerCancellationBeforeProviderConstruction(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	_, st := inspectionTestStore(t)
	providerCalls := 0
	deps := runtimeDeps{
		readiness: func(ctx context.Context, _ *store.Store, _ embedding.Profile, _ int, _ time.Time) (semanticreadiness.Snapshot, error) {
			<-ctx.Done()
			return semanticreadiness.Snapshot{}, ctx.Err()
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			providerCalls++
			return nil, errors.New("provider must not be constructed")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, err := newRuntimeBuilderWithDeps(ctx, config.Config{RootDir: root}, st, "", false, false, deps)
	if !errors.Is(err, context.Canceled) || b != nil || providerCalls != 0 {
		t.Fatalf("builder=%#v err=%v provider_calls=%d", b, err, providerCalls)
	}
}

func TestRuntimeAdmissionUsesShortFailOpenLatencyBudgetBeforeProviderConstruction(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	_, st := inspectionTestStore(t)
	providerCalls := 0
	deps := runtimeDeps{
		readiness: func(ctx context.Context, _ *store.Store, _ embedding.Profile, _ int, _ time.Time) (semanticreadiness.Snapshot, error) {
			<-ctx.Done()
			return semanticreadiness.Snapshot{}, ctx.Err()
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			providerCalls++
			return nil, errors.New("provider must not be constructed")
		},
	}
	started := time.Now()
	b, err := newRuntimeBuilderWithDeps(context.Background(), config.Config{RootDir: root}, st, "", false, false, deps)
	if err != nil || b == nil || b.semanticReadiness.State != semanticreadiness.StateUnavailable || providerCalls != 0 {
		t.Fatalf("builder=%#v err=%v provider_calls=%d", b, err, providerCalls)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runtime admission took %s, want bounded fail-open latency", elapsed)
	}
}

func TestRuntimeAdmissionSkipsProviderForEveryIneligibleReadinessClass(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "on", "embed-model", 2)
	_, st := inspectionTestStore(t)
	base := semanticreadiness.Snapshot{
		Available: true, ProfileExists: true, ProfileProvenanceValid: true,
		ExpectedParents: 1, CurrentParents: 1, ChunkableParents: 1, ParentsWithReadyChunk: 1,
		ChunkCount: 1, ReadyEmbeddings: 1, GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
		LatestRevision: 1, ObservedLatestRevision: 1, L0ReadyCount: 1, ObservedL0ReadyCount: 1,
	}
	tests := []struct {
		name string
		want semanticreadiness.State
		edit func(*semanticreadiness.Snapshot)
		err  error
	}{
		{"unavailable", semanticreadiness.StateUnavailable, nil, store.ErrRetrievalUnavailable},
		{"corrupt", semanticreadiness.StateCorrupt, func(s *semanticreadiness.Snapshot) { s.ProfileProvenanceValid = false }, nil},
		{"building", semanticreadiness.StateBuilding, func(s *semanticreadiness.Snapshot) { addOverBudgetEmbeddingDebt(s); s.BuildingGenerations = 1 }, nil},
		{"stale", semanticreadiness.StateStale, func(s *semanticreadiness.Snapshot) { addOverBudgetEmbeddingDebt(s); s.StaleGenerations = 1 }, nil},
		{"degraded blocked", semanticreadiness.StateDegradedBlocked, func(s *semanticreadiness.Snapshot) { s.ErrorParents = 1 }, nil},
		{"needs projection", semanticreadiness.StateNeedsProjection, func(s *semanticreadiness.Snapshot) {
			s.ExpectedParents = 2
			s.PendingParents = 1
			s.DirtyParents = 501
			s.EstimatedNotReadyChunks = 1
		}, nil},
		{"needs embeddings", semanticreadiness.StateNeedsEmbeddings, addOverBudgetEmbeddingDebt, nil},
		{"needs index", semanticreadiness.StateNeedsIndex, func(s *semanticreadiness.Snapshot) {
			s.ChunkCount = 25_001
			s.ReadyEmbeddings = 25_001
			s.L0ReadyCount = 25_001
			s.ObservedL0ReadyCount = 25_001
		}, nil},
		{"retry scheduled", semanticreadiness.StateRetryScheduled, func(s *semanticreadiness.Snapshot) { s.ScheduledRetries = 1; s.EstimatedNotReadyChunks = 2_501 }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			if tc.edit != nil {
				tc.edit(&snapshot)
			}
			providerCalls := 0
			capabilityCalls := 0
			searcherCalls := 0
			deps := runtimeDeps{
				readiness: func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
					return snapshot, tc.err
				},
				capability: func() semanticindex.Capability {
					capabilityCalls++
					return semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion}
				},
				searcher: func(context.Context, *store.Store, config.Config, embedding.Profile, semanticreadiness.Snapshot, int) (semanticindex.Searcher, error) {
					searcherCalls++
					return nil, errors.New("searcher must not be constructed")
				},
				provider: func(semanticconfig.Config) (embedding.Provider, error) {
					providerCalls++
					return nil, errors.New("provider must not be constructed")
				},
			}
			b, err := newRuntimeBuilderWithDeps(context.Background(), config.Config{RootDir: root}, st, "", false, false, deps)
			if err != nil || b == nil || b.semanticReadiness.State != tc.want || b.semanticReadiness.Searchable || b.semanticRetriever != nil ||
				capabilityCalls != 0 || searcherCalls != 0 || providerCalls != 0 {
				t.Fatalf("builder=%#v err=%v capability_calls=%d searcher_calls=%d provider_calls=%d want_state=%s", b, err, capabilityCalls, searcherCalls, providerCalls, tc.want)
			}
		})
	}
}

func addOverBudgetEmbeddingDebt(s *semanticreadiness.Snapshot) {
	s.ChunkCount = 2
	s.ReadyEmbeddings = 1
	s.PendingEmbeddings = 1
	s.EstimatedNotReadyChunks = 2_501
}

type runtimeProvider struct{ info embedding.Info }

func (p *runtimeProvider) Info() embedding.Info { return p.info }
func (p *runtimeProvider) Embed(context.Context, embedding.Request) (embedding.Response, error) {
	return embedding.Response{}, errors.New("unexpected runtime query")
}

type closeableRuntimeSearcher struct {
	closeCalls int
	closeErr   error
}

func (*closeableRuntimeSearcher) Search(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	return nil, semanticindex.Status{}, errors.New("unexpected runtime search")
}

func (s *closeableRuntimeSearcher) Close() error {
	s.closeCalls++
	return s.closeErr
}

func TestNewRuntimeBuilderForceOffSkipsMalformedUnusedSemanticConfig(t *testing.T) {
	root := t.TempDir()
	writeRuntimeSemanticConfig(t, root, "malformed", "", 0)
	b, err := NewRuntimeBuilder(config.Config{RootDir: root}, nil, "", false, true)
	if err != nil || b.semanticMode != semanticconfig.ModeOff || b.semanticRetriever != nil {
		t.Fatalf("force-off malformed builder = %#v, err=%v", b, err)
	}
	if _, err := NewRuntimeBuilder(config.Config{RootDir: root}, nil, "", true, true); !errors.Is(err, semanticconfig.ErrConflictingOverrides) {
		t.Fatalf("conflict error = %v", err)
	}
}

func writeRuntimeSemanticConfig(t *testing.T, root, mode, model string, dimensions int) {
	writeRuntimeSemanticConfigWithExactCap(t, root, mode, model, dimensions, 0)
}

func writeRuntimeSemanticConfigWithExactCap(t *testing.T, root, mode, model string, dimensions, exactCap int) {
	t.Helper()
	data := []byte("research:\n  semantic:\n    mode: " + mode + "\n    model: " + model + "\n    dimensions: " + fmt.Sprint(dimensions) + "\n")
	if exactCap > 0 {
		data = append(data, []byte("    exact_fallback_max_chunks: "+fmt.Sprint(exactCap)+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
