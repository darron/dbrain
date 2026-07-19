# Production-Corpus Semantic Retrieval Design

## Status

Approved in conversation on 2026-07-19. This document amends, but does not
replace, the approved
`docs/superpowers/specs/2026-07-18-retrieval-first-hybrid-search-design.md`.

The earlier design selected SQLite-authoritative chunks and embeddings plus a
rebuildable local ANN index. The merged foundation implemented deterministic
chunks, portable embeddings, bounded exact search, hybrid fusion, provenance,
and shadow isolation. Testing that foundation against a restored production
database proved the authority and retrieval boundaries sound, but it also
proved that the deferred scalable-index slice and a stricter derived-state
lifecycle are required before semantic retrieval can be enabled.

This amendment defines that production-complete lifecycle. Semantic retrieval
must remain `off` until the readiness, ANN, evaluation, and rollout gates in
this document pass.

## Decision Summary

`dbrain` will keep:

- SQLite as the sole authoritative working database
- the existing raw evidence, chunk, embedding-profile, and parent-citation
  model
- local Ollama embeddings as the default provider boundary
- lexical FTS5 retrieval, protected exact evidence, RRF fusion, parent
  consolidation, shadow isolation, and lexical fail-open
- exact vector search as a correctness oracle, diagnostic path, bounded delta
  search, filtered-small-set search, and small-corpus fallback
- the existing `semanticindex.Searcher` substitution seam

`dbrain` will change:

- projection completeness from an inferred chunk count to durable per-parent
  state
- projection provenance from the raw item/source content hash to a hash of
  every field actually projected
- chunker and projection identities before the first complete embedding
  backfill
- runtime admission from "configured tables exist" to coverage-based
  readiness
- embedding writes and progress reporting from per-row/unbounded behavior to
  batched and constant-memory behavior
- the production semantic accelerator from a whole-profile exact scan to an
  immutable ANN base plus a bounded exact delta
- generation invalidation so ordinary corpus growth makes an index `lagging`,
  while purge, profile mismatch, corruption, and unsafe divergence make it
  unusable
- semantic candidate selection so a giant parent cannot monopolize the lane
  before parent consolidation

`dbrain` will not:

- replace SQLite with Turso or libSQL
- add a vector service, daemon, native extension, CGO requirement, or helper
  process
- make generated model prose authoritative evidence
- silently truncate embedding input
- use partial semantic coverage in normal `on` retrieval
- start an unbounded backfill from a research request
- make semantic retrieval default-on as part of the index implementation

## Production Baseline

The sizing and failure baseline is the restored production database at:

```text
/Users/darron/src/dbrain/data/brain.db
```

The test checkout was `main` at merge commit
`f954d20dbc038cf112a298dea6bd7cad708d49a8`. The database is a development
restore, not the production XDG database or running production service.

Measured corpus state after deterministic chunk projection:

- 34,180 eligible parent rows scanned
- 32,367 parents produced chunks
- 1,813 parents produced no chunkable content
- 286,619 chunks
- 273,479,209 bytes of chunk text
- one parent with 26,512 chunks
- 360 parents with more than 100 chunks, accounting for 137,229 chunks
- 17 parents with more than 1,000 chunks, accounting for 64,445 chunks
- 768-dimensional float32 vectors require exactly 880,493,568 bytes before
  SQLite page and row overhead
- chunk projection grew the database from approximately 1.7 GiB to 2.3 GiB

Observed behavior:

- full projection completed without database corruption and was idempotent
- partial embedding coverage was incorrectly reported as `ready`
- normal `on` mode used 998 arbitrary ready vectors from a 286,619-chunk corpus
- partial semantic results displaced directly relevant Cerebras evidence with
  unrelated material
- shadow mode preserved lexical result order and content
- provider failure and exact-cap overflow failed open to lexical retrieval
- whole-corpus progress JSON accumulated approximately 6.5 MiB of snapshots
- the current chunker emitted newline-only overlap tails
- individual chunks within the rune limit exceeded the selected embedding
  model's context limit
- enabling the semantic path added approximately 4.45 seconds to the measured
  retrieval run, but the run also changed lexical candidate depth, so the
  latency is not attributable to one stage without instrumentation

