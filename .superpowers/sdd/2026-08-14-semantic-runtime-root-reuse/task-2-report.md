# Task 2 Implementation Report

## Boundary

- Repository: `/Users/darron/src/dbrain`
- Approved integrated base: `fdd642342a920fefdf37961f8a0859136bdac822`
- Task 2 implementation commit reviewed: `6513ec1330536ff5ea5bc7830f339e3d93e91030`
- Task 2 lifecycle review-fix commit: `9f3283109379aa2163f99a41fca0a649d32200d2`
- Scope: Task 2 runtime lifecycle, native lazy-root loading, retained generation leases, focused runtime tests, and this report only
- Preserved unchanged: the pre-existing `Taskfile.yml` modification and untracked `docs/superpowers/` work
- Not modified: Task 3/4 workflow, changelog, or documentation owners

## Implementation

- `brainresearch.Runtime` continues to own one `semanticruntime.Manager` for one `*store.Store` lifetime. Builder admission remains lazy and never calls `OpenUSearchRoot`.
- A cold native load still retains the query's already-acquired generation lease and runs under the manager-owned context. The caller has an independent five-second wait and does not cancel the detached native load.
- `runtimeLoadSemanticRoot` now creates the `LoadedSearcher` immediately after a successful native open. If the post-open readiness check fails or the authoritative root key changes, it returns that loaded searcher and close callback alongside the error. `semanticruntime.Manager` therefore owns discarded-root cleanup, records close failures, and returns them from runtime shutdown.
- Query-facing mismatch/readiness outcomes remain fail-open and path-free (`native_root_artifacts_unavailable` or `runtime_readiness_unavailable`) with no generation retry.
- The runtime's root load specifications are now bounded by cache/database/profile scope. Registering a new generation replaces the retired generation's specification, so repeated generation changes do not accumulate historical keys.
- A non-retainable `researchsemantic.GenerationLease` cannot start a cold native load. The production lazy-search path maps the manager's retained-guard requirement to path-free `native_root_artifacts_unavailable`, releases the query lease, and never invokes the loader.
- The retained detached-load test now drives the real `semanticgc.Run` apply path against an eligible generation artifact. GC obtains its maintenance lock but cannot obtain generation-exclusive or delete the artifact until the retained load lease closes; deletion succeeds afterward.
- Restored real tagged native coverage publishes a USearch segment/root and matching SQLite catalog, admits a runtime without opening it, moves a formerly distant member into L0, and verifies first-query validation/reranking/hydration plus warm-root reuse. The fixture uses 50 indexed rows so one deliberately stale native member remains within the production tombstone readiness threshold.
- The tagged positive test keeps `source:runtime-1` as the exact L0 result but no longer assumes USearch will return one particular approximate neighbor. It accepts any distinct current root member and verifies its current chunk ID/hash/text, hydrated evidence, semantic lane/backend/generation, and finite cosine distance.

## TDD Evidence

- The bounded-spec regression first failed with `root spec count=100 want=1 current scope after 100 generations`.
- The discarded-root shutdown test initially failed to compile because the manager-owned close seam did not yet exist.
- The first restored real native fixture correctly exposed the production readiness gate: a two-row root with one stale member exceeded the two-percent tombstone limit. The fixture was expanded to 50 indexed members, preserving the stale-candidate/L0 behavior while remaining production-searchable.
- After implementation, the focused discarded-root cleanup, non-retainable lease, real semantic GC exclusion, bounded-spec, and real native retrieval tests all pass.

## Verification

```text
task fmt
```

Result: passed (`go fmt ./...`).

```text
task lint
```

Result: passed with `0 issues`.

```text
env GOCACHE=/private/tmp/dbrain-task2-gocache CGO_ENABLED=0 \
  go test ./internal/brainresearch ./internal/semanticlock \
  ./internal/researchsemantic ./internal/semanticruntime \
  ./internal/semanticindex -count=1
```

Result: all five focused default-build packages passed. The first sandboxed attempt was blocked because unrelated existing tests could not bind localhost `httptest` ports; the identical command passed outside that network sandbox.

```text
env GOCACHE=/private/tmp/dbrain-task2-gocache \
  MACOSX_DEPLOYMENT_TARGET=12.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CGO_CFLAGS=-I/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/include \
  CGO_LDFLAGS=-L/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/lib \
  go test -tags=usearch -race \
  ./internal/brainresearch ./internal/semanticruntime ./internal/semanticindex \
  ./internal/semanticlock ./internal/researchsemantic \
  -run 'Test(Runtime|Manager|USearch|GenerationLeaseRetain|Retriever)' -count=1
```

Result: all five tagged packages passed with no race report. `internal/brainresearch` completed in 26.969s; the other packages completed in 1.619s to 2.371s.

Deterministic-test follow-up verification:

```text
env GOCACHE=/private/tmp/dbrain-task2-gocache \
  MACOSX_DEPLOYMENT_TARGET=12.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CGO_CFLAGS=-I/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/include \
  CGO_LDFLAGS=-L/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/lib \
  go test -tags=usearch -race ./internal/brainresearch \
  -run '^TestRuntimeUSearchIntegrationLazyLoadHydratesAndReusesRoot$' -count=5
```

Result: passed all five race-enabled runs in 12.288s.

## Remaining Limitation

- No Task 2 focused limitation remains. The full repository-wide `task test-ci` gate was not repeated in this focused REVISE loop: the prior Task 2 run already established an unrelated ambient failure in `internal/releaseautomation/TestUSearchPackagingTaskPolicy` caused by nested `.worktrees/*` discovery, and the user explicitly directed this loop not to wait on unrelated full-repository gates.
