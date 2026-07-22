# HNSW Backend Bakeoff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a reproducible, read-only screening benchmark that decides whether `github.com/coder/hnsw` can advance to immutable-segment implementation for dbrain's restored-corpus vector count.

**Architecture:** A small `internal/annbakeoff` package generates deterministic, L2-normalized 768-dimensional vectors, builds the existing narrow HNSW adapter, and compares sampled ANN top-20 candidates with an exact oracle over the same vectors. A devtool executes staged corpus sizes, performs an export/import reopen check, writes an atomic JSON report, and stops before a configured heap ceiling. It does not read or modify corpus vectors, generate embeddings, construct a segment, or make semantic retrieval available.

**Tech Stack:** Go 1.26, `github.com/coder/hnsw`, existing `internal/semanticindex` adapter, standard-library JSON and runtime metrics.

## Global Constraints

- SQLite remains authoritative; the tool never writes a database or asks an embedding provider for vectors.
- The synthetic corpus uses a fixed seed and 768 dimensions. Real restored vectors are reserved for a later small reopen/correctness sample because the restore has incomplete embedding coverage.
- Treat `coder/hnsw` export bytes as an opaque payload only. The eventual dbrain segment format owns checksums, canonical membership, durability, and publication.
- The report records heap allocation/system metrics, not a claimed peak RSS; the final production gate will still measure OS-level RSS with the completed segment implementation.
- Stages default to 25,000, 100,000, and 286,619 vectors. A stage stops the run before the next stage when the configured Go heap-system ceiling is exceeded or recall drops below the configured floor.
- Acceptance screening requires sampled recall@20 at least 0.95, successful export/import reopen recall, and `CGO_ENABLED=0` test/build compatibility. It does not by itself accept the library for production or enable semantic mode.

---

### Task 1: Define Deterministic Corpus, Exact Oracle, And Stage Runner

**Files:**
- Create: `internal/annbakeoff/run.go`
- Create: `internal/annbakeoff/run_test.go`

**Interfaces:**
- Produces `Run(ctx context.Context, options Options) (Report, error)`.
- `Options` includes `Sizes []int`, `Dimensions`, `QueryCount`, `Seed`, `RecallLimit`, and `MaxHeapBytes`.
- `Report` contains one `StageReport` per attempted size, with build/open/search timings, graph-payload bytes, sampled recall@20, query p50/p95, and Go heap metrics.

- [ ] **Step 1: Write failing tests** for deterministic vectors, exact top-20 agreement on a tiny corpus, explicit rejection of invalid options, and a stage that reports a failed recall gate rather than silently continuing.
- [ ] **Step 2: Run** `go test ./internal/annbakeoff -count=1` and confirm RED because the package does not exist.
- [ ] **Step 3: Implement** deterministic clustered unit-vector generation, exact cosine top-k, percentile calculation, HNSW build/search/export/import, and stage gating. Keep vectors in ordinary Go memory and use the adapter's ordinal-only candidate contract.
- [ ] **Step 4: Run** `go test ./internal/annbakeoff -count=1` and `CGO_ENABLED=0 go test ./internal/annbakeoff -count=1` and confirm GREEN.

### Task 2: Expose A Safe Devtool And Persist The Evidence

**Files:**
- Create: `cmd/devtools/semantic_ann_bakeoff/main.go`
- Create: `cmd/devtools/semantic_ann_bakeoff/main_test.go`

**Interfaces:**
- Consumes `annbakeoff.Options` and serializes `annbakeoff.Report` to an explicitly supplied JSON path.
- Requires `--report`; defaults to the restored-corpus vector count but never opens the database.

- [ ] **Step 1: Write failing tests** for required report path, comma-separated stage parsing, atomic report write, and preservation of a partial report when a stage fails its gate.
- [ ] **Step 2: Run** `go test ./cmd/devtools/semantic_ann_bakeoff -count=1` and confirm RED because the command does not exist.
- [ ] **Step 3: Implement** flags, bounded defaults, atomic JSON serialization, and a concise terminal summary. Reject a size smaller than top-k or duplicate/non-positive sizes.
- [ ] **Step 4: Run** `go test ./cmd/devtools/semantic_ann_bakeoff -count=1` and confirm GREEN.

### Task 3: Screen The Candidate On The Restored-Corpus Shape

**Files:**
- No tracked data or product behavior changes; write the report under `/private/tmp`.

- [ ] **Step 1: Run focused tests and standard repository gates:** `task fmt`, `task lint`, `task test-ci`, and `task build`.
- [ ] **Step 2: Run the devtool with `CGO_ENABLED=0` for the 25,000 and 100,000-vector stages.** Stop before full scale if recall or heap gates fail.
- [ ] **Step 3: If stages pass, run 286,619 vectors and record the JSON report plus OS-level max-RSS from `/usr/bin/time -l` as machine-specific evidence.**
- [ ] **Step 4: Commit the code and plan.** Do not enable semantic mode, add segment lifecycle code, or claim production acceptance from this screening alone.

## Execution Record (2026-07-21)

The first configured candidate was rejected before the 25,000-vector stage.
The committed devtool ran deterministic 1,000-vector screening at 768
dimensions with top-20 recall, 10 exact-oracle queries, three warm ANN timing
repetitions, `M=16`, and `EfSearch=256`. Recall@20 was 0.355, and the reopened
graph returned the same recall. The diagnostic 64-dimensional run was 0.85;
raising `EfSearch` to 1,024 did not change it, while `M=32` reduced recall to
0.56. The production-scale stages were deliberately not run.

This completes the narrow uncertainty-reduction objective: no segment,
manifest, cache, database, provider, or corpus data was changed. The next step
is a separately approved replacement-backend design, not a retry of this
candidate at a larger size.
