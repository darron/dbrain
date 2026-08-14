package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticruntime"
	"github.com/darron/dbrain/internal/store"
)

type runtimeDeps struct {
	readiness  func(context.Context, *store.Store, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
	capability func() semanticindex.Capability
	provider   func(semanticconfig.Config) (embedding.Provider, error)
	searcher   func(context.Context, *Runtime, embedding.Profile, semanticreadiness.Snapshot, int) (semanticindex.Searcher, error)
	rootLoader func(context.Context, *Runtime, semanticruntime.RootKey) (semanticruntime.LoadedSearcher, error)
	rootCache  func(semanticruntime.Loader, time.Duration) *semanticruntime.Manager
}

const (
	semanticRuntimeAdmissionTimeout    = 250 * time.Millisecond
	semanticRuntimeRootLoadWaitTimeout = 5 * time.Second
)

var errNativeBackendUnavailable = errors.New("native_backend_unavailable")
var errNativeRootArtifactsUnavailable = errors.New(string(semanticindex.ReasonNativeRootArtifactsUnavailable))
var errSemanticGenerationBusy = errors.New(string(semanticindex.ReasonGenerationBusy))
var errRuntimeReadinessUnavailable = errors.New(string(semanticindex.ReasonRuntimeReadinessUnavailable))
var errSemanticRuntimeClosed = errors.New("semantic research runtime is shut down")

type semanticLeaseReleaseFailure interface {
	semanticLeaseReleaseFailure()
}

func defaultRuntimeDeps() runtimeDeps {
	return runtimeDeps{
		readiness: func(ctx context.Context, st *store.Store, profile embedding.Profile, exactMax int, now time.Time) (semanticreadiness.Snapshot, error) {
			if st == nil {
				return semanticreadiness.Snapshot{}, store.ErrRetrievalUnavailable
			}
			return st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, exactMax, now)
		},
		capability: semanticindex.RuntimeCapability,
		provider: func(cfg semanticconfig.Config) (embedding.Provider, error) {
			return embedding.NewOllama(embedding.OllamaOptions{BaseURL: cfg.OllamaBaseURL, Model: cfg.Model, Dimensions: cfg.Dimensions})
		},
		searcher:   runtimeSemanticSearcher,
		rootLoader: runtimeLoadSemanticRoot,
		rootCache:  semanticruntime.New,
	}
}

type runtimeRootSpec struct {
	cacheDir       string
	profile        embedding.Profile
	exactMaxChunks int
}

type runtimeRootSpecScope struct {
	cacheDir   string
	databaseID string
	profileID  string
}

type runtimeRootSpecState struct {
	key  semanticruntime.RootKey
	spec runtimeRootSpec
}

func runtimeRootScope(key semanticruntime.RootKey) runtimeRootSpecScope {
	return runtimeRootSpecScope{
		cacheDir: key.CacheDir, databaseID: key.DatabaseID, profileID: key.ProfileID,
	}
}

// Runtime owns one semantic root cache for exactly one Store lifetime.
type Runtime struct {
	cfg       config.Config
	st        *store.Store
	rootCache *semanticruntime.Manager
	deps      runtimeDeps

	rootSpecsMu sync.Mutex
	rootSpecs   map[runtimeRootSpecScope]runtimeRootSpecState

	activeBuilds runtimeBuildTracker
	closeOnce    sync.Once
	closeMu      sync.Mutex
	closeErr     error
	drained      chan struct{}
}

type runtimeBuildTracker struct {
	mu      sync.Mutex
	active  int
	closing bool
	drained chan struct{}
	done    bool
}

func newRuntimeBuildTracker() runtimeBuildTracker {
	return runtimeBuildTracker{drained: make(chan struct{})}
}

func (t *runtimeBuildTracker) begin() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing {
		return errSemanticRuntimeClosed
	}
	t.active++
	return nil
}

func (t *runtimeBuildTracker) release() {
	t.mu.Lock()
	if t.active > 0 {
		t.active--
	}
	t.checkDrainedLocked()
	t.mu.Unlock()
}

func (t *runtimeBuildTracker) shutdown() <-chan struct{} {
	t.mu.Lock()
	t.closing = true
	t.checkDrainedLocked()
	drained := t.drained
	t.mu.Unlock()
	return drained
}

