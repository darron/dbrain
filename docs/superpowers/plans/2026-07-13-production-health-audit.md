# dbrain Production Health Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking. Every production change must also use superpowers:test-driven-development, and completion claims must use superpowers:verification-before-completion.

**Goal:** Add one read-only, deterministic production-health audit that proves configured importer polling, enrichment coverage and provenance, archive/backup/OKF durability, scheduler continuity, and runtime identity; expose the same stable report through CLI, scheduled reports, authenticated MCP, and authenticated admin UI.

**Architecture:** internal/audit owns the closed dbrain.audit.v1 registry, status policy, thresholds, privacy rules, and orchestration. It receives resolved least-authority capabilities from adapters. internal/store owns consistent read snapshots, worker-aligned predicates, identity/integrity inspection, and aggregate audit queries. CLI is canonical; MCP and web reuse completed reports and cannot widen authority. Deep verification is explicit CLI-only and validates temporary archive candidates without restoring the active database.

**Tech Stack:** Go 1.26, Cobra, modernc SQLite, AWS SDK v2/S3-compatible storage, JSONL metrics, net/http plus internal/safehttp, Svelte 5/Vite, Task, and repository-local agent skills.

## Global Constraints

- **Target boundary:** implement in /Users/darron/src/dbrain against the current main-derived feature branch. This plan does not authorize inspecting or mutating production XDG state, deploying a binary, running an import, restoring a backup, pruning media, or calling a real upstream/provider.
- **Normative contract:** docs/superpowers/specs/2026-07-13-production-health-audit-design.md owns the exact dbrain.audit.v1 registry, evidence fields, enums, thresholds, profiles, transition table, resource ceilings, and acceptance criteria. A slice must not silently omit a required registry entry.
- **Read-only audit:** all audit and stats store opens use store.OpenReadOnly. Audit receives no writer, restore, prune, generic path, generic URL, or arbitrary object-key capability.
- **No-write bootstrap:** audit configuration resolution must not call the ordinary loadConfig path because it creates directories, removes legacy files, and runs preflight. The audit adapter gets a dedicated resolver that preserves flag/environment/XDG precedence without EnsureDirs or unrelated secret resolution.
- **Privacy:** the shared report contains no corpus content, titles, URLs, raw identifiers, object keys, ETags, absolute paths, provider bodies, secrets, OCR text, or transcripts. Exact local identifiers exist only in the non-persisted dbrain.audit.local.v1 CLI wrapper after --include-identifiers.
- **Security baseline:** preserve v0.6.0 confinement, safe outbound destination, bearer/session authentication, Origin, private-file, schema-identity, and MCP resource-limit contracts. Unknown baseline ID/epoch pairs remain unknown.
- **Append-only history:** migration numbers and names are immutable. Before allocating audit_provenance_v1, inspect current, main, origin/main, and recent local/remote branches. Do not hard-code a version in advance.
- **TDD:** every behavioral step starts with the narrowest failing test, confirms the intended RED failure, implements the smallest production change, and reruns the focused test before broader gates.
- **Standard gate per code slice:** task fmt, task lint, task test-ci, and task build. Use task test-ci rather than ambient task test for the final gate.
- **Documentation:** update CHANGELOG.md only when user-visible behavior lands. Update README.md, COMMANDS.md, config.yaml.sample, internal/app/env_docs.go, MCP.md, and the named architecture/operations/security documents in the slice that changes their contract.
- **Generated UI:** when web/ui/src changes, run npm test and npm run build from web/ui and commit the refreshed web/ui/dist output.
- **Commits:** the commit names below define reviewable boundaries. Do not combine later adapters with an unfinished or incomplete core registry.

## Delivery And Interface Order

| Slice | Produces | Consumed by | Ship condition |
| --- | --- | --- | --- |
| 1. Read-only/stat foundation | shared pipeline classifiers, read snapshot, active integrity API, confined OKF summary | core audit | selector-equivalence and query-only tests pass |
| 2. Provenance/diagnostics | durable cutover, mirror preservation, retry policy, truthful tsnet/OKF diagnostics | core audit | post-cutover loss is testable |
| 3. Core standard audit + CLI | dbrain.audit.v1, every fast/standard registry check, CLI | all adapters | complete registry and stable fixtures pass |
| 4. Deep verification | bounded media inventory and temporary SQLite validation | release workflow | active DB unchanged and cleanup proven |
| 5. SQLite backup scheduler | write-only sibling scheduler | backup-age audit | audit package still has no writer |
| 6. Audit scheduler/alerts | private report store, retention, state machine, webhook | MCP/web cached reads | transition table passes |
| 7. MCP | bounded dbrain_audit | agents | stdio/authenticated-HTTP capability tests pass |
| 8. Admin API | authenticated exact-profile report/run APIs | UI | fail-closed auth and async state tests pass |
| 9. Admin UI | responsive production-health presentation | operators | browser/mobile tests pass |
| 10. Upstream parity | bounded importer-owned inventories and source commands | deep audit | all seven concrete parity IDs implemented |
| 11. Skill wrapper | repeatable release-audit operator workflow | Codex/Claude | skill validation and fresh-agent tests pass |

---

## Task 1: Build The Read-Only Store, Predicate, Integrity, And OKF Foundation

**Files:**

- Modify internal/app/stats_activity_command.go
- Modify internal/app/stats_count_commands.go
- Modify internal/app/stats_pipeline_commands.go
- Create internal/store/audit_snapshot.go
- Create internal/store/audit_snapshot_test.go
- Create internal/store/pipeline_policy.go
- Modify internal/store/stats_pipeline.go
- Modify internal/store/stats_pipeline_items.go
- Modify internal/store/stats_pipeline_x_items.go
- Modify internal/store/stats_pipeline_helpers.go
- Modify internal/store/source_enrichment_candidates.go
- Modify internal/store/media_archive_candidates.go
- Modify internal/store/predicates.go
- Modify internal/store/source_predicates.go
- Create internal/store/integrity.go
- Create internal/store/integrity_test.go
- Modify internal/store/schema_identity.go
- Modify internal/store/schema_identity_test.go
- Create internal/okf/inspect.go
- Create internal/okf/inspect_test.go
- Modify internal/okf/validate.go
- Modify internal/app/okf.go
- Modify internal/app/okf_test.go
- Create internal/vaultfs/inspect.go
- Create internal/vaultfs/inspect_test.go

