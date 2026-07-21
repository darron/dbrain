# Semantic Foundation Correctness And Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make projection, chunk identity, embedding persistence, progress, and runtime admission correct and bounded on the restored production corpus before introducing ANN segments.

**Architecture:** SQLite gains a durable parent projection ledger, monotonic work/profile revisions, occurrence metadata, bounded giant-parent staging, and aggregate profile state. Projection v2 and chunker v3 produce stable content-local identities. One shared readiness evaluator gates both CLI status and runtime construction before any query embedding; until the segmented-index plan lands, a complete large profile remains `needs_index` and lexical retrieval is unchanged.

**Tech Stack:** Go 1.25, `database/sql`, modernc SQLite, Cobra, existing Ollama embedding boundary, SHA-256 length-prefixed encodings, table-driven Go tests.

## Global Constraints

- SQLite remains the sole authoritative working database; chunks, embeddings, ledgers, and later ANN files are derived state.
- Use additive migration **16** named `retrieval_semantic_foundation_v2` for the
  foundation tables and constraints. Because migration 16 was exercised before
  authoritative dirtying triggers were added, install and repair those triggers
  with append-only migration **17** named
  `retrieval_projection_dirty_triggers`; never fold the new trigger identity
  into migration 16. Because migration 17 was then exercised with raw
  `content_hash` in its update-trigger identity, append-only migration **18**
  named `retrieval_projection_dirty_trigger_provenance_repair` repairs the
  triggers to treat that hash as provenance-only; never reinterpret migration
  17 in place. Migration 18 also gives all existing ledger parents one shared
  new pending revision, clears partial projection staging, and marks index
  generations stale/inactive while preserving raw evidence, chunks, and
  embeddings for normal maintenance.
- Projection identity is exactly `retrieval-projection-v2`; chunk identity is exactly `retrieval-chunker-v3`.
- Chunker v3 hard UTF-8 ceiling is 1,800 bytes; emitted text must also obey existing rune bounds, preserve exact rune offsets, never be blank after trimming, and always make forward progress.
- Stable chunk identity excludes absolute offsets, whole-parent hashes, whole-section hashes, and occurrence number.
- Identical embedded windows within one parent section share one chunk/embedding; every location is retained in `retrieval_chunk_occurrences`.
- One-byte edits at least one hard ceiling from a section edge may change no more than eight chunk identities.
- Giant staging starts when one batch reaches 1,000 unique chunks, 1,000
  occurrences, or 4 MiB of staged JSON. One parent hard-blocks above 50,000
  unique chunks, 200,000 occurrences, or 128 MiB of staged chunk/occurrence
  JSON with reason `projection_too_large_for_flat_retrieval`.
- Embedding provider requests and persistence batches never exceed 5,000 rows. One persistence batch gets one monotonic profile revision and commits atomically.
- Normal embedding work never scans the full ready profile and final progress output is constant-memory.
- `ready` requires the approved 99.9/0.1 coverage gates and, eventually, a valid root. `catching_up` permits at most 500 dirty parents, 2,500 estimated chunks, 30 minutes oldest dirtiness, 10,000 L0 rows, and two percent tombstones.
- This plan does not implement ANN segments. Profiles above the exact cap with otherwise complete foundation state report `needs_index`; semantic runtime fails open before calling the query provider.
- Semantic mode remains off by default. No command, migration, sync, research, MCP, or web request starts a backfill implicitly.
- Preserve raw evidence, lexical ordering, protected anchors, RRF semantics, shadow isolation, and `CGO_ENABLED=0` compatibility.
- Code changes end with `task fmt`, `task lint`, `task test-ci`, and `task build`.

---

