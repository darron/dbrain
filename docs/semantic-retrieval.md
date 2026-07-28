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

The existing coarse `sync-all.lock` spans the whole source-plus-semantic sync
operation. It prevents overlapping manual/scheduled `sync all` work; it is not
the database-scoped cross-process semantic maintenance or generation locking
planned in the next stack.

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

The runtime-admission, resumable-refresh, and universal-sync-integration slices
are implemented. Cross-process semantic maintenance/generation locks, release
and Homebrew packaging for the macOS arm64 native library, and installed
production-corpus acceptance are pending later stacked PRs. Untagged/CGO-free
builds therefore remain explicitly unsupported for semantic refresh while
retaining normal lexical sync and retrieval behavior.
