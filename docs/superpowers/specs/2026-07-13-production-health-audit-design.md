# Production Health Audit Design

**Date:** 2026-07-13

**Status:** Draft for review, refreshed against the v0.6.0 security baseline

**Repository boundary:** This design covers read-only health assessment of the
configured dbrain runtime, CLI/MCP/web presentation of the same report,
scheduled audit execution, and regression alerting. It does not authorize a
production deployment, run an import, alter the active database, restore a
backup, retry failed work, delete local memory, or mutate an upstream source.
The design was reconciled against `origin/main` at `b733c78` after the July 12
security remediation merge. That review covered the checkout contracts, not the
currently running production binary or production state; the audit must verify
those at runtime rather than infer them from repository history.

## Summary

`dbrain` should have one deterministic production-health engine in
`internal/audit`. The engine should inspect the resolved runtime boundary,
SQLite state, durable metrics, configured importers, enrichment coverage,
archive metadata, database backups, and OKF output. It should return a stable,
content-free `dbrain.audit.v1` report with explicit `pass`, `warn`, `fail`,
`unknown`, and `skipped` results.

The engine receives already-resolved, least-authority capabilities. It does not
open arbitrary paths or URLs, construct unrestricted HTTP clients, receive
archive writer/restore interfaces, or trust database-derived paths. Filesystem
inspection is root-confined, network checks reuse the destination policy of the
owning importer/provider, and every remote/deep operation is separately
bounded.

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
11. Carry the v0.6.0 security contracts forward: runtime build provenance,
    schema identity, root-confined filesystem access, safe outbound
    destinations, private audit artifacts, authenticated web access, and
    bounded MCP work.

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
- No generic audit input accepts an arbitrary filesystem path, URL, archive
  key, endpoint override, or credential.
- No service-auth capability is added for audit web routes. Local automation
  uses the CLI; remote automation uses authenticated MCP or browser sessions.

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

Report membership is deterministic:

- an unfiltered `audit all` emits every closed registry entry, including the
  concrete poll/arrival/parity entries for every known importer family;
- a check outside the selected profile or for an intentionally disabled
  feature is emitted as `skipped` with `required=false` and a stable
  `skip_reason` (`profile_excluded` or `feature_disabled`);
- category, source, or explicit check filters omit checks outside the filter and
  record the selected categories, sources, and check IDs in `scope`; and
- `summary.all` counts every emitted check, including optional and skipped
  checks, while `summary.required` and overall status count only emitted
  `required=true` checks.

No adapter may silently drop an implemented registry check. A check that should
run but is unavailable, timed out, or has insufficient evidence is `unknown`,
not omitted or skipped.

If filtering/configuration leaves zero emitted required checks, overall status
and confidence are both `unknown` and CLI exit is 3. Optional passing findings
cannot turn an empty required scope green.

Profile membership and resolved configuration determine `required`. A check for
an intentionally disabled optional feature is `skipped` and not required. A
configured feature is required. Audit configuration may additionally require a
feature, such as SQLite backup, even when its operational configuration is
currently absent; that condition then fails rather than skips.

### Confidence

Status and confidence are separate. Confidence ordering from strongest to
weakest is `high`, `moderate`, `low`, `unknown`. Each check reports one of those
values. Direct database counts and successful object
metadata comparisons are normally high confidence. A conclusion based on
limited metrics history is moderate. A check that did not run is unknown.

Overall confidence is the lowest confidence among all required, non-skipped
checks. Skipped and optional checks do not reduce overall confidence.

### Deterministic Threshold Boundaries

All duration thresholds use half-open intervals. For warning threshold `W` and
failure threshold `F`, age `< W` passes, `W <= age < F` warns, and `age >= F`
fails. Thus the exact 24-hour pending age and 36-hour backup age are warnings;
the exact 72-hour age is a failure. Interval-count checks follow the same rule:
OKF age below two resolved sync intervals passes, age from two through less
than four warns, and age at or above four fails.

Scheduler timing uses:

```text
warn_after = configured_interval + configured_jitter + duration_allowance + 15m
fail_after = warn_after + (2 * configured_interval)
```

`duration_allowance` is the nearest-rank p95 of successful completed run
durations in the requested metrics window when at least five exist, the maximum
successful duration when one through four exist, and zero when none exist. A
zero fallback lowers confidence to `moderate`; missing latest-run evidence can
still make the check `unknown`.

`metrics.window` passes when parseable history covers `--since`. It warns when
history is shorter but contains both the latest completed attempt and at least
two configured scheduler intervals with two completed attempts. It is
`unknown` otherwise. A scheduler gap is "explained" only by an explicit
persisted lock-skip, disabled/enabled transition, or process start/stop marker
covering that gap; elapsed wall time alone never supplies an explanation.

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
  capabilities.go      least-authority dependency interfaces