### Task 1: Add The V2 Retrieval Foundation Schema

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/schema_identity.go`
- Modify: `internal/store/retrieval_schema.go`
- Modify: `internal/store/migrations_test.go`
- Modify: `internal/store/retrieval_store_test.go`
- Modify: `docs/schema-migrations.md`

**Interfaces:**
- Consumes: existing v13-v15 retrieval tables without deleting v2 chunks or partial embeddings.
- Produces: migration 16 tables/columns used by every later task in this plan.

- [ ] **Step 1: Re-scan append-only migration history**

Run:

```bash
git log --all --oneline -G 'currentSchemaVersion.*16|Version:[[:space:]]*16|MigrationVersion.*16' -- internal/store/migrations.go internal/store/retrieval_schema.go
```

Expected: no output. If output exists, use the next globally unused version and update this plan's migration number before editing code.

- [ ] **Step 2: Write failing fresh/existing/repair migration tests**

Add tests named:

```go
func TestSemanticFoundationMigrationCreatesV2TablesAndColumns(t *testing.T)
func TestSemanticFoundationMigrationSeedsEveryEligibleParentPending(t *testing.T)
func TestSemanticFoundationMigrationRepairsExistingMetadataIdempotently(t *testing.T)
func TestOpenReadOnlyPreSemanticFoundationDoesNotWrite(t *testing.T)
```

Assert the exact tables `retrieval_state`, `retrieval_parent_projections`,
`retrieval_chunk_occurrences`, `retrieval_projection_staging`,
`retrieval_embedding_profiles`; chunk columns `section_key`, `heading_hash`,
`derived`; embedding columns `revision`, `vector_hash`; and required unique
indexes. Simulate an existing v15 database with v2 chunks and prove they remain.

- [ ] **Step 3: Verify RED**

Run:

```bash
go test ./internal/store -run 'TestSemanticFoundationMigration|TestOpenReadOnlyPreSemanticFoundation' -count=1
```

Expected: FAIL because migration 16 and the new schema do not exist.

- [ ] **Step 4: Implement migration 16 and repair-safe schema creation**

Use these logical row contracts:

```go
type RetrievalProjectionStatus string

const (
	RetrievalProjectionPending RetrievalProjectionStatus = "pending"
	RetrievalProjectionCurrent RetrievalProjectionStatus = "current"
	RetrievalProjectionEmpty   RetrievalProjectionStatus = "empty"
	RetrievalProjectionBlocked RetrievalProjectionStatus = "blocked"
	RetrievalProjectionError   RetrievalProjectionStatus = "error"
)

type RetrievalEmbeddingProfileRow struct {
	ProfileID, ActiveGenerationID string
	LatestRevision, PurgeEpoch, ActiveSnapshotRevision int64
	ActiveIndexedCount, L0ReadyCount, ActiveTombstoneCount int
}
```

`retrieval_state` contains one row with stable `database_id`,
`projection_work_revision`, and `purge_epoch`. Generate `database_id` once on
writable migration and preserve it on reopen/backup. Seed eligible item/source
parents as pending with one allocated work revision. Do not modify evidence or
delete legacy derived rows.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
go test -race ./internal/store -run 'TestSemanticFoundationMigration|TestOpenReadOnlyPreSemanticFoundation' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations.go internal/store/schema_identity.go internal/store/retrieval_schema.go internal/store/migrations_test.go internal/store/retrieval_store_test.go docs/schema-migrations.md
git commit --no-gpg-sign -m "feat: add semantic foundation schema"
```

---

### Task 2: Implement Projection V2 And Chunker V3

**Files:**
- Modify: `internal/retrievalchunk/types.go`
- Modify: `internal/retrievalchunk/projection.go`
- Modify: `internal/retrievalchunk/identity.go`
- Modify: `internal/retrievalchunk/chunker.go`
- Modify: `internal/retrievalchunk/projection_test.go`
- Modify: `internal/retrievalchunk/chunker_test.go`
- Modify: `internal/retrievalchunk/chunk_test.go`
- Create: `internal/retrievalchunk/corpus_fixture_test.go`
- Create: `cmd/devtools/semantic_profile_bakeoff/main.go`
- Create: `cmd/devtools/semantic_profile_bakeoff/main_test.go`

**Interfaces:**
- Consumes: authoritative item/source model rows.
- Produces: `Projection` with parent/section hashes plus unique chunks and occurrence offsets for Task 3.

- [ ] **Step 1: Write failing identity, byte-limit, and local-churn tests**

Define the public result shape in tests:

```go
type Occurrence struct {
	ChunkID, SectionKey string
	StartChar, EndChar int
}

type Projection struct {
	ParentHash string
	Chunks []Chunk
	Occurrences []Occurrence
}
```

