# Semantic ANN Runtime Admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the normal `dbrain` runtime explicitly report USearch capability and safely admit a proven PR #100 segmented generation, while retaining exact search for small roots and fail-open lexical behavior when native ANN is unsupported or broken.

**Architecture:** SQLite remains authoritative. Readiness proves bounded generation metadata from the same SQLite read transaction used for all other admission facts; opening the immutable cache then proves the root and every segment payload before native search is exposed. A build-tag-neutral capability value is the single source of truth for both `semantic status` and research runtime admission. This first stacked PR does not automate semantic maintenance; it only turns the already-built segmented lifecycle into an explicitly gated serving path.

**Tech Stack:** Go 1.24, SQLite, Cobra, CGO build tags, USearch Go bindings pinned to USearch `2.26.0`, immutable semantic root/segment manifests, Go tests with race coverage.

## Global Constraints

- Work only in `/Users/darron/src/dbrain/.worktrees/semantic-ann-automatic-sync` on `codex/semantic-ann-automatic-sync`.
- The base must remain PR #100 head `2d7ba53ce40524c427dd808345f74968b0be6be3`.
- Do not edit PR #100 or merge it while implementing this stacked PR.
- Do not mutate `data/brain.db` or the production XDG database. The representative corpus smoke test uses only `/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain/brain.db`.
- Semantic mode remains explicit. `off` remains off; this PR does not add a maintenance toggle or change `sync all`.
- SQLite is authoritative. Native ordinals remain untrusted candidates and still pass through current SQLite validation plus exact reranking.
- A normal CGO-free build must compile and report `unsupported`; it must not acquire a native library dependency.
- A tagged build must report `supported_ready` only after a tiny native create/add/search/close probe succeeds.
- An active generation is admitted only when its SQLite generation metadata, root manifest, segment manifests, and payloads all agree on database, profile, generation, snapshot, purge epoch, backend, backend version, metric, dimensions, membership, and checksums.
- The SQLite readiness proof remains bounded by
  `semanticRuntimeAdmissionTimeout` (`250ms`). Subsequent native root/payload
  loading uses the caller context, checks cancellation between stages, and
  cannot preempt the native `LoadBuffer` call. Do not scan all generation
  members during ordinary admission.
- An unsupported or broken native backend is a semantic fail-open condition for query commands: semantic is unavailable, the embedding provider is not constructed, and lexical retrieval remains usable.
- The final automatic-sync error behavior belongs to stacked PR 3. Do not make ordinary query commands fail hard in this PR.
- Add a changelog entry because backend reporting and normal runtime behavior are user-visible.
- Use test-driven development: add each failing test before its production change.
- After all focused tests, run `task fmt`, `task lint`, `task test-ci`, the tagged test gate, `task build`, and the representative-corpus normal-CLI smoke test.

---

## Task 1: Add a build-tag-neutral USearch capability contract

**Files:**

- Create: `internal/semanticindex/capability.go`
- Create: `internal/semanticindex/capability_test.go`
- Create: `internal/semanticindex/capability_default.go`
- Create: `internal/semanticindex/capability_default_test.go`
- Create: `internal/semanticindex/capability_usearch.go`
- Create: `internal/semanticindex/capability_usearch_test.go`
- Modify: `internal/semanticindex/usearch_adapter.go:13`

**Interfaces:**

- Consumes: existing tagged `NewUSearch(USearchOptions) (*USearch, error)`, `USearch.Add`, `USearch.Search`, and `USearch.Close`.
- Produces: common `BackendUSearch`, `USearchVersion`, `CapabilityState`, `Capability`, `Capability.Admit(string, string) (bool, string)`, and build-specific `RuntimeCapability() Capability`.

- [ ] **Step 1: Write common contract tests**

Create `internal/semanticindex/capability_test.go` and cover:

```go
func TestCapabilityAdmit(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		backend    string
		version    string
		wantOK     bool
		wantReason string
	}{
		{"ready", Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}, BackendUSearch, USearchVersion, true, ""},
		{"unsupported", Capability{State: CapabilityUnsupported}, BackendUSearch, USearchVersion, false, "native_backend_unsupported"},
		{"broken", Capability{State: CapabilitySupportedBroken, Backend: BackendUSearch, Version: USearchVersion, Reason: "probe failed"}, BackendUSearch, USearchVersion, false, "native_backend_broken: probe failed"},
		{"backend mismatch", Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}, "other", USearchVersion, false, "native_backend_provenance_mismatch"},
		{"version mismatch", Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}, BackendUSearch, "other", false, "native_backend_provenance_mismatch"},
	}
	// Assert exact stable reason prefixes. Do not expose filesystem paths.
}
```

The production API must be:

```go
const (
	BackendUSearch = "usearch"
	USearchVersion = "2.26.0"
)

type CapabilityState string

const (
	CapabilityUnsupported     CapabilityState = "unsupported"
	CapabilitySupportedReady  CapabilityState = "supported_ready"
	CapabilitySupportedBroken CapabilityState = "supported_broken"
)

type Capability struct {
	State   CapabilityState `json:"state"`
	Backend string          `json:"backend,omitempty"`
	Version string          `json:"version,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

