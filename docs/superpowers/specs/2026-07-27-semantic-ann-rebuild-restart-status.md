# Semantic ANN Rebuild: Restart Status

**Status:** Phase A integrated and verified; production activation remains
deferred

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

The rebuild is therefore at **Stage 2 of 5: lifecycle implementation is mostly
present on the feature branch; corpus construction, measured evaluation, and
production admission have not happened.**

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

### 3. Corpus and backend evidence gathered so far

- A restored production corpus was used to shape the lifecycle design and
  safety bounds.
- A local USearch screen was performed using a separately installed native
  library and tagged build; the recorded result was not sufficient to approve
  production activation by itself.
- The default build remains intentionally safe: an admitted active root without
  the optional native backend reports unavailable and lexical retrieval remains
  available.

No full 34,180-parent embedding drain, production-base index construction, or
live corpus serving experiment was deliberately run as part of this branch.

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
provider, or production serving mode was opened or changed during this work.

## What has *not* been done after Phase A

These are not minor cleanup items. They are the work needed to turn the branch
into a production-capable system.

1. **Prove the optional backend can be distributed.** Decide how the
   native USearch library is obtained for supported environments, while keeping
   the CGO-free binary fully supported. Homebrew availability is a local
   evaluation convenience, not yet a release contract.
2. **Build a disposable production-corpus candidate root.** Use an explicit,
   non-production database/cache/report path and the existing evaluator/build
   machinery. Do not point a first attempt at the live configured cache.
3. **Measure quality and resources.** Establish exact-baseline recall, latency,
   resident memory, build time, reopen time, L0 behavior, filter/diversity
   behavior, and failure modes on the restored corpus. The current 200/500/2000
   expansion policy is a design choice awaiting this gate.
4. **Implement or explicitly defer the remaining concurrency contract.** The
   branch has a SQLite reader snapshot for authority rows, but not the design's
   cross-process maintenance/generation leases, writer-preference handling,
   or snapshot-through-hydration semantics.
5. **Expose bounded maintenance safely.** Flush and compaction executors need
   an operator-facing command/runbook, cancellation/time budget, diagnostics,
   restart handling, and cache garbage-collection policy before routine use.
6. **Implement production admission.** Wire readiness/catch-up gates to actual
   root health, L0 count/age, tombstones, fanout, backend availability, and
   measured caps. `semantic on` must remain unavailable until these predicates
   are true.
7. **Run a staged rollout.** First shadow-only comparison, then explicit local
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

1. Copy/restore the corpus into an explicit evaluation location.
2. Generate embeddings only for the chosen profile, recording provider, model,
   profile ID, corpus revision, and failure/blocked counts.
3. Build one candidate root outside the configured production cache.
4. Run recall, latency, memory, reopen, stale-member, L0, filter, diversity,
   cancellation, and corrupt-cache tests against an exact SQLite baseline.
5. Publish a small report with thresholds and a clear pass/fail recommendation.

**Exit:** evidence that the optional backend and chosen segment parameters are
adequate for this corpus, or a decision to revise the backend/architecture.

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
- [ ] Evaluation database and cache paths are explicit and non-production.
- [ ] The selected embedding profile and corpus snapshot are recorded.
- [x] CGO-free and tagged-native build paths are both reproducible.
- [ ] Exact-baseline quality/resource thresholds are written before measuring.
- [ ] The ownership and timing of the remaining lease/admission work are
      decided.
- [ ] Production activation remains off until a new approval.

## Source of truth

- Full intended architecture:
  [`2026-07-19-production-corpus-semantic-retrieval-design.md`](2026-07-19-production-corpus-semantic-retrieval-design.md)
- Lifecycle branch: `codex/semantic-ann-lifecycle`, with `97082a8` as the last
  pre-integration lifecycle slice
- Current integration base: `main` at `9d25cca`
- This document: current factual restart map; update it after each phase exit.