Add cases proving stable section keys, duplicate-key rejection, length-prefixed
hash sensitivity, 1,800-byte ceiling, exact trimmed rune offsets, no whitespace
tails, duplicate-window dedupe, moved-window identity reuse, heading-sensitive
identity, deterministic output, and at most eight changed IDs after a distant
one-byte edit. Build a synthetic 26,512-window fixture without checking private
text into git.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/retrievalchunk -run 'V3|ProjectionV2|Occurrence|LocalEdit|ProductionOutlier' -count=1
```

Expected: FAIL because v2 uses parent content hash and ordinal identity and has no byte ceiling or occurrences.

- [ ] **Step 3: Implement the v2/v3 contracts**

Use exact version constants and fields:

```go
const (
	Version = "retrieval-chunker-v3"
	ProjectionVersion = "retrieval-projection-v2"
	MaxUTF8Bytes = 1800
)

type Section struct {
	Key, Role, Heading, Text string
	Derived bool
}

type Chunk struct {
	ID, ParentKind, ParentSourceKey string
	SectionKey, EvidenceRole string
	Heading string
	HeadingHash, TextHash string
	Derived bool
	ProjectionVersion, ChunkerVersion string
	Text string
}

func BuildProjection(parent Parent, opts Options) (Projection, error)
func Build(parent Parent, opts Options) ([]Chunk, error)
```

Use length-prefixed SHA-256 for parent, section, heading, and chunk identities.
Use paragraph/sentence-preferred content-defined cut points with a rolling-hash
fallback and a maximum re-synchronization distance of one hard byte ceiling.
Store offsets only in occurrences. Identical `(section key, role, derived,
heading hash, text hash)` windows share one chunk.

Keep `Build` as a compatibility wrapper returning `BuildProjection(...).Chunks`
so this task compiles independently. Task 3 switches the projection worker to
`BuildProjection` when occurrence persistence lands.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test -race ./internal/retrievalchunk -count=1
```

Expected: PASS.

- [ ] **Step 5: Add and run the production-derived profile bakeoff**

The devtool opens an explicit SQLite path read-only, projects without writing,
reports chunk count/byte distribution/context failures, and compares
`embeddinggemma:300m-bf16` at 768 dimensions with the same model's supported
384-dimensional Matryoshka output. It refuses a live production XDG path and
never persists vectors. Add deterministic HTTP/provider tests, then run:

```bash
go test ./cmd/devtools/semantic_profile_bakeoff -count=1
go run ./cmd/devtools/semantic_profile_bakeoff --db /Users/darron/src/dbrain/data/brain.db --model embeddinggemma:300m-bf16 --dimensions 768,384 --max-bytes 1800 --report /private/tmp/dbrain-semantic-profile-bakeoff.json
```

Expected: both supported dimensions return correctly sized finite L2 vectors,
every projected window is at most 1,800 UTF-8 bytes, and context-limit failures
are zero. If 384 dimensions are rejected by the installed model, record that as
an unsupported candidate rather than silently selecting it; 768 remains the
foundation profile pending the production evaluation plan.

- [ ] **Step 6: Commit**

```bash
git add internal/retrievalchunk cmd/devtools/semantic_profile_bakeoff
git commit --no-gpg-sign -m "feat: add stable semantic chunker v3"
```

---

### Task 3: Add Dirty Revisions And Atomic Projection Apply

**Files:**
- Modify: `internal/store/retrieval_projection.go`
- Modify: `internal/store/retrieval_chunks.go`
- Create: `internal/store/retrieval_parent_state.go`
- Modify: `internal/store/retrieval_store_test.go`
- Modify: `internal/semanticbuild/chunk.go`
- Modify: `internal/semanticbuild/run_test.go`

**Interfaces:**
- Consumes: `retrievalchunk.Projection` from Task 2 and migration tables from Task 1.
- Produces: revision-aware dirty selector and atomic `ApplyRetrievalProjection`.

- [ ] **Step 1: Write failing store tests**

Add tests for monotonic dirty revisions, deterministic selection through a
watermark, durable empty parents, atomic chunk/occurrence replacement, unchanged
identity reuse, embedding deletion only for obsolete identities, rollback, and
an old apply losing a race to a same-timestamp newer dirty revision.

Use this exact API in tests:

```go
type RetrievalParentWork struct {
	Parent retrievalchunk.Parent
	DirtyRevision int64
}

type ApplyRetrievalProjectionInput struct {
	ParentKind, ParentSourceKey string
	DirtyRevision int64
	Projection retrievalchunk.Projection
	Status RetrievalProjectionStatus
	Reason string
}

func (s *Store) ProjectionWorkRevision(context.Context) (int64, error)
func (s *Store) ListDirtyRetrievalParents(context.Context, int64, int) ([]RetrievalParentWork, error)
func (s *Store) ApplyRetrievalProjection(context.Context, ApplyRetrievalProjectionInput) (ChunkReplaceResult, error)
```

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/store ./internal/semanticbuild -run 'Projection|DirtyRevision|EmptyParent|Occurrence' -count=1
```

Expected: FAIL for missing ledger APIs and atomic occurrence persistence.

- [ ] **Step 3: Implement revision-aware projection selection and apply**

Allocate work revisions inside the same SQLite transaction that dirties a
parent. `ApplyRetrievalProjection` must validate current parent eligibility,
computed parent hash, and exact selected `dirty_revision`; replace chunks and
occurrences; retain unchanged embeddings; record obsolete indexed entries for
later tombstones; then set `projected_revision` and terminal state. Return a
typed stale-work error without clearing newer work.

Update `RunChunk` to capture watermark `W`, page only work with
`dirty_revision <= W`, persist `empty/no_chunkable_content`, and keep progress
bounded to aggregate totals plus the last sample.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test -race ./internal/store ./internal/semanticbuild -run 'Projection|DirtyRevision|EmptyParent|Occurrence' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/retrieval_projection.go internal/store/retrieval_chunks.go internal/store/retrieval_parent_state.go internal/store/retrieval_store_test.go internal/semanticbuild/chunk.go internal/semanticbuild/run_test.go
git commit --no-gpg-sign -m "feat: persist semantic projection state"
```

---

### Task 4: Dirty Every Projected Authoritative Mutation

**Files:**
- Modify: `internal/store/retrieval_schema.go`
- Create: `internal/store/retrieval_dirty.go`
- Modify: `internal/store/item_write.go`
- Modify: `internal/store/item_enrichment.go`
- Modify: `internal/store/source_schema.go`
- Modify: `internal/store/retrieval_embeddings.go`
- Modify: `internal/store/retrieval_chunks.go`
- Modify: `internal/store/retrieval_store_test.go`
- Modify: `internal/store/item_enrichment_test.go`
- Modify: `internal/store/sources_test.go`

**Interfaces:**
- Consumes: projection ledger from Task 3.
- Produces: database-trigger coverage floor and immediate exclusion of non-current parents.

Migration 17 owns the historical dirty-trigger definitions and migration 18
owns the provenance correction. Read-only schema identity requires the
foundation constraints only when migration 16 is present, validates the
historical trigger definitions for a genuine v17 database, and validates the
corrected definitions when migration 18 is present. A genuine v15 database
must report semantic retrieval unavailable before any selector joins the v16
projection ledger.

- [ ] **Step 1: Write failing mutation/exclusion tests**

Prove projected item/source fields and summary/OCR/transcript enrichment
insert/update/delete each allocate a newer dirty revision in the authoritative
transaction. Prove irrelevant counters/timestamps do not dirty. After dirtying,
prove ready-vector listing, exact search inputs, and hydration exclude the
parent before rechunking. Prove deleted or newly ineligible parents remain
selectable for derived cleanup.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/store -run 'Dirty|ProjectedMutation|PendingParent|IneligibleParent' -count=1
```

Expected: FAIL because no authoritative dirtying triggers or pending-parent filters exist.

- [ ] **Step 3: Implement triggers and named helpers**

Preserve the exercised migration 17 item/source/enrichment trigger identity,
then add repair-safe migration 18 trigger definitions whose `WHEN` clauses
compare only projected fields/roles. Raw `content_hash` is provenance, not a
projection input, and a hash-only recalculation must not dirty a parent.
Triggers increment `retrieval_state` and upsert the ledger as `pending`.
Provide:

```go
func MarkRetrievalParentDirtyTx(ctx context.Context, tx *sql.Tx, kind, sourceKey string) (int64, error)
```

Keep named store calls where the caller needs the allocated revision, but do not
depend on callers for coverage. Join `retrieval_parent_projections` with status
`current` in every semantic vector selector and hydrator.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test -race ./internal/store -run 'Dirty|ProjectedMutation|PendingParent|IneligibleParent' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/retrieval_schema.go internal/store/retrieval_dirty.go internal/store/item_write.go internal/store/item_enrichment.go internal/store/source_schema.go internal/store/retrieval_embeddings.go internal/store/retrieval_chunks.go internal/store/retrieval_store_test.go internal/store/item_enrichment_test.go internal/store/sources_test.go
git commit --no-gpg-sign -m "feat: dirty semantic projections atomically"
```