**Produced interfaces:**

~~~go
// internal/store/audit_snapshot.go
type AuditReadSnapshot struct { /* unexported *sql.Tx */ }
func (s *Store) BeginAuditReadSnapshot(ctx context.Context) (*AuditReadSnapshot, error)
func (s *AuditReadSnapshot) Close() error
func (s *AuditReadSnapshot) PipelinePartitions(ctx context.Context) (AuditPipelinePartitions, error)
func (s *AuditReadSnapshot) ImportAggregates(ctx context.Context, since time.Time) (AuditImportAggregates, error)
func (s *AuditReadSnapshot) Provenance(ctx context.Context, cutover time.Time) (AuditProvenance, error)
func (s *AuditReadSnapshot) MediaDurability(ctx context.Context) (AuditMediaDurability, error)

type DatabaseIntegrity struct {
    QuickCheck string
    QuickViolationCount int
    ForeignKeyViolationCount int
    SchemaCompatibility string
    MigrationCompatibility string
    UserVersion int
    SupportedVersion int
    AppliedMigrationCount int
}
func InspectDatabaseReadOnly(ctx context.Context, path string, includeIntegrity bool) (DatabaseIntegrity, error)

// internal/okf/inspect.go
type InspectionSummary struct {
    ManifestValid bool
    ExportedAt time.Time
    DocumentCount int
    BrokenLinkCount int
    ValidationErrorCount int
    TraversalComplete bool
}
func InspectBundle(root *vaultfs.Root) (InspectionSummary, error)

// internal/vaultfs/inspect.go
type LogicalFileMetadata struct {
    Exists bool
    Regular bool
    SizeBytes int64
}
type LogicalFileError string // missing, outside_root, symlink_rejected, unreadable
func (r *Root) Inspect(name string) (LogicalFileMetadata, LogicalFileError)
~~~

- [ ] **RED — read-only commands:** add command tests that create a legacy store missing the newest migration, invoke every stats subcommand, and assert schema_migrations and PRAGMA user_version are byte-for-byte unchanged.

  ~~~sh
  go test ./internal/app -run 'TestStats.*ReadOnly' -count=1
  ~~~

  Expected RED: at least backlog/pipeline opens through store.Open and migrates the fixture.

- [ ] **GREEN — command opens:** route every stats command through store.OpenReadOnly. Keep writable store.Open only in commands that intentionally mutate dbrain state.

- [ ] **RED — exhaustive shared partitions:** add fixtures for hydration, extraction, summary, transcription, OCR, and media archival. Assert total = current + pending + blocked + terminal + failed + unknown; assert pairwise disjoint membership; and assert worker candidate count equals pending count.

  Include these regressions:

  - downloaded unpruned X photos with a confined local path are OCR pending;
  - pruned, missing-path, archived-only, or non-photo assets are not OCR pending;
  - no_audio, noise, textless too_short, and empty transcription states are terminal;
  - one genuine transcription error is failed or retryable according to the explicit policy, never an unexplained remainder;
  - every invalid legacy status is unknown, not failed.

  ~~~sh
  go test ./internal/store -run 'Test(PipelinePartitions|WorkerPendingMatchesPipeline|XPhotoOCR|XMediaTranscription)' -count=1
  ~~~

  Expected RED: OCR and transcription fixtures expose the known selector/classifier mismatches.

- [ ] **GREEN — shared policy:** centralize named worker eligibility expressions and Go classifiers in pipeline_policy.go. Make worker selectors, Backlog, Pipeline, audit snapshots, and later admin labels call those definitions. Extend PipelineStageRow/aggregate DTOs with Terminal and Unknown without removing existing JSON fields.

  The initial transcription error policy is one shared 24-hour retry cooldown: a younger error is blocked and a due error is pending. Do not add a fake retry-exhausted failure state without a durable retry counter. finalizePipelineStageRow validates the sum and sets partition_valid; it must never assign a remainder to failed.

- [ ] **RED — one consistent snapshot:** start a read transaction, mutate a second connection between two aggregate reads, and prove both reads retain the initial view. Add cancellation and close/rollback tests.

  ~~~sh
  go test ./internal/store -run '^TestAuditReadSnapshot' -count=1
  ~~~

- [ ] **GREEN — snapshot:** implement BeginAuditReadSnapshot with a query-only read transaction and focused aggregate methods. Do not expose *sql.Tx or generic Query to internal/audit.

- [ ] **RED — active integrity separation:** cover current-compatible, accepted legacy-compatible, corrupt, foreign-valid SQLite, future user_version, missing migration, and mismatched migration-name fixtures. Prove quick_check and foreign_key_check use mode=ro and create no migration metadata.

  ~~~sh
  go test ./internal/store -run 'Test(InspectDatabaseReadOnly|ValidateRestorableDatabase)' -count=1
  ~~~

- [ ] **GREEN — identity/integrity:** factor schema identity and migration compatibility into structured read-only results while preserving ValidateRestorableDatabase(ctx, path) error semantics for restore callers. Keep quick check, foreign keys, schema identity, migration compatibility, and archive authenticity as distinct claims.

- [ ] **RED — confined OKF aggregate:** add missing/unreadable/invalid manifest, missing exported_at, broken links, traversal, absolute path, escaping symlink, contained symlink/file, and valid bundle fixtures. Assert the DTO contains only aggregate values and never document paths/titles/source keys/raw errors.

  ~~~sh
  go test ./internal/okf -run 'TestInspectBundle|TestValidateBundle' -count=1
  ~~~

- [ ] **GREEN — OKF inspector:** make automated validation require a readable valid manifest and exported_at. Traverse through vaultfs.Root (or an equally strict os.Root wrapper), return sanitized logical errors, and keep the existing human CLI validator useful without feeding its private inventory into audit output.

- [ ] **RED/GREEN — DB-derived vault paths:** add a narrow metadata-only VaultInspector over vaultfs.Root for note/media logical paths returned by the read snapshot. Cover blank, absolute, traversal, escaping parent/leaf symlink, missing, unreadable, and contained regular-file fixtures. Assert results expose only Exists/Regular/SizeBytes or one stable logical error code; no absolute/logical path, target, or file content enters shared evidence. Use this capability for media-local coverage and any note/materialization existence check; never join a DB value with cfg.VaultDir through filepath.

  ~~~sh
  go test ./internal/vaultfs -run 'Test.*Inspect' -count=1
  ~~~

