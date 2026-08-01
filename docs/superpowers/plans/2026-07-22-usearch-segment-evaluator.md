# USearch Segment Builder Evaluation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (\`- [ ]\`) syntax for tracking.

**Goal:** Make the screened native USearch candidate able to build one opaque 5,000-vector segment through the existing lifecycle, using an explicit restored database and cache only when an operator passes \`--apply\`.

**Architecture:** A \`usearch && cgo\` builder decodes already-verified SQLite f32 vectors, inserts dense segment-local ordinals, exports one opaque native payload, and closes its index before returning the payload writer to \`semanticbuild.Flush\`. A tag-gated devtool creates the immutable segment/root and SQLite activation only after refusing the configured production database and requiring explicit \`--apply\`; it never changes semantic serving configuration.

**Tech Stack:** Go 1.26, existing \`semanticbuild.Flush\`, \`semanticindex.USearch\`, SQLite store, and the temporary development native library only under \`usearch && cgo\`.

## Execution Record (2026-07-22)

Implemented on \`codex/semantic-ann-lifecycle\` with a tag-gated builder and
operator-only evaluator. The evaluator’s guard was exercised against
\`/Users/darron/src/dbrain/data/brain.db\`: it rejected that configured production
path before opening it. The restored profile currently has 1,993 ready 768d
vectors and its schema predates the retrieval-foundation tables, so no physical
segment flush was attempted or authorized.

## Global Constraints

- Default \`CGO_ENABLED=0\` builds, tests, and the dbrain binary never compile or require USearch.
- The normal flush minimum remains exactly 5,000 ready revisions; a smaller restored profile is reported and not partially indexed.
- The devtool must require explicit \`--db\`, \`--cache\`, and \`--apply\`, and reject the configured XDG production database.
- No Cobra command, semantic configuration switch, query serving path, embedding generation, compaction, purge, or release packaging is added.
- The builder uses the passed profile dimensions and the screened USearch settings: connectivity 16, expansion-add 128, expansion-search 256.

---

### Task 1: Tag-gated payload builder

**Files:**

- Create: \`internal/semanticbuild/usearch_builder.go\`
- Create: \`internal/semanticbuild/usearch_builder_test.go\`

**Interfaces:**

\`\`\`go
type USearchSegmentBuilderOptions struct {
    Dimensions, Connectivity, ExpansionAdd, ExpansionSearch int
}
func NewUSearchSegmentBuilder(USearchSegmentBuilderOptions) (*USearchSegmentBuilder, error)
func (b *USearchSegmentBuilder) Build(context.Context, []store.RetrievalEmbeddingRow) (func(io.Writer) error, error)
\`\`\`

- [ ] **Step 1: Write failing tagged tests.**

\`\`\`go
func TestUSearchSegmentBuilderExportsReopenablePayload(t *testing.T) {
    builder, _ := NewUSearchSegmentBuilder(USearchSegmentBuilderOptions{Dimensions: 2})
    payload, err := builder.Build(context.Background(), []store.RetrievalEmbeddingRow{
        {VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2},
        {VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), Dimensions: 2},
    })
    if err != nil { t.Fatal(err) }
    var encoded bytes.Buffer
    if err := payload(&encoded); err != nil { t.Fatal(err) }
    reopened, _ := semanticindex.NewUSearch(semanticindex.USearchOptions{Dimensions: 2})
    defer reopened.Close()
    if err := reopened.Import(&encoded); err != nil { t.Fatal(err) }
}
\`\`\`

Also cover empty rows, wrong dimensions, malformed vector bytes, cancellation before insertion, and short output writers.

- [ ] **Step 2: Run RED.**

\`\`\`bash
CGO_ENABLED=1 CGO_CFLAGS=-I/private/tmp/dbrain-usearch.4zEMyv/extracted \\
CGO_LDFLAGS=-L/private/tmp/dbrain-usearch.4zEMyv/extracted \\
DYLD_LIBRARY_PATH=/private/tmp/dbrain-usearch.4zEMyv/extracted \\
go test -tags usearch ./internal/semanticbuild -run TestUSearchSegmentBuilder -count=1
\`\`\`

Expected: compile failure because the builder does not exist.

- [ ] **Step 3: Implement the smallest builder.**

Use F32 cosine USearch with defaults 16/128/256. Decode each row with \`embedding.DecodeDenseF32\`, check cancellation before each addition, add its slice ordinal, export to a copied byte slice, and close before returning a closure that writes exactly that payload.

- [ ] **Step 4: Run GREEN.**

Run the tagged test above and \`CGO_ENABLED=0 go test ./internal/semanticbuild -count=1\`.

- [ ] **Step 5: Commit.**

\`\`\`bash
git add internal/semanticbuild/usearch_builder.go internal/semanticbuild/usearch_builder_test.go
git commit --no-gpg-sign -m "feat: add tag-gated USearch segment builder"
\`\`\`

### Task 2: Explicit restored-corpus lifecycle evaluator

**Files:**

- Create: \`cmd/devtools/semantic_usearch_segment_flush/main.go\`
- Create: \`cmd/devtools/semantic_usearch_segment_flush/main_test.go\`
- Modify: \`CHANGELOG.md\`
- Modify: \`docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md\`

**Interfaces:**

\`\`\`bash
go run -tags usearch ./cmd/devtools/semantic_usearch_segment_flush \\
  --db /absolute/restored/brain.db --cache /absolute/cache --provider ollama \\
  --model MODEL --dimensions 768 --apply --report /absolute/report.json
\`\`\`

- [ ] **Step 1: Write failing tagged command tests.**

\`\`\`go
func TestRunRequiresExplicitApply(t *testing.T) {
    err := run(context.Background(), []string{"--db", "/tmp/restored.db", "--cache", "/tmp/cache"})
    if err == nil || !strings.Contains(err.Error(), "--apply") { t.Fatalf("err=%v", err) }
}
func TestRunRefusesConfiguredProductionDatabase(t *testing.T) { /* exact configured DB -> error before store.Open */ }
\`\`\`

- [ ] **Step 2: Run RED** with the tagged command package test command from Task 1, substituting the command path.

- [ ] **Step 3: Implement the operator boundary.**

Require all explicit paths and profile settings. Resolve and compare the candidate DB path to the active configured production DB; refuse a match. Open only after \`--apply\`; invoke \`semanticbuild.Flush\` with the USearch builder and configured parameters; write a 0600 atomic JSON report for both preflight and completed states. If ready vectors are below 5,000, report the non-mutating threshold failure and leave database/cache unchanged.

- [ ] **Step 4: Run GREEN.**

Run the tagged command tests and a controlled synthetic 5,000-row temporary database/cache integration test. Then confirm default build isolation:

\`\`\`bash
CGO_ENABLED=0 go build ./cmd/dbrain
CGO_ENABLED=0 go test ./internal/semanticbuild -count=1
\`\`\`

- [ ] **Step 5: Commit.**

\`\`\`bash
git add cmd/devtools/semantic_usearch_segment_flush CHANGELOG.md docs/superpowers/specs/2026-07-19-production-corpus-semantic-retrieval-design.md
git commit --no-gpg-sign -m "feat: add explicit native segment lifecycle evaluator"
\`\`\`

## Self-Review

- The plan is limited to building and evaluating a native payload through the existing lifecycle. Serving, configuration, and packaging remain untouched.
- The devtool cannot mutate the configured production database without bypassing its explicit path comparison, and it cannot mutate any database without \`--apply\`.
- The builder owns only ephemeral native state and opaque bytes; SQLite and dbrain-owned manifests remain authoritative.
