# Semantic ANN Resumable Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a durable, resumable semantic refresh orchestrator and a manual/configured `dbrain semantic refresh` path that projects, embeds, flushes, compacts, verifies, and reaches final readiness without wiring refresh into sync yet.

**Architecture:** SQLite owns a versioned refresh-run ledger with one resumable run per embedding profile, an immutable projection watermark, stage checkpoints, bounded counters, and compare-and-swap updates. A build-neutral runner executes one bounded stage unit at a time through narrow projection and embedding seams, composes the existing immutable `Flush`, `Compact`, `RunVerify`, readiness, and tagged native-root verification paths, and rolls forward to a successor run when writes arrive above the fixed watermark. The app-level configured helper is the sole mode/capability gate and is reusable by stacked PR 3; `off` and `unsupported` skip without constructing providers, `supported_broken` fails, and only `supported_ready` executes refresh.

**Tech Stack:** Go 1.24, SQLite, Cobra, existing retrieval projection/embedding stores, immutable semantic segments and root manifests, CGO-tagged USearch `2.26.0`, Go tests with race coverage.

## Global Constraints

- Work only in
  `/Users/darron/src/dbrain/.worktrees/semantic-ann-resumable-refresh` on
  `codex/semantic-ann-resumable-refresh`.
- Build on the accepted stacked PR 1 code at the current branch head; do not modify PR #100 or rewrite stacked history.
- Recheck `internal/store/migrations.go`, `main`, and recent local semantic branches before implementation. If version `25` is still unused, allocate `25` as `semanticRefreshRunsMigrationVersion` with name `semantic_refresh_runs_v1`; if another branch has claimed `25`, use the next unused version instead of reusing it.
- SQLite remains authoritative. Refresh-run rows and every ANN cache artifact are derived, resumable state; they never become authoritative evidence.
- `research.semantic.mode` remains the only activation control. Do not add a maintenance toggle.
- `off` must not open the writable store, construct an embedding provider, or construct a native builder.
- `unsupported` must be a successful bounded skip and must not open the writable store, construct an embedding provider, or construct a native builder.
- `supported_broken` must return a typed non-zero error and must not silently fall back to exact or lexical maintenance.
- Only `supported_ready` with mode `shadow` or `on` may execute refresh.
- A refresh run owns one immutable projection watermark and purge epoch. Work dirtied above that watermark belongs to a new successor run.
- At most one resumable run per profile may exist. Configuration selecting a new profile supersedes resumable runs for other profiles without deleting their derived rows.
- Database scope is implicit in the SQLite file that owns the ledger. Do not duplicate `database_id` in every row; the existing per-database retrieval identity remains available for cache/root verification.
- Provider and embedding persistence batches must never exceed `5,000` rows.
- Retryable provider calls use at most three consecutive attempts for one selected batch, starting at `1s` exponential backoff and capped explicitly at `30s`; success resets the failure sequence. Fatal configuration, provenance, dimension, authentication, and other terminal errors fail immediately.
- Exact L0 uses `5,000` as the ready target and `10,000` as the absolute catch-up ceiling. Refresh must flush before a provider call whose persisted result could exceed `10,000`.
- Keep each physical compaction bounded by the existing `200,000`-live-vector cap.
- Emit serialized bounded progress no less often than every `5s` while work is active. Persist completed stage checkpoints before emitting progress that claims them.
- Typed errors and stored diagnostics are bounded: error code at most `64` bytes, checkpoint at most `256` bytes, and concise error text at most `512` bytes. Never store provider response bodies, vectors, source text, filesystem paths, or corpus-sized arrays in the run ledger or error output.
- Cancellation is an error, not a false success. Preserve stage data and mark the run resumable before returning whenever the bounded final checkpoint write succeeds.
- Do not wire any `sync` command in this PR. Universal synchronous post-sync integration and strict sync exit semantics belong to stacked PR 3.
- Do not add cross-process maintenance/generation locks or claim concurrent refresh safety in this PR. Locking and writer preference belong to stacked PR 4.
- Do not change native release linking, Homebrew packaging, or release matrices in this PR. Distribution belongs to stacked PR 5.
- Do not run installed-binary or production-corpus acceptance, activate production, or mutate the production XDG database/cache. Those gates belong to stacked PR 6.
- Query, research, MCP, and web paths must not invoke refresh.
- Keep semantic mode explicit-off by default and preserve fail-open lexical querying throughout this intermediate PR.
- Add a changelog entry and user-facing command/status documentation because the run ledger, `semantic refresh`, progress, errors, and status output are user-visible.
- Use test-driven development: add each failing test before its production change.
- After focused tests, run `task fmt`, `task lint`, `task test-ci`, `task build`, and the tagged USearch test/build gates listed in Task 10.

---

## File Map

### Durable store

- `internal/store/semantic_refresh_runs.go`: refresh-run enums, bounded row/update types, create-or-resume/profile-supersession transaction, CAS checkpoint updates, progress heartbeat touch, and latest-run reads.
- `internal/store/semantic_refresh_runs_test.go`: ledger lifecycle, immutability, bounds, supersession, stale-CAS, cancellation resume, and latest-run tests.
- `internal/store/retrieval_schema.go`: call the refresh-run schema repair from the normal semantic schema path.
- `internal/store/schema_identity.go`: require the refresh-run table only for databases stamped with the new migration.
- `internal/store/migrations.go`: append the new migration version and name.
- `internal/store/migrations_test.go`: prove a genuine prior-version database receives the table/indexes exactly once and reopens idempotently.

### Existing bounded stage seams

- `internal/semanticbuild/chunk.go`: expose one projection batch pinned to a caller-supplied watermark while preserving the existing manual command.
- `internal/semanticbuild/projection_batch_test.go`: fixed-watermark, staged-giant-parent resume, and above-watermark exclusion tests.
- `internal/semanticbuild/embed.go`: expose one bounded embedding batch, persist its resulting embedding revision, and add bounded in-memory provider retry/backoff.
- `internal/semanticbuild/embed_batch_test.go`: provider-call count/order, retry reset/circuit, revision persistence, fatal/blocked behavior, and `5,000` cap tests.

### Refresh engine

- `internal/semanticrefresh/types.go`: public-internal request/result/progress/debt/native-lifecycle and stage-executor contracts.
- `internal/semanticrefresh/errors.go`: stable typed error codes, bounded formatting, cancellation classification, and error unwrapping.
- `internal/semanticrefresh/progress.go`: serialized immediate-plus-`5s` heartbeat that touches the durable run before callbacks.
- `internal/semanticrefresh/types_test.go`: validation, JSON bounds, and error tests.
- `internal/semanticrefresh/progress_test.go`: deterministic heartbeat serialization and checkpoint-before-progress tests.
- `internal/semanticrefresh/runner.go`: generic create/resume, CAS transition, pause, completion, and successor-run driver.
- `internal/semanticrefresh/runner_test.go`: fake-executor tests for resume, stale CAS, cancellation, failure, completion, and roll-forward.
- `internal/semanticrefresh/pipeline.go`: concrete projection, embedding, flush, compaction, verify, native-root verification, readiness, and successor handlers.
- `internal/semanticrefresh/pipeline_test.go`: exact stage ordering, L0 headroom, compaction debt, verify cursor, root verification, and readiness tests.
- `internal/semanticrefresh/refresh_integration_test.go`: full fake-provider/store interruption-and-resume and no-duplicate-completed-work cases.

### Configured/manual app surface

