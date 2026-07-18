# Hybrid Retrieval Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an opt-in local-first hybrid retrieval foundation with deterministic chunks, portable embeddings, exact vector search, RRF fusion, and shadow diagnostics while leaving lexical-only behavior unchanged by default.

**Architecture:** SQLite remains authoritative for chunks, vectors, and profile metadata. A local Ollama embedding provider generates portable dense float32 vectors; a bounded exact Go scanner is the correctness backend. `internal/brainresearch` receives semantic evidence through a narrow retriever interface, and `internal/researchhybrid` combines lexical and semantic ranks with RRF while exact tags remain a separate protected evidence floor.

**Tech Stack:** Go 1.26.1, `modernc.org/sqlite`, Cobra, Ollama `/api/embed`, existing dbrain research/eval/trace/MCP packages, no new native dependency, `CGO_ENABLED=0`.

## Global Constraints

- SQLite remains the only authoritative database; raw evidence is never overwritten.
- Release builds remain `CGO_ENABLED=0` across the existing release matrix.
- No helper binary, sidecar, native library, or runtime dynamic library.
- Only local Ollama embeddings are implemented here; hosted embeddings remain deferred.
- Semantic mode defaults to `off`.
- Lexical ordering and synthesis input remain identical when semantic is off, unavailable, disabled, or shadow-only.
- `exact_tag_evidence` remains a separately capped and budgeted pack array.
- `entity` and `graph_related` remain lexical signals, not RRF inputs.
- Purged parents disappear from chunks, embeddings, and exact search before purge success.
- Read-only MCP/status opens pre-retrieval-schema databases without writes.
- Recheck every branch/ref before assigning a migration number.
- Use test-first red-green-refactor for every behavior change.
- Commit with `--no-gpg-sign` because unattended 1Password signing is unavailable.

## File Structure

- `internal/retrievalchunk`: deterministic projection, chunking, Unicode offsets, identity.
- `internal/embedding`: provider-neutral profiles, vectors, validation, failures, Ollama adapter.
- `internal/embeddingtest`: strict race-safe fake provider.
- `internal/semanticindex`: exact vector search and backend-neutral hits.
- `internal/researchsemantic`: query embedding, search, hydration, evidence conversion.
- `internal/semanticconfig`: typed `off|shadow|on` resolution.
- `internal/semanticbuild`: explicit chunk/embed/status orchestration.
- `internal/store`: schema, persistence, selectors, projection paging, purge cascade.
- `internal/researchhybrid`: RRF and parent consolidation.
- `internal/brainresearch`: planner fix, retriever seam, shadow isolation.

---

### Task 1: Fix Concept Roles And Add Cerebras Regression Contracts

**Files:**
- Modify: `internal/brainresearch/strategy_concepts.go`
- Modify: `internal/brainresearch/strategy_variants.go`
- Modify: `internal/brainresearch/planner_sanitize.go`
- Modify: `internal/brainresearch/planner_merge.go`
- Modify: `internal/brainresearch/research_test.go`
- Modify: `internal/researcheval/types.go`
- Modify: `internal/researcheval/run.go`
- Modify: `internal/researcheval/run_test.go`
- Modify: `evals/README.md`
- Modify: `evals/local/research.json`

**Interfaces:**
- Produces `Case.ExpectRequiredConcepts` and `Case.ForbidRequiredConcepts`.
- Preserves four roles: `anchor`, `content`, `intent`, `frame`.

- [ ] **Step 1: Write the failing tests**

Add a deterministic test using the exact question and assertions:

```go
func TestCerebrasQuestionKeepsOnlyDiscriminativeRequiredConcepts(t *testing.T) {
    terms := queryterms.Terms("What can we learn from the Cerebras articles about their new knowledge base system and ontology, and apply to dbrain?")
    got := buildQueryConcepts(terms)

    byKey := map[string]QueryConcept{}
    var required []string
    for _, concept := range got {
        byKey[concept.Key] = concept
        if concept.Required {
            required = append(required, concept.Key)
        }
    }
    if !reflect.DeepEqual(required, []string{"cerebras", "ontology"}) {
        t.Fatalf("required concepts = %#v", required)
    }
    for key, role := range map[string]string{
        "learn": conceptRoleIntent, "apply": conceptRoleIntent,
        "articles": conceptRoleFrame, "new": conceptRoleFrame, "system": conceptRoleFrame,
    } {
        concept, ok := byKey[key]
        if !ok || concept.Role != role || concept.Required {
            t.Fatalf("concept %q = %#v", key, concept)
        }
    }
    for _, key := range []string{"knowledge", "base"} {
        if byKey[key].Required {
            t.Fatalf("concept %q remained required", key)
        }
    }
}
```

