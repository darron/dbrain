# Production-Corpus Semantic Retrieval Design

## Status

Approved in conversation on 2026-07-19 and amended for continuous corpus
growth on the same date. This document amends, but does not replace, the
approved `docs/superpowers/specs/2026-07-18-retrieval-first-hybrid-search-design.md`.

The earlier design selected SQLite-authoritative chunks and embeddings plus a
rebuildable local ANN index. The merged foundation implemented deterministic
chunks, portable embeddings, bounded exact search, hybrid fusion, provenance,
and shadow isolation. Testing that foundation against a restored production
database proved the authority and retrieval boundaries sound, but it also
proved that the deferred scalable-index slice and a stricter derived-state
lifecycle are required before semantic retrieval can be enabled.

The first version of this amendment selected one immutable ANN base plus a
5,000-vector exact delta. Review against the user's append-only growth model
showed that lifecycle would require a full-corpus rebuild after every fixed
amount of growth. From 286,619 to one million chunks, a 5,000-vector rebuild
interval would trigger approximately 143 full builds and about 92 million
cumulative vector insertions. That write amplification grows with the corpus
and is not an acceptable steady state.

This version replaces the monolithic base with content-addressed immutable ANN
segments, an exact level-zero delta, and bounded size-tiered compaction. It also
defines bounded catch-up semantics for continuous ingestion and section-local
chunk identity so a small change to a giant parent does not re-key every chunk.
Semantic retrieval must remain `off` until the readiness, segmented ANN,
evaluation, and rollout gates in this document pass.

## Decision Summary

`dbrain` will keep:

- SQLite as the sole authoritative working database
- the existing raw evidence, chunk, embedding-profile, and parent-citation
  model
- local Ollama embeddings as the default provider boundary
- lexical FTS5 retrieval, protected exact evidence, RRF fusion, parent
  consolidation, shadow isolation, and lexical fail-open
- exact vector search as a correctness oracle, diagnostic path, bounded L0
  search, filtered-small-set search, and small-corpus fallback
- the existing `semanticindex.Searcher` substitution seam

`dbrain` will change:

- projection completeness from an inferred chunk count to durable per-parent
  state
- projection provenance from the raw item/source content hash to a parent hash
  of every field actually projected plus stable section-local hashes used for
  chunk identity
- chunker and projection identities before the first complete embedding
  backfill
- runtime admission from "configured tables exist" to coverage-based
  readiness
- embedding writes and progress reporting from per-row/unbounded behavior to
  batched and constant-memory behavior
- the production semantic accelerator from a whole-profile exact scan to an
  exact level-zero delta plus content-addressed immutable ANN segments
- generation lifecycle from repeated full rebuilds to bounded flush and
  size-tiered compaction
- generation admission so bounded ordinary corpus growth makes an index
  `catching_up`, while excessive lag, purge, profile mismatch, corruption, and
  unsafe divergence make it unusable
- semantic candidate selection so a giant parent cannot monopolize the lane
  before parent consolidation

`dbrain` will not:

- replace SQLite with Turso or libSQL
- add a vector service, daemon, helper process, or a native/CGO requirement to
  the default build
- make generated model prose authoritative evidence
- silently truncate embedding input
- use unbounded or unlabelled partial semantic coverage in normal `on`
  retrieval
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

### One Immutable ANN Base Plus An Exact Delta

Rejected as the steady-state production lifecycle. It is safe for a static
snapshot, but a fixed delta threshold converts continuous append into repeated
full-corpus rebuilds. Retaining current and previous full generations also
duplicates most cache bytes during every rebuild.

### Mutable Incremental HNSW

Rejected. Safely mutating one graph in place would require a graph write-ahead
log, checkpoints, recovery, deletion repair, cross-process writer exclusion,
and a rollback protocol. Those responsibilities would turn the cache into a
second database while still leaving SQLite as the authority.

### SQLite Plus Immutable Segmented ANN

Selected. SQLite retains portable vector bytes and all authoritative metadata.
An exact level-zero delta absorbs new embeddings. Explicit maintenance flushes
that delta into small immutable ANN segments and compacts equal-sized segments
geometrically. A generation is an atomic root manifest that references a set of
content-addressed segments rather than one monolithic graph.

Queries search the active segment set and exact L0, merge and exactly
rerank candidates, validate them against current SQLite state, diversify by
parent, hydrate, and enter the existing hybrid pipeline. This is a targeted
rearchitecture of derived-state freshness and index lifecycle, not an
authoritative-database or research-engine rewrite.

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
                immutable ANN segments      exact L0 delta
                         |                         |
                         +------------+------------+
                                      |
                         exact candidate reranking
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

The ANN segments and root manifests remain cache state. SQLite chunks and
embeddings remain the rebuild source. Deleting every segment and generation
must never lose evidence.

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
- `dirty_revision`
- `projected_revision`
- `projected_at`
- `updated_at`

The migration seeds every currently eligible item and source as `pending`.
Parents with no chunkable content eventually become `empty` with reason
`no_chunkable_content`. They do not remain indistinguishable from unprocessed
parents.

The table is both the projection ledger and dirty queue. A database-wide
monotonic projection-work counter lives in retrieval state. Every dirtying
transaction allocates a new work revision and writes it to each affected
parent. Timestamps remain diagnostic and are never queue identity. No separate
queue is required for the first production release.

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

The foundation tables and constraints belong to migration 16
`retrieval_semantic_foundation_v2`. Authoritative dirtying triggers belong to
append-only migration 17 `retrieval_projection_dirty_triggers`. Because the
exercised migration 17 definitions also dirtied on raw `content_hash`,
append-only migration 18
`retrieval_projection_dirty_trigger_provenance_repair` installs the corrected
canonical definitions. Schema identity validation is version-gated to the
historical v17 definitions or corrected v18 definitions accordingly, so an
already-recorded migration is never silently reinterpreted.

Migration 18 also canonicalizes pre-release derived state that may have been
projected under the historical parent-hash contract. When ledger parents
exist, they all receive one shared new pending revision; partial projection
staging is cleared and index generations become stale/inactive. Raw evidence,
chunks, and embeddings remain preserved until ordinary projection maintenance
replaces or reuses them, while pending-parent filtering excludes them
immediately.

Raw item/source `content_hash` remains provenance only. It is not part of the
parent projection hash and a hash-only recalculation does not dirty semantic
state; changes to the actual projected fields above do.

A pending parent becomes immediately ineligible for semantic hydration. This
prevents old excerpts or vectors from being returned while maintenance catches
up.

Dirty-parent selection also includes parents that became ineligible or were
removed after an earlier projection. Processing those states deletes their
derived chunks and embeddings, records tombstones for affected bases, and
removes the projection ledger row when no parent remains. The selector is not
limited to the current `note_path != ''` population.

### Parent And Section Hashes

`retrieval-projection-v2` computes one parent projection hash and one hash per
projected section. Every encoding is SHA-256 over length-prefixed fields.

The parent projection hash contains:

1. parent kind
2. parent source key
3. title
4. source type
5. author or domain
6. number of projected sections
7. for each ordered section: stable section key and section hash

Every projected section has a stable, non-prose `section_key` assigned by its
origin. Fixed parent fields use fixed keys such as `article_text` or `summary`.
Repeated enrichments use their durable enrichment or asset identity. Section
keys never use list position, generated headings, or model output. Projection
rejects duplicate section keys within one parent rather than allowing chunk-ID
collisions.

The section hash contains:

1. section key
2. evidence role
3. heading
4. derived flag
5. full section text

The parent and section hashes drive completeness and dirty-state comparison;
neither participates directly in chunk identity. Chunker v3 uses deterministic
content-defined cut points that prefer paragraph and sentence boundaries and
fall back to a rolling-hash boundary inside oversized spans. Boundaries
resynchronize no later than one hard byte ceiling after a local edit.