The production corpus is feasible for a local ANN index. The challenge is not
raw vector count. The challenge is proving freshness, bounded operations,
candidate diversity, resource use, and privacy-safe generation lifecycle.

## Approaches Considered

### Raise The Exact-Search Limit

Rejected as a production query path. A vector-only exact implementation should
replace the current metadata-heavy exact query, but it remains linear in the
number of vectors. At 286,619 vectors it is an offline oracle and bounded-set
tool, not the normal accelerator.

### Complete SQLite Plus A Rebuildable Local ANN Index

Selected. SQLite retains portable vector bytes and all authoritative metadata.
An immutable ANN generation under the configured cache directory accelerates
the stable base. Recent embeddings outside that generation are searched as a
bounded exact delta. The two candidate sets are merged, validated against
current SQLite state, diversified by parent, hydrated, and passed into the
existing hybrid pipeline.

This is a targeted rearchitecture of derived-state freshness and index
lifecycle. It is not an authoritative-database or research-engine rewrite.

### Replace SQLite Or Add A Vector Service

Rejected. A new database engine or service would add authority, backup,
migration, FTS, authentication, deployment, privacy, and recovery boundaries
without removing the need for projection completeness, profile provenance,
readiness, candidate diversity, or evaluation.

## Revised Architecture

```text
authoritative items, sources, and enrichments in SQLite
                         |
                         v
              durable dirty projection state
                         |
                         v
            deterministic projection + chunker v3
                         |
              +----------+----------+
              |                     |
              v                     v
    retrieval_parent_projections  retrieval_chunks
                                      |
                                      v
                              local embedding provider
                                      |
                                      v
                       revisioned retrieval_embeddings
                                      |
                         +------------+------------+
                         |                         |
                         v                         v
             immutable ANN base through R   exact delta after R
                         |                         |
                         +------------+------------+
                                      |
                          current-row validation
                                      |
                      adaptive filters + parent diversity
                                      |
                         existing semantic retriever
                                      |
                    existing lexical + RRF + exact floor
                                      |
                         existing evidence inspection
```

The ANN index remains cache state. SQLite chunks and embeddings remain the
rebuild source. Deleting all ANN generations must never lose evidence.

## Projection State And Freshness

### Durable Parent State

Add `retrieval_parent_projections`, keyed by
`(parent_kind, parent_source_key)`, with these logical fields:

- `parent_kind`
- `parent_source_key`
- `projection_hash`
- `projection_version`
- `chunker_version`
- `status`: `pending`, `current`, `empty`, `blocked`, or `error`
- `chunk_count`
- `reason`
- `dirty_at`
- `projected_at`
- `updated_at`

The migration seeds every currently eligible item and source as `pending`.
Parents with no chunkable content eventually become `empty` with reason
`no_chunkable_content`. They do not remain indistinguishable from unprocessed
parents.

The table is both the projection ledger and dirty queue. No separate queue is
required for the first production release.

### Dirtying Contract

Changes to any projected input mark the parent `pending` in the same database
transaction that changes the authoritative or enrichment row. This includes:

- item title, source type, author, handle, text, X post text, article title,
  article text, legacy summary, legacy OCR, and materialized `note_path`
- source title, source type, domain, extracted text, summary text, and
  materialized `note_path`
- item enrichment insert, update, or delete for summary, OCR, and X media
  transcript roles
- explicit purge or exclusion

Database triggers provide the coverage floor because they protect every store
write path. Store methods still expose named invalidation helpers for tests and
for transitions that need stronger synchronous semantics. Triggers only mark a
parent pending; they do not perform chunking or model work.

A pending parent becomes immediately ineligible for semantic hydration. This
prevents old excerpts or vectors from being returned while maintenance catches
up.

Dirty-parent selection also includes parents that became ineligible or were
removed after an earlier projection. Processing those states deletes their
derived chunks and embeddings, records tombstones for affected bases, and
removes the projection ledger row when no parent remains. The selector is not
limited to the current `note_path != ''` population.

### Projection Hash

`retrieval-projection-v2` computes a SHA-256 hash using length-prefixed fields
in this order:

1. parent kind
2. parent source key
3. title
4. source type
5. author or domain
6. number of projected sections
7. for each ordered section: evidence role, heading, derived flag, and full
   text

The hash, not the raw source `content_hash`, becomes the chunk projection input
hash and participates in chunk identity. Raw content hashes remain unchanged
for their existing import and provenance uses.

