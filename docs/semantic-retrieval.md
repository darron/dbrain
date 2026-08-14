# Semantic Retrieval and Automatic Refresh

Semantic retrieval is optional. SQLite and lexical retrieval remain the
authoritative working baseline; semantic state is derived and is maintained only
when `research.semantic.mode` is `shadow` or `on`.

## Sync contract

Every successful `dbrain sync all` and scheduled sync synchronously runs the
durable semantic refresh path after the source store and source metrics have
closed. This includes an unchanged source run. The first successful sync after
enabling `shadow` or `on` performs the initial backfill; a later successful sync
automatically resumes a refresh interrupted by cancellation or failure without
repeating completed durable work.

Source failure stops before semantic admission. For semantic admission:

| Condition | Sync result |
| --- | --- |
| `research.semantic.mode=off` | Explicit successful `semantic_mode_off` skip. |
| Native backend unsupported | Explicit successful `native_backend_unsupported` skip. |
| Supported, ready backend with `shadow` or `on` | Refresh must finish `ready`; source plus semantic work succeeds together. |
| Supported-but-broken backend, cancellation, or refresh failure | The committed source result remains visible, then sync returns the typed semantic error and non-zero status. |

Skip paths do not open the writable semantic store or construct semantic
provider/native dependencies. A semantic failure never rolls back source data
that the source sync has already committed.

The existing coarse `sync-all.lock` still prevents overlapping
manual/scheduled `sync all` commands. Database-scoped locks additionally
coordinate all processes that can write authoritative corpus state, maintain
derived semantic state, activate roots, or query an admitted root.

For database ID `<database_id>`, the two family lock paths are:

```text
<cache_dir>/semantic/<database_id>/locks/maintenance.lock
<cache_dir>/semantic/<database_id>/locks/generation.lock
```

Each family also uses a persistent `<family>.lock.coordinator` and FIFO writer
intent tickets named `<family>.lock.writer-<20-digit-sequence>.intent`. These
companion files coordinate new readers and queued exclusive waiters; their mere
presence is not proof that a live process still owns an intent.

Authoritative corpus transactions that can dirty semantic projection hold
shared maintenance through commit or rollback. Refresh holds exclusive
maintenance for each bounded projection, embedding, flush, compaction,
verification, and readiness unit. Flush and compaction hold exclusive generation
for their entire execution while exclusive maintenance is already held; this
includes native build/publication and SQLite root activation. An emergency L0
flush from the embedding stage uses the same nested order. Runtime construction
takes its SQLite readiness snapshot under a 250 ms budget and admits a lazy
semantic lane; it does not open native-root artifacts. After query embedding, a
query has a separate 250 ms budget to acquire shared generation protection. A
warm root is then leased from the process-local, in-memory runtime cache,
searched natively, SQLite-validated and exactly reranked, merged with exact
L0, hydrated, and turned into evidence. A cold root retains a reference to
that already-acquired shared generation lease while it is imported and
validated and until the manager decides whether to publish or discard it. The
manager releases that retained guard before publishing; successful release is
therefore required for publication. The ordinary caller waits up
to five seconds for that import; a timed-out caller remains lexical while the
non-preemptible load may complete and warm the same runtime cache for a later
request. Published root and segment paths remain immutable.

If a loaded root is rejected before it becomes a pending cache entry, the
manager closes it before releasing the retained guard. If it was admitted as a
pending entry but fails the release or publication checks, the manager closes
it after releasing the guard. Both paths remain manager-owned.

The retained cold-load lease is a deliberate liveness trade-off: an exclusive
refresh or `dbrain semantic gc` writer can wait for native import, validation,
and the pre-publication disposition decision. The manager-owned flight still
keeps ownership through final discarded-searcher cleanup, but that cleanup is
not protected by the generation lease after it has been released. Five seconds
bounds the ordinary caller wait, not the native C call. Runtime shutdown
likewise waits for in-use searches and loads rather than force-closing a native
root.
The normal ten-minute semantic-GC reader grace remains defense in depth, not
the only protection for an importing root.

Ordinary chunk replacement and projection application preserve the active root.
Changed embeddings are removed, replacement embeddings enter exact L0, and the
old immutable membership is counted as a tombstone until bounded flush and
compaction publish a successor. Deleted chunks are likewise filtered by
query-time membership validation. A dirty parent suppresses all of its native
candidates, including unchanged siblings, until the projection is current;
this is the deliberate fail-safe recall cost. Purge and provenance/profile
repair paths still invalidate the root because their authority boundary is
broader than an ordinary chunk delta.

The only valid two-lock order is maintenance before generation; lock upgrade is
not supported. Exclusive waiters publish FIFO writer intent, so later source
writers cannot starve refresh and later queries cannot starve activation.
Kernel-held leases are released on process exit. Intent files from a crashed
waiter are ignored and cleaned only after its owner lease is proven dead.
Different persisted database IDs use independent lock families.