- [ ] **Slice verification:**

  ~~~sh
  go test ./internal/app ./internal/store ./internal/okf -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

**Commit:** fix: align read-only pipeline health predicates

---

## Task 2: Preserve Provenance And Make Diagnostics Truthful

**Files:**

- Modify internal/store/migrations.go
- Modify internal/store/migrations_test.go
- Modify internal/store/item_enrichment.go
- Modify internal/store/item_enrichment_fields.go
- Modify internal/store/item_enrichment_backfill.go
- Modify internal/store/item_enrichment_test.go
- Modify internal/store/x_media_transcription.go
- Modify internal/store/stats_pipeline_x_items.go
- Modify internal/app/tsnet_scheduler_status.go
- Modify internal/app/app_test.go
- Modify internal/okf/validate.go
- Modify internal/okf/inspect.go
- Modify CHANGELOG.md

- [ ] **Migration collision audit:** inspect migration history before editing:

  ~~~sh
  git log --all --oneline -- internal/store/migrations.go
  git show main:internal/store/migrations.go
  git show origin/main:internal/store/migrations.go
  git branch --all --format='%(refname:short)'
  rg -n 'Version:|currentSchemaVersion|Name:' internal/store/migrations.go
  ~~~

  Inspect any recent branch shown by git log that changed migrations.go. Allocate the next unused integer, name it exactly audit_provenance_v1, and raise currentSchemaVersion. Never reuse an integer found on another branch.

- [ ] **RED — migration repair:** simulate an existing database whose schema_migrations contains every prior version but whose enrichment state predates this fix. Reopen with store.Open and assert the new migration exists exactly once, required schema is repaired idempotently, and its applied_at parses as UTC RFC3339.

  ~~~sh
  go test ./internal/store -run 'TestMigration.*AuditProvenance' -count=1
  ~~~

- [ ] **RED — mirror provenance:** save a successful transcript enrichment with text, raw_json, model, tool, tool_version, input_hash, and completed_at. Perform the ordinary later item upsert/mirror path. Assert every field survives in item_enrichments and compatibility columns remain synchronized.

  ~~~sh
  go test ./internal/store -run 'Test.*Transcript.*Provenance.*Upsert' -count=1
  ~~~

  Expected RED: the mirror overwrites some provenance with empty values.

- [ ] **GREEN — non-destructive merge:** change compatibility mirror writes so absent legacy fields never overwrite populated authoritative item_enrichments fields. Persist complete transcript provenance at the initial write. Backfill only values recoverable from durable rows; never invent tool/model/raw JSON/input hashes.

  Load the full existing enrichment before merging compatibility fields. A semantic no-op must not issue UPDATE or advance item_enrichments.updated_at; otherwise a legacy row would be falsely reclassified as post-cutover. Future transcript writes must compute an honest input hash from sorted eligible media content hashes plus the resolved backend/model/language/VAD settings and must persist a real implementation-level tool version.

- [ ] **RED/GREEN — genuine errors:** encode one explicit retry/blocked policy for x_media_transcript_status=error. Add selection, retry exhaustion/blocked, partition, and stats tests so the row cannot be silently excluded forever.

- [ ] **RED/GREEN — provenance aggregates:** make AuditReadSnapshot.Provenance classify successful rows before the audit_provenance_v1 applied_at cutover as legacy and rows at/after it as post-cutover. Cover all four exact IDs and required fields from the design:

  - pipeline.item_summary.provenance
  - pipeline.item_ocr.provenance
  - pipeline.x_media_transcript.provenance
  - pipeline.source_summary.provenance

  Missing/contradictory migration metadata must return unknown evidence, not a guessed build-date cutover.

- [ ] **RED — scheduler auth diagnostic:** simulate tsnet scheduler status returning 401/403 and assert status exposes a sanitized scheduler authentication failure instead of silently omitting sync_all. Retain successful and scheduler-disabled controls.

  ~~~sh
  go test ./internal/app -run 'Test.*TSNet.*Scheduler.*Auth' -count=1
  ~~~

- [ ] **GREEN — diagnostic:** preserve the endpoint status and stable error code without logging credentials, response bodies, or URLs.

- [ ] **Retest OKF:** prove manifest read/parse errors are not discarded and the aggregate DTO remains content-free.

- [ ] **Slice verification and docs:**

  ~~~sh
  go test ./internal/store ./internal/app ./internal/okf -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

  Add a CHANGELOG entry for provenance preservation, truthful terminal/error stats, read-only stats, and OKF validation.

**Commit:** fix: preserve enrichment provenance for audits

---

## Task 3: Implement The Complete Standard Audit Engine And Canonical CLI

**Files:**

- Create internal/audit/types.go
- Create internal/audit/evidence.go
- Create internal/audit/capabilities.go
- Create internal/audit/registry.go
- Create internal/audit/status.go
- Create internal/audit/thresholds.go
- Create internal/audit/privacy.go
- Create internal/audit/runner.go
- Create internal/audit/boundary.go
- Create internal/audit/scheduler.go
- Create internal/audit/imports.go
- Create internal/audit/pipeline.go
- Create internal/audit/durability.go
- Create internal/audit/media_sample.go
- Create internal/audit/duration.go
- Create internal/audit/types_test.go
- Create internal/audit/registry_test.go
- Create internal/audit/status_test.go
- Create internal/audit/thresholds_test.go
- Create internal/audit/privacy_test.go
- Create internal/audit/runner_test.go
- Create internal/metrics/reader.go
- Create internal/metrics/reader_test.go
- Modify internal/sqlitearchive/types.go
- Create internal/sqlitearchive/inspect.go
- Create internal/sqlitearchive/inspect_test.go
- Modify internal/sqlitearchive/s3.go
- Create internal/mediaarchive/inspect.go
- Create internal/mediaarchive/inspect_test.go
- Modify internal/mediaarchive/s3.go
- Modify internal/version/version.go
- Modify internal/version/version_test.go
- Create internal/app/audit.go
- Create internal/app/audit_config.go
- Create internal/app/audit_output.go
- Create internal/app/audit_test.go
- Create internal/app/exit.go
- Create internal/app/exit_test.go
- Modify internal/app/root.go
- Modify internal/app/helpers.go
- Modify internal/app/env_docs.go
- Modify cmd/dbrain/main.go
- Modify internal/syncjob/metrics.go
- Modify internal/syncjob/run_test.go
- Modify internal/youtubeimport/types.go
- Modify internal/youtubeimport/run.go
- Modify internal/youtubeimport/run_test.go
- Modify internal/safehttp/safehttp.go
- Modify internal/safehttp/safehttp_test.go
- Modify config.yaml.sample
- Modify README.md
- Modify COMMANDS.md
- Modify docs/architecture.md
- Modify docs/schema-migrations.md
- Modify CHANGELOG.md