Add eval tests proving the two new expectation fields fail correctly.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/brainresearch ./internal/researcheval`

Expected: FAIL because all nine terms are currently required and the eval fields are absent.

- [ ] **Step 3: Implement the minimum policy**

Classify `learn` and `apply` as intent. Classify `articles`, `new`, and `system`
as context-sensitive frame terms when stronger content exists. Preserve
`knowledge base` as a searchable phrase without two required component terms.
Keep explicit discriminative content families required. Order merged concepts:

```go
func conceptPriority(c QueryConcept) int {
    switch {
    case c.Role == conceptRoleAnchor:
        return 0
    case c.Role == conceptRoleContent && c.Required:
        return 1
    case c.Role == conceptRoleContent:
        return 2
    case c.Role == conceptRoleIntent:
        return 3
    default:
        return 4
    }
}
```

Add:

```go
ExpectRequiredConcepts []string `json:"expect_required_concepts,omitempty"`
ForbidRequiredConcepts []string `json:"forbid_required_concepts,omitempty"`
```

Add planner-on and planner-off private Cerebras cases expecting
`src:458528e78013` first and forbidding the seven generic requirements.
Keep `evals/local/research.json` ignored and private; do not stage it.

- [ ] **Step 4: Verify GREEN and conjunctive controls**

Run: `go test ./internal/brainresearch ./internal/researcheval ./internal/queryterms`

Expected: PASS, including K8s/Helm, people/event, and short-name controls.

- [ ] **Step 5: Commit**

```bash
git add internal/brainresearch internal/researcheval evals/README.md
git commit --no-gpg-sign -m "fix: preserve discriminative research concepts"
```

---

### Task 2: Add Deterministic Projection And Chunking

**Files:**
- Create: `internal/retrievalchunk/types.go`
- Create: `internal/retrievalchunk/projection.go`
- Create: `internal/retrievalchunk/chunker.go`
- Create: `internal/retrievalchunk/identity.go`
- Create: `internal/retrievalchunk/chunker_test.go`
- Create: `internal/retrievalchunk/projection_test.go`

**Interfaces:** Produces `Parent`, `Section`, `Chunk`, `DefaultOptions`, `Build`, `ProjectItem`, and `ProjectSource` without store access.

- [ ] **Step 1: Write failing tests**

Cover short records, headings/paragraphs, 2,400-rune target, 3,600-rune hard
maximum, 300-rune maximum overlap, UTF-8 offsets, oversized paragraph fallback,
separate raw/OCR/transcript/derived roles, and stable IDs.

```go
chunks, err := Build(Parent{
    Kind: "source", SourceKey: "src:test", ContentHash: "input-v1",
    Sections: []Section{{Role: "raw", Heading: "Architecture", Text: longText}},
}, DefaultOptions())
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/retrievalchunk`

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement minimal deterministic types**

```go
const Version = "retrieval-chunker-v1"

type Section struct { Role, Heading, Text string; Derived bool }
type Parent struct {
    Kind, SourceKey, ContentHash, Title, SourceType, Author string
    Sections []Section
}
type Chunk struct {
    ID, ParentKind, ParentSourceKey, EvidenceRole string
    Ordinal, StartChar, EndChar int
    Heading, ChunkerVersion, InputContentHash, TextHash, Text string
}
```

Hash a length-prefixed canonical encoding with SHA-256. Calculate and test all
offsets in Unicode runes. Never merge raw and derived sections.

- [ ] **Step 4: Verify GREEN**

Run: `go test -race ./internal/retrievalchunk`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/retrievalchunk
git commit --no-gpg-sign -m "feat: add deterministic retrieval chunks"
```

---

### Task 3: Persist Retrieval State And Purge Safely

