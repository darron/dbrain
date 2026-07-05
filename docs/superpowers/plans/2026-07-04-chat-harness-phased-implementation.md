# Chat Harness Entity Synthesis Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make web Chat/research runner synthesis over named entities, handles, source keys, tags, or collections preserve and cite the local evidence the user asked for instead of replacing it with generic intent-term matches.

**Architecture:** Add typed protected anchors and concept roles in `internal/brainresearch`, expose them on `Pack.QueryPlan`, make `internal/researchrun` judge only missing anchor/content concepts, and change retries to merge into the initial pack through a `brainresearch` helper that recomputes coverage. Then harden web Chat's raw-question boundary, trace attempt artifacts, runner eval coverage, docs, and changelog.

**Tech Stack:** Go packages `internal/brainresearch`, `internal/researchrun`, `internal/researchtrace`, `internal/researcheval`, Svelte/JS helpers under `web/ui/src/lib`, existing `task` gates, and OpenCode direct GLM review (`opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high`).

---

## Scope And Non-Negotiables

- Do not hardcode `Kristof_Poland`; use generic handle, underscore alias, source-key, tag, and entity/author anchor handling.
- Keep the runner bounded to one retry.
- Preserve raw imported evidence; model answers remain derived synthesis, not future evidence.
- Do not use prior model prose or arbitrary prior evidence titles as source-of-truth anchors.
- Each phase must have a local red/green test target before OpenCode review.
- After each phase, run OpenCode/GLM on the real checkout. Count the review complete only if it returns findings or an explicit no-findings recommendation.
- Phase 4 intentionally precedes Phase 5 even though the design lists trace artifacts first. Chat continuity is the user-visible retrieval boundary, has no hard dependency on attempt artifacts, and should be testable before trace artifact preservation. Do not deploy between phases; the product fix is not complete until Phase 6 and final gates pass.

## Phase 0: Baseline And Plan Review

**Files:**
- Read: `docs/superpowers/plans/2026-07-04-chat-harness-entity-synthesis-retrieval.md`
- Create: `docs/superpowers/plans/2026-07-04-chat-harness-phased-implementation.md`

- [x] **Step 0.1: Record dirty worktree boundary**

Run:

```sh
git status --short --untracked-files=all
```

Expected: existing untracked plan/review documents are visible; no unrelated user changes are modified.

- [x] **Step 0.2: Run baseline fast tests**

Run:

```sh
go test ./internal/brainresearch ./internal/researchrun ./internal/researchtrace ./internal/researcheval
```

Expected: pass before behavior changes. If baseline fails, record the exact failure and only continue if it is unrelated to the chat harness changes.

- [x] **Step 0.3: Review this implementation plan with OpenCode/GLM**

Run from `/Users/darron/src/dbrain`:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review the phased implementation plan before coding. Do not edit files. Inspect docs/superpowers/plans/2026-07-04-chat-harness-entity-synthesis-retrieval.md and docs/superpowers/plans/2026-07-04-chat-harness-phased-implementation.md. Check whether each phase is independently buildable/testable, whether the order matches the accepted design, and whether any phase leaves the destructive retry or intent-term bug unfixed. Return findings first with severity and an explicit ready/not-ready recommendation."
```

Expected: ready or actionable findings. Apply accepted plan feedback before Phase 1.

## Phase 1: Protected Anchors And Concept Roles

**Files:**
- Modify: `internal/brainresearch/types.go`
- Create: `internal/brainresearch/anchors.go`
- Create: `internal/brainresearch/anchor_resolver.go`
- Create: `internal/brainresearch/anchors_test.go`
- Modify: `internal/brainresearch/strategy.go`
- Modify: `internal/brainresearch/strategy_concepts.go`
- Modify: `internal/brainresearch/strategy_variants.go`
- Modify: `internal/brainresearch/planner_merge.go`
- Modify: `internal/brainresearch/research.go`
- Modify: `internal/brainresearch/research_test.go`

- [x] **Step 1.1: Write failing anchor extraction tests**

Add `internal/brainresearch/anchors_test.go` with tests named:

```go
func TestExtractProtectedAnchorsPreservesHandlesAndUnderscoreAliases(t *testing.T)
func TestExtractProtectedAnchorsRejectsEmailAndCodeIdentifiers(t *testing.T)
func TestSourceKeyCandidatesRemainProtectedAnchors(t *testing.T)
func TestSourceKeyCandidatesRejectMarkdownURLsCodeBlocksAndLookalikes(t *testing.T)
func TestExtractProtectedAnchorsFromHashtagDisplayNameAndCollection(t *testing.T)
```

Assertions:

- `@Kristof_Poland` creates one anchor with `Kind: "handle"`, `Relation: "authored_by"`, `Canonical: "kristof_poland"`, `ExactTerms` containing `@Kristof_Poland`, `Kristof_Poland`, and `kristof_poland`, `PhraseTerms` containing `kristof poland`, and `ExpansionTerms` containing `kristof`, `poland`.
- `Kristof_Poland` in a synthesis/author query creates one `Kind: "entity_alias"` or `Kind: "handle"` anchor with `Canonical: "kristof_poland"`.
- `bob@example.com`, `user_id`, `created_at`, `max_retries`, and `snake_case` inside code-like snippets do not create anchors.
- `x:2071948517837353292` and `src:1212afd25440` create `Kind: "source_key"` anchors whose `ExactTerms` include the exact source key.
- Punctuation-adjacent source keys such as `x:2071948517837353292)`, bracketed Markdown references, and code-spanned keys are parsed without trailing punctuation; unrelated URL substrings, `0xdeadbeef`, and `20260704T174350` do not become source-key anchors.
- `#Kristof_Poland` and `#kristof-poland` resolve to tag anchors only when the resolver confirms an exact tag/entity/collection alias. Display-name phrases such as `Synthesize Kristof Poland's tweets`, `posts by Vitalik Buterin`, `from @X`, `about @X`, and `Synthesize the Tyler Cowen collection` are covered as resolved-anchor fixtures; unresolved phrases stay content terms.

