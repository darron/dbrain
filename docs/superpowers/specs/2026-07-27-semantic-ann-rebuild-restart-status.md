# Semantic ANN Rebuild: Restart Status

**Status:** Phases A and B completed on a disposable representative corpus;
production activation remains deferred

**Purpose:** provide one factual starting point for resuming the semantic ANN
rebuild. This is a status document, not an approval to activate, rebuild, or
modify the production corpus.

## The short version

dbrain has a working lexical-plus-exact semantic retrieval foundation on
`main`. A substantial, unmerged branch adds the *derived-index lifecycle*
needed to make that scale beyond the exact-search ceiling:

```
SQLite embeddings (authority)
        │
        ├── bounded exact L0 delta
        └── immutable derived ANN segments + root manifest
                          │
                 optional USearch runtime
                          │
               exact SQLite revalidation/rerank
                          │
                existing evidence hydration/fusion
```

The lifecycle work is on `codex/semantic-ann-lifecycle`. Commit `97082a8`
completed the pinned authority-read slice; merge commit `bdec6784` then
integrated `main` at `9d25cca`. Do not start from `main` and assume the
segment, root, L0, compaction, or optional native-runtime work is present until
this branch merges.

The rebuild is therefore at **Stage 3 of 5: lifecycle implementation and
representative-corpus evaluation are complete on the feature branch;
operator maintenance and production admission have not happened.**

## Target state

The intended production shape is deliberately conservative:

1. SQLite remains the authoritative store for embeddings, parent eligibility,
   revisions, and active generation state.
2. Immutable ANN segments are a rebuildable cache, never a source of truth.
3. A small current delta (L0) is searched exactly; a larger stable base is
   searched approximately, then revalidated and exactly reranked from SQLite.
4. If a root, cache file, native backend, readiness invariant, or bounded
   maintenance policy is unavailable, the semantic lane fails open to the
   existing lexical lane. It never silently returns a partial index as complete.
5. Corpus growth is handled by bounded flushes and compactions, not by
   rebuilding one monolithic index on each change.

The accepted full contract is
[`2026-07-19-production-corpus-semantic-retrieval-design.md`](2026-07-19-production-corpus-semantic-retrieval-design.md).
That document includes some future concurrency/purge requirements which are
not yet implemented; this status document distinguishes those from shipped
branch behavior.

## What is already done

### 1. Semantic foundation on `main`

`main` includes the earlier semantic foundation:

- versioned projection/chunking and embedding-profile provenance;
- bounded/resumable semantic chunk and embedding commands;
- exact semantic search for small, admitted corpora;
- hybrid lexical/semantic fusion, semantic evidence provenance, inspection,
  and fail-open behavior;
- semantic status/readiness reporting and default-off configuration.

This work is represented by the pre-lifecycle commits visible on `main`,
including `051ac74` (semantic shadow retrieval), `c990854` (fusion), and
`67abcd3` (exact semantic retrieval).

### 2. Scalable lifecycle branch

The unmerged lifecycle branch contains these implemented building blocks:

| Area | Branch state | Important constraint |
| --- | --- | --- |
| Immutable segment cache | Content-addressed segment payloads, manifests, checksums, atomic publication, and root manifests | Cache is derived; SQLite records the authoritative generation/member facts. |
| Generation activation | CAS activation with profile/root/purge/snapshot facts and immutable member tuples | Old or corrupt cache is rejected rather than trusted. |
| L0 definition | Exact complement of active immutable `(chunk_id, revision, vector_hash)` membership | Changed content leaves its old segment stale and enters current exact L0. |
| Flush lifecycle | Bounded 5,000-vector flush seam; preserves prior immutable segments in later roots | No production root has been built by this work. |
| Compaction | Deterministic planner, live-member stream, and bounded singleton/pair physical compaction executor | No operator command/scheduler exposes it. |
| Native backend | Optional `usearch && cgo` segment writer/loader/searcher and an evaluator | Normal `CGO_ENABLED=0` builds do not require or use it. |
| Search correctness | Native candidates are manifest-resolved, SQLite-validated, exactly cosine-reranked, merged with exact L0, and parent-diversified | Approximate output is never evidence by itself. |
| Candidate bounds | Adaptive 200/500/2,000 global candidate stages; SQLite validation batched to 190 candidates | This is a safety/coverage bound, not a measured production recall guarantee. |
| Snapshot consistency | `97082a8` pins L0 plus every native-candidate validation batch to one query-only SQLite snapshot | It is not the planned cross-process generation lease or hydration-wide snapshot. |

The branch history to preserve is:

```
fddc06e  optional USearch ANN screen
97448d5  bounded semantic segment flush lifecycle
3e4f039  segmented ANN lifecycle boundary documentation
3c42cbb  content-free authority reads
97082a8  pinned native ANN authority snapshot
```

### 3. Phase B representative-corpus evaluation

