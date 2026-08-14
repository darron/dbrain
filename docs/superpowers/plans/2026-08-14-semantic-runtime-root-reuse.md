# Semantic Runtime Root Reuse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make validated native semantic retrieval usable on the real local corpus by removing the 974 ms cold-root load from the 250 ms query-admission path, reusing loaded roots across queries, and preserving lexical fail-open behavior when native retrieval is unavailable.

**Architecture:** Add a runtime-scoped, process-local, reference-counted semantic-root cache with single-flight loading. Runtime builders will admit semantic retrieval from bounded SQLite readiness facts without opening native files; the native searcher will lazily obtain the current generation after the existing query generation lease is acquired. A cold root load receives a separate bounded caller-wait budget, while cache hits avoid native root loading entirely. A detached manager-owned load retains a reference to the already-acquired query generation lease through native import, validation, and the pre-publication disposition decision. Roots rejected before pending-cache admission close before guard release; pending roots that fail release or publication checks close afterward. The existing semantic-GC reader grace remains defense in depth, not the sole protection. One-shot CLI calls own a short-lived runtime, while research runners, evals, MCP servers, and remote handlers own one runtime for their process lifetime.

**Tech Stack:** Go; SQLite-authoritative semantic readiness; optional `usearch && cgo` native backend; existing `semanticlock` generation leases; Cobra CLI; MCP/HTTP server lifetimes; Taskfile native build and verification targets.

## Global Constraints

- SQLite remains authoritative; native roots and segments remain rebuildable derived caches.
- Keep the optional backend behind `usearch && cgo`; CGO-free and unsupported platforms must retain the existing lexical/exact fallback.
- Keep the 250 ms budget for readiness and generation-lock admission; do not increase it to cover native index import.
- Bound the cold semantic query's wait for native-root readiness with a separate internal initialization budget of 5 seconds, still limited by caller cancellation. A native `LoadBuffer` call is non-preemptible, so a timed-out waiter must not force-close or corrupt a detached import; the manager may finish that flight and populate the warm cache later.
- A detached manager-owned cold load retains a reference to the already-acquired shared generation lease through native import, validation, and the pre-publication disposition decision. A root rejected before pending-cache admission is closed before guard release; a pending root that fails release or publication checks is closed afterward. The existing immutable-root contract and ten-minute semantic-GC reader grace remain defense in depth. A fresh authoritative snapshot under the caller's query lease must match before candidates are searched; otherwise fail open without publishing stale evidence.
- A cache hit must never bypass the existing query-time shared-generation lease, SQLite validation, exact reranking, or hydration.
- A root is usable only after the existing manifest, payload checksum, provenance, snapshot, purge-epoch, dimension, backend, and membership checks succeed.
- A generation or descriptor change must never expose candidates from the previous root as current evidence.
- Cache entries are process-local and in-memory; this plan does not add a daemon, persistent native index format, or cross-process memory sharing.
- Semantic mode remains configured `off` by default; this change does not authorize global `on` rollout.
- No SQLite migration, upstream-source mutation, production database/cache mutation, or new third-party dependency is required.
- Preserve the user’s existing uncommitted `Taskfile.yml` `build-semantic` target and use it for native verification.

## Current Boundary and Evidence

The implementation target is the local development checkout `/Users/darron/src/dbrain`, branch `main` at reviewed baseline commit `3e79646afd181b8fbb7ae67fb24d3b0005930dea` (matching `origin/main` when reviewed), with the restored development database resolved through `direnv exec . ./bin/dbrain --no-debug config paths --json`. The observed semantic state is a native USearch root with 340,449 ready embeddings across six segments. Before implementation, recheck branch, `HEAD`, `git status`, the configured paths, and the affected lock/runtime contracts; if `HEAD` or the boundary changed, re-review this plan before coding.

The current failure is structural:

- `internal/brainresearch/runtime.go` applies `semanticRuntimeAdmissionTimeout = 250 * time.Millisecond` to readiness and native searcher construction.
- `internal/brainresearch/runtime_native_searcher_usearch.go` opens every native root under that same context and reports `generation_busy` when the cooperative import exceeds the budget.
- `internal/brainresearch/research.go` creates and closes a runtime for every top-level build; `internal/mcpserver/research.go` reaches that path for every MCP research request.
- The measured native root load on this corpus was approximately 974 ms, so ordinary semantic `on` requests fail open before ranking quality is exercised.
- The direct evaluation reused one loaded root and showed mixed quality: one clear SQLite-replication win, one neutral pipeline result, one weak/no-material-gain Cerebras result, and one Hermes result with a ranking regression from a metadata-only Reddit item.

The plan therefore separates runtime usability from ranking quality. After implementation, the same private corpus eval must be rerun on real CLI and long-lived paths before any default-on decision.

## File Map

### New files

- `internal/semanticruntime/cache.go` — build-tag-independent cache key, loader, ref-counted lease, single-flight state, retirement, and shutdown behavior.
- `internal/semanticruntime/cache_test.go` — deterministic fake-searcher tests for cache hits, single-flight loading, cancellation, retirement, close ordering, and concurrency.

### Modified files

