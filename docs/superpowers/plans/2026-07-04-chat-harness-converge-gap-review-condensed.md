# Scope note

The supplied brief is **visibly cut off** in §4 at `ExactTagEvidence: keep initial exact-tag evidence; append/dedupe retry...` with an input truncation marker. The findings below are based on the visible brief. If the omitted tail already covers some tests/CI/trace details, treat those items as “verify present”; otherwise they are remaining gaps.

---

# Highest-priority findings

## P0. Anchor satisfaction must use structured metadata, not just text terms

The plan protects handles and aliases, but it does not clearly define how a row is determined to “match” a protected anchor. For the reported failure, the important fact is that X rows are **authored by** `@Kristof_Poland`. Those rows may not contain the literal handle in title/body text.

If merge/judge logic checks only normalized row text, it can still classify true `@Kristof_Poland` rows as non-anchored and allow generic “synthesis” retry rows to dominate.

**Recommendation**

Define anchor matching over structured fields first:

- X author handle / author canonical ID.
- Entity canonical key, e.g. `x-author:kristof_poland`.
- Source key / collection key.
- Exact tag aliases.
- URL/permalink/source metadata.
- Only then fallback to title/body text.

Add tests where the row body does **not** mention `@Kristof_Poland`, but row metadata author is `@Kristof_Poland`; judge and merge must treat it as anchored.

---

## P0. Entity-map integration is under-specified

The problem statement relies on:

```text
dbrain_entity_map("Kristof_Poland") -> x-author:kristof_poland
```

But the proposed fix mostly describes regex extraction and token variants. Regex extraction alone is not enough. The protected anchor should resolve to the same canonical entity representation used by search, scoring, traces, and row metadata.

**Recommendation**

Make protected-anchor extraction call or reuse entity resolution where available:

```text
raw handle/alias -> canonical entity key -> aliases -> structured match predicates
```

For the Kristof case, assert that the query plan contains something equivalent to:

```text
Canonical: x-author:kristof_poland
Aliases:
  - @Kristof_Poland
  - @kristof_poland
  - Kristof_Poland
  - kristof_poland
```

Add tests asserting canonical entity propagation into:

- `QueryPlan.ProtectedAnchors`
- retrieval variants
- judge missing-term filtering
- retry query composition
- merge acceptance/order
- final trace output

---

## P0. `RawQuestion` plumbing may not fix web Chat if `Question` remains polluted

The brief correctly says not to extract anchors from the composed retrieval question that includes prior evidence titles. However, if `Question` is still the composed retrieval question and continues to drive planning, search terms, scorer `MissingTerms`, variant generation, and retry behavior, prior evidence titles can still pollute the pipeline.

This can preserve anchors in metadata while still producing bad initial queries or bad missing terms.

**Recommendation**

Specify the data contract explicitly across the web/UI/server boundary:

```text
RawUserQuestion / CurrentTurnQuestion:
  The latest user utterance before UI composition.
  Used for protected anchors and answer intent.

SynthesisQuestion / AnswerQuestion:
  The question the model should answer.

RetrievalContext:
  Typed continuity/context from prior turns.
  Should not be arbitrary concatenated titles unless intentionally used.

ComposedRetrievalQuestion:
  If still used, must not be the sole source for concepts, anchors, or required terms.
```

Also confirm the server actually receives the current raw user utterance. If `chat.js` composes the question before sending it and the backend receives only the composed `Question`, `Options.RawQuestion` cannot be set correctly.

**Concrete web contract test**

Assert that a request contains distinct fields equivalent to:

```json
{
  "Question": "composed retrieval question with prior evidence titles",
  "RawQuestion": "synthesize tweets/essays from @Kristof_Poland",
  "SynthesisQuestion": "synthesize tweets/essays from @Kristof_Poland",
  "ChatContinuity": {
    "OriginalQuestion": "..."
  }
}
```

Then assert backend extraction uses `RawQuestion`, not `Question`.

---

## P0. Use the current turn’s raw question, not always `ChatContinuity.OriginalQuestion`