- `internal/app/semantic_refresh_helper.go`: reusable configured refresh helper and its dependency boundary.
- `internal/app/semantic_refresh_helper_test.go`: mode/capability gate and provider-construction ordering tests.
- `internal/app/semantic_refresh.go`: `semantic refresh` Cobra command.
- `internal/app/semantic_refresh_test.go`: human/JSON output, cancellation, and CLI tests.
- `internal/app/semantic_refresh_native_default.go`: CGO-free native-lifecycle constructor, which remains unreachable after the `unsupported` skip.
- `internal/app/semantic_refresh_native_usearch.go`: tagged USearch builder plus active-root open/close verification.
- `internal/app/semantic.go`: register the new subcommand.
- `internal/app/semantic_output.go`: bounded refresh progress/final/error and latest-run status rendering.
- `internal/semanticbuild/status.go`: attach the latest selected-profile or database run to status.
- `internal/semanticbuild/status_refresh_test.go`: status latest-run object coverage.

### Documentation

- `README.md`: document the manual refresh/status contract without claiming automatic sync, locking, packaging, or installed support.
- `docs/research-harness.md`: replace the old list of separate maintenance commands with the resumable refresh diagnostic flow and state its current stacked-PR limitations.
- `CHANGELOG.md`: record the durable run ledger, manual refresh command, progress/errors, and status object.

---

### Task 1: Add the versioned durable refresh-run ledger

**Files:**

- Create: `internal/store/semantic_refresh_runs.go`
- Create: `internal/store/semantic_refresh_runs_test.go`
- Modify: `internal/store/retrieval_schema.go`
- Modify: `internal/store/schema_identity.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`

**Interfaces:**

- Consumes: existing `retrieval_state.purge_epoch`, monotonic `projection_work_revision`, SQLite transactions, and migration validation.
- Produces: `SemanticRefreshRun`, `StartSemanticRefreshRunInput`, `SemanticRefreshRunUpdate`, `StartOrResumeSemanticRefreshRun`, `UpdateSemanticRefreshRun`, `TouchSemanticRefreshRunProgress`, `LatestSemanticRefreshRun`, and `ErrSemanticRefreshRunStale`.

- [ ] **Step 1: Recheck migration ownership and write the failing migration test**

Run:

```bash
git log --all --oneline -G 'currentSchemaVersion[[:space:]]*=[[:space:]]*25|MigrationVersion[[:space:]]*=[[:space:]]*25' -- internal/store/migrations.go
git show main:internal/store/migrations.go | sed -n '1,45p'
sed -n '1,45p' internal/store/migrations.go
```

Expected at plan-writing time: the stacked head is at schema `24`, `main` is behind it, and no branch commit claims `25`. If that remains true, use:

```go
const (
	currentSchemaVersion                 = 25
	semanticRefreshRunsMigrationVersion = 25
	semanticRefreshRunsMigrationName    = "semantic_refresh_runs_v1"
)
```

Add `TestSemanticRefreshRunsMigrationUpgradesV24DatabaseIdempotently`. Construct a real store, remove migration rows above `24`, set `PRAGMA user_version=24`, drop only `semantic_refresh_runs` and its indexes, then reopen. Assert:

- migration `25` exists once with the exact name;
- every required column exists;
- `idx_semantic_refresh_runs_one_resumable` and `idx_semantic_refresh_runs_latest` exist;
- a second reopen adds no migration row and preserves an inserted run.

- [ ] **Step 2: Write failing ledger lifecycle tests**

In `semantic_refresh_runs_test.go`, define a fixed clock and cover:

```go
func TestSemanticRefreshRunResumePreservesImmutableWatermark(t *testing.T)
func TestSemanticRefreshRunProfileChangeSupersedesOldRun(t *testing.T)
func TestSemanticRefreshRunPurgeEpochChangeSupersedesOldRun(t *testing.T)
func TestSemanticRefreshRunCASRejectsStaleWriter(t *testing.T)
func TestSemanticRefreshRunFailedAndCancelledStatesResume(t *testing.T)
func TestSemanticRefreshRunBoundsCheckpointAndDiagnostics(t *testing.T)
func TestLatestSemanticRefreshRunFiltersProfileOrReturnsDatabaseLatest(t *testing.T)
```

Use the exact state/stage contracts:

```go
type SemanticRefreshRunState string

const (
	SemanticRefreshRunRunning    SemanticRefreshRunState = "running"
	SemanticRefreshRunFailed     SemanticRefreshRunState = "failed"
	SemanticRefreshRunCancelled  SemanticRefreshRunState = "cancelled"
	SemanticRefreshRunCompleted  SemanticRefreshRunState = "completed"
	SemanticRefreshRunSuperseded SemanticRefreshRunState = "superseded"
)

type SemanticRefreshStage string

const (
	SemanticRefreshProjection SemanticRefreshStage = "projection"
	SemanticRefreshEmbedding  SemanticRefreshStage = "embedding"
	SemanticRefreshFlush      SemanticRefreshStage = "flush"
	SemanticRefreshCompaction SemanticRefreshStage = "compaction"
	SemanticRefreshVerify     SemanticRefreshStage = "verify"
	SemanticRefreshReadiness  SemanticRefreshStage = "readiness"
)
```

`running`, `failed`, and `cancelled` are resumable. `completed` and `superseded` are terminal.

- [ ] **Step 3: Prove the tests fail**

Run:

```bash
go test ./internal/store -run 'SemanticRefreshRun|SemanticRefreshRunsMigration'
```

Expected: compile failures because the ledger types and methods do not exist.

- [ ] **Step 4: Create the exact bounded schema**

Implement `ensureSemanticRefreshRunSchema` with:

```sql
CREATE TABLE IF NOT EXISTS semantic_refresh_runs (
	run_id TEXT PRIMARY KEY,
	profile_id TEXT NOT NULL,
	purge_epoch INTEGER NOT NULL,
	projection_watermark INTEGER NOT NULL,
	embedding_revision INTEGER NOT NULL DEFAULT 0,
	stage TEXT NOT NULL,
	checkpoint TEXT NOT NULL DEFAULT '',
	projected_parents INTEGER NOT NULL DEFAULT 0,
	embedded_chunks INTEGER NOT NULL DEFAULT 0,
	flushed_vectors INTEGER NOT NULL DEFAULT 0,
	compacted_vectors INTEGER NOT NULL DEFAULT 0,
	verified_vectors INTEGER NOT NULL DEFAULT 0,
	successor_runs INTEGER NOT NULL DEFAULT 0,
	current_generation_id TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	error_code TEXT NOT NULL DEFAULT '',
	error_text TEXT NOT NULL DEFAULT '',
	readiness_state TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	last_progress_at TEXT NOT NULL,
	CHECK(length(run_id) BETWEEN 1 AND 64),
	CHECK(length(profile_id) BETWEEN 1 AND 192),
	CHECK(purge_epoch >= 0),
	CHECK(projection_watermark >= 0),
	CHECK(embedding_revision >= 0),
	CHECK(stage IN ('projection','embedding','flush','compaction','verify','readiness')),
	CHECK(length(checkpoint) <= 256),
	CHECK(projected_parents >= 0 AND embedded_chunks >= 0),
	CHECK(flushed_vectors >= 0 AND compacted_vectors >= 0 AND verified_vectors >= 0),
	CHECK(successor_runs >= 0),
	CHECK(state IN ('running','failed','cancelled','completed','superseded')),
	CHECK(length(error_code) <= 64),
	CHECK(length(error_text) <= 512),
	CHECK(length(readiness_state) <= 64),
	CHECK(version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_semantic_refresh_runs_one_resumable
	ON semantic_refresh_runs(profile_id)
	WHERE state IN ('running','failed','cancelled');

CREATE INDEX IF NOT EXISTS idx_semantic_refresh_runs_latest
	ON semantic_refresh_runs(updated_at DESC, run_id DESC);
```

Call it from the normal retrieval schema repair path and migration `25`. Add a conditional `dbrainSemanticRefreshRunSchemaV25` identity group so read-only compatibility checks require it only when migration `25` is stamped.

- [ ] **Step 5: Implement immutable rows and CAS writes**

Use:

```go
type SemanticRefreshCounters struct {
	ProjectedParents int64 `json:"projected_parents"`
	EmbeddedChunks   int64 `json:"embedded_chunks"`
	FlushedVectors   int64 `json:"flushed_vectors"`
	CompactedVectors int64 `json:"compacted_vectors"`
	VerifiedVectors  int64 `json:"verified_vectors"`
	SuccessorRuns    int64 `json:"successor_runs"`
}

type SemanticRefreshRun struct {
	RunID, ProfileID, Checkpoint, CurrentGenerationID string
	ErrorCode, ErrorText, ReadinessState               string
	PurgeEpoch, ProjectionWatermark, EmbeddingRevision int64
	Version                                            int64
	Stage                                              SemanticRefreshStage
	State                                              SemanticRefreshRunState
	Counters                                           SemanticRefreshCounters
	CreatedAt, UpdatedAt, LastProgressAt               time.Time
}

type StartSemanticRefreshRunInput struct {
	RunID, ProfileID                    string
	PurgeEpoch, ProjectionWatermark     int64
	Now                                 time.Time
}

type SemanticRefreshRunUpdate struct {
	RunID, Checkpoint, CurrentGenerationID string
	ErrorCode, ErrorText, ReadinessState    string
	ExpectedVersion, EmbeddingRevision      int64
	Stage                                   SemanticRefreshStage
	State                                   SemanticRefreshRunState
	Counters                                SemanticRefreshCounters
	Now                                     time.Time
}

var ErrSemanticRefreshRunStale = errors.New("semantic refresh run changed")

func (s *Store) StartOrResumeSemanticRefreshRun(
	ctx context.Context,
	input StartSemanticRefreshRunInput,
) (SemanticRefreshRun, bool, error)

func (s *Store) UpdateSemanticRefreshRun(
	ctx context.Context,
	input SemanticRefreshRunUpdate,
) (SemanticRefreshRun, error)

func (s *Store) TouchSemanticRefreshRunProgress(
	ctx context.Context,
	runID string,
	at time.Time,
) error

func (s *Store) LatestSemanticRefreshRun(
	ctx context.Context,
	profileID string,
) (*SemanticRefreshRun, error)
```

`StartOrResumeSemanticRefreshRun` must transact in this order:

1. supersede resumable rows for other profiles;
2. supersede a same-profile resumable row whose purge epoch changed;
3. resume the remaining same-profile row by changing state to `running`, clearing its last error, incrementing `version`, and preserving its original watermark and all counters;
4. otherwise insert the supplied run at stage `projection`, state `running`, and version `1`.

`UpdateSemanticRefreshRun` updates only mutable columns with `WHERE run_id=? AND version=?`, increments `version`, and returns `ErrSemanticRefreshRunStale` when affected rows are not exactly one. It never changes profile, purge epoch, projection watermark, run ID, or creation time.

`TouchSemanticRefreshRunProgress` updates only `last_progress_at` and `updated_at`; it deliberately does not change `version`, so a heartbeat cannot invalidate a stage checkpoint CAS.

- [ ] **Step 6: Run focused store tests**

Run:

```bash
go test ./internal/store -run 'SemanticRefreshRun|SemanticRefreshRunsMigration|SchemaIdentity'
```

Expected: all tests pass, including stale writer and version-24 repair coverage.

- [ ] **Step 7: Commit**

```bash
git add internal/store/semantic_refresh_runs.go \
  internal/store/semantic_refresh_runs_test.go \
  internal/store/retrieval_schema.go \
  internal/store/schema_identity.go \
  internal/store/migrations.go \
  internal/store/migrations_test.go
git commit -m "feat(semantic): persist resumable refresh runs"
```

---

### Task 2: Expose one projection batch pinned to the run watermark

**Files:**

- Modify: `internal/semanticbuild/chunk.go`
- Create: `internal/semanticbuild/projection_batch_test.go`

**Interfaces:**

- Consumes: `ChunkStore.ProjectionWorkRevision`, `ListDirtyRetrievalParents(ctx, watermark, limit)`, existing giant-parent staging, and `ApplyRetrievalProjection`.
- Produces: `ProjectionBatchOptions` and `RunProjectionBatch`, used by the concrete refresh pipeline in Task 6.

- [ ] **Step 1: Write failing fixed-watermark tests**

Add:

```go
type ProjectionBatchOptions struct {
	Watermark int64
	Limit     int
	Progress  func(ChunkProgress) error
	Now       func() time.Time
}

func RunProjectionBatch(
	ctx context.Context,
	st ChunkStore,
	opts ProjectionBatchOptions,
) (ChunkProgress, error)
```

Cover:

- a batch processes only `dirty_revision <= Watermark`;
- a parent dirtied above the watermark remains untouched and `HasMore` describes only work through the watermark;
- rerunning the same watermark resumes a staged giant parent rather than rebuilding committed staging rows;
- stale old-revision validation leaves the newly dirtied parent for a successor watermark;
- `Watermark < 0`, `Limit <= 0`, and `Limit > 5_000` fail before store access;
- an empty fixed-watermark batch returns bounded zero progress and no cursor array.

- [ ] **Step 2: Prove the new seam is absent**

Run:

```bash
go test ./internal/semanticbuild -run 'ProjectionBatch|PinnedWatermark|AboveWatermark'
```

Expected: compile failure because `ProjectionBatchOptions` and `RunProjectionBatch` do not exist.

- [ ] **Step 3: Refactor without duplicating projection logic**

Move the current per-page body behind `RunProjectionBatch`. It must pass the supplied watermark directly to:

```go
st.ListDirtyRetrievalParents(ctx, opts.Watermark, opts.Limit+1)
```

Keep giant-parent staging, stale-work handling, occurrence limits, and checkpoint persistence unchanged.

Refactor `RunChunk` as a compatibility wrapper:

1. capture `ProjectionWorkRevision` once when the command starts;
2. call `RunProjectionBatch` repeatedly only when `UntilIdle`;
3. never advance that invocation's watermark mid-command;
4. retain the command's cooperative `MaxDuration` behavior and aggregate progress.

The refresh runner, not `RunProjectionBatch`, decides when to allocate a successor watermark.

- [ ] **Step 4: Run focused projection tests**

Run:

```bash
go test ./internal/semanticbuild -run 'Chunk|ProjectionBatch|PinnedWatermark|Giant'
```

Expected: the new fixed-watermark cases and all existing chunk command cases pass.

- [ ] **Step 5: Commit**

```bash
git add internal/semanticbuild/chunk.go \
  internal/semanticbuild/projection_batch_test.go
git commit -m "refactor(semantic): pin projection refresh batches"
```

---

### Task 3: Expose one embedding batch with persisted revision and bounded retry

**Files:**

- Modify: `internal/semanticbuild/embed.go`
- Create: `internal/semanticbuild/embed_batch_test.go`

**Interfaces:**

- Consumes: existing profile-aware chunk selection, `PutRetrievalEmbeddingBatch`, embedding failure classification, and vector validation.
- Produces: `EmbedBatchOptions`, `EmbedBatchResult`, and `RunEmbedBatch`, including the exact persisted profile revision and a before-provider headroom callback.

- [ ] **Step 1: Write failing one-batch and retry tests**

Define:

```go
const (
	MaxEmbeddingBatchSize          = 5_000
	MaxConsecutiveProviderFailures = 3
	DefaultRetryInitialBackoff     = time.Second
	DefaultRetryMaxBackoff         = 30 * time.Second
)

type EmbedBatchOptions struct {
	BatchSize          int
	RetryCooldown      time.Duration
	RetryInitialBackoff time.Duration
	RetryMaxBackoff    time.Duration
	Now                func() time.Time
	Sleep              func(context.Context, time.Duration) error
	BeforeProvider     func(context.Context, int) error
}

type EmbedBatchResult struct {
	Progress
	Revision int64 `json:"revision"`
	HasMore  bool  `json:"has_more"`
}

func RunEmbedBatch(
	ctx context.Context,
	st EmbedStore,
	provider embedding.Provider,
	opts EmbedBatchOptions,
) (EmbedBatchResult, error)
```