func (c Capability) Admit(backend, version string) (bool, string)
func RuntimeCapability() Capability
```

Remove the tagged-only `BackendUSearch` constant from `usearch_adapter.go`; the common file owns persisted backend/version identifiers so CGO-free status code can describe an existing generation.

- [ ] **Step 2: Prove the common tests fail**

Run:

```bash
go test ./internal/semanticindex -run 'TestCapabilityAdmit'
```

Expected: compile failure because `Capability`, its states, and `USearchVersion` do not exist.

- [ ] **Step 3: Implement the common value and default build**

In `capability.go`, make `Admit`:

1. return `native_backend_unsupported` for `unsupported`;
2. return `native_backend_broken` plus the sanitized probe reason for `supported_broken`;
3. reject any unknown state as broken;
4. reject a backend/version mismatch with `native_backend_provenance_mismatch`;
5. return success only for `supported_ready` with exact backend and version equality.

In `capability_default.go`:

```go
//go:build !usearch || !cgo

package semanticindex

func RuntimeCapability() Capability {
	return Capability{State: CapabilityUnsupported}
}
```

Add a default-build test asserting that exact JSON fields are stable and `RuntimeCapability().State == CapabilityUnsupported`.

- [ ] **Step 4: Write tagged probe tests**

In `capability_usearch_test.go`, cover:

- successful create/reserve/add/search/close returns `supported_ready`, `usearch`, `2.26.0`;
- constructor error returns `supported_broken`;
- add/search/close error returns `supported_broken`;
- a successful probe proves ordinal `0` is the nearest result;
- `RuntimeCapability` is immutable across repeated calls.

Use an internal test seam:

```go
type capabilityProbeIndex interface {
	Reserve(int) error
	Add(...HNSWNode) error
	Search([]float32, int) ([]HNSWHit, error)
	Close() error
}

type capabilityIndexFactory func(USearchOptions) (capabilityProbeIndex, error)

func probeUSearch(factory capabilityIndexFactory) Capability
```

Do not add an environment variable or public injection hook.

- [ ] **Step 5: Implement and run the native probe**

In `capability_usearch.go`, cache only the real production probe:

```go
var (
	runtimeCapabilityOnce sync.Once
	runtimeCapability     Capability
)

func RuntimeCapability() Capability {
	runtimeCapabilityOnce.Do(func() {
		runtimeCapability = probeUSearch(func(options USearchOptions) (capabilityProbeIndex, error) {
			return NewUSearch(options)
		})
	})
	return runtimeCapability
}
```

The probe uses two normalized 2D vectors, searches for `[1, 0]`, requires ordinal `0` first, and always closes the index. Combine the primary failure and close failure without losing either cause.

Run the normal gate:

```bash
go test ./internal/semanticindex -run 'TestCapability|TestRuntimeCapability'
```

Run the tagged gate against the pinned development library:

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -tags usearch ./internal/semanticindex -run 'TestCapability|TestRuntimeCapability'
```

Expected: both gates pass; the untagged binary remains free of USearch symbols.

- [ ] **Step 6: Commit**

```bash
git add internal/semanticindex/capability.go \
  internal/semanticindex/capability_test.go \
  internal/semanticindex/capability_default.go \
  internal/semanticindex/capability_default_test.go \
  internal/semanticindex/capability_usearch.go \
  internal/semanticindex/capability_usearch_test.go \
  internal/semanticindex/usearch_adapter.go
git commit -m "feat(semantic): report native backend capability"
```

---

## Task 2: Prove active generation metadata in bounded readiness snapshots

**Files:**

- Create: `internal/store/semantic_generation_admission.go`
- Create: `internal/store/semantic_generation_admission_test.go`
- Modify: `internal/semanticreadiness/readiness.go:52-105`
- Modify: `internal/semanticreadiness/readiness_test.go:1-260`
- Modify: `internal/store/semantic_runtime_readiness.go:96-147`
- Modify: `internal/store/semantic_readiness.go:430-496`
- Modify: `internal/store/semantic_runtime_readiness_bounded_test.go:1-180`
- Modify: `internal/store/semantic_readiness_snapshot_test.go:1-700`

**Interfaces:**

- Consumes: the immutable generation/segment rows activated by `Store.CompleteRetrievalIndexGeneration`.
- Produces: `proveActiveSemanticGenerationMetadata(context.Context, *sql.Tx, embedding.Profile, *semanticreadiness.Snapshot) error` and the active-generation provenance fields on `semanticreadiness.Snapshot` used by Tasks 3 and 4.

- [ ] **Step 1: Add failing readiness tests for a real completed generation**

Seed a ready profile with `CompleteRetrievalIndexGeneration` rather than direct inserts. Assert both:

```go
statusSnapshot, err := st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
runtimeSnapshot, err := st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, 25_000, now)
```

return:

```go
ActiveGenerationValid:          true
ActiveGenerationID:             generationID
ActiveGenerationBackend:        "usearch"
ActiveGenerationBackendVersion: "2.26.0"
ActiveGenerationDistanceMetric: "cosine"
ActiveGenerationDimensions:     profile.Dimensions
ActiveSnapshotRevision:          snapshotRevision
```