- [x] **Step 1.2: Verify anchor tests fail**

Run:

```sh
go test ./internal/brainresearch -run 'TestExtractProtectedAnchors|TestSourceKeyCandidatesRemainProtectedAnchors'
```

Expected: fail because `ProtectedAnchor` and `extractProtectedAnchors` do not exist yet.

- [x] **Step 1.3: Add anchor and role types**

In `internal/brainresearch/types.go` add:

```go
type ProtectedAnchor struct {
    Kind           string   `json:"kind"`
    Relation       string   `json:"relation,omitempty"`
    Raw            string   `json:"raw"`
    Canonical      string   `json:"canonical,omitempty"`
    ResolvedID     string   `json:"resolved_id,omitempty"`
    Source         string   `json:"source,omitempty"`
    Confidence     string   `json:"confidence,omitempty"`
    ExactTerms     []string `json:"exact_terms,omitempty"`
    PhraseTerms    []string `json:"phrase_terms,omitempty"`
    ExpansionTerms []string `json:"expansion_terms,omitempty"`
}
```

Also add `RawQuestion string`, `ContinuityAnchors []ProtectedAnchor`, `Attempt string`, and optional `AnchorResolver AnchorResolver` to `Options`; add `ProtectedAnchors []ProtectedAnchor` to `QueryPlan`; add `Role string 'json:"role,omitempty"'` to `QueryConcept`.

Add the resolver interface in the same package:

```go
type AnchorResolver interface {
    ResolveAnchors(ctx context.Context, anchors []ProtectedAnchor) ([]ProtectedAnchor, error)
}
```

- [x] **Step 1.4: Implement deterministic anchor extraction**

Create `internal/brainresearch/anchors.go` with:

- `extractProtectedAnchors(raw string) []ProtectedAnchor`
- `anchorFromHandle(raw string, source string) ProtectedAnchor`
- `anchorFromUnderscoreAlias(raw string, source string) ProtectedAnchor`
- `anchorFromSourceKey(raw string, source string) ProtectedAnchor`
- `anchorTerms(raw string) (canonical string, exact []string, phrase []string, expansion []string)`
- `dedupeProtectedAnchors(anchors []ProtectedAnchor) []ProtectedAnchor`

Rules:

- Use `(^|[^A-Za-z0-9_])@[A-Za-z0-9_]{2,32}` and preserve the token after the left boundary.
- Use `\b[A-Za-z][A-Za-z0-9]+_[A-Za-z][A-Za-z0-9_]+\b` only when the question has author/entity/synthesis context or the token is quoted.
- Reuse `sourceKeyCandidates(raw)` for source-key anchors.
- Canonicalize handles and aliases by trimming `@`, lowercasing, and preserving underscores.
- Do not pluralize anchor terms through `conceptTermAliases`.

- [x] **Step 1.4a: Add entity-backed anchor resolution**

Create `internal/brainresearch/anchor_resolver.go` with:

- `type storeAnchorResolver struct { st *store.Store }`
- `func (r storeAnchorResolver) ResolveAnchors(ctx context.Context, anchors []ProtectedAnchor) ([]ProtectedAnchor, error)`
- `func (b *Builder) resolveProtectedAnchors(ctx context.Context, anchors []ProtectedAnchor, opts Options) []ProtectedAnchor`

Implementation rules:

- If `opts.AnchorResolver` is set, call it with a bounded child context and keep raw anchors if it returns an error or times out.
- Otherwise use `storeAnchorResolver` backed by `internal/entities.Search` / `entities.Filter` over the local entity index.
- For handle or underscore alias anchors, resolve exact alias/name/key matches to entity keys such as `x-author:kristof_poland`, copy matching entity aliases into `ExactTerms`/`PhraseTerms`, set `ResolvedID`, and keep `Confidence: "alias"` or `Confidence: "exact"`.
- Do not drop unresolved anchors.
- Emit a trace event such as `protected_anchors_resolved` with raw/canonical/resolved IDs and error state.

Add tests to `internal/brainresearch/anchors_test.go`:

```go
func TestAnchorResolverEnrichesKnownXAuthorEntity(t *testing.T)
func TestAnchorResolverKeepsRawAnchorWhenResolutionFails(t *testing.T)
```

Use a deterministic fake resolver in the unit tests and a small store-backed fixture for the X author path.

- [x] **Step 1.5: Add concept role policy tests**

In `internal/brainresearch/research_test.go`, add tests named:

```go
func TestBuildResearchStrategyRolesIntentAndFrameTermsForAnchoredSynthesis(t *testing.T)
func TestBuildResearchStrategyAllowsIntentTermsWhenNoStrongerTopicExists(t *testing.T)
func TestMergeQueryConceptsCannotReRequireIntentAfterPlannerMerge(t *testing.T)
```

Assertions:

- For `Can you synthesize the Tweets from @Kristof_Poland - they're in the dbrain.`, concepts include a `Role: "anchor"` concept for `kristof_poland`; `synthesize` is `Role: "intent"` and `Required:false`; `dbrain`, `they`, and `re` are absent or `Role: "frame"` with `Required:false`.
- For `Find notes about synthesis essays`, `synthesis` and `essays` remain searchable and at least `essays` remains `Required:true`.
- Planner-added `synthesize` or `summary` cannot become `Required:true` after merge when an anchor/content concept exists.

- [x] **Step 1.6: Verify role tests fail**