Failure to acquire refresh's exclusive maintenance lease is the typed
`semantic_lock_unavailable` refresh error and therefore makes an enabled,
supported sync exit non-zero. Failure to acquire a source transaction's shared
maintenance lease remains a source-write/source-sync error and stops before
semantic admission. `generation_busy` means only that shared-generation lease
admission encountered contention or exhausted its 250 ms acquisition budget;
it never means a slow native root open. The following path-free semantic-lane
reasons fail open to unchanged lexical evidence:

| Reason | Meaning |
| --- | --- |
| `generation_busy` | Shared-generation admission was contended for longer than its 250 ms budget. |
| `root_load_timeout` | A cold-root caller waited five seconds without the shared import becoming ready; the detached import may still warm this runtime later. |
| `native_root_artifacts_unavailable` | Native cache/root artifacts, descriptor validation, or root load/validation failed. |
| `runtime_readiness_unavailable` | The query-time authoritative readiness snapshot could not be read or no longer describes a stable searchable native root. |

Caller cancellation or deadline expiry remains an error. Other semantic
artifact, readiness, and lease-cleanup failures, plus query-time
embedding-provider/search failures for a valid configuration, preserve lexical
evidence at the research-pack boundary with the path-free status reason.
Provider construction or configuration failures still abort setup. A semantic
lane is `used` only after native candidates pass current SQLite validation and
exact reranking; a cache miss or a warmed root is not evidence of semantic
ranking quality.

## Output and recovery

Interactive human `sync all` output writes the ordinary source summary, then
renders projection, embedding, flush, compaction, verification, and readiness
as stage-local in-place progress bars. Once a stage has a known denominator and
positive measured throughput, its bar shows completed/total units, percentage,
elapsed time, and an approximate ETA. Totals may expand when retry work or a
new compaction pass becomes eligible, so percentage and ETA can adjust during a
run. A known zero-work stage is distinct from a stage that is still planning.

Redirected human output and scheduled runs retain bounded, sanitized semantic
progress lines plus one terminal semantic line. A failed or cancelled stage
does not render successful completion. These display measurements are
process-local and do not change durable refresh counters or recovery state.

`dbrain sync all --json` emits exactly one flattened JSON document. It retains
the normal top-level sync fields and contains exactly one of:

- `semantic` for completion or an explicit skip;
- `semantic_error` for a typed refresh failure.

`dbrain semantic refresh`, `semantic status`, `semantic chunk`, `semantic
embed`, `semantic verify`, and `semantic gc` retain their standalone diagnostic
and recovery contracts. Query, research, MCP, and web paths never start
maintenance.

### Garbage collection

Semantic publication is content-addressed and generations can share segments.
Garbage collection therefore uses reachability, not generation age: it retains
the active/profile root, resumable refresh roots, the newest published rollback
roots (including roots later marked stale), and roots updated within the grace
period. A segment is prunable only when no retained root references it.

The default command is read-only:

```sh
dbrain semantic gc
dbrain semantic gc --json
```

Review the plan, then opt in to deletion:

```sh
dbrain semantic gc --apply
```

Apply recomputes the plan while holding exclusive maintenance followed by
exclusive generation locking. It removes obsolete SQLite catalog/membership
rows in one transaction before unlinking cache directories, and also sweeps old
uncatalogued profile, generation, and segment directories. Symlinks and paths
outside the configured semantic database root are rejected. An unlink failure
cannot leave SQLite pointing at an absent artifact; the leftover directory is
rediscovered by the next run.

Automatic housekeeping is opt-in and default-off. Set either
`sync_all.semantic_gc: true` or `DBRAIN_SYNC_ALL_SEMANTIC_GC=true` to apply GC
after a successfully completed semantic refresh in both manual and scheduled
`sync all`. The stage uses the normal ten-minute reader grace and retains two
published rollback generations. It bounds semantic lock admission, reports a
timeout as a skipped cleanup, and reports other cleanup failures without
changing an otherwise successful sync result. A later successful run retries
grace-delayed or failed cleanup. Skipped or failed semantic refreshes do not run
automatic GC, and automatic GC never runs SQLite `VACUUM`.

Row deletion does not necessarily reduce the physical SQLite file while
`auto_vacuum` is disabled. `--vacuum` requires `--apply`, checkpoints the WAL on
both sides, and rebuilds the database file. Archive the database first, ensure
free space for the rewrite, and stop the dbrain daemon and other writers before
running it; a busy database produces an actionable error. A run shortly after
a root is superseded can reclaim less than the eventual total because the
default ten-minute reader grace remains in force.

## Scope of this stack

Runtime admission, resumable refresh, universal sync integration, and
cross-process maintenance/generation locking are implemented. The Homebrew
macOS arm64 artifact statically links checksum-pinned USearch v2.26.0, targets
macOS 12.0 or later, and proves native capability during release and clean
Homebrew installation. It has no external USearch dynamic-library dependency;
Apple's system `libc++` remains dynamically linked.

Untagged/CGO-free builds and all other release targets remain free of the native
library and report the explicit successful `native_backend_unsupported` skip
while retaining normal sync and lexical retrieval behavior.