Any projected-field change therefore produces a new projection hash. A chunk
cannot retain its identity while its heading, role, projected metadata, or
evidence text changes.

### Transaction Boundary

For one parent, the following occur in one SQLite transaction:

1. validate the parent is still eligible and still has the projected hash that
   was computed
2. replace its complete chunk set
3. delete embeddings for obsolete chunk identities
4. record affected profile tombstone counts and revisions
5. write `current`, `empty`, `blocked`, or `error` projection state

Readers see the old complete parent projection or the new complete projection,
never a partial replacement.

## Chunker V3

The first complete production profile uses:

```text
projection: retrieval-projection-v2
chunker:    retrieval-chunker-v3
```

Chunker v3 preserves headings, paragraph and sentence preference, Unicode rune
offsets, and bounded overlap. It adds these invariants:

- every emitted window is validated independently
- an emitted window whose trimmed text is empty is skipped
- trimming never falsifies `start_char` or `end_char`; offsets are advanced or
  reduced by the exact trimmed rune count
- a chunk obeys both rune and UTF-8 byte ceilings
- overlap cannot create a whitespace-only terminal chunk
- forward progress is guaranteed after overlap selection
- chunk identity uses the complete projection hash

Embedding requests continue to set `truncate:false`. Silent model-side
truncation would make the stored chunk hash disagree with what was embedded.

### Model And Size Selection

Chunker v3 constants are frozen only after the local embedding bakeoff runs on
the restored corpus. The bakeoff compares at least one 768-dimensional model
and one supported lower-dimensional candidate if their retrieval quality is
credible.

For a selected model, the UTF-8 hard ceiling must be no greater than the
model's usable token context minus 128 tokens when every input byte is treated
as a possible fallback token. For the tested 2,048-token EmbeddingGemma
profile, the initial ceiling is 1,800 UTF-8 bytes. The bakeoff must demonstrate
zero context-limit failures across every projected chunk. A model/profile that
cannot satisfy that invariant without unacceptable chunk growth is rejected.

Changing any boundary or overlap behavior after this point requires a new
chunker version and profile. It never mutates the interpretation of v3 rows.

### Transition From The Restored V2 State

The additive migration does not drop existing v2 chunks or the partially built
EmbeddingGemma profile. It seeds every eligible parent as `pending`, and
readiness refuses semantic retrieval while the corpus contains mixed or
unprocessed projection state.

The v3 projection command then replaces one parent at a time. Obsolete v2
chunk identities and their partial embeddings are removed through the existing
derived-state cascade. Only after every eligible parent reaches a terminal v3
state may the new embedding profile begin its complete backfill. No item,
source, raw extract, note, OCR text, transcript, or summary is rewritten by
this transition.

## Revisioned Embedding State

### Profile State

Add `retrieval_embedding_profiles` with one row per profile:

- `profile_id`
- `latest_revision`
- `purge_epoch`
- `base_generation_id`
- `base_indexed_revision`
- `base_indexed_count`
- `delta_ready_count`
- `base_tombstone_count`
- `updated_at`

Add `revision` to `retrieval_embeddings`. A write transaction allocates one
new monotonic profile revision and assigns it to every changed embedding row in
that provider batch. Timestamps and SQLite row IDs are not revision
substitutes.

An obsolete base chunk increments the profile tombstone count before its
embedding row is deleted. A newly ready replacement receives a revision above
the active generation's watermark and enters the exact delta.

### Batched Writes

One validated provider response is persisted in one SQLite transaction:

1. validate profile invariants, response cardinality, dimensions,
   representation, normalization, finite values, and current chunk hashes
2. allocate one profile revision
3. insert or update all batch rows
4. update aggregate profile counters
5. commit

The current single-row method remains a narrow wrapper around the batch method
for tests and exceptional callers.

Ordinary embedding writes do not deactivate a valid ANN base. They make the
profile `lagging` until the delta is empty again.

### Verification

Normal embedding work does not rescan every ready vector. Validation occurs:

- when the provider response is received
- inside the batch write transaction
- while streaming vectors into an ANN build
- during explicit paged `semantic verify`

`semantic verify` is resumable, bounded by row count or duration, and records
corruption without repeatedly rereading the whole profile after each bad row.