```

`audit.Run` receives resolved `config.Config`, an already opened read-only
store snapshot, a clock, metrics reader, root-confined filesystem inspectors,
runtime version, and optional remote/upstream read clients. This keeps package
tests deterministic and prevents command wiring from containing health policy.

The capability boundary is explicit:

```text
StoreSnapshot       relational counts through one read transaction
DatabaseInspector   read-only integrity, core identity, and migration compatibility
VaultInspector      root-confined metadata/stat operations for DB-derived logical paths
OKFInspector        root-confined manifest and aggregate validation
MetricsReader       aggregate reads from the resolved metrics file only
ArchiveLister       list/head metadata only; no put/delete/get
ArchiveReader       bounded get capability injected only for explicit deep CLI runs
UpstreamInventory   importer-owned bounded identity inventory
RuntimeVersion      version.Current plus the compiled security-baseline ID
```

The runner never receives archive writer, prune, active restore, generic
filesystem, or unrestricted HTTP interfaces. It cannot accept a caller-supplied
path, URL, object key, bucket, or endpoint override.

### Read-Only Store Boundary

All CLI stats and audit commands must use `store.OpenReadOnly`. `OpenReadOnly`
must remain migration-free and query-only.

Audit database counts should execute through a new read-snapshot abstraction in
`internal/store`. The abstraction begins one SQLite read transaction and
exposes focused audit/stat methods through an interface. This gives relational
checks one consistent database view without copying the multi-gigabyte database
or holding a writable connection.

Active-database integrity must use a read-only DSN and expose separate results
for SQLite `quick_check`, `foreign_key_check`, dbrain core-schema identity, and
migration compatibility. It must not reuse `sqlitearchive.validateSQLite`
unchanged because that helper currently opens its quick-check connection with a
normal DSN. Temporary deep candidates must call
`store.ValidateRestorableDatabase`; ad hoc table-presence tests are not a
substitute. Schema identity proves that a file is a compatible dbrain database,
not that the archive is cryptographically authentic. Until signed archive
metadata exists, deep reports state `archive_authenticity=unverified` even when
integrity and identity pass.

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
- importer packages for optional read-only upstream inventories;
- `internal/vaultfs` (or an equally strict generalized root capability) for
  every DB-derived note/media path and for OKF inspection;
- `internal/safehttp` for generic outbound delivery with redirect and dial-time
  destination enforcement; and
- `internal/version` for the running binary identity and compiled security
  baseline.

Before the audit uses OKF validation, `okf.ValidateBundle` must be replaced or
wrapped by a root-confined aggregate inspector that requires a readable valid
manifest and `exported_at`. It may not return document paths, titles, source
keys, or raw parse errors to the shared report.

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
    "sources": ["apple-notes", "feeds", "github-stars", "safari-tabs", "x-bookmarks", "youtube-liked", "youtube-watch-later"],
    "check_ids": [],
    "filtered": false,
    "whole_system": true
  },
  "started_at": "2026-07-13T23:02:24Z",
  "completed_at": "2026-07-13T23:02:31Z",
  "status": "fail",
  "confidence": "high",
  "boundary": {
    "layout": "xdg",
    "config_verified": true,
    "database_verified": true,
    "version": "v0.6.0",
    "commit": "b733c78",
    "git_status": "clean",
    "platform": "darwin/arm64",
    "security_baseline": "v0.6.0-security-pass",
    "security_baseline_epoch": 1,
    "schema_version": 11,
    "schema_compatibility": "current_compatible"
  },
  "summary": {
    "all": {"pass": 18, "warn": 2, "fail": 1, "unknown": 0, "skipped": 4},
    "required": {"pass": 17, "warn": 1, "fail": 1, "unknown": 0}
  },
  "checks": []
}
```

The shared report never contains absolute config, database, vault, metrics,
temporary, archive, or OKF paths. It records the resolved layout
(`explicit_config`, `explicit_root`, or `xdg`), verification booleans, running
build identity, security-baseline ID, platform, and schema compatibility. Local
CLI human output prints exact resolved paths in a separate, non-persisted target
preamble. With `--json --include-identifiers`, the CLI emits the explicit local
wrapper `{"schema":"dbrain.audit.local.v1","local_target":{...},
"local_details":{"checks":[...]},"report":{...dbrain.audit.v1...}}`; it does
not mutate the shared report. `local_target` holds the exact resolved paths.
Each `local_details.checks` entry has `check_id`, up to 100 `row_ids`, up to 100
`source_keys`, up to 20 `cleanup_paths`, and `truncated`; empty slices encode as
`[]`. That wrapper is unavailable to MCP, web, scheduled reports, metrics, and
alerts and is never accepted as an engine input.

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
are check-specific and may contain seconds, bytes, integer counts, UTC RFC3339
timestamps, booleans, closed-registry enum/error-code strings, and bounded
arrays/objects declared by the registry. They never contain free-form provider
or corpus prose.

Filtered category/source commands set `scope.filtered=true` and
`scope.whole_system=false`. Their overall status describes only the selected
scope and must never be labeled whole-system health by CLI, MCP, or web.
`scope.categories`, `scope.sources`, and `scope.check_ids` contain the effective
filter after profile and explicit-source resolution. Empty `check_ids` means no
explicit check-ID filter, not an unknown set.

By default, evidence must not contain titles, URLs, source/item keys, transcript
text, OCR text, note text, prompts, secrets, cookies, object-store credentials,
or raw error bodies that may contain content. `--include-identifiers` may add
bounded internal row IDs, source keys, and local cleanup paths to the local CLI
wrapper only. It is unavailable to MCP, web, scheduled reports, and webhook
alerts.

Summaries, remediation text, and errors are selected from fixed templates keyed
by check ID and sanitized error code. Arbitrary provider/upstream errors may not
flow into those strings. Corpus/source URLs are prohibited; an explicitly
configured dbrain admin origin is an operational URL and may appear only in an
alert action link after validation as a pure HTTP(S) origin with no userinfo,
path, query, or fragment. It comes from configuration, never the request `Host`
or forwarded headers.

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
silently omitted. Each relational check has a five-second child deadline; the
metrics and manifest checks each have ten seconds. The runner executes at most
four local checks concurrently and only after obtaining the shared read
snapshot.

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
Remote metadata requests use a 30-second per-request deadline, eight-request
maximum concurrency, and the complete-run deadline. SQLite integrity and OKF
validation each have a two-minute child deadline. Exhausting any child,
pagination, object-count, or run budget produces `unknown` with aggregate
progress counts; partial evidence never passes.

### Deep

Designed for explicit release validation and operator-invoked drills. It
includes standard checks plus:

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
deadline with `--timeout`. Deep is CLI-only in v1 and is not scheduled. Each
metadata/listing request has a 30-second whole-request deadline; remote listing
is capped at 1,000,000 objects and 10,000 pages, and concurrency is capped at
eight. Large archive GETs instead use a ten-second connect timeout, ten-second
TLS-handshake timeout, 30-second response-header timeout, 60-second read-idle
watchdog that resets on forward progress, and the remaining deep-run deadline
(never more than two hours) as the whole-stream deadline. The default compressed
download ceiling is 20 GiB, decompressed SQLite ceiling is 100 GiB, and total
temporary-space budget is 120 GiB; configuration may lower these limits but
increasing them requires an explicit CLI flag. The audit checks free space
before downloading and enforces ceilings while streaming rather than trusting
object metadata. Any incomplete listing, download, decompression, or budget
exhaustion is `unknown`, not pass.

## Check Families

### Boundary And Integrity

- resolved config, DB, log, media, vault, temp, and OKF paths;
- binary version, commit, dirty state when available, and schema version;
- platform and compiled security-baseline ID from the running binary rather
  than the checkout;
- separate core-schema identity and migration compatibility without applying
  migrations;
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

