# Semantic Compaction Planner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deterministically select bounded eligible segment compaction work and classify its outputs, without reading vector payloads, publishing a root, or changing serving behavior.

**Architecture:** The planner is pure Go and receives only segment metadata: stable ID, live count, tombstone count, and immutable creation order. It selects a singleton cleanup for a segment over one-percent tombstones, otherwise the two oldest same non-capped size-class segments. It classifies a resulting live count into exact L0, a normal half-open class, or the capped packing shape defined by the accepted semantic retrieval design.

**Tech Stack:** Go 1.26, standard library, `internal/semanticbuild` unit tests; no SQLite migration, CGO, native library, filesystem mutation, or CLI.

## Global Constraints

- Size classes are `[5,000,10,000)`, `[10,000,20,000)`, `[20,000,40,000)`, `[40,000,80,000)`, `[80,000,160,000)`, and capped `[160,000,200,000]`.
- A segment below 5,000 live rows is exact L0, never an undersized ANN segment.
- A normal pair chooses the two oldest segments in one non-capped class; a singleton with tombstones over one percent takes precedence.
- Capped segments never take ordinary pair compaction. A pair total over 200,000 emits one capped output of `min(200,000,total-5,000)` plus a lower-class remainder.
- The planner does not choose vectors, inspect SQLite rows, write payloads, mutate root membership, remove cache paths, or expose compaction through a command or serving path.

## Task 1: Failing pure-planner tests

**Files:** Create `internal/semanticbuild/compaction_plan_test.go`.

- [ ] Define test metadata with `SegmentHash`, `CreatedOrder`, `LiveCount`, and `TombstoneCount`.
- [ ] Add failing tests for class boundaries; tombstone singleton priority; deterministic oldest pair selection; no plan for capped-only or mixed-class input; undersized exact-L0 output; and a 200,001-vector upper-pair result producing capped 195,001 plus lower remainder 5,000.
- [ ] Run `go test ./internal/semanticbuild -run TestCompaction -count=1`; it must fail because the planner does not exist.

## Task 2: Pure deterministic planner

**Files:** Create `internal/semanticbuild/compaction_plan.go`.

- [ ] Export `SegmentCompactionInput`, `SegmentCompactionPlan`, `SegmentCompactionOutput`, `SegmentCompactionKind`, and `PlanSegmentCompaction`.
- [ ] Validate unique non-empty segment hashes, non-negative tombstones, positive live counts, and strictly ordered creation keys.
- [ ] Return a singleton plan for the oldest tombstone-heavy segment; otherwise group non-capped inputs by class and select the oldest eligible pair.
- [ ] Classify live output using only counts. For totals below 5,000 return exact-L0. For totals over 200,000 apply the documented capped/remainder packing. Never return output above 200,000 or two same-class sibling outputs that immediately requalify.
- [ ] Run `go test ./internal/semanticbuild -run TestCompaction -count=1` and `CGO_ENABLED=0 go test ./internal/semanticbuild -count=1`; both must pass.
- [ ] Commit with `git commit --no-gpg-sign -m "feat: plan bounded semantic compaction"` after staging only `internal/semanticbuild`.

## Task 3: Record the boundary and verify

**Files:** Modify `CHANGELOG.md` and `docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md`.

- [ ] Record that deterministic planning exists but no compaction payload/root replacement or cache cleanup can occur yet.
- [ ] Run `task fmt`, `task lint`, `task test-ci`, `task build`, `CGO_ENABLED=0 go build ./cmd/dbrain`, and `git diff --check`; every command must exit zero.
- [ ] Commit documentation and plan, then push `codex/semantic-ann-lifecycle`.

## Self-Review

- This slice turns the approved size-tier rules into tested deterministic policy, exposing no mutation or runtime behavior.
- Physical compaction remains blocked on a later store snapshot/vector streaming API and root replacement orchestration.