- `internal/brainresearch/runtime.go` — introduce reusable `Runtime`, split readiness/lock admission from cold root loading, and wire the cache into runtime searcher construction.
- `internal/brainresearch/runtime_native_searcher_usearch.go` — replace eager root import with a lazy current-generation cache-backed searcher and map load failures to explicit semantic status reasons.
- `internal/brainresearch/runtime_native_searcher_default.go` — preserve the CGO-free unsupported-backend contract with no native cache work.
- `internal/brainresearch/types.go` — make builder/runtime ownership and close behavior explicit for shared versus transient runtimes.
- `internal/brainresearch/research.go` — keep the existing top-level `Build` API while routing it through a transient reusable runtime.
- `internal/brainresearch/runtime_test.go` — update dependency seams and add non-native/runtime ownership coverage.
- `internal/brainresearch/runtime_usearch_integration_test.go` — replace eager-open timeout expectations with lazy-load, cache-hit, root-key-mismatch, retained-lease, and shutdown tests.
- `internal/semanticindex/index.go` — add bounded, path-free status reasons for root-load timeout and query-time readiness failure.
- `internal/semanticlock/scope.go` — add retainable shared-generation lease references so a detached native import keeps the already-acquired guard alive without reacquiring behind a queued exclusive writer.
- `internal/researchsemantic/retriever.go` — expose the acquired generation lease to the searcher through request context and retain/release it around a detached cache load; preserve cancellation and lease-release error semantics.
- `internal/researchrun/run.go` — retain one `brainresearch.Runtime` across initial retrieval and the one bounded retry.
- `internal/researcheval/run.go` — retain one runtime across all cases so the eval records cold and warm behavior instead of reopening every root.
- `internal/researcheval/trace.go` — let trace replay use an owner-supplied runtime while preserving the transient compatibility wrapper for the CLI.
- `internal/mcpeval/run.go` and `internal/mcpeval/retrieval.go` — retain one MCP server/runtime across an eval report and close it once.
- `internal/mcpserver/server.go` and `internal/mcpserver/research.go` — own a reusable research runtime and expose idempotent server shutdown.
- `internal/mcpserver/transport_stdio.go` and `internal/mcpserver/http.go` — close the server/runtime with the transport lifecycle.
- `internal/remote/server_handler.go` — retain the MCP server object so remote cleanup closes its runtime cache.
- `web/server.go` — make the long-lived web handler own a reusable research runtime and expose an idempotent close hook without breaking the `http.Handler` constructor API.
- `web/research_handlers.go` — route web research requests through the server-owned runtime instead of the transient top-level build wrapper.
- `web/research_run_handlers.go` and `web/research_trace_handlers.go` — inject the web server's shared runtime into runner and trace-replay paths so those requests do not recreate a root cache.
- `docs/semantic-retrieval.md` — document lazy loading, warm reuse, separate cold-load timeout, the query-lease-versus-reader-grace boundary, and failure reasons.
- `COMMANDS.md` — update the semantic retrieval runtime contract and user-visible failure semantics.
- `MCP.md` and `skills/dbrain-mcp/SKILL.md` — align MCP/operator guidance with actual warm/cold status behavior.
- `README.md` — update the user-facing 250 ms readiness versus lazy native cold/warm behavior description.
- `CHANGELOG.md` — record the runtime behavior change under the actual implementation/merge date heading.
- `Taskfile.yml` — preserve the user-added `build-semantic` target, add the runtime package to native race coverage, and keep the tagged verification commands reusable.

## Task 1: Build the Reference-Counted Root Cache

**Files:**

- Create: `internal/semanticruntime/cache.go`
- Create: `internal/semanticruntime/cache_test.go`

**Interfaces:**

- Consumes: `semanticindex.Searcher`, a native-searcher close callback, and a caller-supplied root key.
- Produces: a runtime-scoped, process-local `Manager` whose acquired `Lease` implements `semanticindex.Searcher` and whose `Close` releases only that lease reference.

Define the cache contract around the complete validated root identity, not only `generation_id`:

```go
type RootKey struct {
	CacheDir         string
	DatabaseID       string
	ProfileID        string
	GenerationID     string
	SnapshotRevision int64
	PurgeEpoch       int64
	BackendVersion   string
	DescriptorSHA256 string
}

type LoadedSearcher struct {
	Searcher semanticindex.Searcher
	Close    func() error
}

type Loader func(context.Context, RootKey) (LoadedSearcher, error)
type RetainLoadGuard func() (release func() error, err error)

type Manager struct { /* mutex, keyed entries, single-flight state, shutdown context, load timeout */ }

func New(loader Loader, loadWaitTimeout time.Duration) *Manager
func (m *Manager) Acquire(context.Context, RootKey, RetainLoadGuard) (*Lease, error)
func (m *Manager) Shutdown(context.Context) error
func (m *Manager) Close() error

type Lease struct { /* one reference to one immutable cache entry */ }

func (l *Lease) Search(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error)
func (l *Lease) Close() error
```

Implement these invariants:

