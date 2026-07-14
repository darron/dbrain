# Production Health Audit Design

**Date:** 2026-07-13

**Status:** Approved for implementation planning

**Repository boundary:** This design covers read-only health assessment of the
configured dbrain runtime, CLI/MCP/web presentation of the same report,
scheduled audit execution, and regression alerting. It does not authorize a
production deployment, run an import, alter the active database, restore a
backup, retry failed work, delete local memory, or mutate an upstream source.

## Summary

`dbrain` should have one deterministic production-health engine in
`internal/audit`. The engine should inspect the resolved runtime boundary,
SQLite state, durable metrics, configured importers, enrichment coverage,
archive metadata, database backups, and OKF output. It should return a stable,
content-free `dbrain.audit.v1` report with explicit `pass`, `warn`, `fail`,
`unknown`, and `skipped` results.

The CLI is the canonical operator interface:

```text
dbrain audit all --profile standard --json
```

MCP, the authenticated admin page, scheduled audits, and reviewer skills must
consume that same report instead of recreating health definitions. Existing
`stats` commands remain descriptive views, but their predicates must be aligned
with the worker selectors before audit results depend on them.

The release-audit workflow should combine a deep read-only audit with the
existing automated tests. Scheduled fast and standard audits should detect
runtime regressions between releases and alert only on meaningful state
transitions or repeated failures.

## Problem

Tests prove code behavior against fixtures. They do not prove that the current
production runtime is pointed at the intended database, that every configured
importer is still polling, that worker and dashboard predicates agree, that
derived-data provenance survives later writes, or that remote durability is
current and restorable.

The July 13 production audit demonstrated the gap:

- every configured import stage was polling successfully, but that fact was
  reconstructed from `metrics.jsonl` rather than reported by one health tool;
- quiet new-row periods looked suspicious until successful polling was
  separated from new arrivals;
- `stats pipeline` reported one pending OCR item that the OCR worker could not
  run;
- transcription stats reported 604 failures even though only one row was a
  genuine error and 603 were expected terminal no-content outcomes;
- the source backlog reported `drained=true` while omitting OCR,
  transcription, importer polling, media archival, SQLite backups, OKF export,
  and scheduler continuity;
- 2,553 successful transcript enrichments had lost tool/model/raw provenance
  during later mirror writes; and
- media archival was current, while the newest remote SQLite archive was almost
  ten days old because database archival was manual and unscheduled.

The admin page therefore answers a narrower question than its presentation
implies. It is a source-processing operations view, not a whole-system health
assessment.

## Goals

1. Make the configured production boundary explicit in every report.
2. Distinguish successful polling from new-row arrival for every importer.
3. Use the same eligibility and terminal-state predicates as the workers.
4. Detect real errors, blocked work, terminal outcomes, stale provenance, and
   durability gaps without conflating them.
5. Provide deterministic, machine-readable status and exit codes for release
   gates and periodic automation.
6. Reuse one report across CLI, MCP, web, scheduler logs, and reviewer skills.
7. Keep audit checks read-only with respect to authoritative dbrain state,
   upstream sources, and remote object storage. Scheduled adapters may append
   content-free audit telemetry and send configured alert webhooks.
8. Avoid corpus content, secrets, titles, URLs, raw identifiers, OCR text, and
   transcripts in audit logs and alerts by default.
9. Preserve append-only import semantics: upstream reconciliation reports what
   is missing locally and never proposes automatic local deletion.
10. Make uncertainty explicit when credentials, history, or upstream state are
    unavailable.

## Non-Goals

- Audits do not run imports, enrichment workers, repair commands, backups, or
  restores.
- Audits do not judge the semantic accuracy of OCR, transcripts, summaries, or
  categorizations. They judge execution, coverage, state, and provenance.
- Audits do not compare exact model/backend provenance unless a check is
  explicitly about provenance completeness.
- No anomaly-detection model or probabilistic baseline decides health in v1.
- No audit results are stored in the authoritative SQLite memory database.
- No upstream disappearance causes local deletion or a failure by itself.
- No public or unauthenticated health endpoint is added.
- No Prometheus, OpenTelemetry, or external monitoring dependency is required.

## Considered Approaches

### Approach 1: Skill-Only Audit

A Codex or Claude skill could run a collection of SQLite, metrics, and object
storage queries and summarize the result.

This is useful as an operator wrapper, but it is not the source of truth. SQL
and state semantics would drift from worker code, there would be no stable exit
status or report schema, and the admin page would remain independently wrong.

### Approach 2: Expand Existing Stats And Admin Queries

Existing stats could be extended until the admin page shows more stages.

This improves presentation, but descriptive stats are not a release gate. It
also leaves runtime-boundary checks, metrics continuity, remote archive checks,
restore verification, upstream comparison, and alert state without a coherent
owner.