Run:

```sh
go test ./internal/brainresearch -run 'TestBuildResearchStrategyRolesIntent|TestBuildResearchStrategyAllowsIntent|TestMergeQueryConceptsCannotReRequireIntent'
```

Expected: fail because `Role` policy is not implemented and default concepts are still all required.

- [x] **Step 1.7: Implement concept role policy**

In `internal/brainresearch/strategy_concepts.go`, add constants:

```go
const (
    conceptRoleAnchor  = "anchor"
    conceptRoleContent = "content"
    conceptRoleIntent  = "intent"
    conceptRoleFrame   = "frame"
)
```

Add helpers:

- `buildQueryConceptsWithAnchors(terms []string, anchors []ProtectedAnchor) []QueryConcept`
- `conceptsForAnchors(anchors []ProtectedAnchor) []QueryConcept`
- `classifyConceptRole(term string) string`
- `applyConceptRolePolicy(concepts []QueryConcept) []QueryConcept`
- `hasStrongConcept(concepts []QueryConcept) bool`
- `conceptPreferredTerm(concept QueryConcept) string`

Policy:

- Anchor concepts come first, are `Required:true`, and keep exact/phrase terms without plural expansion.
- Intent terms are optional when at least one anchor or content concept exists.
- Frame terms are optional and can be dropped before variant generation.
- Existing special domain concepts keep role `content` unless the switch case is already explicitly optional.

- [x] **Step 1.8: Thread anchors through strategy and pack**

In `internal/brainresearch/research.go`:

- Compute `rawQuestion := strings.TrimSpace(firstNonEmpty(opts.RawQuestion, opts.Question))` before `ask.SearchText(question)`.
- Extract anchors from `rawQuestion`.
- If no explicit current-turn anchors are found, append `opts.ContinuityAnchors`; if explicit current-turn anchors exist, ignore stale continuity anchors for this build.
- Resolve anchors through `b.resolveProtectedAnchors(ctx, anchors, opts)` before strategy construction.
- Emit anchors in `question_normalized` and `query_plan_built`.
- Add `ProtectedAnchors: anchors` to `Pack.QueryPlan`.

In `internal/brainresearch/strategy.go`:

- Keep the package helper as `buildResearchStrategy(question, hints)` calling a new helper with nil anchors.
- Change builder strategy construction to pass anchors into deterministic and planner merge paths.
- Reapply role policy after `mergeQueryConcepts`.

In `internal/brainresearch/strategy_variants.go`:

- Make `preferredConceptQueryExcluding` and `focusedConceptVariants` skip `Role: "intent"` and `Role: "frame"` when stronger anchor/content concepts exist.

In `internal/brainresearch/planner_merge.go`:

- Preserve `Role` in `sanitizeMergedConcept`.
- Merge role with anchor > content > intent > frame precedence.
- Reapply concept role policy after merge and before truncating non-anchor concepts.

- [x] **Step 1.9: Verify Phase 1 targeted tests pass**

Run:

```sh
go test ./internal/brainresearch -run 'TestExtractProtectedAnchors|TestSourceKeyCandidatesRemainProtectedAnchors|TestBuildResearchStrategyRolesIntent|TestBuildResearchStrategyAllowsIntent|TestMergeQueryConcepts'
```

Expected: pass.

- [x] **Step 1.10: Run Phase 1 package tests**

Run:

```sh
go test ./internal/brainresearch
```

Expected: pass.

- [x] **Step 1.11: Run OpenCode/GLM Phase 1 review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review Phase 1 of the chat harness fix. Do not edit files. Inspect the current diff plus docs/superpowers/plans/2026-07-04-chat-harness-entity-synthesis-retrieval.md and docs/superpowers/plans/2026-07-04-chat-harness-phased-implementation.md. Focus on protected anchor extraction, AnchorResolver/entity-map enrichment, raw question use, QueryPlan.ProtectedAnchors, QueryConcept.Role, planner merge role preservation, variant generation, false positives for emails/code identifiers, graceful resolver degradation, and tests. Verification run: go test ./internal/brainresearch. Return P0/P1/P2 findings first with file/line evidence and an explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted findings and rerun targeted tests before Phase 2.

## Phase 2: Judge Missing Content, Not Intent

**Files:**
- Modify: `internal/brainresearch/anchors.go`
- Create: `internal/brainresearch/anchor_match_test.go`
- Modify: `internal/researchrun/judge.go`
- Modify: `internal/researchrun/run_test.go`
- Modify: `internal/researchrun/types.go`

- [x] **Step 2.1: Write failing anchor matcher tests**

Create `internal/brainresearch/anchor_match_test.go` with tests named:

```go
func TestEvidenceMatchesProtectedAnchorFromAuthorMetadata(t *testing.T)
func TestEvidenceDoesNotMatchProtectedAnchorByExpansionTermsOnly(t *testing.T)
func TestEvidenceMatchesSourceKeyAnchorExactly(t *testing.T)
```

Assertions:

- `ask.Evidence{Author: "Krzysztof Szczawinski @Kristof_Poland"}` matches a `kristof_poland` handle anchor even if Title/Summary omit the handle.
- `ask.Evidence{Title: "Poland economy notes"}` does not match the handle anchor only because it contains `poland`.
- `ask.Evidence{SourceKey: "x:2071948517837353292"}` matches a source-key anchor for the same key.

- [x] **Step 2.2: Write failing judge tests**

In `internal/researchrun/run_test.go`, add:

```go
func TestJudgeIgnoresMissingIntentForAnchoredEvidence(t *testing.T)
func TestJudgeAggregatesMissingContentAcrossAnchoredRows(t *testing.T)
func TestJudgeStillRetriesGenuineMissingContent(t *testing.T)
```