**Files:**
- Create: `internal/store/retrieval_schema.go`
- Create: `internal/store/retrieval_chunks.go`
- Create: `internal/store/retrieval_embeddings.go`
- Create: `internal/store/retrieval_index_generations.go`
- Create: `internal/store/retrieval_projection.go`
- Create: `internal/store/retrieval_store_test.go`
- Modify: `internal/store/schema.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Modify: `internal/store/open.go`
- Modify: `internal/store/schema_init.go`
- Modify: `internal/store/item_purge.go`
- Modify: `internal/applenotes/run_test.go`

**Interfaces:** Produces retrieval availability, parent paging, chunk replacement, embedding selectors/writes, status, and generation activation.

- [ ] **Step 1: Recheck migration history**

Run: `git log --all -G 'Version: 1[3-9]|currentSchemaVersion *= *1[3-9]' -- internal/store/migrations.go`

Expected at this snapshot: no migration 13+. If changed, use the next free number.

- [ ] **Step 2: Write failing store tests**

Required tests:

```go
func TestReplaceRetrievalChunksReusesUnchangedEmbeddings(t *testing.T)
func TestReplaceRetrievalChunksRollsBackWholeParent(t *testing.T)
func TestRetrievalProfilesCoexist(t *testing.T)
func TestOnlyCompletedGenerationCanActivate(t *testing.T)
func TestOnlyOneGenerationIsActivePerProfile(t *testing.T)
func TestOpenReadOnlyPreRetrievalSchemaDoesNotWrite(t *testing.T)
func TestPurgeItemIndexedContentDeletesRetrievalState(t *testing.T)
func TestMigrationRepairsPartialRetrievalSchema(t *testing.T)
```

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/store ./internal/applenotes`

Expected: FAIL for missing schema and APIs.

- [ ] **Step 4: Implement schema and migration**

Create `retrieval_chunks`, `retrieval_embeddings`, and
`retrieval_index_generations`. Embeddings cascade from chunks. Generations
record dimensions and distance metric and enforce one active row per profile.
Call an idempotent `ensureRetrievalTables` from fresh schema and the next
migration, tentatively:

```go
{Version: 13, Name: "retrieval_hybrid_storage_v1", Run: func(s *Store) error {
    return s.ensureRetrievalTables()
}},
```

Do not make retrieval tables an unconditional pre-migration core-schema check.

- [ ] **Step 5: Implement transactional APIs and purge cascade**

```go
func (s *Store) ReplaceRetrievalChunks(context.Context, string, string, []retrievalchunk.Chunk) (ChunkReplaceResult, error)
func (s *Store) ListRetrievalParents(context.Context, string, int) ([]retrievalchunk.Parent, error)
func (s *Store) ListChunksNeedingEmbedding(context.Context, string, string, int) ([]RetrievalChunkRow, error)
func (s *Store) PutRetrievalEmbedding(context.Context, RetrievalEmbeddingRow) error
func (s *Store) ListReadyEmbeddings(context.Context, string, int) ([]RetrievalEmbeddingRow, error)
func (s *Store) RetrievalStatus(context.Context, string) (RetrievalStatus, error)
```

Diff replacements by stable chunk ID. Capture affected profiles inside
`PurgeItemIndexedContent`, delete parent chunks/embeddings, and mark their
generations stale/inactive in the same transaction. The later ANN plan adds
file deletion; this plan creates no ANN file.

- [ ] **Step 6: Verify GREEN**

Run: `go test ./internal/store ./internal/applenotes`

Expected: PASS with raw evidence and pre-v13 read-only opening unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/store internal/applenotes/run_test.go
git commit --no-gpg-sign -m "feat: persist retrieval chunks and embeddings"
```

---

### Task 4: Add Portable Embedding Contracts And Fake Provider

**Files:**
- Create: `internal/embedding/provider.go`
- Create: `internal/embedding/profile.go`
- Create: `internal/embedding/errors.go`
- Create: `internal/embedding/vector.go`
- Create: `internal/embedding/validate.go`
- Create: `internal/embedding/provider_test.go`
- Create: `internal/embedding/profile_test.go`
- Create: `internal/embedding/vector_test.go`
- Create: `internal/embedding/validate_test.go`
- Create: `internal/embeddingtest/fake.go`
- Create: `internal/embeddingtest/fake_test.go`

**Interfaces:** Provider-neutral request/response/profile API with no store or research dependency.

- [ ] **Step 1: Write failing tests**

Cover order/cardinality, positive fixed dimensions, NaN/Inf, zero L2 vectors,
little-endian float32 round-trip, corrupt byte length, stable profile ID, strict
fake mapping, deep copies, and concurrent call recording.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/embedding ./internal/embeddingtest`

Expected: FAIL because packages are absent.

