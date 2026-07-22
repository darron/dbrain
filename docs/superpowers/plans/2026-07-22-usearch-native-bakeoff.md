# USearch Native Backend Bakeoff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Decide whether optional USearch can meet dbrain ANN recall and reopen gates without changing SQLite-authoritative runtime behavior.

**Architecture:** Retain the content-free synthetic corpus and exact oracle, but make its index factory injectable. Add a usearch-and-cgo adapter that uses segment-local ordinals, f32 cosine vectors, in-memory save/load payloads, and explicit Close calls. A tag-gated devtool can run only when development supplies the pinned temporary header and dylib. Normal dbrain builds never compile or require it.

**Tech Stack:** Go 1.26, github.com/unum-cloud/usearch/golang v2.26.0, cgo under the usearch build tag, and existing internal/annbakeoff exact-oracle logic.

## Global Constraints

- SQLite stays authoritative; this work never opens brain.db, reads restored vectors, calls an embedding provider, or writes corpus state.
- The default build and CGO_ENABLED=0 go test ./... work without USearch.
- Native code compiles only with -tags usearch and explicit CGO flags; no temporary path is committed.
- USearch sees only dense uint64 ordinals and f32 vectors. Its payload is opaque and temporary; no dbrain segment, manifest, L0 state, or semantic serving path is created.
- Acceptance requires recall@20 >= 0.95, matching save/load reopen recall, bounded resource use, cancellation, and errors. Passing does not enable retrieval or decide release packaging.

---

### Task 1: Tag-Gated Adapter

**Files:**

- Modify: go.mod and go.sum
- Create: internal/semanticindex/usearch_adapter.go
- Create: internal/semanticindex/usearch_adapter_test.go

**Interfaces:**

- NewUSearch(USearchOptions) (*USearch, error) exists only with usearch and cgo.
- USearchOptions: Dimensions, Connectivity, ExpansionAdd, ExpansionSearch.
- USearch implements Reserve(int) error, Add(...HNSWNode), Search([]float32, int), Export(io.Writer), Import(io.Reader), and Close().

- [ ] **Step 1: Write failing tag-gated tests.**

~~~go
func TestUSearchAdapterSearchAndReopen(t *testing.T) {
    index, err := NewUSearch(USearchOptions{Dimensions: 2, Connectivity: 16})
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = index.Close() })
    if err := index.Add(HNSWNode{Ordinal: 11, Vector: []float32{1, 0}}); err != nil { t.Fatal(err) }
    var payload bytes.Buffer
    if err := index.Export(&payload); err != nil { t.Fatal(err) }
    reopened, err := NewUSearch(USearchOptions{Dimensions: 2})
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = reopened.Close() })
    if err := reopened.Import(&payload); err != nil { t.Fatal(err) }
    assertUSearchOrdinals(t, reopened, []uint64{11})
}
~~~

Also cover wrong dimensions, negative options, malformed payload, and use after Close.

- [ ] **Step 2: Verify RED.**

~~~bash
CGO_ENABLED=1 CGO_CFLAGS=-I/private/tmp/dbrain-usearch.4zEMyv/extracted \
CGO_LDFLAGS=-L/private/tmp/dbrain-usearch.4zEMyv/extracted \
DYLD_LIBRARY_PATH=/private/tmp/dbrain-usearch.4zEMyv/extracted \
go test -tags usearch ./internal/semanticindex -run TestUSearchAdapter -count=1
~~~

Expected: package/build failure because the adapter does not exist.

- [ ] **Step 3: Implement the smallest adapter.**

Use F32/Cosine, Reserve before additions, connectivity and expansion options, SerializedLength plus SaveBuffer/LoadBuffer, and a copied byte payload. Reject nil/closed indexes and wrong dimensions; close exactly once.

~~~go
func (u *USearch) Export(w io.Writer) error {
    size, err := u.index.SerializedLength()
    if err != nil { return err }
    payload := make([]byte, size)
    if err := u.index.SaveBuffer(payload, size); err != nil { return err }
    _, err = w.Write(payload)
    return err
}
~~~

- [ ] **Step 4: Verify GREEN.**

Run the tagged package suite with the three variables above, then run:

~~~bash
CGO_ENABLED=0 go test ./internal/semanticindex -count=1
~~~

- [ ] **Step 5: Commit** the adapter, its dependency lock, and tests.

### Task 2: Candidate-Injectable Bakeoff

**Files:**

- Modify: internal/annbakeoff/run.go
- Modify: internal/annbakeoff/run_test.go

**Interfaces:**

- Index has Reserve, Add, Search, Export, Import, and Close methods matching the existing HNSW ordinal/vector contract. HNSW Reserve is a no-op; USearch reserves the current stage's exact capacity before additions.
- Factory is func(Options) (Index, error).
- RunWith(ctx, opts, backend, factory) returns the existing Report.
- Run remains a wrapper for HNSW.
- Report and StageReport add Parameters map[string]int, preserving old HNSW fields while recording native connectivity and expansion.

