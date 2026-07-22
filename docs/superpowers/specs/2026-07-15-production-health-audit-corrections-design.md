# Production Health Audit Corrections Design

## Status

Approved for implementation from the production v0.7.0 audit findings on
2026-07-15. This document narrows the correction scope before code changes.

## Goal

Make `dbrain audit` distinguish real production regressions from window-edge
and legacy-data artifacts, expose actionable remote-check failures, and provide
a supported repair command for pruned media that still needs OCR or
transcription.

## Non-goals

- Do not change which import or enrichment stages `sync all` runs.
- Do not add automatic network repair to `sync all` or scheduled audits.
- Do not mutate production data as part of an audit.
- Do not silently enable SQLite backups in user configuration.
- Do not backfill historical provenance that cannot be reconstructed exactly.

## Considered Approaches

### 1. Audit-only corrections

Fix the scheduler and transcription classifiers and leave pruned-media repair
as an internal development tool. This is the smallest change, but it leaves a
verified production durability finding without a supported operator action.

### 2. Audit corrections plus an explicit repair command

Fix classification and remote diagnostics, then promote the existing
pruned-media repair workflow into `dbrain repair pruned-media`. The command is
dry-run by default and performs network/download writes only with `--apply`.
This is the selected approach because it fixes audit truthfulness without
adding background work or changing normal sync behavior.

### 3. Automatic repair during `sync all`

Automatically restore pruned media whenever a new item link needs enrichment.
This would eventually heal the rows, but it changes sync latency, network use,
and archive lifecycle policy. It is deliberately out of scope.

## Corrections

### Scheduler continuity

Continuity comparisons use only runs with a known non-zero `StartedAt`.
Incomplete records may still contribute to metrics-window sufficiency and
latest-run diagnostics, but they cannot be sorted against year-zero or create
a saturated `time.Duration` gap. The production seven-day case must classify
the real 7,143-second gap as `warn`, not manufacture a failure.

### Transcription partition

Terminal status is determined by the closed status enum (`no_audio`, `noise`,
`too_short`, `empty`), independently of whether raw transcript text is present.
Raw text remains preserved. An `ok` row still requires non-empty transcript
text to be current. Invalid statuses and valid statuses that match no other
partition remain `unknown`.

### Remote media diagnostics

Remote audit initialization and request failures map to existing sanitized
`dbrain.audit.v1` error codes:

- secret-reference failure: `credential_resolution_error`;
- invalid archive client/configuration: `configuration_error`;
- request deadline: `timeout`;
- cancellation: `canceled`;
- other remote metadata failure: `read_error`;
- absent capability without a more specific cause: `unavailable`.

The report never includes provider bodies, credentials, endpoints, buckets,
object keys, or raw error strings. Standard and deep remote checks use the same
classification rules where applicable.

### Pruned-media repair

Add `dbrain repair pruned-media` using the existing store predicates and
`mediadownload.RunForItem` path.

- Default behavior is a read-only dry run that reports bounded candidate
  counts. Dry-run configuration and database opening must not create
  directories, apply migrations, or otherwise mutate the target.
- `--apply` is required before downloads or database/media writes occur.
- `--ocr` and `--transcripts` select categories; both default to enabled when
  neither is explicitly selected.
- `--limit` bounds candidates per category; `--timeout` bounds each media
  download; `--json` emits stable operator stats.
- Candidate selection uses authoritative `item_enrichments` with compatibility
  fallbacks, not legacy columns alone.
- Only items with archived/pruned downloaded media and no runnable local media
  are selected. Already-current or terminal items are excluded.
- The command re-downloads through normal confined media persistence. Restoring
  directly from remote archive objects is a separate future feature.
- After repair, ordinary OCR/transcription workers remain responsible for
  enrichment; the repair command does not invoke models.

The existing `cmd/devtools/restore_pruned_pending_x_media` implementation is
removed or reduced to a wrapper so candidate policy has one owner.

### SQLite backup configuration

Keep `durability.sqlite_backup_configuration` as an optional warning. Improve
human remediation so it names the exact supported settings:

- `scheduler.sqlite_archive.enabled: true` for scheduled backups;
- `audit.require.sqlite_backup: true` when backup health must affect audits.

No release changes production configuration automatically.

## Testing

Every behavior change is test-first:

1. A metrics window containing a boundary-only incomplete run plus a real
   sub-failure-threshold gap returns `warn` and reports the real largest gap.
2. A `too_short` transcript with preserved text is terminal and the partition
   remains valid.
3. Remote dependency/bootstrap and HEAD failures emit the sanitized error code
   without raw error leakage.
4. Pruned-media dry-run performs no writes or network requests; `--apply`
   selects only pending OCR/transcription items and uses bounded downloads.
5. Backup configuration warning output includes exact remediation settings.

Focused package tests run before the repository gates. Final verification is
`task fmt`, `task lint`, `task test-ci`, and `task build`.

## Documentation and Release Notes

Update `COMMANDS.md`, `docs/maintenance-operations.md`, the production audit
design where its terminal-status wording is ambiguous, and `CHANGELOG.md`.