## ANN Base And Exact Delta

### Backend Selection

The first implementation plan must bake off:

1. a narrow repo-owned adapter around `github.com/coder/hnsw`
2. one pure-Go disk-backed candidate if the first candidate fails resource,
   persistence, or corruption gates

No library is accepted until it passes the production-vector gates in this
document. The adapter owns the on-disk format contract so a library can be
forked or replaced without changing SQLite evidence or embedding schemas.

Graph identifiers are dense unsigned integer ordinals. A separate immutable
mapping file relates ordinals to 64-character chunk IDs. Chunk ID strings are
not used as graph node keys.

### Immutable Generation

An ANN generation contains:

- graph data
- ordinal-to-chunk-ID mapping
- manifest
- checksums for every file

The manifest records:

- database identity
- generation ID
- profile ID
- projection and chunker versions
- backend and backend format version
- dimensions, representation, normalization, and distance metric
- indexed revision and purge epoch
- indexed chunk count
- deterministic embedding manifest hash
- graph build parameters and deterministic seed
- creation and completion times

The retrieval schema also stores a stable database identity generated when the
retrieval tables are first created and preserved by SQLite backup and restore.
An older restore may share that identity, but its revision, purge epoch, count,
and embedding manifest hash must still match before a cache generation is
eligible.

Build lifecycle:

1. take a consistent SQLite read snapshot and record revision `R` and purge
   epoch `P`
2. stream every current ready embedding with revision at most `R`
3. validate vectors while building into a new temporary directory
4. write mappings, manifest, and checksums
5. reopen the completed files and run structural and sampled recall checks
6. atomically rename the temporary directory into the generation path
7. transactionally activate it only if the current purge epoch remains `P`
8. record embeddings after `R` as the exact delta

An interrupted build leaves the prior active generation untouched.

### Exact Delta

The exact delta contains current ready embeddings whose revision is greater
than the base generation's indexed revision. Its initial hard limit is 5,000
vectors. At 768 dimensions that is approximately 15 MiB of raw vector data.

A normal semantic search:

1. embeds the query once
2. searches the ANN base
3. streams and exactly scores the bounded delta
4. merges the two sets by exact cosine distance
5. validates every candidate against current SQLite embedding status, chunk
   hash, projection state, and parent eligibility
6. adaptively expands, filters, and diversifies the valid candidates
7. hydrates only final candidates

If the delta exceeds 5,000 vectors or base tombstones exceed 2 percent of the
base count, normal semantic retrieval becomes `needs_index` and fails open to
lexical retrieval. It does not silently search a truncated delta or stale-only
base. A rebuild may start explicitly while the previous base remains available
for diagnostic comparison.

### Lean Exact Search

Exact scoring uses a new vector-only store projection containing only:

- chunk ID
- vector bytes
- revision
- parent kind and key
- evidence role and source type needed by filters
- current chunk hash and embedding status

It does not join or materialize chunk text, title, URL, summary, or other
hydration fields. Full-profile exact search is explicit and diagnostic. Normal
automatic exact search is limited to:

- profiles below the configured small-corpus cap
- the active delta
- selective filtered sets below the measured exact cap
- offline ANN recall evaluation

## Filters And Parent Diversity

The production corpus has enough parent skew that post-fusion consolidation is
too late. Semantic candidate generation must produce a parent-diverse lane.

For an unanchored query:

- the semantic lane exposes at most three chunks per parent
- the ANN backend begins with an overfetch of ten times the requested semantic
  depth
- if filters or parent caps leave too few candidates, it expands through
  bounded stages of 200, 500, and 2,000 raw ANN hits
- every expansion validates current SQLite state before accepting a candidate
- the lane stops when it has the requested number of candidates, exhausts the
  graph, or reaches 2,000 examined hits
- diagnostics record examined hits, rejected stale hits, filtered hits,
  distinct parents, and exhaustion reason

Explicitly pinned parent keys are protected and exempt from the ordinary
three-chunk candidate cap until the existing bounded consolidation stage.

Small or highly selective filter sets route directly to bounded exact search.
Broad filters use adaptive ANN overfetch. If a valid filtered candidate window
cannot be produced within the ANN and exact safety limits, the semantic lane
returns `filter_too_selective` and lexical retrieval continues. It never
returns disallowed candidates or pretends an undersized post-filtered window is
complete.

