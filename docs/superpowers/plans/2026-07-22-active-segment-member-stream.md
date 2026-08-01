# Active Segment Member Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream the current live embeddings for one or two selected active-root segments without materializing their vectors, mutating SQLite, or enabling semantic serving.

**Architecture:** `internal/store` receives a selected segment-hash set plus the active-root CAS facts observed by the compaction snapshot. In one read-only transaction it proves that the profile still points to that completed active generation and that every requested hash remains in that root. It then visits only memberships whose exact `(chunk_id, revision, vector_hash)` still joins a ready embedding and a current parent projection, in deterministic active-segment creation order and immutable member ordinal order. The callback receives one copied row at a time; the store retains no vector collection. A later compactor can build replacement payloads from this stream and must still use activation CAS before publishing its root.

**Tech Stack:** Go 1.26, SQLite read transaction, existing `internal/store`; no migration, CGO, native build, cache write, CLI, root activation, or serving-path change.

## Global Constraints

- Accept exactly one or two non-empty, unique segment hashes; this mirrors the singleton/pair plan and prevents an accidental full-root vector scan.
- Require expected active generation ID, purge epoch, and active snapshot revision from `RetrievalActiveSegmentCompactionSnapshot` before opening the member query.
- A streamed member is live only if the exact stored identity joins `retrieval_embeddings.status='ready'`, the current embedding revision/hash, `retrieval_chunks`, and `retrieval_parent_projections.status='current'`.
- Validate the encoded vector and current chunk text hash with the existing retrieval-embedding corruption check before invoking the visitor; corrupt current membership fails closed instead of feeding a native builder.
- Order callback delivery by selected segments' active-root order `(created_at, segment_hash)`, then `member.ordinal`; output segment construction can therefore assign deterministic new ordinals.
- The callback must not call the same store or mutate SQLite while the stream is open. The transaction is read-only and closes before returning.
- Do not add a `semantic` command, payload builder integration, cache access, native dependency, root replacement, cache cleanup, or retrieval serving behavior.

## Task 1: Failing store contract tests

**Files:** Create `internal/store/retrieval_compaction_stream_test.go`.

- [ ] Seed the existing two-segment active-root fixture and request `segment-alpha` and `segment-bravo` with its profile CAS values. Assert the visitor receives exactly three rows, in `alpha ordinal 0`, `alpha ordinal 1`, `bravo ordinal 0` order, with vector bytes only one row at a time.
- [ ] Replace `chunk-b`'s embedding and make `chunk-c`'s parent non-current. Assert the stream visits only the still-live `chunk-a`; stale indexed memberships are omitted rather than returned as historical vectors.
- [ ] Request a hash outside the active root and an altered expected snapshot revision. Assert each fails before the visitor is called.
- [ ] Have the visitor return a sentinel error after its first row. Assert the method returns that error, reports only successful visits, and closes its read transaction.
- [ ] Corrupt a selected current embedding's vector bytes directly in SQLite. Assert the stream fails before invoking the visitor.
- [ ] Run `go test ./internal/store -run TestStreamRetrievalActiveSegmentMembers -count=1`; it must fail because the stream API does not exist.

## Task 2: Read-only bounded live-member stream

**Files:** Create `internal/store/retrieval_compaction_stream.go`.

- [ ] Define `RetrievalActiveSegmentMemberStreamRequest` with `ProfileID`, `ExpectedActiveGenerationID`, `ExpectedPurgeEpoch`, `ExpectedActiveSnapshotRevision`, and `SegmentHashes`; define `RetrievalActiveSegmentMember` with `SegmentHash`, `Ordinal`, and `RetrievalEmbeddingRow`; define a visitor callback type.
- [ ] Validate the request before starting a transaction: profile/root IDs are non-empty, snapshot is positive, segment selection is one or two unique hashes, and the visitor is non-nil.
- [ ] In one `sql.TxOptions{ReadOnly:true}` transaction, load the active profile and reject any CAS mismatch; prove the expected generation is completed/active for that profile; load every requested segment from that generation and fail if any is absent or belongs to another profile.
- [ ] Query only the selected memberships joined to exact ready/current embeddings, scan one row, run `retrievalEmbeddingCorruptionReason` against the current chunk hash, invoke the visitor, then discard the row. Return the successfully visited count and wrap visitor/scan/iteration/commit failures with operation context.
- [ ] Run the focused test and `CGO_ENABLED=0 go test ./internal/store -count=1`.

## Task 3: Record the physical-compaction prerequisite and verify

**Files:** Modify `CHANGELOG.md` and `docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md`.

- [ ] State that selected live embeddings are now streamable under root CAS, while the current builder still materializes vectors and no payload/root mutation or semantic serving occurs.
- [ ] Run `task fmt`, `task lint`, `task test-ci`, `task build`, `CGO_ENABLED=0 go build ./cmd/dbrain`, and `git diff --check`.
- [ ] Remove only a generated worktree-root `dbrain` binary if present, commit with `git commit --no-gpg-sign`, and push `codex/semantic-ann-lifecycle` over authenticated HTTPS.

## Self-Review

- [ ] The stream is bounded to a singleton or pair and produces only current valid embedding rows; it cannot borrow inactive historical memberships.
- [ ] It intentionally does not solve native index memory use. The next independent slice changes `SegmentPayloadBuilder` into a streaming builder and then composes it with this store seam.
- [ ] It intentionally does not solve the empty-root/L0-only transition; that remains a root-format and activation-contract amendment before physical compaction execution.
