# Active Segment Compaction Snapshot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` or `superpowers:subagent-driven-development` to implement this plan task-by-task.

**Goal:** Read the active immutable-segment root as one SQLite-consistent, deterministic compaction input without selecting vectors, changing a generation, writing cache artifacts, or enabling semantic serving.

**Architecture:** `internal/store` exposes a read-only active-root snapshot. It returns the profile CAS facts and each active segment's immutable metadata, stable creation order, live membership count, and tombstone count. “Live” means the member still joins a `ready` embedding with the identical `(chunk_id, revision, vector_hash)` and a current parent projection. Any other stored member is a tombstone. The pure `semanticbuild` planner remains the policy owner; a future executor will revalidate this snapshot under its activation transaction before publishing a replacement root.

**Tech Stack:** Go 1.26, SQLite read transaction, existing `internal/store` and `internal/semanticbuild`; no schema migration, CGO, native dependency, cache write, CLI, or serving-path change.

## Task 1: Read-only store contract and failing tests

**Files:** Create `internal/store/retrieval_compaction_snapshot_test.go`.

- [ ] Seed an active generation with two immutable segments and prove the snapshot reports only that root's segments, their profile CAS state, deterministic creation order, and their immutable metadata.
- [ ] Change one member's embedding revision and make another member's parent non-current; prove both are tombstones while a matching ready/current member remains live.
- [ ] Corrupt a catalog `indexed_chunk_count`; prove the snapshot fails closed instead of offering an inconsistent compaction plan.
- [ ] Prove an inactive/no-root profile yields an empty segment list rather than borrowing historical segments.
- [ ] Run `go test ./internal/store -run TestRetrievalActiveSegmentCompactionSnapshot -count=1`; it must initially fail because the store contract does not exist.

## Task 2: Implement the snapshot

**Files:** Create `internal/store/retrieval_compaction_snapshot.go`.

- [ ] Define snapshot and per-segment types containing the active profile state, active generation ID, immutable segment metadata, stable `CreatedOrder`, and live/tombstone counts.
- [ ] Use one read-only transaction to load the profile and active-root segment rows ordered by `(created_at, segment_hash)`.
- [ ] Count every catalogued member exactly once as live or tombstoned, applying the same ready/current identity condition used by activation. Reject a segment whose catalogued count disagrees with its membership rows.
- [ ] Keep the method observational: no root mutation, cache access, vector bytes, migration, or serving integration.
- [ ] Run the focused store test and `CGO_ENABLED=0 go test ./internal/store -count=1`.

## Task 3: Record the executable boundary and verify

**Files:** Modify `CHANGELOG.md` and `docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md`.

- [ ] State that compaction can now obtain deterministic active-root facts but cannot build replacement payloads, activate a replacement root, remove segments, or clean cache paths.
- [ ] Run `task fmt`, `task lint`, `task test-ci`, `task build`, `CGO_ENABLED=0 go build ./cmd/dbrain`, and `git diff --check`.
- [ ] Remove only the generated worktree-root `dbrain` binary, commit with `--no-gpg-sign`, and push the branch over authenticated HTTPS.

## Safety Boundary

- [ ] The current root-manifest format forbids an empty segment list. This slice deliberately does not decide or alter the last-segment-to-L0 transition; that requires an explicit root-format and activation-contract amendment before physical compaction execution.
