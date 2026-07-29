# Automatic Semantic Refresh After Sync Implementation Plan

> **Execution rule:** implement with test-driven development and review each task before starting the next.

**Goal:** Make every successful `dbrain sync` execution automatically run the durable semantic refresh path, while preserving explicit non-error skips for mode `off` and unsupported builds and returning a typed non-zero error for every supported enabled refresh failure.

**Stack boundary:** This PR is stacked on `codex/semantic-ann-resumable-refresh`.
It adds universal synchronous sync integration only. Cross-process semantic
locks, release/Homebrew native packaging, and installed production-corpus
activation remain in later stacked PRs.

**Execution status (2026-07-28):** Tasks 1–3 implement and test the central CLI
hook, scheduled-sync hook, and real initial-backfill/cancellation-resume
composition. Task 4 documents the resulting automatic-sync contract. Tasks 5
and later-stack acceptance remain pending.

**Runtime boundaries:**

- CLI: the `sync` Cobra command family in `internal/app/sync.go`.
- Scheduler: `runScheduledSyncAllUnlocked` in `internal/app/scheduler.go`, which bypasses Cobra.
- Refresh: the existing `runConfiguredSemanticRefresh` helper and durable `internal/semanticrefresh` orchestration.
- Authoritative source work must finish and its store/transactions must close before refresh starts.

## Task 1: Centralize the CLI post-sync hook

**Files:**

- Modify: `internal/app/sync.go`
- Modify: `internal/app/sync_output.go`
- Add/modify tests: `internal/app/app_test.go` or a focused `internal/app/sync_semantic_test.go`

**Required behavior:**

1. Add a dependency-injected `newSyncCommandWithSemanticDeps` constructor used by tests; keep `newSyncCommand` as the production default.
2. Register sync leaves through one command-family boundary with a per-execution completion record. Reset it at leaf entry, populate it only after successful source cleanup, consume it exactly once in the parent `PersistentPostRunE`, and clear it after every success or failure.
3. A source error must prevent semantic admission, provider construction, store open, native work, and refresh execution.
4. A successful source run, including an unchanged/no-row run, must invoke `runConfiguredSemanticRefresh` exactly once after the leaf `RunE` has explicitly closed the source store and completed source-local output/metrics cleanup.
5. The existing coarse `sync-all.lock` spans the complete source-plus-semantic command. It is distinct from the semantic maintenance lock planned for PR 4. The family boundary releases it after semantic success/failure; source failure releases it without running refresh.
6. Mode `off` and capability `unsupported` must return zero and emit an explicit bounded semantic skip.
7. `supported_broken` or any refresh failure must return a typed non-zero `RefreshError`; it must not be converted into a successful sync.
8. Human success output must contain the ordinary sync summary followed by the semantic result.
9. The leaf captures config, committed stats, and output mode without writing final stdout. `--json` success output is emitted by the parent as exactly one bounded JSON document: flatten the existing top-level sync fields and add one `semantic` object using the existing public semantic DTO.
10. `--json` refresh failure emits exactly one bounded JSON document containing the same flattened source stats and exactly one `semantic_error` object, then returns `ExitError{Code: 1, Silent: true, Err: refreshErr}`.
11. Human refresh failure must show the committed source summary and return the existing bounded semantic human error with stage, run, checkpoint, readiness, and aggregate debt.
12. Every registered direct `sync` leaf must be covered by an invariant test proving it uses the central post-sync contract and does not declare a descendant persistent post-hook that would shadow the family hook.

**TDD cases:**