Assertions:

- A pack with `QueryPlan.ProtectedAnchors` and concepts `kristof_poland(anchor required)` plus `synthesize(intent optional)` whose top row has `MissingTerms: []string{"synthesize", "they"}` returns `JudgeEnoughEvidence`.
- If one anchored row misses `essays` but another inspected anchored row matches it, the judge does not retry.
- If every inspected anchored row misses `essays`, the judge returns `JudgeWeakEvidence`, `RetryFocusedVariant`, and `MissingConcepts: []string{"essays"}`.

- [x] **Step 2.3: Verify Phase 2 tests fail**

Run:

```sh
go test ./internal/brainresearch ./internal/researchrun -run 'TestEvidenceMatchesProtectedAnchor|TestEvidenceDoesNotMatchProtectedAnchor|TestEvidenceMatchesSourceKeyAnchor|TestJudgeIgnoresMissingIntent|TestJudgeAggregatesMissingContent|TestJudgeStillRetriesGenuineMissingContent'
```

Expected: fail because matcher and judge role filtering are not implemented.

- [x] **Step 2.4: Export anchor matching helper**

In `internal/brainresearch/anchors.go`, add:

```go
func EvidenceMatchesProtectedAnchor(row ask.Evidence, anchor ProtectedAnchor) bool
func EvidenceMatchesAnyProtectedAnchor(row ask.Evidence, anchors []ProtectedAnchor) bool
```

Match source key, URL/permalink text, `Author`, `EntityMatches`, `UserTags`, title/body/summary/excerpt/content sections against exact and phrase terms. Do not treat `ExpansionTerms` as proof of an anchor match.

- [x] **Step 2.5: Replace judge missing-term logic**

In `internal/researchrun/judge.go`:

- Build a concept-role lookup from `pack.QueryPlan.Concepts`.
- Filter `Retrieval.MissingTerms` to mapped `Role: "anchor"` or `Role: "content"` concepts only.
- Drop unmapped terms such as `re` and optional intent/frame terms.
- Inspect anchored rows first when protected anchors exist; otherwise inspect up to three direct rows plus exact-tag fallback.
- Compute missing concepts by intersection: retry only concepts missing from every inspected row.
- Keep related-expansion behavior when direct evidence count is below `MinEvidenceForEnough`.

- [x] **Step 2.6: Update judge result trace fields if useful**

In `internal/researchrun/types.go`, add optional fields only if tests need visibility:

```go
AnchorSupport map[string]int `json:"anchor_support,omitempty"`
```

Keep backward compatibility for existing JSON consumers.

- [x] **Step 2.7: Verify Phase 2 package tests pass**

Run:

```sh
go test ./internal/brainresearch ./internal/researchrun -run 'TestEvidenceMatchesProtectedAnchor|TestEvidenceDoesNotMatchProtectedAnchor|TestEvidenceMatchesSourceKeyAnchor|TestJudge'
```

Expected: pass.

- [x] **Step 2.8: Run OpenCode/GLM Phase 2 review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review Phase 2 of the chat harness fix. Do not edit files. Inspect the current diff and plan docs. Focus on anchor matching, judge role filtering, top-N/anchored-row selection, dropping unmapped intent/frame missing terms, exact-tag fallback, and regression tests. Verification run: go test ./internal/brainresearch ./internal/researchrun -run 'TestEvidenceMatchesProtectedAnchor|TestEvidenceDoesNotMatchProtectedAnchor|TestEvidenceMatchesSourceKeyAnchor|TestJudge'. Return findings first with severity and an explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted findings and rerun targeted tests before Phase 3.

## Phase 3: Merge-Not-Replace Retries

**Files:**
- Create: `internal/brainresearch/retry_merge.go`
- Create: `internal/brainresearch/retry_merge_test.go`
- Modify: `internal/brainresearch/synthesize.go`
- Modify: `internal/brainresearch/synthesize_test.go`
- Modify: `internal/researchrun/run.go`
- Modify: `internal/researchrun/run_test.go`

- [x] **Step 3.1: Write failing merge helper tests**

Create `internal/brainresearch/retry_merge_test.go` with:

```go
func TestMergeRetryPackPreservesInitialAnchoredRowsAndRejectsGenericRetry(t *testing.T)
func TestMergeRetryPackAcceptsRetryRowsThatFillMissingContent(t *testing.T)
func TestMergeRetryPackRecomputesCoverageAndRecallNote(t *testing.T)
func TestMergeRetryPackReordersNoAnchorCaseByContentMatch(t *testing.T)
```

Assertions:

- Initial `x:kristof-1` and `x:kristof-2` survive when retry returns only `src:generic-synthesis`.
- Retry row matching the protected anchor and containing missing content `essays` is accepted.
- `Coverage.EvidenceCount`, `ByKind`, `BySourceType`, `TopUserTags`, and `RecallNote` reflect merged evidence and preserved corpus coverage fields.
- In a no-anchor case, an accepted retry row that fills the missing content concept may rank ahead of off-topic initial rows, while initial direct rows are still preserved and not evicted by `Limit`.

Before implementing Phase 3, confirm `buildCoverage`, `mergeCoverage`, and `recallNote` are package-local helpers in `internal/brainresearch`; `MergeRetryPack` belongs in that package so it can reuse those helpers without exporting coverage internals.

- [x] **Step 3.2: Write failing runner retry tests**

In `internal/researchrun/run_test.go`, add:

```go
func TestRunFocusedRetryMergesInsteadOfReplacingInitialEvidence(t *testing.T)
func TestRunFocusedRetryQuestionCarriesProtectedAnchors(t *testing.T)
```