Then corrupt one field at a time in a transaction and assert both readers fail closed:

- generation profile ID differs;
- generation is not active and completed;
- generation backend/version/metric/dimensions differ from segment provenance;
- generation indexed count differs from active profile count;
- source manifest hash or relative cache path is empty;
- a generation-to-segment row is missing;
- segment count sum differs from generation count;
- segment path, membership hash, payload hash, or manifest hash is empty.

The reader should return a snapshot with `ActiveGenerationValid == false`, not hide the state as a SQL error. Actual query failures still return errors.

- [ ] **Step 2: Prove the new tests fail**

Run:

```bash
go test ./internal/store -run 'TestSemantic(Readiness|RuntimeReadiness).*ActiveGeneration'
```

Expected: the current hard-coded `snapshot.ActiveGenerationValid = snapshot.ActiveGenerationID == ""` leaves every seeded active generation invalid.

- [ ] **Step 3: Extend the immutable readiness snapshot**

Add:

```go
ActiveSnapshotRevision          int64  `json:"active_snapshot_revision"`
ActiveGenerationBackend        string `json:"active_generation_backend,omitempty"`
ActiveGenerationBackendVersion string `json:"active_generation_backend_version,omitempty"`
ActiveGenerationDistanceMetric string `json:"active_generation_distance_metric,omitempty"`
ActiveGenerationDimensions     int    `json:"active_generation_dimensions,omitempty"`
ActiveGenerationProblem        string `json:"active_generation_problem,omitempty"`
```

Keep the existing `ActiveGenerationValid` boolean. Update `Evaluate` so an invalid active generation returns `StateCorrupt` with a stable prefix:

```text
active semantic generation provenance is unproven
```

Append only a bounded, path-free `ActiveGenerationProblem` detail.

- [ ] **Step 4: Implement one shared bounded metadata proof**

Create:

```go
func proveActiveSemanticGenerationMetadata(
	ctx context.Context,
	tx *sql.Tx,
	profile embedding.Profile,
	snapshot *semanticreadiness.Snapshot,
) error
```

Behavior:

1. If no active generation exists, set `ActiveGenerationValid = true` and return.
2. Load the active generation by primary key and require:
   - the row profile equals `snapshot.ProfileID`;
   - `active = 1`;
   - `build_status = completed`;
   - backend, backend version, source manifest hash, and relative path are non-empty;
   - metric is `cosine`;
   - dimensions equal the configured profile;
   - indexed count equals the profile active indexed count;
   - snapshot revision is positive, no greater than latest revision, and matches the profile watermark.
3. Aggregate only the generation's segment metadata:

```sql
SELECT
	COUNT(gs.segment_hash),
	COALESCE(SUM(s.indexed_chunk_count), 0),
	COALESCE(SUM(
		s.profile_id != g.profile_id OR
		s.backend != g.backend OR
		s.backend_version != g.backend_version OR
		s.dimensions != g.dimensions OR
		s.distance_metric != g.distance_metric OR
		s.indexed_chunk_count <= 0 OR
		TRIM(s.relative_cache_path) = '' OR
		TRIM(s.membership_hash) = '' OR
		TRIM(s.payload_hash) = '' OR
		TRIM(s.manifest_hash) = ''
	), 0)
FROM retrieval_index_generations g
LEFT JOIN retrieval_generation_segments gs
	ON gs.generation_id = g.generation_id
LEFT JOIN retrieval_index_segments s
	ON s.segment_hash = gs.segment_hash
WHERE g.generation_id = ?
GROUP BY g.generation_id
```

Require at least one segment, no mismatches, and the segment indexed-count sum to equal the generation indexed count.

Do **not** count or scan `retrieval_index_segment_members` in runtime admission. `CompleteRetrievalIndexGeneration` already performs the full member proof before activation, and immutable manifest hashes carry that proof into bounded admission. The cache-opening stage re-verifies every immutable member manifest before serving.

Store any invariant failure in `ActiveGenerationProblem`, leave `ActiveGenerationValid` false, and return nil. Return an error only for query/context failures.

- [ ] **Step 5: Call the helper from both readiness readers**

Replace both hard-coded assignments. Also stop scanning the profile watermark into a local/discarded variable; scan it into `snapshot.ActiveSnapshotRevision`.

Call the helper before committing each read transaction:

```go
if snapshot.ProfileExists {
	if err := proveActiveSemanticGenerationMetadata(ctx, tx, profile, &snapshot); err != nil {
		return semanticreadiness.Snapshot{}, err
	}
} else {
	snapshot.ActiveGenerationValid = snapshot.ActiveGenerationID == ""
}
```

Delete the obsolete comment in `semantic_readiness.go` claiming the schema cannot persist segmented lifecycle proof.

- [ ] **Step 6: Protect the 250ms admission query shape**

Extend `semantic_runtime_readiness_bounded_test.go` with `EXPLAIN QUERY PLAN` coverage for the generation and segment aggregate query. Assert:

- lookup of `retrieval_index_generations` uses its primary-key/unique index;
- lookup of `retrieval_generation_segments` uses its generation key;
- lookup of `retrieval_index_segments` uses its primary key;
- the plan does not mention `retrieval_index_segment_members`;
- there is no temp B-tree sort.

Run:

```bash
go test ./internal/store ./internal/semanticreadiness -run 'ActiveGeneration|GenerationMetadata|RuntimeReadiness.*Plan|Evaluate'
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/store/semantic_generation_admission.go \
  internal/store/semantic_generation_admission_test.go \
  internal/store/semantic_runtime_readiness.go \
  internal/store/semantic_readiness.go \
  internal/store/semantic_runtime_readiness_bounded_test.go \
  internal/store/semantic_readiness_snapshot_test.go \
  internal/semanticreadiness/readiness.go \
  internal/semanticreadiness/readiness_test.go
git commit -m "fix(semantic): admit proven active generations"
```

---

## Task 3: Enforce USearch provenance in verification and cache opening

**Files:**

- Modify: `internal/semanticbuild/verify.go:111-150`
- Modify: `internal/semanticbuild/run_test.go:780-980`
- Modify: `internal/semanticindex/usearch_root_loader.go:39-71`
- Modify: `internal/semanticindex/usearch_adapter_test.go:51-120`
- Modify: `internal/brainresearch/runtime_native_searcher_usearch.go:18-33`

**Interfaces:**

- Consumes: `semanticindex.BackendUSearch`, `semanticindex.USearchVersion`, and the active-generation snapshot fields produced by Task 2.
- Produces: `semanticindex.USearchRootExpectations` and the strengthened `OpenUSearchRoot(string, string, string, string, USearchRootExpectations) (*USearchRoot, error)` contract used by tagged runtime serving.

- [ ] **Step 1: Write failing verification tests**

Change verification fixtures from the obsolete `"exact"` active-generation backend to:

```go
GenerationBackend:         semanticindex.BackendUSearch
GenerationBackendVersion:  semanticindex.USearchVersion
GenerationDistanceMetric:  "cosine"
GenerationDimensions:      profile.Dimensions
GenerationStatus:          store.RetrievalGenerationCompleted
GenerationActive:          true
```

Add table tests rejecting:

- `exact`;
- unknown backend;
- wrong USearch version;
- non-cosine metric;
- wrong dimensions;
- inactive or non-completed generation.

- [ ] **Step 2: Prove verification fails on valid USearch provenance**

Run:

```bash
go test ./internal/semanticbuild -run 'TestRunVerify.*Generation|TestValidateVerificationState'
```

Expected: valid `usearch/2.26.0/cosine` is rejected because current code only accepts `"exact"`.

- [ ] **Step 3: Fix verification policy**

Import `internal/semanticindex` and require exactly:

```go
state.GenerationBackend == semanticindex.BackendUSearch
state.GenerationBackendVersion == semanticindex.USearchVersion
state.GenerationDistanceMetric == "cosine"
state.GenerationDimensions == profile.Dimensions
```

Keep all existing active/completed/count/revision checks. An absent active generation remains valid only when all active root aggregates are zero.

- [ ] **Step 4: Write failing root provenance tests**

Introduce:

```go
type USearchRootExpectations struct {
	Index            USearchOptions
	SnapshotRevision int64
	PurgeEpoch       int64
	BackendVersion   string
}
```

Update the loader test fixture to publish a root and segment with `BackendVersion: USearchVersion`. Add rejection cases for:

- root snapshot revision mismatch;
- root purge epoch mismatch;
- segment backend mismatch;
- segment backend-version mismatch;
- segment metric mismatch;
- segment dimensions mismatch;
- existing membership/payload/descriptor checksum mismatch cases.

- [ ] **Step 5: Strengthen `OpenUSearchRoot`**

Change the signature:

```go
func OpenUSearchRoot(
	cacheDir, databaseID, profileID, generationID string,
	expect USearchRootExpectations,
) (*USearchRoot, error)
```

After `semanticsegment.OpenRoot`, require:

```go
root.Manifest.SnapshotRevision == expect.SnapshotRevision
root.Manifest.PurgeEpoch == expect.PurgeEpoch
```

For every opened segment require:

```go
segment.Manifest.Backend == BackendUSearch
segment.Manifest.BackendVersion == expect.BackendVersion
segment.Manifest.DistanceMetric == "cosine"
segment.Manifest.Dimensions == expect.Index.Dimensions
```

The existing `semanticsegment.OpenRoot`/`OpenSegment` checksum and descriptor checks remain mandatory. Close every already-opened native index on any failure.

Update `runtime_native_searcher_usearch.go`:

```go
root, err := semanticindex.OpenUSearchRoot(
	cfg.CacheDir,
	databaseID,
	snapshot.ProfileID,
	snapshot.ActiveGenerationID,
	semanticindex.USearchRootExpectations{
		Index: semanticindex.USearchOptions{
			Dimensions: profile.Dimensions,
			Connectivity: 16,
			ExpansionAdd: 128,
			ExpansionSearch: 256,
		},
		SnapshotRevision: snapshot.ActiveSnapshotRevision,
		PurgeEpoch:       snapshot.ProfilePurgeEpoch,
		BackendVersion:   snapshot.ActiveGenerationBackendVersion,
	},
)
```