- source failure means refresh callback count is zero;
- unchanged source success means refresh callback count is one;
- source store close is observed before refresh admission;
- coarse sync lock remains held during refresh and is released after terminal output/error;
- mode off and unsupported skip explicitly;
- supported broken and stage failure are non-zero typed errors;
- cancellation is non-zero and preserves `semantic_refresh_cancelled`;
- JSON success and failure decode as one value followed by EOF, preserve exact existing sync keys, contain exactly one of `semantic` or `semantic_error`, and preserve `errors.As` to `*semanticrefresh.RefreshError`;
- human output contains source summary plus semantic completion/skip/error;
- bare `sync` never refreshes;
- repeated execution of one command object does not reuse stale completion state;
- source failure after a prior success does not refresh;
- all registered leaves use the command-family completion boundary and cannot shadow its persistent post-hook.

## Task 2: Integrate scheduled sync through the same helper

**Files:**

- Modify: `internal/app/scheduler.go`
- Modify/add tests: `internal/app/scheduler_test.go`

**Required behavior:**

1. Add a dependency-injected scheduled-sync helper while retaining the production wrapper.
2. On source error, close the source store and return without invoking semantic refresh.
3. On source success, explicitly close the source store before invoking `runConfiguredSemanticRefresh`. The scheduler's existing outer coarse sync lock intentionally spans both source and semantic work.
4. Run refresh even for unchanged source stats so a prior cancelled/failed run resumes automatically.
5. Log one bounded semantic completion or skip line on success.
6. Propagate typed refresh failures to scheduler status/logging as failures.
7. Preserve the existing sync run lock, metrics, scheduler status (`ok`, `error`, or `skipped`), and post-run audit behavior.

**TDD cases:**

- source error skips refresh;
- successful unchanged scheduled run invokes refresh;
- source store is closed before refresh;
- unsupported skip succeeds and is logged explicitly;
- supported-broken/stage/cancellation errors fail the scheduled run and preserve stable error codes;
- existing scheduler status reports `error`, not `ok`, when semantic refresh fails;
- the post-run audit still executes after an attempted scheduled sync whose semantic refresh fails, and only after status/lock settlement.

## Task 3: Prove automatic initial backfill and resume composition

**Files:**

- Modify/add tests: `internal/app/sync_semantic_test.go`
- Reuse: `internal/app/semantic_refresh_helper_test.go`
- Reuse: `internal/semanticrefresh` integration fixtures

**Required behavior:**

1. Exercise the real command boundary twice against the same isolated writable SQLite store with a fake provider/native lifecycle.
2. Prove the first enabled successful sync runs projection, embedding, flush, verification, and readiness before returning.
3. First run source successfully and inject cancellation after durable projection/embedding/index progress. Then run an unchanged source sync against the same database and prove it resumes without repeating committed work.
4. Prove final supported-enabled success is possible only with readiness `ready`.
5. Prove mode off, unsupported builds, and supported-broken admission do not open the writable semantic store or construct provider/native dependencies.

## Task 4: Documentation and truthful stack status

**Files:**

- Modify: `CHANGELOG.md`
- Modify: `docs/semantic-retrieval.md`
- Modify: `docs/superpowers/specs/2026-07-27-semantic-ann-automatic-sync-design.md`

**Required behavior:**

1. Document that mode `shadow`/`on` makes normal sync synchronously refresh semantic state.
2. Document explicit `off`/unsupported success skips and supported-broken/non-ready failures.
3. Explain initial backfill, cancellation, automatic next-sync resume, JSON output, and the continued manual command as diagnostics only.
4. Update implementation status to mark runtime admission, resumable refresh, and universal sync integration implemented without claiming locks, packaging, or installed-corpus acceptance.
5. Add a concise dated changelog entry.

## Task 5: Verification and publication

Run:

```text
go test -race ./internal/app ./internal/semanticrefresh ./internal/syncjob
task fmt
task lint
task test-ci
task build
```

Additional gates:

- tagged USearch app/refresh tests using the pinned 2.26.0 development library;
- default and tagged isolated sync smokes proving explicit skip behavior;
- diff/status/scope audit proving no cross-process lock, release, Homebrew, or production activation work entered this PR;
- independent task reviews and one final broad review.

Publish a draft stacked PR with base `codex/semantic-ann-resumable-refresh`.