The brief says `RawQuestion` should come from `researchrun.synthesisQuestion()` / `ChatContinuity.OriginalQuestion`. This is ambiguous and dangerous in multi-turn Chat.

Example:

```text
Turn 1: Tell me about dbrain.
Turn 2: Now synthesize tweets from @Kristof_Poland.
```

If `RawQuestion` is taken from the original conversation question, the new anchor in turn 2 is missed.

Conversely:

```text
Turn 1: Find tweets from @Kristof_Poland.
Turn 2: Synthesize those.
```

If only the latest raw user message is used, the anchor may be lost.

**Recommendation**

Define precedence clearly:

1. Use the **current turn raw user utterance** for newly introduced anchors and intent.
2. Carry forward only **typed continuity anchors** from prior turns, not scraped prior evidence titles.
3. Fall back to `Question` only for non-web callers that do not have separate raw/current-turn fields.

A useful structure would be:

```go
type ContextAnchor struct {
    Kind      string
    Canonical string
    Raw       string
    Source    string // "current_user_text", "prior_turn_entity", "selected_citation", etc.
}
```

The important point is not the exact struct name, but that continuity anchors should be typed and traced, not inferred from arbitrary composed text.

---

## P0. Pack merge is not enough; anchored rows must survive into final answer context

The plan focuses on preserving initial direct rows in the merged `Pack`. But the user-visible failure was the final answer saying no Kristof sources existed. Even if the merged pack is correct, later stages may still drop the anchored rows:

- display limit
- citation limit
- answer-context row limit
- token-budget trimming
- reranking/compaction before final synthesis
- frontend evidence truncation

**Recommendation**

Audit every post-merge selection boundary and add an invariant:

> If initial direct rows match protected anchors, anchored rows must survive into the final synthesis context unless impossible due to explicit token budget constraints.

Trace these counts/IDs:

```json
{
  "initial_anchor_rows": ["x:..."],
  "merged_anchor_rows": ["x:..."],
  "answer_context_anchor_rows": ["x:..."],
  "dropped_anchor_rows": [
    {
      "source": "x:...",
      "reason": "token_budget"
    }
  ]
}
```

Add an end-to-end runner test asserting not only that the merged pack contains `@Kristof_Poland` rows, but that the final answer context contains them too.

---

## P0. Prevent false “no sources” answers when anchored evidence exists

The original bad answer claimed there were no Kristof sources despite evidence existing. Retrieval fixes should be paired with answer-stage safeguards.

**Recommendation**

Add a synthesis-level assertion/eval:

- If answer context contains protected-anchor evidence, the answer must not claim absence of sources for that anchor.
- The answer should cite or summarize at least one anchored row when asked to synthesize that entity’s material.

This can be tested with deterministic/mock synthesis or a postcheck over generated text.

---

# High-priority implementation gaps

## P1. Anchor terms need purpose-specific tiers

The proposed `Terms` list includes:

```text
@Kristof_Poland
Kristof_Poland
kristof_poland
kristof poland
kristof-poland
kristof
poland
```

Including `kristof` and `poland` is useful for recall expansion, but dangerous for anchor satisfaction. A generic row about Poland should not count as matching the protected author anchor.

**Recommendation**

Separate terms by purpose:

```go
type ProtectedAnchor struct {
    Kind      string
    Raw       string
    Canonical string

    ExactTerms     []string // @Kristof_Poland, Kristof_Poland, kristof_poland
    PhraseTerms    []string // kristof poland, kristof-poland
    ExpansionTerms []string // kristof, poland
}
```

Use `ExactTerms`, `PhraseTerms`, source keys, and structured metadata for protected-anchor satisfaction. Use single-token expansion terms only for recall/boosting, not for judging that a row matches the anchor.

---

## P1. Normalize once across scorer, concepts, anchors, and judge

`Retrieval.MissingTerms` comes from `ask/scoring.go`, while concept/anchor terms are built elsewhere. The brief notes that `queryterms/normalize.go` strips `@` and `_`. If the judge maps missing terms to concepts using a different normalizer, terms can be dropped or misclassified.