- [ ] **Step 6: Run normal and tagged focused tests**

```bash
go test ./internal/semanticbuild -run 'Verify'
```

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -tags usearch ./internal/semanticindex ./internal/brainresearch -run 'USearchRoot|NativeSearcher'
```

Expected: verification accepts only the pinned generation provenance, and the tagged loader accepts only a root matching the admitted SQLite watermark.

- [ ] **Step 7: Commit**

```bash
git add internal/semanticbuild/verify.go \
  internal/semanticbuild/run_test.go \
  internal/semanticindex/usearch_root_loader.go \
  internal/semanticindex/usearch_adapter_test.go \
  internal/brainresearch/runtime_native_searcher_usearch.go
git commit -m "fix(semantic): verify native root provenance"
```

---

## Task 4: Use one capability decision in status and runtime admission

**Files:**

- Modify: `internal/semanticbuild/status.go:13-60`
- Modify: `internal/semanticbuild/run_test.go:990-1060`
- Modify: `internal/app/semantic.go:24-48,107-175`
- Modify: `internal/app/semantic_output.go:10-37`
- Modify: `internal/app/app_test.go:330-390`
- Modify: `internal/brainresearch/runtime.go:19-140`
- Modify: `internal/brainresearch/runtime_test.go:45-260`
- Modify: `internal/brainresearch/runtime_native_searcher_default.go:1-25`

**Interfaces:**

- Consumes: `semanticindex.RuntimeCapability`, `Capability.Admit`, and the generation/cache proof from Tasks 2 and 3.
- Produces: capability-aware `semanticbuild.ReadStatus`, `semanticDeps.capability`, and `runtimeDeps.capability`; normal status and query runtime now share the same backend admission decision.

- [ ] **Step 1: Write failing status tests**

Extend `semanticbuild.Status` with:

```go
BackendCapability semanticindex.Capability `json:"backend_capability"`
```

Change `ReadStatus` to receive a capability before `now`:

```go
func ReadStatus(
	ctx context.Context,
	st StatusStore,
	profile embedding.Profile,
	configured bool,
	enabled bool,
	exactMaxChunks int,
	capability semanticindex.Capability,
	now time.Time,
) (Status, error)
```

Add tests:

- no active generation: exact-small readiness remains `ready` even when capability is unsupported;
- active USearch generation plus matching `supported_ready`: `ready`, searchable;
- active USearch generation plus `unsupported`: `unavailable`, not searchable, reason `native_backend_unsupported`;
- active USearch generation plus `supported_broken`: `unavailable`, not searchable, reason starts `native_backend_broken`;
- active generation plus backend/version mismatch: `unavailable`, not searchable, reason `native_backend_provenance_mismatch`;
- JSON always contains `backend_capability`;
- all slices remain `[]`, never `null`.

- [ ] **Step 2: Prove the status tests fail**

Run:

```bash
go test ./internal/semanticbuild ./internal/app -run 'SemanticStatus|ReadStatus.*Capability'
```

Expected: compile/test failures because status has no capability and active-generation availability is not evaluated.

- [ ] **Step 3: Implement capability-aware status**

Populate `BackendCapability` for every status, including off and unconfigured modes.

After ordinary readiness evaluation, only if `snapshot.ActiveGenerationID != ""`, call:

```go
ok, reason := capability.Admit(
	snapshot.ActiveGenerationBackend,
	snapshot.ActiveGenerationBackendVersion,
)
```

If not admitted, replace the decision with:

```go
semanticreadiness.Decision{
	State: semanticreadiness.StateUnavailable,
	Reason: reason,
	Searchable: false,
}
```

Add `capability func() semanticindex.Capability` to `semanticDeps`, defaulting to `semanticindex.RuntimeCapability`.

Human status output must include exactly one explicit line:

```text
Backend: state=supported_ready backend=usearch version=2.26.0
```

For unsupported builds, omit empty backend/version values but still print:

```text
Backend: state=unsupported
```

If broken, append a single sanitized `reason=` field.

- [ ] **Step 4: Write failing runtime tests**

Add `capability func() semanticindex.Capability` to `runtimeDeps`. Cover:

- exact-small readiness does not call or require native search;
- active root plus unsupported capability returns a builder with semantic `unavailable`, no retriever, no provider call, and `native_backend_unsupported`;
- broken capability behaves the same with explicit broken reason;
- backend/version mismatch behaves the same with provenance mismatch;
- matching ready capability calls the searcher once, then constructs the provider;
- readiness remains evaluated before capability/provider/searcher;
- caller cancellation and the 250ms fail-open budget remain unchanged.

The valid active-root fixture must populate every new snapshot provenance field.

- [ ] **Step 5: Implement runtime capability admission**

Default `runtimeDeps.capability` to `semanticindex.RuntimeCapability`. After `semanticreadiness.Evaluate(snapshot)` and before opening a native root:

```go
if b.semanticReadiness.Searchable && snapshot.ActiveGenerationID != "" {
	ok, reason := deps.capability().Admit(
		snapshot.ActiveGenerationBackend,
		snapshot.ActiveGenerationBackendVersion,
	)
	if !ok {
		b.semanticReadiness = semanticreadiness.Decision{
			State: semanticreadiness.StateUnavailable,
			Reason: reason,
			Searchable: false,
		}
		return b, nil
	}
}
```

Keep the default tagged/untagged searcher split as a final defense. Rename `errNativeBackendUnavailable` only if needed for compatibility tests; runtime availability must now normally be decided before the searcher is called.

- [ ] **Step 6: Run focused tests**

```bash
go test ./internal/semanticbuild ./internal/app ./internal/brainresearch -run 'Capability|SemanticStatus|RuntimeAdmission'
```

Expected: all pass, and provider construction remains after both readiness and cache opening.

- [ ] **Step 7: Commit**

```bash
git add internal/semanticbuild/status.go \
  internal/semanticbuild/run_test.go \
  internal/app/semantic.go \
  internal/app/semantic_output.go \
  internal/app/app_test.go \
  internal/brainresearch/runtime.go \
  internal/brainresearch/runtime_test.go \
  internal/brainresearch/runtime_native_searcher_default.go