A chunk identity uses parent identity, projection and chunker versions, stable
section key, evidence role, derived flag, heading hash, and exact
embedded-window text hash. Absolute offsets and whole-section hashes are
metadata, not identity.

Identical embedded windows within one section share one chunk and one
embedding. `retrieval_chunk_occurrences` stores every section-local start/end
offset for that chunk separately. Occurrence rows may be replaced when text is
inserted before repeated content, but they do not change chunk or embedding
identity. Hydration retains parent citation and may expose all matching offsets
for diagnostics; occurrence rows never become independent search candidates.
Moving an unchanged window updates its citation offsets without invalidating
its embedding. A changed heading or embedded window changes identity, while a
local body edit replaces only overlapping windows and their bounded overlap
neighbours. Chunker v3's initial maximum churn contract is eight chunk
identities for a one-byte edit at least one hard ceiling from a section edge.
The 26,512-chunk production outlier is a required fixture for that invariant.

Mutable parent title, author, domain, and presentation metadata are hydrated
separately or projected as their own small sections. They are not repeated in
every body window's embedded input. If a source format supplies a meaningful
body-section heading that is embedded, only windows in that body section depend
on its heading hash.

Raw item and source `content_hash` values remain unchanged for their existing
import and provenance uses.

### Transaction Boundary

For one parent work revision, the following occur in one SQLite transaction:

1. validate the parent is still eligible, still has the projected hash that was
   computed, and still has the selected `dirty_revision`
2. diff and apply its complete chunk and occurrence sets, retaining unchanged
   embedded-window identities
3. delete embeddings for obsolete chunk identities
4. record affected profile tombstone counts and revisions
5. write `current`, `empty`, `blocked`, or `error` projection state and set
   `projected_revision` to the selected work revision

If the parent is re-dirtied while projection runs, its revision advances. The
staged result fails validation, is discarded, and the newer revision remains
pending. An older run can never clear newer work.

Readers see the old complete parent projection or the new complete projection,
never a partial replacement.

### Giant Parents And Resumability

A parent estimated to produce more than 1,000 chunks is a giant projection
job. `retrieval_projection_staging` stores its section-local chunks in durable,
non-searchable batches keyed by a work ID. The associated checkpoint records
section key and next window boundary, so `max-duration` can stop and resume
without reprocessing the parent from the beginning. The chunker prepares each
section's content-defined boundary plan once and stores the opaque, versioned
seek state in a reserved metadata row. That row is not a chunk or occurrence,
is excluded from progress JSON and staging counts, and never enters promotion
or search. Resume validates the plan's projection/chunker versions, parent
hash, and options before using it.

Only a complete staged projection enters the atomic parent apply transaction
above. Promotion independently streams the current authoritative parent and
requires exact canonical equality with every staged chunk and occurrence;
caller-supplied completion state is not accepted as proof. Missing, fabricated,
altered, or extra staged rows fail closed. The intentional cost is exactly one
prepared boundary pass for staging plus one bounded authoritative pass at
promotion; avoiding the second pass would require trusting mutable staged JSON
as evidence of completeness. Abandoned staging belongs to the derived-state
cleanup and purge contracts and is never included by retrieval queries.

Staging begins when a batch reaches any of 1,000 unique chunks, 1,000
occurrences, or 4 MiB of staged JSON. The first release hard-limits one parent
to 50,000 unique chunks, 200,000 occurrences, and 128 MiB of staged
chunk/occurrence JSON. Exceeding any limit becomes terminal `blocked` with reason
`projection_too_large_for_flat_retrieval`; partial staged chunks never become
searchable, including after restart from a previously complete checkpoint.
Raising a limit or adding a hierarchical representation requires a new measured
design. On the 2026-07-21 restored corpus, chunker v3's largest parent produced
1,785 unique chunks and 1,967 occurrences; its two independent projection
passes took 56.5 ms and 54.7 ms respectively. The synthetic 26,512-window
regression also remains inside the explicit bounds.

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
- content-defined boundaries resynchronize within one hard byte ceiling after
  a local edit
- chunk identity uses only stable origin and exact embedded-window inputs, not
  absolute offsets or a whole-section hash

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
- `active_generation_id`
- `active_snapshot_revision`
- `active_indexed_count`
- `l0_ready_count`
- `active_tombstone_count`
- `updated_at`

`purge_epoch` is database-wide retrieval state, denormalized into each profile
row for admission checks. One purge advances the global epoch and updates or
invalidates every profile together; an inactive or rollback profile never
retains an older usable purge epoch.

Add `revision` to `retrieval_embeddings`. A write transaction allocates one
new monotonic profile revision and assigns it to every changed embedding row in
that provider batch. Timestamps and SQLite row IDs are not revision
substitutes.

An obsolete indexed chunk increments its active segment and profile tombstone
counts before its embedding row is deleted. A newly ready replacement receives
a revision above the active generation's watermark and enters exact L0.

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

Ordinary embedding writes do not deactivate a valid segment set. A fully
projected and embedded profile remains `ready` while L0 is below its target and
no compaction is overdue. It becomes `catching_up` when projection or embedding
work remains, L0 reaches its target, or compaction is due, subject to the hard
admission limits below.

### Verification

Normal embedding work does not rescan every ready vector. Validation occurs:

- when the provider response is received
- inside the batch write transaction
- while streaming vectors into an ANN build
- during explicit paged `semantic verify`

`semantic verify` is resumable, bounded by row count or duration, and records
corruption without repeatedly rereading the whole profile after each bad row.

## Segmented ANN Generations And Exact L0

### Backend Selection

No backend is accepted until it passes the production-vector gates in this
document. The adapter owns both the segment format and search contract so a
library can be replaced without changing SQLite evidence, embedding, or
root-manifest schemas.

#### 2026-07-21 Backend Screening Record

`github.com/coder/hnsw` is rejected before any segment-lifecycle implementation.
The deterministic screening harness used the same 768 float32 dimensions and
top-20 recall target as the restored-corpus profile. At only 1,000 vectors it
returned sampled recall@20 of 0.355 against the exact oracle, below the 0.95
gate. Export/import reproduced the same result, so persistence did not explain
the failure. A 64-dimensional diagnostic run also failed at 0.85; increasing
`EfSearch` from 256 to 1,024 did not change recall, and raising `M` from 16 to
32 reduced it to 0.56.

The source explains why this is not a scale experiment worth extending: the
library's graph search retains only `k` results and may terminate as soon as
there is no immediate improvement. Its `EfSearch` setting therefore does not
provide the normal wider result-beam behavior needed to tune recall. The
adapter and devtool remain as content-free rejection evidence only; they do
not authorize a graph payload in an immutable segment.