**Failure modes**

- `kristof_poland`, `kristof-poland`, and `kristof poland` fail to map to the same anchor.
- `synthesize` remains a required missing term despite being intent.
- Legitimate short content terms such as `AI`, `Go`, or `S3` are dropped as unmapped.

**Recommendation**

Use one canonical normalization helper for:

- anchor term generation
- concept key/term normalization
- scorer missing-term production
- judge missing-term-to-concept mapping
- row anchor/content matching

Trace unmapped/dropped terms separately:

```json
{
  "missing_terms_raw": ["synthesize", "re", "kristof_poland"],
  "missing_terms_unmapped": ["re"],
  "missing_terms_dropped_as_intent_or_frame": ["synthesize"],
  "missing_concepts_for_retry": []
}
```

Add round-trip tests covering handles, underscores, dashes, quoted phrases, plurals, punctuation, and short terms.

---

## P1. Judge aggregation over `min(3, directRows)` is under-defined

The brief says to inspect `min(3, len(directRows))` and aggregate by intersection. This is better than judging only the first row, but still ambiguous.

Questions:

- Are these the first three rows by retrieval score?
- The first three anchor-matching rows?
- The first three direct rows after exact-tag evidence is removed?
- What if rows 1–3 are generic but rows 4–10 are anchored X rows?
- What if a multi-facet query has partial support across rows?

**Recommendation**

When protected anchors exist, judge primarily over anchor-matching rows, or at least compute concept support counts across inspected rows.

Trace support counts:

```json
{
  "concept_support": {
    "x-author:kristof_poland": 10,
    "tweets": 8,
    "essays": 2,
    "synthesize": 0
  }
}
```

Suggested policy:

- Anchor concepts: satisfied if enough evidence rows match structured anchor metadata.
- Content concepts: track support count across the inspected set.
- Intent/frame concepts: ignored for weakness/retry when anchors/content exist.
- Exact source-key queries: require exact source-key satisfaction.

---

## P1. Exact-tag-only packs need stronger preservation semantics

The brief says exact-tag rows with no direct rows should be judged with role-filtered missing-term logic. That is good, but merge and answer-context behavior must also be explicit.

**Recommendation**

If exact-tag evidence satisfies a protected tag/source/entity anchor:

- preserve it like initial direct evidence;
- do not let retry rows displace it;
- include it in answer-context protection;
- trace it separately from direct, retry, and related expansion evidence.

---

## P1. Retry merge semantics still need precise contracts

The visible brief says merge-not-replace is unconditional for focused retries, but the cut-off occurs exactly where merge winners are being specified. Several critical details must be explicit.

**Recommendations**

Define:

1. **Dedupe identity**

   Priority order should be something like:

   ```text
   canonical source key
   -> external platform ID / collection ID
   -> canonical URL/permalink
   -> normalized title/body hash fallback
   ```

   Preserve the richest metadata and all evidence reasons when duplicates merge.

2. **Limit behavior**

   “Never evict initial direct rows solely because of `Limit`” conflicts with bounded runner behavior unless limits are separated.

   Define separate limits, for example:

   ```text
   InitialDirectPreserveLimit
   RetryAcceptLimit
   MergedDisplayLimit
   AnswerContextTokenBudget
   ```

   At minimum:

   - retry rows must not displace anchored initial rows;
   - internal trace can preserve more rows than display/answer context;
   - answer context should prioritize anchored initial rows under token budget.

3. **Ordering algorithm**

   The current phrase “anchor/content-aware” is policy, not an algorithm.

   Define a deterministic sort key using explicit match reasons and original per-pack order. For example:

   ```text
   1. initial rows matching protected anchors
   2. exact-tag/source-key anchored rows
   3. accepted retry rows matching the same protected anchors and filling missing content
   4. initial rows matching content concepts
   5. no-anchor retry rows filling genuine missing content
   6. remaining initial rows
   ```

   Use original pack order as tie-breaker. Do not compare raw `Retrieval.Score` across different queries.