The first release does not truncate giant parents at indexing time. If a flat
index cannot meet recall and diversity gates after adaptive overfetch and exact
reranking, the accepted fallback is a separately designed two-stage system:
parent-representation ANN followed by exact chunk search within selected
parents. That conditional design does not block testing the simpler flat index.

## Readiness And Runtime Admission

Runtime mode and derived-state readiness are separate values.

Modes remain:

- `off`
- `shadow`
- `on`

Readiness states are:

- `not_configured`
- `disabled`
- `needs_projection`
- `needs_embeddings`
- `retry_scheduled`
- `needs_index`
- `building`
- `lagging`
- `degraded_blocked`
- `stale`
- `corrupt`
- `ready`
- `unavailable`

Status reports separate projection, embedding, and index sections. It includes
expected parents, current parents, empty parents, blocked/error/pending
parents, chunk count, ready/pending/blocked/error embeddings, due and scheduled
retries, active generation, base revision, delta count, tombstone count,
manifest health, and backend eligibility.

### Normal On-Mode Gate

Normal `on` retrieval is eligible only when:

- every eligible parent has terminal current projection state
- no projection error or pending parent exists
- every current chunk has a current embedding row for the configured profile
- no embedding is pending or has a retryable error, whether due or scheduled
- at least 99.9 percent of chunks are ready
- no more than 0.1 percent are terminal blocked, every blocked reason is
  classified, and at least 99.9 percent of chunkable parents have one ready
  chunk
- a validated active generation exists for the current profile and purge epoch
- every ready embedding is covered by the base or bounded exact delta
- the exact delta is no larger than 5,000
- base tombstones are no more than 2 percent

Failure of any invariant disables only the semantic lane and records a precise
reason. Returned lexical evidence and ordering remain identical to semantic
`off` behavior.

`ready` means the exact delta is empty. `lagging` means the full current corpus
is still covered by the valid ANN base plus a non-empty exact delta within the
5,000-vector and 2-percent tombstone limits; it remains eligible for normal
retrieval. `degraded_blocked` remains eligible only while every block is
terminal and reviewed and the 99.9/0.1-percent coverage gates pass. All other
non-disabled readiness states are ineligible for normal semantic retrieval.

### Shadow And Diagnostics

Normal shadow comparisons use the same readiness gate so quality evidence is
drawn only from complete profiles. An explicit diagnostic command may use
partial state with `--allow-partial`; its output is labelled
`partial_coverage` and cannot be fed into synthesis or counted as a quality
evaluation.

Runtime construction enforces admission before query embedding or semantic
search. CLI status is not the security or correctness boundary.

## Generation Concurrency And Purge

The existing cross-platform `internal/runlock` package will be extended with
shared and exclusive generation leases:

- a semantic query holds a shared lease from generation validation through
  candidate SQLite validation
- build completion and activation take an exclusive lease
- generation deletion and explicit privacy purge take an exclusive lease and
  wait for readers to drain

Within one process, an RW mutex prevents close/delete races around a loaded
graph. The file lease handles distinct CLI, MCP, and server processes.

Ordinary content changes do not require removing immutable generation files.
Old chunk IDs are rejected by current SQLite validation, new chunks enter the
delta, and tombstone thresholds force a rebuild before degradation grows
unbounded.

Explicit purge is different. Before purge reports success, dbrain must:

1. take the exclusive generation lease
2. commit a short SQLite transaction that increments the profile purge epoch
   and deactivates every generation whose manifest contains the old epoch
3. remove or quarantine every affected generation directory under the
   configured cache root
4. commit the existing authoritative purge transaction, including deletion of
   the parent's chunks and embeddings
5. verify no active manifest or exact row can return the purged chunk IDs
6. release the lease

If cache removal or verification fails, purge fails. It never reports success
while a usable generation still contains the purged representation. A failure
after step 2 may leave semantic retrieval disabled until repair, but it leaves
the authoritative item unpurged and cannot reactivate an old-epoch generation.

The first release retains the current and immediately previous valid
generation only. Automatic unbounded retention is prohibited at production
scale. Cleanup of older non-purge generations is explicit and lease-protected;
status reports their count and disk use.

## Production Operations

### Commands

The operational surface will provide:

```text
dbrain semantic status
dbrain semantic chunk --until-idle --max-duration <duration>
dbrain semantic embed --until-idle --max-duration <duration>
dbrain semantic verify --limit <rows> --resume
dbrain semantic index build
dbrain semantic index verify
dbrain semantic refresh --max-duration <duration>
dbrain semantic query --allow-partial
```

`semantic refresh` performs only explicit maintenance:

1. project dirty parents
2. embed due chunks
3. verify readiness counters
4. build and activate an index when thresholds require it
5. report final readiness

It never changes `research.semantic.mode` and is never launched implicitly by
research, MCP, web chat, or ordinary import.

### Bounded Progress

Interactive progress is streamed periodically. Optional JSONL emits one
bounded event per interval. Final JSON contains:

- aggregate counters
- durable resume checkpoint
- elapsed time and per-stage timing
- last bounded progress sample
- `snapshot_count`
- `snapshots_truncated`

It never contains an array proportional to parents, chunks, batches, vectors,
or elapsed time.

### Provider Circuit Breaker

After three consecutive retryable provider batch failures, embedding stops.
Attempted rows retain their durable retry state. Unattempted rows remain
pending. The command exits with a resumable diagnostic rather than writing the
same outage to hundreds of thousands of rows.

## Instrumentation

Record these timings independently:

- lexical shallow retrieval
- lexical deep candidate-pool retrieval
- query embedding
- ANN search
- exact-delta search
- candidate SQLite validation
- hydration
- parent diversity and consolidation
- RRF fusion
- total retrieval

Record these resource and health values:

- projection and embedding coverage
- blocked/error reason counts
- base and delta vector counts
- tombstone ratio
- ANN build time and peak RSS
- generation disk bytes and loaded steady RSS
- graph candidates examined
- exact-versus-ANN recall
- unique parent count before and after diversity enforcement
- semantic-only additions accepted or rejected by reviewers

Operational metrics retain profile, backend, and generation provenance without
exposing chunk text or cache paths on public surfaces.

## Production-Corpus Evaluation

The current ignored local baseline contains 10 research cases in
`evals/local/research.json` and 6 MCP cases in
`evals/local/darron-mcp.json`. Neither contains a Cerebras or ontology case.
They remain private and are extended rather than replaced.

The existing private research eval harness remains the evaluation surface. It
will compare, on identical questions and profile state:

1. lexical off
2. shadow
3. hybrid using the exact oracle
4. hybrid using ANN plus exact delta

The reviewed dataset contains at least 30 cases:

- eight protected/exact cases covering source keys, canonical URLs, handles,
  quotations, identifiers, and tags
- eight semantic-recall cases including the Cerebras question and low lexical
  overlap paraphrases
- five long-document or hidden-passage cases
- four broad semantic-drift cases
- three source-type or tag-filtered cases
- two no-evidence or semantic-unavailable cases
- explicit coverage of the 26,512-chunk parent and numeric/data-heavy sources

Each case records source-level relevance judgments rather than only one
expected source key. Both planner-assisted and deterministic planner-disabled
runs are included where planning can affect the result.

### Quality Gates

Before opt-in production use:

- all existing lexical-only evals pass
- shadow evidence and ordering are byte-for-byte identical to off
- protected-anchor retention is 100 percent
- the direct Cerebras source reaches the reviewed top-k target
- aggregate semantic-case recall@10 improves and aggregate MRR does not fall
- no reviewed drift case gains an irrelevant semantic-only result in the top
  five
- no pathological parent monopolizes the semantic candidate window
- ANN recall@20 is at least 0.95 against exact search on identical vectors
- filtered ANN cases meet the same 0.95 recall@20 target or route safely to
  exact/lexical fallback

### Latency And Resource Gates

Measure at least 30 warm repetitions plus cold starts on the restored corpus:

- warm semantic-lane p50 no greater than 250 milliseconds
- warm semantic-lane p95 no greater than 750 milliseconds
- hybrid total p95 no greater than lexical p95 plus 750 milliseconds
- cold index open and first search no greater than 3 seconds
- no query reaches the existing 15-second semantic timeout
- ANN recall gates pass with peak build RSS no greater than 4 GiB
- steady loaded semantic RSS no greater than 3 GiB
- one generation uses no more than 2 GiB at the selected profile dimensions
- current plus previous generation fits within 4 GiB of cache