**Core API:**

~~~go
const SchemaV1 = "dbrain.audit.v1"

type Request struct {
    Profile Profile
    Since time.Duration
    Categories []Category
    Sources []Source
    CheckIDs []CheckID
    ExpectCommit string
}

type Dependencies struct {
    Store StoreSnapshot
    Database DatabaseInspector
    Vault VaultInspector
    OKF OKFInspector
    Metrics MetricsReader
    Archives ArchiveLister
    Media MediaArchiveInspector
    Runtime RuntimeVersion
    Clock func() time.Time
}

func Run(ctx context.Context, req Request, deps Dependencies) (Report, error)
func ExitCode(report Report) int
func ValidateReport(report Report) error
~~~

Archive capabilities are split:

~~~go
type ObjectLister interface {
    ListObjects(ctx context.Context, prefix string, budget ListBudget) (ListResult, error)
}
type ObjectReader interface {
    GetObject(ctx context.Context, key string) (io.ReadCloser, ObjectMetadata, error)
}
type ObjectWriter interface {
    PutObject(ctx context.Context, key string, body io.Reader, contentType string, length int64) (string, error)
}
~~~

Standard audit receives lister/head-style metadata only. Existing archive/restore commands may use an adapter implementing all three, but internal/audit cannot name or accept ObjectWriter.

- [ ] **RED — closed public schema:** add golden JSON fixtures for pass, warn, fail, unknown, and skipped. Assert non-nil arrays, deterministic check order, exact status/confidence precedence, zero-required-scope unknown/exit 3, exact scope semantics, and rejection of unknown IDs/categories/enums/evidence keys.

  ~~~sh
  go test ./internal/audit -run 'Test(ReportJSON|Registry|Overall|ExitCode)' -count=1
  ~~~

- [ ] **GREEN — types/registry:** transcribe every exact check ID, source ID, pipeline ID, profile, required condition, timeout class, evidence field, and enum from the approved design into typed constants and registry entries. Add a registry completeness test that compares the emitted unfiltered set to one literal sorted fixture.

  The closed v1 registry has 55 entries. An unfiltered standard run emits all 55: 46 standard-applicable entries (four boundary, four integrity, four scheduler/metrics, fourteen importer poll/arrival, ten pipeline partition/pending-age, four provenance, and six standard durability checks) plus seven deep parity and two deep-only durability entries as skipped(profile_excluded). Registry order is output order. Missing executors for applicable entries become explicit unknown checks; they are never omitted.

- [ ] **RED/GREEN — privacy allowlist:** recursively validate evidence. Reject arbitrary strings and content-bearing keys. Permit only declared integers, booleans, byte/second counts, UTC timestamps, stable enums/error codes, and bounded declared aggregate arrays. Fuzz strings containing absolute paths, URLs, source keys, tokens, OCR text, transcripts, titles, and provider errors.

- [ ] **RED/GREEN — thresholds:** table-test exact 24/72-hour pending boundaries, 36/72-hour backup boundaries, two/four OKF intervals, scheduler W/F formulas, nearest-rank p95 for at least five durations, max-observed for one through four, zero fallback, and metrics-window sufficiency.

- [ ] **RED/GREEN — JSONL metrics reader:** parse only the resolved metrics file; reverse-read bounded blocks until the requested start with a 1 MiB maximum line plus explicit byte/event budgets; report malformed-line counts/positions without raw lines; distinguish attempted/successful polls from arrivals; reconstruct completed runs, selected/completed stages, lock skips, process transitions, gaps, tolerated stage errors, and duration samples.

  Add content-free sync.import.completed events for all seven concrete source families and explicit scheduler.sync.enabled, scheduler.sync.stopped, scheduler.sync.lock_skipped, and scheduler.sync.overlap_skipped markers. Extend YouTube import stats so liked and watch-later results are emitted separately rather than inferred from a combined stage count. Only explicit markers may explain continuity gaps.

  ~~~sh
  go test ./internal/metrics -run '^TestReader' -count=1
  ~~~

- [ ] **RED/GREEN — runtime baseline:** extend version.Details with SecurityBaselineID and SecurityBaselineEpoch compiled into the running binary. Register only (0, pre-v0.6.0) and (1, v0.6.0-security-pass at b733c78). Add pair-mismatch and unknown-pair tests; never compare labels lexically.

- [ ] **RED/GREEN — importer resolution:** reuse resolveSyncAllFlags/resolvedSyncImportPolicy through a small app-owned resolver adapter so fast/standard requiredness follows legacy defaults, YAML/runtime values, environment overrides, CLI rules, and scheduler restrictions. Do not let internal/audit parse YAML keys.

- [ ] **RED/GREEN — complete fast/standard checks:** implement all fast and standard registry entries from the design:

  - boundary.config, boundary.runtime, boundary.security_baseline, boundary.database;
  - integrity.schema_identity, integrity.migration_compatibility, standard quick_check and foreign_keys;
  - scheduler.latest_sync, scheduler.stage_coverage, scheduler.continuity, metrics.window;
  - seven concrete importer poll and seven optional arrival checks;
  - ten pipeline partition/pending-age checks and four provenance checks;
  - media_local_coverage, standard sampled media_remote, sqlite_backup_configuration, sqlite_backup_age, okf_freshness, and okf_validation.

  Every runtime-unavailable required check emits unknown. Disabled features emit explicit skipped(feature_disabled). Profile-excluded entries remain present for unfiltered audit all.

- [ ] **RED/GREEN — deterministic media sample:** newest 500 recent records by archived_at with SHA-256(key) tie break; 100 older records by SHA-256 of the exact weekly seed from the design. Record only counts/mode. Confidence is moderate whenever either population is sampled and high only when both are complete.

- [ ] **RED/GREEN — backup list semantics:** distinguish not configured, configured disabled, required ready, missing provider, missing credential, and resolver error. No required snapshot is fail; runtime resolver/list failure is unknown. Add list/head helpers that cannot enter restore.