- [ ] **Step 3: Implement minimal contracts**

```go
type Purpose string
const ( PurposeDocument Purpose = "document"; PurposeQuery Purpose = "query" )
type Request struct { Texts []string; Purpose Purpose }
type Response struct { Vectors [][]float32; Provider, Model string; Dimensions int }
type Provider interface { Info() Info; Embed(context.Context, Request) (Response, error) }
```

Profiles include provider, exact model, projection versions, chunker version,
representation, normalization, and dimensions. Typed failures are retryable,
blocked, or fatal configuration. The fake rejects unmapped text by default.

- [ ] **Step 4: Verify GREEN**

Run: `go test -race ./internal/embedding ./internal/embeddingtest`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/embedding internal/embeddingtest
git commit --no-gpg-sign -m "feat: add portable embedding contracts"
```

---

### Task 5: Add Local Ollama Embeddings And Typed Configuration

**Files:**
- Create: `internal/semanticconfig/config.go`
- Create: `internal/semanticconfig/config_test.go`
- Create: `internal/embedding/ollama.go`
- Create: `internal/embedding/ollama_test.go`
- Modify: `internal/app/env_docs.go`
- Modify: `internal/app/app_test.go`
- Modify: `config.yaml.sample`
- Modify: `internal/install/templates/config.yaml.sample`
- Modify: `internal/install/install_test.go`

**Interfaces:** Produces `off|shadow|on` config and a local `/api/embed` provider. Hosted providers are rejected.

- [ ] **Step 1: Write failing config and HTTP tests**

Test precedence, invalid mode, default off, default provider Ollama, missing
model as not configured, hosted rejection, ordered batch input, `truncate:false`,
redirect/proxy disabling, cardinality, and typed HTTP/context failures.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/semanticconfig ./internal/embedding ./internal/install ./internal/app`

Expected: FAIL for absent resolver/adapter/docs.

- [ ] **Step 3: Implement configuration and adapter**

```yaml
research:
  semantic:
    mode: off
    provider: ollama
    model: ""
    index_backend: exact
    candidate_depth: 50
    exact_fallback_max_chunks: 25000
```

Document matching `DBRAIN_RESEARCH_SEMANTIC_*` keys in `config env` and both
sample files. Use a dedicated HTTP client; do not route through Chat.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/semanticconfig ./internal/embedding ./internal/install ./internal/app`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/semanticconfig internal/embedding internal/app config.yaml.sample internal/install
git commit --no-gpg-sign -m "feat: configure local semantic embeddings"
```

---

### Task 6: Add Explicit Chunk, Embed, And Status Commands

**Files:**
- Create: `internal/semanticbuild/chunk.go`
- Create: `internal/semanticbuild/embed.go`
- Create: `internal/semanticbuild/status.go`
- Create: `internal/semanticbuild/run_test.go`
- Create: `internal/app/semantic.go`
- Create: `internal/app/semantic_output.go`
- Modify: `internal/app/root.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/app/stats_read_only_test.go`

**Interfaces:** Produces `semantic status`, `semantic chunk`, and `semantic embed`; research never backfills implicitly.

- [ ] **Step 1: Write failing orchestration/CLI tests**

Prove page rows close before writes, unchanged/current counts, selector/status
predicate parity, blocked versus failed states, cancellation, read-only status
on pre-v13 DB, non-null JSON arrays, and explicit progress counters.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/semanticbuild ./internal/app`

Expected: FAIL because commands are absent.

- [ ] **Step 3: Implement bounded operations**

```go
type Progress struct {
    Stage string
    Scanned, Current, Generated, Blocked, Failed, Remaining int
}
```

Commands:

```text
dbrain semantic status [--json]
dbrain semantic chunk [--limit N] [--json]
dbrain semantic embed [--limit N] [--batch-size N] [--json]
```

Status uses the audit/no-write config resolver and `OpenReadOnly`; mutations
use normal writable loading.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/semanticbuild ./internal/app`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/semanticbuild internal/app
git commit --no-gpg-sign -m "feat: add semantic indexing commands"
```

---

### Task 7: Add Exact Vector Search And Semantic Hydration

**Files:**
- Create: `internal/semanticindex/index.go`
- Create: `internal/semanticindex/exact.go`
- Create: `internal/semanticindex/exact_test.go`
- Create: `internal/researchsemantic/retriever.go`
- Create: `internal/researchsemantic/retriever_test.go`
- Modify: `internal/store/retrieval_embeddings.go`
- Modify: `internal/store/retrieval_chunks.go`

**Interfaces:** Produces exact `Searcher` and a retriever returning chunk-level `retrieval.EvidenceDocument` rows.

- [ ] **Step 1: Write failing tests**

Cover cosine distance, deterministic ties, limits, dimension/profile mismatch,
bounded fallback, pre-top-k source filters, deleted/purged hydration, evidence
roles, and unavailable versus searched-empty states.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/semanticindex ./internal/researchsemantic`