An explicit source command is an operator override for parity only. It emits
and requires that source's concrete parity ID from the registry even if the
source is disabled in the resolved sync policy. In that case its concrete poll
ID is emitted as `skipped(feature_disabled)` and arrivals remain optional; the
report does not pretend the scheduler should have been polling a disabled
source.

Upstream inventories reuse the owning importer's authenticated client,
pagination, destination policy, response-size limits, and identity semantics.
They do not accept audit-specific endpoint or URL overrides. Generic imported
URLs use `safehttp`; operator-configured private application or object-store
origins are authorized only as their exact canonical origins and never through
global private-network access. Clients for trusted configured origins are not
shared with untrusted imported URLs.

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

The cutover is the `applied_at` value of the named migration that lands the
provenance-preservation fix. Successful rows whose authoritative completion or
update timestamp is before that durable marker are legacy and may warn when
provenance is missing. A successful row at or after the marker fails if a
required provenance field is absent. Missing or contradictory cutover metadata
makes the provenance check `unknown`; build dates and wall-clock guesses are
not used.

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
failure. They are configurable durations. A required archive with no remote
snapshot is an immediate failure.

Standard remote-media verification selects keys by authoritative `archived_at`.
An archived record with a key but no parseable `archived_at` is invalid local
state and fails. The check HEADs the newest 500 keys archived within `--since`
and 100 older keys. Recent keys sort by `archived_at` descending and then
`SHA-256(key)` ascending for a deterministic tie break. If more than 500 recent
keys exist, the report records the population and cap and confidence is
`moderate`; this bounded sample may pass but never claims complete
reconciliation. Older sampling ranks each key by
`SHA-256("dbrain.audit.media.v1" || provider-logical-name || UTC-ISO-week ||
key)` and takes the lowest 100, making selection deterministic and rotating
weekly without emitting keys. Missing keys or size mismatches fail.

Standard media confidence is `high` only when both recent and older populations
are fully checked. If either population exceeds its cap and is sampled,
confidence is `moderate`, even when every checked object matches.

Deep verification paginates the complete configured remote prefix and
reconciles every local recorded key/size within its explicit budgets. Local
recorded keys missing remotely or with mismatched sizes fail. Remote-only keys
do not fail append-only durability; optional
`durability.media_remote_only` counts them and warns when nonzero so abandoned
test or legacy objects remain visible. A truncated listing, timeout,
inconsistent pagination, or object-cap exhaustion makes required
reconciliation `unknown`.

Remote SQLite backup is required when
`scheduler.sqlite_archive.enabled=true` or
`audit.require.sqlite_backup=true`. Credentials and provider configuration are
capability, not enablement; their presence alone does not silently turn a local
installation into a promised backup service. When backup is required but the
provider configuration or required credential reference is absent, the check
is a deterministic configuration `fail`. A configured provider whose credential
resolver or remote listing fails at runtime is `unknown`. Successful listing
with no snapshot is `fail`. The check is skipped on an intentionally local-only
installation unless explicitly required. A separate optional configuration
finding warns when credentials are present but neither archive scheduling nor
the audit requirement is enabled.

## Normative Check Registry

The implementation owns a typed registry, not a loose collection of callbacks.
Each entry defines stable ID, category, profile membership, required-condition,
evidence schema, timeout, thresholds, and status classifier. Configuration may
override documented durations/counts but may not replace classifier code.

Initial registry rules:

Category mapping is closed: `boundary.*` and `integrity.*` are `boundary`;
`scheduler.*` and `metrics.*` are `scheduler`; `imports.*` and `upstream.*` are
`imports`; `pipeline.*` is `pipeline`; and `durability.*` is `durability`.
These are the only values accepted by `scope.categories` in v1.

Security baselines use a compiled ordered registry, never lexical string
comparison. Epoch `0` is `pre-v0.6.0` and fails. Epoch `1` is
`v0.6.0-security-pass` from merge `b733c78` and is the initial minimum. A future
successor must add a monotonically increasing epoch and ID to this registry in
the same change that introduces the new contract. Unknown IDs/epochs are
`unknown`, not assumed newer.

| Check ID | Profiles | Required when | Deterministic status rule |
| --- | --- | --- | --- |
| `boundary.config` | all | always | `pass` when the resolved layout and config source are verified; command-level exit 3 if resolution cannot produce a report. |
| `boundary.runtime` | all | always | `pass` for known version/commit/platform and a clean build; `warn` for dirty build; `unknown` when build identity is unavailable; `fail` when `--expect-commit` is supplied and does not match. |
| `boundary.security_baseline` | all | always | `pass` when the running binary declares a registered ID/epoch pair whose epoch is at least 1; `fail` for registered epoch 0; `unknown` when the ID/epoch pair is absent, unregistered, or mismatched. |
| `boundary.database` | all | always | `pass` when the configured DB opens query-only. Any open failure is a bootstrap failure: no report is produced and the command exits 3. Schema mismatch is classified by `integrity.schema_identity` after a successful open. |
| `integrity.schema_identity` | all | always | `pass` when core dbrain tables/columns match; `fail` for a verified mismatch; `unknown` on read/timeout error. Evidence distinguishes `legacy_compatible` and `current_compatible`. |
| `integrity.migration_compatibility` | all | always | `pass` when migration metadata is complete, known, and not newer than the binary; `fail` for verified missing/unknown/incompatible metadata; `unknown` on read error. |
| `integrity.sqlite_quick_check` | standard, deep | always | `pass` only for exact `quick_check=ok`; `fail` for reported violations; `unknown` on timeout/read error. |
| `integrity.foreign_keys` | standard, deep | always | `pass` for zero `foreign_key_check` rows; `fail` for one or more; `unknown` on timeout/read error. |
| `scheduler.latest_sync` | all | scheduler enabled | With `W=interval+jitter+duration_allowance+15m` and `F=W+2*interval`, `pass` for age `<W`, `warn` for `W<=age<F`, and `fail` for age `>=F` or an unresolved failed latest attempt. |
| `scheduler.stage_coverage` | all | scheduler enabled | `pass` when every resolved selected stage completed in the latest completed run; `fail` when a selected stage is absent; `unknown` when the run record is incomplete. |
| `scheduler.continuity` | standard, deep | scheduler enabled | `pass` with no gap at or above `W`; `warn` for any gap `W<=gap<F` or any explicitly explained gap at/above `W`; `fail` for an unexplained gap at/above `F`; `unknown` when metrics do not meet the window sufficiency rule. |
| `metrics.window` | all | scheduler enabled | `pass` when coverage reaches `--since`; `warn` when shorter but containing the latest completed attempt plus at least two intervals and two completed attempts; `unknown` otherwise. |
| `durability.media_local_coverage` | all | media archive or local media enrichment enabled | `pass` when no media was pruned before terminal enrichment coverage; `fail` for any uncovered pruned asset; orphan rows are a separate optional warning. |
| `durability.media_remote` | standard, deep | remote media archive enabled | `pass` for the profile-defined sample/reconciliation; `fail` for missing/size-mismatched recorded keys or invalid recorded archive timestamps; `unknown` for incomplete/budget-exhausted evidence. |
| `durability.media_remote_only` | deep | never (`required=false`) | `pass` for zero remote-only keys; `warn` for one or more; never proposes deletion. |
| `durability.sqlite_backup_configuration` | all | never (`required=false`) | `warn` when archive capability is configured but neither scheduling nor explicit audit requirement is enabled; `pass` when capability exists and scheduling/requirement is enabled; `skipped(feature_disabled)` when no archive capability is configured. |
| `durability.sqlite_backup_age` | standard, deep | archive scheduler enabled or explicitly required | For defaults `W=36h`, `F=72h`: `pass` below `W`, `warn` at/above `W` and below `F`, `fail` at/above `F`, for no snapshot, or for definitively missing required provider/credential configuration; `unknown` for resolver/network/listing failure after configuration is present. |
| `durability.sqlite_restore` | deep | SQLite backup required | `pass` when the newest bounded candidate downloads, decompresses, passes read-only quick/FK checks and `store.ValidateRestorableDatabase`; `fail` for verified invalid content/identity; `unknown` for interrupted or budget-exhausted retrieval. Evidence always states archive authenticity is unverified. |
| `durability.okf_freshness` | all | OKF export enabled | `pass` below two intervals, `warn` at/above two and below four, `fail` at/above four or for a missing/invalid manifest. |
| `durability.okf_validation` | standard, deep | OKF export enabled | `pass` for conformant summary with zero broken links/errors; `fail` for validation errors; `unknown` for incomplete traversal/read failure. |