The recent 2,497,155,072-byte backup at `data/brain.db` (modified
2026-07-19 12:03 MDT) was copied to an explicit disposable root under
`/private/tmp`; the source backup and configured production database/cache were
not mutated. The copy expanded to a 3.9 GB SQLite database after projection and
embedding.

The first full-corpus rechunk run exposed a real scaling defect rather than a
deadlock: deleting obsolete chunks repeatedly scanned the entire occurrence
table because the cleanup trigger had no `chunk_id`-leading index. Migration 24
adds `idx_retrieval_chunk_occurrences_chunk`; its regression test simulates an
already-migrated schema-23 database. With the index applied, the previously
stuck 1.70 MB PDF completed immediately and the remaining queue drained.

Final corpus state:

| Fact | Result |
| --- | ---: |
| Eligible parents | 34,180 |
| Current parents | 32,367 |
| Empty parents (`no_chunkable_content`) | 1,813 |
| Current v2/v3 chunks | 290,535 |
| Legacy chunks / staging rows | 0 / 0 |
| Ready embeddings | 290,535 |
| Embedding blocked / failed | 0 / 0 |
| SQLite integrity | `ok` |

The evaluated profile was
`embedding-profile-v1:189c8da80d3bc59c63865d8a2988ee28d65982e1362442cc38ce1c8ed43912d3`:
Ollama `embeddinggemma:300m-bf16`, 768 dimensions, dense float32,
L2-normalized. The existing 10,000 vectors remained valid; three resumable
default-batch CLI runs generated the remaining 280,535 vectors with no
duplicate work or failure.

The candidate root contains 290,000 vectors in 58 immutable 5,000-vector
segments, with an exact 535-vector L0 and zero tombstones. The cache occupies
945 MB. All 56 additional flushes published, checksum-reopened, and
CAS-activated successfully, leaving exactly one active generation:
`semantic-root-v1:949bcf9e01faa7ff71e8c69acd6a148b`. The evaluator used
USearch connectivity 16, construction expansion 128, and search expansion 256.

Measured on the restored corpus:

| Gate | Result |
| --- | --- |
| Cold root reopen | 1.49-1.60 seconds |
| Exact top-1 agreement | 12/12 |
| Mean raw recall@10 | 0.9583 |
| Tie-aware recall | 1.0; the sole raw 0.5 sample had more than ten vectors at the identical exact distance |
| Native search | 1.30 seconds mean, 1.46 seconds p95 on the warm repeat |
| Exact SQLite baseline | 7.33 seconds mean, 8.56 seconds p95 on the warm repeat |
| Native rows exactly reranked | 735/query (535 L0 plus 200 validated candidates) |
| L0 participation | both sampled L0 vectors ranked first |
| Diversity / pinned parent | three-per-parent cap held; pinned parent returned 10/10 |
| Source-type filter | 10/10 returned hits matched |
| Corrupt payload | failed closed on checksum mismatch before native import |
| Peak observed process RSS | about 4.2 GB |

The existing evaluator recall floor of 0.95 passes. No latency or resident-memory
approval cap was declared before this measurement, so the 1.3-second native
query and 4.2 GB peak RSS are characterization data, not a production resource
approval. The Go heap after reopen was about 72 MB; native graph allocations
dominate resident memory.

An actual tagged `dbrain research --semantic` command was also run against the
copy. It deliberately failed open to lexical/exact-tag retrieval with
`active semantic generation provenance is unproven`. This is the current
Phase C admission gate, not a backend failure: direct tagged root search passed,
but normal CLI serving remains intentionally unavailable.

**Phase B recommendation:** the optional backend and chosen candidate expansion
are correct enough to retain and merge as an evaluated lifecycle foundation.
Do not describe this PR as a user-enabled semantic ANN feature, and do not
activate production. Phase C must prove active-generation readiness, expose
bounded maintenance, and set explicit latency/RSS limits first.

### 4. Phase A integration result

The branch now has a reproducible merge-readiness baseline:

- current `main` is integrated with no remaining code, schema, documentation,
  or test conflict;
- the CI-only read-snapshot test deadlock is fixed by comparing the pinned
  snapshot with an independent read-only handle rather than trying to borrow a
  second transaction from the same one-connection pool;
- the rejected pure-Go HNSW probe is isolated under `internal/annbakeoff`, so
  it no longer prevents the normal Windows CGO-free release binary from
  compiling;
- the standard formatting, lint, race-test, and native build gates pass;
- the CGO-free Darwin amd64/arm64, Linux amd64/arm64, and Windows amd64 release
  builds pass; and
- the tagged USearch packages pass against the checksum-verified upstream
  v2.26.0 macOS arm64 library.

No production database, configured semantic cache, production root, embedding
provider, or production serving mode was opened or changed during Phase A.

## What has *not* been done after Phase B

These are not minor cleanup items. They are the work needed to turn the branch
into a production-capable system.