---

### Task 5: Stage And Resume Giant Parent Projection

**Files:**
- Create: `internal/store/retrieval_projection_staging.go`
- Modify: `internal/store/retrieval_projection.go`
- Modify: `internal/store/retrieval_store_test.go`
- Modify: `internal/retrievalchunk/chunker.go`
- Modify: `internal/retrievalchunk/chunker_test.go`
- Modify: `internal/semanticbuild/chunk.go`
- Modify: `internal/semanticbuild/run_test.go`

**Interfaces:**
- Consumes: streaming v3 section windows and atomic apply.
- Produces: resumable non-searchable staging with bounded chunk, occurrence,
  byte, and boundary-planning costs.

- [ ] **Step 1: Write failing interruption/resume tests**

Use a fake clock/deadline to stop after two staged batches. Assert the checkpoint
contains work ID, dirty revision, section key, and next boundary; no staged row
is searchable; resume advances rather than restarting; completion promotes once;
redirty discards stale staged work; planning runs once and its opaque versioned
plan survives restart; fabricated, altered, missing, or extra staging cannot be
promoted; and exceeding 50,000 unique chunks, 200,000 occurrences, or 128 MiB
produces terminal blocked state with no searchable partial chunks. Also prove a
loaded complete checkpoint cannot bypass those limits and `MaxDuration` is
checked between ordinary parents.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/retrievalchunk ./internal/store ./internal/semanticbuild -run 'Giant|Staging|Resume|ProjectionTooLarge' -count=1
```

Expected: FAIL because chunking is all-in-memory and staging APIs are absent.

- [ ] **Step 3: Implement streaming windows and staging**

Expose:

```go
type Cursor struct { SectionKey string; NextBoundary int }
func Stream(parent Parent, opts Options, cursor Cursor, emit func(Chunk, Occurrence) error) (Cursor, bool, error)
```

Persist batches keyed by `(work_id, dirty_revision)`. Prepare section boundary
state once, persist it as hidden versioned metadata, and resume directly from
that plan without re-planning the giant section on every batch or restart.
Exclude metadata from staged counts, JSON progress, and promotion. Promote only
after independently streaming the current authoritative projection and proving
exact equality with every staged chunk and occurrence, then revalidate the
complete parent hash and selected revision in one apply transaction. Cleanup
abandoned/stale work explicitly. `RunChunk` accepts `MaxDuration` and returns a
durable resume checkpoint without accumulating batch history.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test -race ./internal/retrievalchunk ./internal/store ./internal/semanticbuild -run 'Giant|Staging|Resume|ProjectionTooLarge' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/retrieval_projection_staging.go internal/store/retrieval_projection.go internal/store/retrieval_store_test.go internal/retrievalchunk/chunker.go internal/retrievalchunk/chunker_test.go internal/semanticbuild/chunk.go internal/semanticbuild/run_test.go
git commit --no-gpg-sign -m "feat: resume giant semantic projections"
```

---

### Task 6: Persist Atomic Embedding Batches And Add Explicit Verify

**Files:**
- Modify: `internal/store/retrieval_embeddings.go`
- Create: `internal/store/retrieval_embedding_profiles.go`
- Create: `internal/store/retrieval_vectors.go`
- Modify: `internal/store/retrieval_store_test.go`
- Modify: `internal/semanticbuild/embed.go`
- Create: `internal/semanticbuild/verify.go`
- Modify: `internal/semanticbuild/run_test.go`
- Modify: `internal/app/semantic.go`
- Modify: `internal/app/semantic_output.go`
- Modify: `internal/app/semantic_output_test.go`
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Consumes: current v3 chunks and embedding-profile state.
- Produces: one-revision atomic batch writes, lean vector rows, bounded verify, circuit breaker, constant-size progress.