Core checks use these exact evidence fields (all timestamps are UTC RFC3339 and
all durations are integer seconds):

| Check ID | Timeout class | Evidence fields |
| --- | --- | --- |
| `boundary.config` | `bootstrap` | `layout`, `config_source`, `verified` |
| `boundary.runtime` | `local_query` | `release_known`, `commit_known`, `platform_known`, `git_status`, `expected_commit_matched` |
| `boundary.security_baseline` | `local_query` | `baseline_id`, `baseline_epoch`, `minimum_epoch` |
| `boundary.database` | bootstrap | `opened_query_only` |
| `integrity.schema_identity` | `local_query` | `compatibility`, `missing_table_count`, `missing_column_count` |
| `integrity.migration_compatibility` | `local_query` | `user_version`, `supported_version`, `applied_count`, `compatibility` |
| `integrity.sqlite_quick_check` | `sqlite_or_okf_integrity` | `result`, `violation_count` |
| `integrity.foreign_keys` | `sqlite_or_okf_integrity` | `violation_count` |
| `scheduler.latest_sync` | `metrics_or_manifest` | `latest_attempt_at`, `latest_success_at`, `age_seconds`, `warn_after_seconds`, `fail_after_seconds`, `duration_allowance_seconds`, `duration_allowance_source` |
| `scheduler.stage_coverage` | `metrics_or_manifest` | `expected_stage_count`, `completed_stage_count`, `missing_stage_count`, `record_complete` |
| `scheduler.continuity` | `metrics_or_manifest` | `observed_attempt_count`, `gap_count`, `explained_gap_count`, `unexplained_gap_count`, `largest_gap_seconds`, `warn_after_seconds`, `fail_after_seconds` |
| `metrics.window` | `metrics_or_manifest` | `requested_seconds`, `covered_seconds`, `completed_attempt_count`, `latest_attempt_present`, `latest_completed_present`, `parse_error_count` |
| `durability.media_local_coverage` | `local_query` | `eligible_local_count`, `uncovered_pruned_count`, `orphan_count` |
| `durability.media_remote` | `remote_metadata` | `population_count`, `checked_count`, `recent_population_count`, `recent_checked_count`, `older_population_count`, `older_checked_count`, `missing_count`, `size_mismatch_count`, `invalid_timestamp_count`, `sample_mode`, `inventory_complete` |
| `durability.media_remote_only` | `remote_metadata` | `remote_only_count`, `inventory_complete` |
| `durability.sqlite_backup_configuration` | `local_query` | `capability_configured`, `scheduler_enabled`, `audit_required`, `configuration_state` |
| `durability.sqlite_backup_age` | `remote_metadata` | `configuration_state`, `archive_count`, `latest_age_seconds`, `latest_size_bytes`, `warn_after_seconds`, `fail_after_seconds`, `listing_complete` |
| `durability.sqlite_restore` | `deep_stream` | `compressed_bytes`, `decompressed_bytes`, `quick_check`, `foreign_key_violation_count`, `schema_compatibility`, `migration_compatibility`, `archive_authenticity`, `cleanup_complete` |
| `durability.okf_freshness` | `metrics_or_manifest` | `manifest_valid`, `exported_at`, `age_seconds`, `warn_after_seconds`, `fail_after_seconds` |
| `durability.okf_validation` | `sqlite_or_okf_integrity` | `manifest_valid`, `document_count`, `broken_link_count`, `validation_error_count`, `traversal_complete` |

The v1 enum registry is closed: `layout` is `explicit_config`,
`explicit_root`, or `xdg`; `config_source` is `flag`, `environment`, or
`default`; `git_status` is `clean`, `dirty`, or `unknown`; schema/migration
`compatibility` is `current_compatible`, `legacy_compatible`, or
`incompatible`; `duration_allowance_source` is `p95`, `max_observed`, or
`none`; integrity `result`/`quick_check` is `ok` or `violation`; `sample_mode`
is `complete`, `bounded_sample`, or `full_inventory`; `archive_authenticity`
is only `unverified` in v1; and `configuration_state` is `not_configured`,
`configured_disabled`, `required_ready`, `required_missing_provider`,
`required_missing_credential`, or `resolution_error`. New enum values require a
schema-version decision.

The concrete source registry is closed in v1; the tokens below are the literal
check IDs, not placeholders:

| Source | Poll check ID | Arrival check ID | Deep parity check ID |
| --- | --- | --- | --- |
| Apple Notes | `imports.apple_notes.poll` | `imports.apple_notes.arrivals` | `upstream.apple_notes.parity` |
| Safari tabs | `imports.safari_tabs.poll` | `imports.safari_tabs.arrivals` | `upstream.safari_tabs.parity` |
| X bookmarks | `imports.x_bookmarks.poll` | `imports.x_bookmarks.arrivals` | `upstream.x_bookmarks.parity` |
| GitHub stars | `imports.github_stars.poll` | `imports.github_stars.arrivals` | `upstream.github_stars.parity` |
| YouTube liked | `imports.youtube_liked.poll` | `imports.youtube_liked.arrivals` | `upstream.youtube_liked.parity` |
| YouTube watch later | `imports.youtube_watch_later.poll` | `imports.youtube_watch_later.arrivals` | `upstream.youtube_watch_later.parity` |
| Feeds | `imports.feeds.poll` | `imports.feeds.arrivals` | `upstream.feeds.parity` |

Every poll check is in all profiles, uses `metrics_or_manifest`, and is required
only when that source and the scheduler are enabled. It uses the same `W`/`F`
classifier as `scheduler.latest_sync`. Its evidence schema is exactly
`attempted_at`, `succeeded_at`, `age_seconds`, `warn_after_seconds`,
`fail_after_seconds`, `attempt_count`, `success_count`, and `failure_count`.
Every arrival check is in all profiles, always optional, and uses
`metrics_or_manifest`; its evidence is `quiet_seconds` plus bounded daily
aggregate objects containing `day`, `created`, `updated`, `unchanged`,
`skipped`, `linked`, `blocked`, and `failed`. Quiet arrivals always pass.
Every parity check is deep/source-command only, uses `upstream_inventory`, and
is required for the configured deep set or explicit source override. Evidence
is `upstream_count`, `matched_local_count`, `missing_local_count`, `page_count`,
and `inventory_complete`; complete zero-missing passes, complete missing fails,
and incomplete/auth/pagination/snapshot failure is unknown. V1 caps each source
at 100,000 upstream identities and 10,000 pages inside its five-minute timeout;
reaching either cap before an importer-declared end makes inventory incomplete.

Pipeline checks use five closed stage IDs. Per-kind/source detail is bounded
aggregate evidence within the stage; it does not create dynamic check IDs.

| Stage | Partition check ID | Pending-age check ID | Population |
| --- | --- | --- | --- |
| X hydration | `pipeline.hydration.partition` | `pipeline.hydration.pending_age` | X bookmark and first-class quote items eligible for post/media hydration repair |
| Extraction | `pipeline.extraction.partition` | `pipeline.extraction.pending_age` | Sources plus Apple Note and Safari tab materialization rows |
| Summary | `pipeline.summary.partition` | `pipeline.summary.pending_age` | Source summaries plus item summaries, including X media and Apple Notes |
| Transcription | `pipeline.transcription.partition` | `pipeline.transcription.pending_age` | Downloadable X video/animated-GIF items governed by transcription policy |
| OCR | `pipeline.ocr.partition` | `pipeline.ocr.pending_age` | Downloaded, unpruned X photos governed by OCR policy |

All ten pipeline checks are in all profiles, use `local_query`, and are required
when their existing worker stage is selected. Partition evidence is exactly
`total`, `current`, `pending`, `blocked`, `terminal`, `failed`, `unknown`, and
`partition_valid`, plus bounded `by_kind` objects with those same counts. A
non-exhaustive/overlapping partition or nonzero `unknown` fails. Pending-age
evidence is `pending_count`, `oldest_pending_age_seconds`,
`warn_after_seconds`, and `fail_after_seconds`; defaults use the 24/72-hour
half-open classifier.

The provenance registry uses one durable cutover: the `applied_at` timestamp of
the new append-only migration named `audit_provenance_v1`. Its numeric migration
version is selected only after checking all branch histories and is not fixed by
this design. These are the only v1 provenance check IDs:

| Check ID | Successful population | Required non-empty fields | Authoritative row timestamp |
| --- | --- | --- | --- |
| `pipeline.item_summary.provenance` | `item_enrichments.role='summary'` with successful status, including X media and Apple Note summaries | `raw_json`, `model`, `prompt_version`, `tool`, `tool_version`, `input_hash`, `completed_at` | `item_enrichments.updated_at` |
| `pipeline.item_ocr.provenance` | `item_enrichments.role='ocr'` with successful status | `raw_json`, `model`, `tool`, `tool_version`, `input_hash`, `completed_at` | `item_enrichments.updated_at` |
| `pipeline.x_media_transcript.provenance` | `item_enrichments.role='x_media_transcript'` with successful status | `raw_json`, `model`, `tool`, `tool_version`, `input_hash`, `completed_at` | `item_enrichments.updated_at` |
| `pipeline.source_summary.provenance` | successful `source_summary_versions` rows | `summary_json`, `summary_model`, `summary_prompt_version`, `summary_tool`, `summary_tool_version`, `content_hash`, `summarized_at` | `source_summary_versions.summarized_at` |

All four provenance checks are in all profiles, use `local_query`, and are
required when the owning stage is enabled. Evidence is exactly
`successful_count`, `complete_count`, `legacy_missing_count`,
`post_cutover_missing_count`, `cutover_at`, and `missing_by_field` integer
counts. Zero missing passes; legacy-only missing warns; any post-cutover missing
fails; absent/contradictory cutover metadata is unknown.

Evidence values may be integers, booleans, bytes, seconds, UTC RFC3339
timestamps, stable registry enums/error codes, and bounded arrays/objects whose
keys are declared above. Arbitrary strings, paths, URLs, object keys, ETags,
provider bodies, and corpus values are forbidden. Each core check uses the
exact evidence fields declared above; adding a field or enum
requires a schema-version decision.