- [ ] **RED/GREEN — safe S3 readers:** refactor media/SQLite S3 client construction to validate the configured endpoint as one exact canonical origin and use the safehttp dial/DNS/IP/no-proxy/redirect policy. Add read-only metadata/list adapters. Reject userinfo, query, fragment, unsafe endpoint paths, and audit-time endpoint overrides. Do not call buildSQLiteArchiveStore from audit because it accepts command overrides and returns combined read/write authority.

- [ ] **RED/GREEN — no-write config resolver:** add loadAuditConfig(root, configFile) preserving --config-file, --root, DBRAIN_CONFIG_FILE, DBRAIN_ROOT, and XDG precedence. It may register the selected config but must not call EnsureDirs, cleanup legacy files, startup preflight, or resolve unrelated secrets. Convert the existing resolved sync policy into typed Features; never parse importer enablement directly from YAML.

- [ ] **RED — CLI bootstrap and exits:** test config-resolution failure and database-open failure produce no report and exit 3. For produced reports, JSON must be written before exit 1/2/3. Cover:

  ~~~text
  dbrain audit all --profile fast|standard
  dbrain audit imports
  dbrain audit pipeline
  dbrain audit durability
  ~~~

  The registry contains deep entries so fast/standard output can emit them as skipped(profile_excluded), but Task 3 rejects --profile deep with a bootstrap/configuration exit 3 until Task 4 provides the required deep executors. Do not ship a nominal deep profile whose required checks are silently absent.

- [ ] **GREEN — CLI:** implement standard as default, --since 7d, --json, --timeout, --include-identifiers, and --expect-commit. Human output begins with non-persisted target boundary. Shared JSON remains byte-identical across adapters unless --include-identifiers selects the dbrain.audit.local.v1 wrapper.

  Add exact local-wrapper fixtures: every local_details.checks entry has check_id, at most 100 row_ids, at most 100 source_keys, at most 20 cleanup_paths, and truncated; empty slices serialize as []. Overflow sets truncated and deterministically keeps the first bounded values. Tests must prove MCP, web, scheduled reports, metrics, and alerts cannot request, persist, or receive the wrapper, and that internal/audit never accepts it as input.

  Add audit.ParseDuration so the documented 7d value is accepted alongside time.ParseDuration forms. Add a typed app.ExitError carrying Code, Err, and Silent. cmd/dbrain/main.go keeps existing cancellation semantics for unrelated commands, but an interrupted audit emits all possible completed checks plus unknown for interrupted/unstarted required checks and returns exit 3. It suppresses stderr for silent health exits and prints bootstrap/config errors at exit 3. The report must be fully encoded before the command returns a non-zero health error.

- [ ] **Standard completeness gate:** add a test that fails if any standard required registry entry lacks a runner or emits no check. Run:

  ~~~sh
  go test ./internal/audit ./internal/metrics ./internal/sqlitearchive ./internal/mediaarchive ./internal/version ./internal/app -count=1
  direnv exec . ./bin/dbrain --no-debug config paths --json
  direnv exec . ./bin/dbrain --no-debug audit all --profile standard --json
  ~~~

  The smoke test uses the repo/dev boundary only. Inspect JSON for schema validity and content exclusion; do not infer production health from it.

- [ ] **Slice verification:**

  ~~~sh
  task fmt
  task lint
  task test-ci
  task build
  git diff --check
  ~~~

**Commit:** feat: add production health audit cli

---

## Task 4: Add Explicit, Bounded Deep Verification

**Files:**

- Create internal/audit/deep.go
- Create internal/audit/deep_test.go
- Modify internal/audit/durability.go
- Modify internal/audit/registry_test.go
- Modify internal/sqlitearchive/inspect.go
- Create internal/sqlitearchive/stream.go
- Create internal/sqlitearchive/stream_test.go
- Modify internal/sqlitearchive/s3.go
- Modify internal/mediaarchive/inspect.go
- Modify internal/mediaarchive/inspect_test.go
- Create internal/vaultfs/temp.go
- Create internal/vaultfs/temp_test.go
- Modify internal/app/audit.go
- Modify internal/app/audit_test.go
- Modify README.md
- Modify COMMANDS.md
- Modify docs/maintenance-operations.md
- Modify docs/web-route-capabilities.md
- Modify CHANGELOG.md

- [ ] **RED — authority:** assert deep-only limits are rejected for fast/standard; deep cannot be requested through shared service adapters; Dependencies for ordinary Run cannot expose a writer/restore method.

- [ ] **RED — resource ceilings:** fake streams exceed compressed 20 GiB, decompressed 100 GiB, temp 120 GiB, insufficient free space, 1,000,000 objects, 10,000 pages, concurrency eight, metadata 30 seconds, read-idle 60 seconds, and whole run two hours. Each incomplete/budget result is unknown and cleans generated temp state.

- [ ] **GREEN — private temp capability:** create a generated 0700 directory below cfg.TempDir through a no-symlink/root-confined capability; create 0600 files; enforce byte ceilings during streaming, not only from metadata; remove on success, error, timeout, and cancellation.

- [ ] **RED — archive verification:** download and decompress a valid current dbrain DB, accepted legacy DB, corrupt gzip, foreign-valid SQLite, future schema, migration mismatch, quick-check violation, and FK violation. Assert:

  - store.ValidateRestorableDatabase is always called for completed candidates;
  - quick/FK checks remain query-only;
  - archive_authenticity is unverified;
  - active DB/WAL/SHM hashes never change;
  - sqlitearchive.Restore is never invoked.

  ~~~sh
  go test ./internal/sqlitearchive ./internal/audit -run 'TestDeep|TestStream' -count=1
  ~~~

- [ ] **GREEN — restore check:** inject ArchiveReader only in the CLI deep dependency builder. Validate the newest candidate entirely under temp root and return only declared aggregate evidence.

- [ ] **RED/GREEN — full media inventory:** page the complete configured prefix, reconcile every recorded key/size within budgets, fail missing/size mismatch, warn remote-only through optional durability.media_remote_only, and return unknown for truncated/inconsistent/exhausted inventory. No key enters the report.

- [ ] **CLI deep smoke:** use only synthetic temp roots/object stores in automated tests. Verify raised ceilings require explicit flags and configuration may only lower defaults.