Use a deterministic store fixture with Kristof-like X rows and generic synthesis source rows. Use fake synthesis binary. Assert final `result.Pack.Evidence` contains the initial `x:` rows and does not consist only of generic `src:` rows.

- [x] **Step 3.3: Verify Phase 3 tests fail**

Run:

```sh
go test ./internal/brainresearch ./internal/researchrun -run 'TestMergeRetryPack|TestRunFocusedRetry'
```

Expected: fail because retries still replace packs and no merge helper exists.

- [x] **Step 3.4: Implement merge helper**

In `internal/brainresearch/retry_merge.go`, add:

```go
type MergeRetryOptions struct {
    MissingConcepts []string
    RetryAction     string
    RetryQuestion   string
}

type MergeRetryDecision struct {
    PreservedInitialSourceKeys []string `json:"preserved_initial_source_keys,omitempty"`
    AcceptedRetrySourceKeys    []string `json:"accepted_retry_source_keys,omitempty"`
    RejectedRetrySourceKeys    []string `json:"rejected_retry_source_keys,omitempty"`
    FinalSourceKeys            []string `json:"final_source_keys,omitempty"`
    Reason                     string   `json:"reason,omitempty"`
}

func MergeRetryPack(initial Pack, retry Pack, opts MergeRetryOptions) (Pack, MergeRetryDecision)
```

Rules:

- Keep initial `QueryPlan`, `Topic`, `TopicBrief`, `UsedTopicBrief`, and `NextSteps`.
- Dedupe by `SourceKey`.
- Preserve every initial direct evidence row.
- Accept retry rows that match any protected anchor or fill a missing content concept; reject generic retry rows that do neither.
- Rebuild row-derived coverage with `buildCoverage(mergedEvidence)`, merge initial corpus coverage fields with `mergeCoverage`, and recompute `RecallNote`.
- Append/dedupe exact-tag evidence; do not let retry exact-tag rows erase initial exact-tag rows.

- [x] **Step 3.5: Compose anchored retry questions in runner**

In `internal/researchrun/run.go`:

- Change `runRetry(judge)` to `runRetry(initialPack, judge)`.
- For `RetryFocusedVariant`, build the retry question from `initialPack.QueryPlan.ProtectedAnchors` exact/preferred terms plus `judge.MissingConcepts`.
- For `RetryRelatedExpansion`, keep the related-expansion question shape, but still merge through `MergeRetryPack`; retry merge is unconditional for both retry actions.
- Build retry pack with `Attempt: "retry-1"` once Phase 5 adds attempt support; until then keep behavior compatible.
- Replace `pack = retryPack` with:

```go
merged, mergeDecision := brainresearch.MergeRetryPack(pack, retryPack, brainresearch.MergeRetryOptions{
    MissingConcepts: judge.MissingConcepts,
    RetryAction:     string(judge.RetryAction),
    RetryQuestion:   retryQuestion,
})
pack = merged
```

- Emit merge decision fields in `runner_retry_done`.

- [x] **Step 3.5a: Audit anchored rows that reach synthesis context**

In `internal/brainresearch/synthesize.go`, add an anchored-context helper used by `PrepareSynthesis`:

```go
func anchoredSynthesisContextStatus(pack Pack, prepared PreparedSynthesis) AnchorSynthesisContextStatus
```

`AnchorSynthesisContextStatus` should report per-anchor supported source keys, citation source keys, dropped source keys, partially trimmed keys, and reasons (`citation_limit`, `token_budget`, or `not_supported`). If a protected anchor is satisfied in the pack, at least one matching row for that anchor should appear in `prepared.Citations` unless every matching row is dropped by the token budget. Add a warning such as `anchor_evidence_truncated` when a satisfied anchor is not represented in citations.

Add `internal/brainresearch/synthesize_test.go` tests:

```go
func TestPrepareSynthesisKeepsAtLeastOneAnchoredRowInContext(t *testing.T)
func TestPrepareSynthesisWarnsWhenAnchorRowsDropFromTokenBudget(t *testing.T)
```

The first test uses a normal budget and expects a matching anchored source key in citations. The second uses a tiny budget and expects the anchored source key in truncation metadata plus the warning.

- [x] **Step 3.6: Verify Phase 3 package tests pass**

Run:

```sh
go test ./internal/brainresearch ./internal/researchrun -run 'TestMergeRetryPack|TestRunFocusedRetry|TestRunPerformsOneJudgedRelatedExpansion|TestPrepareSynthesis.*Anchor'
```

Expected: pass.

- [x] **Step 3.7: Run OpenCode/GLM Phase 3 review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review Phase 3 of the chat harness fix. Do not edit files. Inspect current diff and plan docs. Focus on MergeRetryPack correctness, unconditional merge for focused and related retries, coverage rebuilds, preservation of initial direct evidence, generic retry rejection, anchored retry question composition, synthesis-context anchored-row survival/truncation audit, result ordering, trace/progress fields, and tests. Verification run: go test ./internal/brainresearch ./internal/researchrun -run 'TestMergeRetryPack|TestRunFocusedRetry|TestRunPerformsOneJudgedRelatedExpansion|TestPrepareSynthesis.*Anchor'. Return findings first with severity and an explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted findings and rerun targeted tests before Phase 4.

## Phase 4: Web Chat Raw Question Boundary And Continuity Hygiene

**Files:**
- Modify: `internal/researchrun/types.go`
- Modify: `internal/researchrun/run.go`
- Modify: `internal/researchtrace/types.go`
- Modify: `web/research_run_handlers.go`
- Modify: `web/server_test.go`
- Modify: `web/ui/src/lib/chat.js`
- Modify: `web/ui/src/lib/chat.test.mjs`
- Modify: `web/ui/src/App.svelte`