git commit -m "feat(semantic): gate runtime on native capability"
```

---

## Task 5: Prove normal tagged runtime search with a real immutable root

**Files:**

- Create: `internal/brainresearch/runtime_usearch_integration_test.go`
- Modify: `internal/semanticindex/usearch_adapter_test.go:120-380`

**Interfaces:**

- Consumes: the capability, bounded readiness, strengthened root loader, `semanticbuild.NewUSearchSegmentBuilder`, `semanticbuild.Flush`, and `runtimeSemanticSearcher` delivered by Tasks 1-4.
- Produces: a tagged integration contract proving a real store/root/query traverses native candidate search and SQLite exact reranking.

- [ ] **Step 1: Add a tagged end-to-end runtime test**

Create `runtime_usearch_integration_test.go` with `//go:build usearch && cgo`.

The test must:

1. create a temporary SQLite store;
2. create two current chunks with ready, normalized 2D embeddings through public store APIs;
3. build a real USearch segment with `semanticbuild.NewUSearchSegmentBuilder`;
4. flush and activate the segment as `usearch/2.26.0/cosine`;
5. obtain `SemanticRuntimeReadinessSnapshotAt` and assert `ready` plus valid generation provenance;
6. publish/open the real immutable cache via `runtimeSemanticSearcher`;
7. search for one query vector;
8. assert the nearest chunk is returned from backend `usearch`;
9. assert SQLite exact reranking determines the final order;
10. close the searcher/root and store without leaks.

Use this fixture shape so the test is reproducible without private store helpers:

```go
func TestRuntimeUSearchIntegration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	st, err := store.Open(filepath.Join(root, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	profile := semanticbuild.Profile(embedding.Info{
		Provider: "fake", Model: "fake-v1", Dimensions: 2,
	})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	for index, text := range []string{"nearest semantic evidence", "distant semantic evidence"} {
		sourceKey := fmt.Sprintf("source:runtime-%d", index)
		url := fmt.Sprintf("https://example.com/runtime-%d", index)
		upserted, err := st.UpsertSource(ctx, model.SourceCandidate{
			OriginalURL: url, CanonicalURL: url, NormalizedURL: url,
			SourceType: "article", Domain: "example.com",
			SourceKey: sourceKey, NotePath: sourceKey + ".md",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveSourceExtraction(ctx, upserted.SourceID, model.ExtractResult{
			CanonicalURL: url, FinalURL: url, Title: sourceKey,
			Content: text, Status: "ok", FetchedAt: time.Now().UTC(),
			Tool: "test", ToolVersion: "1",
		}, "content-"+sourceKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := semanticbuild.RunChunk(ctx, st, semanticbuild.ChunkOptions{
		Limit: 10, UntilIdle: true,
	}); err != nil {
		t.Fatal(err)
	}
	chunks, err := st.ListChunksNeedingEmbeddingForProfileAt(
		ctx, profile, "", 10, time.Now().UTC(),
	)
	if err != nil || len(chunks) != 2 {
		t.Fatalf("chunks=%+v err=%v", chunks, err)
	}
	vectors := [][]float32{{1, 0}, {0, 1}}
	for index, chunk := range chunks {
		if err := st.PutRetrievalEmbedding(ctx, store.RetrievalEmbeddingRow{
			ChunkID: chunk.ChunkID, ProfileID: profileID,
			Provider: profile.Provider, Model: profile.Model,
			Dimensions: profile.Dimensions,
			Representation: profile.Representation,
			Normalization: profile.Normalization,
			VectorBytes: embedding.EncodeDenseF32(vectors[index]),
			ChunkTextHash: chunk.ChunkTextHash,
			Status: store.RetrievalEmbeddingReady,
			AttemptCount: 1, EmbeddedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	builder, err := semanticbuild.NewUSearchSegmentBuilder(
		semanticbuild.USearchSegmentBuilderOptions{Dimensions: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	flushed, err := semanticbuild.Flush(ctx, st, builder, semanticbuild.FlushOptions{
		Profile: profile, Backend: semanticindex.BackendUSearch,
		BackendVersion: semanticindex.USearchVersion,
		DistanceMetric: "cosine", CacheDir: cache, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(
		ctx, profile, semanticreadiness.DefaultExactMaxChunks, time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Configured, snapshot.Enabled = true, true
	if decision := semanticreadiness.Evaluate(snapshot); decision.State != semanticreadiness.StateReady {
		t.Fatalf("decision=%+v snapshot=%+v", decision, snapshot)
	}
	searcher, err := runtimeSemanticSearcher(
		ctx, st, config.Config{CacheDir: cache}, profile, snapshot,
		semanticreadiness.DefaultExactMaxChunks,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeable, ok := searcher.(interface{ Close() error }); ok {
			_ = closeable.Close()
		}
	}()
	hits, status, err := searcher.Search(ctx, []float32{1, 0}, semanticindex.SearchOptions{
		Profile: profile, Limit: 2,
		MaxChunks: semanticreadiness.DefaultExactMaxChunks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != semanticindex.StateSearched ||
		status.Backend != semanticindex.BackendUSearch ||
		status.GenerationID != flushed.GenerationID ||
		len(hits) != 2 ||
		hits[0].ChunkID != chunks[0].ChunkID ||
		hits[0].Distance > hits[1].Distance {
		t.Fatalf("hits=%+v status=%+v", hits, status)
	}
}
```