Every registry entry names a timeout class. `bootstrap` is ten seconds and
fails the command without a report; `local_query` is five seconds in fast and
30 seconds in standard/deep; `metrics_or_manifest` is ten seconds;
`sqlite_or_okf_integrity` is two minutes; `remote_metadata` is 30 seconds per
request and two minutes per check; `upstream_inventory` is five minutes per
source; and `deep_stream` uses the connect/TLS/header/read-idle controls and
whole-stream deadline defined by the Deep profile. `durability.media_remote`
uses `remote_metadata`; `durability.sqlite_restore` uses `deep_stream`.
Configuration may lower classes, never silently remove them. Timeout produces
`unknown` for a required check.

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
| `--expect-commit <sha>` | empty | Fail `boundary.runtime` when the running binary does not report this commit. |
| `--max-archive-bytes`, `--max-database-bytes`, `--max-temp-bytes` | deep defaults | Explicitly raise or lower deep streaming ceilings; CLI deep only. |

Human output starts with target boundary, overall status, observation window,
and build identity. It then prints failed and unknown checks, warnings, and a
compact category summary. Passing detail is available in JSON rather than
flooding the terminal.

`--include-identifiers` affects only the local adapter. It cannot be set by
configuration or environment for scheduled, MCP, or web execution. Without it,
CLI JSON is byte-for-byte the same shared report schema those adapters consume.

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
   commit, platform, security-baseline ID, and exact production boundary;
4. run `dbrain audit all --profile deep --expect-commit <released-sha> --json`
   with the explicit production config file;
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
  post_sync_fast: true
  standard_interval: 6h
  since: 7d
  alert:
    webhook_url: ""
    bearer_token_ref: ""
    allow_private_origin: false
    consecutive_observations: 2
    repeat_after: 24h