### Approach 3: Shared Audit Engine

This is the selected approach. A read-only Go package owns check execution and
report semantics. CLI, MCP, web, scheduler, and skills are adapters. Existing
store predicates and operational packages supply evidence rather than being
reimplemented in UI code or prompts.

This requires more initial work, but it is the only approach that prevents the
release gate, admin page, and automated monitor from disagreeing about health.

## Health Semantics

### Status

Every check has one status:

| Status | Meaning |
| --- | --- |
| `pass` | The check ran and satisfied its contract. |
| `warn` | The check found a non-terminal risk or threshold breach that does not yet prove loss or breakage. |
| `fail` | The check found a definite contract violation, unresolved failure, corruption, or required durability gap. |
| `unknown` | The check could not establish a result from the available evidence. |
| `skipped` | The check was outside the selected profile or the corresponding feature was intentionally disabled. |

Overall status precedence is `fail`, `unknown`, `warn`, `pass`. `skipped`
checks do not affect overall status. Only checks with `required=true` contribute
to overall status; optional checks remain visible as findings. A definite
required failure remains `fail` even if another required check is `unknown`.

Profile membership and resolved configuration determine `required`. A check for
an intentionally disabled optional feature is `skipped` and not required. A
configured feature is required. Audit configuration may additionally require a
feature, such as SQLite backup, even when its operational configuration is
currently absent; that condition then fails rather than skips.

### Confidence

Status and confidence are separate. Each check reports `high`, `moderate`,
`low`, or `unknown` confidence. Direct database counts and successful object
metadata comparisons are normally high confidence. A conclusion based on
limited metrics history is moderate. A check that did not run is unknown.

Overall confidence is the lowest confidence among all required, non-skipped
checks. Skipped and optional checks do not reduce overall confidence.

### Pipeline Partitions

Pipeline coverage must partition candidate rows into non-overlapping states:

- `current`: valid output exists for the current input under model-agnostic
  freshness policy;
- `pending`: runnable work selected by the worker's current predicate;
- `blocked`: work cannot run until a prerequisite or operator action changes;
- `terminal`: an expected no-output outcome such as `no_audio`, `noise`,
  `too_short`, `empty`, `gone`, or another policy-defined terminal state;
- `failed`: a genuine unresolved error or retry-exhausted failure; and
- `unknown`: legacy or invalid state that cannot be classified.

For every stage, `total` must equal the sum of those partitions. Worker
candidate selection, backlog counts, pipeline stats, audit checks, and admin
labels must use shared named predicates and shared status classification.

### Polling Versus Arrivals

Every importer reports these concepts separately:

- latest attempted poll;
- latest successful poll;
- latest upstream item seen;
- latest local row created or materially updated;
- created, updated, unchanged, skipped, linked, blocked, and failed counts;
- arrival counts by day for the requested audit window.

A quiet arrival period is informational when polls are succeeding. It becomes a
warning or failure only when polling is stale, a configured stage is absent, or
an explicit upstream comparison proves local omissions.

## Architecture

### `internal/audit`

The new package owns only orchestration and public audit semantics. It should be
split into focused files rather than one large query module:

```text
internal/audit/
  types.go             stable report and check types
  runner.go            profile resolution and check orchestration
  status.go            overall-status and exit-code policy
  boundary.go          paths, build, schema, configuration
  scheduler.go         metrics continuity and stage execution
  imports.go           configured importer polling and arrivals
  pipeline.go          enrichment partitions and provenance
  durability.go        local archive/backup/OKF checks
  upstream.go          optional source-of-truth comparisons
  privacy.go           identifier redaction and evidence allowlist
  thresholds.go        deterministic default/configured thresholds
```

`audit.Run` receives resolved `config.Config`, an already opened read-only
store, a clock, metrics reader, filesystem adapter, and optional remote/upstream
clients. This keeps package tests deterministic and prevents command wiring from
containing health policy.

### Read-Only Store Boundary

All CLI stats and audit commands must use `store.OpenReadOnly`. `OpenReadOnly`
must remain migration-free and query-only.

Audit database counts should execute through a new read-snapshot abstraction in
`internal/store`. The abstraction begins one SQLite read transaction and
exposes focused audit/stat methods through an interface. This gives relational
checks one consistent database view without copying the multi-gigabyte database
or holding a writable connection.

Filesystem, metrics, remote, and upstream checks have their own `observed_at`
timestamps because they cannot share the SQLite transaction. The report-level
time is the start of the run, not a false claim that every external system was
observed simultaneously.

### Existing Package Reuse

The audit engine should call existing domain packages through narrow read-only
interfaces:

- `internal/store` for counts, shared predicates, schema state, feed health,
  media metadata, and enrichment provenance;
- `internal/metrics` plus a new JSONL reader for scheduler/run/stage history;
- `internal/sqlitearchive` for read-only list/latest/download-to-temp helpers;
- `internal/mediaarchive` for configured object-store clients and remote
  metadata reconciliation;
- `internal/okf` for manifest reads and validation summaries; and
- importer packages for optional read-only upstream inventories.

The engine must not shell out to `sqlite3`, `jq`, `aws`, `rclone`, or another
`dbrain` process. Those were appropriate audit-discovery tools, not a durable
product architecture.

## Report Schema

The stable JSON envelope is:

```json
{
  "schema": "dbrain.audit.v1",
  "audit_id": "audit_20260713T230224Z_4e6f0c1a",
  "profile": "standard",
  "scope": {
    "categories": ["boundary", "scheduler", "imports", "pipeline", "durability"],
    "filtered": false,
    "whole_system": true
  },
  "started_at": "2026-07-13T23:02:24Z",
  "completed_at": "2026-07-13T23:02:31Z",
  "status": "fail",
  "confidence": "high",
  "boundary": {
    "config_path": "/Users/example/.config/dbrain/config.yaml",
    "db_path": "/Users/example/.local/share/dbrain/brain.db",
    "version": "v0.5.1",
    "commit": "4269f53",
    "schema_version": 11
  },
  "summary": {
    "pass": 18,
    "warn": 2,
    "fail": 1,
    "unknown": 0,
    "skipped": 4
  },
  "checks": []
}
```

The example above shows the local CLI form. Absolute paths are optional fields.
Local CLI human/JSON output includes them so an operator can verify the exact
target. MCP, web, persisted scheduled reports, metrics, and alerts omit absolute
paths and instead report the resolved layout (`explicit-config`, `explicit-root`,
or `xdg`), verification booleans, build identity, and schema version. The
redaction mode is recorded in the report so omission is not mistaken for failed
boundary resolution.

Each check contains:

```json
{
  "id": "durability.sqlite_backup_age",
  "category": "durability",
  "status": "fail",
  "confidence": "high",
  "required": true,
  "summary": "Newest remote SQLite archive is older than the failure threshold",
  "observed_at": "2026-07-13T23:02:29Z",
  "threshold": {"warn_after_seconds": 129600, "fail_after_seconds": 259200},
  "evidence": {"latest_age_seconds": 847858, "archive_count": 32},
  "remediation": "Run and schedule dbrain sqlite archive"
}
```

Check IDs, enum values, field meanings, and units are stable API. Evidence keys
are check-specific but must use seconds, bytes, integer counts, UTC RFC3339
timestamps, and booleans rather than formatted prose.

Filtered category/source commands set `scope.filtered=true` and
`scope.whole_system=false`. Their overall status describes only the selected
scope and must never be labeled whole-system health by CLI, MCP, or web.

By default, evidence must not contain titles, URLs, source/item keys, transcript
text, OCR text, note text, prompts, secrets, cookies, object-store credentials,
or raw error bodies that may contain content. `--include-identifiers` may add
internal row IDs and source keys to local CLI output only. It is unavailable to
MCP, web, scheduled reports, and webhook alerts.

Summaries, remediation text, and errors are selected from fixed templates keyed
by check ID and sanitized error code. Arbitrary provider/upstream errors may not
flow into those strings. Corpus/source URLs are prohibited; an explicitly
configured dbrain admin origin is an operational URL and may appear only in an
alert action link after allowlist validation.

## Audit Profiles

### Fast

Designed to run after every scheduled sync without network access:

- runtime boundary and build identity;
- expected schema/table presence;
- configured importer stage presence and latest successful poll from metrics;
- exact worker-runnable backlog counts;
- pipeline state partitions;
- enrichment provenance completeness;
- local media archive eligibility;
- latest local OKF manifest freshness; and
- metrics history availability.

Fast profile does not run full SQLite `quick_check`, walk the OKF tree, list
remote objects, download backups, or contact upstream applications.

The default fast deadline is 30 seconds, with a five-second target on the
production-sized corpus. A timed-out required check is `unknown`; it is not
silently omitted.

### Standard

The default interactive and periodic profile. It includes fast checks plus:

- SQLite `PRAGMA quick_check` and foreign-key check;
- full local OKF validation with summary-only output;
- read-only remote media archive reconciliation;
- remote SQLite archive listing and age thresholds; and
- seven-day scheduler continuity and tolerated-error summaries.

If a configured remote feature lacks credentials, its required standard check
is `unknown`, not silently omitted.

The default standard deadline is ten minutes, with a two-minute target.

### Deep