Add tests proving:

- one call selects and persists no more than `BatchSize` and never more than `5,000`;
- `BeforeProvider(ctx, len(batch))` runs before the first provider call;
- a preflight error prevents the provider call and persistence;
- successful persistence returns the exact revision from `PutRetrievalEmbeddingBatch`;
- two retryable failures sleep `1s`, then `2s`, and a third successful attempt persists once;
- three retryable failures persist one bounded retry/error batch and return `ErrEmbedCircuitOpen`;
- the configured backoff can never exceed `30s`;
- a successful batch starts the next call with zero prior consecutive failures;
- fatal configuration/provenance/dimension/authentication errors return immediately without sleeping;
- blocked multi-row batches preserve the existing bounded bisection behavior and persist terminal single-row blocks;
- cancellation during backoff returns `context.Canceled` or `context.DeadlineExceeded` without another provider call.

- [ ] **Step 2: Prove the tests fail**

Run:

```bash
go test ./internal/semanticbuild -run 'EmbedBatch|ProviderRetry|EmbedCircuit'
```

Expected: compile failures for the new batch types and function.

- [ ] **Step 3: Implement one selected logical batch**

Refactor candidate selection, provider validation, row construction, and persistence out of `RunEmbed` without copying them.

`RunEmbedBatch` must:

1. resolve the profile and purge epoch;
2. select one due/current batch using one fixed `Now`;
3. when no candidates exist, read and return the profile's current `LatestRevision` with `HasMore=false` and no provider call; return revision zero only when the profile does not exist yet;
4. invoke `BeforeProvider` with the selected count;
5. retry the same selected texts in memory for retryable failures only;
6. validate provider provenance, cardinality, dimensions, normalization, and finite values;
7. call `PutRetrievalEmbeddingBatch` exactly once for a successful logical batch;
8. return that method's revision;
9. report `HasMore` from a bounded remaining-count query.

When the third retryable attempt fails, return an `EmbedBatchResult` containing the revision committed for the persisted retry/error rows alongside `ErrEmbedCircuitOpen`. This lets the durable runner checkpoint the committed revision before it marks the run failed.

Use a context-aware default sleep:

```go
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
```

Compute delay with saturating doubling and `min(delay, RetryMaxBackoff)`. Reject a configured maximum greater than `30s`; callers may lower but not raise the safety bound.

- [ ] **Step 4: Preserve the existing manual embed command**

Make `RunEmbed` loop over `RunEmbedBatch` for `UntilIdle`, retain `MaxDuration`, merge bounded snapshots, and preserve current `Limit`/`BatchSize` command semantics. A circuit-open error remains an error; do not convert it into graceful interruption.

- [ ] **Step 5: Run focused embedding tests**

Run:

```bash
go test ./internal/semanticbuild -run 'Embed|ProviderRetry|Circuit|Batch'
```

Expected: all new cases and existing semantic embed tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/semanticbuild/embed.go \
  internal/semanticbuild/embed_batch_test.go
git commit -m "feat(semantic): checkpoint embedding refresh batches"
```

---

### Task 4: Define bounded refresh results, errors, native lifecycle, and heartbeat

**Files:**

- Create: `internal/semanticrefresh/types.go`
- Create: `internal/semanticrefresh/errors.go`
- Create: `internal/semanticrefresh/progress.go`
- Create: `internal/semanticrefresh/types_test.go`
- Create: `internal/semanticrefresh/progress_test.go`

**Interfaces:**

- Consumes: store refresh-run types, semantic readiness, semantic capability, and existing flush/compaction builder interfaces.
- Produces: `Request`, `Result`, `Progress`, `Debt`, `NativeLifecycle`, `StageExecutor`, `StageOutcome`, `RefreshError`, and the serialized progress emitter used by Tasks 5–8.

- [ ] **Step 1: Write failing type and bounded-error tests**

Use these exact public-internal contracts:

```go
type Outcome string

const (
	OutcomeSkipped   Outcome = "skipped"
	OutcomeCompleted Outcome = "completed"
)

type Debt struct {
	DirtyParents      int `json:"dirty_parents"`
	PendingEmbeddings int `json:"pending_embeddings"`
	DueRetries       int `json:"due_retries"`
	ScheduledRetries int `json:"scheduled_retries"`
	BlockedEmbeddings int `json:"blocked_embeddings"`
	FailedEmbeddings int `json:"failed_embeddings"`
	L0Ready          int `json:"l0_ready"`
	Tombstones       int `json:"tombstones"`
	Segments         int `json:"segments"`
}

type Progress struct {
	RunID, ProfileID, Checkpoint, Readiness string
	Stage                                   store.SemanticRefreshStage
	Counters                                store.SemanticRefreshCounters
	Debt                                    Debt
	At                                      time.Time
}

type Result struct {
	Outcome    Outcome                  `json:"outcome"`
	SkipReason string                   `json:"skip_reason,omitempty"`
	Capability semanticindex.Capability `json:"capability"`
	Run        *store.SemanticRefreshRun `json:"run,omitempty"`
	Debt       Debt                     `json:"remaining_debt"`
}

type RootExpectation struct {
	CacheDir, DatabaseID, ProfileID, GenerationID string
	SnapshotRevision, PurgeEpoch                   int64
	Dimensions                                    int
	BackendVersion                                string
}

type NativeLifecycle interface {
	semanticbuild.SegmentPayloadBuilder
	semanticbuild.StreamingSegmentPayloadBuilder
	VerifyRoot(context.Context, RootExpectation) error
}
```

Define stable error codes:

```go
const (
	ErrorBackendBroken      = "semantic_backend_broken"
	ErrorRunConflict        = "semantic_run_conflict"
	ErrorProjection         = "semantic_projection_failed"
	ErrorEmbedding          = "semantic_embedding_failed"
	ErrorEmbeddingCircuit   = "semantic_embedding_circuit_open"
	ErrorFlush              = "semantic_flush_failed"
	ErrorCompaction         = "semantic_compaction_failed"
	ErrorVerify             = "semantic_verify_failed"
	ErrorNativeRoot         = "semantic_native_root_failed"
	ErrorReadiness          = "semantic_readiness_not_ready"
	ErrorCancelled          = "semantic_refresh_cancelled"
)
```

`RefreshError` must expose bounded JSON fields `code`, `stage`, `run_id`, `checkpoint`, `readiness`, `remaining_debt`, and `message`; its cause remains available only through `Unwrap`.

- [ ] **Step 2: Prove the type tests fail**

Run:

```bash
go test ./internal/semanticrefresh -run 'Error|Result|Bounds'
```

Expected: package/types do not exist.

- [ ] **Step 3: Implement validation and bounded diagnostics**

Implement:

```go
type RefreshError struct {
	Code       string                     `json:"code"`
	Stage      store.SemanticRefreshStage `json:"stage,omitempty"`
	RunID      string                     `json:"run_id,omitempty"`
	Checkpoint string                     `json:"checkpoint,omitempty"`
	Readiness  string                     `json:"readiness,omitempty"`
	Debt       Debt                       `json:"remaining_debt"`
	Message    string                     `json:"message"`
	cause      error
}

