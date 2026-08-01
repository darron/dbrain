# Segmented ANN L0 Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (\`- [ ]\`) syntax for tracking.

**Goal:** Add immutable, verified segment and root artifacts plus a SQLite-proven, bounded 5,000-vector L0 flush lifecycle, without enabling ANN retrieval or requiring the optional native backend in normal builds.

**Architecture:** \`internal/semanticsegment\` owns content-addressed files under \`<cache>/semantic/<database-id>/<profile-id>\`, deterministic manifests, checksums, and atomic publication. SQLite remains authoritative: migration 22 records exact \`(chunk_id, revision, vector_hash)\` membership; \`semanticbuild.Flush\` captures the next revision-ordered L0 window, publishes segment/root first, then atomically records and activates the generation. Search/runtime configuration remain unchanged.

**Tech Stack:** Go 1.26, existing SQLite store/migration framework, SHA-256, canonical JSON with fixed structs, and standard-library filesystem durability primitives.

## Execution Record (2026-07-22)

Implemented on `codex/semantic-ann-lifecycle` in commits `6587bdc`,
`524d2d7`, and `97448d5`. The implemented scope is the tested segment/root/L0
foundation described here: a normal flush requires exactly 5,000 current ready
embedding revisions, publishes and reopens derived cache artifacts before the
SQLite activation transaction, and retains prior immutable segments in the next
root. It deliberately does not expose a CLI command or semantic serving path.

Verified with focused segment/store/flush suites, `task fmt`, `task lint`,
`task test-ci`, `task build`, and `CGO_ENABLED=0 go build ./cmd/dbrain`.

## Global Constraints

- SQLite is the authoritative corpus and membership source; segment files are replaceable derived cache artifacts.
- Store no source text or vectors in manifests. Membership is only ordinal, chunk ID, embedding revision, and vector hash; payload is opaque.
- Segment paths are exactly \`<cache>/semantic/<database-id>/<profile-id>/segments/<segment-hash>/\`; root paths are exactly \`<cache>/semantic/<database-id>/<profile-id>/generations/<generation-id>/\`.
- A segment is immutable and content-addressed by the SHA-256 of its canonical descriptor. Roots are atomically published before their SQLite generation can become active.
- A flush window is revision ordered, targets 5,000 vectors, rejects a caller limit above 10,000, and advances only to the greatest persisted member revision.
- This slice must not add a \`semantic flush\` CLI command, accept \`research.semantic.index_backend=usearch\`, enable semantic serving, read the production database, or invoke an embedding provider.
- Default \`CGO_ENABLED=0\` builds and tests remain independent of the temporary USearch dylib/header.

---

## File Structure

- \`internal/semanticsegment/types.go\`: canonical segment/root descriptors, path validation, deterministic hashes.
- \`internal/semanticsegment/publish.go\`: durable atomic publish and verified reopen for opaque payloads.
- \`internal/semanticsegment/segment_test.go\`: deterministic descriptor, corruption, duplicate-publish, and root-reference tests.
- \`internal/store/retrieval_index_segments.go\`: segment catalog/member records, revision-window selection, and proof-carrying generation activation.
- \`internal/store/retrieval_index_segments_test.go\`: membership, activation, stale-window, and L0-bound tests.
- \`internal/store/migrations.go\` and \`internal/store/retrieval_schema.go\`: append migration 22 and its idempotent tables/indexes.
- \`internal/semanticbuild/flush.go\`: store-plus-injected-builder orchestration.
- \`internal/semanticbuild/flush_test.go\`: fake-builder lifecycle tests; no native dependency.
- \`docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md\` and \`CHANGELOG.md\`: document implemented/non-implemented boundary.

### Task 1: Immutable segment and root artifacts

**Files:**

- Create: \`internal/semanticsegment/types.go\`
- Create: \`internal/semanticsegment/publish.go\`
- Create: \`internal/semanticsegment/segment_test.go\`

**Interfaces:**

\`\`\`go
const SchemaVersion = 1

type Member struct { Ordinal uint64; ChunkID, VectorHash string; Revision int64 }
type SegmentInput struct {
    DatabaseID, ProfileID, Backend, BackendVersion, DistanceMetric string
    Dimensions int
    Members []Member
    Payload func(io.Writer) error
}
type Segment struct { Hash, RelativePath string; Manifest Manifest }
type RootInput struct { DatabaseID, ProfileID, GenerationID string; SnapshotRevision, PurgeEpoch int64; Segments []RootSegment }
type Root struct { RelativePath string; Manifest RootManifest }
func PublishSegment(cacheDir string, input SegmentInput) (Segment, error)
func OpenSegment(cacheDir, databaseID, profileID, hash string) (Segment, error)
func PublishRoot(cacheDir string, input RootInput) (Root, error)
func OpenRoot(cacheDir, databaseID, profileID, generationID string) (Root, error)
\`\`\`

- [ ] **Step 1: Write failing artifact tests.**

\`\`\`go
func TestPublishSegmentReopensVerifiedPayload(t *testing.T) {
    cache := t.TempDir()
    segment, err := PublishSegment(cache, SegmentInput{
        DatabaseID: "db-1", ProfileID: "profile-1", Backend: "usearch",
        BackendVersion: "2.26.0", DistanceMetric: "cosine", Dimensions: 2,
        Members: []Member{{Ordinal: 0, ChunkID: "chunk-a", Revision: 7, VectorHash: "hash-a"}},
        Payload: func(w io.Writer) error { _, err := io.WriteString(w, "opaque"); return err },
    })
    if err != nil { t.Fatal(err) }
    reopened, err := OpenSegment(cache, "db-1", "profile-1", segment.Hash)
    if err != nil { t.Fatal(err) }
    if got := reopened.Manifest.Members[0].ChunkID; got != "chunk-a" { t.Fatalf("chunk=%q", got) }
}
func TestOpenSegmentRejectsTamperedPayload(t *testing.T) { /* mutate payload.bin; expect checksum error */ }
func TestPublishRootRejectsDuplicateAndUnknownSegmentOrder(t *testing.T) { /* expect validation error */ }
\`\`\`

- [ ] **Step 2: Run RED.**

Run: \`go test ./internal/semanticsegment -run 'Test(PublishSegment|OpenSegment|PublishRoot)' -count=1\`

Expected: package does not exist.

- [ ] **Step 3: Implement the smallest durable artifact layer.**

Validate before filesystem mutation: non-empty safe IDs, positive dimensions, strictly increasing zero-based ordinals, unique chunk IDs, positive revisions, and non-empty vector hashes. Serialize fixed structs (no maps), calculate SHA-256 for \`payload.bin\`, canonical member bytes, then descriptor bytes. Create a temporary directory beneath the final \`segments\` or \`generations\` parent, write \`0600\` files, sync each file and temporary directory, rename once, and sync its parent. An existing content hash must be reopened/validated rather than overwritten. \`OpenSegment\` recomputes all checksums; \`OpenRoot\` checks its descriptor and every referenced segment.

- [ ] **Step 4: Run GREEN.**

Run: \`go test ./internal/semanticsegment -count=1\`

Expected: PASS.

- [ ] **Step 5: Commit.**

\`\`\`bash
git add internal/semanticsegment
git commit --no-gpg-sign -m "feat: add immutable semantic segment artifacts"
\`\`\`

### Task 2: SQLite membership provenance and activation

**Files:**

- Modify: \`internal/store/migrations.go\`
- Modify: \`internal/store/retrieval_schema.go\`
- Create: \`internal/store/retrieval_index_segments.go\`
- Create: \`internal/store/retrieval_index_segments_test.go\`
- Modify: \`internal/store/retrieval_index_generations.go\`
- Modify: \`internal/store/migrations_test.go\`

**Interfaces:**

\`\`\`go
const RetrievalSegmentTarget = 5_000
const RetrievalSegmentHardLimit = 10_000

type RetrievalIndexSegmentRow struct {
    SegmentHash, ProfileID, Backend, BackendVersion, DistanceMetric, RelativeCachePath string
    Dimensions, IndexedChunkCount int
    MembershipHash, PayloadHash, ManifestHash string
}
type RetrievalIndexSegmentMember struct { SegmentHash, ChunkID, VectorHash string; Ordinal uint64; Revision int64 }
type RetrievalFlushWindow struct { Profile RetrievalEmbeddingProfileRow; Rows []RetrievalEmbeddingRow; SnapshotRevision int64 }
func (s *Store) NextRetrievalFlushWindow(ctx context.Context, profileID string, limit int) (RetrievalFlushWindow, error)
func (s *Store) CompleteRetrievalIndexGeneration(ctx context.Context, generation RetrievalIndexGenerationRow, segments []RetrievalIndexSegmentRow, members []RetrievalIndexSegmentMember, snapshotRevision int64) error
\`\`\`

- [ ] **Step 1: Write failing store tests.**

\`\`\`go
func TestNextRetrievalFlushWindowUsesReadyRevisionPrefix(t *testing.T) {
    // Seed ready revisions 1..3 and a blocked revision 4.
    // limit=2 must return revisions 1,2 and SnapshotRevision=2.
}
func TestCompleteRetrievalIndexGenerationActivatesProvenMemberRoot(t *testing.T) {
    // Matching segment/member evidence activates exactly one completed root,
    // sets snapshot=max member revision, and counts later ready vectors as L0.
}
func TestCompleteRetrievalIndexGenerationRejectsChangedMember(t *testing.T) {
    // Change vector_hash or revision after build; no generation becomes active.
}
\`\`\`

- [ ] **Step 2: Run RED.**

Run: \`go test ./internal/store -run 'Test(NextRetrievalFlushWindow|CompleteRetrievalIndexGeneration)' -count=1\`

Expected: compilation failure because lifecycle APIs/schema do not exist.

- [ ] **Step 3: Append migration 22 and implement transactional proof.**

Set \`currentSchemaVersion = 22\`; append \`retrieval_segment_membership_v1\` after migration 21. Add \`retrieval_index_segments\` (hash key), \`retrieval_index_segment_members\` (unique ordinal and chunk per segment), and \`retrieval_generation_segments\` (root references) with cache-relative paths/checksums only. Select current-parent, ready embeddings where \`revision > active_snapshot_revision\`, ordered by \`revision, chunk_id\`, capped by \`min(limit, 5_000)\`. Completion proves member rows still have ready status and matching revision/hash, retains all prior root segments in the new root, then atomically switches the active generation and recalculates snapshot/L0 counters. The legacy \`ActivateRetrievalIndexGeneration\` remains fail-closed for callers without stored evidence.

- [ ] **Step 4: Run GREEN and migration regression.**

\`\`\`bash
go test ./internal/store -run 'Test(NextRetrievalFlushWindow|CompleteRetrievalIndexGeneration|Migration)' -count=1
go test ./internal/store -count=1
\`\`\`

Expected: PASS; opening an already-migrated database twice is idempotent.

- [ ] **Step 5: Commit.**

\`\`\`bash
git add internal/store
git commit --no-gpg-sign -m "feat: prove segmented retrieval generation membership"
\`\`\`

### Task 3: Bounded flush orchestration without serving

**Files:**

- Create: \`internal/semanticbuild/flush.go\`
- Create: \`internal/semanticbuild/flush_test.go\`
- Modify: \`internal/semanticbuild/status.go\`

**Interfaces:**

\`\`\`go
type SegmentPayloadBuilder interface {
    Build(context.Context, []store.RetrievalEmbeddingRow) (func(io.Writer) error, error)
}
type FlushOptions struct {
    Profile embedding.Profile
    Backend, BackendVersion, DistanceMetric, CacheDir string
    Limit int
}
type FlushResult struct { GenerationID, SegmentHash string; Indexed, L0Ready int; SnapshotRevision int64 }
func Flush(ctx context.Context, st FlushStore, builder SegmentPayloadBuilder, opts FlushOptions) (FlushResult, error)
\`\`\`

- [ ] **Step 1: Write failing lifecycle tests.**

\`\`\`go
func TestFlushPublishesBeforeActivatingRoot(t *testing.T) {
    // Fake builder writes opaque bytes; root must reopen before the store reports active.
}
func TestFlushLeavesSQLiteInactiveWhenPublishFails(t *testing.T) {
    // Builder error must create no active root.
}
func TestFlushStopsAtFiveThousandAndReportsExactL0Tail(t *testing.T) {
    // 5,001 revision-ordered rows -> 5,000-member segment and L0=1.
}
\`\`\`

- [ ] **Step 2: Run RED.**

Run: \`go test ./internal/semanticbuild -run TestFlush -count=1\`

Expected: compilation failure because \`Flush\` does not exist.

- [ ] **Step 3: Implement only orchestration.**

Resolve immutable profile/database IDs through the store; select a window; assign dense ordinals in selected order; invoke the injected opaque payload builder; publish the segment; compose a root from existing active-root segments plus the new segment; reopen/validate the root; then call \`CompleteRetrievalIndexGeneration\`. On builder, publication, reopen, or transaction failure, return a descriptive error without enabling retrieval. Do not add Cobra wiring or instantiate USearch in the default build.

- [ ] **Step 4: Run GREEN.**

Run: \`go test ./internal/semanticbuild -run TestFlush -count=1\`

Expected: PASS without CGO flags or native library.

- [ ] **Step 5: Commit.**

\`\`\`bash
git add internal/semanticbuild
git commit --no-gpg-sign -m "feat: add bounded semantic segment flush lifecycle"
\`\`\`

### Task 4: Record the boundary and verify the branch

**Files:**

- Modify: \`docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md\`
- Modify: \`CHANGELOG.md\`

- [ ] **Step 1: Update status.**

Record that segment/root/L0 lifecycle is implemented and test-covered, while native serving, compaction, full-corpus drain, semantic \`on\`, packaging, and full resource gates remain unimplemented.

- [ ] **Step 2: Add changelog text.**

Describe immutable local derived-cache artifacts and fail-closed activation; state that this does not enable semantic results.

- [ ] **Step 3: Verify all supported paths.**

\`\`\`bash
task fmt
task lint
task test-ci
task build
CGO_ENABLED=0 go build ./cmd/dbrain
git diff --check
git status --short --branch
\`\`\`

Expected: formatter/linter/CI/build pass and default binary needs no native library.

- [ ] **Step 4: Commit and push.**

\`\`\`bash
git add CHANGELOG.md docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md
git commit --no-gpg-sign -m "docs: record segmented ANN lifecycle boundary"
git push origin codex/semantic-ann-lifecycle
\`\`\`

## Self-Review

- **Spec coverage:** segment format, membership map, checksums, canonical addressing, fixed paths, atomic publication, root-before-activation, L0 target/hard cap, corruption validation, and SQLite provenance map to Tasks 1–3.
- **Explicit deferrals:** size-tiered/tombstone compaction, native serving, release packaging, full-corpus drain, and evaluation are not hidden in this slice.
- **Type consistency:** Task 1 produces artifacts; Task 2 records and proves them; Task 3 composes both through a backend-agnostic flush seam.