If no pure-Go flat ANN candidate meets these gates, the backend is rejected.
The authority and hybrid architecture remain intact while the conditional
two-stage parent/chunk design is specified separately.

## Production Rollout

### Phase 0: Production-Derived Lab

- preserve a pristine online backup or production restore
- create disposable database and cache copies for migrations and backend
  benchmarks
- record corpus, database, model, build, latency, memory, and disk baselines
- never repeatedly develop against the live authoritative database

### Phase 1: Hardened Binary, Semantic Off

- take and verify an online backup
- deploy additive schema and correctness changes with semantic mode `off`
- verify lexical CLI, HTTPS MCP, web, and normal sync behavior
- do not pull an embedding model or begin backfill implicitly

### Phase 2: Explicit Derived-State Build, Still Off

- select and record the embedding model/profile through the bakeoff
- project every eligible parent to terminal state
- embed every current chunk to terminal state
- build, reopen, and verify the ANN generation
- run a projection/embedding catch-up pass
- confirm full readiness, SQLite integrity, lexical availability during
  builders, and the evaluation/resource gates

SQLite WAL and parent/batch-sized transactions keep read-only research
available. No corpus-wide write transaction is allowed.

### Phase 3: Shadow

- change only `research.semantic.mode` to `shadow`
- retain one immutable verified profile/generation during the observation
  window
- collect several days or a representative query volume of complete shadow
  comparisons
- turn surprises into reviewed private eval cases
- return to `off` on any operational or quality regression

### Phase 4: Explicit Opt-In

- keep configured mode at `shadow`
- allow selected CLI `--semantic` and MCP `use_semantic` calls
- review real evidence packs and answers
- keep default user-visible behavior lexical

### Phase 5: Separate Default-On Decision

Default-on requires a separate design review, changelog entry, PR, deployment
approval, and verified production shadow evidence. Building an index never
changes the default mode by itself.

## Rollback And Recovery

Operational rollback is `research.semantic.mode: off`. Chunks, embeddings, and
cache files may remain because they are not authoritative and cannot affect
off-mode results.

A bad active generation rolls back by atomically reactivating the previous
validated generation if its profile, database identity, purge epoch, and
manifest still match. Otherwise semantic retrieval remains unavailable and
lexical retrieval continues.

A restored or replaced SQLite database rejects cache generations whose
database identity, manifest hash, indexed revision, or purge epoch does not
match. It never adopts a generation merely because the cache path exists.

Profile changes are blue/green: build complete embeddings and a generation for
the new profile, verify it, then switch configuration. The old profile remains
available for rollback.

Chunker v3 is finalized before the first production semantic activation.
Simultaneous active chunker projections are not part of this design. A future
chunker change may temporarily return semantic retrieval to `off` while the
new projection rebuilds.

## Testing Strategy

### Unit Tests

- complete projection hash sensitivity and stable length-prefixed encoding
- chunker v3 whitespace tails, UTF-8 byte ceiling, rune offsets, overlap
  progress, and context-bound corpus fixtures
- readiness state and admission matrix
- monotonic profile revisions and batch allocation
- ANN/delta merge ordering and exact rerank
- adaptive overfetch, filters, parent caps, and protected-parent behavior
- bounded progress serialization
- manifest and checksum validation

### Store And Migration Tests

- fresh and existing databases create projection/profile state safely
- migration-history repair behavior follows append-only policy
- migration seeds every eligible parent pending
- projected-field changes dirty exactly the affected parent
- irrelevant field changes do not create semantic churn
- projection state and chunk replacement are atomic
- empty parents reach terminal state
- embedding batches commit atomically and allocate one revision
- tombstones and delta counters remain correct across replace, retry, block,
  and delete transitions
- read-only open performs no migration or maintenance work

### Integration Tests

- runtime refuses partial, dirty, oversized-delta, stale, and corrupt profiles
- shadow cannot alter returned evidence or synthesis
- normal corpus changes produce delta results without disabling the base
- stale ANN candidates are rejected through current SQLite validation
- interrupted generation build leaves the prior base active
- activation rejects a changed purge epoch or manifest
- missing generation follows exact-cap or lexical fallback policy
- giant-parent search produces a parent-diverse lane
- selective filters route to bounded exact search
- broad filters expand ANN candidates without leaking disallowed rows
- shared readers and exclusive activation/purge leases do not race
- purge removes database rows and every usable generation before success
- restored older SQLite state rejects a newer cache generation