- Identical keys share one loaded searcher; concurrent misses single-flight one loader call.
- The first cold caller supplies `RetainLoadGuard`; the manager retains the already-acquired shared-generation lease before detaching the load and releases it exactly once after import, validation, and the pre-publication disposition decision. A root rejected before pending-cache admission closes before release; a pending root that fails release or publication checks closes afterward. It never reacquires generation-shared inside the loader, so a queued exclusive writer cannot deadlock with a nested reader.
- A caller waiting for an in-flight load may cancel its own wait without canceling the shared manager-owned load. The manager runs that load with its own runtime-shutdown context. `loadWaitTimeout` bounds the caller's wait, not a forced native cancellation: a timed-out waiter receives a typed timeout result while a non-preemptible native import may finish later and populate the warm cache. `Manager.Shutdown` cancels cooperative work; it never force-closes an in-use native index.
- A load that finishes after manager shutdown closes and discards its native searcher. A load that finishes after its caller's wait timed out may still publish only after the retained generation guard and all validation checks succeed.
- A newer key for the same cache/database/profile retires older entries. Retired entries close immediately at zero references or after the final lease closes; the manager never accumulates one native root per historical generation.
- `Lease.Search` takes an operation reference before checking/using the searcher and releases it after the serialized native call returns. `Lease.Close` is idempotent, rejects new searches, and defers dropping its entry reference until operations already started through that lease finish.
- Retirement and manager shutdown use the same operation gate. The underlying `LoadedSearcher.Close` runs only after the entry is retired, all lease references are gone, and all active search operations have returned; it never races a native search or import.
- Native searches are serialized per cached entry until a tagged integration test proves the linked USearch version’s concurrent search contract. This prevents a shared index from being closed or used concurrently through an unspecified C API contract.
- `Manager.Shutdown(ctx)` rejects new acquisitions/builds, cancels cooperative in-flight work, waits for active operations/imports to drain until `ctx`, closes all eligible searchers, and returns joined close errors. On deadline it leaves a draining entry plus an asynchronous reaper; callers must keep the owning store alive until the runtime's drain signal. `Manager.Close` is the compatibility path for already-drained one-shot owners and may wait without a deadline.

- [x] **Step 1: Write cache contract tests before implementation.** Cover `TestManagerSharesIdenticalRoot`, `TestManagerSingleFlightsConcurrentMisses`, `TestManagerWaiterCancellationDoesNotCancelSharedLoad`, `TestManagerRetainsGenerationGuardUntilDetachedLoadFinishes`, `TestManagerRetiresOlderGenerationAfterLastLease`, `TestManagerRejectsAcquireAfterShutdown`, `TestLeaseCloseConcurrentWithSearchDefersUnderlyingClose`, `TestRetirementConcurrentWithSearchClosesAfterSearch`, `TestManagerShutdownDuringLoadStartsReaper`, and `TestManagerClosesLoadedSearcherAfterLastLeaseOnShutdown` using fake searchers, a fake retainable generation guard, and loader call counters.

- [x] **Step 2: Run the focused tests and verify they fail for the missing manager.**

Run:

```sh
go test ./internal/semanticruntime -run 'TestManager' -count=1
```

Expected: compile failure until the cache types and test fixture are introduced; after the implementation step, all named manager tests must pass under `-race`.

- [x] **Step 3: Implement the cache state machine.** Keep the loader outside the manager mutex, retain the caller's already-acquired generation guard before starting a detached flight, run the loader with the manager-owned shutdown context, publish a successful entry exactly once, close discarded loads, and use per-entry reference counts plus an operation gate to make root close safe. Do not pass the initiating request context to the loader; use that context only for the caller's wait budget and cancellation.

- [x] **Step 4: Run the focused race test.**

Run:

```sh
go test -race ./internal/semanticruntime -run 'TestManager' -count=1
```

Expected: all cache tests pass with no race reports, exactly one loader invocation for concurrent identical misses, exactly-once guard release, and no underlying close until the final active operation returns.

## Task 2: Move Native Root Loading Behind Lazy Runtime Search

**Files:**

- Modify: `internal/brainresearch/runtime.go`
- Modify: `internal/brainresearch/runtime_native_searcher_usearch.go`
- Modify: `internal/brainresearch/runtime_native_searcher_default.go`
- Modify: `internal/brainresearch/types.go`
- Modify: `internal/brainresearch/research.go`
- Modify: `internal/semanticindex/index.go`
- Test: `internal/brainresearch/runtime_test.go`
- Test: `internal/brainresearch/runtime_usearch_integration_test.go`

**Interfaces:**

- Consumes: the `semanticruntime.Manager`, current `semanticreadiness.Snapshot`, the existing `semanticlock` scope, and `OpenUSearchRoot`.
- Produces: a reusable `brainresearch.Runtime` and a lazy `semanticindex.Searcher` whose first search loads the current validated root and whose later searches reuse it.

Add the reusable runtime ownership boundary:

```go
type Runtime struct {
	cfg       config.Config
	st        *store.Store
	rootCache *semanticruntime.Manager
	activeBuilds runtimeBuildTracker
	closeOnce sync.Once
	closeErr  error
}

func NewRuntime(config.Config, *store.Store) *Runtime
func (r *Runtime) NewBuilderContext(context.Context, semanticconfig.Mode, bool, bool) (*Builder, error)
func (r *Runtime) Build(context.Context, Options) (Pack, error)
func (r *Runtime) Shutdown(context.Context) error
func (r *Runtime) Drained() <-chan struct{}
func (r *Runtime) Close() error
```