Expected: FAIL because packages are absent.

- [ ] **Step 3: Implement exact search and hydration**

```go
type Hit struct { ChunkID string; Rank int; Distance float64 }
type Searcher interface {
    Search(context.Context, []float32, SearchOptions) ([]Hit, Status, error)
}
```

Scan ready embeddings deterministically with a bounded max heap and refuse
automatic exact fallback above 25,000 chunks. Embed the cleaned query with
`PurposeQuery`, hydrate IDs through current SQLite, drop missing/purged rows,
and return parent-cited chunk evidence with semantic provenance.

- [ ] **Step 4: Verify GREEN**

Run: `go test -race ./internal/semanticindex ./internal/researchsemantic`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/semanticindex internal/researchsemantic internal/store/retrieval_embeddings.go internal/store/retrieval_chunks.go
git commit --no-gpg-sign -m "feat: add exact semantic retrieval"
```

---

### Task 8: Add RRF, Consolidation, And Semantic-Safe Inspection

**Files:**
- Modify: `internal/retrieval/types.go`
- Modify: `internal/researchhybrid/hybrid.go`
- Modify: `internal/researchhybrid/hybrid_test.go`
- Create: `internal/researchhybrid/consolidation.go`
- Create: `internal/researchhybrid/consolidation_test.go`
- Modify: `internal/brainresearch/types.go`
- Modify: `internal/brainresearch/strategy_evidence.go`
- Modify: `internal/brainresearch/inspection.go`
- Modify: `internal/brainresearch/inspection_test.go`
- Modify: `internal/ask/inspect.go`

**Interfaces:** Produces `Fuse` with k=60, weights 1.0, depths 50/50, final 20; injects a semantic retriever into `brainresearch.Builder`.

- [ ] **Step 1: Write failing fusion/inspection tests**

Cover exact RRF values, ties, lane rank/distance/contribution/fused score,
lexical-only identity, unavailable identity, duplicate contributions, protected
anchors, chunk dedupe, max three unanchored chunks per parent, adjacent merge,
exact-tag separation, and semantic chunk survival through inspection.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/researchhybrid ./internal/brainresearch ./internal/ask`

Expected: FAIL because live retrieval never calls the current append-only merge.

- [ ] **Step 3: Implement typed provenance and fusion**

```go
type RetrievalLaneScore struct {
    Name string `json:"name"`
    Rank int `json:"rank"`
    RawScore, Distance, Contribution float64
}
```

Add `FusedScore` and lane scores to `RetrievalInfo`. Return the original lexical
slice when no semantic rows are usable. Otherwise compute `1/(60+rank)` per
lexical/semantic lane, protect exact anchors after fusion, consolidate parent
chunks, and preserve semantic chunk identity through inspection.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/researchhybrid ./internal/brainresearch ./internal/ask ./internal/researchrun`

Expected: PASS with semantic-off outputs unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/retrieval internal/researchhybrid internal/brainresearch internal/ask
git commit --no-gpg-sign -m "feat: fuse lexical and semantic retrieval"
```

---

### Task 9: Propagate Off, Shadow, And On Through Every Research Surface

**Files:**
- Modify: `internal/brainresearch/types.go`
- Modify: `internal/brainresearch/research.go`
- Modify: `internal/researchrun/types.go`
- Modify: `internal/researchrun/run.go`
- Modify: `internal/app/research.go`
- Modify: `internal/mcpserver/research.go`
- Modify: `internal/mcpserver/tool_schemas_core.go`
- Modify: `internal/mcpserver/tool_schemas_research.go`
- Modify: `internal/mcpserver/server_test.go`
- Modify: `internal/researcheval/types.go`
- Modify: `internal/researcheval/run.go`
- Modify: `internal/researcheval/trace.go`
- Modify: `internal/researchtrace/markdown.go`
- Modify: `internal/researchtrace/trace_test.go`
- Modify: `web/api_types.go`
- Modify: `web/research_handlers.go`
- Modify: `web/research_run_handlers.go`
- Modify: `web/research_trace_handlers.go`