func (t *runtimeBuildTracker) checkDrainedLocked() {
	if t.closing && t.active == 0 && !t.done {
		t.done = true
		close(t.drained)
	}
}

func NewRuntime(cfg config.Config, st *store.Store) *Runtime {
	return newRuntimeWithDeps(cfg, st, defaultRuntimeDeps())
}

func newRuntimeWithDeps(cfg config.Config, st *store.Store, deps runtimeDeps) *Runtime {
	defaults := defaultRuntimeDeps()
	if deps.readiness == nil {
		deps.readiness = defaults.readiness
	}
	if deps.capability == nil {
		deps.capability = defaults.capability
	}
	if deps.provider == nil {
		deps.provider = defaults.provider
	}
	if deps.searcher == nil {
		deps.searcher = defaults.searcher
	}
	if deps.rootLoader == nil {
		deps.rootLoader = defaults.rootLoader
	}
	if deps.rootCache == nil {
		deps.rootCache = defaults.rootCache
	}
	r := &Runtime{
		cfg: cfg, st: st, deps: deps,
		rootSpecs:    make(map[runtimeRootSpecScope]runtimeRootSpecState),
		activeBuilds: newRuntimeBuildTracker(),
		drained:      make(chan struct{}),
	}
	r.rootCache = deps.rootCache(func(ctx context.Context, key semanticruntime.RootKey) (semanticruntime.LoadedSearcher, error) {
		return deps.rootLoader(ctx, r, key)
	}, semanticRuntimeRootLoadWaitTimeout)
	return r
}

func NewRuntimeBuilder(cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	return NewRuntimeBuilderContext(context.Background(), cfg, st, configuredOverride, forceOn, forceOff)
}

func NewRuntimeBuilderContext(ctx context.Context, cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	return newRuntimeBuilderWithDeps(ctx, cfg, st, configuredOverride, forceOn, forceOff, defaultRuntimeDeps())
}

func newRuntimeBuilderWithDeps(ctx context.Context, cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool, deps runtimeDeps) (*Builder, error) {
	runtime := newRuntimeWithDeps(cfg, st, deps)
	b, err := runtime.NewBuilderContext(ctx, configuredOverride, forceOn, forceOff)
	if err != nil {
		_ = runtime.Close()
		return nil, err
	}
	b.lifecycle.ownedRuntime = runtime
	return b, nil
}

func (r *Runtime) NewBuilderContext(ctx context.Context, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	if r == nil {
		return nil, errSemanticRuntimeClosed
	}
	if err := r.activeBuilds.begin(); err != nil {
		return nil, err
	}
	b, err := r.newBuilderContext(ctx, configuredOverride, forceOn, forceOff)
	if err != nil {
		r.activeBuilds.release()
		return nil, err
	}
	b.lifecycle = &builderLifecycle{releaseRuntimeBuild: r.activeBuilds.release}
	return b, nil
}

func (r *Runtime) newBuilderContext(ctx context.Context, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	cfg, st, deps := r.cfg, r.st, r.deps
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
	if snapshot.ActiveGenerationID != "" {
		if ok, reason := deps.capability().Admit(snapshot.ActiveGenerationBackend, snapshot.ActiveGenerationBackendVersion); !ok {
			b.semanticReadiness = semanticreadiness.Decision{
				State:      semanticreadiness.StateUnavailable,
				Reason:     reason,
				Searchable: false,
			}
			return b, nil
		}
	}
	searcher, err := deps.searcher(ctx, r, profile, snapshot, exactMaxChunks)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var releaseFailure semanticLeaseReleaseFailure
		if errors.As(err, &releaseFailure) {
			return nil, err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		reason := "semantic searcher unavailable: " + err.Error()
		if errors.Is(err, errNativeBackendUnavailable) {
			reason = errNativeBackendUnavailable.Error()
		} else if errors.Is(err, errNativeRootArtifactsUnavailable) {
			reason = errNativeRootArtifactsUnavailable.Error()
		} else if errors.Is(err, errSemanticGenerationBusy) {
			reason = errSemanticGenerationBusy.Error()
		}
		b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateUnavailable, Reason: reason}
		return b, nil
	}
	provider, err := deps.provider(ready)
	if err != nil {
		closeRuntimeSemanticSearcher(searcher)
		return nil, fmt.Errorf("construct semantic embedding provider: %w", err)
	}
	if actual := semanticbuild.Profile(provider.Info()); actual != profile {
		closeRuntimeSemanticSearcher(searcher)
		return nil, fmt.Errorf("constructed semantic provider provenance does not match admitted profile")
	}
	acquireGeneration, err := runtimeGenerationLeaseAcquirer(ctx, st, cfg)
	if err != nil {
		closeRuntimeSemanticSearcher(searcher)
		return nil, err
	}
	retriever := researchsemantic.NewWithGenerationLease(provider, searcher, st, acquireGeneration)
	return b.WithSemanticRetriever(retriever, researchsemantic.Options{
		Profile: profile, Limit: ready.CandidateDepth, MaxChunks: exactMaxChunks,
		Timeout: researchsemantic.DefaultQueryTimeout,
	}), nil
}

