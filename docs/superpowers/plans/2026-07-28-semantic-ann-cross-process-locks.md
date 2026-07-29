# Semantic ANN Cross-Process Concurrency Implementation Plan

> Execute this stacked PR with task-scoped implementation and two-stage review.

**Goal:** Make authoritative corpus writes, semantic maintenance, generation
activation/deletion, and admitted semantic queries safe across processes without
holding a coarse lock across network-bound sync work.

**Architecture:** Extend `internal/runlock` with context-cancellable
shared/exclusive leases and crash-recoverable FIFO writer intent. Build
database-scoped `maintenance` and `generation` lock families under the semantic
cache. Hold shared maintenance only around authoritative SQLite transactions,
exclusive maintenance around one bounded refresh unit, and shared generation
through semantic candidate validation and hydration. Enforce maintenance-before-
generation ordering and purge-epoch compare-and-swap fences.

**Stack:** Base this PR on the completed universal sync integration branch.
Native release packaging and installed-corpus acceptance remain later PRs.

---

## Task 1: Fair shared/exclusive run locks

**Files:**

- Create `internal/runlock/mode.go`
- Create `internal/runlock/intent.go`
- Create `internal/runlock/local_gate.go`
- Create `internal/runlock/subprocess_test.go`
- Modify `internal/runlock/lock.go`
- Modify `internal/runlock/lock_unix.go`
- Modify `internal/runlock/lock_windows.go`
- Modify `internal/runlock/lock_test.go`

Add:

```go
type Mode uint8

const (
    Shared Mode = iota + 1
    Exclusive
)

type AcquireOptions struct {
    Mode     Mode
    Metadata string
}

func AcquireContext(ctx context.Context, path string, options AcquireOptions) (*Lock, error)
```

Preserve `Acquire(path, metadata)` as the current immediate exclusive attempt.
Use nonblocking OS lock attempts under the caller context. Add a same-process
gate because advisory OS locks alone do not define useful ordering among
goroutines in one process.

Implement writer preference with a coordinator lease and monotonically ordered
ticket files. An exclusive waiter retains an OS lease on its ticket for its
queued and held lifetime. A shared waiter checks intent, acquires shared, then
rechecks intent so it cannot barge through the check/acquire race. Remove an
abandoned ticket only while holding the coordinator and after successfully
acquiring the ticket's OS lease.

Tests must prove shared/shared coexistence, exclusive exclusion, context
cancellation, FIFO writers, no reader barging after writer intent, crash
recovery, process-exit release, symlink/reparse rejection, and same-process
close/acquire ordering.

Commit: `feat(runlock): add fair shared and exclusive leases`

## Task 2: Database-scoped semantic lock families

**Files:**

- Create `internal/semanticlock/scope.go`
- Create `internal/semanticlock/scope_test.go`

Use `retrieval_state.database_id` as the stable identity. No SQLite migration is
required. Derive exact lock roots under:

```text
<cache>/semantic/<database-id>/locks/
```

Expose shared/exclusive maintenance acquisition and shared generation
acquisition. Expose generation-exclusive acquisition only from an existing
exclusive maintenance lease so lock order is structurally enforced. Prohibit
upgrades.

Test exact paths, unsafe identifiers, distinct database isolation, cancellation,
close idempotence, and family/mode diagnostics.

Commit: `feat(semantic): add database-scoped lock families`

## Task 3: Fence bounded semantic refresh units

**Files:**

- Create `internal/semanticrefresh/locked_pipeline.go`
- Create `internal/semanticrefresh/locked_pipeline_test.go`
- Modify `internal/semanticrefresh/pipeline.go`
- Modify `internal/app/semantic_refresh_helper.go`
- Modify `internal/semanticbuild/chunk.go`
- Modify `internal/store/retrieval_projection_staging.go`
- Modify focused tests beside those files

Wrap each `StageExecutor.Execute` call in one exclusive maintenance lease.
Projection, embedding, verification, and readiness use maintenance only. Flush,
compaction, repair, garbage collection, and purge also acquire exclusive
generation through the maintenance lease. The embedding-stage emergency L0
flush must reuse the existing maintenance lease rather than reacquiring or
upgrading it. Release stage leases before ledger checkpoint/progress writes.