- [ ] **Step 1: Write failing batch/verify/progress tests**

Use this exact store API:

```go
type PutRetrievalEmbeddingBatchInput struct {
	Profile embedding.Profile
	Rows []RetrievalEmbeddingRow
	ExpectedPurgeEpoch int64
}

func (s *Store) PutRetrievalEmbeddingBatch(context.Context, PutRetrievalEmbeddingBatchInput) (int64, error)
func (s *Store) ListRetrievalVectors(context.Context, string, VectorPage) ([]RetrievalVectorRow, error)
```

Test whole-batch rollback, one shared revision, monotonic later revision, current
chunk-hash race, vector hash, no full-profile read during normal embed, provider
request/persistence cap 5,000, three consecutive retryable failures stopping
without touching unattempted rows, paged corruption verify, and final JSON with
only `snapshot_count`, `snapshots_truncated`, and last sample.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/store ./internal/semanticbuild ./internal/app -run 'EmbeddingBatch|SemanticVerify|CircuitBreaker|BoundedProgress' -count=1
```

Expected: FAIL because writes are per-row, normal embed scans all ready vectors, and verify is absent.

- [ ] **Step 3: Implement atomic batch persistence and bounded maintenance**

Validate every row before opening the write transaction, then revalidate current
hashes/profile/epoch inside it, allocate one `latest_revision`, write all rows,
update aggregate counters, and commit. Retain `PutRetrievalEmbedding` as a
one-row wrapper. Replace `quarantineCorruptReady` with explicit paged
`semantic verify --limit <rows> --resume` and never call it from ordinary embed.
Persist provider failure rows in one batch. Keep only the latest progress sample.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test -race ./internal/store ./internal/semanticbuild ./internal/app -run 'EmbeddingBatch|SemanticVerify|CircuitBreaker|BoundedProgress' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/retrieval_embeddings.go internal/store/retrieval_embedding_profiles.go internal/store/retrieval_vectors.go internal/store/retrieval_store_test.go internal/semanticbuild/embed.go internal/semanticbuild/verify.go internal/semanticbuild/run_test.go internal/app/semantic.go internal/app/semantic_output.go internal/app/semantic_output_test.go internal/app/app_test.go
git commit --no-gpg-sign -m "feat: batch and verify semantic embeddings"
```

---

### Task 7: Share One Readiness Evaluator Between Status And Runtime

**Files:**
- Create: `internal/semanticreadiness/readiness.go`
- Create: `internal/semanticreadiness/readiness_test.go`
- Modify: `internal/store/retrieval_projection.go`
- Modify: `internal/store/retrieval_embedding_profiles.go`
- Modify: `internal/semanticbuild/status.go`
- Modify: `internal/semanticbuild/run_test.go`
- Modify: `internal/brainresearch/runtime.go`
- Modify: `internal/brainresearch/runtime_test.go`
- Modify: `internal/brainresearch/strategy_evidence.go`
- Modify: `internal/brainresearch/strategy_evidence_test.go`
- Modify: `internal/app/semantic.go`
- Modify: `internal/app/semantic_output.go`
- Modify: `internal/app/semantic_output_test.go`

**Interfaces:**
- Consumes: one immutable readiness snapshot from store.
- Produces: pure state/reason evaluation used before provider construction and by CLI status.

- [ ] **Step 1: Write the failing readiness matrix**

Define:

```go
type State string
const (
	StateNotConfigured State = "not_configured"
	StateDisabled State = "disabled"
	StateNeedsProjection State = "needs_projection"
	StateNeedsEmbeddings State = "needs_embeddings"
	StateRetryScheduled State = "retry_scheduled"
	StateNeedsIndex State = "needs_index"
	StateBuilding State = "building"
	StateCatchingUp State = "catching_up"
	StateDegradedBlocked State = "degraded_blocked"
	StateStale State = "stale"
	StateCorrupt State = "corrupt"
	StateReady State = "ready"
	StateUnavailable State = "unavailable"
)

type Decision struct { State State; Reason string; Searchable bool }
func Evaluate(Snapshot) Decision
```

