# Bounded Physical Segment Compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute one deterministic singleton/pair compaction by streaming current selected members into immutable replacement segments and atomically activating a replacement root.

**Architecture:** `semanticbuild.Compact` loads the read-only active-root snapshot, applies the pure planner, and returns a no-op when there is no eligible work. For a plan with physical output, it begins one streaming builder session per output, streams the selected active members under the snapshot CAS, records only manifest membership metadata, publishes/reopens replacement segments, retains every unselected active segment, publishes/reopens a rewrite root, and invokes existing `CompleteRetrievalIndexGeneration` with only new members. A stream-count mismatch or activation CAS failure leaves the published artifacts unreferenced and the old root active. The empty resulting-root case is rejected explicitly.

**Tech Stack:** Go 1.26, `internal/semanticbuild`, `semanticsegment`, existing SQLite store interfaces, injected streaming payload builder; no migration, CLI, serving path, native default dependency, cache cleanup, or production-corpus mutation.

## Global Constraints

- Select exactly the pure planner's singleton/pair; stream expected CAS facts and use `RetrievalGenerationRewriteSnapshot` because compaction does not advance the source snapshot.
- Build no ANN segment below 5,000 live rows. Such selected rows re-enter membership L0 by omission from the replacement root.
- Retain unselected active segments unchanged and insert only memberships for newly published segments; existing catalog membership is immutable and reused.
- Require streamed live count to match the planner's output count before publishing any root. Abort before activation on changed source state.
- Reject a compaction that would leave no root segments; empty-root/L0-only semantics require their own approved root-format and activation amendment.
- Publish and reopen every replacement segment and root before SQLite activation. Do not remove inputs, garbage-collect, expose a command, or enable serving.

## Tasks

### Task 1: Failing orchestration tests

**Files:** Create `internal/semanticbuild/compaction_execute_test.go`.

- [ ] Use a fake store/snapshot with one tombstone-heavy segment plus one retained segment and a fake streaming builder. Assert one replacement segment, retained-root membership, rewrite CAS, and only replacement members in completion input.
- [ ] Assert no plan is a no-op; changed stream count, builder failure, root reopen failure, or activation CAS failure leaves completion uncalled.
- [ ] Assert a singleton whose live output is exact L0 is allowed only with an unselected retained segment; the last-segment-to-L0 case returns a clear error before root publication.

### Task 2: Compact orchestration

**Files:** Create `internal/semanticbuild/compaction_execute.go`.

- [ ] Define a `CompactionStore` that combines database ID, active snapshot, selected-member stream, generation segment listing, and existing completion.
- [ ] Convert snapshot segments to planner input, begin one streaming payload session per physical output, partition deterministic streamed rows by output count, then finish/publish/reopen replacements.
- [ ] Compose sorted root/catalog rows from unselected existing plus replacements; publish/reopen root and complete a rewrite activation with snapshot CAS and new members only.
- [ ] Return diagnostic result facts without exposing a command.

### Task 3: Docs and verification

**Files:** Modify `CHANGELOG.md` and the accepted semantic retrieval design.

- [ ] Record bounded explicit compaction execution, its CAS/no-serving boundary, and the remaining empty-root/cache-cleanup limitations.
- [ ] Run focused, tagged optional-backend, `task fmt`, `task lint`, `task test-ci`, `task build`, CGO-free build, diff check; commit unsigned and push.
