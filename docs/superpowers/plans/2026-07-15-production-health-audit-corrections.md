# Production Health Audit Corrections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct the v0.7.0 production-audit false failures, expose sanitized remote-media failure causes, add an explicit pruned-media repair command, and make disabled SQLite backups actionable without changing `sync all`.

**Architecture:** Keep classification policy in `internal/audit` and worker-aligned data predicates in `internal/store`. Carry sanitized capability error codes through audit dependencies rather than raw errors. Promote the existing pruned-media development workflow into a bounded `internal/prunedmediarepair` coordinator and `dbrain repair pruned-media` command that is read-only unless `--apply` is explicit.

**Tech Stack:** Go 1.26, Cobra, modernc SQLite, existing dbrain metrics/store/media-download packages, Task.

## Global Constraints

- Target branch is `audit-fixes` in `/Users/darron/src/dbrain`, based on release `v0.7.0` commit `9b8c37c`.
- `sync all`, scheduled sync selection, and normal import/enrichment runtime are unchanged.
- Audits remain read-only and never invoke repair.
- `dbrain repair pruned-media` is dry-run by default; only `--apply` permits network, database, or media writes.
- Raw transcript/OCR text is preserved. Terminal transcription status does not require empty text.
- Shared reports contain only existing stable error codes; no raw errors, credentials, endpoints, buckets, object keys, or provider bodies.
- Production configuration is not edited or silently enabled.
- Every production-code change follows a witnessed RED/GREEN test cycle.
- User-visible behavior updates `CHANGELOG.md` and relevant command/operations documentation.
- Final verification is `task fmt`, `task lint`, `task test-ci`, and `task build`.

---

### Task 1: Ignore Boundary-Incomplete Runs in Scheduler Continuity

**Files:**
- Modify: `internal/audit/scheduler.go`
- Modify: `internal/audit/runner_test.go`

**Interfaces:**
- Consumes: `metrics.Window.Runs []metrics.RunRecord`
- Produces: continuity gaps calculated only between records with non-zero `StartedAt`

- [ ] **Step 1: Write the failing regression test**

Add `TestSchedulerContinuityIgnoresBoundaryIncompleteRun` to `internal/audit/runner_test.go`. Construct a standard-profile scheduler report with:

```go
window.Runs = []metrics.RunRecord{
    {ID: "boundary-only", CompletedAt: now.Add(-7 * 24 * time.Hour), RecordComplete: false},
    {ID: "first", StartedAt: now.Add(-4 * time.Hour), CompletedAt: now.Add(-3*time.Hour - 55*time.Minute), RecordComplete: true},
    {ID: "second", StartedAt: now.Add(-2*time.Hour - time.Minute), CompletedAt: now.Add(-115 * time.Minute), RecordComplete: true},
}
```

Use one-hour scheduler interval, zero jitter, and duration samples that make the warning threshold less than 7,143 seconds and the failure threshold greater than 7,143 seconds. Assert `scheduler.continuity=warn`, `gap_count=1`, `unexplained_gap_count=1`, and `largest_gap_seconds=7140` (or use exact timestamps producing 7,143 seconds and assert that exact value). Assert it is never `math.MaxInt64/time.Second`.

- [ ] **Step 2: Verify RED**

Run:

```sh
go test ./internal/audit -run '^TestSchedulerContinuityIgnoresBoundaryIncompleteRun$' -count=1
```

Expected: FAIL because the zero `StartedAt` record creates a saturated gap and a failure.

- [ ] **Step 3: Implement the minimal filter**

In `executeScheduler`, build the continuity slice without incomplete start times:

```go
runs := make([]metrics.RunRecord, 0, len(s.metrics.Runs))
for _, run := range s.metrics.Runs {
    if run.StartedAt.IsZero() {
        continue
    }
    runs = append(runs, run)
}
sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
```

Do not change metrics-window sufficiency, attempt counts, or latest-run selection.

- [ ] **Step 4: Verify GREEN**

Run the focused test and existing scheduler tests:

```sh
go test ./internal/audit -run 'TestScheduler|TestMetrics' -count=1
```

- [ ] **Step 5: Commit**

```sh
git add internal/audit/scheduler.go internal/audit/runner_test.go
git commit -m "fix: ignore incomplete audit continuity boundaries"
```

---

### Task 2: Preserve Text in Terminal Transcription Partitions

**Files:**
- Modify: `internal/store/stats_pipeline_x_items.go`
- Modify: `internal/store/stats_test.go`
- Modify: `docs/superpowers/specs/2026-07-13-production-health-audit-design.md`