The generation-lock seam must be retainable without changing the existing owner-facing `Close` behavior: `semanticlock.Lease.Retain()` returns an independent idempotent reference whose release closes the kernel lease only after the original owner and every retained reference are done. `researchsemantic` places the acquired `GenerationLease` in request context before calling `Searcher.Search`; the native lazy searcher extracts the optional retainable reference and passes it to `semanticruntime.Manager.Acquire` as `RetainLoadGuard`. A fake/non-retainable lease in lexical or unit-only tests never starts a native detached load. This retains the lease already held by the request and never reacquires generation-shared behind a queued exclusive writer.

Keep `NewRuntimeBuilder`, `NewRuntimeBuilderContext`, and top-level `Build` as compatibility wrappers. A wrapper-created runtime is owned by the returned builder or top-level call; a caller-created `Runtime` remains open across multiple builds. `Builder.Close` must release its retriever lease before closing an owned transient runtime, while a shared runtime’s cache remains available to the next build.

Change runtime admission as follows:

1. Continue using the 250 ms context for SQLite readiness and capability admission.
2. Do not call `OpenUSearchRoot` while constructing the builder.
3. On native builds, construct a lazy searcher carrying the store, canonicalized cache directory, profile, exact cap, readiness reader, and root cache. A runtime manager is never shared across different `*store.Store` lifetimes, even when their paths happen to match.
4. When `researchsemantic.Retriever` calls the searcher, it has already acquired the existing shared generation lease and places a retainable reference in the request context. Read a fresh bounded readiness snapshot, canonicalize `CacheDir`, derive `RootKey`, and acquire a cache lease. A cold acquisition must retain the context lease before detaching its load; it must not reacquire generation-shared.
5. On a cache miss, the manager invokes `OpenUSearchRoot` under the manager-owned runtime shutdown context. The separate `semanticRuntimeRootLoadWaitTimeout = 5 * time.Second` bounds how long the request waits for the cache entry; it does not force-cancel a non-preemptible native `LoadBuffer` call. The retained shared-generation lease stays alive through import, validation, and the pre-publication disposition decision. A root rejected before pending-cache admission closes before release; a pending root that fails release or publication checks closes afterward. If a waiter times out, the request fails open while the single-flight load may finish and populate the warm cache for a later query.
6. After a successful load, re-read the authoritative readiness snapshot while the caller's shared generation lease is still held. Publish and search through the cache lease only when the key still matches. The native searcher performs candidate validation, exact reranking, and L0 merge; the `researchsemantic.Retriever` performs chunk hydration after search, as it does now.
7. A manifest, descriptor, snapshot, purge-epoch, backend, dimension, checksum, or provenance mismatch is an artifact failure, not expected query-time drift: discard the loaded root, do not publish it, and fail open. SQLite candidate compare-and-set/current-generation validation remains the final stale-evidence defense. There is no generation retry while a compliant shared lease is held.

Use distinct status reasons:

- `generation_busy`: the existing 250 ms generation-lease acquisition failed.
- `root_load_timeout`: the caller's wait for a cold root exceeded the separate 5-second initialization budget; this does not claim that a non-preemptible detached native import stopped.
- `native_root_artifacts_unavailable`: manifest, payload, checksum, provenance, native import, or root-key validation failed for a non-timeout reason.
- `runtime_readiness_unavailable`: the fresh query-time authoritative readiness snapshot could not be read or did not provide a stable searchable profile.

These reasons must remain path-free and must not expose cache paths, database identifiers, or native diagnostic strings. `root_load_timeout`, `native_root_artifacts_unavailable`, and `runtime_readiness_unavailable` return `StateUnavailable` with empty semantic hits and no ordinary error so hybrid retrieval preserves the lexical result; actual caller cancellation/deadline remains an error with the existing cancellation reason.

- [x] **Step 1: Add runtime/cache dependency seams and the new timeout/status constants.** Keep the existing `openRuntimeUSearchRoot` test seam and add a manager/loader seam so tagged tests can count imports without touching real corpus files. Add the retainable lease/context seam in `internal/semanticlock/scope.go` and `internal/researchsemantic/retriever.go`, and track active runtime builds so shutdown can reject new work and signal when the store is safe to close. Centralize `native_root_artifacts_unavailable` as one status-reason definition rather than adding another independent string beside the existing sentinel/diagnostic uses. Add a production-path assertion that the runtime generation lease is retainable before a native detached load can begin; a missing retainer must fail closed as `native_root_artifacts_unavailable` with no ordinary error.

- [x] **Step 2: Write the failing runtime tests.** Add or update tests named `TestRuntimeBuilderDoesNotOpenNativeRootDuringAdmission`, `TestRuntimeLazySearcherLoadsRootOnFirstSearch`, `TestRuntimeLazySearcherReusesWarmRoot`, `TestRuntimeLazySearcherRejectsMismatchedRoot`, `TestRuntimeRootLoadWaitTimeoutFailsOpenWithExplicitReason`, `TestRuntimeReadinessFailureFailsOpenWithPathFreeReason`, `TestRuntimeDetachedLoadRetainsGenerationLease`, `TestRuntimeOwnedCacheClosesAfterBuilderClose`, `TestRuntimeShutdownRejectsNewBuilds`, and `TestRuntimeLazyRootFailureFailsOpenForShadowAndOn`. The end-to-end shadow/on assertions must preserve lexical evidence, expose only the named path-free reason, and keep provider/root diagnostics out of JSON. Update the old eager-open tests so they cover the retained-lease cold-load contract, including a tagged blocked-native-search shutdown test and a generation-exclusive/GC attempt that cannot delete the retained root files.