### Full Gates

Every implementation PR runs:

```text
task fmt
task lint
task test-ci
task build
```

The ANN PR additionally runs the `CGO_ENABLED=0` release build matrix for
Darwin amd64/arm64, Linux amd64/arm64, and Windows amd64.

Production-corpus benchmarks and private evals run against disposable restored
copies and are reported separately from deterministic CI tests.

## Documentation And Product Surfaces

Implementation updates must keep these surfaces aligned:

- `README.md`
- `COMMANDS.md`
- `MCP.md`
- `docs/research-harness.md`
- sample and installer configuration
- `dbrain config env`
- semantic CLI help and JSON schemas
- research traces and eval documentation
- repository and installed `dbrain-mcp` skill when MCP behavior changes
- `CHANGELOG.md` for each user-visible implementation slice

Normal human-facing output continues to cite parent source keys and canonical
URLs. Chunk IDs, profile revisions, generation IDs, hashes, and cache paths are
diagnostic data only.

## Implementation Decomposition

This design is implemented through four independently reviewable plans:

1. **Foundation correctness and readiness**
   - projection ledger and dirtying
   - projection hash and chunker v3
   - batched embeddings, explicit verification, bounded progress
   - readiness status and runtime admission
2. **Scalable index and base/delta lifecycle**
   - lean exact APIs and revision state
   - backend bakeoff and selected ANN adapter
   - immutable generation build, activation, validation, leases, and purge
   - exact delta, adaptive filters, and parent diversity
3. **Production evaluation and benchmarks**
   - reviewed local cases and relevance judgments
   - lexical/exact/ANN/hybrid comparison runner
   - latency, recall, diversity, memory, and disk reporting
4. **Operational rollout and rollback**
   - explicit refresh workflow
   - off-mode migration and backfill runbook
   - shadow observation and opt-in gates
   - default-on decision checklist

Each plan must leave lexical retrieval working and semantic behavior disabled
or fail-open when its own slice is incomplete.

## Acceptance Criteria

This amended feature is production-ready only when:

1. Every eligible parent has durable, current projection state, including
   intentionally empty parents.
2. Every projected-field mutation immediately makes old semantic evidence
   ineligible.
3. Chunker v3 produces no empty or context-overflowing chunks on the restored
   corpus.
4. Normal embedding work is batched, resumable, constant-memory in progress
   output, and does not scan all ready vectors before each batch.
5. Runtime `on` never searches partial, dirty, retry-due, oversized-delta,
   stale, corrupt, or mismatched state.
6. A production-sized immutable ANN base can be built, validated, atomically
   activated, reopened, queried, and rolled back under `CGO_ENABLED=0`.
7. Ordinary new or changed evidence remains searchable through the bounded
   exact delta without immediately invalidating the base.
8. Every ANN candidate is revalidated against current SQLite state before it
   becomes evidence.
9. Filters and parent skew cannot silently empty or monopolize the semantic
   lane.
10. Explicit purge makes database rows and every usable generation containing
    the purged representation unreachable before reporting success.
11. The reviewed production-corpus quality, latency, recall, memory, disk, and
    release-matrix gates pass.
12. Shadow and failure states preserve byte-for-byte lexical evidence and
    ordering.
13. Default-on remains a separate approved production change.

## Deferred Work

The following remain outside this implementation:

- ontology extraction or graph reasoning
- Turso/libSQL adoption
- hosted embeddings by default
- cross-device ANN synchronization
- automatic unlimited generation garbage collection
- a learned reranker
- quantized production vectors unless the float32 bakeoff fails resource gates
- parent-first hierarchical retrieval unless flat ANN fails the accepted
  recall/diversity gates
- simultaneous active chunker projections

## References

- `docs/superpowers/specs/2026-07-18-retrieval-first-hybrid-search-design.md`
- `docs/research-harness.md`
- <https://github.com/coder/hnsw>
- <https://github.com/hupe1980/vecgo>
- <https://github.com/tursodatabase/turso>
- <https://github.com/tursodatabase/turso/blob/main/docs/manual.md>
