# Local Maintenance Operations

Date: 2026-05-05

This document records the local delete, prune, purge, replace, and reset paths
that intentionally modify or remove local `dbrain` state.

Normal imports are append-only by default. Do not add new destructive cleanup to
ordinary import paths without updating this document and the architecture
cleanup tracker.

## Operations

| Operation | Local state changed | Guardrails | `sync all` behavior |
|-----------|---------------------|------------|---------------------|
| `dbrain audit all --profile deep` | None. Downloads and validates an archive candidate only inside a private temporary directory; it never calls the restore path or replaces `brain.db`, its WAL, or SHM. | Explicit deep profile; compressed, decompressed, temporary-space, object, page, request, idle-read, and whole-run limits; temporary files are cleaned on every exit. | Not part of `sync all`. |
| `serve remote` scheduled SQLite archive | Creates and removes a private local online-snapshot/compression workspace, and persists a fixed last-attempt timestamp under the data directory; the active `brain.db`, WAL, and SHM are not replaced. Adds one compressed object under `archive/db/` remotely. | Disabled by default; positive interval; durable pre-attempt rate limit across restarts; shared cross-process archive/restore lease; in-flight work is canceled and awaited during shutdown. | Separate sibling scheduler; not a `sync all` stage and does not change sync/audit status. |
| `dbrain archive media --prune-local` | Deletes archived local media files under `media/...` and marks all same-path `media_assets.local_pruned_at` rows. Item/source rows remain. | Requires explicit `--prune-local`; only prunes after every asset sharing the same `local_path` is archived. | Runs only when `--archive-media`, `DBRAIN_AUTO_ARCHIVE_MEDIA=1`, or `archive.auto` enables the archive stage. |
| `dbrain sqlite restore` | Replaces the active `brain.db` after moving existing `brain.db`, `brain.db-wal`, and `brain.db-shm` aside with timestamped `.pre-restore-...` suffixes. | Validates the restored DB with `PRAGMA quick_check`; asks for confirmation unless `--yes` is used. | Not part of `sync all`. |
| `dbrain tsnet reset` | Removes the resolved tsnet/Tailscale state directory. | Refuses to run while the state lock is held; asks for the literal `reset` confirmation unless `--yes` is used. | Not part of `sync all`. |
| `dbrain import apple-notes --forget-excluded` | Purges indexed content for previously imported Apple Notes that are now excluded, clears FTS, and leaves a privacy tombstone. | Requires explicit `--forget-excluded`; ordinary Apple Notes import only skips newly excluded notes. | `sync all` does not pass `--forget-excluded`. |
| `dbrain import youtube` legacy cleanup | Deletes deprecated `youtube_history` item rows, removes their note files, and deletes orphaned legacy YouTube source rows/notes. | Limited to obsolete `youtube_history` rows and orphaned `youtube` sources from older importer versions; command output reports deleted counts. | Runs at the start of the YouTube import stage, including `sync all` unless YouTube is skipped. This is the known legacy cleanup exception and must not become upstream mirror pruning. |
| `dbrain repair sources` | Clears selected source extraction/summary derived state. With `--rehydrate-x-articles`, also clears linked X item article-hydration cache and invalidates affected summaries. | Requires at least one filter; supports `--dry-run`; asks for confirmation unless `--yes` is used. | Not part of `sync all`. |
| `cmd/devtools/restore_pruned_pending_x_media` | Re-downloads pruned media for pending OCR/transcript repair through normal media download persistence. | Development tool, not shipped as a normal user command. | Not part of `sync all`. |

## Store Helpers

`internal/store/cleanup.go` currently exposes physical item/source deletion
helpers:

- `DeleteItemsBySourceType`
- `DeleteOrphanSources`

The production caller is `internal/youtubeimport/cleanup.go`, which limits the
delete scope to deprecated `youtube_history` rows and orphaned legacy YouTube
sources.

Other reset/purge helpers clear derived or indexed state rather than removing
whole memories:

- `PurgeItemIndexedContent` is used by Apple Notes `--forget-excluded`.
- `ResetSourceEnrichment` is used by `dbrain repair sources`.
- `MarkMediaLocalPrunedByPath` marks rows after media files are pruned.

## Rules For New Maintenance Paths

- Prefer `--dry-run` for any broad reset or purge path.
- Require confirmation or an explicit flag for local deletes, purges, restores,
  and resets.
- Keep upstream app integrations import-only; do not mutate upstream state.
- Do not treat upstream disappearance as a reason to delete local memory during
  ordinary sync.
- Keep local media cleanup reference-aware: only delete a local file when all
  same-path asset rows are safely archived or otherwise covered.
- Document whether the path can run from `sync all`.
- Add regression tests for any new destructive path or for any expansion of an
  existing path's selection predicate.