func (r *Runtime) Build(ctx context.Context, opts Options) (Pack, error) {
	b, err := r.NewBuilderContext(ctx, opts.EffectiveSemanticMode, opts.UseSemantic, opts.DisableSemantic)
	if err != nil {
		return Pack{}, err
	}
	pack, buildErr := b.Build(ctx, opts)
	return pack, errors.Join(buildErr, b.Close())
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.closeOnce.Do(func() {
		buildsDrained := r.activeBuilds.shutdown()
		go func() {
			cacheErr := r.rootCache.Close()
			<-buildsDrained
			r.closeMu.Lock()
			r.closeErr = cacheErr
			r.closeMu.Unlock()
			close(r.drained)
		}()
	})
	select {
	case <-r.drained:
		r.closeMu.Lock()
		defer r.closeMu.Unlock()
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Drained() <-chan struct{} {
	if r == nil {
		drained := make(chan struct{})
		close(drained)
		return drained
	}
	return r.drained
}

func (r *Runtime) Close() error {
	return r.Shutdown(context.Background())
}

func (r *Runtime) registerRootSpec(key semanticruntime.RootKey, spec runtimeRootSpec) {
	r.rootSpecsMu.Lock()
	r.rootSpecs[runtimeRootScope(key)] = runtimeRootSpecState{key: key, spec: spec}
	r.rootSpecsMu.Unlock()
}

func (r *Runtime) rootSpec(key semanticruntime.RootKey) (runtimeRootSpec, bool) {
	r.rootSpecsMu.Lock()
	defer r.rootSpecsMu.Unlock()
	state, ok := r.rootSpecs[runtimeRootScope(key)]
	if !ok || state.key != key {
		return runtimeRootSpec{}, false
	}
	return state.spec, true
}

func runtimeGenerationLeaseAcquirer(ctx context.Context, st *store.Store, cfg config.Config) (researchsemantic.GenerationLeaseAcquirer, error) {
	if st == nil {
		return nil, errors.New("semantic generation lease requires a retrieval store")
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		return nil, fmt.Errorf("read semantic generation lease database ID: %w", err)
	}
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		resolved, err := config.Load(cfg.RootDir)
		if err != nil {
			return nil, fmt.Errorf("resolve semantic generation lease cache: %w", err)
		}
		cacheDir = resolved.CacheDir
	}
	scope, err := semanticlock.NewScope(cacheDir, databaseID)
	if err != nil {
		return nil, fmt.Errorf("construct semantic generation lease scope: %w", err)
	}
	return func(queryCtx context.Context) (researchsemantic.GenerationLease, error) {
		acquireCtx, cancelAcquire := context.WithTimeout(queryCtx, semanticRuntimeAdmissionTimeout)
		defer cancelAcquire()
		lease, err := scope.AcquireGenerationShared(acquireCtx, "owner=research-query\noperation=semantic-retrieval\n")
		if err != nil {
			return nil, err
		}
		return lease, nil
	}, nil
}

func closeRuntimeSemanticSearcher(searcher semanticindex.Searcher) {
	if closer, ok := searcher.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