4. **Partial anchor matches**

   For multi-anchor queries, specify whether a retry row matching only one anchor is accepted, and how it is ranked.

5. **Retry rejection**

   If retry rows match neither protected anchors nor missing content, discard them and keep the initial pack.

---

## P1. `RetryRelatedExpansion` must use the same merge path in the same fix

The brief says:

> Route `RetryRelatedExpansion` through the same merge helper once it exists.

The phrase “once it exists” sounds potentially deferred. If `RetryRelatedExpansion` still replaces the initial pack, the same destructive-evidence bug can survive through that code path.

**Recommendation**

Do not ship with any retry/expansion path that can replace anchored initial evidence. Add a test that fails if `RetryRelatedExpansion` drops initial anchor rows.

---

## P1. Concept role classification needs deterministic precedence rules

The brief defines roles but does not fully specify how roles are assigned, especially after planner merge.

Ambiguous examples:

- `synthesis essays`
- quoted `"synthesis essays"`
- `tag:synthesis`
- `analysis of Analysis essays`
- `Overview Effect`
- `Go`, `AI`, `S3`
- `X` as platform vs. letter

**Recommendation**

Centralize role assignment and define precedence:

1. source key / exact source reference
2. protected entity/author/tag alias
3. quoted exact phrase
4. known tag/source collection
5. content
6. intent
7. frame

Rules should include:

- Do not demote quoted exact phrases merely because they contain an intent word.
- If no stronger anchor/content exists, terms like `synthesis` may remain content.
- After planner merge, rerun role assignment/demotion and assert invariants:
  - protected anchors remain present;
  - intent/frame concepts are not `Required:true` when stronger anchor/content concepts exist.

Add a test where planner returns `Required:true` for `synthesize` alongside `@Kristof_Poland`; post-merge concepts must demote `synthesize`.

---

## P1. Concept caps and anchor caps are ambiguous

The brief says anchors must be prepended before caps like `maxPlannerConcepts`. It does not say whether anchors count against the cap, whether there is a separate anchor cap, or where the cap is applied.

**Recommendation**

Make the contract explicit:

- Protected anchors should not be truncated by frame/intent concepts.
- Anchors may need a separate safety cap to avoid unbounded query growth.
- Final concept caps should be applied after planner merge and final role demotion, with anchors preserved first.

Add a test where a verbose composed question/planner output produces many frame concepts; final plan must still include all protected anchors or the documented capped subset.

---

# Web Chat edge cases

## P1. Prior evidence titles can still contaminate non-anchor stages

Even if anchors are extracted from `RawQuestion`, prior evidence titles in the composed `Question` can contaminate:

- planner concepts
- raw query terms
- `MissingTerms`
- retry variants
- scorer behavior

**Recommendation**

Add a web regression where prior evidence titles contain misleading terms such as `synthesis essays`, while the current user asks about `@Kristof_Poland`. Assert:

- protected anchors come only from current raw user question or typed continuity anchors;
- generic prior-title terms do not become required concepts;
- retry query is not just `synthesize` / `synthesis essays`;
- final answer context contains Kristof rows.

---

## P1. Legitimate contextual follow-ups need typed anchors

The plan says not to extract anchors from composed retrieval questions. That is correct, but follow-ups can legitimately refer to prior evidence:

```text
Turn 1: Find tweets from @Kristof_Poland.
Turn 2: Synthesize those.
```

**Recommendation**

Carry forward explicitly resolved prior-turn anchors, not arbitrary title text. Trace the source of each anchor:

```json
{
  "protected_anchors": [
    {
      "canonical": "x-author:kristof_poland",
      "raw": "@Kristof_Poland",
      "source": "prior_turn_entity"
    }
  ]
}
```

---

## P2. Streaming/cancellation/in-flight chat state should be tested

Web Chat can have overlapping requests, cancellation, or stale continuity. A stale `RawQuestion`/continuity object from a previous in-flight message could attach the wrong anchor.

**Recommendation**

Add a lightweight web/server contract test for:

- cancellation;
- second message sent before first finishes;
- continuity from another conversation/session;
- retry after browser refresh.

---

# Tests and evals that should be required

## P0. Add a deterministic `researchrun.Run`-level regression for the actual bug

The brief correctly notes that current `internal/researcheval` evaluates `brainresearch.Build`, not the full runner path. The bug is in the interaction of build → judge → retry → replacement/merge → answer context.

**Recommendation**

Add a runner-level test that fails on current main and passes after the fix.

Minimum scenario:

1. Query: `synthesize tweets/essays from @Kristof_Poland`.
2. Initial pack contains 10 X rows authored by `@Kristof_Poland`.
3. Scorer/judge raw missing terms include `synthesize`.
4. Old focused retry would query only `synthesize` or `synthesis essays`.
5. Retry pack contains generic synthesis/second-brain rows.
6. Final merged pack preserves initial Kristof rows.
7. Final answer context includes Kristof rows.
8. Retry rows do not dominate unless they also match anchor/content criteria.

This should be fixture-backed and hermetic, not dependent on production search.

---

## P0. Replay the two named traces

The two traces are the clearest reproduction artifacts:

- `20260704T174350.016236000Z-65ae1fd81bd8`
- `20260704T174159.767967000Z-180306535a5a`

**Recommendation**

Create a trace-replay fixture or simplified JSON replay harness that captures:

- raw user question
- composed web question
- initial pack
- initial query plan
- judge result
- retry query
- retry pack
- merged pack
- final answer-context selection

Assertions:

- protected anchor resolves to `x-author:kristof_poland`;
- `synthesize` / `synthesis` does not trigger destructive retry;
- initial Kristof rows are preserved;
- generic synthesis rows do not replace anchored evidence;
- final answer context includes anchored rows.

Version the trace schema so old traces remain readable.

---

## P1. Add `MergeRetryPack` unit tests

Minimum table:

| Scenario | Expected result |
|---|---|
| Anchored initial rows + generic retry rows | Keep initial; discard or rank retry below |
| Anchored initial rows + same-anchor retry rows filling content | Merge both; preserve initial anchored rows |
| No-anchor weak initial + retry fills genuine content | Accept retry rows; may rank ahead of off-topic initial rows |
| Duplicate row appears in both packs | Deduped by canonical source identity; stable metadata |
| Retry row matches only `poland` expansion token | Does **not** count as protected anchor |
| Initial exact-tag evidence + generic retry | Preserve exact-tag evidence |
| Related expansion from anchored source | Accepted through same merge helper |
| Nil `Retrieval` row | No panic; no fake missing terms |
| `Limit` smaller than initial direct rows | Anchored initial rows not evicted by retry |

---

## P1. Add no-anchor and ambiguous-intent evals

The plan explicitly says not to globally delete intent words because `synthesis essays` can be a real topic.

Add cases:

- `synthesis essays` with no anchor retrieves synthesis/essay rows.
- quoted `"synthesis essays"` remains content.
- `tag:synthesis` is tag/content, not output intent.
- `synthesize tweets from @Kristof_Poland` treats `synthesize` as intent, `tweets` as content/source-type, and `@Kristof_Poland` as anchor.
- `overview of @Kristof_Poland` does not retry only on `overview`.

---

## P1. Add web Chat contract tests

Tests should cover:

- raw user question sent separately from composed retrieval question;
- current turn with new handle;
- follow-up turn relying on prior entity;
- prior evidence titles containing another handle;
- prior evidence titles containing generic terms like `synthesis essays`;
- frontend display/context limits do not hide all anchored evidence.

---

# CI-safety and determinism gaps

## P1. Required tests must be hermetic

Runner-level evals must not require:

- production search;
- live MCP;
- network access;
- live LLM planner output;
- current corpus state.

**Recommendation**

Use fake/search fixture components for CI. Optional live-corpus integration evals can exist, but they should be non-blocking.

---

## P1. Planner/model nondeterminism must be isolated

Because planner merge can reintroduce `Required:true`, CI must not rely on live model behavior to expose this.

