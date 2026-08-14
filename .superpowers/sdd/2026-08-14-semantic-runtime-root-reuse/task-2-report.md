# Task 2 Implementation Report

## Boundary

- Repository: `/Users/darron/src/dbrain`
- Integrated base: `fdd642342a920fefdf37961f8a0859136bdac822`
- Scope: Task 2 runtime, native lazy-search, status, generation-lease, and retriever-context files only
- Preserved: the pre-existing `Taskfile.yml` modification and untracked `docs/superpowers/` work
- Not modified: Task 3 workflow owners or Task 4 documentation/changelog files

## Implementation

- Added `brainresearch.Runtime`, with one `semanticruntime.Manager` per runtime/store lifetime, reusable `NewBuilderContext` and `Build` methods, bounded shutdown, active-builder admission tracking, `Drained`, and compatibility ownership for existing builder/top-level wrappers.
- Removed native root opening from builder admission. Native admission now constructs a lazy searcher after the existing 250 ms readiness/capability checks.
- Added query-time authoritative readiness and complete `semanticruntime.RootKey` derivation using a canonical cache directory, database ID, profile, generation, snapshot revision, purge epoch, backend version, and descriptor checksum.
- Added the 5-second manager acquisition wait independently from the 250 ms admission budget. Caller cancellation remains an ordinary cancellation; a cold wait timeout returns the path-free `root_load_timeout` status while the manager-owned load may finish.
- Registered immutable root load specifications with the runtime manager. Cold loads run under the manager shutdown context, open and validate the native root once, re-read authoritative readiness before publication, and discard mismatched roots without generation retry.
- Added retainable `semanticlock.Lease` references. Closing the query owner no longer releases the kernel lease until every retained detached-load reference closes; references are independent and idempotent.
- Added the acquired generation lease to the `researchsemantic` search context. The native lazy searcher retains that exact lease for a cold detached load and never reacquires generation-shared behind an exclusive writer.
- Preserved lexical fail-open behavior for shadow/on root timeout, root artifact, and runtime readiness failures. Added centralized path-free semantic reasons for generation contention, cold-load timeout, native artifacts, and runtime readiness.
- Preserved CGO-free behavior: active native generations remain unavailable without the tagged backend, while exact-small retrieval remains unchanged.

## Tests

TDD red evidence:

- `semanticlock` initially failed to compile because `Lease.Retain` did not exist.
- `researchsemantic` initially failed to compile because the generation-lease context seam did not exist.
- The first tagged Task 2 compile failed against the eager `runtimeSemanticSearcher` contract; the converted lazy runtime tests then exposed the missing test cache directory before passing.

Final verification:

```text
task fmt
task lint
```

Result: formatter completed; linter reported `0 issues`.

```text
CGO_ENABLED=0 go test ./internal/brainresearch ./internal/semanticlock ./internal/researchsemantic ./internal/semanticruntime ./internal/semanticindex -count=1
```

Result: all five packages passed.

```text
MACOSX_DEPLOYMENT_TARGET=12.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CGO_CFLAGS=-I/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/include \
  CGO_LDFLAGS=-L/private/tmp/dbrain-usearch-v2.26.0-darwin-arm64/stage/lib \
  go test -tags=usearch -race \
  ./internal/brainresearch ./internal/semanticruntime ./internal/semanticindex \
  ./internal/semanticlock ./internal/researchsemantic \
  -run 'Test(Runtime|Manager|USearch|GenerationLeaseRetain|Retriever)' -count=1
```

Result: all five packages passed with no race report. The tagged runtime coverage includes lazy first load, warm reuse, root-key mismatch discard, explicit cold wait timeout, path-free readiness failure, retained-generation writer exclusion, blocked native-search shutdown, and shadow/on lexical fail-open JSON.

```text
task test-ci
```

Result: every Task 2 package passed in the repository-wide clean-environment race run. The overall task exited nonzero only because `internal/releaseautomation/TestUSearchPackagingTaskPolicy` recursively discovered unrelated `.worktrees/*` checkouts and compared those nested tagged packages with the root `USEARCH_RACE_PACKAGES` list. This is the ambient contamination explicitly anticipated by the approved plan; no worktree was removed and the unrelated `Taskfile.yml` change was not modified.

## Remaining Limitation

- Task 2 focused default, lint, and tagged native race gates passed. The repository-wide `task test-ci` gate is ambient-red only on the unrelated `.worktrees/*` packaging-policy discovery described above; a clean-clone rerun was not performed in this time-bounded checkpoint. Task 3 long-lived workflow reuse and Task 4 documentation/changelog work remain intentionally deferred and out of scope.