Designed for release validation and scheduled weekly drills. It includes
standard checks plus:

- download and decompress the newest SQLite archive into the configured temp
  directory;
- run schema, `quick_check`, and foreign-key validation against that temporary
  database without replacing the active database;
- full remote media key/size reconciliation;
- configured source-of-truth comparisons; and
- longer-window scheduler and arrival analysis when history exists.

Deep audits may consume substantial time, bandwidth, and temporary disk space.
They require explicit `--profile deep`; MCP and ordinary page loads may not run
them.

The default deep deadline is two hours. CLI callers may lower any profile
deadline with `--timeout`; scheduled defaults are configurable. Each network
check also has a shorter bounded child timeout so one provider cannot consume
the entire run budget.

## Check Families

### Boundary And Integrity

- resolved config, DB, log, media, vault, temp, and OKF paths;
- binary version, commit, dirty state when available, and schema version;
- expected tables/migrations without applying them;
- database readability, `quick_check`, and foreign keys by profile; and
- configured/runtime disagreement, such as an endpoint that cannot report the
  scheduler because authentication failures were discarded.

### Scheduler And Metrics

- latest completed sync and its invocation;
- expected configured stages versus actual stage completions;
- unmatched starts, failed runs, lock skips, and service-restart gaps;
- median, p95, and maximum start interval and duration;
- metrics coverage start/end so a 30-day conclusion is never made from nine
  days of evidence; and
- tolerated internal error counters reported separately from run status.

The stale-sync threshold derives from configured interval, jitter, and recent
run duration. It must not use a fixed one-hour threshold that creates false
alerts around normal jitter or long successful runs.

### Imports

The initial supported source families are Apple Notes, Safari tabs, X
bookmarks, GitHub stars, YouTube liked/watch-later, and feeds. Every configured
family receives poll and arrival evidence. Disabled families are `skipped`.

Configured/selected status must come from the existing resolved sync import
policy, including legacy defaults, `sync_all.imports`, importer enablement,
environment overrides, explicit CLI overrides, and scheduler restrictions in
their established precedence. Reading YAML keys directly is not sufficient and
would misreport older or overridden installations.

Source-specific commands always perform a bounded read-only upstream
reconciliation for that source; they are not aliases for local descriptive
stats. Their effective profile is `deep`; they default to deep and reject an
explicit fast/standard profile. `audit all --profile deep` runs the configured
set together. The source-specific commands compare current upstream identities
to local source keys:

```text
dbrain audit github-stars --json
dbrain audit youtube-watch-later --json
dbrain audit youtube-liked --json
dbrain audit x-bookmarks --json
dbrain audit feeds --json
dbrain audit apple-notes --json
dbrain audit safari-tabs --json
```

These commands report upstream records missing locally. Local-only historical
records are expected under append-only semantics and are not deletion
candidates.

### Enrichment

- X hydration and media completeness as distinct checks;
- source extraction and summary coverage;
- X media transcript and transcript-summary coverage;
- X photo OCR coverage;
- Apple Notes note, attachment materialization, OCR, unsupported, offloaded,
  encrypted, and blocked classifications;
- item/source categorization coverage;
- exact terminal-versus-error classifications; and
- tool/model/tool-version/input-hash/raw-provenance completeness for derived
  rows.

Historical provenance already lost cannot be invented. The mirror fix must
preserve future provenance. The audit should report legacy missing provenance
as a bounded warning and fail when a newly completed enrichment loses required
provenance after the fixed release.

### Durability

- archive-eligible local media backlog using the media archive worker predicate;
- remote presence and size for recorded archive keys;
- local unlinked/orphan media counts as warnings, not automatic cleanup;
- newest SQLite archive time, age, count, and historical gaps;
- temporary restore verification in deep profile;
- OKF manifest freshness and full validation; and
- explicit labeling that OKF is a recoverable derivative, not an independent
  substitute for the SQLite database.

Default SQLite archive thresholds are 36 hours for warning and 72 hours for
failure. They are configurable durations. A configured archive with no remote
snapshot is an immediate failure.

Standard remote-media verification HEAD-checks every locally recorded key
created or archived within `--since`, plus a deterministic hash-selected sample
of 100 older keys. Missing keys or size mismatches fail. Deep verification
paginates the complete configured remote prefix and reconciles every local and
remote key/size. A truncated listing, timeout, or inconsistent pagination makes
the required check `unknown`, never pass.

Remote SQLite backup is required when archive credentials/provider are
configured or `audit.require.sqlite_backup=true`. It is skipped on an
intentionally local-only installation unless explicitly required. This keeps
remote durability optional for the product while making an enabled or promised
backup contract enforceable.

## Normative Check Registry

