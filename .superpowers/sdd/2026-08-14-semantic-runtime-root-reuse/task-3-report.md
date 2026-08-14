# Task 3 Implementation Report

## Boundary

- Repository: `/Users/darron/src/dbrain`
- Integrated base: `c10955b5cd65a80859fe5cd0eb095d6a212ec96e`
- Scope: Task 3 workflow owners in `internal/researchrun`, `internal/researcheval`, `internal/mcpeval`, `internal/mcpserver`, `internal/remote`, and the web server/research handlers plus focused lifecycle tests
- Preserved unchanged: the pre-existing `Taskfile.yml` modification and the untracked approved plan under `docs/superpowers/`
- Not modified: Task 4 documentation, changelog, semantic policy, MCP wire schemas, request semantic flags, or broad native/runtime internals

## Implementation

- `researchrun.Run` accepts an optional owner `*brainresearch.Runtime`. Without one it creates exactly one runtime before constructing the runner, uses it for initial retrieval and the bounded retry, and closes it once from the outer `Run` return path.
- `researcheval.Run` creates one runtime for the complete case list. Direct cases call that runtime and runner cases receive the same runtime through `researchrun.Options`.
- `mcpeval.Run` creates one `mcpserver.Server` for the complete report. Research-pack cases reuse it; ordinary `ask` cases continue to call the existing ask path.
- `mcpserver.Server` now owns a pointer-shared lifecycle containing one runtime, request admission/drain state, and idempotent close state. Transport-capability clones share the lifecycle pointer. `BuildResearchPack` uses the owned runtime, `Shutdown` is bounded, and `Close` is idempotent.
- MCP stdio and HTTP transports drain transport work before shutting down the server runtime and closing SQLite. Deadline paths transfer runtime/store ownership to an asynchronous reaper instead of force-closing an active native root.
- The web server owns one runtime. Direct research, research-run, and trace-current runner paths reuse it. `NewHandlerWithOptions` still returns `http.Handler`, with a concrete `web.CloseableHandler` wrapper providing request drain, idempotent `Close`, and bounded `Shutdown`.
- Web and remote cleanup keep each store open until its handler/server runtime drains. Remote `buildHandler` returns an error-aware idempotent cleanup function, and `serveWithDeps` joins cleanup errors into its return. Deadline paths retain stores in asynchronous reapers.
- Focused tests cover owner runtime injection, retry reuse, eval/server reuse, clone pointer sharing and concurrent idempotent close, bounded shutdown, close-error propagation, request drain, store lifetime, asynchronous reaping, and joined remote cleanup errors.

## TDD Evidence

- The initial lifecycle test build failed on the absent APIs and ownership fields: `researchrun.Options.Runtime`, runner runtime-bound build state, eval/server parameters, MCP `Close`/`Shutdown`, shared lifecycle state, web close-capable wrapper, and remote owned-store cleanup.
- After implementation, the exact new lifecycle tests passed in all six owner packages.
- `TestServeWithDepsSurfacesHandlerCleanupError` was mutation-checked: temporarily suppressing the cleanup join produced `serve error = <nil>, want joined cleanup error`; restoring the join returned the test to green.
- The first wider web lifecycle run exposed one manually constructed test server without the newly required runtime. The production constructor was correct; the test fixture was updated to match the production ownership contract, after which the same focused web suite passed.

## Verification

```text
task fmt
```

Result: passed (`go fmt ./...`).

```text
env GOCACHE=/private/tmp/dbrain-task3-gocache \
  go test ./internal/researchrun ./internal/researcheval \
  ./internal/mcpeval ./internal/mcpserver ./internal/remote \
  -run 'Runtime|Research|MCP|Handler|Eval' -count=1
```

Result: passed outside the managed listener sandbox:

- `internal/researchrun` 0.384s
- `internal/researcheval` 0.867s
- `internal/mcpeval` 0.923s
- `internal/mcpserver` 1.499s
- `internal/remote` 1.106s

```text
env GOCACHE=/private/tmp/dbrain-task3-gocache \
  go test ./web -run 'Runtime|Research|Handler|Closeable' -count=1
```

Result: passed outside the managed listener sandbox in 1.096s.

```text
env GOCACHE=/private/tmp/dbrain-task3-gocache \
  go test ./internal/researchrun ./internal/researcheval \
  ./internal/mcpeval ./internal/mcpserver ./internal/remote ./web \
  -run '^$' -count=1
```

Result: all six packages compiled successfully.

```text
git diff --check
```

Result: passed with no whitespace errors.

## Race Status and Limitations

The requested race command was started:

```text
env GOCACHE=/private/tmp/dbrain-task3-race-gocache \
  go test -race ./internal/researchrun ./internal/researcheval \
  ./internal/mcpeval ./internal/mcpserver ./internal/remote -count=1
```

It reported these packages green before the user-directed immediate handoff interrupted the remaining wait:

- `internal/researchrun` 16.327s
- `internal/researcheval` 10.088s
- `internal/mcpeval` 5.162s

No final result was collected for `internal/mcpserver` or `internal/remote`; the process was terminated rather than left running. The repository-wide `task lint` and `task test-ci` gates were not run in this shortened handoff. Existing Task 1/2 manager/runtime loader-count tests were preserved but not duplicated outside the Task 3 ownership boundary.