Add expected purge epoch to projection staging/promote/block inputs and reject a
final commit when the current epoch differs. Preserve existing embedding and
generation epoch fences.

Lock timeout/failure returns `semantic_lock_unavailable`; caller cancellation
remains `semantic_refresh_cancelled`.

Commit: `feat(semantic): fence refresh units with cross-process locks`

## Task 4: Lease authoritative projected-parent writes

**Files:**

- Create `internal/store/authoritative_write_lock.go`
- Create `internal/store/authoritative_write_lock_test.go`
- Modify `internal/store/open.go`
- Modify authoritative item/source/enrichment write files selected by semantic
  dirty triggers
- Modify writable Store call sites under `internal/app`, `internal/remote`, and
  `web`

Extend writable Store configuration with the semantic cache directory. After
schema initialization, read `database_id`, construct the semantic scope, and
attach it to the Store.

Create a transaction helper that acquires shared maintenance, begins the
authoritative transaction, holds the lease through commit/rollback, reports
release errors, and supports nested calls through a store-specific context
token without an upgrade attempt.

Apply it only to writes that can change semantic projected inputs: relevant
`items`, `sources`, and projected `item_enrichments` roles. Do not lock
unrelated authentication, audit, stats, tag-only, or read-only operations.

Add source-level coverage preventing production writable Store call sites from
omitting semantic lock configuration.

Commit: `feat(store): lease authoritative semantic writes`

## Task 5: Pin admitted generations through hydration

**Files:**

- Modify `internal/brainresearch/runtime.go`
- Modify `internal/brainresearch/runtime_native_searcher_usearch.go`
- Modify `internal/researchsemantic/retriever.go`
- Modify related tests

Acquire a short shared generation lease while opening and validating the
immutable native root. After query embedding, acquire a shared generation lease
for semantic retrieval and retain it through candidate search, exact SQLite
validation/reranking, L0 reads, chunk hydration, and final evidence document
construction.

Use the existing bounded semantic query context. If acquisition times out or a
writer is pending, report `generation_busy` and preserve lexical results exactly
as semantic-off.

Test that activation waits for deliberately blocked hydration and that later
queries do not barge after activation intent exists.

Commit: `feat(research): pin semantic generations through hydration`

## Task 6: Subprocess composition and failure tests

**Files:**

- Create `internal/app/semantic_lock_subprocess_test.go`
- Extend package-focused subprocess tests as needed

Use real helper subprocesses and isolated SQLite/cache roots to prove:

- a refresh waits for a source transaction holding shared maintenance;
- once refresh has exclusive intent, a later source transaction cannot barge;
- after the source commit, refresh acquires before the later source writer;
- activation waits for a query blocked during hydration;
- a queued process crash leaves no permanent blockage;
- deadline expiry leaves no write, activation, or published generation;
- retry succeeds after release;
- different database IDs do not block each other;
- maintenance always precedes generation.

Commit: `test(semantic): prove cross-process lock ordering`

## Task 7: Documentation and verification

**Files:**

- Modify `CHANGELOG.md`
- Modify `docs/semantic-retrieval.md`
- Modify `docs/superpowers/specs/2026-07-27-semantic-ann-automatic-sync-design.md`

Document exact paths, reader/writer roles, FIFO intent and crash recovery, lock
order/no-upgrade, lexical fallback on generation contention, sync errors on
maintenance acquisition failure, and explicit successful native skips on
unsupported targets.

Verify:

```text
go test -race ./internal/runlock ./internal/semanticlock
go test -race ./internal/store ./internal/semanticrefresh ./internal/researchsemantic ./internal/brainresearch ./internal/app
go test -race -tags usearch ./internal/semanticrefresh ./internal/semanticindex ./internal/researchsemantic ./internal/brainresearch ./internal/app
task fmt
task lint
task test-ci
task build
```

Cross-compile CGO-free Darwin amd64/arm64, Linux amd64/arm64, and Windows amd64
to prove unsupported-native platforms remain usable.

After task-scoped reviews are clean, run one broad final review against the
accepted automatic-sync design and audit the diff for release/packaging scope
creep.