**Interfaces:**
- Consumes: authoritative `item_enrichments` status/text with compatibility fallback
- Produces: mutually exclusive transcription partition where terminal enum status dominates preserved text

- [ ] **Step 1: Write the failing store regression**

Extend the transcription partition fixture in `internal/store/stats_test.go` with an item whose effective status is `model.XMediaTranscriptStatusTooShort` and whose transcript text is non-empty. Assert:

```go
if row.Terminal != wantTerminalWithPreservedText || row.Unknown != 0 || !row.PartitionValid {
    t.Fatalf("terminal transcript with preserved text misclassified: %+v", row)
}
```

Keep a separate invalid-status fixture and assert it remains `Unknown`.

- [ ] **Step 2: Verify RED**

```sh
go test ./internal/store -run 'TestPipeline.*Transcription' -count=1
```

Expected: FAIL because the terminal query currently requires `text = ''`.

- [ ] **Step 3: Make terminal status authoritative**

Change the terminal query in `pipelineXMediaTranscriptionRow` from:

```go
candidateWhere + ` AND ` + text + ` = '' AND ` + status + ` IN (...)`
```

to:

```go
candidateWhere + ` AND ` + status + ` IN (...)`
```

Keep `current` restricted to `status='ok' AND text!=''`, and keep invalid enum values in `unknown`.

- [ ] **Step 4: Clarify the normative design and verify GREEN**

Update the pipeline partition text in the production audit design to state that raw text may exist for terminal transcription outcomes. Then run:

```sh
go test ./internal/store -run 'TestPipeline.*Transcription|TestWorkerPendingMatchesPipeline' -count=1
```

- [ ] **Step 5: Commit**

```sh
git add internal/store/stats_pipeline_x_items.go internal/store/stats_test.go docs/superpowers/specs/2026-07-13-production-health-audit-design.md
git commit -m "fix: classify preserved terminal transcripts"
```

---

### Task 3: Report Sanitized Remote-Media Failure Causes

**Files:**
- Modify: `internal/audit/capabilities.go`
- Modify: `internal/audit/runner.go`
- Modify: `internal/audit/durability.go`
- Modify: `internal/audit/runner_test.go`
- Modify: `internal/app/audit.go`
- Modify: `internal/app/audit_test.go`

**Interfaces:**
- Add: `Dependencies.MediaErrorCode ErrorCode`
- Add if deep initialization needs it: `DeepDependencies.MediaErrorCode ErrorCode`
- Add: an unexported `remoteMetadataErrorCode(error) ErrorCode` classifier in `internal/audit/durability.go`

- [ ] **Step 1: Write failing core error-code tests**

Add table tests that run only `durability.media_remote` with an archived-media record and inspectors returning:

```go
context.DeadlineExceeded // want ErrorTimeout
context.Canceled         // want ErrorCanceled
errors.New("provider secret response") // want ErrorRead
```

Assert the check is `unknown`, contains the expected `error_code`, and marshaled JSON does not contain `provider secret response`.

Add a missing-inspector test with `Dependencies.MediaErrorCode=ErrorCredentialResolution` and assert that exact code survives instead of becoming `unavailable`.

- [ ] **Step 2: Verify core RED**

```sh
go test ./internal/audit -run 'Test.*MediaRemote.*Error' -count=1
```

Expected: FAIL because all missing/request failures currently collapse to `unavailable` or omit an error code.

- [ ] **Step 3: Implement core propagation**

Initialize absent codes to `ErrorUnavailable`. When the media capability is absent, use `Dependencies.MediaErrorCode`. When a HEAD request fails, select a deterministic code with priority `timeout`, `canceled`, then `read_error`; return the existing bounded evidence with `StatusUnknown`, `ConfidenceUnknown`, and that code. Never attach the raw error.

Use a helper shaped as:

```go
func remoteMetadataErrorCode(err error) ErrorCode {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return ErrorTimeout
    case errors.Is(err, context.Canceled):
        return ErrorCanceled
    case err != nil:
        return ErrorRead
    default:
        return ErrorUnavailable
    }
}
```

- [ ] **Step 4: Write failing adapter initialization tests**

In `internal/app/audit_test.go`, use an invalid secret reference for an explicitly selected durability audit and assert `credential_resolution_error`. Add a test seam around `mediaarchive.NewS3Inspector` or a small constructor variable, return a configuration error, and assert `configuration_error`. Assert neither raw error appears in JSON.

- [ ] **Step 5: Verify adapter RED, implement, and verify GREEN**

