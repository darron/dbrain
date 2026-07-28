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
flush from the embedding stage uses the same nested order. Queries use a short
shared generation lease while opening and validating the immutable native root.
After query embedding, they reacquire shared generation and retain it through
native candidate search, current-generation SQLite validation and reranking,
exact L0 merge, chunk hydration, and final evidence construction.

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
semantic admission. Generation contention during a query fails open with the
path-free `generation_busy` reason and preserves the lexical result exactly.
Caller cancellation or deadline expiry remains an error, and lease-release
errors fail closed rather than being hidden.

## Output and recovery

Human sync output writes the ordinary source summary, streams bounded semantic
refresh progress, and finishes with one semantic completion, skip, or error
line. Scheduled runs log the same bounded progress plus one terminal semantic
line, and record semantic failures as scheduler errors.

`dbrain sync all --json` emits exactly one flattened JSON document. It retains
the normal top-level sync fields and contains exactly one of:

- `semantic` for completion or an explicit skip;
- `semantic_error` for a typed refresh failure.

`dbrain semantic refresh`, `semantic status`, `semantic chunk`, `semantic
embed`, and `semantic verify` remain diagnostics and recovery tools. They are
not a manual step required after normal sync, and query, research, MCP, and web
paths never start maintenance.

## Scope of this stack

Runtime admission, resumable refresh, universal sync integration, and
cross-process maintenance/generation locking are implemented. Static release
and Homebrew packaging for the macOS arm64 native library, followed by
installed-binary full-corpus acceptance, remain later stacked work.
Untagged/CGO-free builds and other targets without the native library continue
to report the explicit successful `native_backend_unsupported` skip while
retaining normal sync and lexical retrieval behavior.