- [x] **Step 3: Run the tagged focused tests before implementation.**

Run:

```sh
task usearch-static-darwin-arm64
MACOSX_DEPLOYMENT_TARGET=12.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/include" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/lib" \
  go test -tags=usearch -c ./internal/brainresearch
MACOSX_DEPLOYMENT_TARGET=12.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/include" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/lib" \
  go test -tags=usearch ./internal/brainresearch -run 'TestRuntime' -count=1
```

Expected: the new tests fail against eager root admission or missing runtime/cache seams; existing cancellation, artifact-error, and lease-release tests continue to identify the old contracts that need deliberate updates.

- [x] **Step 4: Implement lazy search and runtime ownership.** Ensure a cache hit performs no root file read; ensure a miss performs one validated import with the manager-owned context while retaining the already-acquired generation lease; ensure a caller cancellation remains a cancellation while ordinary native load/provider failures fail open through the existing research-harness path. The cold path intentionally holds a shared generation lease through import, validation, and the pre-publication disposition decision; document and test that this can delay an exclusive refresh/GC writer for that bounded portion, with manager-owned cleanup and reader grace retained as defense in depth.

- [x] **Step 5: Run the tagged focused tests and race coverage.**

Run:

```sh
go test -tags=usearch -race ./internal/brainresearch ./internal/semanticruntime ./internal/semanticindex -run 'Test(Runtime|Manager|USearch)' -count=1
```

Expected: cold load succeeds within the caller-wait budget or reports the explicit timeout while its single-flight may continue, a warm repeat does not invoke the loader again, mismatched roots never publish stale evidence, and all close/cancellation paths pass without races.

## Task 3: Reuse One Runtime Across Each Long-Lived Workflow

**Files:**

- Modify: `internal/researchrun/run.go`
- Modify: `internal/researcheval/run.go`
- Modify: `internal/mcpeval/run.go`
- Modify: `internal/mcpeval/retrieval.go`
- Modify: `internal/mcpserver/server.go`
- Modify: `internal/mcpserver/research.go`
- Modify: `internal/mcpserver/transport_stdio.go`
- Modify: `internal/mcpserver/http.go`
- Modify: `internal/remote/server_handler.go`
- Modify: `internal/remote/server.go`
- Modify: `web/server.go`
- Modify: `web/research_handlers.go`
- Modify: `web/research_run_handlers.go`
- Modify: `web/research_trace_handlers.go`
- Test: `internal/researchrun/*_test.go`, `internal/researcheval/*_test.go`, `internal/mcpeval/*_test.go`, `internal/mcpserver/*_test.go`, `internal/remote/*_test.go`, `web/*_test.go`

**Interfaces:**

- Consumes: `brainresearch.NewRuntime`, `Runtime.Build`, `Runtime.Shutdown`, and the optional shared-runtime injection on `researchrun.Options`.
- Produces: one cache lifetime per runner/eval/server, with no change to request-level semantic mode flags or MCP wire schemas.

Wire ownership explicitly:

- `researchrun.Run` uses an optional owner-supplied runtime when present; otherwise it creates one before `newRunner`, stores it on `runner`, routes `buildPack` through `runtime.Build`, and shuts it down in the outer `Run` cleanup. Initial retrieval and the bounded retry therefore share the root, while the web server and eval runner can share their owner runtime rather than creating another.
- `researcheval.Run` creates one runtime for the whole case list and passes it to both direct and runner cases. The eval report remains private/local; this only changes reuse and timing.
- `mcpeval.Run` creates one `mcpserver.Server` for the whole report, passes it to research-pack cases, and closes it once. Non-research `ask` cases remain unchanged.
- `mcpserver.Server` owns a `*brainresearch.Runtime` through a shared pointer lifecycle object; `BuildResearchPack` calls that runtime instead of the top-level per-request wrapper. Add idempotent `Close()` and bounded `Shutdown(context.Context)`. `withTransportCapabilities` copies the server value but never copies the lifecycle mutex/once/wait state.
- The web `server` owns a `*brainresearch.Runtime`; `handleResearch`, `handleResearchRun`, and trace-current harnesses call that runtime or inject it into `researchrun.Run` instead of using transient builders. Keep `NewHandlerWithOptions`'s `http.Handler` API by returning a close-capable wrapper that delegates requests to the guarded mux and exposes the server's idempotent `Close() error` and bounded `Shutdown(context.Context)`.
- `Serve` and `ServeHTTP` defer handler/server close after the owning store-close defer is registered. Remote `buildHandler` retains both close-capable web and MCP server objects and returns an error-aware cleanup function; `serveWithDeps` joins runtime/server close errors into its return. Cleanup closes the runtime/cache before the SQLite store. The same ordering applies to the MCP stdio and HTTP transports: drain handlers, call bounded server/runtime shutdown, and close `st` only after the runtime drain signal. On a shutdown deadline, an asynchronous reaper retains the store until native work is safe to close it.
- Direct `brainresearch.Build` and the ordinary one-shot CLI remain compatible: they create one transient runtime for that invocation, so the first semantic query may incur one bounded cold load and the process exits afterward.

