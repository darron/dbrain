# Schema Migrations

Date: 2026-05-05

This document describes how `dbrain` manages its local SQLite schema and what
users should do before upgrading or downgrading across schema changes.

SQLite remains the authoritative working database. Rendered Markdown notes are
projections and can be repaired or regenerated from database state.

## Current Mechanism

Writable store opens use `store.Open`, which:

- opens the configured `brain.db`
- applies normal writable SQLite pragmas, including WAL mode
- runs the ordered migration registry in `internal/store/migrations.go`
- emits migration progress when the startup command supplies a reporter, such
  as `sync all` and web/remote serve startup
- records applied migrations in `schema_migrations`
- writes `PRAGMA user_version` to the current schema version

Read-only store opens use `store.OpenReadOnly`, which:

- opens SQLite with `mode=ro`
- applies `PRAGMA query_only = ON`
- verifies the expected existing tables are present
- intentionally skips schema creation and migrations

`dbrain audit` additionally holds one read-only transaction with
`PRAGMA query_only = ON` for its database-backed checks. It never upgrades an
older schema: schema and migration incompatibility are findings in the report,
not triggers for repair.

Current migration history:

| Version | Name                             | Purpose |
|---------|----------------------------------|---------|
| 1       | `current_schema_baseline`        | Adopt the checked-in schema that existed when migrations were introduced. |
| 2       | `media_download_retry_state`     | Add/backfill media download retry state for errored media assets. |
| 3       | `item_enrichments_current_state` | Add/backfill current item enrichment state for summaries, OCR, and X media transcripts. |
| 4       | `x_article_canonical_i_article_urls` | Backfill canonical URLs for X article references. |
| 5       | `feed_ingestion_tables` | Add feed-ingestion state tables. |
| 6       | `auth_user_approvals` | Add authenticated-user approval state. |
| 7       | `mcp_bearer_tokens` | Add MCP bearer-token state. |
| 8       | `auth_user_approvals_repair` | Repair authenticated-user approval state. |
| 9       | `public_chat_shares` | Add private public-chat-share records. |
| 10      | `feed_parse_error_retry_repair` | Repair retryable feed parse errors. |
| 11      | `review_event_indexes` | Add review-event indexes. |
| 12      | `audit_provenance_v1` | Add and backfill audit provenance for enrichments. |
| 13      | `retrieval_hybrid_storage_v1` | Add local hybrid retrieval chunks, embeddings, and generation state. |
| 14      | `retrieval_profile_invariant_triggers_repair` | Repair retrieval embedding profile invariant triggers. |
| 15      | `retrieval_chunk_projection_provenance` | Persist retrieval chunk projection provenance. |
| 16      | `retrieval_semantic_foundation_v2` | Add the v2 semantic projection ledger, occurrence and staging tables, profile aggregates, stable database identity, and revision columns. Existing v2 chunks and embeddings remain in place for explicit v3 projection/embedding replacement. |

Version 1 is the adoption baseline, not a permanent "current schema" label.
The current schema version is the highest registered migration version.

## User Upgrade Expectations

Most upgrades require no manual action. The first writable command that opens
the store applies any missing migrations before continuing.

Before running a newly upgraded `dbrain` binary that includes schema changes,
make a backup if the database contains data you cannot easily recreate.

Back up all SQLite files when doing a file-level backup:

- `brain.db`
- `brain.db-wal`
- `brain.db-shm`

Stop long-running `dbrain` commands before copying or replacing these files.
This includes `serve`, `sync --watch`, workers, and long enrichment jobs.

If S3-compatible archive credentials are configured, `dbrain sqlite archive`
creates a consistent SQLite snapshot with SQLite itself, compresses it, and
uploads it under the configured archive prefix. This is the preferred backup
path for normal operators.

## Restore Expectations

`dbrain sqlite restore` restores the newest archived SQLite snapshot under the
configured archive prefix. It:

- asks for confirmation unless `--yes` is passed
- downloads and decompresses the archive
- validates the restored database with `PRAGMA quick_check`
- moves any existing `brain.db`, `brain.db-wal`, and `brain.db-shm` aside with a
  timestamped `.pre-restore-...` suffix
- installs the restored database at the configured DB path

For a manual local restore, stop all `dbrain` processes, move the current
SQLite triplet aside, copy the backed-up triplet back into place, then start the
binary version that matches the restored database.

## Downgrade Policy

`dbrain` does not provide automatic down migrations.

The supported downgrade path is:

1. Stop all running `dbrain` processes.
2. Restore a database backup created before the schema upgrade.
3. Run the older binary against that restored database.

Do not rely on an older binary opening a newer database safely. It may miss new
tables, columns, status values, or backfilled state, and any writes from the old
binary are unsupported.

If a schema migration fails, treat the database as possibly partially migrated
unless you have inspected it. The safest recovery is to restore the pre-upgrade
backup, fix or upgrade the binary, and retry from the backup.

## Authoring Future Migrations

When changing SQLite schema:

- add a new ordered entry to `schemaMigrations`
- bump `currentSchemaVersion` to the newest migration version
- never renumber, remove, or rewrite migrations that may have shipped
- keep version 1 as the adoption baseline
- make each migration safe to retry after a partial failure where practical
- preserve raw evidence during backfills
- keep compatibility reads/writes when removing old columns would be risky
- add representative upgrade tests for the old state being migrated
- add raw-evidence preservation tests when a migration touches imported text,
  source extracts, transcripts, OCR, media metadata, tags, or summaries
- keep `OpenReadOnly` migration-free

For user-visible schema or pipeline behavior changes, update `CHANGELOG.md` and
the relevant docs before the change is considered complete.