- [x] **Step 4.1: Write failing JS prior-expansion tests**

In `web/ui/src/lib/chat.test.mjs`, add tests:

```js
test("buildChatRetrievalQuestion suppresses prior evidence for current handle searches", () => {})
test("buildChatRetrievalQuestion suppresses prior evidence for current underscore aliases", () => {})
test("buildChatTraceContinuity carries prior typed anchors only for pronoun followups", () => {})
test("buildChatTraceContinuity replaces stale prior anchors when current turn has an explicit anchor", () => {})
```

Assert a current `@Kristof_Poland` or `Kristof_Poland` question returns only `Current question: ...` and omits `Recent user questions` and `Prior evidence titles`.
Assert a follow-up such as `Synthesize those` carries the previous turn's `research_pack.query_plan.protected_anchors` as typed continuity anchors, while a current explicit `@Other_Author` does not carry the stale `@Kristof_Poland` anchor.

- [x] **Step 4.2: Write failing server/raw-question tests**

In `web/server_test.go` or a focused handler test, assert `/research/run` passes both:

- composed `Question` for retrieval
- current raw user text as `SynthesisQuestion`/`RawQuestion` into runner/brainresearch

If direct handler inspection is too broad, add a smaller `runnerRawQuestion` helper test and wire it.

- [x] **Step 4.3: Verify Phase 4 tests fail**

Run:

```sh
go test ./web -run 'TestResearchRun.*Raw|TestWebHandler.*ResearchRun'
npm --prefix web/ui test -- --test-name-pattern='handle|underscore'
```

Expected: at least the new tests fail before implementation.

- [x] **Step 4.4: Add `RawQuestion` to runner options and brainresearch calls**

In `internal/researchrun/types.go`, add `RawQuestion string` to `Options`.

In `internal/researchtrace/types.go`, add typed continuity anchors to `ChatContinuity`:

```go
ContinuityAnchors []brainresearch.ProtectedAnchor `json:"continuity_anchors,omitempty"`
```

In `internal/researchrun/run.go`, pass:

```go
RawQuestion: firstNonEmpty(r.opts.RawQuestion, r.synthesisQuestion()),
```

into `brainresearch.Options`.

In `web/research_run_handlers.go`, set runner `RawQuestion` from the latest user utterance, preferring `TraceContinuity.OriginalQuestion` only when it is the current turn's raw question. Keep `SynthesisQuestion` for answer framing. Pass `TraceContinuity.ContinuityAnchors` through to `brainresearch.Options` as prior anchors only when the current raw question has no explicit anchors and the request is a pronoun follow-up.

- [x] **Step 4.5: Tighten JS prior expansion**

In `web/ui/src/lib/chat.js`, add helpers:

```js
function hasCurrentProtectedAnchor(question) {
  return /(^|[^A-Za-z0-9_])@[A-Za-z0-9_]{2,32}/.test(question) ||
    /\b[A-Za-z][A-Za-z0-9]+_[A-Za-z][A-Za-z0-9_]+\b/.test(question) ||
    /\b(?:x:\d+|src:[0-9a-f]{8,}|feed-entry:[A-Za-z0-9._:-]+)\b/i.test(question);
}
```

Call it before the `words.length <= 5` shortcut in `shouldIncludePriorExpansion`.

Add a new exported JS helper, `buildChatTraceContinuity(question, retrievalQuestion, turns, pinnedEvidenceKeys)`, and use it from `web/ui/src/App.svelte` instead of manually constructing the trace-continuity object inline. The helper must:

- set `original_question` to the current user text,
- set `retrieval_question` to the composed retrieval text,
- include `prior_question_ids`, `pinned_evidence_keys`, and `merged_prior_evidence`,
- include `continuity_anchors` only for pronoun-style follow-ups with no current explicit anchor,
- never derive continuity anchors from model answers or arbitrary evidence titles.

- [x] **Step 4.6: Verify Phase 4 tests pass**

Run:

```sh
go test ./internal/researchrun ./web -run 'Test.*Raw|TestResearchRunStreamsProgressAnswerAndTrace'
npm --prefix web/ui test
```

Expected: pass.

- [x] **Step 4.7: Run OpenCode/GLM Phase 4 review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review Phase 4 of the chat harness fix. Do not edit files. Inspect current diff and plan docs. Focus on raw current-user question plumbing, typed continuity anchors for pronoun follow-ups, explicit current-turn anchor precedence, web chat continuity, suppression of prior bad evidence titles for handle/underscore/source-key searches, preservation of legitimate follow-ups, JS/Go regex drift, and tests. Verification run: go test ./internal/researchrun ./web -run 'Test.*Raw|TestResearchRunStreamsProgressAnswerAndTrace' and npm --prefix web/ui test. Return findings first with severity and explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted findings and rerun targeted tests before Phase 5.

## Phase 5: Attempt-Specific Trace Artifacts

**Files:**
- Modify: `internal/brainresearch/types.go`
- Modify: `internal/brainresearch/planner.go` or planner observer call site
- Modify: `internal/researchtrace/recorder.go`
- Modify: `internal/researchtrace/types.go`
- Modify: `internal/researchtrace/write.go`
- Modify: `internal/researchtrace/trace_test.go`
- Modify: `internal/researchrun/run.go`
- Modify: `internal/researchrun/run_test.go`

- [x] **Step 5.1: Write failing trace artifact test**

In `internal/researchrun/run_test.go` or `internal/researchtrace/trace_test.go`, add a test that builds an initial pack and retry with a fake planner binary and asserts the trace directory contains:

- `planner-initial-input.md`
- `planner-initial-output.json`
- `planner-retry-1-input.md`
- `planner-retry-1-output.json`