In `buildAuditDependencies`:

```go
deps.MediaErrorCode = audit.ErrorUnavailable
if resolveErr != nil {
    deps.MediaErrorCode = audit.ErrorCredentialResolution
} else if inspectErr != nil {
    deps.MediaErrorCode = audit.ErrorConfiguration
} else {
    deps.Media = auditMediaInspector{inspector: inspector}
    deps.MediaErrorCode = ""
}
```

Apply the equivalent safe code to deep media initialization. Run:

```sh
go test ./internal/audit ./internal/app -run 'Test.*MediaRemote.*Error|TestBuild.*Audit.*Media' -count=1
```

- [ ] **Step 6: Commit**

```sh
git add internal/audit/capabilities.go internal/audit/runner.go internal/audit/durability.go internal/audit/runner_test.go internal/app/audit.go internal/app/audit_test.go
git commit -m "fix: expose sanitized media audit failures"
```

---

### Task 4: Promote Pruned-Media Repair into the Supported CLI

**Files:**
- Create: `internal/store/pruned_media_repair.go`
- Create: `internal/store/pruned_media_repair_test.go`
- Create: `internal/prunedmediarepair/run.go`
- Create: `internal/prunedmediarepair/run_test.go`
- Create: `internal/app/repair_pruned_media.go`
- Modify: `internal/app/root.go`
- Modify: `internal/app/app_test.go`
- Delete: `cmd/devtools/restore_pruned_pending_x_media/main.go`
- Delete: `cmd/devtools/restore_pruned_pending_x_media/queries.go`
- Delete: `cmd/devtools/restore_pruned_pending_x_media/main_test.go`

**Interfaces:**

```go
// internal/store/pruned_media_repair.go
type PrunedMediaRepairCandidates struct {
    OCRItemIDs        []int64
    TranscriptItemIDs []int64
}
func (s *Store) ListPrunedMediaRepairCandidates(ctx context.Context, includeOCR, includeTranscripts bool, limit int) (PrunedMediaRepairCandidates, error)

// internal/prunedmediarepair/run.go
type Options struct {
    Apply       bool
    OCR         bool
    Transcripts bool
    Limit       int
    Timeout     time.Duration
    Logger      *slog.Logger
}
type Stats struct {
    Apply                bool `json:"apply"`
    OCRCandidates        int  `json:"ocr_candidates"`
    TranscriptCandidates int  `json:"transcript_candidates"`
    ItemsVisited         int  `json:"items_visited"`
    ItemsRestored        int  `json:"items_restored"`
    MediaCandidates      int  `json:"media_candidates"`
    MediaRequested       int  `json:"media_requested"`
    MediaDownloaded      int  `json:"media_downloaded"`
    MediaGone            int  `json:"media_gone"`
    MediaErrors          int  `json:"media_errors"`
    MediaBlocked         int  `json:"media_blocked"`
    MediaChanged         int  `json:"media_changed"`
}
func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error)
```

- [ ] **Step 1: Write failing selector tests**

Move the existing devtool fixtures into `internal/store/pruned_media_repair_test.go`. Add authoritative `item_enrichments` cases and shared archived/pruned assets. Assert selection includes only items that:

- have downloaded media marked pruned and archived;
- have no runnable unpruned local media of the required type;
- are pending/error for OCR or blank/due-error for transcription under shared policy;
- are not current, terminal, or a young transcription error.

Assert category limits are applied independently and returned slices are deterministic non-nil arrays.

- [ ] **Step 2: Verify selector RED and implement GREEN**

```sh
go test ./internal/store -run '^TestListPrunedMediaRepairCandidates' -count=1
```

Implement fixed SQL using `itemOCRStatusExpr`, `itemXMediaTranscriptStatusExpr`, the shared retry cooldown, and archive/prune predicates. Do not accept arbitrary SQL or paths.

- [ ] **Step 3: Write coordinator RED tests**

Add an internal downloader seam:

```go
type downloadItemFunc func(context.Context, config.Config, *store.Store, int64, mediadownload.Options) (mediadownload.Stats, error)
```

Test that dry-run returns candidate counts and calls the fake downloader zero times. Test `Apply=true` deduplicates item IDs across OCR/transcript categories, passes `Force:true` and the configured timeout, and aggregates all counters.

- [ ] **Step 4: Verify coordinator RED and implement GREEN**

```sh
go test ./internal/prunedmediarepair -count=1
```

The exported `Run` calls an unexported `runWithDownloader` using `mediadownload.RunForItem`. Return an error without continuing when a per-item invocation returns an operational error; retain per-item media errors reported in stats.

