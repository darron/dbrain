# Retrieval-First Hybrid Search Design

## Status

Approved on 2026-07-18. This is the build-ready architecture for improving
`dbrain` retrieval with deterministic chunking, local embeddings, and a
rebuildable local approximate-nearest-neighbour index while preserving SQLite
as the authoritative database.

The design deliberately does not introduce an ontology engine or replace
SQLite. It keeps both options open through stable evidence identifiers,
portable embedding records, and replaceable retrieval interfaces.

## Summary

`dbrain` should add semantic retrieval as a second, optional ranked lane beside
its existing SQLite FTS5 retrieval. Lexical and semantic lanes retrieve
independently, retain their own ranks and provenance, and are combined with
reciprocal-rank fusion (RRF). Existing exact metadata and exact-tag evidence
remain a protected floor outside RRF so exact anchors and directly matching
evidence cannot be displaced by vague semantic neighbours.

SQLite remains authoritative for raw evidence, deterministic chunks, embedding
metadata, portable vector bytes, and index-generation metadata. A local
HNSW-style ANN index lives under the configured cache directory and is always
rebuildable. Exact vector scanning in Go is the correctness baseline, small
corpus implementation, and recovery fallback.

The semantic lane rolls out in three stages:

1. shadow comparisons that cannot change user-visible results
2. explicit opt-in through the existing semantic controls
3. default hybrid retrieval only after quality, latency, and operational gates
   pass

Local embeddings are the default. Hosted embeddings remain explicitly opt-in
and must retain exact provider and model provenance.

## Boundary And Evidence

This design covers the production brain served over HTTPS and the repository
implementation that powers its research surfaces.

The production corpus was inspected through the configured HTTPS MCP endpoint,
not through the development database or direct SQLite access. At the
2026-07-18 snapshot it contained 20,545 items and 13,373 sources. Those counts
are sizing evidence, not hard-coded product assumptions.

The motivating production regression was the query:

> What can we learn from the Cerebras articles about their new knowledge base
> system and ontology, and apply to dbrain?

The exact Cerebras article, `src:458528e78013`, ranked tenth behind generic
knowledge-base material. The planner also treated low-information words such
as `learn`, `articles`, `new`, `knowledge`, `base`, and `system` as required
concepts. This is the failure class the first regression eval must capture.

Relevant saved-corpus sources:

- Cerebras, [How we built our knowledge base](https://www.cerebras.ai/blog/how-we-built-our-knowledge-base), `src:458528e78013`
- Atya critique of the Cerebras system, `x:2078257946044018805`
- Sentra, *Memory Is State, Not a Service*, `src:645943bbcd66`
- *What Building a Company Brain over the last Year Taught Me*, `src:d0b044658ee3`

The repository already has the intended seam:

- `internal/researchhybrid` declares lexical, semantic, and exact-tag lanes.
- CLI, MCP, eval, runner, and core research options already carry
  `UseSemantic` and `DisableSemantic`.
- `retrieval.EvidenceDocument` already supports chunk metadata and retrieval
  lane provenance.
- `docs/research-harness.md` Phase 6 calls for chunk-level evidence and Phase 7
  calls for optional local hybrid retrieval.

This design completes those existing contracts rather than adding a parallel
research system.

## Problem

Lexical retrieval is indispensable but insufficient by itself. It performs
well for identifiers, URLs, names, quoted phrases, and rare terminology, but it
can miss evidence that expresses the same idea with different words. It can
also overvalue generic query terms when the query planner misclassifies user
instructions as required evidence concepts.

Long sources create a second problem. Source-level summaries and large raw
extracts can contain the answer while failing to rank because the relevant
passage is a small part of the document. Feeding an entire source into the
synthesis window is also wasteful and makes evidence inspection harder.

Semantic search alone is not a solution. It can retrieve plausible but weakly
related material, blur exact identities, and make relevance failures difficult
to explain. The design therefore needs independent retrieval lanes,
inspectable fusion, and a lexical-only fallback.

## Goals

1. Improve recall for paraphrases and conceptually related passages without
   weakening exact-name, tag, URL, source-key, or quotation retrieval.
2. Retrieve relevant windows from long sources while preserving their parent
   source identity and raw evidence.
3. Keep raw data, chunks, embeddings, and index state distinguishable and
   independently rebuildable.
4. Keep SQLite as the authoritative local-first database.
5. Keep the implementation Go-first, embedded, and compatible with the normal
   single-binary and Homebrew distribution model.
6. Make every winning result explainable through lane, rank, match, fusion,
   and evidence-role metadata.
7. Roll out against production-derived evals before changing default answers.
8. Preserve future attachment points for entity and ontology information
   without making ontology work part of retrieval v1.

## Non-Goals

- No SQLite replacement or Turso dependency.
- No external vector database, hosted sidecar, or required daemon.
- No hosted embedding calls by default.
- No ontology extraction, graph reasoner, or entity-resolution rewrite.
- No model-generated prose stored as authoritative evidence.
- No replacement of FTS5 with vector search.
- No semantic retrieval requirement for normal imports or basic brain use.
- No first-release cross-device vector-index synchronization.
- No required local reranking model in the first hybrid release.
- No migration number reserved in this document. Implementation must inspect
  current, `main`, and work-in-progress migration history before assigning one.

## Hard Implementation Constraints

- Release builds must remain `CGO_ENABLED=0` across the existing Darwin
  amd64/arm64, Linux amd64/arm64, and Windows amd64 matrix.
- The semantic feature must not require a helper binary, sidecar, native
  library, or runtime dynamic library.
- A purged parent must become unreachable through every retrieval lane before
  the purge operation returns.
- Read-only MCP and status surfaces must continue to open older databases that
  do not yet contain retrieval tables.
- Restoring or replacing SQLite must never make a mismatched cache generation
  eligible merely because its path still exists.

## Approaches Considered

### 1. SQLite Authority Plus Rebuildable Local ANN Index

This is the selected approach.

SQLite stores chunks and portable embedding records. An embedded Go index
implementation builds an HNSW-style index under `Config.CacheDir`. Retrieval
uses exact scanning when the corpus is small or the ANN index is unavailable.

Advantages:

- preserves the current authority and backup boundary
- provides scalable semantic retrieval without a service dependency
- keeps exact search available as a correctness oracle
- permits the ANN implementation to change without a database migration
- supports shadow operation and deterministic lexical fallback

Costs:

- requires explicit index generation and recovery lifecycle
- duplicates vector data between SQLite and the derived index
- requires a carefully selected Go ANN dependency

### 2. Exact Vector Scans Only

This is retained as a baseline and fallback, not selected as the production
default at the projected chunk count. It has the smallest implementation and
is ideal for tests, correctness comparisons, and small installations, but its
query cost grows linearly with every embedded chunk.

### 3. External Vector Service Or Sidecar

This is rejected. It would add deployment, authentication, versioning,
availability, backup, and privacy boundaries that are disproportionate for a
local-first single-binary system.

### 4. Replace SQLite With Turso Database

This is deferred. The Rust rewrite offers useful vector representations,
incremental-state ideas, and concurrent-write work, but it does not currently
provide the ANN capability needed by this design. Its FTS implementation is
also not SQLite FTS5-compatible. Adopting it would turn a retrieval upgrade
into an authoritative-database, migration, build, and recovery project.

## Architecture

```text
authoritative items and sources
             |
             v
deterministic evidence projection
             |
             v
retrieval_chunks in SQLite
        |                    |
        |                    v
        |             local embedding provider
        |                    |
        |                    v
        |        retrieval_embeddings in SQLite
        |                    |
        |                    v
        |         rebuildable ANN generation
        |                    |
        v                    v
     FTS5 lane          semantic lane
        |                    |
        +--------+-----------+
                 |
          reciprocal-rank fusion
                 |
      exact identity/tag protection
                 |
      protected-anchor enforcement
                 |
      parent-source consolidation
                 |
      existing inspection and reranking
                 |
          research evidence pack
                 |
      existing cited synthesis path
```

MCP and the CLI remain transports. `internal/brainresearch` continues to own
research-pack construction. The semantic implementation should plug into
`internal/researchhybrid` and return normal `retrieval.EvidenceDocument`
records rather than creating a second pack type.

## Authoritative And Derived State

### Raw Evidence

Items, source extracts, transcripts, OCR, summaries, and their existing
provenance retain their current authority and semantics. Chunking and embedding
must never overwrite them.

### Chunks

Chunks are deterministic, derived evidence windows stored in SQLite so they
can be inspected, searched, embedded, invalidated, and reproduced. A chunk is
not a new source and does not receive an independent citation key. Research
continues to cite the parent item or source key while carrying chunk metadata.

### Embeddings

Embedding records are derived, but their bytes and provenance live in SQLite
because they are expensive enough to preserve and useful for exact fallback or
index rebuilding. They can be regenerated without changing source evidence.

### ANN Index

The ANN index is cache state only. It is not part of the brain backup contract.
Deleting it must cause a diagnosable fallback and a clean rebuild, never data
loss or silent empty retrieval.

The index stores vector-to-chunk-ID mappings, not standalone evidence payloads.
Every candidate must be hydrated through current SQLite rows before it can be
returned. A missing or purged chunk is therefore rejected even if an older
immutable index file still contains its vector.

### Purge And Forget Semantics

Invalidation is insufficient when a user explicitly asks dbrain to forget
content. `PurgeItemIndexedContent`, Apple Notes `--forget-excluded`, and future
equivalent purge paths must participate in the retrieval transaction.

Before a purge returns successfully, dbrain must:

1. delete all `retrieval_chunks` and `retrieval_embeddings` rows for the
   parent, rather than marking them stale
2. mark every affected active ANN generation unusable
3. make all semantic readers re-check the SQLite generation state and hydrate
   candidate IDs through current chunk rows
4. ensure exact scan cannot see the removed vectors
5. remove or rebuild immutable generation files containing the purged vector
   under a cross-process-safe reader/writer protocol

If the on-disk purge cannot complete, the purge fails rather than reporting
success while derived private state remains. Semantic retrieval fails open to
lexical-only while the profile has no valid generation. This synchronous
privacy path is distinct from ordinary stale-content rebuilding and from
optional cache garbage collection.

## Deterministic Chunking

### Input Projection

Chunking operates on explicit evidence sections rather than concatenating all
available text into one unlabeled document. The initial priority is:

1. raw source extract or raw item text
2. raw transcript or OCR as separate evidence roles
3. derived source/item summaries only as separately labeled fallback chunks

Title, heading path, source type, author, and canonical identity may be used as
compact retrieval context. They do not become part of raw quoted evidence.
User tags and resolved entities stay structured metadata rather than being
repeated throughout every chunk.

### Boundaries

Chunker v1 should:

- preserve headings and paragraph boundaries where possible
- keep short records as a single chunk
- target approximately 2,400 Unicode characters per raw chunk
- permit a hard maximum of approximately 3,600 characters
- use at most 300 characters of overlap, preferring a paragraph boundary
- split oversized paragraphs deterministically at sentence, whitespace, then
  Unicode-character boundaries
- never split or measure by raw byte offsets in a way that can corrupt UTF-8

The exact defaults are configuration constants, not public compatibility
promises. Changing their behavior requires a new chunker version so existing
chunk identities are never silently reinterpreted.

### Identity And Invalidation

Each chunk has a stable identifier derived from:

```text
parent kind
parent source key
evidence role
input content hash
chunker version
ordinal
chunk text hash
```

The row also records start and end character offsets, heading, ordinal, input
content hash, and chunk text hash. A source-content change invalidates only the
old generation of chunks and its embeddings. Unchanged chunk IDs can be reused
when a re-chunk produces the same identities.

The implementation must process updates transactionally: readers either see a
complete current chunk set for a parent or the previous complete set, never a
partially replaced set.

## SQLite Storage Model

The implementation should introduce three logical tables. Names are proposed
contracts; the implementation plan may refine columns while retaining these
semantics.

### `retrieval_chunks`

Required fields:

- `chunk_id` primary key
- `parent_kind` and `parent_source_key`
- `evidence_role`
- `ordinal`, `start_char`, and `end_char`
- `heading`
- `chunker_version`
- `input_content_hash` and `chunk_text_hash`
- `text`
- `created_at` and `updated_at`

Indexes should support current-parent lookup, stale-content detection, role
filtering, and deterministic iteration for backfills.

### `retrieval_embeddings`

Required fields:

- `chunk_id`
- `provider`, `model`, and an opaque embedding profile identifier
- `dimensions`
- `representation`: initially `dense_f32`
- `normalization`: initially `l2` or `none`
- `vector_bytes`
- `chunk_text_hash`
- `status`: `pending`, `ready`, `blocked`, or `error`
- `attempt_count`, `last_error`, and retry timestamps where applicable
- `embedded_at`

The primary key is the chunk and embedding profile. A model or representation
change creates a new profile; it does not mutate provenance on old vectors.

Dense float vectors use explicitly documented little-endian IEEE-754 float32
encoding. The schema leaves room for quantized, binary, or sparse
representations later, but v1 must not implement them speculatively.

Dimension, byte length, finite values, and normalization must be validated at
write and read boundaries. Corrupt vectors become diagnosable blocked records
and cannot enter an index generation.

### `retrieval_index_generations`

Required fields:

- generation identifier
- embedding profile identifier
- backend and backend format version
- dimensions and distance metric
- indexed chunk count
- source embedding high-water mark or deterministic manifest hash
- build status and error
- relative cache path
- build start/completion timestamps
- activation timestamp

Only a completed generation can become active. The database row and on-disk
manifest must agree before it is used.

## Embedding Provider Boundary

Add a narrow provider interface that accepts bounded text batches and returns
validated vectors plus provider/model provenance. It must not depend on the
research-pack or ANN implementation.

Provider policy:

- local is the default and must be supported without sending corpus text over
  the network
- hosted providers require an explicit provider/config selection
- provider failures do not break lexical search
- input limits, batch sizes, timeouts, and retry classification are explicit
- context-limit and permanently invalid input failures become `blocked`, not
  hot-looping `error`
- a selected profile includes projection/chunker version as well as the model
  so an incompatible input change cannot reuse vectors accidentally

The first local embedding model should be selected through a small bakeoff on
reviewed dbrain queries. The architecture does not hard-code a model before
that evidence exists.

## ANN Index Boundary And Lifecycle

The semantic index interface should support:

- build a complete generation from validated embedding records
- open and validate an existing generation
- query by vector with a candidate limit and optional metadata filter
- report backend, profile, generation, count, freshness, and health
- close all resources cleanly

The first scalable backend should be an embedded Go HNSW-style implementation
that persists under:

```text
<Config.CacheDir>/retrieval/semantic/<profile>/<generation>/
```

The precise library must be chosen during implementation planning after checks
for license, maintenance, macOS ARM64 support, race safety, persistence format,
memory use, and Homebrew build compatibility. It must be pure Go and build with
`CGO_ENABLED=0` on every existing release target; this is a hard acceptance
constraint, not a preference.

Build lifecycle:

1. Read a consistent set of ready embeddings for one profile.
2. Build into a new temporary generation directory.
3. Write and validate a manifest with counts and hashes.
4. Atomically rename the completed directory into place.
5. Transactionally mark that generation active in SQLite.
6. Keep the previous valid generation until activation succeeds.
7. Retain older generations in the first release; do not add automatic garbage
   collection until cross-process reader leases or an equally strong lifetime
   protocol exist.

An interrupted build leaves the previous generation active. Startup cleans or
reports abandoned temporary generations without following unsafe symlinks.

Every index open compares the active SQLite generation record with the on-disk
manifest, profile, format version, dimensions, distance metric, chunk count,
and deterministic manifest/high-water hash. A database restore, binary
rollback, or cache copied from another database therefore produces a visible
`stale` or `corrupt` lane state and lexical fallback, never implicit reuse.

### Exact Scan

The exact backend decodes vectors from SQLite and computes the configured
distance in Go. It is required for:

- deterministic unit and integration tests
- ANN recall comparison
- small corpora
- recovery while an index is missing or stale
- explicit diagnostic use

Exact scanning must be bounded and visible in retrieval metadata so a large
production corpus cannot silently fall back to an unexpectedly slow path. The
implementation plan should define a size threshold above which automatic exact
fallback requires a warning or fails the semantic lane open to lexical-only.

## Query Planning

### Concept Roles

The existing four-role system remains the wire and implementation contract:

- **protected anchors:** source keys, handles, exact URLs, quoted phrases,
  repository names, and resolved stable identities
- **content concepts:** discriminative entities, topics, technologies, events,
  or attributes that describe desired evidence
- **intent concepts:** requested operations such as `learn`, `apply`,
  `synthesize`, `summarize`, or `explain`
- **frame concepts:** query scaffolding and low-information modifiers such as
  `articles`, `new`, or `system` when stronger anchor/content concepts exist

Protected anchors are always required. Existing explicitly discriminative
content families remain required; arbitrary three-character terms must not
become required merely by falling through the classifier. Other content
concepts become required only when the deterministic strategy or a bounded,
validated planner rule establishes that the query is conjunctive. Frame
concepts are never required. Intent concepts retain the current useful
single-intent behavior but become non-required whenever an anchor or content
concept is present.

Classification is context-aware rather than a global stopword deletion.
`knowledge base` remains a searchable content phrase, but `knowledge` and
`base` must not become two independent hard requirements in the Cerebras
question. `Cerebras` and `ontology` remain discriminative content concepts for
that conjunctive query. The preferred-content query variant excludes optional
intent/frame concepts even though the full normalized query remains available
as a broad lexical variant.

Concept trimming order is stable and semantic: anchors, required content,
optional content, intent, then frame. Model-planned concepts pass through the
same role policy and cannot re-require a demoted deterministic term.

The Cerebras regression must prove that generic frame language no longer
pushes the directly named article behind generic knowledge-base results.

### Semantic Query Projection

The embedding query should preserve the user's complete information need while
removing chat scaffolding and non-evidentiary instructions. It may include
typed continuity anchors from prior turns, but it must not embed prior model
answers as factual query context.

Filters such as source type, project, date, tag, or explicitly pinned evidence
apply to candidate generation where supported. They must not be implemented
only as post-retrieval deletion that can leave an empty top window.

## Retrieval Lanes

### Lexical

SQLite FTS5 remains the baseline. It continues to own exact and rare-token
strengths and must pass the existing planner-off eval suite unchanged.

### Semantic

The semantic lane embeds the query using the active profile and retrieves
chunk candidates from the active ANN generation or allowed exact backend. It
returns parent evidence documents with chunk windows, raw distance, lane rank,
profile, backend, and generation provenance.

Semantic unavailability is a lane status, not a research failure. Reasons must
distinguish at least:

- not requested
- disabled for lexical debugging
- not configured
- embedding provider unavailable
- index missing
- index stale
- index corrupt
- query embedding failed

### Exact Identity And Metadata

The current `exact_tag_evidence` pack field remains a separate protected array
with its existing independent cap, retry merge, and reserved synthesis budget.
It is not absorbed into the fused evidence list in v1. Duplicate parent keys
between the fused evidence and exact-tag array are deduplicated when building
citations and synthesis context.

Rows in the main evidence list may still carry an `exact_tag` lane annotation
or exact-identity signal. Protected source-key, canonical-URL, handle, and
other stable-identity matches are protected during truncation. Existing MCP
and trace clients continue to receive the top-level `exact_tag_evidence`
array unchanged.

## Rank Fusion

Raw FTS scores, vector distances, and exact-match scores are not comparable.
They must not be normalized into a single scale by ad hoc arithmetic.

Use weighted reciprocal-rank fusion:

```text
rrf(candidate) = sum(weight(lane) / (k + rank(lane, candidate)))
```

Initial constants:

- `k = 60`
- lexical candidate depth: 50
- semantic candidate depth: 50
- final fused candidate window before inspection: 20

Initial lexical and semantic weights should both be 1.0 to serve the approved
balanced precision/recall objective. Exact matches are not handled merely by a
large weight: protected-anchor candidates that directly satisfy the query are
retained through fusion and truncation.

`entity` and `graph_related` remain provenance/signals within the existing
lexical candidate pipeline in v1; they are not independent ranked retrievers
and therefore do not receive artificial RRF ranks. Their current boosts are
resolved before the lexical lane's final rank enters RRF. The trace records
those lane annotations and legacy signal contributions so they are not lost or
double-counted. If either becomes an independent retriever later, it must
receive a separately reviewed candidate depth and RRF weight.

The separate `exact_tag_evidence` array is a protected evidence floor alongside
the fused list, not a fourth RRF input. Exact identity signals within the main
candidate list are protected after lexical/semantic RRF.

These constants must be named, traced, and adjustable through code/config or
eval profiles rather than scattered magic numbers. Production defaults change
only with eval evidence.

Each fused candidate records:

- rank and raw score/distance from every contributing lane
- RRF contribution from every lane
- total fused score
- matched and missing concepts
- exact/protected-anchor signals
- embedding profile and index generation when semantic retrieval contributed

The existing public integer score may remain as a compatibility projection,
but traces and internal types must retain the unrounded fusion details.

## Deduplication And Parent Consolidation

Fusion initially deduplicates by chunk identity. Before evidence inspection:

- overlapping chunks from the same parent are merged into one bounded window
- no unanchored parent contributes more than three chunks to the inspection
  window by default
- the strongest passage remains primary
- one adjacent chunk may be expanded on either side when context budget allows
- exact anchored evidence cannot be displaced solely because another parent
  produced more semantic chunks

The final evidence document cites the parent source key and carries
`EvidenceChunk` metadata. Source-level coverage counts remain counts of unique
parents, not chunk hits.

## Reranking

The existing deterministic evidence inspection and concept-coverage logic runs
after fusion. A new model reranker is not required for v1.

A local reranker may be introduced later only if reviewed evals show a material
gain over RRF plus deterministic inspection. It must be optional, local by
default, bounded to the fused candidate window, and unable to remove protected
exact evidence without an explicit recorded reason.

## Rollout And Controls

### Modes

The semantic system has three runtime modes:

- `off`: lexical and exact behavior only
- `shadow`: run semantic retrieval and save comparisons, but return the exact
  lexical/exact result set that would have been returned without semantics
- `on`: fuse semantic candidates into the user-visible retrieval pack

The existing `--semantic` and `--no-semantic` controls remain the explicit
user-facing opt-in and debugging override. MCP retains `use_semantic` and
`disable_semantic`.

Configuration should live under a coherent `research.semantic` namespace and
support environment-variable parity. Required settings include mode, provider,
model/profile, index backend, candidate depth, and safe exact-fallback policy.
The implementation plan must update `config env`, sample config, CLI help,
MCP schemas, and the installed `dbrain-mcp` skill where behavior is exposed.

### Shadow Contract

Shadow mode must execute lexical retrieval once and preserve its returned
ordering. It records the hybrid candidate set and comparison metrics separately
in the existing research trace. Shadow failures do not fail the research run.

No shadow result can enter synthesis context or alter retry/judge decisions.
This must be enforced by types or separate variables, not by convention.

The shadow comparison also lives in bounded pack/query-plan diagnostic
metadata. Transports that already save research traces persist it there. MCP
returns the diagnostic metadata but remains trace-free and must not create a
`research-runs` directory.

### Default-On Gate

Hybrid retrieval can become the default only when:

- existing lexical-only evals remain green
- at least 25 reviewed local retrieval cases cover the major query families
- the Cerebras regression and other semantic-recall cases improve or remain
  neutral at the agreed top-k thresholds
- protected-anchor and exact-tag cases show no unacceptable regressions
- ANN recall is measured against exact scanning on the same vectors
- p50/p95 retrieval latency and index memory/disk use are recorded on the
  production-sized corpus
- stale, absent, and corrupt index behavior is tested end to end
- shadow traces show no systematic source-diversity or vague-neighbour failure

The default change is a separate user-visible decision and changelog entry, not
an incidental consequence of installing embeddings.

## Indexing And Operational Commands

The implementation should expose CLI-shaped operations rather than a resident
sidecar:

- inspect semantic configuration and status
- build/update deterministic chunks
- generate missing embeddings for one profile
- build or rebuild an ANN generation
- verify an index against SQLite metadata and sampled exact results
- report stale, missing, blocked, and failed counts
- explicitly purge a profile generation when required by a parent-content
  forget operation

Normal research may diagnose missing derived state, but it must not start an
unbounded corpus backfill implicitly. Configured `sync all` integration can be
considered after explicit commands and progress reporting are reliable.

Automatic generation garbage collection is out of scope for the first
release. Status reports orphaned/old generation count and disk use; later GC
must be an explicit, dry-run-first maintenance design with a disk budget and a
cross-process reader-lifetime protocol. Mandatory privacy purge remains part of
v1 and is not treated as ordinary GC.

Long operations must show advancing counts and distinguish scanning unchanged
rows from embeddings actually generated. Worker selectors and backlog/status
queries must share the same eligibility predicate.

## Failure Semantics

The retrieval pipeline fails open to lexical search while making semantic
state visible.

- No configuration: semantic lane is `disabled/not_configured`.
- Provider unavailable: current batch records a retryable operational error;
  normal lexical research continues.
- Empty or unsupported chunk: mark `blocked` with a reason.
- Model context limit after deterministic bounding: mark `blocked`, not an
  infinite retry.
- Corrupt vector: quarantine that embedding record from index builds and mark
  it blocked/corrupt.
- Missing/stale ANN index: use exact search only when allowed by the configured
  size policy; otherwise use lexical-only with a visible lane reason.
- Corrupt ANN generation: retain or restore the last validated generation,
  otherwise disable the semantic lane.
- Partial rebuild: never activate it.
- Explicit parent purge: delete chunk/embedding rows and disable/remove every
  affected generation before returning success; never serve a stale ANN hit.
- Restored/replaced database: reject any cache manifest that does not match the
  active SQLite generation and deterministic high-water hash.

Semantic failures must not be reported as no corpus evidence. The research
pack distinguishes “semantic unavailable” from “semantic searched and found no
candidate” and from “the complete retrieval system found no evidence.”

## Privacy And Security

- Local embedding is the default because chunk text is private corpus content.
- Hosted embedding requires explicit configuration and follows existing secret
  resolution; keys never enter SQLite, traces, or index manifests.
- Index files contain derived representations of private text and inherit the
  data directory's privacy expectations even though they are rebuildable.
- Cache paths are derived from configured roots, not uncontrolled query text.
- Index readers and cleanup operations reject symlinks and path traversal.
- Public shares expose neither embeddings nor retrieval-internal cache paths.
- Research traces retain existing local, non-indexed, retention-bound policy.

## Ontology Compatibility

Ontology is future-compatible, not part of v1. The retrieval schema preserves:

- stable parent and chunk identifiers
- evidence-role and source-type metadata
- entity matches as structured identifiers
- typed source relationships
- query filters that can later consume entity or relationship constraints
- lane and ranking provenance that remains intelligible if an ontology lane is
  added later

Future entity mentions should attach to chunks through separate typed rows.
They must not be baked into chunk IDs or vector bytes. That permits improved
entity resolution without invalidating raw chunks and embeddings unnecessarily.

## Lessons Borrowed From Turso

Without adopting Turso, v1 should borrow these durable ideas:

- embedding representation and normalization are explicit, versioned fields
- the storage format can later support dense, sparse, quantized, or binary
  vectors without changing evidence identity
- derived indexes are replaceable accelerators, not source-of-truth data
- content changes drive incremental derived-state invalidation
- field-aware ranking is preferable to concatenating all metadata into one
  undifferentiated document

A future Turso or libSQL experiment can implement the same semantic-index
interface against a copied database. It is not an accepted production backend
until it proves ANN performance, FTS compatibility or replacement quality,
migrations, backup/restore, multiprocess behavior, and macOS ARM64/Homebrew
distribution. No authoritative schema should use engine-specific vector SQL in
the meantime.

## Evaluation

### Retrieval Metrics

Record at least:

- recall at 5, 10, and 20
- mean reciprocal rank and first relevant rank
- lexical-only, semantic-only, exact-only, and fused wins
- semantic-only additions that reviewers mark relevant or irrelevant
- protected-anchor retention
- unique parent-source diversity
- per-lane and total retrieval latency
- parent consolidation and context-expansion counts
- embedding and ANN generation/profile provenance
- index freshness and fallback reason

### Eval Cases

The first cases must include:

- the Cerebras article regression
- exact source key and canonical URL lookups
- handles and short discriminative names
- quoted phrases and rare technical identifiers
- paraphrases with little lexical overlap
- generic conceptual queries where semantic drift is likely
- long articles whose relevant passage is not prominent in the summary
- multiple relevant chunks from one parent
- source-type and tag-filtered queries
- no-evidence and semantic-unavailable states
- stale and corrupt index simulations
- explicit Apple Notes forget/purge through exact and ANN semantic lanes
- restored older SQLite database beside a newer cache generation

Saved trace-to-eval cases remain private under the existing local eval policy.
The eval runner must be able to compare lexical-only, exact-vector hybrid, and
ANN hybrid on the same query and embedding profile.

### ANN Acceptance

ANN results are compared with exact top-k results on the same vectors. The
initial target should be at least 0.95 recall@20 on reviewed corpus samples,
subject to memory and latency evidence. Lower recall requires explicit tuning
or rejection of the backend; it cannot be hidden by end-to-end answer quality.

## Testing Strategy

### Unit Tests

- deterministic chunk boundaries and stable IDs
- UTF-8, oversized paragraph, heading, overlap, and short-record behavior
- projection keeps raw and derived roles separate
- vector encoding/decoding and corruption rejection
- content-hash invalidation
- exact distance calculations
- RRF math, lane provenance, ties, and deduplication
- protected-anchor retention
- parent chunk consolidation and expansion
- semantic lane status reasons
- planner role classification for generic frame terms

### Store And Migration Tests

- fresh database creates required tables and indexes
- existing database migrates without touching raw evidence
- migration-history repair scenario required by project policy
- chunk replacement is transactional
- unchanged chunks and embeddings are reused
- profile changes coexist without provenance mutation
- read-only store open performs no migration or indexing writes
- `PurgeItemIndexedContent` atomically removes chunk and embedding rows and
  marks affected generations unusable

### Integration Tests

- local fake embedding provider through exact semantic retrieval
- ANN build, atomic activation, reopen, and query
- interrupted build leaves the prior generation active
- missing/stale/corrupt index follows fallback policy
- purged/excluded evidence is unreachable through exact and ANN search and its
  immutable generation files are removed before purge success
- older restored SQLite state rejects a newer cache manifest
- CLI and MCP semantic enable/disable behavior
- shadow mode cannot alter returned evidence or synthesis input
- MCP shadow diagnostics remain bounded and create no server-side trace files
- research traces carry comparison and provenance data
- filtered semantic retrieval does not empty a valid candidate window through
  post-filtering

### Regression And Full Gates

The Cerebras regression should fail before the planner/fusion correction and
pass afterward. Existing lexical-only evals must remain green.

After implementation changes:

```text
task fmt
task lint
task test-ci
task build
```

Run repo-local smoke checks through `direnv exec . ./bin/dbrain` after verifying
the development runtime paths. Production shadow deployment and default-on
changes require separate explicit approval.

## Documentation And Product Surfaces

Implementation must update:

- `docs/research-harness.md`
- `README.md`, `COMMANDS.md`, and `MCP.md` where commands or flags change
- sample/installer configuration and `dbrain config env`
- eval documentation and example cases
- the repository and installed `dbrain-mcp` skill if its retrieval guidance or
  tool arguments change
- `CHANGELOG.md` once user-visible behavior is implemented

Human-facing output should use parent source keys and canonical URLs. Chunk IDs,
embedding profile hashes, and index paths belong in diagnostic output, not
normal citations.

## Initial Implementation Slices

### Slice 1: Correctness Foundation

- deterministic evidence projection and chunking
- SQLite chunk and embedding records
- fake and selected local embedding-provider boundary
- portable dense float32 vectors
- exact vector retrieval
- planner concept-role regression
- Cerebras and foundational retrieval evals

### Slice 2: Hybrid Retrieval In Shadow

- semantic evidence documents and full lane provenance
- RRF fusion and protected-anchor enforcement
- parent-source consolidation
- shadow trace comparisons
- CLI/MCP/config status and diagnostics

### Slice 3: Scalable Local Index

- select and integrate an embedded HNSW-style Go backend
- atomic index generations and verification
- exact-versus-ANN recall reporting
- production-sized latency, disk, and memory measurements
- stale/corrupt/missing recovery paths

### Slice 4: Opt-In Product Use

- enable existing `--semantic` and MCP opt-in against the configured backend
- surface semantic status and retrieval reasons
- complete reviewed eval set and operational docs
- leave default behavior lexical until the acceptance gate is separately met

Ontology extraction, Turso experiments, a model reranker, cross-device index
sync, and default-on semantics remain later work.

## Acceptance Criteria

The approved design is implemented when:

1. SQLite remains the only authoritative database and existing lexical search
   works with semantic retrieval absent or disabled.
2. Raw evidence remains unchanged and chunks cite their parent evidence with
   inspectable offsets, hashes, and roles.
3. Local embeddings can be generated, validated, stored portably, and searched
   exactly.
4. A rebuildable embedded ANN index can be built, atomically activated,
   verified against exact results, deleted, and recovered without data loss.
5. Lexical and semantic results fuse through traced RRF while exact identity
   signals are protected after fusion; the compatible `exact_tag_evidence`
   array remains a separately budgeted protected floor, while entity/graph
   signals contribute to the lexical rank rather than receiving fabricated RRF
   ranks.
6. Shadow mode provably cannot alter user-visible evidence or synthesis.
7. The Cerebras regression improves without breaking protected-anchor,
   lexical-only, or no-evidence evals.
8. CLI, MCP, config, docs, traces, stats, and failure states explain whether and
   how semantic retrieval was used.
9. Local embeddings are the default and hosted embedding requires explicit
   configuration.
10. No ontology engine, Turso dependency, or external vector service is
    introduced.
11. Explicitly purged content is absent from chunk/embedding tables, exact
    search, active ANN generations, and retained generation files before the
    purge reports success.
12. The selected ANN implementation passes the existing release matrix with
    `CGO_ENABLED=0`.

## Open Implementation Decisions

The architecture is settled. These bounded choices remain for the
implementation plan and must be decided by measured comparison:

- first local embedding model and provider path
- embedded Go HNSW library
- automatic exact-fallback corpus threshold
- final chunk-size constants after model/context validation
- production candidate depths and RRF weights after reviewed evals

None of these choices may change the authority boundary, privacy policy,
rollout stages, exact fallback requirement, protected-anchor contract, purge
guarantee, or `CGO_ENABLED=0` release constraint.

## External References

- [Turso Database repository and current feature roadmap](https://github.com/tursodatabase/turso)
- [Turso Database SQLite compatibility matrix](https://github.com/tursodatabase/turso/blob/main/COMPAT.md)
- [Turso Database manual](https://github.com/tursodatabase/turso/blob/main/docs/manual.md)
- [libSQL native vector-search announcement](https://turso.tech/blog/turso-brings-native-vector-search-to-sqlite)