- [x] **Step 1: Add lifecycle tests before wiring.** Prove that two builds through one runtime invoke the native loader once, two builds through separate runtimes invoke it twice, an owner-supplied runtime is reused by web runner requests, a runner retry shares the runtime, `withTransportCapabilities` clone/close is idempotent, cleanup ordering keeps the store alive until runtime drain, and close/shutdown errors are surfaced.

- [x] **Step 2: Run the focused lifecycle tests.**

Run:

```sh
go test ./internal/researchrun ./internal/researcheval ./internal/mcpeval ./internal/mcpserver ./internal/remote -run 'Runtime|Research|MCP|Handler|Eval' -count=1
```

Expected: the new lifecycle assertions fail until the runtime is threaded through each owner; existing MCP response schemas and remote cleanup tests remain otherwise unchanged.

- [x] **Step 3: Implement runner/eval/server ownership.** Preserve all existing context cancellation and transport shutdown behavior. Define one named web close-capable handler interface (the existing `http.Handler` plus `Close() error` and bounded `Shutdown(context.Context) error`) so `ServeWithOptions` and `remote/server_handler.go` type-assert the same contract. Enforce the lifecycle invariant that every runtime/cache fully drains builds, leases, imports, and native searches before its owning `*store.Store` closes. `buildHandler` returns an error-aware cleanup function, and every close/shutdown error is joined with the workflow/transport error rather than silently discarded where the caller already has an error-return path. A shutdown deadline never force-closes an in-use native root; the reaper closes the runtime and store after its drain signal.

- [x] **Step 4: Run focused lifecycle and race tests.**

Run:

```sh
go test -race ./internal/researchrun ./internal/researcheval ./internal/mcpeval ./internal/mcpserver ./internal/remote -count=1
```

Expected: all workflow lifetimes close exactly once, concurrent MCP/web requests share the warm cache safely, clone close state is shared rather than copied, cleanup errors are visible, and no request retains a runtime reference after its server/runner is closed.

## Task 4: Make Runtime Diagnostics and Documentation Truthful

**Files:**

- Modify: `internal/semanticindex/index.go`
- Modify: `docs/semantic-retrieval.md`
- Modify: `COMMANDS.md`
- Modify: `MCP.md`
- Modify: `skills/dbrain-mcp/SKILL.md`
- Modify: `CHANGELOG.md`

**Interfaces:**

- Consumes: the final cache/load status reasons and lifecycle semantics from Tasks 1–3.
- Produces: stable operator guidance that distinguishes admission contention, cold-root import wait timeout, artifact failure, and query-time readiness instability.

Document the resulting sequence explicitly:

```text
readiness snapshot (250 ms)
        |
        v
builder admits lazy semantic lane
        |
query embeds -> shared generation lease (250 ms acquisition budget)
        |
        +--> warm root: cache lease -> native search -> SQLite validation
        |
        +--> cold root: retained lease -> native import (5 s wait budget) -> cache lease -> search
```

State that the cold path can add approximately the root-import duration to the first semantic query, while subsequent queries in the same runtime reuse the validated in-memory root. State that a cache miss is not evidence of semantic quality: the lane is `used` only after native candidates pass SQLite validation and exact reranking. Explicitly acknowledge the liveness trade-off: a cold import retains shared generation protection, so an exclusive refresh/GC writer can wait for that import; the five-second value bounds the ordinary caller wait, not a non-preemptible C call. Reader grace remains defense in depth.

Update operator instructions so they no longer say that `generation_busy` can mean a slow root open. Add the new path-free reasons to the retrieval/MCP guidance, retain the lexical fail-open contract, distinguish readiness/artifact instability from ordinary lock admission, and keep the explicit warning that semantic ranking quality must be established by corpus-specific evals.

- [x] **Step 1: Update the status-reason constants and output tests.** Add assertions for the new reasons and ensure JSON remains backward-compatible for existing `generation_busy`, provider, cancellation, and artifact failures.

- [x] **Step 2: Update docs and the dated changelog entry.** Remove stale claims that root import is part of the 250 ms builder-admission budget; document the 5-second caller-wait budget, process-local runtime cache, retained-lease shutdown, and GC safety contract. Rewrite the lock-order paragraph in `docs/semantic-retrieval.md` to acknowledge that cold import retains shared generation protection and can delay an exclusive refresh/GC writer, while reader grace remains defense in depth. Update the exact false sentence in `skills/dbrain-mcp/SKILL.md` that says “ANN, other providers, background sync, and default-on are not available”; native segmented USearch is available when the tagged backend is built, while the remaining availability/default-on claims must be stated separately. Date the changelog entry with the actual implementation/merge date, not the plan-authoring date.

- [x] **Step 3: Verify documentation consistency.**

Run:

```sh
rg -n 'slow root open|root import|generation_busy|250 ms|root_load_timeout|runtime_readiness_unavailable' README.md docs/semantic-retrieval.md COMMANDS.md MCP.md skills/dbrain-mcp/SKILL.md CHANGELOG.md
```

Expected: every remaining `generation_busy` reference describes lock admission only; timeout, readiness, artifact, and retained-lease behavior is documented consistently; no review debris remains.