- [ ] **Step 5: Write CLI RED tests**

Add command tests proving:

- `repair pruned-media --json` opens through no-write config plus `store.OpenReadOnly`, emits `apply:false`, creates no directories/migrations, and performs no network work;
- `--apply` uses writable config/store and invokes the coordinator;
- neither `--ocr` nor `--transcripts` means both; specifying one means only that category;
- `--limit` must be positive and is capped at 5,000; default is 5,000;
- `--timeout` must be positive; default is 45 seconds.

- [ ] **Step 6: Implement the command and verify GREEN**

Register `newRepairPrunedMediaCommand(opts)` under `repair`. Dry-run uses `loadAuditConfig` plus `store.OpenReadOnly`; apply uses `loadConfig` plus `store.Open`. Human output includes mode and all `Stats` counters. JSON uses `writeJSON`.

Run:

```sh
go test ./internal/store ./internal/prunedmediarepair ./internal/app -run 'PrunedMedia|RepairPruned' -count=1
```

- [ ] **Step 7: Remove the duplicate devtool and commit**

```sh
git add internal/store/pruned_media_repair.go internal/store/pruned_media_repair_test.go internal/prunedmediarepair internal/app/repair_pruned_media.go internal/app/root.go internal/app/app_test.go cmd/devtools/restore_pruned_pending_x_media
git commit -m "feat: add explicit pruned media repair"
```

---

### Task 5: Make Backup Configuration Actionable and Finish Documentation

**Files:**
- Modify: `internal/audit/runner.go`
- Modify: `internal/audit/runner_test.go`
- Modify: `internal/app/audit_output.go`
- Modify: `internal/app/audit_test.go`
- Modify: `COMMANDS.md`
- Modify: `docs/maintenance-operations.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- `fixedRemediation(CheckDurabilitySQLiteBackupConfiguration)` returns a fixed, privacy-safe operator instruction.
- Human audit output prints remediation only for non-pass checks where the fixed registry template is non-empty.

- [ ] **Step 1: Write failing remediation tests**

Assert the configured-disabled backup check carries this exact fixed remediation:

```text
Enable scheduler.sqlite_archive.enabled or set audit.require.sqlite_backup when remote SQLite backups are required.
```

Assert `ValidateReport` accepts it, rejects arbitrary replacement text, and JSON contains no target-specific values. Add a human-output test asserting the remediation appears directly below the backup warning and is absent for passing checks.

- [ ] **Step 2: Verify RED and implement GREEN**

```sh
go test ./internal/audit ./internal/app -run 'Test.*SQLiteBackup.*Remediation|Test.*AuditHuman.*Remediation' -count=1
```

Implement the fixed mapping and bounded human rendering. Do not add free-form errors or paths.

- [ ] **Step 3: Update operator documentation and changelog**

Document `dbrain repair pruned-media`, dry-run/apply behavior, limits, and the fact that workers perform enrichment afterward. Update maintenance operations to classify it as an explicit network/database/media write only with `--apply`. Add a dated changelog entry covering the two false-failure corrections, sanitized remote diagnostics, repair command, and backup remediation.

- [ ] **Step 4: Run focused regression packages**

```sh
go test ./internal/audit ./internal/store ./internal/prunedmediarepair ./internal/app -count=1
```

- [ ] **Step 5: Run standard repository gates**

```sh
task fmt
task lint
task test-ci
task build
```

- [ ] **Step 6: Smoke-check CLI help and dry-run against an isolated fixture**

```sh
./bin/dbrain --no-debug repair pruned-media --help
```

Create a temporary root in a test or existing fixture helper, run JSON dry-run, and confirm no target mutation. Do not run repair against production in this implementation task.

- [ ] **Step 7: Commit**

```sh
git add internal/audit/runner.go internal/audit/runner_test.go internal/app/audit_output.go internal/app/audit_test.go COMMANDS.md docs/maintenance-operations.md CHANGELOG.md
git commit -m "docs: make production audit findings actionable"
```

---

## Final Review

- [ ] Compare the complete branch diff against `docs/superpowers/specs/2026-07-15-production-health-audit-corrections-design.md`.
- [ ] Confirm no changes to `sync all` stage selection or automatic repair behavior.
- [ ] Confirm all RED/GREEN evidence is recorded in task reports.
- [ ] Run a whole-branch reviewer over `git merge-base main HEAD..HEAD`.
- [ ] Resolve every Critical or Important review finding and rerun covering tests.