**Recommendation**

Inject deterministic planner outputs in tests. Separately table-test role reapplication after planner merge.

---

## P2. Regex performance and input bounding

Anchor extraction may run on long chat continuity/composed text if plumbing is wrong, and underscore/handle regexes will see pasted logs/code.

**Recommendation**

- Compile regexes once.
- Bound extraction input length.
- Add benchmarks/fuzz tests for large pasted inputs.
- Test emails, markdown links, code blocks, URLs, and punctuation.

---

# Extraction and parsing hardening

## P1. Underscore alias extraction will false-positive on code/log identifiers

The regex:

```regex
\b[A-Za-z][A-Za-z0-9]+_[A-Za-z][A-Za-z0-9_]+\b
```

will match:

```text
user_id
created_at
max_retries
test_case
feed_entry
```

If these become protected anchors, ordinary technical queries can be corrupted.

**Recommendation**

Only promote underscore aliases to protected anchors if at least one is true:

- entity map resolves it;
- it is quoted;
- it matches known author/tag/source alias patterns;
- surrounding text suggests a platform/entity handle;
- it is an exact known tag alias.

Otherwise treat it as content, not a protected anchor.

Add false-positive fixtures from code snippets, logs, SQL, JSON, and Go identifiers.

---

## P1. Source-key grammar and collision policy need to be centralized

The brief says “source keys such as `x:<digits>`, `src:<hex>`, `feed-entry:<id>`, etc.” That needs a real grammar.

**Recommendation**

Implement source-key parsing in one tested package. Include cases for:

- `x:123`
- `src:abcdef`
- `feed-entry:...`
- quoted source keys
- punctuation-adjacent keys
- markdown links
- URLs containing similar substrings
- code blocks
- timestamps/hex strings that should not match

Also decide whether source-key anchors are hard filters, boosts, or both.

---

## P2. Handle extraction edge cases need tests

Test at least:

- `[@Kristof_Poland](...)`
- `(@Kristof_Poland)`
- `@Kristof_Poland's`
- `https://x.com/Kristof_Poland`
- `x.com/@Kristof_Poland`
- `bob@example.com`
- `name+tag@example.co`
- unicode-adjacent characters
- handles longer than 32 chars
- one-character handles if any supported corpus allows them

---

# Trace observability gaps

## P1. Trace fields need to explain judge/retry/merge decisions

The brief says anchors should be in `QueryPlan`, but future debugging needs more than that.

**Recommendation**

Add trace fields like:

```json
{
  "raw_question_used_for_anchors": "...",
  "composed_question": "...",
  "protected_anchors": [],
  "concepts_with_roles": [],
  "missing_terms_raw": [],
  "missing_terms_unmapped": [],
  "missing_terms_dropped_as_intent_or_frame": [],
  "missing_concepts_for_retry": [],
  "retry_query": "...",
  "retry_reason": "...",
  "merge_decision": {
    "accepted_retry_rows": [],
    "discarded_retry_rows": [],
    "preserved_initial_rows": [],
    "dedupe_keys": []
  },
  "answer_context_anchor_rows": []
}
```

This is especially important because the intended fix relies on filtering noisy missing terms rather than removing them at source.

---

# Bottom-line required additions before shipping

The visible plan is directionally strong, but the fix can still fail unless these are nailed down:

1. **Structured metadata anchor matching** for author/entity/source fields.
2. **Canonical entity-map integration** for protected anchors.
3. **Clear web data contract** separating current raw user question, answer question, and retrieval context.
4. **Typed continuity anchors** for multi-turn follow-ups.
5. **Anchor term tiers** so `poland` does not satisfy `@Kristof_Poland`.
6. **Post-merge answer-context preservation**, not just pack preservation.
7. **Deterministic `researchrun.Run` regression** reproducing the destructive retry.
8. **Trace replay** for the two named failing traces.
9. **Hermetic CI** with fake search/planner/judge, no production dependencies.
10. **Precise merge contracts** for dedupe, ordering, limits, exact-tag evidence, and related expansion.