The implementation owns a typed registry, not a loose collection of callbacks.
Each entry defines stable ID, category, profile membership, required-condition,
evidence schema, timeout, thresholds, and status classifier. Configuration may
override documented durations/counts but may not replace classifier code.

Initial registry rules:

| Check ID | Profiles | Required when | Deterministic status rule |
| --- | --- | --- | --- |
| `boundary.config` | all | always | `pass` when the resolved layout and config source are verified; command-level exit 3 if resolution cannot produce a report. |
| `boundary.database` | all | always | `pass` when the configured DB opens query-only with expected tables/schema; `fail` for verified wrong/missing schema; `unknown` for an inconclusive open error. |
| `integrity.sqlite` | standard, deep | always | `pass` for `quick_check=ok` and no FK rows; `fail` for any reported violation; `unknown` on timeout/read error. |
| `scheduler.latest_sync` | all | scheduler enabled | `pass` within interval+jitter+p95-duration+15m; `warn` beyond that; `fail` after two configured intervals beyond that grace or after an unresolved failed latest run. |
| `scheduler.stage_coverage` | all | scheduler enabled | `pass` when every resolved selected stage completed in the latest completed run; `fail` when a selected stage is absent; `unknown` when the run record is incomplete. |
| `scheduler.continuity` | standard, deep | scheduler enabled | `pass` with no unexplained gap above the latest-sync warning threshold; `warn` for one explained restart/lock gap; `fail` when completed runs are absent for two intervals; `unknown` when the requested metrics window is unavailable. |
| `metrics.window` | all | scheduler enabled | `pass` when history covers `--since`; `warn` when shorter but still sufficient for latest-state checks; `unknown` when no parseable evidence exists. |
| `imports.<source>.poll` | all | source selected by resolved import policy | `pass` when its stage/poll succeeded within the scheduler threshold; `warn` after one stale interval; `fail` after two or an unresolved explicit poll failure; `unknown` with no usable evidence. |
| `imports.<source>.arrivals` | all | source selected | Informational optional check: `pass` with counts/quiet interval; quiet alone never warns or fails. |
| `upstream.<source>.parity` | deep/source command | source selected for upstream audit | `pass` when every enumerated upstream identity exists locally; `fail` for proven missing local identities; `unknown` for partial inventory, auth, pagination, or snapshot failure. Local-only rows do not affect status. |
| `pipeline.<stage>.partition` | all | stage selected | `pass` when partitions are exhaustive/non-overlapping; `fail` for invalid/unclassified rows; `unknown` on query error. |
| `pipeline.<stage>.pending_age` | all | stage selected | `pass` when no runnable row is older than 24h; `warn` at 24h; `fail` at 72h. Per-stage overrides are allowed and shown in evidence. |
| `pipeline.<role>.provenance` | all | role enabled | `pass` when new successful rows have required provenance; `warn` for bounded pre-fix legacy loss; `fail` for any post-fix success missing required fields. |
| `durability.media_local_coverage` | all | media archive or local media enrichment enabled | `pass` when no media was pruned before terminal enrichment coverage; `fail` for any uncovered pruned asset; orphan rows are a separate optional warning. |
| `durability.media_remote` | standard, deep | remote media archive configured | `pass` for the profile's complete sample/reconciliation; `fail` for missing/size-mismatched recorded keys; `unknown` for incomplete listing/HEAD evidence. |
| `durability.sqlite_backup_age` | standard, deep | remote SQLite backup configured or explicitly required | `pass` through 36h; `warn` above 36h; `fail` above 72h or when no snapshot exists; `unknown` when listing cannot complete. |
| `durability.sqlite_restore` | deep | SQLite backup required | `pass` when newest archive downloads, decompresses, opens read-only, and passes integrity/schema checks in temp; `fail` for verified invalid archive; `unknown` for interrupted/incomplete retrieval. |
| `durability.okf_freshness` | all | OKF export enabled | `pass` when manifest age is within two selected sync intervals; `warn` above two; `fail` above four or for missing/invalid manifest. |
| `durability.okf_validation` | standard, deep | OKF export enabled | `pass` for conformant summary with zero broken links/errors; `fail` for validation errors; `unknown` for incomplete traversal/read failure. |

Terminal/blocked counts are still displayed even when their check passes. A
future check ID requires a registry entry, tests for every status branch, and a
schema-version decision when it changes public meaning.

## CLI Design

Top-level command:

```text
dbrain audit all [flags]
```