## Task 5: Verify Native, Default, and Corpus Behavior

**Files:**

- Verify: `Taskfile.yml`
- Verify: `evals/local/semantic-retrieval.json` and generated ignored results under `evals/local/`
- Verify: all files changed by Tasks 1–4

**Interfaces:**

- Consumes: the completed runtime/cache implementation and current private local eval corpus.
- Produces: evidence that native warm reuse works, default builds remain unaffected, and the quality decision is not conflated with runtime availability.

- [x] **Step 1: Run default focused tests.**

Run:

```sh
CGO_ENABLED=0 go test ./internal/semanticruntime ./internal/brainresearch ./internal/researchrun ./internal/researcheval ./internal/mcpeval ./internal/mcpserver ./internal/remote -count=1
```

Expected: default/CGO-free builds compile and pass without importing native packages or requiring Ollama/USearch.

- [x] **Step 2: Run repository formatting and lint.**

Run:

```sh
task fmt
task lint
```

Expected: both exit successfully with no formatting diff and no lint findings.

- [x] **Step 3: Run the clean CI-like gate.**

Run:

```sh
task test-ci
```

Expected: the complete clean-environment race/coverage gate passes. The ambient checkout currently contains unrelated `.worktrees/` directories that the repository-wide USearch packaging policy test can discover, so do not delete them or treat that environmental failure as an implementation result. If the ambient gate fails for that reason, create a worktree-free verification clone containing the final implementation snapshot and rerun the same gate:

```sh
clean_root="$(mktemp -d)/dbrain"
git clone --no-local --no-hardlinks "$(git rev-parse --show-toplevel)" "$clean_root"
(cd "$clean_root" && task test-ci)
```

The clean-clone run is the acceptance gate for packaging policy; record the ambient `.worktrees/` contamination separately if it remains.

- [x] **Step 4: Build and verify the native binary.**

Run:

```sh
task test-usearch-darwin-arm64
task build-semantic
task verify-usearch-darwin-arm64
```

Expected: `bin/dbrain` is a tagged arm64 native build with the pinned USearch 2.26.0 static linkage, and the capability check reports `supported_ready`.

Add `./internal/semanticruntime` to `Taskfile.yml`'s `USEARCH_RACE_PACKAGES` so the cache operation/close safety tests also run in the tagged race suite, even though `internal/semanticruntime/cache.go` itself remains build-tag-independent.

- [x] **Step 5: Run a real cold one-shot query.**

Run from the verified dev boundary:

```sh
env DBRAIN_RESEARCH_SEMANTIC_MODE=on \
  DBRAIN_RESEARCH_SEMANTIC_PROVIDER=ollama \
  DBRAIN_RESEARCH_SEMANTIC_MODEL=embeddinggemma:300m-bf16 \
  DBRAIN_RESEARCH_SEMANTIC_DIMENSIONS=768 \
  direnv exec . ./bin/dbrain --no-debug research \
  "SQLite replication comparison" --semantic --retrieval-only --json
```

Expected: the request may take approximately the measured cold-load duration, but the semantic retrieval lane reports `used`, not `generation_busy`, and every returned native candidate is still SQLite-validated. If root import exceeds 5 seconds, the request remains lexical with `root_load_timeout`.

- [x] **Step 6: Run the private multi-case eval to prove real semantic lane use.**

Run:

```sh
env DBRAIN_RESEARCH_SEMANTIC_MODE=on \
  DBRAIN_RESEARCH_SEMANTIC_PROVIDER=ollama \
  DBRAIN_RESEARCH_SEMANTIC_MODEL=embeddinggemma:300m-bf16 \
  DBRAIN_RESEARCH_SEMANTIC_DIMENSIONS=768 \
  direnv exec . ./bin/dbrain --no-debug eval research \
  --file evals/local/semantic-retrieval.json --json
```

Expected: semantic shadow/on cases no longer all fail with `generation_busy`; the report shows semantic `used` status after the cold case and later cases can observe the warm path. Loader-count integration tests, not the corpus report alone, prove root reuse. The private report must retain separate off/shadow/on results and must not be committed.

The 5-second root-load budget must leave headroom inside `researchsemantic.DefaultQueryTimeout` (currently 15 seconds): cold import plus embedding and hydration must remain comfortably below the outer timeout. If a local model makes that inequality false, report the model/provider timing separately rather than treating the result as evidence that the native root cache is unusable.

- [x] **Step 7: Reclassify ranking quality after runtime is fixed.** Compare the same four questions and source-key lists against the prior direct results. Keep the decision `do_not_enable_on_globally_yet` unless a fresh corpus review shows the metadata-only false positive is resolved and the semantic lane has repeatable net benefit.

## Execution Results (2026-08-14)

The approved plan was implemented in the local development checkout on `main`.
The verified runtime boundary is `/Users/darron/src/dbrain`, with
`direnv exec . ./bin/dbrain --no-debug config paths --json` resolving the local
database to `data/brain.db` and the semantic cache under the checkout's `cache`
directory. The restored corpus reports 340,449 ready embeddings across six
segments.

Native/runtime acceptance passed:

- The tagged arm64 binary built with `task build-semantic`; `task verify-usearch-darwin-arm64` reported USearch 2.26.0, backend `usearch`, and capability `supported_ready`.
- The real one-shot query `SQLite replication comparison` returned both lexical and semantic lanes with semantic status `used`, SQLite validation/reranking completed, and no `generation_busy`. A separate process is short-lived, so repeated standalone CLI invocations are not treated as warm-cache evidence.
- The private four-query eval at `evals/local/semantic-retrieval.json` ran 12 off/shadow/on cases in one long-lived eval runtime: `passed=12`, `failed=0`, `duration_ms=424905`. Every shadow/on case reported semantic `used`; none reported `generation_busy`. The raw JSON result remains local and uncommitted.
- Default/CGO-free focused tests, the full `task test-ci` race/coverage gate, `task fmt`, `task lint`, the tagged native full/race suite, and `git diff --check` passed. The release-policy test now skips repository-managed `.worktrees/` while still checking the real package set.

The fresh ranking comparison remains mixed, so runtime usability and ranking
quality stay separate:

- `SQLite replication comparison`: semantic retrieval added plausible direct alternatives including Marmot, Litestream, sqledge, Rails-on-SQLite, and celld material that lexical retrieval missed. This is the clearest net benefit.
- `Cerebras`: semantic retrieval broadened results toward ontology/knowledge-base material. Those additions are plausible but not yet a decisive quality win.
- `pipeline`: the semantic candidate set added noisy unrelated items, including political X/source material, although the fused top results remained mostly relevant.
- `Hermes`: release-note and Hermes-related results were useful, but a metadata-only `Reddit - The heart of the internet` item was a likely false positive.

Decision: `do_not_enable_on_globally_yet`. The runtime fix is operational and
safe to carry forward, but the current model/ranking behavior does not yet
justify enabling semantic retrieval by default across future versions and
platforms.

### Claude code review follow-up

The adversarial Claude review of `b4edc87` found no cache-state-machine blocker,
but identified two lifecycle gaps. Both were verified against the code and
fixed:

- Web trace comparison now calls `researcheval.DiffTraceWithRuntime` with the
  server-owned runtime, so trace replay no longer rebuilds a transient cache on
  every request. `TestWebTraceCompareUsesServerRuntime` fails against the old
  wiring and passes with the fix.
- `Runtime.Shutdown` now waits for active builds before closing the root cache.
  `TestRuntimeShutdownDrainsBuildBeforeClosingRootCache` fails against the old
  ordering and passes with the fix.

Focused tests, the full default gate, and the tagged native suite were rerun
after these fixes; a focused Claude confirmation was requested against the
revised checkout.

## Acceptance Criteria

- A native root is opened at most once per `(runtime, database, profile, generation, descriptor, snapshot, purge epoch)` key while the entry remains current.
- A warm semantic query performs no native root filesystem import and does not report `generation_busy` merely because root loading would have exceeded 250 ms.
- A cold one-shot CLI query can use semantic retrieval when the caller's root wait completes within 5 seconds; its first-query latency includes that initialization cost and later queries in the same runtime do not. A timed-out waiter may still benefit from the detached single-flight completing later.
- A detached cold import retains the already-acquired generation guard through import, validation, and the pre-publication disposition decision, so an exclusive refresh/GC writer may wait for that portion; manager-owned cleanup continues after release when required. The 5-second value bounds ordinary caller wait, not a non-preemptible native call. Reader grace remains defense in depth, and shutdown never force-closes an in-use root.
- Concurrent identical cold requests single-flight one root import, and cancellation of one waiter does not cancel the shared import needed by other live requests.
- Root retirement and runtime shutdown never close a native index while a lease is searching; close errors are surfaced through the owning workflow where an error path exists.
- Descriptor, snapshot, purge-epoch, checksum, provenance, readiness, and root-load wait failures cannot produce stale or unvalidated evidence; they fail open with bounded path-free reasons. A compliant generation lease makes a normal generation-change retry unnecessary.
- Existing lexical output remains unchanged when semantic mode is off, when semantic mode is shadow, when native support is unavailable, and when semantic/provider/root retrieval fails.
- Default/CGO-free builds compile without USearch and retain the existing `native_backend_unavailable` behavior for active native generations.
- The public docs, MCP skill guidance, command reference, and changelog describe the same cold/warm and failure semantics.
- Native packaging and clean CI gates pass, lifecycle/cache tests prove safe warm reuse and shutdown, and the private corpus eval demonstrates actual semantic lane use before any global-on rollout is reconsidered.

## Non-Goals and Deferred Work

- No change to RRF weights, candidate depth, embedding model, metadata filtering, parent diversity, or ranking policy.
- No attempt to make the current semantic ranking globally better as part of the runtime fix; quality remains a separate eval gate.
- No persistent on-disk memory map or shared native-root daemon across CLI processes.
- No automatic semantic default-on rollout, automatic eval-threshold policy, or production activation.
- No broad semantic GC redesign; the in-memory root is safe after validated import, cold import retains the already-acquired generation lease for safety, and reader grace remains defense in depth.

## Implementation Handoff

The user approved this plan and the tasks were executed in order on the local
development checkout. The implementation is committed on `main`; the approved
plan and this execution record remain as the final handoff document. Private
corpus eval output stays ignored and local, while the summary above records the
acceptance evidence and the decision not to enable semantic retrieval globally.