If chunk ID ordering does not match source insertion order, map vectors by each chunk's `ParentSourceKey` instead of relying on slice position. Keep the expected nearest chunk explicit.

- [ ] **Step 2: Prove the tagged test fails before all integration is complete**

Run:

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -tags usearch ./internal/brainresearch -run 'TestRuntimeUSearchIntegration'
```

Expected before final wiring: failure at active-generation admission or root provenance.

- [ ] **Step 3: Complete only the minimum integration needed by the test**

Do not add a second runtime path. Repair any mismatched fixture/build provenance so the test exercises:

```text
SQLite readiness proof
  -> capability probe
  -> immutable root and segment verification
  -> native approximate candidates
  -> authoritative SQLite validation
  -> exact rerank
```

Add an assertion in the existing native candidate searcher tests that a stale/deleted SQLite row returned by an ANN ordinal is removed before final results.

- [ ] **Step 4: Run tagged package gates**

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -race -tags usearch \
    ./internal/semanticindex \
    ./internal/semanticbuild \
    ./internal/brainresearch
```

Expected: pass under the race detector.

- [ ] **Step 5: Commit**

```bash
git add internal/brainresearch/runtime_usearch_integration_test.go \
  internal/semanticindex/usearch_adapter_test.go
git commit -m "test(semantic): exercise tagged runtime root"
```

---

## Task 6: Document, verify, and smoke-test the representative corpus

**Files:**

- Modify: `CHANGELOG.md`
- Modify: `docs/superpowers/specs/2026-07-27-semantic-ann-automatic-sync-design.md`
- Create: `docs/superpowers/reports/2026-07-27-semantic-ann-runtime-admission.md`

**Interfaces:**

- Consumes: the completed code and test artifacts from Tasks 1-5 plus the existing representative corpus copy and ANN cache.
- Produces: user-visible changelog/design status, an evidence-backed runtime admission report, and the clean verification state used to open the first stacked PR.

- [ ] **Step 1: Update user-visible documentation**

Add a short current-date changelog entry stating:

- `semantic status` now reports explicit native backend capability;
- normal tagged runtime can admit a fully proven segmented USearch generation;
- unsupported builds skip ANN and preserve lexical retrieval;
- this is runtime admission only; automatic post-sync maintenance follows in later stacked PRs.

Update the design's implementation-status section to mark only stacked PR 1 complete. Do not mark the automatic-sync end goal complete.

- [ ] **Step 2: Run the standard untagged gates**

From the worktree:

```bash
task fmt
task lint
task test-ci
task build
```

Expected:

- formatting clean;
- no lint failures;
- `go test -cover -race ./...` passes under the clean CI-like environment;
- normal `bin/dbrain` builds without USearch.

Verify the untagged binary is capability-safe:

```bash
./bin/dbrain --no-debug semantic status --json
```

Expected JSON includes:

```json
"backend_capability":{"state":"unsupported"}
```

The command may report semantic off/not configured for the worktree data; capability reporting is the assertion.

- [ ] **Step 3: Run the full tagged test gate**

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -race -tags usearch -timeout=20m ./...
```

Expected: pass.

- [ ] **Step 4: Build a tagged development binary**

```bash
env \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  go build -tags usearch -buildvcs=true \
    -o ./bin/dbrain-usearch-dev ./cmd/dbrain
```

Confirm the binary is arm64 and dynamically linked only for this development gate:

```bash
file ./bin/dbrain-usearch-dev
otool -L ./bin/dbrain-usearch-dev
```

The later packaging PR replaces this development dylib dependency with the approved static, checksum-verified Homebrew build.

- [ ] **Step 5: Resolve the representative corpus boundary before using it**

Run the branch binary through the copied XDG roots:

```bash
env \
  XDG_CONFIG_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config" \
  XDG_DATA_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  ./bin/dbrain-usearch-dev --no-debug config paths --json