Common flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--profile fast|standard|deep` | `standard` | Select check depth. |
| `--since <duration>` | `7d` | Metrics and arrival-history window. |
| `--json` | `false` | Emit the stable JSON report. |
| `--timeout <duration>` | profile default | Bound the complete audit run. |
| `--include-identifiers` | `false` | Include internal identifiers in local CLI evidence only. |

Human output starts with target boundary, overall status, observation window,
and build identity. It then prints failed and unknown checks, warnings, and a
compact category summary. Passing detail is available in JSON rather than
flooding the terminal.

Exit codes:

| Code | Meaning |
| ---: | --- |
| `0` | Overall `pass`. |
| `1` | Overall `warn`. |
| `2` | Overall `fail`. |
| `3` | Overall `unknown`, invalid configuration, or failure to produce a valid report. |

JSON is written before returning a non-zero health exit whenever a report was
successfully produced. Startup/configuration failures use the ordinary command
error channel and exit 3.

Category commands reuse the same runner with a check filter:

```text
dbrain audit imports
dbrain audit pipeline
dbrain audit durability
```

### Release Workflow

A release audit is a comparison, not one isolated green command:

1. before changing the installed binary, run standard audit and require a fresh
   SQLite archive;
2. preserve that JSON report as the pre-release baseline;
3. after the separately approved install/restart, verify the reported binary
   commit and exact production boundary;
4. run `dbrain audit all --profile deep --json` against the explicit production
   config;
5. compare check IDs, statuses, configured importers, pipeline partitions, and
   durability evidence to the baseline; and
6. retain the post-release report and investigate every new non-pass finding.

The reviewer skill automates these read-only steps after the CLI lands. It does
not install, restart, roll back, repair, or deploy anything without separate
authorization.

## Scheduled Audits And Alerts

Scheduled execution belongs in the existing long-running `serve remote`
process; no new daemon or helper binary is added.

Suggested configuration:

```yaml
audit:
  enabled: false
  run_after_sync: true
  interval: 6h
  profile: standard
  since: 7d
  alert:
    webhook_url: ""
    consecutive_observations: 2
    repeat_after: 24h
```

When enabled:

1. run a fast audit from a post-run hook after every scheduled sync attempt,
   outside the sync stage plan and after the sync lock/result are settled;
2. run a standard audit on the configured interval without overlapping another
   audit;
3. write content-free completed reports to `<log_dir>/audit.jsonl`;
4. emit compact `audit.run.completed` metrics with profile, status, duration,
   and status counts; and
5. persist alert transition state atomically at
   `<log_dir>/audit-alert-state.json`.

An audit failure must not rewrite a successful sync as a failed import. Sync
status and audit status remain separate events.

Alerts are transition-based. Confirm an ordinary `warn`, `fail`, or `unknown`
state for `consecutive_observations` runs before sending the transition. Once
confirmed, send on `pass -> non-pass`, severity escalation, and recovery; one
passing observation is sufficient for recovery. Database integrity failure,
missing recorded remote media, and failed restore verification alert
immediately. Repeat unresolved alerts no more often than `repeat_after`.

Alert state is keyed by profile and check ID. A fast audit that skips remote
durability cannot clear or recover a standard/deep durability finding, and a
skipped check never changes its prior alert state.

Notifications are assembled from per-check transitions. If overall status
remains `fail` but a new check fails, an existing check escalates, or a required
check becomes `unknown`, that changed check set produces a new debounced alert.
Recovery is likewise tracked per check; the overall recovery notification is
sent only when no confirmed required non-pass checks remain for that profile.

The initial delivery adapter is a generic JSON webhook. The URL is redacted
from config/status output and never written to metrics. Alert bodies contain
build identity, overall status, check IDs, summaries, observation time, and the
admin URL when configured; they do not contain paths, credentials, identifiers,
or corpus content. A missing webhook still leaves exit codes, JSONL reports,
metrics, logs, MCP, and admin visibility operational.

## Adjacent Database-Backup Scheduler

Audit detection must remain read-only. The stale-backup finding should be fixed
by a separate sibling scheduler using the existing `internal/sqlitearchive`
archive path:

```yaml
scheduler:
  sqlite_archive:
    enabled: false
    interval: 24h
    run_on_start: true