The bounded pure-Go disk-persistence survey did not identify an acceptable
replacement. [`habedi/hann`](https://github.com/habedi/hann) requires a C/C++
compiler and AVX, violating the CGO-free boundary. [`viant/sqlite-vec`](https://github.com/viant/sqlite-vec)
is CGO-free, but its public cover package documents a brute-force delegate,
has no cover-index tests in its v0.3.0 source tree, and persists/rebuilds a
mutable index blob in a separate shadow SQLite schema. It is neither a proven
ANN accelerator nor compatible with the SQLite-authoritative immutable-segment
contract.

#### 2026-07-22 Optional Native Backend Screening Decision

The user approved a separately evaluated optional native backend. The first
candidate is USearch (`github.com/unum-cloud/usearch/golang`) backed by its
pinned arm64 macOS C library for development screening. This is a narrow,
temporary candidate boundary, not acceptance of USearch for dbrain's product
or release distribution.

The screening integration must meet all of these constraints:

1. SQLite remains the sole authority. USearch receives only dense segment-local
   ordinals and float32 vectors; it neither opens nor writes `brain.db`.
2. The candidate runs only in a content-free devtool until it clears the exact
   recall, save/load reopen, resource, corruption, and cancellation gates.
   It must not construct a production segment, call an embedding provider, or
   make semantic retrieval available.
3. The default dbrain build remains CGO-free and works without USearch. Native
   support is isolated behind an explicit build tag and must report
   `native_backend_unavailable` rather than degrade correctness or block lexical
   retrieval.
4. The test library is supplied from the upstream versioned release archive in
   an isolated temporary directory. It is not installed globally, added to a
   user PATH, or represented as a Homebrew dependency. Distribution is deferred
   until the candidate passes screening.
5. The eventual dbrain segment envelope, membership map, checksums, atomic
   publication, provenance, and cache reclamation remain repo-owned. USearch's
   `Save`, `Load`, or `View` files are opaque payloads inside that envelope.

The candidate screen passed on the upstream v2.26.0 arm64 macOS library using
the content-free 768-dimensional corpus and the exact top-20 oracle. The
qualifying parameters were connectivity 16, expansion-add 128, and
expansion-search 256. A 1,000-vector stage had 1.00 recall@20, 25,000 had
1.00, 100,000 had 0.995, and the 286,619-vector production-shaped stage had
0.97 after save/load reopen. At the smaller expansion-search 128 setting, the
same full stage reached only 0.94 and was correctly rejected; the reopen value
matched in both runs, ruling out persistence as the cause.

At the qualifying full stage, graph construction took 170.6 seconds, the
opaque payload was 923,077,720 bytes, and sampled query latency was 1.00 ms
p50 and 3.55 ms p95. The Go heap-system measurement was 0.89 GiB after build
and 3.77 GiB after reopen, below the 4 GiB screen ceiling. A sampled process
inspection observed approximately 1.75 GiB resident memory during construction,
but a trustworthy process max-RSS capture remains a segment-lifecycle gate; Go
heap-system counters are not OS RSS.

Backend status is **candidate screen passed; the first lifecycle foundation is
implemented; serving remains unapproved**. The foundation owns deterministic
content-addressed opaque payload envelopes, checksum-verified segment/root
reopen, atomic cache publication, SQLite segment catalog/member/generation
references, and an internal 5,000-vector revision-prefix L0 flush seam. It is
backend-injected and does not construct USearch in the default binary.

This does not authorize native serving, a semantic configuration value other
than `exact`, a CLI flush command, compaction, cache reclamation, a full corpus
embedding drain, release packaging, or semantic `on`. Each remains separately
gated by the resource, freshness, recall, and operational checks below.
Semantic mode remains off and all unavailable paths remain lexical fail-open.

The optional evaluator is deliberately separate from the dbrain CLI. Under
`usearch && cgo`, it may build one opaque payload only through the internal
5,000-vector lifecycle seam and only after explicit `--apply`. It requires an
explicit database, cache, profile, and report path, rejects the configured
production database before opening it, and also rejects a candidate path that
is its own `<root>/data/brain.db` configured database. This supports controlled
restored-corpus evidence without weakening the local-first production boundary.

Graph identifiers are dense unsigned integer ordinals. A separate immutable
membership map relates each ordinal to its chunk, parent, embedding revision,
and vector hash. Chunk ID strings are not used as graph node keys.

### Immutable Segments

Each ANN segment contains:

- graph data
- an ordinal membership map containing chunk ID, parent kind and key,
  embedding revision, and vector hash for every graph ordinal
- immutable secondary indexes over that map sorted by chunk ID and by parent
  identity
- chunk-ID and parent-identity Bloom filters used only to avoid unnecessary
  exact index scans
- a segment manifest
- checksums for every graph and mapping payload file

The segment manifest records:

- segment content hash and size class
- profile, projection, and chunker versions
- backend and backend format version
- dimensions, representation, normalization, and distance metric
- minimum and maximum source revisions
- indexed chunk count and deterministic embedding manifest hash
- graph build parameters and deterministic seed
- creation and completion times

The segment content hash is computed from a canonical descriptor with a frozen
field order and length-prefix encoding. The descriptor excludes the hash field
and manifest file itself and includes logical vector membership, profile and
format fields, build parameters, and graph/mapping payload checksums. The root
manifest separately records a checksum of the completed segment manifest.
Completed segments live under:

```text
<cache>/semantic/<database-id>/<profile-id>/segments/<segment-hash>/
```

Segments are immutable and may be shared by the active and retained rollback
generations. Segment files are never authoritative. The currently eligible
semantic index is always rebuildable from SQLite; exact bytes for an obsolete,
tombstoned historical segment need not be reproducible.

### Root Generation Manifest

An ANN generation is a small root manifest that atomically selects an ordered
set of immutable segments. It records:

- database identity
- generation ID and profile ID
- projection and chunker versions
- purge epoch
- source snapshot revision
- ordered segment hashes and their manifest hashes
- total indexed count and tombstone count at activation
- deterministic indexed-membership hash
- creation and completion times

The SQLite generation record stores the completed root-manifest checksum and
the actual `activated_at` time written inside the compare-and-swap activation
transaction. The immutable root manifest does not predict activation or contain
its own checksum.

Root manifests live under:

```text
<cache>/semantic/<database-id>/<profile-id>/generations/<generation-id>/
```

SQLite stores a derived segment catalog, immutable segment-to-chunk membership,
and generation-to-segment references. Each membership row records parent kind
and key, chunk ID, embedding revision, and vector hash. Rows are staged in
bounded batches before a segment is published and are reconstructible exactly
from its ordinal map and checked secondary indexes, without consulting current
chunk or embedding rows. Indexed membership is the join of the active root's
segment references and those immutable rows; each
`(chunk_id, revision, vector_hash)` entry appears once.
A usable membership additionally requires the current ready SQLite embedding
to match the recorded revision and vector hash, and each current embedding may
have at most one usable membership. Updating an indexed embedding therefore
tombstones the old immutable entry and puts the current row in L0 rather than
making one chunk usable in both places. A later flush may leave the old version
physically present until compaction, but validation cannot accept it.
Invalidation uses the join to update the correct segment tombstone counter
without scanning every segment. Historical membership rows retain their parent
provenance until their physical segment is deleted; ordinary projection cleanup
must not erase the only link from an old vector to its parent. None of these
tables is evidence.

The retrieval schema also stores a stable database identity generated when the
retrieval tables are first created and preserved by SQLite backup and restore.
An older restore may share that identity, but its revision, purge epoch, count,
indexed-membership hash, and every segment manifest hash must still match before
a root generation is eligible.

### L0 Flush And Size-Tiered Compaction

Exact L0 contains current ready embeddings that do not have a usable matching
membership in the active root. They normally have revisions above the root's
source snapshot revision; an intentionally unflushed tail from that snapshot
and repaired or replaced rows with mismatched indexed
revision or vector hash are also L0. The initial target is 5,000 vectors and the
hard cap is 10,000. At 768 dimensions those limits contain approximately 15 MiB
and 30 MiB of raw vector data respectively.

Explicit maintenance applies these rules:

1. snapshot L0 through revision `R` under purge epoch `P`
2. validate and partition it into temporary immutable segments no larger than
   the physical cap
3. reopen the segment and run structural and sampled recall checks
4. atomically publish the content-addressed segment
5. create and verify a new root manifest containing that segment
6. transactionally activate the root only if purge epoch remains `P` and the
   expected prior root is still active
7. leave embeddings after `R` in L0

The activation transaction inserts the root and its small segment-reference
set, advances the profile pointer and source snapshot revision, and updates L0
and tombstone counters together. Segment membership has already been staged and
verified. Readers observe the old complete root state or the new complete root
state, never a mixed manifest and membership view.

Publication uses temporary directories on the same filesystem as their final
paths. The writer closes and `fsync`s every payload, writes and `fsync`s the
canonical manifest, `fsync`s the temporary directory, renames it atomically,
and `fsync`s the parent directory. It applies the same sequence to the root
manifest before committing SQLite activation. SQLite is never allowed to point
at output that has not completed that durability sequence.

On restart, a missing or checksum-invalid active root or segment makes semantic
retrieval `corrupt` and lexical-only. Completed but unreferenced published
segments, staged membership, and temporary directories are never adopted by
path presence; explicit repair or lease-protected garbage collection reconciles
them. Failpoint tests cover process exit and simulated I/O failure before and
after every close, sync, rename, and SQLite commit boundary.

Every normal flush contains at least 5,000 vectors. Size classes are half-open
live-count ranges `[5,000, 10,000)`, `[10,000, 20,000)`, `[20,000, 40,000)`,
`[40,000, 80,000)`, and `[80,000, 160,000)`. Pair compaction selects the two
oldest segments in one non-capped range, streams their currently valid SQLite
vectors, omits tombstones, and classifies output by actual live count. If fewer
than 5,000 live rows remain, no undersized ANN segment is published; those rows
become exact L0 under the membership-based L0 definition. No undersized
age-flush segment exists.

A segment above one percent tombstones is eligible for singleton cleanup even
without a same-class partner. Singleton cleanup rewrites only that segment's
live rows and may move its output to a lower class or exact L0. Pair and
singleton compaction both replace their inputs in one new root and cannot
select their own unpublished output recursively.

The capped class targets 160,000 vectors and permits `[160,000, 200,000]`.
Physical segments never exceed 200,000 vectors. Pair input from the
`[80,000, 160,000)` class can total at most 319,998 live rows. When it exceeds
200,000, the deterministic packer emits one capped segment containing
`min(200,000, total-5,000)` rows and classifies the remaining 5,000–119,998 rows
by their actual lower range. It therefore never creates two same-class sibling
outputs that immediately qualify to repeat the same compaction. The capped
output is not eligible for ordinary pair compaction; the remainder can only
combine later with another segment in its actual lower class, so this transition
strictly removes the selected pair and cannot loop on its own output.

This bounds per-segment compaction memory, purge deletion, and repair work. The
float32 flat-segment backend must be replaced or made hierarchical before
capped-segment fanout violates the resource and latency gates.

Multiple capped-class segments may coexist and are not repeatedly merged merely
because they share a class; they compact only to remove tombstones or repair
corruption. Their fanout is therefore the explicit signal that the flat backend
is approaching its scale envelope.

Compaction is bounded work, not a corpus-wide rebuild. It temporarily retains
only the input segments and their replacement output. Unchanged segments are
shared between root generations. An interrupted flush or compaction leaves the
prior active root untouched.

The first full production build partitions the SQLite snapshot directly into
the same size classes. A final tail below 5,000 remains exact L0. The build does
not simulate hundreds of incremental flushes.

Flush begins when L0 reaches its 5,000-vector target. A smaller L0 remains an
exact, `ready` serving tier regardless of age. Compaction begins when two
segments occupy the same non-capped size class or a segment exceeds one percent
tombstones. Maintenance may start proactively; serving remains eligible while
L0 is no larger than 10,000 and aggregate active tombstones are no more than two
percent.

If L0 exceeds 10,000 vectors, aggregate tombstones exceed two percent, a
segment cannot be opened, or fanout exceeds its measured resource gate, normal
semantic retrieval becomes `needs_index` and fails open to lexical retrieval.
It does not silently truncate L0, ignore a segment, or search a known-unsafe
root.

### Implemented Activation Foundation (2026-07-22)

Migration 23 implements the prerequisite activation contract for the planned
size-tiered compactor. L0 is now the exact set of current ready embeddings that
have no active root membership with the same chunk ID, embedding revision, and
vector hash; it is not inferred from `revision > active_snapshot_revision`.
This preserves a compacted sub-5,000 live remainder as exact L0 even when its
revision predates the root snapshot.

Every new root activation carries its expected active generation ID, purge
epoch, and active snapshot revision. SQLite checks those expectations in the
same transaction as generation activation and profile-counter update. An
an ordinary all-new flush advances the snapshot; a membership-L0 flush ending
at the active snapshot uses an internal equal-snapshot rewrite. The proposed root is rejected if it contains
duplicate usable memberships. Migration repair recomputes L0 and tombstone
counters from membership rather than trusting stored aggregate values.

This does not yet implement compaction selection or payload building, leases,
cache garbage collection, ANN query serving, or production corpus mutation.

### Implemented Compaction Policy (2026-07-22)

`internal/semanticbuild` now has a pure deterministic planner for the accepted
size-tier rules. It prefers the oldest segment whose tombstones exceed one
percent, otherwise the two oldest segments in one non-capped class, classifies
undersized output as exact L0, and applies the capped/remainder packing rule.
The planner consumes only stable metadata and does not read vectors, publish a
replacement root, remove cache files, or affect semantic serving.

### Implemented Active-Root Compaction Snapshot (2026-07-22)

`internal/store` can now read an active root in one read-only SQLite
transaction as deterministic compaction facts: profile activation/CAS state,
immutable segment catalog metadata, stable creation order, and per-segment live
and tombstone counts. A member is live only when its exact stored
`(chunk_id, revision, vector_hash)` still joins a ready embedding and a current
parent projection; every other catalogued member is a tombstone. The read fails
closed when an active generation is unavailable, a segment belongs to another
profile, or its catalogued count disagrees with immutable membership rows.

This is an orchestration input only. It does not select vectors, build a
replacement payload, activate or remove a root segment, mutate cache files, or
change semantic serving. In particular, the current root format forbids an
empty segment list, so a last-segment-to-L0 transition remains an explicit
future format/activation-contract change rather than an implicit compaction
side effect.

### Implemented Active-Segment Live-Member Stream (2026-07-22)

`internal/store` can now stream current live embeddings for one or two selected
active-root segments under the snapshot's expected root, purge epoch, and
snapshot revision. It rechecks the active completed generation, selected
membership catalog counts, exact ready `(chunk_id, revision, vector_hash)`
identity, current parent projection, current chunk text hash, and encoded vector
integrity before invoking its callback. Rows arrive in stable active-segment and
member-ordinal order and are not collected by the store.

The callback is synchronous and may not re-enter the store or mutate SQLite
while the read transaction is open. This lets a later compactor feed a streaming
backend builder without borrowing inactive historical vectors, but it does not
yet change the current materializing native builder, publish a replacement
payload/root, remove input segments, clean cache paths, or enable semantic
serving. Final activation must still use the existing root CAS because current
state can change after this read stream closes.

### Implemented Optional Streaming Payload Session (2026-07-22)

The optional `usearch && cgo` payload builder now has a bounded session that
reserves a known segment size and accepts vectors one at a time with dense
ordinals. It rejects overflow, underfilled finish, corrupt vectors, cancellation,
and use after close; finishing releases native state before returning the opaque
payload writer. The existing 5,000-row flush API is a compatibility adapter over
that same session, so default CGO-free builds and flush callers are unchanged.

This removes the future compactor's Go source-vector slice and a second payload
copy. It does not make USearch's in-memory graph or its final serialized payload
buffer streaming; those peak-memory costs remain measured native-backend gates.
No compactor invokes the session yet, and it does not publish a root, alter
cache state, or enable semantic serving.

### Implemented Bounded Physical Compaction (2026-07-22)

`internal/semanticbuild.Compact` now composes the active-root snapshot, pure
planner, CAS-checked live-member stream, and injected streaming payload builder
into one bounded singleton/pair rewrite. It creates/reopens replacement
segments, keeps unselected immutable segments in the new root, reopens that
root, and activates only through the existing equal-snapshot CAS contract.
Only replacement memberships are inserted; retained segment membership is
reused as immutable catalog state.

If the live stream differs from the selected plan or activation loses its CAS,
the old root remains active and any completed output is unreferenced. The
executor refuses the last-segment-to-exact-L0 transition because the present
root format requires at least one segment. It does not expose a command, remove
input/cache paths, retain rollback roots, or enable semantic serving.

### Implemented Optional Native Root Loader (2026-07-22)

The tag-gated native adapter can now open an immutable root only by reopening
the root and every referenced segment through the content-addressed checksum
validators, checking the USearch backend/dimensions, and importing each payload
into a closeable native index. Any manifest, checksum, backend, dimension, or
import failure rejects the complete root. A separate non-wired candidate gate
now resolves native ordinals through those immutable member maps, uses
approximate order only to cap a 190-row SQLite candidate read, CAS-checks the
active root/purge/snapshot and exact member tuple, drops stale rows, and exactly
reranks surviving vectors by cosine distance. It does not search L0, implement
adaptive expansion or parent diversity, acquire the planned reader lease, or
construct the application searcher; semantic serving remains disabled.

The storage layer also has a bounded exact-L0 read. It CAS-checks the same root
facts and returns only current ready rows with no exact active-root membership;
it rejects an over-limit L0 rather than silently truncating it. Native/L0 merge
and runtime backend selection remain deliberately unwired.

The runtime now selects exact search only when no active root exists. An active
root requires the optional tagged backend: a default CGO-free binary reports it
unavailable and fails open to lexical retrieval, while `usearch && cgo` opens
the verified configured-cache root and supplies the internal native-plus-L0
searcher. This does not itself build a root, install a native library, or
approve production semantic activation.

### Query Lifecycle

A normal semantic search:

1. embeds the query once
2. acquires a shared generation lease, opens one SQLite read snapshot, and pins
   and validates the active root manifest recorded by that snapshot
3. searches active segments with bounded parallelism
4. streams and exactly scores bounded L0 from the same snapshot
5. unions segment and L0 candidates and retrieves their authoritative float32
   vectors from SQLite
6. exactly reranks the union by cosine distance
7. validates each candidate's embedding status, chunk hash, projection state,
   parent eligibility, and active segment or L0 membership
8. adaptively expands, filters, and diversifies the valid candidates
9. hydrates and rechecks final candidates inside the same SQLite snapshot, then
   releases the snapshot and shared lease

Each segment initially returns at least the requested semantic depth. Filter
and parent-diversity expansion remains bounded by the global examined-hit
budget. Diagnostics separate segment search, L0 search, exact reranking, and
SQLite rejection costs.

The read snapshot is the ordinary-update linearization point. “Immediately
ineligible” means every query whose snapshot begins after the authoritative
dirtying commit excludes that parent. A query already holding an older snapshot
may finish against that coherent version. Explicit purge is stronger: its
exclusive lease waits for all such readers before removing representations.

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
- active L0
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
- `catching_up`
- `degraded_blocked`
- `stale`
- `corrupt`
- `ready`
- `unavailable`

Status reports separate projection, embedding, and index sections. It includes
expected parents, current parents, empty parents, blocked/error/pending
parents, chunk count, ready/pending/blocked/error embeddings, due and scheduled
retries, active generation, source snapshot revision, L0 count and age, segment
count and size-class distribution, compaction debt, tombstone counts,
dirty-parent count and age, manifest health, and backend eligibility.

### Normal On-Mode Gate

Normal `on` retrieval is fully `ready` only when:

- every eligible parent has terminal current projection state
- no projection error or pending parent exists
- every current chunk not classified terminal-blocked has a current embedding
  row for the configured profile
- no embedding is pending or has a retryable error, whether due or scheduled
- at least 99.9 percent of chunks are ready
- no more than 0.1 percent are terminal blocked, every blocked reason is
  classified, and at least 99.9 percent of chunkable parents have one ready
  chunk
- a validated active root generation exists for the current profile and purge
  epoch
- every ready embedding is covered by exactly one active segment or bounded L0
- L0 is below its 5,000-vector target
- aggregate active tombstones are no more than one percent
- segment fanout and resource projections remain inside measured gates

Temporary foundation exception: before segmented ANN and membership manifests
ship, a complete and provenance-valid profile with at most 25,000 current ready
embeddings is `ready` through bounded exact search even though it has no active
root. A complete profile above 25,000 is `needs_index` and is not searchable.
The 25,000-vector value is a measured hard safety ceiling. Configuration may
select a smaller exact-search cap, but a larger configured value must be clamped
for readiness admission and query execution and cannot authorize a larger scan.
Any claimed legacy active root is `corrupt` in this phase because the current
schema cannot prove its source revision, purge epoch, membership hash, or
segment manifest. This exception is removed when the segmented lifecycle can
persist and validate those facts; it does not weaken the eventual active-root
gate above.

Failure of a full-readiness invariant enters `catching_up` only when every
bounded catch-up invariant below holds. Otherwise it disables only the semantic
lane and records a precise reason. Returned lexical evidence and ordering then
remain identical to semantic `off` behavior.

`ready` means projection and embedding work has reached the status watermark,
the active root is valid, L0 is within its target, and compaction has no overdue
work. `ready` may include classified terminal blocks only while every block is
reviewed and the 99.9/0.1-percent coverage gates pass. `degraded_blocked` is
diagnostic and ineligible for normal semantic retrieval.

### Bounded Catch-Up Admission

Continuous append must not make the first new item disable the entire semantic
lane until a human runs maintenance. `catching_up` is therefore eligible for
normal retrieval only when all of these initial limits hold:

- at most 500 dirty or unprojected parents
- at most 2,500 total not-ready chunks, combining exact dirty-parent occurrence
  plans with every current missing, stale, pending, retryable-error, or
  scheduled-retry embedding row not already counted by those dirty parents
- the oldest projection or embedding debt is no more than 30 minutes old
- L0 no larger than 10,000 vectors
- aggregate active tombstones no more than two percent
- every dirty existing parent is excluded immediately from segment, L0, and
  hydration results
- all newly imported or dirty evidence remains available to lexical retrieval
- no projection or embedding error is corrupt or unclassified; retryable work
  counts against the same age and count limits
- the active root, remaining segment memberships, profile, and purge epoch are
  otherwise valid

The estimate is deterministic and uses exact capped chunker-v3 occurrence
planning. Status and admission load dirty parent evidence through the same
immutable SQLite read transaction, run the production v3 boundary planner, and
stop as soon as the shared 2,501 over-budget sentinel is reached. A parent's
estimate is its exact planned occurrence count and, for an existing parent, is
never lower than its last current chunk count. Planner cancellation, resource
failure, or input beyond the authoritative readiness planning ceiling fails
semantic admission closed while lexical retrieval remains available.

The planner does not materialize the whole parent before it can discover the
sentinel. An allocation-free, cancellation-aware preflight scans at most 128
MiB of normalized UTF-8 and can prove dense oversized input over budget.
Section metadata is independently capped at 4,096 entries before allocating
the duplicate-key map, with cooperative cancellation checks during metadata
validation. Exact rune, anchor, and window materialization is separately capped
at 8 MiB. Sparse input above 8 MiB that preflight cannot prove over budget
fails closed; the 128 MiB preflight ceiling is not an allocation budget for
exact planning.

The byte-ratio proposal was rejected with restored-corpus evidence. Chunker v3
can guarantee only one byte of forward progress because natural boundaries may
be dense. Dividing by that guarantee would classify 14,197 of 34,180 parents
(41.5 percent) above the 2,500 budget, while exact v3 planning classifies zero
current parents above it. The observed distribution was p50 1,344 bytes / 2
occurrences, p90 15,268 / 18, p95 23,796 / 28, p99 72,325 / 85, and maximum
1,881,497 / 2,208. Chunker v3 still publishes its 1,800-byte ceiling,
zero-byte maximum overlap, and one-byte minimum guaranteed advance as truthful
versioned constants; readiness does not misuse the minimum as an estimator.

A single parent estimated above the 2,500-chunk catch-up budget makes normal
semantic retrieval ineligible until it is projected and embedded; it is not
divided into misleadingly small queue rows. Giant-job staging remains bounded
and resumable even while the semantic lane is lexically failed open.

While `catching_up`, fusion preserves at least the first three distinct lexical
parents unless they violate an explicit filter. This prevents recently
imported lexical-only evidence from being displaced merely because older
evidence appears in both lexical and semantic lanes. Status and traces label
the result `catching_up` and record omitted parent and estimated chunk counts.

Crossing any catch-up age, count, L0, tombstone, segment-health, or resource
limit disables only the semantic lane and records the exact reason. Lexical
evidence and ordering then remain identical to semantic `off` behavior. These
limits may become configurable only within a separately benchmarked safe
range; the first release does not expose arbitrary overrides.

Ordinary runtime admission uses transactionally maintained projection-ledger
and per-profile embedding-status counters rather than the full status joins.
Counter triggers are installed before an authoritative backfill in the same
SQLite write transaction. Admission reads one immutable transaction, requires
the dirty-age and dirty-parent keyset indexes, plans no more than 500 dirty
parents, and validates profile/current rows only when both fit the immutable
25,000-row exact cap. Missing required indexes, structurally impossible
counters, cancellation, or validation failure fail closed before provider
construction. Ordinary request admission has a fixed 250 ms budget. Exhausting
that budget records semantic readiness as unavailable and returns the unchanged
lexical path without constructing or calling the query embedding provider. The
full status/maintenance path scans authoritative rows and marks the state
`corrupt` when stored counters drift. Counts above the exact cap
are rejection-only summaries; they are not ANN membership counters and cannot
make a large profile searchable.

All other non-disabled readiness states are ineligible for normal semantic
retrieval.

### Shadow And Diagnostics

Normal shadow comparisons record `ready` and `catching_up` separately. Only
`ready` runs count toward complete-profile quality gates. Catch-up runs may be
used to evaluate freshness and lexical-preservation policy, but not ANN recall
or full-corpus relevance. An explicit diagnostic command may use state outside
the catch-up gate with `--allow-partial`; its output is labelled
`partial_coverage` and cannot be fed into synthesis or counted as a quality
evaluation.

Runtime construction enforces admission before query embedding or semantic
search. CLI status is not the security or correctness boundary.

## Generation Concurrency And Purge

The existing cross-platform `internal/runlock` package will be extended with
two database-scoped lock files under
`<cache>/semantic/<database-id>/locks/`:

- `maintenance.lock` is shared by authoritative write transactions that can
  create, change, or delete a projected parent and exclusive for projection
  staging/apply, embedding batches, flush, compaction, full build, repair,
  garbage collection, and purge across every profile
- `generation.lock` is shared by queries and exclusive for root activation or
  any segment/root deletion

Lock order is always maintenance first, then generation. Queries acquire only
the shared generation lease and hold it through hydration and final SQLite
validation. An authoritative writer holds shared maintenance through its
SQLite transaction. A semantic maintenance writer performs one bounded
projection, embedding, or ANN unit while holding maintenance exclusively. ANN
writers additionally acquire generation exclusively for publication and
activation. Purge acquires maintenance exclusively and then generation
exclusively before enumerating cache state. Lock upgrade is prohibited.

Acquisition is context-cancellable and bounded by the query timeout or command
`max-duration`; timeout returns an explicit busy/unavailable state and never
continues unlocked. Process exit releases OS locks. In-process RW mutexes mirror
the same order and prevent close/delete races around loaded graphs.

`internal/runlock` also provides a database-scoped, cross-process
writer-preference turnstile for both maintenance and generation locks. Before
waiting for either lock exclusively, a writer atomically publishes a FIFO
intent ticket for that lock under a short coordinator lease. New authoritative
writers use the maintenance intent queue; new queries use the generation intent
queue. Each shared acquirer checks its queue, acquires the shared lock, and
rechecks before beginning work; if intent appeared in that interval, it releases
and waits rather than barging. Existing shared holders drain, the oldest live
exclusive ticket acquires its lock, and intent remains published until that
holder releases or hands off to the next ticket. An ANN writer publishes
generation intent after taking exclusive maintenance and before waiting for
exclusive generation.

Tickets carry operation nonces and crash-releasing OS leases; stale-ticket
cleanup is permitted only while holding the coordinator lease and proving the
owner lease is no longer held. Continuous authoritative writes cannot starve
refresh, continuous queries cannot starve activation or purge, and a crashed
exclusive waiter cannot block either class of shared work forever.

Projection staging/apply transactions, embedding batches, and ANN publications
record the global purge epoch they began under and refuse their final commit if
it has changed. This epoch fence is defense in depth; the exclusive maintenance
lock is the first-release boundary that prevents those producers from racing a
purge across processes.

Every authoritative writer also checks `retrieval_purge_suppressions` inside
the fenced write transaction before inserting or updating a parent. A
suppression stores only SHA-256 over a version tag and length-prefixed canonical
parent kind and upstream identity, plus policy timestamps. This is the minimal
non-searchable policy tombstone needed to prevent re-import; it does not retain
title, URL, text, excerpts, embeddings, or the raw identity, and it does not
claim unlinkability from someone who already knows a candidate identity. Purge
publishes the suppression before authoritative deletion, so a later sync cannot
silently recreate forgotten evidence. Removing a suppression and allowing
re-import is a separate explicit operation, never an effect of ordinary sync.

Builders register their operation ID, purge epoch, and temporary paths before
writing. Because purge owns the exclusive maintenance lock, no authoritative,
projection, embedding, or ANN writer can create, stage, copy, commit, or publish
representations concurrently.
Purge enumeration includes registered temporary builds, published roots,
referenced and unreferenced segments, rollback roots, garbage-collection
candidates, and purge staging. This closes the orphan-file gap rather than
relying only on activation-time epoch rejection.

Purge deletes every registered temporary output created under an older epoch,
whether or not an incomplete file has enough metadata to test membership. Only
completed immutable segments use Bloom prefiltering and exact map inspection.

Ordinary content changes do not require immediately removing immutable segment
files. Old chunk IDs are rejected by current SQLite validation, new chunks
enter L0, and tombstone thresholds schedule bounded compaction before
degradation grows unbounded.

Explicit purge is different. Before purge reports success, dbrain must:

1. take the database-wide maintenance lock exclusively and then the exclusive
   generation lease, waiting for every authoritative writer and semantic
   producer that began earlier
2. use parent-identity Bloom filters only as a prefilter, then exact immutable
   parent indexes to identify every current or historical membership in every
   published, retained, or unreferenced segment in every profile for the
   purged parents; registered temporary output from the old epoch is affected
   in full
3. create or resume a durable `retrieval_purge_operations` journal row that
   records a target-set hash, affected profiles, roots, segments, old and new
   global purge epochs, and current stage; normalized journal child rows retain
   the exact target parent identities, discovered physical membership tuples,
   and affected paths until completion
4. commit a short SQLite transaction that advances the database-wide purge
   epoch for every profile, inserts the target suppression digests, deactivates
   every old-epoch root, and permanently invalidates every root in every profile
   that references an affected segment
5. atomically rename affected segment and root directories into
   `<cache>/semantic/<database-id>/.purge/<operation-id>/`; that `0700` staging
   path is never scanned, opened, adopted, or treated as a cache generation
6. durably delete every staged cache file using the release-platform adapter:
   sync both source and staging parents after each rename, unlink files, sync
   containing directories after their contents change, remove directories
   bottom-up, and sync each surviving parent through the semantic cache root;
   retained quarantine is not permitted when the operation reports success
7. commit the authoritative purge transaction, including deletion of the
   parent's chunks, embeddings, projection staging, and affected
   segment-membership rows
8. verify no retained root, segment map, active membership join, exact row, or
   purge-staging file can return or contain any journalled target parent or
   physical membership
9. mark the journal row complete, transactionally remove its sensitive child
   rows while retaining the set hash and stage audit, and release the lease

Every stage is idempotent. On startup and before any semantic maintenance,
dbrain checks for an incomplete purge journal. Semantic retrieval remains
disabled for every affected profile until the operation is resumed or
explicitly repaired. A crash before authoritative deletion leaves the item
authoritative but semantically unavailable; a crash afterward resumes deletion
verification from the persisted exact target and membership rows, then
completes. No recovery path may decrement the global purge epoch or reactivate
an old-epoch root.

If cache deletion or verification fails, purge fails. It never reports success
while a root, segment, staging file, or exact row contains the purged
representation. A failure after root invalidation may leave semantic retrieval
disabled until repair, but it cannot reactivate an old-epoch root.

The deletion adapter must implement and test the strongest durable-directory
operations available on every supported release platform. If a platform cannot
provide the required rename, unlink, and parent-directory durability contract,
privacy purge is reported unsupported there rather than claiming success. Crash
and simulated-I/O failpoints cover every rename, unlink, directory sync,
verification, and final journal-cleanup boundary. Exact journal child rows are
removed only after the last cache-root sync and verification have completed.

Purge does not synchronously rebuild affected segments. After success, lexical
retrieval remains complete and `semantic refresh` rebuilds missing semantic
coverage from the remaining SQLite rows. The 200,000-vector cap bounds each
repair unit, not aggregate repair: one parent may occur in every segment, so a
purge may invalidate and eventually rebuild the entire semantic corpus. That
work remains a sequence of resumable capped jobs and semantic retrieval stays
lexical-only until its readiness gates pass.

The first release retains the current and immediately previous valid root
generation only. Their unchanged content-addressed segments are shared.
Compaction input segments remain until the previous root is released or
expired; all other unreferenced segments are garbage-collection candidates.
Automatic unbounded retention is prohibited. Cleanup is explicit,
reference-aware, and lease-protected; status reports root count, unique segment
count, unreferenced bytes, and total disk use.

## Production Operations

### Commands

The operational surface will provide:

```text
dbrain semantic status
dbrain semantic chunk --until-idle --max-duration <duration>
dbrain semantic embed --until-idle --max-duration <duration>
dbrain semantic verify --limit <rows> --resume
dbrain semantic verify --repair-counters --limit <rows>
dbrain semantic index build --full
dbrain semantic index flush
dbrain semantic index compact --max-duration <duration>
dbrain semantic index verify
dbrain semantic index gc [--dry-run]
dbrain semantic refresh --max-duration <duration>
dbrain semantic query --allow-partial
```

`semantic refresh` captures the current monotonic projection-work revision as
fixed watermark `W` and performs bounded maintenance through that work set:

1. project dirty parents
2. embed due chunks in bounded provider batches
3. cap provider requests and persistence batches at 5,000 rows; before
   committing a batch larger than the remaining 10,000-row L0 headroom, flush
   at least 5,000 current L0 rows and then continue embedding
4. verify readiness counters
5. compact at most the bounded work that fits the remaining duration
6. verify the active root and report final readiness and remaining debt

At start, it selects parents whose `dirty_revision <= W` and
`projected_revision < dirty_revision`, plus embedding work already due. Every
checkpoint records the selected parent and dirty revision. Projection outputs
created for that work item remain owned by the run and are eligible for its
embedding and L0 flush stages even though their row revisions are newer than
`W`. A parent re-dirtied above `W` fails the old work item's final validation
and remains pending for the next run. Unrelated work above `W` is never pulled
into the run. Continuous ingestion therefore cannot prevent one refresh from
completing. The command never changes `research.semantic.mode` and is never
launched by research, MCP, or web chat.

The 10,000-vector value is a serving and steady-state admission cap, not a limit
on an initial semantic-off backfill with no active root. During steady-state
refresh, embedding and flush are interleaved as above. Because a provider batch
is no larger than the 5,000-row flush target, any batch that exceeds remaining
headroom always has a legal flush transition. If the duration budget cannot
finish that flush, refresh stops before the provider call and records its resume
checkpoint; no batch commits above the serving cap. Every successful run or
resume advances at least one parent window, provider batch, flush, or compaction
checkpoint; external provider failure is reported separately from starvation.

Normal imports remain import-only and do not perform model work by default.
After explicit rollout approval, configuration may set semantic maintenance to
`research.semantic.maintenance: after_sync`; then `sync all` invokes the same
bounded refresh after imports complete. The default is `manual`. The configured
maintenance duration bounds the post-sync work. Sync releases every shared
authoritative-write lease before invoking refresh; it never upgrades the lock.
This adds no daemon or resident watcher, and query paths never initiate
maintenance.

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
- per-segment ANN search and aggregate segment fanout
- exact-L0 search
- authoritative exact candidate reranking
- candidate SQLite validation
- hydration
- parent diversity and consolidation
- RRF fusion
- total retrieval

Record these resource and health values:

- projection and embedding coverage
- blocked/error reason counts
- segment and L0 vector counts
- segment size-class distribution and fanout
- per-segment and aggregate tombstone ratios
- flush and compaction vector rewrite counts
- flush, compaction, and full-build time and peak RSS
- active, retained-shared, unreferenced, and peak-compaction disk bytes
- loaded steady RSS
- dirty-parent count, estimated dirty chunks, and oldest dirty age
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
4. hybrid using segmented ANN plus exact L0

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
expected source key. Each positive case also records its accepted source set
and maximum acceptable rank, so “top-k” is not a prose judgment. Both
planner-assisted and deterministic planner-disabled runs are included where
planning can affect the result.

### Quality Gates

Before opt-in production use:

- all existing lexical-only evals pass
- shadow evidence and ordering are byte-for-byte identical to off
- protected-anchor retention is 100 percent
- the direct Cerebras source ranks in the top five
- aggregate semantic-case recall@10 improves by at least 0.05 absolute over
  lexical and aggregate MRR is no lower than lexical
- no reviewed drift case gains an irrelevant semantic-only result in the top
  five
- no parent contributes more than the configured three-chunk cap to the
  semantic candidate window
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
- active unique segment bytes use no more than 2 GiB at the restored-corpus
  profile dimensions
- active plus previous-root unique segment bytes and one worst-case compaction
  fit within 4 GiB of cache
- current-corpus query fanout is no more than eight ANN segments
- compaction rewrites no more than the selected input size class plus its
  replacement output

Those byte gates are acceptance limits for the restored production corpus, not
claims of unlimited scale. Growth follows explicit backend envelopes:

- up to 25,000 vectors: bounded exact search is eligible
- above 25,000 and below one million vectors: segmented float32 ANN is the
  intended default if measured gates continue to pass
- from one million through three million vectors: a new bakeoff must approve
  lower dimensions, quantized or disk-resident segments, parent-first
  retrieval, or a measured combination before growth enters that band
- above three million vectors: flat global float32 chunk ANN is ineligible
  without a separately accepted design; the expected direction is global
  parent or section retrieval followed by bounded chunk search

Status projects raw vector bytes, active segment fanout, next-compaction peak
disk, and estimated loaded RSS against the next envelope. It warns before a
threshold is crossed and fails semantic open to lexical rather than operating
outside an accepted envelope.

If no pure-Go segmented ANN candidate meets the current gates, the backend is
rejected. The authority and hybrid architecture remain intact while the
conditional two-stage parent/chunk design is specified separately.

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
- build the initial size-tiered segments directly from one snapshot
- activate, reopen, and verify the root generation and every referenced segment
- run a projection/embedding catch-up pass
- confirm full readiness, SQLite integrity, lexical availability during
  builders, and the evaluation/resource gates

SQLite WAL and parent/batch-sized transactions keep read-only research
available. No corpus-wide write transaction is allowed.

### Phase 3: Shadow

- change only `research.semantic.mode` to `shadow`
- freeze the embedding profile, chunker, ANN backend, format, and build
  parameters during the observation window; allow normal verified root
  succession as L0 flushes and compaction replace the active root
- label every comparison with its root generation and retain the current and
  immediately previous valid roots under the ordinary rollback policy
- collect at least seven days and 50 `ready` shadow comparisons; if natural
  traffic does not reach 50 within 21 days, run the reviewed production eval
  cases to reach 50 and label those repetitions synthetic
- exercise at least two L0 flushes, one compaction, and one bounded catch-up
  interval under normal imports
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

A bad active root rolls back by atomically reactivating the previous validated
root if its profile, database identity, purge epoch, indexed-membership hash,
and every referenced segment manifest still match. Unchanged segments are
shared; compacted input segments remain available only while that rollback
root is retained. Otherwise semantic retrieval remains unavailable and lexical
retrieval continues.

A restored or replaced SQLite database rejects cache roots whose database
identity, indexed-membership hash, source snapshot revision, purge epoch, or
referenced segment manifests do not match. It never adopts a root or segment merely
because the cache path exists.

Profile changes are blue/green: build complete embeddings, segments, and a root
for the new profile, verify them, then switch configuration. Segments never
mix profiles. The old profile remains available for rollback.

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
- stable section keys, duplicate-key rejection, content-defined
  resynchronization, identical-window deduplication independent of occurrence
  offsets, and the eight-chunk local-edit churn cap
- deterministic catch-up estimates and giant-parent resume checkpoints
- segment/L0 merge ordering and authoritative exact rerank
- size-class selection, singleton cleanup, and deterministic terminating
  mixed-class compaction planning across every boundary total
- embedding batch/L0 transition planning at every headroom boundary
- adaptive overfetch, filters, parent caps, and protected-parent behavior
- bounded progress serialization
- root, segment-manifest, segment-membership, and checksum validation

### Store And Migration Tests

- fresh and existing databases create projection/profile state safely
- migration-history repair behavior follows append-only policy
- migration seeds every eligible parent pending
- projected-field changes dirty exactly the affected parent
- irrelevant field changes do not create semantic churn
- projection work revisions are monotonic; an older staged apply cannot clear a
  parent re-dirtied at the same timestamp or at a newer revision
- projection state, chunk replacement, and occurrence replacement are atomic
- giant-parent staging remains non-searchable until atomic apply and blocks at
  the 50,000-chunk hard limit
- empty parents reach terminal state
- embedding batches commit atomically and allocate one revision
- tombstone, segment-membership, and L0 counters remain correct across replace,
  retry, block, flush, compaction, and delete transitions
- segment membership, including historical parent provenance, reconstructs
  exactly from immutable segment maps after current chunks are replaced
- purge suppression digests block every authoritative re-import path until a
  separate explicit allow-reimport operation
- read-only open performs no migration or maintenance work

### Integration Tests

- runtime admits bounded catch-up and refuses excessive dirty age/count,
  oversized L0, stale, and corrupt profiles
- shadow cannot alter returned evidence or synthesis
- normal corpus changes produce L0 results without disabling valid segments
- catch-up preserves the first three eligible lexical parents
- a one-byte body edit in the 26,512-chunk parent changes no more than eight
  chunk identities and does not overflow L0
- stale segment candidates are rejected through current SQLite validation
- interrupted flush and compaction leave the prior root active
- publication failpoints before and after every payload sync, manifest sync,
  rename, parent-directory sync, and SQLite activation never expose partial
  output
- activation rejects a changed purge epoch, prior root, membership hash, or
  segment manifest
- missing generation follows exact-cap or lexical fallback policy
- giant-parent search produces a parent-diverse lane
- selective filters route to bounded exact search
- broad filters expand ANN candidates without leaking disallowed rows
- shared readers, the maintenance writer, and exclusive activation/purge
  leases do not race
- canonical lock ordering, cancellation, timeout, process-crash release, and
  upgrade prohibition hold across separate test processes
- under continuous authoritative writes, published writer intent prevents new
  shared transactions from barging and every live exclusive maintenance ticket
  acquires or times out within its declared bound; crashed intent is recovered
- under continuous semantic queries, published generation intent prevents new
  query leases from barging and root activation or purge acquires the exclusive
  generation lease within its bound; crashed generation intent is recovered
- purge waits for authoritative writers and excludes projection staging/apply,
  embedding batches, and ANN builders across separate processes; every semantic
  producer also rejects a commit after an observed purge-epoch change
- an in-flight import completes before deletion, a later import is suppressed,
  and ordinary sync cannot recreate a successfully purged parent
- purge removes affected registered temporary output as well as published
  segments
- purge invalidates every affected root and removes every affected segment
  before success
- purge finds historical tombstoned memberships by parent provenance after the
  current chunk and embedding rows have already been replaced
- crash injection at every purge journal stage resumes idempotently and never
  loses its exact verification targets or reactivates an old global purge epoch
- power-loss and I/O failpoints through every purge rename, unlink, parent sync,
  verification, and journal cleanup boundary never permit success while bytes
  can reappear
- purge invalidates inactive and rollback profiles as well as the active
  profile
- restored older SQLite state rejects a newer root or mismatched shared segment
- an initial build, two L0 flushes, and geometric compaction preserve exact
  coverage without duplicate membership in the active root join

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
2. **Scalable segmented-index lifecycle**
   - lean exact APIs and revision state
   - backend bakeoff and selected ANN adapter
   - immutable segments, content-addressed storage, root manifests, and segment
     membership
   - exact L0, flush, size-tiered compaction, leases, garbage collection, and
     purge
   - adaptive filters, exact reranking, and parent diversity
3. **Production evaluation and benchmarks**
   - reviewed local cases and relevance judgments
   - lexical/exact/ANN/hybrid comparison runner
   - latency, recall, diversity, memory, and disk reporting
4. **Operational rollout and rollback**
   - explicit and opt-in after-sync bounded refresh workflow
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
5. Runtime `on` searches only complete state or explicitly bounded
   `catching_up` state; it refuses excessive dirty age/count, retry-due,
   oversized-L0, stale, corrupt, or mismatched state.
6. Production-sized immutable ANN segments and a root manifest can be built,
   validated, atomically activated, reopened, queried, compacted, and rolled
   back under `CGO_ENABLED=0`, including the publication failpoint matrix.
7. Ordinary new or changed evidence remains searchable through bounded exact
   L0 without invalidating unaffected segments, and bounded refresh cannot be
   starved by continuous ingestion or restart giant-parent projection from its
   beginning after every duration boundary; each successful resume advances a
   durable unit checkpoint.
8. Every ANN candidate is revalidated against current SQLite state before it
   becomes evidence.
9. Filters and parent skew cannot silently empty or monopolize the semantic
   lane.
10. Explicit purge makes database rows, segment memberships, root manifests,
    purge-staging files, and every retained segment in every profile containing
    a current or historical representation of the purged parent unreachable
    before reporting success; every interrupted stage resumes idempotently
    from durable exact verification targets. Only the documented non-searchable
    suppression digest remains, and ordinary import cannot recreate the parent
    while it exists.
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
- mutable in-place HNSW
- quantized or disk-resident production segments before the scale-envelope
  bakeoff requires them
- parent-first hierarchical retrieval before the one-million-vector bakeoff or
  earlier flat-segment recall/diversity/resource failure requires it
- simultaneous active chunker projections

## References

- `docs/superpowers/specs/2026-07-18-retrieval-first-hybrid-search-design.md`
- `docs/research-harness.md`
- <https://github.com/coder/hnsw>
- <https://github.com/hupe1980/vecgo>
- <https://github.com/tursodatabase/turso>
- <https://github.com/tursodatabase/turso/blob/main/docs/manual.md>