- [ ] **Slice verification:**

  ~~~sh
  go test ./internal/audit ./internal/sqlitearchive ./internal/mediaarchive ./internal/vaultfs ./internal/app -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

**Commit:** feat: add bounded deep audit verification

---

## Task 5: Add The Separate Daily SQLite Archive Scheduler

**Files:**

- Create internal/app/sqlite_archive_scheduler.go
- Create internal/app/sqlite_archive_scheduler_test.go
- Modify internal/app/scheduler.go
- Modify internal/app/scheduler_test.go
- Modify internal/app/serve_remote.go
- Modify internal/app/sqlite_archive_options.go
- Modify internal/sqlitearchive/types.go
- Modify internal/sqlitearchive/archive.go
- Modify internal/metrics/metrics.go
- Modify internal/metrics/metrics_test.go
- Modify config.yaml.sample
- Modify internal/app/env_docs.go
- Modify README.md
- Modify COMMANDS.md
- Modify docs/maintenance-operations.md
- Modify CHANGELOG.md

- [ ] **RED — config:** cover scheduler.sqlite_archive.enabled=false, interval=24h, run_on_start=true, malformed/zero/negative intervals, and explicit environment precedence.

- [ ] **RED — serialization:** with fake clock/writer, prove at most one archive per interval; run-on-start behavior; no overlap with another archive or restore; clean stop/cancellation; and independent sync/audit status.

- [ ] **GREEN — sibling scheduler:** construct a separate scheduler in serve remote. It alone receives sqlitearchive.ObjectWriter and calls the existing online snapshot/archive path. Audit remains read-only.

- [ ] **RED/GREEN — telemetry:** emit archive attempt/start/completed/failed/lock-skip metrics containing timing/status/counts only. No keys, paths, credentials, or provider errors.

- [ ] **Slice verification:**

  ~~~sh
  go test ./internal/app ./internal/sqlitearchive ./internal/metrics -run 'Test.*SQLiteArchive' -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

**Commit:** feat: schedule daily sqlite archives

---

## Task 6: Persist Scheduled Audits And Deliver Transition Alerts

**Files:**

- Create internal/audit/report_store.go
- Create internal/audit/report_store_test.go
- Create internal/audit/freshness.go
- Create internal/audit/freshness_test.go
- Create internal/audit/alert_state.go
- Create internal/audit/alert_state_test.go
- Create internal/audit/webhook.go
- Create internal/audit/webhook_test.go
- Modify internal/safehttp/safehttp.go
- Modify internal/safehttp/safehttp_test.go
- Create internal/app/audit_scheduler.go
- Create internal/app/audit_scheduler_test.go
- Modify internal/app/scheduler.go
- Modify internal/app/serve_remote.go
- Modify internal/metrics/metrics.go
- Modify internal/runtimeenv/config.go
- Modify internal/runtimeenv/secrets.go
- Modify config.yaml.sample
- Modify internal/app/env_docs.go
- Modify README.md
- Modify COMMANDS.md
- Modify docs/maintenance-operations.md
- Modify docs/web-route-capabilities.md
- Modify CHANGELOG.md

- [ ] **RED — private report store:** assert fixed generated paths below log/audit, 0700 directories, 0600 files, no symlink traversal, no-follow append, UTC daily rotation, fsync plus atomic same-directory state rename, exact-profile latest/history, malformed-line isolation, 90-day/256-MiB retention, and content privacy.

- [ ] **GREEN — store:** expose only Save(report), Latest(profile), History(profile, limit), LoadAlertState, and SaveAlertState. Never accept a caller path.

- [ ] **RED/GREEN — freshness:** fast deadline is twice resolved sync interval; standard deadline is max(twice standard_interval, 12h). Exact profile only. Absent report returns report:null plus unknown/not_found without age. Stale retains report but presentation freshness is unknown/stale; never rewrite the immutable report.

- [ ] **RED — schedule ownership:** test post-sync fast runs after the sync result/lock settles, standard every 6h, no overlap, no deep schedule, audit failure cannot change sync success, and disabled audit starts no work.

- [ ] **GREEN — scheduler:** add audit.enabled=false, post_sync_fast=true, standard_interval=6h, and since=7d. Emit audit.run.completed metrics with profile/status/duration/status counts only.

- [ ] **RED — full transition table:** table-test every row in the approved design keyed by exact profile/check ID, including profile-excluded no-op, feature-disabled resolution, optional no-webhook, consecutive observations, status-change counter reset, escalation/de-escalation, repeat_after, immediate recovery, and overall recovery.

  Add one-observation immediate fail exceptions only for:

  - integrity.schema_identity
  - integrity.migration_compatibility
  - integrity.sqlite_quick_check
  - integrity.foreign_keys
  - durability.media_remote
  - durability.sqlite_restore

- [ ] **GREEN — alert state:** persist only stable content-free state. Confirmation ordering is pass < warn < unknown < fail; skipped is not a severity.

- [ ] **RED/GREEN — webhook safety:** URL requires HTTP(S) host/path with no userinfo/query/fragment; public requires HTTPS; private/link-local/loopback requires allow_private_origin and exact canonical origin; proxy disabled; redirects disabled; dial-time DNS/IP validation retained; 10-second timeout; request/response ceilings 64 KiB; bearer token only through ResolveSecretRef. Alert body contains only build identity, overall status, changed check IDs/fixed summaries, observation time, and validated admin origin.

- [ ] **Slice verification:**

  ~~~sh
  go test ./internal/audit ./internal/app ./internal/safehttp ./internal/metrics -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

**Commit:** feat: schedule production audits and alerts

---

## Task 7: Expose The Bounded dbrain_audit MCP Tool

**Files:**

- Create internal/mcpserver/tools_audit.go
- Create internal/mcpserver/tool_schemas_audit.go
- Modify internal/mcpserver/tool_definitions.go
- Modify internal/mcpserver/tools.go
- Modify internal/mcpserver/tool_filter.go
- Modify internal/mcpserver/server.go
- Modify internal/mcpserver/http.go
- Modify internal/mcpserver/transport_stdio.go
- Modify internal/mcpserver/server_test.go
- Modify MCP.md
- Modify README.md
- Modify skills/dbrain-mcp/SKILL.md
- Modify CHANGELOG.md

- [ ] **RED — transport capability:** stdio advertises/calls dbrain_audit; bearer-authenticated HTTP advertises/calls it; auth-disabled HTTP omits it from tools/list and rejects direct calls. Tailnet reachability alone is insufficient.