```

There is no generic `profile` setting: schedule ownership is fixed and
unambiguous. `post_sync_fast` owns local fast runs and `standard_interval` owns
periodic standard runs. Deep remains explicit CLI-only in v1.

When enabled:

1. run a fast audit from a post-run hook after every scheduled sync attempt,
   outside the sync stage plan and after the sync lock/result are settled;
2. run a standard audit on the configured interval without overlapping another
   audit;
3. write content-free completed reports to UTC-day files such as
   `<log_dir>/audit/reports/2026-07-13.jsonl`;
4. emit compact `audit.run.completed` metrics with profile, status, duration,
   and status counts; and
5. persist alert transition state atomically at
   `<log_dir>/audit/alert-state.json`.

Audit output paths are fixed generated names below a private
`<log_dir>/audit/` directory, not caller-supplied paths. That subdirectory and
its `reports/` child are opened as confinement capabilities and created `0700`;
files are `0600`, symlink traversal is rejected, daily report append uses a
no-follow private handle, and state replacement uses a same-directory `0600`
temporary file, fsync, and atomic rename. Daily rotation makes retention
enforceable by both age and total bytes (defaults: 90 days and 256 MiB). Raw
provider errors and report paths are never logged.

An audit failure must not rewrite a successful sync as a failed import. Sync
status and audit status remain separate events.

Alerts are transition-based and state is keyed by exact profile and check ID.
For alert ordering, `pass < warn < unknown < fail`; `skipped` is not a
severity. Optional (`required=false`) findings are persisted and displayed but
do not send webhooks in v1.

| Previous confirmed state | New observation | Transition rule |
| --- | --- | --- |
| none | `pass` | Establish baseline; no notification. |
| none or `pass` | required `warn`/`unknown`/`fail` | Increment the pending counter for that exact status; confirm and notify at `consecutive_observations`. |
| confirmed non-pass | higher severity | Reset the pending counter for the higher status; confirm and notify at `consecutive_observations`. |
| confirmed non-pass | same status | Retain confirmation; repeat only when `repeat_after` has elapsed. |
| confirmed non-pass | lower non-pass severity | Confirm the lower state after `consecutive_observations`, then notify the de-escalation. |
| confirmed non-pass | `pass` | Confirm recovery immediately on one observation and notify. |
| any | `skipped(profile_excluded)` | Do not change state; another profile cannot resolve it. |
| confirmed non-pass | `skipped(feature_disabled)` or `required` becomes false through explicit configuration | Resolve immediately with reason `resolved_by_configuration` and notify once. |
| optional finding | any | Update persisted display state only; no webhook. |

Only these exact failure transitions bypass debounce and confirm on one
observation: `integrity.schema_identity=fail`,
`integrity.migration_compatibility=fail`,
`integrity.sqlite_quick_check=fail`, `integrity.foreign_keys=fail`,
`durability.media_remote=fail`, and `durability.sqlite_restore=fail`. Initial
no-state failures for those IDs follow the same immediate exception; warnings
and unknowns never bypass debounce.
Pending counters reset when status changes. If overall status remains `fail`
but a new check confirms, an existing check escalates, or a required check
becomes `unknown`, the changed check set produces a notification. Overall
recovery is emitted only when no confirmed required non-pass check remains for
that profile.

The initial delivery adapter is a constrained JSON webhook. Its URL must be an
HTTP(S) URL with host and optional path, but no userinfo, query, or fragment;
public destinations require HTTPS. Private/link-local/loopback destinations are
rejected unless `allow_private_origin=true`, in which case only the exact
configured canonical origin is added to `safehttp.AllowedPrivateOrigins`.
Global private-network access is forbidden. Delivery uses the shared safe HTTP
dial policy, environment proxies disabled, redirects disabled (requiring a
small `safehttp` extension), a ten-second timeout, a 64 KiB request ceiling,
and a 64 KiB discarded response ceiling. Authentication comes only from the
separate typed `bearer_token_ref`; credentials in URLs are rejected.

The webhook URL and token are redacted from config/status output and never
written to reports or metrics. Alert bodies contain build identity, overall
status, check IDs, fixed summaries, observation time, and the configured admin
origin when valid; they contain no paths, credentials, identifiers, provider
errors, or corpus content. A missing webhook still leaves exit codes, JSONL
reports, metrics, logs, MCP, and admin visibility operational.

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

Audit and scheduler wiring must split object-store capabilities: standard audit
receives `ArchiveLister`, deep audit additionally receives a bounded
`ArchiveReader`, and only the separate backup scheduler receives
`ArchiveWriter`. The audit never receives the existing combined put/list/get
interface. Custom object-store endpoints are resolved from operator
configuration, validated once, and authorized only as their exact origin; no
audit flag can replace them. Their SDK transport must use the same no-proxy,
redirect-revalidation, and dial-time DNS/IP policy as `safehttp`, with private
access limited to that exact configured origin.

Deep verification creates a generated `0700` directory beneath the configured
temporary root and `0600` files through a root-confined, no-symlink capability.
It streams through the compressed, decompressed, and free-space ceilings,
calls read-only integrity checks plus `store.ValidateRestorableDatabase`, and
removes the directory on every success, error, timeout, and cancellation path.
It never invokes `sqlitearchive.Restore`, moves the active DB/WAL/SHM, or calls
archive/prune operations.

## MCP Design

Add one tool:

```text
dbrain_audit
```

Arguments:

- `profile`: `fast` or `standard`, default `fast`.

The tool is advertised and callable over stdio, or over HTTP/tsnet only when
bearer authentication is required and successfully configured. It is omitted
from `tools/list` and rejected when MCP HTTP bearer auth is disabled; tailnet
reachability alone is insufficient. This is enforced by transport capability
wiring, not a caller argument.

MCP always returns one adapter envelope:
`{"report":{...dbrain.audit.v1...},"freshness":{...}}`. A fast call executes
only the full local fast profile, under a fixed ten-second tool deadline and
process-wide singleflight, and returns `freshness.status=current` with age zero.
A standard call returns the newest persisted exact-profile standard report and
its freshness; it never initiates remote network work. Category filtering is
not exposed in v1, avoiding derived mutation of an immutable cached report.
The tool cannot request deep, change `--since`, supply a path, URL, endpoint,
source identifier, or archive key, or expose identifiers. Output is capped at
256 KiB and the closed registry bounds the maximum emitted checks.
Existing `dbrain_stats_*` tools remain available for exploratory counts, but
MCP guidance should use `dbrain_audit` for health claims.

The MCP server already opens its store read-only and caps protocol batches, but
those transport limits do not bound one tool call. Tool tests must verify the
internal deadline/singleflight/output bounds, exact-profile caching, schema
agreement, empty arrays rather than `null`, content exclusion, status
aggregation, and no mutation.

## Admin Design

The authenticated admin page should become the human presentation of the latest
shared audit report, while retaining source-activity drill-downs.

API additions:

- `GET /api/audit/latest?profile=standard`: return the newest persisted report
  for that exact profile plus a freshness envelope; it never runs an audit;
- `GET /api/audit/history?profile=standard&limit=N`: return bounded compact
  summaries for that exact profile; and
- `POST /api/audit/run` with a bounded JSON body
  `{"profile":"fast|standard"}`: start an authenticated on-demand audit without
  permitting deep download/restore checks; and
- `GET /api/audit/runs/{audit_id}`: return bounded state for an on-demand run
  owned by this process, including the completed freshness/report envelope.

Audit APIs fail closed when web authentication is disabled, even for local
listeners; CLI remains available. They are mounted only in the authenticated
application mux, never `/share/`, and are not added to `serviceAuthRoute`.
Existing session authorization applies to GET and POST, and the shared Origin
guard applies to POST. Handlers enforce method, `application/json`, a 4 KiB body
limit, bounded history limit, one process-wide in-flight audit, and a minimum
60-second interval between standard starts. History defaults to 20 and rejects
values outside 1 through 100. A newly accepted POST returns HTTP 202 with
`audit_id`, `profile`, `state=running`, and `status_path`. A duplicate request
for the same active profile returns HTTP 202 with the same ID; a different
profile returns HTTP 409 with the active ID/profile and starts nothing. Invalid
profile/body returns 400, unauthenticated returns the existing auth response,
and rate-limited standard starts return 429 with `retry_after_seconds`.

Every completed on-demand report is persisted through the same private daily
report store before its run state becomes `completed`. The run-status endpoint
returns `running`, `completed`, or `failed`; completed includes the same
`report`/`freshness` envelope, while failed contains only a stable sanitized
error code. Process restart may forget in-flight IDs, but completed reports
remain discoverable through exact-profile latest/history.

In-process run state retains all active runs plus at most 100 terminal
completed/failed records for 24 hours. On insert and once per hour, terminal
records older than 24 hours are removed first, then the oldest terminal records
are evicted until the count is 100. Active runs are never evicted. An evicted or
unknown ID returns 404 without revealing whether it existed before; the
persisted exact-profile report APIs remain the durable history surface.

The page should show:

1. overall health, build, verified runtime layout, last sync, and last audit
   time;
2. configured importers with separate successful-poll and latest-arrival times;
3. pipeline partitions with terminal outcomes separate from failures;
4. media archive, SQLite backup, and OKF durability cards;
5. failed/unknown checks first, with threshold and remediation detail; and
6. recent audit status history and recovery transitions.

Mobile layout must stack cards without horizontal overflow and move selected
check detail into view. Check IDs and long fixed summaries must wrap safely.

The existing `backlog.drained` field remains for API compatibility, but its UI
label becomes **Source backlog drained** and its payload gains an explicit scope
description. Whole-system health comes only from the audit report.

Page loads never trigger scans. The UI uses the latest exact-profile scheduled
report, with an explicitly labeled authenticated fast refresh and on-demand
standard run. A newer fast report cannot replace, recover, or make a standard
health view current.

Persisted reports have a per-profile freshness deadline: fast is stale after
twice the configured sync interval; standard is stale after the greater of
twice `standard_interval` or 12 hours. `GET` returns
`{"report":...,"freshness":{"status":"current|unknown","age_seconds":...,
"deadline_seconds":...}}`. Freshness is presentation metadata, not an
unregistered synthetic check injected into the immutable report. An absent or
stale report is rendered as **unknown/stale**, and its prior `pass` is not
presented as current health. When absent, the wire form is
`{"report":null,"freshness":{"status":"unknown",
"reason":"not_found","deadline_seconds":N}}`; `age_seconds` is omitted.
When stale, the report remains present, freshness has `reason="stale"` and an
age, and consumers must present overall health as unknown rather than rewriting
the stored report.

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
- Outbound destination-policy rejection is a sanitized `unknown` check error;
  audits never retry through a less restrictive client.
- A metrics parse error reports line count and sanitized position, not the raw
  line.
- Scheduled report/alert-file write failures are logged and emitted as audit
  sink errors; they do not mutate the database or change sync results.
- Deep temporary restore files are created under the configured temp directory
  using generated names, private modes, and no-symlink/root confinement, then
  removed on success, failure, timeout, or cancellation. Cleanup failure is a
  warning with the path shown only in local CLI identifier-enabled output.
- Filesystem findings report logical classifications such as `outside_root`,
  `missing`, `symlink_rejected`, or `unreadable`; shared reports never expose
  the path or file contents.

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
- Active integrity tests prove the quick/FK connection is query-only, while
  corrupt, foreign-valid, legacy-compatible, current-compatible, future, and
  migration-name-mismatch fixtures exercise identity separately.
- DB-derived media/note path fixtures cover traversal, absolute paths, escaping
  symlinks, contained files, and logical redacted errors through the root
  capability.

### Audit Package Tests

- Table-driven status and overall-precedence tests.
- Stable JSON fixtures for pass, warn, fail, unknown, and skipped reports.
- Poll-versus-arrival fixtures covering active quiet sources.
- Metrics fixtures covering restart gaps, lock skips, tolerated stage errors,
  incomplete history, malformed lines, and missing stages.
- Threshold boundary tests with an injected clock.
- Exact boundary fixtures cover 24/72 hours, 36/72 hours, two/four intervals,
  one-through-four-run p95 fallback, metrics sufficiency, explicit gap reasons,
  and migration-backed provenance cutover.
- Privacy tests that reject content-bearing keys and raw identifiers.
- Remote archive fixtures covering missing keys, size mismatches, stale
  backups, absent credentials, and recovery.
- Deep restore tests against a temporary object store and temporary database;
  compressed/decompressed/free-space ceilings and cancellation are enforced,
  temp files are cleaned, archive authenticity remains labeled unverified, and
  the active DB path stays byte-for-byte unchanged.
- Upstream comparison tests prove local-only rows do not become deletion
  candidates.
- Safe-network tests cover public/private/loopback/link-local/IPv6, mixed DNS,
  redirects, userinfo, proxy non-use, exact configured private origins, and
  separate trusted/untrusted clients.

### CLI, MCP, Scheduler, And Web Tests

- CLI output and exit-code tests for every overall status.
- JSON must be emitted before non-zero health exits.
- MCP schema matches runtime structured content and never exposes identifiers.
- Scheduler writes one private root-confined report, enforces retention,
  suppresses duplicate alerts, repeats after the configured interval, and
  covers every row of the transition table including initial failure,
  escalation, de-escalation, explicit disablement, optional findings, profile
  isolation, immediate exceptions, and recovery.
- Audit failure does not change sync run status.
- Webhook tests prove URL validation, no redirects/proxy, bounded bodies,
  private-origin opt-in, token redaction, and fixed content-free templates.
- Admin API tests prove fail-closed auth, Origin enforcement, no service-auth
  expansion, method/body/history bounds, exact-profile caching, singleflight,
  cross-profile conflict, 202/409/429 wire bodies, async status polling,
  persistence-before-completion, absent/stale freshness shapes, rate limiting,
  terminal-run TTL/count eviction, and that GET never starts work.
- MCP tests prove its per-call timeout, singleflight, cached-standard behavior,
  output ceiling, fixed enums, lack of arbitrary path/URL inputs, stdio
  availability, bearer-authenticated HTTP availability, and omission/rejection
  on auth-disabled HTTP.
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
   selector-equivalence tests. Add the query-only active integrity/identity API
   and root-confined aggregate OKF inspector.
2. **Provenance/diagnostics PR:** preserve transcript provenance, define error
   retry/blocked policy, surface scheduler auth failures, and harden OKF manifest
   validation. The fixing migration's `applied_at` becomes the durable
   provenance cutover.
3. **Core report, complete standard, and CLI PR:** add store read snapshots,
   the typed check registry, runtime/security-baseline reporting,
   `internal/audit`, fast and every standard registry check, split
   `ArchiveLister`/`ArchiveReader` capabilities, bounded sampled reconciliation,
   backup-age and summary-only OKF inspection, CLI rendering, JSON, privacy
   policy, and exit codes. Default `standard` cannot ship with a required
   registry check silently absent; any runtime-unavailable required check emits
   `unknown`.
4. **Deep verification PR:** add complete bounded media reconciliation and temporary
   SQLite download/decompress/integrity verification, without any active restore
   call. Own archive-identity, private temp-file, cleanup, and resource-ceiling
   tests here.
5. **Backup scheduler PR:** separately add the write-capable daily SQLite
   archive scheduler and its metrics/serialization tests.
6. **Audit scheduler/alert PR:** add fast post-run and periodic standard audits,
   private root-confined JSONL/state retention, audit metrics, the full
   per-profile/check transition table, the no-redirect `safehttp` extension,
   and constrained webhook delivery.
7. **MCP PR:** add the bounded/cached `dbrain_audit` after the JSON schema and
   check registry are stable. Own deadline, output-cap, singleflight, and input
   authority tests here.
8. **Admin API PR:** add fail-closed authenticated latest/history/run APIs,
   exact-profile caching, Origin-guarded bounded POST, singleflight/rate limits,
   no service-auth expansion, and stale-report envelopes.
9. **Admin UI PR:** add the responsive audit panel while preserving existing
    source-operations views.
10. **Upstream reconciliation PRs:** implement source-specific commands one
    importer at a time, starting with GitHub and YouTube before X and local app
    databases. Each reuses importer destination/pagination/response policies and
    adds no generic endpoint override.
11. **Skill wrapper PR:** add/update the Codex/Claude production-audit skill only
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
- the report proves the running binary's build/platform/security-baseline
  identity rather than inferring it from the checkout;
- every configured importer reports successful polling separately from
  arrivals;
- worker pending counts, audit pending counts, and admin pending counts agree;
- terminal outcomes are not labeled as failures;
- newly completed OCR/transcript/summary rows retain required provenance;
- standard audit detects stale or missing remote SQLite archives;
- deep audit validates a downloaded backup without touching the active DB;
- integrity, dbrain schema identity, migration compatibility, and unverified
  archive authenticity remain distinct claims;
- remote media reconciliation detects missing objects and size mismatches;
- scheduled audits preserve sync status, suppress alert noise, and report
  recovery;
- MCP and admin render the same check IDs/statuses as CLI JSON;
- audit artifacts and alerts contain no corpus content or secrets; and
- filesystem, remote object, webhook, MCP, and admin checks satisfy the v0.6.0
  confinement, destination, authentication, Origin, and resource-limit
  contracts; and
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
- `docs/web-route-capabilities.md` and the security remediation/review follow-up
  when audit API, webhook, filesystem, archive, or MCP capabilities land;
- MCP tool documentation and installed skill copies when the tool/skill lands;
  and
- `CHANGELOG.md` for each shipped CLI, scheduler, durability, MCP, or admin
  behavior change.