```

Assert the reported database is:

```text
/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain/brain.db
```

and the cache resolves beneath the copied XDG data root. Stop if it resolves to `~/.local/share/dbrain` or the repository `data/brain.db`.

- [ ] **Step 6: Prove status admits the real 290,535-vector generation**

Run:

```bash
env \
  XDG_CONFIG_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config" \
  XDG_DATA_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  ./bin/dbrain-usearch-dev --no-debug semantic status --json
```

Require:

- `status = ready`;
- `searchable = true`;
- `backend_capability.state = supported_ready`;
- `backend_capability.backend = usearch`;
- `backend_capability.version = 2.26.0`;
- active generation is non-empty and valid;
- active indexed count is `290000`;
- L0 ready count is `535`;
- tombstones are `0`;
- active snapshot/backend/version/metric/dimensions are populated.

- [ ] **Step 7: Run an actual normal CLI semantic query**

Use the normal command, not `corpus-eval`:

```bash
env \
  XDG_CONFIG_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config" \
  XDG_DATA_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  ./bin/dbrain-usearch-dev --no-debug research \
    "Apple silicon local model memory pressure" \
    --semantic \
    --retrieval-only \
    --no-planner \
    --no-trace \
    --json
```

Require:

- exit code zero;
- semantic status is searched/searchable;
- reported backend is `usearch`;
- a non-empty evidence pack is returned;
- no `native_backend_unavailable`, exact-only fallback, or silent lexical-only result is reported.

Record wall time and peak RSS with `/usr/bin/time -l`. This PR does not rerun the full 100-query recall benchmark, because PR #100 already established recall and resource gates on this exact generation. It does prove that the real normal CLI now reaches that generation.

- [ ] **Step 8: Prove unsupported behavior explicitly**

Run the untagged binary against the same copied corpus:

```bash
env \
  XDG_CONFIG_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config" \
  XDG_DATA_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data" \
  ./bin/dbrain --no-debug semantic status --json
```

Require:

- capability `unsupported`;
- semantic status `unavailable`;
- `searchable = false`;
- reason `native_backend_unsupported`;
- command exits zero;
- no native library load attempt;
- the copied database remains unchanged.

Then run a retrieval-only research command without `--semantic` and require lexical evidence still works.

- [ ] **Step 9: Write the verification report**

Create `docs/superpowers/reports/2026-07-27-semantic-ann-runtime-admission.md` containing:

- branch and commit SHA;
- exact database and cache boundary;
- untagged and tagged capability output;
- active generation metadata;
- focused, tagged, and standard gate commands/results;
- actual normal CLI semantic query backend and timing;
- unsupported-build behavior;
- unchanged representative DB checksum before/after;
- residual scope: no automatic refresh/sync integration yet.

Do not include secrets or the copied corpus.

- [ ] **Step 10: Final diff review and commit**

```bash
git status --short
git diff --check
git diff --stat 2d7ba53ce40524c427dd808345f74968b0be6be3...HEAD
git log --oneline --decorate 2d7ba53ce40524c427dd808345f74968b0be6be3..HEAD
```

Confirm:

- no production or copied DB/cache artifacts are tracked;
- no dylib/zip/binary is tracked;
- no sync behavior changed;
- no unsupported platform acquired a native dependency;
- every user-visible status has explicit capability.

Commit:

```bash
git add CHANGELOG.md \
  docs/superpowers/specs/2026-07-27-semantic-ann-automatic-sync-design.md \
  docs/superpowers/reports/2026-07-27-semantic-ann-runtime-admission.md
git commit -m "docs(semantic): record runtime admission verification"
```

---

## Completion Criteria

This first stacked PR is complete only when all of the following are true:

- a normal untagged build reports `unsupported` and never tries to load USearch;
- a tagged macOS arm64 build reports `supported_ready` only after its native probe passes;
- both readiness readers admit the same valid active-generation metadata and reject the same corrupt metadata;
- the SQLite readiness proof stays within the existing 250ms budget, while
  subsequent native loading remains caller-cancellable between stages;
- root opening proves SQLite watermark, root manifest, segment provenance, and payload integrity;
- normal `dbrain research --semantic --retrieval-only` uses backend `usearch` on the representative 290,535-vector corpus;
- an unsupported build against that same corpus reports semantic unavailable while lexical retrieval remains usable;
- `task fmt`, `task lint`, `task test-ci`, tagged race tests, and `task build` pass;
- `CHANGELOG.md` and the runtime verification report are current;
- the report explicitly states that synchronous automatic maintenance after `sync all` is still pending in later stacked PRs.

The next plan after this PR is accepted is stacked PR 2: durable, resumable semantic refresh orchestration with checkpointed projection, embedding, flush/compaction, verify, and progress reporting. Stacked PR 3 then wires that refresh synchronously into every sync and makes a non-`ready` result fail the sync command exactly as approved.