- [ ] **RED — inputs:** schema permits only profile fast|standard, default fast. Reject deep, categories, since, paths, URLs, endpoints, identifiers, keys, and archive limits.

- [ ] **RED — behavior:** fast runs the full local fast profile under a fixed ten-second deadline and process-wide singleflight; standard returns newest persisted exact-profile standard and never invokes network/audit execution. Both return {report,freshness}; output is <=256 KiB; arrays are [] not null.

- [ ] **GREEN — tool:** inject local fast runner plus report store through server dependencies. Enforce the capability at registration and dispatch, not from a caller boolean.

- [ ] **Schema/privacy controls:** validate runtime structured content against advertised schema; assert no identifier/path/content keys; category mutation of cached reports is impossible.

- [ ] **Slice verification:**

  ~~~sh
  go test ./internal/mcpserver -run 'Test.*Audit' -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

**Commit:** feat: expose bounded audit over mcp

---

## Task 8: Add Fail-Closed Authenticated Admin Audit APIs

**Files:**

- Create web/audit_handlers.go
- Create web/audit_runs.go
- Create web/audit_handlers_test.go
- Modify web/api_types.go
- Modify web/server.go
- Modify web/server_test.go
- Modify web/auth.go only if dependency injection requires it; do not add a new auth mode
- Modify internal/app/serve_remote.go
- Modify docs/web-route-capabilities.md
- Modify README.md
- Modify CHANGELOG.md

**Routes:**

~~~text
GET  /api/audit/latest?profile=standard
GET  /api/audit/history?profile=standard&limit=20
POST /api/audit/run
GET  /api/audit/runs/{audit_id}
~~~

- [ ] **RED — fail closed:** when web authentication is disabled, every audit route is unavailable/forbidden even on local listeners. Prove routes are absent from /share and serviceAuthRoute. Existing authenticated session checks protect GET/POST; shared Origin guard protects POST.

- [ ] **RED — read APIs:** exact-profile latest/history only; GET never starts work; history default 20 and accepts 1..100; absent/stale/current freshness wire forms match the design.

- [ ] **RED — bounded POST:** require application/json, 4 KiB maximum body, profile fast|standard only. Accepted returns 202 with ID/profile/running/status_path. Same active profile returns same 202/ID; different profile returns 409 with active ID/profile; standard starts less than 60 seconds apart return 429/retry_after_seconds.

- [ ] **RED — run lifecycle:** one process-wide in-flight audit; persist completed report before state=completed; failed returns only sanitized error code; status returns running/completed/failed; unknown/evicted ID is 404.

- [ ] **RED/GREEN — retention:** retain active plus at most 100 terminal records for 24 hours. On insert and hourly, remove expired first, then oldest until 100; never evict active.

- [ ] **GREEN — handlers:** inject audit runner/report store/run coordinator through web.HandlerOptions. Use the same immutable report/freshness envelope as MCP.

- [ ] **Slice verification:**

  ~~~sh
  go test ./web -run 'TestAudit' -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

**Commit:** feat: add authenticated audit admin api

---

## Task 9: Replace Ambiguous Admin Health With The Shared Audit View

**Files:**

- Create web/ui/src/components/AuditOverview.svelte
- Create web/ui/src/components/AuditImporters.svelte
- Create web/ui/src/components/AuditPipeline.svelte
- Create web/ui/src/components/AuditDurability.svelte
- Create web/ui/src/components/AuditFindings.svelte
- Create web/ui/src/components/AuditHistory.svelte
- Modify web/ui/src/App.svelte
- Modify web/ui/src/app.css
- Modify web/ui/src/app.test.js
- Refresh web/ui/dist
- Modify web/stats_handlers.go
- Modify web/api_types.go
- Modify web/server_test.go
- Modify README.md
- Modify CHANGELOG.md

- [ ] **RED — semantic presentation:** browser/component tests prove overall health comes only from the standard audit report; absent/stale is visibly unknown/stale; a newer fast report cannot replace standard; poll and arrivals are distinct; terminal is separate from failed; SQLite backup/media/OKF cards use audit checks.

- [ ] **RED — legacy scope:** keep backlog.drained for compatibility but label it Source backlog drained and return an explicit source-processing scope description.

- [ ] **GREEN — panel:** show overall health/build/layout/last sync/last audit, configured importer poll/arrival, pipeline partitions, durability, failed/unknown first, warnings, and recent history/recovery. Provide authenticated fast refresh and standard start/poll behavior; page load performs only GET.

- [ ] **Mobile/product checks:** at narrow width cards stack, check IDs/fixed summaries wrap, no horizontal overflow, selected detail scrolls into view, empty/loading/error states are clear, and no raw Markdown/JSON/path leaks.

- [ ] **UI verification:**

  ~~~sh
  cd web/ui
  npm test
  npm run build
  cd ../..
  go test ./web -run 'Test(Audit|Bootstrap|Stats)' -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

  Inspect the rendered authenticated admin page at desktop and mobile sizes against a synthetic server. Do not use production credentials or state.

**Commit:** feat: add production health admin dashboard

---

## Task 10: Add Bounded Upstream Parity One Importer At A Time

**Files:**

- Create internal/audit/upstream.go
- Create internal/audit/upstream_test.go
- Create internal/store/audit_upstream.go
- Create internal/store/audit_upstream_test.go
- Modify internal/app/audit.go
- Modify internal/app/audit_test.go
- Modify importer-owned client files under internal/githubimport, internal/youtubeimport, internal/xapi or the existing X bookmark package, internal/feedimport, internal/applenotes, and internal/safaritabs
- Add focused tests beside each modified importer client
- Modify README.md
- Modify COMMANDS.md
- Modify importer-specific docs
- Modify CHANGELOG.md

**Interface:**

~~~go
type UpstreamInventory interface {
    Inventory(ctx context.Context, budget InventoryBudget) (InventoryResult, error)
}
type InventoryBudget struct {
    MaxIdentities int // fixed ceiling 100000
    MaxPages int      // fixed ceiling 10000
}
func (s *AuditReadSnapshot) CountLocalIdentityMatches(
    ctx context.Context,
    source AuditSource,
    identities []string,
) (int, error)
~~~

InventoryResult contains normalized identity hashes/counts and completion/page metadata only. The adapter performs local matching through StoreSnapshot; report evidence contains counts, never identities.