func (e *RefreshError) Error() string
func (e *RefreshError) Unwrap() error
func NewError(code string, run store.SemanticRefreshRun, readiness string, debt Debt, cause error) *RefreshError
```

Normalize whitespace and truncate by UTF-8 bytes without producing invalid UTF-8. Enforce the same `64`/`256`/`512` limits before both storage and JSON output. Do not include `cause.Error()` separately after it has been bounded into `Message`.

- [ ] **Step 4: Write deterministic heartbeat tests**

Test an injected ticker/clock rather than sleeping five real seconds. Prove:

- one immediate progress event is written;
- active work produces another event for each `5s` tick;
- `TouchSemanticRefreshRunProgress` finishes before the callback observes the event;
- two slow callbacks never overlap;
- checkpoint events and heartbeat events use the same serialization mutex;
- callback failure cancels the derived work context and is returned once;
- stopping the emitter prevents later ticks and leaks no goroutine.

- [ ] **Step 5: Implement the serialized progress emitter**

Use:

```go
const ProgressInterval = 5 * time.Second

type ProgressLedger interface {
	TouchSemanticRefreshRunProgress(context.Context, string, time.Time) error
}

type ProgressCallback func(Progress) error
```

The emitter owns one mutex around durable touch plus callback. Heartbeats use the last fully persisted checkpoint snapshot; they may update `last_progress_at` but never claim uncommitted counters. A stage checkpoint calls the ledger CAS first, then asks the same emitter to publish the returned row. Callback errors cancel stage work through a derived context.

- [ ] **Step 6: Run focused tests**

Run:

```bash
go test ./internal/semanticrefresh -run 'Error|Result|Bounds|Progress|Heartbeat'
```

Expected: all tests pass without real-time sleeps.

- [ ] **Step 7: Commit**

```bash
git add internal/semanticrefresh/types.go \
  internal/semanticrefresh/errors.go \
  internal/semanticrefresh/progress.go \
  internal/semanticrefresh/types_test.go \
  internal/semanticrefresh/progress_test.go
git commit -m "feat(semantic): define bounded refresh reporting"
```

---

### Task 5: Build the generic durable runner and roll-forward driver

**Files:**

- Create: `internal/semanticrefresh/runner.go`
- Create: `internal/semanticrefresh/runner_test.go`

**Interfaces:**

- Consumes: Task 1 ledger APIs and Task 4 progress/error contracts.
- Produces: `Run(context.Context, RunLedger, StageExecutor, Request) (Result, error)`, which Task 6 supplies with the concrete semantic pipeline.

- [ ] **Step 1: Write failing runner tests with a fake stage executor**

Define:

```go
type Request struct {
	ProfileID                        string
	PurgeEpoch, ProjectionWatermark  int64
	Capability                       semanticindex.Capability
	Progress                         ProgressCallback
	Now                              func() time.Time
	NewRunIDFunc                     func() (string, error)
}

type StageOutcome struct {
	NextStage          store.SemanticRefreshStage
	Checkpoint         string
	EmbeddingRevision  int64
	Counters           store.SemanticRefreshCounters
	CurrentGenerationID string
	Readiness          string
	Debt               Debt
	Complete           bool
	SuccessorWatermark *int64
}

type StageExecutor interface {
	Execute(context.Context, store.SemanticRefreshRun) (StageOutcome, error)
}
```

Cover:

- a new request starts at `projection`;
- a failed/cancelled run resumes its original run ID, watermark, stage, checkpoint, embedding revision, and counters;
- each stage outcome is CAS-persisted before the next executor call;
- when a stage commits durable work and then returns an error, the non-zero `StageOutcome` is CAS-persisted before the run is marked failed;
- a stale CAS becomes `ErrorRunConflict` and never calls the next stage;
- an executor error stores `failed`, bounded error fields, current checkpoint/readiness/debt, and returns a typed error;
- parent cancellation stores `cancelled` using a `2s` `context.WithoutCancel` checkpoint context and returns `ErrorCancelled`;
- completion stores `completed` and returns the completed row;
- a successor outcome completes the old run, allocates a fresh run ID at the supplied higher watermark, carries `SuccessorRuns+1`, and continues at projection;
- a successor watermark not greater than the current watermark is rejected as a run conflict;
- progress callback failure cancels execution and is checkpointed once.

- [ ] **Step 2: Prove the runner is absent**

Run:

```bash
go test ./internal/semanticrefresh -run 'Runner|Resume|Successor|RunConflict'
```

Expected: compile failures because `Run` and runner contracts do not exist.

- [ ] **Step 3: Implement the one-unit-at-a-time runner**

Define the narrow ledger:

```go
type RunLedger interface {
	StartOrResumeSemanticRefreshRun(context.Context, store.StartSemanticRefreshRunInput) (store.SemanticRefreshRun, bool, error)
	UpdateSemanticRefreshRun(context.Context, store.SemanticRefreshRunUpdate) (store.SemanticRefreshRun, error)
	TouchSemanticRefreshRunProgress(context.Context, string, time.Time) error
}
```

The loop is:

1. start/resume the run;
2. start the immediate-plus-heartbeat emitter;
3. call exactly one `StageExecutor.Execute`;
4. persist the complete returned checkpoint with expected `version`, including a non-zero outcome returned alongside an error;
5. emit progress from the returned persisted row;
6. either call the executor again, complete and return, or complete and allocate the successor;
7. on error/cancellation, make one bounded pause update and return typed failure.

Generate opaque run IDs from 16 random bytes encoded as 32 lowercase hexadecimal characters. Tests inject `NewRunIDFunc`; production never accepts a user-provided run ID.

- [ ] **Step 4: Run runner and race tests**

Run:

```bash
go test -race ./internal/semanticrefresh -run 'Runner|Resume|Successor|RunConflict|Heartbeat'
```

Expected: all tests pass with no data race or leaked heartbeat.

- [ ] **Step 5: Commit**

```bash
git add internal/semanticrefresh/runner.go \
  internal/semanticrefresh/runner_test.go
