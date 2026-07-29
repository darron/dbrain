# Streaming Segment Payload Builder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an optional ANN payload builder ingest a known bounded number of embeddings one at a time, so a later compactor does not need a Go slice containing all selected source vectors.

**Architecture:** Add a backend-neutral streaming session contract beside the existing slice-based `SegmentPayloadBuilder`; retain the latter for the already-verified 5,000-vector flush. The optional USearch builder implements `Begin(expectedRows)`, `Add(row)`, `Finish()`, and idempotent `Close()`. `Build([]rows)` becomes a compatibility adapter over that session. `Finish` still necessarily serializes the opaque graph payload for `semanticsegment.PublishSegment`; this slice eliminates the source-row slice and its duplicate payload copy, not native graph or final-payload memory.

**Tech Stack:** Go 1.26, standard library, existing tagged `internal/semanticbuild` USearch builder and `semanticindex.USearch`; no migration, cache write, CLI, root activation, serving change, or default CGO dependency.

## Global Constraints

- The new generic interface is CGO-free and does not select a backend; the only concrete implementation remains behind `usearch && cgo`.
- A session accepts a positive known row count, reserves exactly that capacity, and rejects overflow, underflow at finish, malformed rows, cancellation, and use after close.
- `Build(ctx, rows)` keeps its existing external behavior by adapting through the session; flush callers require no changes.
- Finish must close native state before returning a payload writer. Closing twice is safe.
- Do not claim the native graph, `SerializedLength` buffer, or payload writer are streaming; record those residual peak-memory costs honestly.

## Task 1: Backend-neutral session types and failing tagged tests

**Files:** Create `internal/semanticbuild/stream_builder.go`; modify `internal/semanticbuild/usearch_builder_test.go`.

- [ ] Define `StreamingSegmentPayloadBuilder` with `Begin(context.Context, int) (StreamingSegmentPayloadSession, error)` and `StreamingSegmentPayloadSession` with `Add(context.Context, store.RetrievalEmbeddingRow) error`, `Finish(context.Context) (func(io.Writer) error, error)`, and `Close() error`.
- [ ] Add tagged tests that begin a two-row session, add rows one at a time, finish/reopen its payload, and prove ordinal order is `0,1`.
- [ ] Add tagged tests for expected-row overflow, underfilled finish, malformed vector, canceled add, and idempotent close/use-after-close rejection.
- [ ] Run the existing USearch-tagged semanticbuild tests; the new tests must initially fail because `Begin` does not exist.

## Task 2: USearch streaming session and compatibility adapter

**Files:** Modify `internal/semanticbuild/usearch_builder.go`.

- [ ] Add `USearchSegmentBuilder.Begin` that validates builder/context/count, creates `semanticindex.USearch`, reserves `expectedRows`, and returns a session holding the index and next ordinal.
- [ ] Add `Add` to validate context/dimensions/vector bytes, insert at the next dense ordinal, and reject calls after close or after the expected count.
- [ ] Add `Finish` to reject underfilled/closed sessions, export to one payload buffer, close the native index, and return a short-write-aware writer without a second payload copy.
- [ ] Make `Close` idempotently release native state; refactor `Build` to begin, add every supplied row, and finish through the new session.
- [ ] Run tagged semanticbuild tests and `CGO_ENABLED=0 go test ./internal/semanticbuild -count=1`.

## Task 3: Document residual boundary and verify

**Files:** Modify `CHANGELOG.md` and `docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md`.

- [ ] State that the optional builder can now ingest compaction rows incrementally, but no compactor invokes it yet and native graph/final-payload peak memory still need evaluation.
- [ ] Run `task fmt`, `task lint`, `task test-ci`, `task build`, `CGO_ENABLED=0 go build ./cmd/dbrain`, and `git diff --check`.
- [ ] Remove only a generated worktree-root `dbrain` binary if present, commit with `git commit --no-gpg-sign`, and push `codex/semantic-ann-lifecycle` over authenticated HTTPS.
