# Membership-Based L0 Activation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make active-root activation compare-and-swap safe and define exact L0 from active immutable membership rather than a revision watermark, without adding compaction, serving, or native runtime wiring.

**Architecture:** SQLite remains authoritative. An active root covers only ready embedding rows whose `(chunk_id, revision, vector_hash)` occur in the active segment set; every other current ready row is exact L0. Activation carries the root, epoch, and snapshot it was built from, rejects stale writers atomically, and permits an internal equal-snapshot rewrite for a later compactor.

**Tech Stack:** Go 1.26, SQLite migration 23, existing segment-membership catalog, `semanticbuild.Flush`, CGO-free tests.

## Global Constraints

- SQLite rows and exact segment membership remain authoritative; cache files remain replaceable derived state.
- Default `CGO_ENABLED=0` builds and tests remain independent of USearch.
- A flush of only newly revisioned vectors advances its snapshot; a 5,000-vector membership-L0 remainder at the current snapshot rewrites that snapshot.
- Equal-snapshot rewrite is an internal activation capability only: no input selection, payload rebuild, cache deletion, CLI, or ANN serving is in scope.
- Failed root/epoch compare-and-swap leaves the active root and aggregate counters unchanged.
- No current ready embedding may have more than one usable active membership.

## Files

- `internal/store/migrations.go`: append migration 23 `retrieval_membership_l0_activation_v1`.
- `internal/store/retrieval_embedding_profiles.go`: replace revision-tail trigger accounting and repair counters from active membership.
- `internal/store/retrieval_index_segments.go`: activation expectations/mode, membership L0 queries, CAS, and duplicate-membership proof.
- `internal/store/retrieval_index_segments_test.go`: store contract regression tests.
- `internal/store/migrations_test.go`: upgraded-schema counter/trigger regression.
- `internal/semanticbuild/flush.go` and `flush_test.go`: ordinary flush supplies advancing expectations.
- `CHANGELOG.md` and the accepted production semantic design: completed/deferred boundary.

## Task 1: Failing store contract tests

**Files:** Modify `internal/store/retrieval_index_segments_test.go`.

**Interface:**

```go
type RetrievalGenerationActivationMode string
const (
    RetrievalGenerationAdvanceSnapshot RetrievalGenerationActivationMode = "advance_snapshot"
    RetrievalGenerationRewriteSnapshot RetrievalGenerationActivationMode = "rewrite_snapshot"
)
type CompleteRetrievalIndexGenerationInput struct {
    Generation RetrievalIndexGenerationRow
    Segments []RetrievalIndexSegmentRow
    Members []RetrievalIndexSegmentMember
    SnapshotRevision int64
    ExpectedActiveGenerationID string
    ExpectedPurgeEpoch int64
    ExpectedActiveSnapshotRevision int64
    ActivationMode RetrievalGenerationActivationMode
}
```

- [ ] Write failing tests for changed-root rejection, changed-epoch rejection, matching equal-snapshot rewrite, and a sub-5,000 omitted member that remains eligible L0 despite an old revision.
- [ ] Run `go test ./internal/store -run 'Test(CompleteRetrievalIndexGenerationRejectsChangedActiveRoot|CompleteRetrievalIndexGenerationRejectsChangedPurgeEpoch|CompleteRetrievalIndexGenerationAllowsSameSnapshotRewrite|NextRetrievalFlushWindowUsesMembershipBasedL0)' -count=1`; it must fail because the contract does not yet exist.

## Task 2: Membership L0 and CAS activation

**Files:** Modify `internal/store/retrieval_index_segments.go`, `internal/store/retrieval_embedding_profiles.go`, `internal/store/migrations.go`, and `internal/store/migrations_test.go`.

- [ ] Append migration 23. It recreates profile-accounting triggers and transactionally recomputes `l0_ready_count` and `active_tombstone_count` from active exact membership; it never trusts pre-migration counters.
- [ ] Change all L0 selection/counting to `ready AND NOT EXISTS active-generation member with same chunk_id, revision, vector_hash`; retain ordering by `revision,chunk_id`.
- [ ] Require the observed active generation, purge epoch, and snapshot in `CompleteRetrievalIndexGeneration`. Advance mode requires a larger snapshot; rewrite mode requires equality. Update the profile pointer using those expected values in `WHERE` and require one affected row.
- [ ] After recording proposed root references, reject duplicate usable active membership, recalculate indexed/L0/tombstone counters inside the transaction, then activate.
- [ ] Rewrite ready insert/update/delete trigger accounting to use active-member existence. A deletion of an active member increases tombstones; a deletion of L0 decreases only L0.
- [ ] Run `go test ./internal/store -run 'Test(CompleteRetrievalIndexGeneration|NextRetrievalFlushWindow|Migration)' -count=1` and `go test ./internal/store -count=1`; all must pass.
- [ ] Commit with `git commit --no-gpg-sign -m "feat: make semantic L0 membership-based"` after staging `internal/store`.

## Task 3: Preserve advancing flush behavior

**Files:** Modify `internal/semanticbuild/flush.go` and `internal/semanticbuild/flush_test.go`.

- [ ] Write failing tests asserting that `Flush` supplies `AdvanceSnapshot` for a newer revision prefix and `RewriteSnapshot` when membership L0 ends exactly at the active snapshot; both carry the observed root, epoch, and snapshot expectations.
- [ ] Run `go test ./internal/semanticbuild -run TestFlush -count=1`; the assertion must fail.
- [ ] Populate those fields from the observed flush window, selecting advance when its final revision is newer and rewrite when it equals the active snapshot. Do not add a replacement set or compaction caller.
- [ ] Run `go test ./internal/semanticbuild -run TestFlush -count=1` and `CGO_ENABLED=0 go test ./internal/semanticbuild -count=1`; all must pass without native libraries.
- [ ] Commit with `git commit --no-gpg-sign -m "feat: guard semantic flush activation"` after staging `internal/semanticbuild`.

## Task 4: Document and verify

**Files:** Modify `CHANGELOG.md` and `docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md`.

- [ ] Record CAS-protected root activation and membership-complement L0, explicitly deferring physical compaction, serving, GC, and production mutation.
- [ ] Run `task fmt`, `task lint`, `task test-ci`, `task build`, `CGO_ENABLED=0 go build ./cmd/dbrain`, and `git diff --check`; every command must exit zero.
- [ ] Commit the documentation and this plan, then push `codex/semantic-ann-lifecycle`.

## Self-Review

- The plan addresses the exact L0 complement, equal-snapshot rewrite, root/epoch CAS, and duplicate usable membership prerequisites from the approved size-tier compaction design.
- It explicitly excludes compaction input selection, native payload building, query serving, leases, garbage collection, and production database mutation.