git commit -m "feat(semantic): run durable refresh checkpoints"
```

---

### Task 6: Compose projection, embedding, flush, compaction, verification, and readiness

**Files:**

- Create: `internal/semanticrefresh/pipeline.go`
- Create: `internal/semanticrefresh/pipeline_test.go`
- Create: `internal/semanticrefresh/refresh_integration_test.go`

**Interfaces:**

- Consumes: `RunProjectionBatch`, `RunEmbedBatch`, existing `semanticbuild.Flush`, `semanticbuild.Compact`, `semanticbuild.RunVerify`, `SemanticReadinessSnapshotAt`, `semanticreadiness.Evaluate`, and `NativeLifecycle.VerifyRoot`.
- Produces: `NewPipeline(PipelineStore, PipelineOptions) (StageExecutor, error)` for the configured helper in Task 7.

- [ ] **Step 1: Write failing stage-order and fixed-bound tests**

Define:

```go
type PipelineOptions struct {
	Profile          embedding.Profile
	Provider         embedding.Provider
	Native           NativeLifecycle
	CacheDir         string
	ExactMaxChunks   int
	ProjectionBatch  int
	EmbeddingBatch   int
	Now              func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

type PipelineStore interface {
	semanticbuild.ChunkStore
	semanticbuild.EmbedStore
	semanticbuild.FlushStore
	semanticbuild.CompactionStore
	semanticbuild.VerifyStore
	ProjectionWorkRevision(context.Context) (int64, error)
	RetrievalPurgeEpoch(context.Context) (int64, error)
	RetrievalDatabaseID(context.Context) (string, error)
	RetrievalEmbeddingProfile(context.Context, string) (store.RetrievalEmbeddingProfileRow, error)
	SemanticReadinessSnapshotAt(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
}
```

Use default bounded units:

```go
const (
	DefaultProjectionBatch = 100
	DefaultEmbeddingBatch  = 16
)
```

Add tests for the exact stage chain:

```text
projection -> embedding -> flush -> compaction -> verify -> readiness
```

Assert:

- projection always receives `run.ProjectionWatermark`;
- projection remains in its stage until an empty page proves no work through the watermark;
- embedding stores the exact `EmbedBatchResult.Revision` in the next ledger outcome;
- embedding with `l0=9,500` and a selected batch of `501` calls `Flush` before the provider;
- embedding with `l0=9,500` and a selected batch of `500` does not preflush because the result cannot exceed `10,000`;
- the flush stage repeats while `L0ReadyCount > 5,000`, and each call flushes the existing `5,000` target;
- a flush failure occurs before a headroom-threatening provider call;
- compaction calls one bounded `Compact` per executor unit and remains in compaction until `SegmentCompactionNone`;
- verify uses pages of at most `5,000`, persists the returned resume chunk ID, and always sets `RepairCounters=false` so corruption fails refresh instead of being concealed by an automatic repair;
- after vector verification, an active generation must pass `NativeLifecycle.VerifyRoot`;
- no active generation skips native-root open but still evaluates readiness;
- final readiness requires ordinary `semanticreadiness.StateReady`, `L0ReadyCount <= 5,000`, no overdue compaction, and no projection watermark advance;
- a higher `ProjectionWorkRevision` returns a successor watermark instead of processing that work in the current run;
- non-ready without a higher watermark returns `ErrorReadiness` with bounded aggregate debt.

- [ ] **Step 2: Prove the pipeline is absent**

Run:

```bash
go test ./internal/semanticrefresh -run 'Pipeline|L0|Compaction|NativeRoot|Readiness'
```

Expected: compile failures because the concrete pipeline does not exist.

- [ ] **Step 3: Implement the projection and embedding handlers**

Projection handler:

```go
page, err := semanticbuild.RunProjectionBatch(ctx, p.store, semanticbuild.ProjectionBatchOptions{
	Watermark: run.ProjectionWatermark,
	Limit:     p.options.ProjectionBatch,
})
```

Increment only committed `ProjectedParents`. Persist a bounded checkpoint such as:

```text
projection:watermark=123
```

When the page is empty with no staged checkpoint and no remaining through-watermark work, advance to embedding.

Embedding handler calls `RunEmbedBatch` with:

```go
BeforeProvider: func(ctx context.Context, selected int) error {
	profile, err := p.store.RetrievalEmbeddingProfile(ctx, p.profileID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if profile.L0ReadyCount+selected <= store.RetrievalSegmentHardLimit {
		return nil
	}
	_, err = semanticbuild.Flush(ctx, p.store, p.options.Native, p.flushOptions())
	return err
}
```

The real implementation must handle the profile-not-yet-created case as zero L0 without string-matching SQL errors. Count any preflight flush in `FlushedVectors` and persist its generation ID. If no embedding candidates remain, advance to flush.

- [ ] **Step 4: Implement flush and compaction-to-idle**

Flush reads `RetrievalEmbeddingProfile`. If no profile exists, advance to compaction. If `L0ReadyCount > store.RetrievalSegmentTarget`, call:

```go
semanticbuild.Flush(ctx, p.store, p.options.Native, semanticbuild.FlushOptions{
	Profile:        p.options.Profile,
	Backend:        semanticindex.BackendUSearch,
	BackendVersion: semanticindex.USearchVersion,
	DistanceMetric: "cosine",
	CacheDir:       p.options.CacheDir,
	Limit:          store.RetrievalSegmentTarget,
})
```

Persist `GenerationID`, `SnapshotRevision`, and `FlushedVectors += 5_000`. Otherwise advance to compaction.

Compaction calls:

```go
semanticbuild.Compact(ctx, p.store, p.options.Native, semanticbuild.CompactionOptions{
	Profile:        p.options.Profile,
	Backend:        semanticindex.BackendUSearch,
	BackendVersion: semanticindex.USearchVersion,
	DistanceMetric: "cosine",
	CacheDir:       p.options.CacheDir,
})
```

If `Plan.Kind == SegmentCompactionNone`, advance to verify. Otherwise persist replacement generation and live-vector count, then remain in compaction for another bounded planner pass.

- [ ] **Step 5: Implement verification, native root proof, readiness, and successor**

Encode only the verify cursor in the bounded checkpoint. Run pages of `5,000`
until `HasMore=false`, always with `RepairCounters: false`. Counter repair stays
an explicit diagnostic/repair operation; refresh verification must detect and
return corruption rather than rewrite counters and claim success.

After the final vector page:

1. load the profile row;
2. if it has an active generation, load database ID and call `Native.VerifyRoot` with exact profile, generation, snapshot, purge epoch, dimensions, and USearch version;
3. advance to readiness only after native open/import/close succeeds.

At readiness:

1. read one authoritative `SemanticReadinessSnapshotAt`;
2. derive bounded debt fields;
3. read `ProjectionWorkRevision`;
4. if the revision is greater than the run watermark, return it as `SuccessorWatermark`;
5. otherwise require `semanticreadiness.Evaluate(snapshot).State == ready`;
6. independently require `snapshot.L0ReadyCount <= store.RetrievalSegmentTarget`;
7. return `Complete=true` only after all checks pass.

Each handler returns a stable stage error. Projection, embedding, flush, compaction, and verify failures use their matching `Error*` code; `ErrEmbedCircuitOpen` uses `ErrorEmbeddingCircuit`; active-root open/import/close failures use `ErrorNativeRoot`; and a terminal non-ready snapshot uses `ErrorReadiness`. The generic runner preserves an existing `*RefreshError` rather than wrapping it a second time.

- [ ] **Step 6: Add full interruption/resume integration coverage**

In `refresh_integration_test.go`, use `store.Open` on a temporary database for the run ledger, projection queue, embedding rows, generations, and readiness counters. Inject only a deterministic fake embedding provider and a deterministic fake `NativeLifecycle` payload/root implementation. Cover:

- cancellation after one committed projection batch resumes without reapplying that parent;
- cancellation after one committed embedding batch resumes without calling the provider again for ready chunks;
- failure after segment publication/activation resumes from current SQLite membership without duplicating the completed flush;
- a provider circuit failure leaves the run `failed`; a later successful call resumes the same run ID and reaches completion;
- a concurrent dirty revision above the watermark produces a completed first run and one successor;
- selecting a different profile marks the old resumable run `superseded`;
- all returned progress/results/errors remain bounded and contain no vectors or source text.

- [ ] **Step 7: Run pipeline and integration tests**

Run:

```bash
go test -race ./internal/semanticrefresh -run 'Pipeline|Refresh|Resume|L0|Compaction|Verify|Readiness|Successor'
```

Expected: all stage, recovery, and race tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/semanticrefresh/pipeline.go \
  internal/semanticrefresh/pipeline_test.go \
  internal/semanticrefresh/refresh_integration_test.go
git commit -m "feat(semantic): orchestrate resumable refresh"
```

---

### Task 7: Add the reusable configured refresh helper and native build-tag seam

**Files:**

- Create: `internal/app/semantic_refresh_helper.go`
- Create: `internal/app/semantic_refresh_helper_test.go`
- Create: `internal/app/semantic_refresh_native_default.go`
- Create: `internal/app/semantic_refresh_native_usearch.go`

**Interfaces:**

- Consumes: semantic configuration, `RuntimeCapability`, provider construction, writable store, Tasks 5–6 refresh runner/pipeline, and tagged USearch root loader.
- Produces: `runConfiguredSemanticRefresh`, the exact reusable helper stacked PR 3 will call after source transactions close.

- [ ] **Step 1: Write failing mode/capability gate tests**

Define a refresh-only dependency boundary instead of expanding the existing chunk/embed/status dependencies:

```go
type semanticRefreshDeps struct {
	resolve         func(string) (semanticconfig.Config, error)
	capability      func() semanticindex.Capability
	openWritable    func(string) (*store.Store, error)
	provider        func(semanticconfig.Config) (embedding.Provider, error)
	nativeLifecycle func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error)
	runRefresh      func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error)
}
```

Add:

```go
func runConfiguredSemanticRefresh(
	ctx context.Context,
	cfg config.Config,
	deps semanticRefreshDeps,
	progress semanticrefresh.ProgressCallback,
) (semanticrefresh.Result, error)
```

Test:

- `mode=off` returns `OutcomeSkipped`, reason `semantic_mode_off`, capability included, and does not call open/provider/native/run;
- `mode=shadow` plus `unsupported` returns a successful skip, reason `native_backend_unsupported`, and does not call open/provider/native/run;
- `mode=on` plus `unsupported` behaves identically;
- `supported_broken` returns `ErrorBackendBroken`, with sanitized bounded reason, and does not call open/provider/native/run;
- `supported_ready` resolves a valid profile, opens writable storage, constructs provider and native lifecycle, captures purge epoch and projection watermark, constructs the pipeline, and invokes the runner;
- provider construction happens only after mode/capability admission;
- open/provider/native errors are typed and stage-appropriate;
- configuration selecting a new profile relies on the ledger transaction to supersede the old run;
- cancellation remains an error.

- [ ] **Step 2: Prove the helper is absent**

Run:

```bash
go test ./internal/app -run 'ConfiguredSemanticRefresh|SemanticRefreshCapability'
```

Expected: compile failures for the helper and dependency fields.

- [ ] **Step 3: Implement the gate in one place**

The helper order must be:

```text
resolve config
-> inspect mode
-> inspect capability
-> skip/error decision
-> construct profile
-> open writable store
-> construct provider
-> construct native lifecycle
-> capture purge epoch and projection watermark
-> construct pipeline
-> run durable refresh
```

Do not duplicate this ordering in the Cobra command. PR 3 will call this helper directly after sync transactions and future shared maintenance leases are released.

- [ ] **Step 4: Implement build-tag-specific native lifecycle**

Default file:

```go
//go:build !usearch || !cgo

func newSemanticNativeLifecycle(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
	return nil, fmt.Errorf("native semantic lifecycle is unavailable in this build")
}
```

The default constructor is defensive only; an unsupported build must skip before calling it.

Tagged file:

```go
//go:build usearch && cgo

type usearchRefreshLifecycle struct {
	*semanticbuild.USearchSegmentBuilder
}
```

Construct the existing builder with configured dimensions. `VerifyRoot` must call:

```go
root, err := semanticindex.OpenUSearchRoot(
	expect.CacheDir,
	expect.DatabaseID,
	expect.ProfileID,
	expect.GenerationID,
	semanticindex.USearchRootExpectations{
		Index: semanticindex.USearchOptions{
			Dimensions:      expect.Dimensions,
			Connectivity:    16,
			ExpansionAdd:    128,
			ExpansionSearch: 256,
		},
		SnapshotRevision: expect.SnapshotRevision,
		PurgeEpoch:       expect.PurgeEpoch,
		BackendVersion:   expect.BackendVersion,
	},
)
if err != nil {
	return err
}
return root.Close()
```

This reuses the same manifest, checksum, payload-import, provenance, and native-open proof as normal tagged runtime admission.

- [ ] **Step 5: Run normal and tagged helper tests**

Run:

```bash
go test ./internal/app -run 'ConfiguredSemanticRefresh|SemanticRefreshCapability'
```

Then:

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -tags usearch ./internal/app -run 'ConfiguredSemanticRefresh|NativeLifecycle'
```

Expected: default tests prove no unsupported construction; tagged tests prove real builder/root verification wiring.

- [ ] **Step 6: Commit**

```bash
git add internal/app/semantic_refresh_helper.go \
  internal/app/semantic_refresh_helper_test.go \
  internal/app/semantic_refresh_native_default.go \
  internal/app/semantic_refresh_native_usearch.go
git commit -m "feat(semantic): configure manual refresh"
```

---

### Task 8: Add `semantic refresh`, bounded output, and latest-run status

**Files:**

- Create: `internal/app/semantic_refresh.go`
- Create: `internal/app/semantic_refresh_test.go`
- Modify: `internal/app/semantic.go`
- Modify: `internal/app/semantic_output.go`
- Modify: `internal/semanticbuild/status.go`
- Create: `internal/semanticbuild/status_refresh_test.go`

**Interfaces:**

- Consumes: Task 7 configured helper and `Store.LatestSemanticRefreshRun`.
- Produces: `dbrain semantic refresh [--max-duration D] [--json]`, bounded progress/final/error output, and `semantic status.latest_run`.

- [ ] **Step 1: Write failing CLI behavior tests**

Register:

```go
func newSemanticRefreshCommand(root *rootOptions, deps semanticRefreshDeps) *cobra.Command
```

Test:

- `semantic refresh` accepts no positional arguments;
- `--max-duration=0` is unlimited and a negative duration is rejected;
- mode off exits zero with `Semantic refresh: skipped reason=semantic_mode_off`;
- unsupported exits zero with `Semantic refresh: skipped reason=native_backend_unsupported`;
- broken exits non-zero with the typed stable code;
- supported completion prints capability, run ID, stage counts, profile/generation, indexed/L0/tombstone/segment debt, final readiness, and elapsed time;
- failure prints run ID, stage, checkpoint, stable code, readiness, and aggregate debt;
- progress is written to stderr so `--json` stdout remains one valid bounded JSON document;
- timeout/cancellation returns non-zero and the stored latest run is `cancelled`;
- no output includes vectors, source text, provider bodies, or filesystem paths.

- [ ] **Step 2: Write failing latest-run status tests**

Extend:

```go
type Status struct {
	// existing fields
	LatestRun *store.SemanticRefreshRun `json:"latest_run"`
}
```

Extend `StatusStore` with:

```go
LatestSemanticRefreshRun(context.Context, string) (*store.SemanticRefreshRun, error)
```

Cover:

- configured status reads the latest run for the selected profile;
- off/unconfigured status reads the database-latest run with an empty profile filter when storage is available;
- no ledger rows encode `"latest_run": null`;
- a resumable run includes state/stage/checkpoint/last progress/error code;
- the object is bounded and contains no history array;
- store unavailability preserves existing diagnostic behavior.

- [ ] **Step 3: Prove command/status tests fail**

Run:

```bash
go test ./internal/app ./internal/semanticbuild -run 'SemanticRefreshCommand|SemanticStatus.*LatestRun|ReadStatus.*Run'
```

Expected: refresh is not registered and status has no latest-run field.

- [ ] **Step 4: Implement the Cobra command**

The command:

1. loads writable config;
2. creates a child context only when `--max-duration > 0`;
3. passes a progress callback to `runConfiguredSemanticRefresh`;
4. writes every progress event through `writeSemanticRefreshProgress(cmd.ErrOrStderr(), progress)`;
5. writes the final human result or one JSON result to stdout;
6. if a `*semanticrefresh.RefreshError` occurs with `--json`, writes that bounded object to stdout and returns `&ExitError{Code: 1, Err: err, Silent: true}`;
7. otherwise returns the typed error normally.

Do not add `--until-idle`; refresh always runs through successor watermarks until ready, skipped, failed, or cancelled.

- [ ] **Step 5: Implement status latest-run loading and rendering**

`ReadStatus` calls `LatestSemanticRefreshRun` after profile resolution/readiness. A missing row is not an error. The app status command should attempt a read-only store even when mode is off or the profile is incomplete so an earlier failed/resumable run remains visible.

Human status adds at most two lines:

```text
Refresh: run=<id> state=<state> stage=<stage> watermark=<n> embedding_revision=<n> last_progress=<RFC3339> readiness=<state>
Refresh error: code=<code> checkpoint=<checkpoint>
```

Omit the error line when there is no code. Do not print the stored error text twice; the ordinary status reason and stable code are sufficient.

- [ ] **Step 6: Run CLI and status tests**

Run:

```bash
go test ./internal/app ./internal/semanticbuild -run 'SemanticRefresh|SemanticStatus|ReadStatus'
```

Expected: command, output, cancellation, JSON, and status latest-run cases pass.

- [ ] **Step 7: Commit**

```bash
git add internal/app/semantic_refresh.go \
  internal/app/semantic_refresh_test.go \
  internal/app/semantic.go \
  internal/app/semantic_output.go \
  internal/semanticbuild/status.go \
  internal/semanticbuild/status_refresh_test.go
git commit -m "feat(semantic): expose resumable refresh command"
```

---

### Task 9: Document the stacked PR 2 operator contract

**Files:**

- Modify: `README.md`
- Modify: `docs/research-harness.md`
- Modify: `CHANGELOG.md`

**Interfaces:**

- Consumes: the final CLI/status behavior from Task 8.
- Produces: accurate user-facing documentation that does not overstate automatic sync, locking, packaging, or installed acceptance.

- [ ] **Step 1: Update the README command and limitation text**

Document:

```bash
dbrain semantic status
dbrain semantic refresh
dbrain semantic refresh --max-duration 30m
dbrain semantic refresh --json
```

State exactly:

- refresh uses the configured profile and does not change semantic mode;
- `off` and unsupported builds skip successfully;
- a broken supported backend errors;
- supported enabled refresh runs to `ready` or returns a typed failure;
- cancellation/failure preserves a resumable run;
- later invocations resume automatically;
- this stacked PR does not invoke refresh from sync;
- normal untagged/CGO-free artifacts remain unsupported until the later distribution stack.

Do not change `research.semantic.index_backend` or claim Homebrew support.

- [ ] **Step 2: Update research-harness operations**

Replace the old implication that operators must manually sequence chunk/embed/verify for this stack. Keep those commands as diagnostics, but identify `semantic refresh` as the composed manual/emergency path and explain fixed watermark plus successor runs.

Include the stable error fields and status `latest_run` object. State that queries never trigger maintenance.

- [ ] **Step 3: Add a dated changelog entry**

Add one concise entry under the current dated heading:

```text
- **Resumable semantic refresh**: Added a migration-backed per-profile refresh ledger, fixed-watermark projection and revision-checkpointed embedding batches, bounded provider retry, L0/compaction/verification/readiness orchestration, serialized progress, typed failures, `semantic refresh`, and latest-run status. Automatic post-sync refresh, cross-process locks, release packaging, and installed-corpus activation remain in later stacked PRs.
```

- [ ] **Step 4: Inspect documentation diff**

Run:

```bash
git diff --check
git diff -- README.md docs/research-harness.md CHANGELOG.md
```

Expected: no whitespace errors and no claim that this PR wires sync, ships Homebrew native support, or activates production.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/research-harness.md CHANGELOG.md
git commit -m "docs(semantic): explain resumable refresh"
```

---

### Task 10: Run standard, tagged, and scope gates

**Files:**

- Verify only; modify a file only to fix a failure caused by this plan's implementation.

**Interfaces:**

- Consumes: all prior tasks.
- Produces: evidence that default builds remain portable, tagged builds execute the native refresh seam, and stacked PR 2 did not absorb PRs 3–6.

- [ ] **Step 1: Run all focused packages with race detection**

Run:

```bash
go test -race ./internal/store ./internal/semanticbuild ./internal/semanticrefresh ./internal/app
```

Expected: pass.

- [ ] **Step 2: Run standard repository gates**

Run:

```bash
task fmt
task lint
task test-ci
task build
```

Expected: all pass. `task test-ci` remains the authoritative clean-environment gate.

- [ ] **Step 3: Prove the normal binary is unsupported and skips without writes**

Create an isolated root:

```bash
SMOKE_ROOT="$(mktemp -d /private/tmp/dbrain-pr2-refresh-default.XXXXXX)"
DBRAIN_ROOT="$SMOKE_ROOT" ./bin/dbrain --no-debug semantic refresh --json
DBRAIN_ROOT="$SMOKE_ROOT" ./bin/dbrain --no-debug semantic status --json
```

Expected:

- refresh returns a bounded successful skip (`semantic_mode_off`);
- status reports `backend_capability.state=unsupported`;
- no provider or native-library error appears.

Do not point this smoke test at the configured development or production DB.

- [ ] **Step 4: Run tagged USearch tests**

Run:

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -race -tags usearch \
    ./internal/semanticindex \
    ./internal/semanticbuild \
    ./internal/semanticrefresh \
    ./internal/app
```

Expected: pass, including capability self-check, real builder construction, root import/close, and configured-helper wiring.

- [ ] **Step 5: Build the tagged development binary**

Run:

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  go build -tags usearch -o ./bin/dbrain-usearch-dev ./cmd/dbrain
```

Then run only an isolated off-mode smoke:

```bash
TAGGED_SMOKE_ROOT="$(mktemp -d /private/tmp/dbrain-pr2-refresh-tagged.XXXXXX)"
DBRAIN_ROOT="$TAGGED_SMOKE_ROOT" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  ./bin/dbrain-usearch-dev --no-debug semantic refresh --json
```

Expected: bounded `semantic_mode_off` skip. This is not the Homebrew/static-link/install acceptance gate.

- [ ] **Step 6: Audit stacked-scope boundaries**

Run:

```bash
git diff --stat codex/semantic-ann-lifecycle...HEAD
git diff --name-only codex/semantic-ann-lifecycle...HEAD
rg -n 'runConfiguredSemanticRefresh|semantic refresh' internal/app
rg -n 'post-sync|postSync|runConfiguredSemanticRefresh' internal/app/sync*.go
rg -n 'runlock|maintenance lock|generation lock' internal/semanticrefresh internal/app/semantic_refresh*.go
```

Expected:

- the helper and manual command exist;
- no sync command calls the helper;
- no new runlock/maintenance/generation lock implementation exists;
- no Homebrew/release workflow files changed;
- no production or installed-corpus artifact was created.

- [ ] **Step 7: Inspect the final diff**

Run:

```bash
git status --short
git diff --check
git diff --stat
```

Expected: no uncommitted implementation changes and no whitespace errors. If a gate fails, return to the task that owns the failing file, make the smallest TDD fix there, rerun that task's focused command, and use that task's explicit `git add` list before creating a new narrowly named fix commit. Do not squash the task commits before review. Do not push, open a PR, merge, deploy, package, or activate production as part of this plan.

---

## Completion Criteria

- Migration `25` (or the next revalidated unused version) durably stores one CAS-versioned resumable run per profile.
- A resumed run retains its run ID, purge epoch, fixed projection watermark, embedding revision, stage, checkpoint, counters, and current generation.
- Above-watermark writes always roll into a successor run; the current run never advances its watermark.
- Projection and embedding execute through bounded one-batch seams.
- Retryable provider calls are bounded to three attempts with `1s` exponential backoff capped at `30s`.
- Provider/persistence batches never exceed `5,000`.
- No provider call can push exact L0 above `10,000`; completed refresh ends with L0 at or below `5,000`.
- Existing immutable flush, bounded compaction, vector verification, readiness, and tagged native-root verification are composed rather than reimplemented.
- Refresh verification never repairs counters automatically; inconsistent counters or vectors produce a typed failure and leave explicit repair to the existing diagnostic command.
- Progress is serialized, bounded, durable-first, and emitted at least every `5s`.
- Cancellation and every stage failure leave a resumable row plus a bounded typed error.
- `semantic refresh` and `runConfiguredSemanticRefresh` share one configuration/capability gate.
- `off` and `unsupported` skip; `supported_broken` errors; `supported_ready` plus `shadow`/`on` executes.
- `semantic status` includes one bounded latest-run object.
- No sync wiring, cross-process locks, packaging, installed acceptance, or production activation enters this PR.
- Focused, standard, and tagged gates pass, and user-facing docs/changelog describe only the behavior actually added.