The test must also assert old aggregate metrics remain populated.

- [x] **Step 5.2: Verify trace test fails**

Run:

```sh
go test ./internal/researchtrace ./internal/researchrun -run 'Test.*Planner.*Attempt|Test.*Trace.*Attempt'
```

Expected: fail because attempt artifacts are not written.

- [x] **Step 5.3: Add optional attempt observer methods**

In `internal/brainresearch/types.go`, keep `Observer` unchanged and add optional interfaces:

```go
type AttemptPlannerInputObserver interface {
    PlannerInputAttempt(attempt string, input string)
}

type AttemptPlannerOutputObserver interface {
    PlannerOutputAttempt(attempt string, output string)
}
```

Make planner observer call sites use attempt methods when `opts.Attempt` is non-empty, then still populate aggregate planner input/output for backward compatibility.

- [x] **Step 5.4: Store attempt artifacts in recorder and writer**

In `internal/researchtrace`, add an attempt-artifact map to `ArtifactContents` and write each attempt input/output to deterministic filenames. Keep old `planner-input.md` and `planner-output.json` for compatibility when aggregate fields are present.

- [x] **Step 5.5: Pass attempt labels from runner**

In `internal/researchrun/run.go`, call `buildPack(false, r.opts.Question, "initial")` for initial pack and `buildPack(..., retryQuestion, "retry-1")` for retry pack. Thread the attempt into `brainresearch.Options.Attempt`.

- [x] **Step 5.6: Verify Phase 5 tests pass**

Run:

```sh
go test ./internal/brainresearch ./internal/researchtrace ./internal/researchrun -run 'Test.*Planner.*Attempt|Test.*Trace.*Attempt|TestRun'
```

Expected: pass.

- [x] **Step 5.7: Run OpenCode/GLM Phase 5 review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review Phase 5 of the chat harness fix. Do not edit files. Inspect current diff and plan docs. Focus on attempt-scoped planner artifact compatibility, metrics, trace writer behavior, old trace-reader compatibility, runner attempt labels, and tests. Verification run: go test ./internal/brainresearch ./internal/researchtrace ./internal/researchrun -run 'Test.*Planner.*Attempt|Test.*Trace.*Attempt|TestRun'. Return findings first with severity and explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted findings and rerun targeted tests before Phase 6.

## Phase 6: Runner-Level Eval, Docs, And Changelog

**Files:**
- Modify: `internal/researcheval/types.go`
- Modify: `internal/researcheval/run.go`
- Modify: `internal/researcheval/run_test.go`
- Modify: `internal/researcheval/trace.go`
- Create: `internal/researchrun/answer_guard.go`
- Modify: `internal/researchrun/run_test.go`
- Modify: `docs/research-harness.md`
- Modify: `docs/README.md`
- Modify: `CHANGELOG.md`

- [x] **Step 6.1: Write failing runner eval tests**

In `internal/researcheval/run_test.go`, add tests:

```go
func TestRunnerEvalCatchesRetryReplacementRegression(t *testing.T)
func TestTraceDiffUsesRawQuestionFromChatContinuity(t *testing.T)
func TestAnchoredAnswerGuardRejectsFalseNoSourcesClaim(t *testing.T)
func TestRunnerEvalUsesTypedContinuityAnchorForPronounFollowUp(t *testing.T)
func TestRunnerEvalReplacesStaleAnchorOnTopicShift(t *testing.T)
```

Assertions:

- A CI-safe runner eval case can stop after judge/retry merge without calling a model.
- The Kristof-like fixture returns anchored `x:` rows and forbids generic synthesis-only `src:` rows.
- Trace diff replay sets `brainresearch.Options.RawQuestion` from trace chat continuity when available.
- A fake synthesis answer that claims there are no sources for a protected anchor fails the answer guard when the prepared synthesis context contains matching anchored evidence.
- A runner eval with a composed retrieval question containing prior evidence-title lines and a raw current question `Synthesize those` reuses only the typed prior `x-author:kristof_poland` continuity anchor.
- A runner eval where the current raw question introduces `@Other_Author` ignores the stale prior `@Kristof_Poland` continuity anchor.

- [x] **Step 6.2: Verify eval tests fail**

Run:

```sh
go test ./internal/researcheval ./internal/researchrun -run 'TestRunnerEval|TestTraceDiffUsesRawQuestion|TestAnchoredAnswerGuard'
```

Expected: fail because runner eval mode and the answer guard do not exist.

- [x] **Step 6.3: Add runner eval mode**

Extend `researcheval.Case` with:

```go
RunWithRunner bool `json:"run_with_runner,omitempty"`
StopAfterJudge bool `json:"stop_after_judge,omitempty"`
RawQuestion string `json:"raw_question,omitempty"`
ExpectJudgeVerdict string `json:"expect_judge_verdict,omitempty"`
ForbidRetryAction string `json:"forbid_retry_action,omitempty"`
```

Add a runner execution path that disables persistent traces by default and stops before synthesis when `StopAfterJudge` is true. If `researchrun` needs an option such as `StopAfterJudge`, add it there with tests rather than using model calls in eval.

- [x] **Step 6.4: Update trace replay**

In `internal/researcheval/trace.go`, set `RawQuestion` from `trace.ChatContinuity.OriginalQuestion` when it is present and use the original trace question as the composed retrieval question. Do not reconstruct eval inputs from the bad final retry pack.

- [x] **Step 6.4a: Add answer-stage anchored-evidence guard**

Create `internal/researchrun/answer_guard.go` with:

```go
func GuardAnchoredAnswer(pack brainresearch.Pack, prepared brainresearch.PreparedSynthesis, synthesis brainresearch.SynthesisResult) VerificationResult
```

Guard rules:

- If there are no protected anchors or no prepared citations matching protected anchors, pass.
- If there is protected-anchor evidence in prepared citations, fail answers that claim the corpus has no source material for that anchor using conservative patterns such as `no sources`, `no evidence`, `does not contain`, `corpus lacks`, or `not present` near the anchor raw/canonical/phrase.
- Return a verification error that names the anchor and supporting source keys.
- Wire this guard after synthesis and before normal citation verification in `researchrun.Run`.

Add a fake-synthesis runner test that produces `The corpus has no sources for @Kristof_Poland [x:kristof-1].` while the prepared context contains `x:kristof-1`, and assert `StopVerificationFailed`.

- [x] **Step 6.5: Update docs and changelog**

In `docs/research-harness.md`, document:

- protected anchors
- concept roles
- non-destructive retry merge
- raw question versus retrieval question
- attempt-specific planner artifacts
- runner-level evals
- answer-stage anchored-evidence guard
- synthesis-context anchor truncation audit

In `docs/README.md`, keep the research harness docs indexed if needed.

In `CHANGELOG.md`, add a dated fix entry:

```md
- Fixed web Chat/research runner synthesis over explicit handles/entities so intent-term retries cannot replace anchored local evidence with unrelated generic documents.
```

- [x] **Step 6.6: Verify Phase 6 tests pass**

Run:

```sh
go test ./internal/researcheval ./internal/researchrun ./internal/brainresearch
```

Expected: pass.

- [x] **Step 6.7: Run OpenCode/GLM Phase 6 review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Review Phase 6 of the chat harness fix. Do not edit files. Inspect current diff and plan docs. Focus on runner-level eval safety, trace replay/raw-question semantics, no model/credential/persistent trace dependency in CI tests, answer-stage anchored-evidence guard, docs accuracy, changelog, and whether the Kristof-like regression is now covered at the layer where the bug occurred. Verification run: go test ./internal/researcheval ./internal/researchrun ./internal/brainresearch. Return findings first with severity and explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted findings and rerun targeted tests before final validation.

## Phase 7: Full Validation And Final OpenCode Pass

**Files:**
- All files touched in Phases 1-6.

- [x] **Step 7.1: Run all targeted tests**

Run:

```sh
go test ./internal/brainresearch ./internal/researchrun ./internal/researchtrace ./internal/researcheval ./web/...
npm --prefix web/ui test
```

Expected: pass.

- [x] **Step 7.2: Run project gates**

Run:

```sh
task fmt
task lint
task test-ci
```

Expected: pass.

- [x] **Step 7.3: Build CLI if eval or CLI behavior changed**

Run:

```sh
task build
```

Expected: pass and produce an updated local `./bin/dbrain`.

- [x] **Step 7.4: Run real trace/eval smoke when local data is available**

If the production trace path exists locally, run:

```sh
./bin/dbrain --no-debug eval research diff --trace /Users/darron/.local/share/dbrain/research-runs/20260704T174350.016236000Z-65ae1fd81bd8
```

Expected: the diff path no longer encodes generic replacement as the desired behavior; any runner eval derived from the trace keeps Kristof_Poland `x:` rows.

- [x] **Step 7.5: Run final OpenCode/GLM review**

Run:

```sh
opencode run --dir /Users/darron/src/dbrain --model zai-coding-plan/glm-5.2 --variant high "Final review of the complete chat harness fix. Do not edit files. Inspect git status, full diff, tests, docs, and docs/superpowers/plans/2026-07-04-chat-harness-entity-synthesis-retrieval.md plus docs/superpowers/plans/2026-07-04-chat-harness-phased-implementation.md. User goal: explicit entity/handle chat synthesis should preserve anchored dbrain evidence and non-destructive retries should not replace it with generic intent-term documents. Verification run: go test ./internal/brainresearch ./internal/researchrun ./internal/researchtrace ./internal/researcheval ./web/..., npm --prefix web/ui test, task fmt, task lint, task test-ci, and task build if CLI changed. Focus on correctness bugs, regressions, missing tests, trace/eval reliability, raw/composed question semantics, and user-facing confusion. Return findings first with severity and explicit ready/not-ready recommendation."
```

Expected: ready or findings. Fix accepted P0/P1 findings, rerun affected tests/gates, and rerun final review if the fixes materially change behavior.

## Acceptance Checklist

- [x] `@Kristof_Poland` and `Kristof_Poland` are protected anchors before normalization.
- [x] Query plan JSON exposes protected anchors and concept roles.
- [x] `synthesize`, `summary`, `overview`, `they`, `re`, `come`, and `up` cannot by themselves make anchored evidence weak.
- [x] Judge evaluates missing anchor/content concepts across anchored rows, not only the first row.
- [x] Focused retry query carries protected anchors when anchors exist.
- [x] Focused and related retries merge into the initial pack rather than replacing it.
- [x] Merged packs preserve initial direct evidence and recompute coverage.
- [x] Web Chat uses raw current user text for new anchors and suppresses prior evidence-title expansion for current handle/underscore/source-key searches.
- [x] Web Chat pronoun follow-ups reuse prior resolved anchors only through typed continuity anchors, and explicit current-turn anchors replace stale prior anchors.
- [x] Attempt-specific planner artifacts preserve initial and retry inputs/outputs.
- [x] Prepared synthesis context keeps at least one row per satisfied anchor unless token budget truncation is traced.
- [x] Answer guard rejects false "no sources for this anchor" claims when protected-anchor evidence is present in the answer context.
- [x] Runner-level eval/test coverage catches the destructive retry replacement class of bug.
- [x] Docs and changelog describe the user-visible fix.
- [x] OpenCode/GLM reviewed every implementation phase and final readiness.