- [ ] **Shared RED/GREEN:** source-specific commands are effective deep and reject explicit fast/standard. Explicit source override requires that source parity even when scheduler-disabled; its poll is skipped(feature_disabled), arrivals optional. Five-minute timeout per source. Hitting identity/page cap before importer-declared end is incomplete/unknown.

- [ ] **GitHub stars:** implement dbrain audit github-stars --json by reusing the existing authenticated pagination and gh-star identity semantics. Complete zero-missing passes; complete missing fails; auth/pagination failure unknown.

- [ ] **YouTube:** implement youtube-liked and youtube-watch-later separately, reusing existing cookie/browser/session, pagination, response-size, and identity rules. Never accept an audit endpoint override.

- [ ] **X bookmarks:** reuse the native cookie-backed GraphQL/session client, overlap/identity semantics, pagination limits, and destination policy. Do not infer saved_at or mutate bookmarks.

- [ ] **Feeds:** inventory configured feed origins and bounded upstream entries using feedimport safe HTTP policy. Local-only historical rows remain expected; never propose deletion.

- [ ] **Apple Notes:** use a dbrain-owned snapshot and read-only parser path. Do not mutate the live Notes DB and do not retain protected-note metadata. Snapshot/access/schema failure is unknown.

- [ ] **Safari tabs:** use a read-only dbrain-owned snapshot and explicit device identity. Do not launch Safari or mutate/close tabs from audit.

- [ ] **Aggregate deep:** audit all --profile deep runs only the configured source set, while each explicit source command runs its named parity override. Assert all seven exact upstream.*.parity registry entries now have runners.

- [ ] **Safe-network matrix:** test public/private/loopback/link-local/IPv6, mixed DNS, redirects, userinfo, proxy non-use, exact configured private origins, and separate trusted/untrusted clients with injected transports only.

- [ ] **Slice verification after each importer and again after all seven:**

  ~~~sh
  go test ./internal/audit ./internal/app ./internal/store ./internal/githubimport ./internal/youtubeimport ./internal/feedimport ./internal/applenotes ./internal/safaritabs -count=1
  go test ./internal/xapi/... -count=1
  task fmt
  task lint
  task test-ci
  task build
  ~~~

  CountLocalIdentityMatches accepts only the closed seven-value AuditSource enum and enforces the 100,000-identity cap before constructing SQL. Keep one commit per importer if review size warrants it.

**Commits:**

- feat: audit github and youtube upstream parity
- feat: audit x and feed upstream parity
- feat: audit local app upstream parity

---

## Task 11: Add The Release-Audit Skill Wrapper

**Files:**

- Create skills/dbrain-production-audit/SKILL.md
- Create skills/dbrain-production-audit/agents/openai.yaml
- Create skills/dbrain-production-audit/references/release-workflow.md
- Modify README.md
- Modify MCP.md
- Modify .github/workflows/publish.yml only if this skill is approved for published release artifacts
- Modify CHANGELOG.md

- [ ] **Baseline test:** ask a fresh agent to audit a synthetic installation without the skill and record omissions: target resolution, pre/post comparison, expect-commit, deep-only archive validation, explicit config, content privacy, and no deploy/repair authority.

- [ ] **Create skill:** use the installed skill-creator tooling. The skill must:

  - resolve the real installed production config explicitly before audit;
  - run a standard pre-release report and require fresh backup evidence;
  - after separately approved installation/restart, run deep with --expect-commit;
  - compare exact check IDs/statuses/configured sources/partitions/durability;
  - retain content-free pre/post JSON reports privately;
  - interpret exit 0/1/2/3 correctly;
  - stop for deployment, restore, repair, retry, prune, or upstream mutation approval;
  - invoke the CLI only and own no SQL/health policy.

- [ ] **Skill RED/GREEN forward tests:** give fresh agents scenarios for a quiet but healthy importer, stale backup, terminal transcription outcomes, missing post-cutover provenance, unknown metrics history, and changed released commit. Verify they use audit evidence and do not improvise SQLite queries or mutate state.

- [ ] **Validate structure/content:**

  ~~~sh
  python3 /Users/darron/.codex/skills/.system/skill-creator/scripts/quick_validate.py skills/dbrain-production-audit
  rg -n 'TODO|TBD|FIXME|\[TODO' skills/dbrain-production-audit
  git diff --check
  ~~~

  Expected: validator exits zero; placeholder search has no matches.

- [ ] **Publication decision:** if the skill is repo-local only, document that and do not edit publish.yml. If it is added to the public skill matrix, verify the exact package identity, pinned actions, and existing OIDC permissions without broadening them.

**Commit:** feat: add dbrain production audit skill

---

## Final Cross-Slice Verification And Release Handoff

- [ ] Confirm all normative check IDs have one registry entry and, after Task 10, one intended runner or explicit profile/feature skip path.

- [ ] Confirm audit dependencies cannot reach store writes, restore, prune, arbitrary paths/URLs/keys, archive writer, or unrestricted HTTP.

- [ ] Run full gates from a clean CI-like environment:

  ~~~sh
  task fmt
  task lint
  task test-ci
  task build
  git diff --check
  ~~~

- [ ] Build and smoke the repo/dev boundary:

  ~~~sh
  direnv exec . ./bin/dbrain --no-debug config paths --json
  direnv exec . ./bin/dbrain --no-debug audit all --profile fast --json
  direnv exec . ./bin/dbrain --no-debug audit all --profile standard --json
  ~~~

  Record the resolved repo/dev paths. Do not label this production verification.

- [ ] Validate shared JSON against dbrain.audit.v1, assert content/privacy scan, and verify CLI/MCP/admin expose identical immutable check IDs/statuses for the same persisted report.

- [ ] Render and inspect the authenticated admin page on desktop and mobile synthetic fixtures.

- [ ] Review README.md, COMMANDS.md, config.yaml.sample, internal/app/env_docs.go, MCP.md, docs/architecture.md, docs/schema-migrations.md, docs/maintenance-operations.md, docs/web-route-capabilities.md, importer docs, skills, and CHANGELOG.md for exact shipped behavior.

- [ ] Before any real production check or deployment, request separate approval. Then resolve ~/.config/dbrain/config.yaml or the installed binary's explicit config, preserve a pre-release report, verify the running binary identity, and run the approved read-only post-release workflow. Never infer production from the checkout.
