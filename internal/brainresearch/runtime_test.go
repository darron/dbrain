package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticconfig"
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
			deps := runtimeDeps{
				readiness: func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
					return snapshot, tc.err
				},
				provider: func(semanticconfig.Config) (embedding.Provider, error) {
					providerCalls++
					return nil, errors.New("provider must not be constructed")
				},
			}
			b, err := newRuntimeBuilderWithDeps(context.Background(), config.Config{RootDir: root}, st, "", false, false, deps)
			if err != nil || b == nil || b.semanticReadiness.State != tc.want || b.semanticReadiness.Searchable || b.semanticRetriever != nil || providerCalls != 0 {
				t.Fatalf("builder=%#v err=%v provider_calls=%d want_state=%s", b, err, providerCalls, tc.want)
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