- [ ] **Step 1: Write failing tests.**

~~~go
func TestRunWithRecordsCandidateAndClosesIndexes(t *testing.T) {
    var closed int
    report, err := RunWith(context.Background(), testOptions(), "native-test", func(Options) (Index, error) {
        return &fakeIndex{onClose: func() { closed++ }}, nil
    })
    if err != nil { t.Fatal(err) }
    if report.Backend != "native-test" || closed != 2 {
        t.Fatalf("report=%+v closed=%d", report, closed)
    }
}
~~~

- [ ] **Step 2: Verify RED** with go test ./internal/annbakeoff -count=1.

- [ ] **Step 3: Refactor Run and runStage.**

Pass the factory through RunWith; call Reserve(size) before additions; defer Close on the built and reopened indexes; retain corpus generator, exact oracle, gate order, report schema, and HNSW behavior.

~~~go
func Run(ctx context.Context, opts Options) (Report, error) {
    return RunWith(ctx, opts, semanticindex.BackendHNSW, newHNSWFactory(opts))
}
~~~

- [ ] **Step 4: Verify GREEN.**

~~~bash
go test ./internal/annbakeoff ./cmd/devtools/semantic_ann_bakeoff -count=1
CGO_ENABLED=0 go test ./internal/annbakeoff ./cmd/devtools/semantic_ann_bakeoff -count=1
~~~

- [ ] **Step 5: Commit** the seam and tests.

### Task 3: Native Runner and Screen

**Files:**

- Create: internal/annbakeoff/usearch_runner.go
- Create: internal/annbakeoff/usearch_runner_test.go
- Create: cmd/devtools/semantic_usearch_bakeoff/main.go
- Create: cmd/devtools/semantic_usearch_bakeoff/main_test.go
- Modify: CHANGELOG.md

**Interfaces:**

- RunUSearch exists only with usearch and cgo and delegates to RunWith with backend usearch.
- The native command requires --report and accepts --connectivity, --expansion-add, and --expansion-search.

- [ ] **Step 1: Write failing tagged tests** for parameter reporting and missing --report.

~~~go
func TestRunUSearchRecordsParameters(t *testing.T) {
    report, err := RunUSearch(context.Background(), Options{
        Sizes: []int{32}, Dimensions: 8, QueryCount: 2,
        WarmRepetitions: 1, RecallAt: 5, MinimumRecall: 0,
    })
    if err != nil { t.Fatal(err) }
    if report.Backend != "usearch" || report.Parameters["connectivity"] == 0 {
        t.Fatalf("report=%+v", report)
    }
}
~~~

- [ ] **Step 2: Verify RED** with the tagged command/package suite and temporary library variables.

- [ ] **Step 3: Implement the runner and command.**

Do not expose them through dbrain semantic. Reject invalid parameters. Save a 0600 atomic JSON report before returning a non-zero screening rejection.

- [ ] **Step 4: Run the narrow 1,000-vector 768d screen.**

~~~bash
CGO_ENABLED=1 CGO_CFLAGS=-I/private/tmp/dbrain-usearch.4zEMyv/extracted \
CGO_LDFLAGS=-L/private/tmp/dbrain-usearch.4zEMyv/extracted \
DYLD_LIBRARY_PATH=/private/tmp/dbrain-usearch.4zEMyv/extracted \
go run -tags usearch ./cmd/devtools/semantic_usearch_bakeoff \
  --sizes 1000 --dimensions 768 --queries 10 --warm-repetitions 3 \
  --recall-at 20 --minimum-recall 0.95 --report /private/tmp/dbrain-usearch-screen.json
~~~

Stop on a recall, reopen, or resource failure. Only if it passes, run 25,000 then 100,000; run 286,619 only after both pass and record max RSS with /usr/bin/time -l.

- [ ] **Step 5: Update CHANGELOG.md** to state that USearch is test-only, optional, and does not enable semantic retrieval.

- [ ] **Step 6: Final verification.**

~~~bash
task fmt
task lint
task test-ci
task build
CGO_ENABLED=0 go build ./cmd/dbrain
CGO_ENABLED=1 CGO_CFLAGS=-I/private/tmp/dbrain-usearch.4zEMyv/extracted \
CGO_LDFLAGS=-L/private/tmp/dbrain-usearch.4zEMyv/extracted \
DYLD_LIBRARY_PATH=/private/tmp/dbrain-usearch.4zEMyv/extracted \
go test -tags usearch ./internal/semanticindex ./internal/annbakeoff ./cmd/devtools/semantic_usearch_bakeoff -count=1
git diff --check
~~~

- [ ] **Step 7: Commit** the screening-only runner, tests, evidence documentation, and changelog.

## Plan Self-Review

- The three tasks cover tag isolation, opaque payloads, exact/reopen recall, staged resource gates, and default lexical safety.
- Segment lifecycle, cache publication, semantic runtime integration, and release packaging are intentionally out of scope.
- The same ordinal/vector/hit contract is shared by HNSW and USearch through RunWith.