**Interfaces:** Produces typed effective mode and bounded `ShadowComparison` diagnostics while retaining boolean transport overrides.

- [ ] **Step 1: Write failing mode/schema tests**

Cover configured modes, CLI overrides/conflict, MCP conflict, exact shadow
evidence/synthesis equality, bounded comparison metadata, trace replay, MCP
chunk/fusion schemas, non-null arrays, and MCP shadow creating no trace files.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/brainresearch ./internal/researchrun ./internal/app ./internal/mcpserver ./internal/researcheval ./internal/researchtrace ./web`

Expected: FAIL because booleans cannot represent shadow and schemas omit fields.

- [ ] **Step 3: Implement mode precedence and shadow isolation**

```go
func EffectiveMode(configured Mode, forceOn, forceOff bool) (Mode, error) {
    if forceOn && forceOff { return "", ErrConflictingOverrides }
    if forceOn { return ModeOn, nil }
    if forceOff { return ModeOff, nil }
    return configured, nil
}
```

Keep lexical returned evidence and shadow hybrid evidence in distinct variables
and types. Shadow metadata contains only bounded keys/ranks/counts/errors. It
never enters evidence, inspection, judge/retry, or synthesis. MCP returns the
metadata without writing `research-runs`.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/brainresearch ./internal/researchrun ./internal/app ./internal/mcpserver ./internal/researcheval ./internal/researchtrace ./web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/brainresearch internal/researchrun internal/app internal/mcpserver internal/researcheval internal/researchtrace web
git commit --no-gpg-sign -m "feat: add semantic shadow retrieval mode"
```

---

### Task 10: Document, Evaluate, Verify, And Review

**Files:**
- Modify: `.gitignore`
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `COMMANDS.md`
- Modify: `MCP.md`
- Modify: `docs/research-harness.md`
- Modify: `evals/README.md`
- Modify: `skills/dbrain-mcp/SKILL.md`
- Modify: `internal/mcpserver/resource_definitions.go`

- [ ] **Step 1: Update user-facing documentation**

Document commands, config/env, local Ollama-only support, off/shadow/on,
overrides, exact fallback limit, purge semantics, MCP read-only/trace-free
behavior, separate exact-tag evidence, and deferred ANN/default-on. Add `/cache/`
to `.gitignore` before repo-local cache generation.

- [ ] **Step 2: Build and verify the dev boundary**

Run:

```bash
task build
direnv exec . ./bin/dbrain --no-debug config paths --json
direnv exec . ./bin/dbrain --no-debug eval research --file evals/local/research.json
```

Expected: paths point to repo/dev state, Cerebras assertions pass, lexical cases stay green.

- [ ] **Step 3: Run full gates**

```bash
task fmt
task lint
task test-ci
task build
```

Expected: all exit 0. Rerun after any fix.

- [ ] **Step 4: Refresh the installed MCP skill**

Use the repository-supported skill update path, then run:

```bash
diff -u skills/dbrain-mcp/SKILL.md /Users/darron/.codex/skills/dbrain-mcp/SKILL.md
```

Expected: no diff.

- [ ] **Step 5: Commit**

```bash
git add .gitignore CHANGELOG.md README.md COMMANDS.md MCP.md docs/research-harness.md evals/README.md skills/dbrain-mcp/SKILL.md internal/mcpserver/resource_definitions.go
git commit --no-gpg-sign -m "docs: ship hybrid retrieval foundation"
```

- [ ] **Step 6: Request whole-branch review and fix findings**

Review against this plan and
`docs/superpowers/specs/2026-07-18-retrieval-first-hybrid-search-design.md`.
Fix every Critical/Important finding, then rerun focused tests and all gates.

---

## Deferred Second Plan: Scalable ANN Lifecycle

After this foundation is green and reviewed, create a separate plan for a
pure-Go HNSW bakeoff, license review, same-parent staging/fsync/promotion,
cross-process builder and reader lifetime, exact-versus-ANN recall@20 >= 0.95,
restore fencing, synchronous purge of immutable generation files, corruption
tests, production-sized benchmarks, and explicit index/verify commands. It may
not change SQLite authority, the exact oracle, privacy, modes, or `CGO_ENABLED=0`.