Table-test every priority and numeric boundary: 99.9/0.1 coverage, 500/501
dirty parents, 2,500/2,501 estimated chunks, 30-minute age, 5,000/10,000 L0,
one/two-percent tombstones, due versus scheduled retry, corrupt/unclassified
error, absent/invalid root, and small exact profile versus large `needs_index`.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/semanticreadiness ./internal/semanticbuild ./internal/brainresearch -run 'Readiness|RuntimeAdmission|CatchingUp' -count=1
```

Expected: FAIL because the evaluator does not exist and runtime currently embeds against partial profiles.

- [ ] **Step 3: Implement one store snapshot and evaluator**

Read all counts/ages/profile/index health from one SQLite read transaction.
`semanticbuild.ReadStatus` delegates to `semanticreadiness.Evaluate`.
`NewRuntimeBuilder` evaluates before constructing Ollama; force-on changes mode,
not readiness. In this plan, only exact-eligible small corpora can become
searchable without ANN; otherwise return `needs_index` and a nil semantic lane
with the precise reason. Preserve at least the first three distinct lexical
parents while admitted `catching_up`; outside admission preserve byte-identical
lexical-off output.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test -race ./internal/semanticreadiness ./internal/semanticbuild ./internal/brainresearch -run 'Readiness|RuntimeAdmission|CatchingUp' -count=1
```

Expected: PASS and provider fakes record zero calls for ineligible state.

- [ ] **Step 5: Commit**

```bash
git add internal/semanticreadiness internal/store/retrieval_projection.go internal/store/retrieval_embedding_profiles.go internal/semanticbuild/status.go internal/semanticbuild/run_test.go internal/brainresearch/runtime.go internal/brainresearch/runtime_test.go internal/brainresearch/strategy_evidence.go internal/brainresearch/strategy_evidence_test.go internal/app/semantic.go internal/app/semantic_output.go internal/app/semantic_output_test.go
git commit --no-gpg-sign -m "feat: gate semantic runtime on readiness"
```

---

### Task 8: Finish Foundation Commands, Documentation, And Gates

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `COMMANDS.md`
- Modify: `MCP.md`
- Modify: `docs/research-harness.md`
- Modify: `internal/app/env_docs.go`
- Modify: `config.yaml.sample`
- Modify: `internal/install/templates/config.yaml.sample`
- Modify: `internal/app/semantic.go`
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Consumes: all foundation behavior from Tasks 1-7.
- Produces: documented `chunk --until-idle --max-duration`, `embed --until-idle --max-duration`, `verify --limit --resume`, truthful status, and an implementation handoff to the segmented-index plan.

- [ ] **Step 1: Write failing CLI contract tests**

Test exact help/JSON fields, read-only status, duration cancellation, durable
resume checkpoint, default-off behavior, and that research/MCP/web cannot launch
chunk/embed/verify. Prove config samples remain semantic off and exact fallback
does not claim production readiness above 25,000 vectors.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/app ./internal/install ./internal/mcpserver ./web -run 'Semantic|NoImplicitBackfill' -count=1
```

Expected: FAIL for missing flags/verify/help and stale documentation assertions.

- [ ] **Step 3: Implement CLI flags and update user-facing documentation**

Keep mutating commands writable and status read-only. Document v2-to-v3
transition, durable empty state, batching, readiness, exact-small-corpus only,
semantic-off default, and the fact that ANN/default-on remain unshipped after
this plan. Add a dated changelog entry for the foundation slice.

- [ ] **Step 4: Run focused and full verification**

Run:

```bash
task fmt
task lint
task test-ci
task build
direnv exec . ./bin/dbrain --no-debug config paths --json
```

Expected: all commands exit 0; paths resolve to the isolated repo/dev root, not production XDG state.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md README.md COMMANDS.md MCP.md docs/research-harness.md internal/app/env_docs.go config.yaml.sample internal/install/templates/config.yaml.sample internal/app/semantic.go internal/app/app_test.go
git commit --no-gpg-sign -m "docs: ship semantic readiness foundation"
```

- [ ] **Step 6: Request whole-plan review**

Review the complete range from the plan's base commit against this plan and
`docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md`.
Fix every Critical/Important finding and rerun all four standard gates. Record
any segmented-index dependency as plan-2 work only when this foundation safely
reports `needs_index` and preserves lexical behavior.