1. **Prove the optional backend can be distributed.** Decide how the
   native USearch library is obtained for supported environments, while keeping
   the CGO-free binary fully supported. Homebrew availability is a local
   evaluation convenience, not yet a release contract.
2. **Implement or explicitly defer the remaining concurrency contract.** The
   branch has a SQLite reader snapshot for authority rows, but not the design's
   cross-process maintenance/generation leases, writer-preference handling,
   or snapshot-through-hydration semantics.
3. **Expose bounded maintenance safely.** Flush and compaction executors need
   an operator-facing command/runbook, cancellation/time budget, diagnostics,
   restart handling, and cache garbage-collection policy before routine use.
4. **Implement production admission.** Wire readiness/catch-up gates to actual
   root health, L0 count/age, tombstones, fanout, backend availability, and
   measured caps. `semantic on` must remain unavailable until these predicates
   are true.
5. **Set resource acceptance limits.** Decide whether roughly 1.3-second warm
   native queries and 4.2 GB peak RSS are acceptable, or require L0-query,
   segment-fanout, quantization, or compaction changes before local on-mode.
6. **Run a staged rollout.** First shadow-only comparison, then explicit local
   on-mode on a copy/restore, then production only after separate approval.

## Recommended restart sequence

### Phase A — regain a trustworthy baseline

1. [x] Work from the checked-out `codex/semantic-ann-lifecycle` branch.
2. [x] Compare it with current `main` and integrate code, schema, documentation,
   and tests deliberately.
3. [x] Re-run focused lifecycle tests, diagnose the CI timeout as a test
   connection-pool deadlock, and complete the clean race suite.
4. [x] Review the accepted design against actual code and record the deliberately
   deferred production scope here.

**Exit achieved:** one clean integrated branch with a reproducible CGO-free
release matrix and a separately reproducible tagged USearch build.

### Phase B — evaluate, do not activate

1. [x] Copy/restore the corpus into an explicit evaluation location.
2. [x] Generate embeddings only for the chosen profile, recording provider, model,
   profile ID, corpus revision, and failure/blocked counts.
3. [x] Build one candidate root outside the configured production cache.
4. [x] Run recall, latency, memory, reopen, L0, filter, diversity,
   cancellation, and corrupt-cache tests against an exact SQLite baseline.
5. [x] Publish the aggregate result and a clear pass/defer recommendation in
   this document.

**Exit achieved:** quality/correctness evidence supports retaining the backend
and parameters; resource limits and runtime admission remain explicitly
deferred to Phase C.

### Phase C — complete the operational gap

1. Decide whether the full cross-process lease contract is required before the
   first local on-mode experiment. The safest default is yes before automatic
   maintenance or purge; a deliberately bounded manual-only experiment may
   defer it only if that limitation is explicit.
2. Add bounded operator maintenance commands and diagnostics.
3. Wire real readiness/admission gates and lexical fail-open states.
4. Write the production runbook: backup, cache path, build, evaluate, shadow,
   rollback, and cache cleanup.

**Exit:** no hidden background process or manual cache manipulation is needed
to keep the index correct as the corpus grows.

### Phase D — staged activation

1. Run shadow mode on a restored/local corpus and inspect disagreement traces.
2. Enable local on-mode only under the measured corpus and maintenance limits.
3. Request a new explicit production approval with the evaluation report and
   rollback plan.

## Non-goals and rejected shortcuts

- Do not adopt Cognee or Turso merely to obtain ANN behavior. Their ideas may
  inform future work, but SQLite remains the authoritative local database.
- Do not make a native shared library mandatory for ordinary dbrain installs.
- Do not use a whole-profile exact scan above its measured safe ceiling just
  because an ANN root is unavailable.
- Do not build, activate, or mutate the production corpus/cache while restarting
  this engineering work without explicit fresh approval.
- Do not treat a successful native index build as a quality or rollout pass.

## Restart handoff checklist

Before writing code, confirm all of these facts:

- [x] Current branch and merge-base with `main` are recorded.
- [x] Evaluation database and cache paths are explicit and non-production.
- [x] The selected embedding profile and corpus snapshot are recorded.
- [x] CGO-free and tagged-native build paths are both reproducible.
- [ ] Explicit latency and resident-memory approval limits are set before
      activation; the existing 0.95 recall floor passed.
- [ ] The ownership and timing of the remaining lease/admission work are
      decided.
- [x] Production activation remains off until a new approval.

## Source of truth

- Full intended architecture:
  [`2026-07-19-production-corpus-semantic-retrieval-design.md`](2026-07-19-production-corpus-semantic-retrieval-design.md)
- Lifecycle branch: `codex/semantic-ann-lifecycle`, with `97082a8` as the last
  pre-integration lifecycle slice
- Current integration base: `main` at `9d25cca`
- This document: current factual restart map; update it after each phase exit.
