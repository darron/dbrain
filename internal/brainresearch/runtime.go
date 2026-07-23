package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

type runtimeDeps struct {
	readiness func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
	provider  func(semanticconfig.Config) (embedding.Provider, error)
	searcher  func(context.Context, *store.Store, config.Config, embedding.Profile, semanticreadiness.Snapshot, int) (semanticindex.Searcher, error)
}

const semanticRuntimeAdmissionTimeout = 250 * time.Millisecond

func defaultRuntimeDeps() runtimeDeps {
	return runtimeDeps{
		readiness: func(ctx context.Context, st *store.Store, profile embedding.Profile, exactMax int, now time.Time) (semanticreadiness.Snapshot, error) {
			if st == nil {
				return semanticreadiness.Snapshot{}, store.ErrRetrievalUnavailable
			}
			return st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, exactMax, now)
		},
		provider: func(cfg semanticconfig.Config) (embedding.Provider, error) {
			return embedding.NewOllama(embedding.OllamaOptions{BaseURL: cfg.OllamaBaseURL, Model: cfg.Model, Dimensions: cfg.Dimensions})
		},
		searcher: runtimeSemanticSearcher,
	}
}

func NewRuntimeBuilder(cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	return NewRuntimeBuilderContext(context.Background(), cfg, st, configuredOverride, forceOn, forceOff)
}

func NewRuntimeBuilderContext(ctx context.Context, cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	return newRuntimeBuilderWithDeps(ctx, cfg, st, configuredOverride, forceOn, forceOff, defaultRuntimeDeps())
}

func newRuntimeBuilderWithDeps(ctx context.Context, cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool, deps runtimeDeps) (*Builder, error) {
	if deps.searcher == nil {
		deps.searcher = defaultRuntimeDeps().searcher
	}
	if _, err := semanticconfig.EffectiveMode(semanticconfig.ModeOff, forceOn, forceOff); err != nil {
		return nil, err
	}
	if forceOff {
		b := New(cfg, st).WithSemanticMode(semanticconfig.ModeOff)
		b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateDisabled, Reason: "semantic retrieval mode is off"}
		return b, nil
	}
	configured, err := semanticconfig.ResolveDiagnostic(cfg.RootDir)
	if err != nil {
		return nil, err
	}
	configuredMode := configured.Mode
	if configuredOverride != "" {
		configuredMode = configuredOverride
	}
	mode, err := semanticconfig.EffectiveMode(configuredMode, forceOn, forceOff)
	if err != nil {
		return nil, err
	}
	b := New(cfg, st).WithSemanticMode(mode)
	if mode == semanticconfig.ModeOff {
		b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateDisabled, Reason: "semantic retrieval mode is off"}
		return b, nil
	}
	ready := configured
	ready.Mode = mode
	if err := ready.Validate(); err != nil {
		return nil, err
	}
	exactMaxChunks := semanticreadiness.EffectiveExactMaxChunks(ready.ExactFallbackMaxChunks)
	profile := semanticbuild.Profile(embedding.Info{Provider: string(ready.Provider), Model: ready.Model, Dimensions: ready.Dimensions})
	readinessCtx, cancelReadiness := context.WithTimeout(ctx, semanticRuntimeAdmissionTimeout)
	defer cancelReadiness()
	snapshot, snapshotErr := deps.readiness(readinessCtx, st, profile, exactMaxChunks, time.Now().UTC())
	if snapshotErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if errors.Is(snapshotErr, store.ErrRetrievalUnavailable) {
			b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateUnavailable, Reason: "retrieval schema is unavailable"}
			return b, nil
		}
		b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateUnavailable, Reason: "semantic readiness snapshot unavailable: " + snapshotErr.Error()}
		return b, nil
	}
	snapshot.Configured, snapshot.Enabled = true, true
	snapshot.ExactMaxChunks = exactMaxChunks
	if snapshot.Now.IsZero() {
		snapshot.Now = time.Now().UTC()
	}
	b.semanticReadiness = semanticreadiness.Evaluate(snapshot)
	var oldestDebtAt *time.Time
	if !snapshot.OldestDirtyAt.IsZero() {
		oldest := snapshot.OldestDirtyAt
		oldestDebtAt = &oldest
	}
	b.WithSemanticReadinessDiagnostics(SemanticReadinessDiagnostics{
		OmittedParentCount:      max(snapshot.DirtyParents, snapshot.PendingParents),
		EstimatedNotReadyChunks: snapshot.EstimatedNotReadyChunks,
		OldestDebtAt:            oldestDebtAt,
	})
	if !b.semanticReadiness.Searchable {
		return b, nil
	}
	searcher, err := deps.searcher(ctx, st, cfg, profile, snapshot, exactMaxChunks)
	if err != nil {
		b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateUnavailable, Reason: "semantic searcher unavailable: " + err.Error()}
		return b, nil
	}
	provider, err := deps.provider(ready)
	if err != nil {
		return nil, fmt.Errorf("construct semantic embedding provider: %w", err)
	}
	if actual := semanticbuild.Profile(provider.Info()); actual != profile {
		return nil, fmt.Errorf("constructed semantic provider provenance does not match admitted profile")
	}
	retriever := researchsemantic.New(provider, searcher, st)
	return b.WithSemanticRetriever(retriever, researchsemantic.Options{
		Profile: profile, Limit: ready.CandidateDepth, MaxChunks: exactMaxChunks,
		Timeout: researchsemantic.DefaultQueryTimeout,
	}), nil
}