```

The scheduler should create at most one archive per interval, use the existing
SQLite online snapshot mechanism, emit archive metrics, and serialize against
another archive or restore. It must not make `audit` mutate state. Deep restore
verification always installs into a temp path and never calls the active-DB
replacement flow.

## MCP Design

Add one tool:

```text
dbrain_audit
```

Arguments:

- `profile`: `fast` or `standard`, default `fast`;
- `category`: optional `all`, `imports`, `pipeline`, or `durability`;
- `since_seconds`: bounded history window.

MCP returns the same `dbrain.audit.v1` structured report. It cannot request
deep audits or identifiers. Existing `dbrain_stats_*` tools remain available
for exploratory counts, but MCP guidance should use `dbrain_audit` for health
claims.

The MCP server already opens its store read-only. Tool tests must verify schema
agreement, empty arrays rather than `null`, content exclusion, status
aggregation, and no mutation.

## Admin Design

The authenticated admin page should become the human presentation of the latest
shared audit report, while retaining source-activity drill-downs.

API additions:

- `GET /api/audit/latest`: return the newest persisted report, or a fast report
  when no persisted report exists;
- `GET /api/audit/history?limit=N`: return compact report summaries for trend
  display; and
- `POST /api/audit/run?profile=fast|standard`: run an authenticated on-demand
  audit without permitting deep download/restore checks.

The page should show:

1. overall health, build, verified runtime layout, last sync, and last audit
   time;
2. configured importers with separate successful-poll and latest-arrival times;
3. pipeline partitions with terminal outcomes separate from failures;
4. media archive, SQLite backup, and OKF durability cards;
5. failed/unknown checks first, with threshold and remediation detail; and
6. recent audit status history and recovery transitions.

Mobile layout must stack cards without horizontal overflow and move selected
check detail into view. Check IDs and long paths must wrap safely.

The existing `backlog.drained` field remains for API compatibility, but its UI
label becomes **Source backlog drained** and its payload gains an explicit scope
description. Whole-system health comes only from the audit report.

Page loads must not trigger standard remote scans repeatedly. The UI uses the
latest scheduled report, with an explicitly labeled fast refresh and an
authenticated on-demand standard run.

Persisted reports have a freshness deadline of the greater of twice the
configured standard-audit interval or 12 hours. An older report is rendered as
**stale**, its prior `pass` is not presented as current health, and the API
returns report age plus an `unknown` freshness finding until a new audit
completes.

## Required Correctness Fixes

The first implementation slice must repair known false inputs before presenting
the audit as authoritative:

1. Change all read-only `stats` commands from `store.Open` to
   `store.OpenReadOnly`.
2. Make X photo OCR pipeline pending use the same local-path and
   `local_pruned_at` eligibility as the worker.
3. Classify `no_audio`, `noise`, textless `too_short`, and `empty` as terminal,
   not failed.
4. Stop assigning every unexplained pipeline remainder to `failed`; expose
   invalid/unclassified state explicitly so predicate gaps cannot masquerade as
   processing failures.
5. Preserve transcript enrichment tool, model, tool version, raw JSON, and input
   hash during compatibility mirror writes; add a regression test that performs
   a later ordinary item upsert.
6. Give the one genuine transcription-error state an explicit retry/blocked
   policy instead of silently excluding it forever.
7. Surface scheduler endpoint authentication failures in `tsnet status` rather
   than silently dropping `sync_all`.
8. Add read-only SQLite archive list/status helpers so audits never need to
   enter the restore flow merely to inspect backup age.
9. Require and validate a readable OKF manifest; current bundle validation must
   not silently succeed after a manifest-read failure.
10. Add a summary-only OKF validation DTO so automated output does not expose the
   private document inventory.

Historical provenance repair is best effort only. No implementation may invent
tool/model values that cannot be recovered from durable evidence.

## Error Handling

- A check-level error becomes `unknown` with a sanitized error code and short
  message; other independent checks continue.
- Failure to resolve configuration, open the configured database read-only, or
  encode the report prevents a valid report and exits 3.
- Context cancellation stops new checks, marks interrupted required checks
  unknown, emits a partial report when possible, and returns exit 3.
- Network timeouts are bounded per remote/upstream check and do not block local
  checks.
- Remote credentials are resolved through existing configuration/Keychain
  paths and never returned in evidence.
- A metrics parse error reports line count and sanitized position, not the raw
  line.
- Scheduled report/alert-file write failures are logged and emitted as audit
  sink errors; they do not mutate the database or change sync results.
- Deep temporary restore files are created under the configured temp directory
  and removed on success or failure. Cleanup failure is a warning with the path
  shown only in local CLI output.

## Testing Strategy

### Store And Predicate Tests

- A fixture for each pipeline stage proves candidate partitions are exhaustive
  and non-overlapping.
- Worker selection count equals audit pending count for OCR, transcription,
  hydration, source extraction/summary, and media archive.
- Terminal transcription states never count as failed.
- Archived/pruned photo assets never count as runnable OCR.
- Transcript mirror updates preserve provenance across later item upserts.
- Stats and audit commands open an existing database read-only and do not apply
  migrations or write `PRAGMA user_version`.

### Audit Package Tests

- Table-driven status and overall-precedence tests.
- Stable JSON fixtures for pass, warn, fail, unknown, and skipped reports.
- Poll-versus-arrival fixtures covering active quiet sources.
- Metrics fixtures covering restart gaps, lock skips, tolerated stage errors,
  incomplete history, malformed lines, and missing stages.
- Threshold boundary tests with an injected clock.
- Privacy tests that reject content-bearing keys and raw identifiers.
- Remote archive fixtures covering missing keys, size mismatches, stale
  backups, absent credentials, and recovery.
- Deep restore tests against a temporary object store and temporary database;
  the active DB path must remain byte-for-byte unchanged.
- Upstream comparison tests prove local-only rows do not become deletion
  candidates.

### CLI, MCP, Scheduler, And Web Tests

- CLI output and exit-code tests for every overall status.
- JSON must be emitted before non-zero health exits.
- MCP schema matches runtime structured content and never exposes identifiers.
- Scheduler writes one report, suppresses duplicate alerts, repeats after the
  configured interval, and emits recovery.
- Audit failure does not change sync run status.
- Admin API authorization and caching tests.
- Browser tests for overall state, poll/arrival distinction, terminal versus
  failed counts, backup staleness, empty states, long values, and mobile
  layout.

### Standard Gates

Every code slice runs:

```text
task fmt
task lint
task test-ci
task build
```

CLI, MCP, scheduler, and browser behavior receive focused smoke tests against a
temporary root. Production audit validation must resolve the explicit
production config and remain read-only; deployment requires separate approval.

## Implementation Sequence

1. **Read-only/stat foundation PR:** switch stats opens, centralize selector and
   terminal classification, expose invalid partition state, and add
   selector-equivalence tests.
2. **Provenance/diagnostics PR:** preserve transcript provenance, define error
   retry/blocked policy, surface scheduler auth failures, and harden OKF manifest
   validation.
3. **Core report/CLI PR:** add store read snapshots, the typed check registry,
   `internal/audit`, fast/local standard checks, profiles, CLI rendering, JSON,
   privacy policy, and exit codes.
4. **Remote inspector PR:** add read-only media and SQLite archive interfaces,
   sampled standard reconciliation, backup-age checks, and summary-only OKF
   inspection.
5. **Deep verification PR:** add complete media reconciliation and temporary
   SQLite download/decompress/integrity verification, without any active restore
   call.
6. **Backup scheduler PR:** separately add the write-capable daily SQLite
   archive scheduler and its metrics/serialization tests.
7. **Audit scheduler/alert PR:** add fast post-run and periodic standard audits,
   JSONL output, audit metrics, per-profile/check transition state, and webhook
   delivery.
8. **MCP PR:** add `dbrain_audit` after the JSON schema and check registry are
   stable.
9. **Admin API PR:** add authenticated latest/history/run APIs, caching, and
   stale-report semantics.
10. **Admin UI PR:** add the responsive audit panel while preserving existing
    source-operations views.
11. **Upstream reconciliation PRs:** implement source-specific commands one
    importer at a time, starting with GitHub and YouTube before X and local app
    databases.
12. **Skill wrapper PR:** add/update the Codex/Claude production-audit skill only
    after the CLI schema is stable. The skill invokes the CLI and interprets
    results; it owns no SQL or health policy.

Each slice should be independently shippable and add a changelog entry when its
user-visible behavior lands. The implementation plan may split these slices
across multiple PRs, but they share this report contract.

## Acceptance Criteria

The design is complete when implementation can demonstrate all of the
following:

- one command identifies the exact configured runtime boundary and returns a
  stable report without writing to production;
- every configured importer reports successful polling separately from
  arrivals;
- worker pending counts, audit pending counts, and admin pending counts agree;
- terminal outcomes are not labeled as failures;
- newly completed OCR/transcript/summary rows retain required provenance;
- standard audit detects stale or missing remote SQLite archives;
- deep audit validates a downloaded backup without touching the active DB;
- remote media reconciliation detects missing objects and size mismatches;
- scheduled audits preserve sync status, suppress alert noise, and report
  recovery;
- MCP and admin render the same check IDs/statuses as CLI JSON;
- audit artifacts and alerts contain no corpus content or secrets; and
- source-of-truth audits report upstream omissions without deleting local-only
  historical memory.

## Documentation Deliverables

Implementation must update:

- `README.md` command/configuration reference and roadmap;
- `COMMANDS.md` command examples, output semantics, and exit codes;
- `config.yaml.sample` and `dbrain config env` documentation when audit or
  backup scheduler configuration lands;
- `docs/architecture.md` with the audit/report boundary;
- `docs/schema-migrations.md` with release-audit and backup expectations;
- `docs/maintenance-operations.md` if scheduled SQLite archive behavior is
  added;
- MCP tool documentation and installed skill copies when the tool/skill lands;
  and
- `CHANGELOG.md` for each shipped CLI, scheduler, durability, MCP, or admin
  behavior change.
