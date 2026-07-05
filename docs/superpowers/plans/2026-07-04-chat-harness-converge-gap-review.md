## Caveat

The supplied document is truncated mid-way through §2 (`Extend QueryConcept with ... Role stri...`). If later sections already specify runner merge semantics, judge behavior, evals, or rollout, treat the relevant findings below as verification checkpoints. Based on the visible plan, the biggest gaps are still around the **actual retry replacement fix**, **anchor semantics**, **runner-level tests**, and **web-chat continuity**.

---

# P0 — Gaps that can make the fix fail

## 1. Retry replacement is diagnosed but not concretely fixed

**Gap:**  
The plan correctly identifies `pack = retryPack` as destructive, but the visible proposal does not define the replacement/merge algorithm. “Supplement, don’t replace” is not enough. A naïve union, rerank, then truncate can still evict the original Kristof rows and reproduce the bug.

**Failure mode:**  
Initial pack has 10 `@Kristof_Poland` rows. Retry pack has 10 generic “synthesis” rows. Final pack is unioned, reranked, capped, and the anchored rows fall out.

**Recommendation:**  
Define a monotonic preservation invariant in `researchrun.Run`:

- If the initial pack contains rows matching a protected anchor, those rows are pinned/preserved.
- Retry rows may fill remaining capacity but must not compete away pinned rows.
- Deduplicate by stable source key / row ID.
- Define behavior under row-count and token-budget caps.
- Trace which rows were pinned, added, deduped, or evicted.

Example invariant:

```go
// If initialPack contains anchor-matching evidence,
// finalPack must preserve the pinned anchor rows before adding retry rows.
finalPack := MergeRetryPack(initialPack, retryPack, protectedAnchors, PackCap{
    MaxRows:   opts.MaxRows,
    MaxTokens: opts.MaxEvidenceTokens,
})
```

Add tests asserting:

- Good initial anchored pack + bad retry pack => final pack still contains anchored rows.
- Merged pack over cap => anchored rows survive truncation.
- Retry pack duplicates initial rows => no duplicates.
- No anchor matches in initial pack => retry may replace/augment according to current weak-pack behavior.

---

## 2. Judge must evaluate anchors over the pack and metadata, not only top-row text

**Gap:**  
The document notes that `judge.go` checks missing concepts on only the top direct evidence row. The visible fix does not clearly require changing that. It also does not specify that anchor matching should use structured metadata.

**Failure mode:**  
A tweet authored by `@Kristof_Poland` may not contain the literal string `Kristof_Poland` in the tweet body. If the judge checks only body text, it may still mark a good pack weak.

**Recommendation:**  
Change judge logic to evaluate protected anchors across the full pack or top-K evidence rows, using structured fields first:

- source key
- author handle
- author entity ID, e.g. `x-author:kristof_poland`
- source collection ID
- tags
- entity aliases
- canonical source metadata

Only fall back to body/title text when structured metadata is unavailable.

Add explicit rules:

- Missing `intent` or `frame` concepts must not make an anchor-matching pack weak.
- Anchor satisfaction should be evaluated per protected anchor, not only as “any anchor matched.”
- A concept should not be considered missing if it is satisfied by metadata.

Tests needed:

- Authored tweet body lacks handle, metadata has `author = x-author:kristof_poland` => judged strong.
- Multiple anchored rows appear below rank 1 => judged strong.
- Top row is generic but top-K contains anchored evidence => retry not triggered solely due to top-row miss.

---

## 3. Focused retry generation must carry anchors forward or be suppressed

**Gap:**  
The plan says focused retry must carry anchors forward, but does not specify the required `runRetry` / `RetryFocusedVariant` behavior. The current bad path rebuilt from only `judge.RetryVariant`, producing `synthesize` or `synthesis essays`.

**Failure mode:**  
Even with anchor extraction, retry query remains generic if `runRetry` still uses only missing concepts.

**Recommendation:**  
Define retry construction explicitly:

- If protected anchors exist and retry is still needed, retry query must be `anchors + missing content`, never missing intent/frame alone.
- If the only missing concepts are `intent` or `frame`, suppress retry.
- If initial pack has anchor matches, retry should be supplement-only.
- If initial pack has zero anchor matches, retry should still carry the anchor to improve recall.

Example rule:

```text
if protectedAnchors != empty:
    if missingConcepts only contain intent/frame:
        skip retry
    else:
        retryQuery = anchorTerms + missingContentTerms
else:
    retryQuery = current focused retry behavior
```

Trace both:

- original retry variant
- anchor-augmented retry variant
- retry decision: `skip`, `supplement`, `replace`, `augment`

Add a regression test asserting the Kristof retry variant is not just `synthesize`.

---

## 4. Runner-level evals are not specified concretely enough

**Gap:**  
The document correctly says `researcheval/run.go` evaluates `brainresearch.Build`, not the full bounded runner. But the visible plan does not define a concrete runner eval harness, golden fixtures, or CI gate.

**Failure mode:**  
The exact web Chat failure recurs while pack-builder evals still pass.

**Recommendation:**  
Add a runner-level eval or test path that exercises:

```text
question -> initial pack -> judge -> retry -> merge/replacement decision -> final pack -> synthesis evidence
```

Minimum required tests:

1. Golden or synthetic reproduction of trace  
   `20260704T174350.016236000Z-65ae1fd81bd8`

2. Golden or synthetic reproduction of trace  
   `20260704T174159.767967000Z-180306535a5a`

Assertions:

- Protected anchors are extracted.
- Initial pack contains expected Kristof rows.
- Retry query is not generic-only.
- Final pack contains `x:` rows authored by `@Kristof_Poland`.
- Generic “synthesis” source rows do not replace the anchored rows.

Suggested test names:

```go
TestBoundedRunnerPreservesAnchorsOnFocusedRetry
TestFocusedRetryCarriesProtectedAnchors
TestGoodInitialPackBadRetryDoesNotReplaceEvidence
TestTraceReplayKristofPolandRegression
```

Use hermetic fake search/entity-map fixtures in CI, not live production search.

---

## 5. Plain named entities are under-covered

**Gap:**  
The extraction rules focus on handles, underscore aliases, source keys, quoted identifiers, and tag aliases. But the stated goal includes “named entity, author, handle, tag, or source collection.”

Queries may not contain `@`, `_`, or quoted text:

- “Synthesize Kristof Poland’s tweets.”
- “What has Kristof Poland been saying?”
- “Summarize posts by Vitalik Buterin.”
- “Synthesize the Tyler Cowen collection.”

**Failure mode:**  
The fix works for `@Kristof_Poland` and `Kristof_Poland`, but not for ordinary display names or collection names.

**Recommendation:**  
Add a deterministic alias/collection lookup pass before generic normalization:

- exact entity display names
- known author aliases
- source collection names/slugs
- tag names and aliases
- token-boundary matching to avoid broad false positives

Do not rely solely on regex-style handles/underscore aliases.

Add tests for display-name-only and collection-name-only queries.

---

## 6. Web Chat follow-ups can lose anchors if only the current raw question is used

**Gap:**  
The plan prefers `RawQuestion` over composed chat retrieval text. That is good for avoiding injected-context pollution, but insufficient for multi-turn chat.

Example:

```text
User: Find tweets from @Kristof_Poland.
User: Can you synthesize them?
```

The second raw question has no anchor.

**Failure mode:**  
Anchor extraction returns empty for the synthesis turn, and the runner searches for generic `synthesize them`.

**Recommendation:**  
For web Chat, extract anchors from a controlled continuity source:

1. Current raw user question.
2. Prior resolved protected anchors / prior selected sources.
3. Current turn continuity subject if available.

Define explicit behavior:

- If current turn has no new anchor and uses pronouns like `them`, reuse prior anchors.
- If current turn introduces a new anchor, decide whether it replaces or adds to prior anchors.
- If topic shifts, do not keep stale anchors indefinitely.
- Never extract generic anchors from system/composed/injected retrieval text unless they are structured source keys or previously resolved continuity anchors.

Add multi-turn tests:

```text
Find tweets from @Kristof_Poland. -> Synthesize them.
Now compare those to @OtherHandle.
No, I meant @Kristof_Poland, not Poland the country.
```

---

# P1 — High-risk implementation ambiguities

## 7. Anchor representation needs typed identity, relation, and confidence

**Gap:**  
The proposed `ProtectedAnchor` has `Kind`, `Raw`, `Canonical`, and `Terms`, but that is not enough to distinguish author, mention, tag, collection, source key, or entity identity.

**Failure mode:**  
The system may treat documents mentioning `@Kristof_Poland` as equivalent to documents authored by `@Kristof_Poland`, or confuse a tag/collection/entity with the same text.

**Recommendation:**  
Extend anchors with resolved identity and relation:

```go
type ProtectedAnchor struct {
    Kind       string   // handle, source_key, tag, entity_alias, collection, quoted_text
    Raw        string
    Canonical  string
    Terms      []string // search expansion only

    ResolvedID string // x-author:kristof_poland, tag:..., collection:...
    Relation   string // authored_by, about, mentions, source_key, collection, tag, unknown
    Confidence string // exact, alias, inferred, fallback
}
```

Infer relation from local syntax:

- `from @X`, `by @X`, `@X's` => likely `authored_by`
- `about @X`, `mentioning @X`, `replies to @X` => `about`/`mentions`
- `in collection`, `source collection` => `collection`
- `tagged`, `#tag` => `tag`

Add tests for `from @X` versus `about @X`.

---

## 8. Search-expansion terms and anchor-satisfaction terms must be separated

**Gap:**  
The example anchor terms include `kristof` and `poland`. These may be useful for broad retrieval, but they are unsafe for judge satisfaction.

**Failure mode:**  
A document about Poland or another Kristof falsely satisfies the `Kristof_Poland` anchor.

**Recommendation:**  
Separate:

- `SearchTerms`: broad variants used to retrieve candidates.
- `SatisfactionTerms` / `ResolvedIDs`: exact aliases and metadata IDs used to prove anchor match.

Do not allow bare components like `kristof` or `poland` to satisfy a handle/entity anchor unless resolved through metadata/entity map.

---

## 9. Planner merge precedence can undo the fix

**Gap:**  
The document notes `planner_merge.go` can merge planner concepts back into deterministic concepts and preserve requiredness. If not constrained, planner output could re-promote `synthesize` or `summary` to required.

**Failure mode:**  
Deterministic classifier marks `synthesize` as `intent`/optional; planner merge restores it as required; judge retries on generic intent again.

**Recommendation:**  
Define merge precedence:

1. Protected anchors cannot be demoted.
2. Intent/frame concepts cannot become required solely because planner output says so.
3. Planner may add content concepts but cannot override protected role classification.
4. Requiredness conflicts should be traced.

Add unit tests for deterministic/planner conflicts.

---

## 10. Unknown-term requiredness policy is not fully specified

**Gap:**  
The evidence identifies `strategy_concepts.go` turning any unknown term length ≥3 into a required concept. The visible fix proposes roles but does not clearly state the new default.

**Failure mode:**  
Terms like `they`, `come`, `essays`, or new synthesis verbs continue becoming required and triggering bad retries.

**Recommendation:**  
Invert or narrow the default:

- `anchor`: required.
- high-confidence `content`: possibly required.
- `intent`/`frame`: optional.
- unknown/unclassified: optional unless there is strong evidence it is topical content.

Run broad before/after evals because this affects all retrieval, not only entity synthesis.

---

## 11. Intent/frame classification can overcorrect topical queries

**Gap:**  
Words like `synthesis`, `summary`, `analysis`, and `essay` can be request intent, but they can also be content.

Examples:

- “Find documents about synthesis essays.”
- “Summary judgment cases.”
- “Analysis of variance.”
- “The Synthesis Essay.”

**Failure mode:**  
The fix suppresses legitimate topical retrieval.

**Recommendation:**  
Make classification contextual:

- With a protected anchor and command phrasing, treat as intent/output-shape.
- In quotes, title context, or after `about`, allow content role.
- Without a protected anchor, terms like `synthesis essays` may need to remain content-bearing.

Add negative regression tests for unanchored topical queries.

---

## 12. Source collections and hashtags are in scope but not concretely handled

**Gap:**  
The scope includes source collections and tags. The visible extraction rules mention source keys and tag aliases but do not explicitly handle:

- source collection IDs
- source collection names
- collection aliases/slugs
- raw hashtags

**Failure mode:**  
Queries like “Synthesize the Poland OSINT source collection” or “Summarize #kristof-poland” still degrade into generic content terms.

**Recommendation:**  
Add first-class extraction for:

```text
collection IDs/slugs/names
#[A-Za-z0-9_][A-Za-z0-9_-]*
```

Map hashtags to canonical tag aliases and exact tags before normalization.

Add tests for:

- `#Kristof_Poland`
- `#kristof-poland`
- collection names with spaces
- collection slugs
- collection owner/source metadata

---

## 13. Entity-map enrichment needs a bounded resolver interface

**Gap:**  
The plan says “if entity-map lookup is cheap enough.” That is too vague for a core web Chat path and CI.

**Failure mode:**  
Build latency increases, tests become flaky, or lookup failure drops anchors.

**Recommendation:**  
Define an explicit resolver abstraction:

```go
type AnchorResolver interface {
    ResolveAnchors(ctx context.Context, anchors []ProtectedAnchor) ([]ProtectedAnchor, error)
}
```

Requirements:

- bounded timeout
- cacheable
- deterministic fake implementation for tests
- failure degrades gracefully to raw anchors
- anchors are never dropped because resolution fails
- no live production dependency in CI

Trace resolver status: resolved, unresolved, timeout, error.

---

## 14. `RawQuestion` plumbing and provenance need verification

**Gap:**  
The plan says to set `RawQuestion` from `researchrun.synthesisQuestion()`, but it does not prove that this is truly raw/pre-normalization user text.

**Failure mode:**  
If `synthesisQuestion()` already composes, cleans, or normalizes the question, handles and underscores may already be corrupted.

**Recommendation:**  

- Document exactly what `synthesisQuestion()` returns.
- Add a unit test that raw `@Kristof_Poland` and `Kristof_Poland` survive unchanged.
- Define fallback:

```go
anchorText := opts.RawQuestion
if strings.TrimSpace(anchorText) == "" {
    anchorText = opts.Question
}
```

- Trace both raw and composed question carefully, subject to privacy/redaction policies.

---

## 15. Regex extraction edge cases are under-tested

**Gap:**  
The proposed regexes need precise boundary, punctuation, and false-positive handling.

Risk cases:

- `@Kristof_Poland,`
- `@Kristof_Poland.`
- `@Kristof_Poland's`
- `bob@example.com`
- markdown link: `[@Kristof_Poland](...)`
- URL with `@`
- `foo_bar`, `feature_flag`, `snake_case`
- `v1_0`
- non-ASCII names
- X handles may have different actual max lengths than 32

**Recommendation:**  
Add table-driven tests for `extractProtectedAnchors`.

Also explicitly decide whether non-ASCII display names are in scope or documented as a limitation.

---

## 16. `conceptTermAliases` exclusion should be structural, not prose-only

**Gap:**  
The plan says not to send protected anchors through `conceptTermAliases`, but if anchors are represented as generic strings, a future refactor may violate this.

**Failure mode:**  
`Kristof_Poland` could become malformed aliases like `kristof_polands`.

**Recommendation:**  

- Keep anchors as a distinct typed path.
- Do not pass `ProtectedAnchor` through generic concept alias expansion.
- Add a test asserting protected anchor variants are not pluralized/stemmed/sanitized by generic concept alias logic.

---

## 17. `buildResearchStrategy` wrapper divergence risk

**Gap:**  
The plan offers two options: preserve the old helper as wrapper or update direct test call sites. Leaving this as optional can create production/test divergence.

**Failure mode:**  
Existing tests use a no-anchor helper and pass, while production path uses anchor-aware strategy and remains under-tested.

**Recommendation:**  
Make one deliberate decision:

- Either update all call sites to pass `protectedAnchors`, even if empty.
- Or restrict the old wrapper to tests with an explicit name like `buildResearchStrategyNoAnchorsForTest`.

Avoid silent divergence.

---

# P1 — Missing tests, evals, and CI safety

## 18. Need hermetic CI fixtures, not live production dependencies

**Gap:**  
The evidence references production search, MCP/core pack calls, and entity-map results. Tests depending on those will be flaky.

**Recommendation:**  

- Use synthetic fixture corpora that reproduce the failure shape.
- Use fake search, fake entity map, fake collection resolver.
- Keep production trace IDs as documentation.
- Gate any live integration test behind an explicit env var.

Example CI-safe fixture should include:

- anchored X rows authored by `x-author:kristof_poland`
- generic documents containing `synthesize`, `summary`, `essays`
- judge path that produces a bad focused retry

---

## 19. Add broad regression evals for requiredness and intent changes

**Gap:**  
Changing unknown-term requiredness, concept roles, and judge aggregation can affect all research queries.

**Recommendation:**  

- Run full existing research eval suite before/after.
- Add non-regression thresholds.
- Include anchored and unanchored cases.
- Include topical uses of “synthesis”, “summary”, “analysis”, and “essay.”
- Include cases where retry genuinely improves a weak initial pack.

---

## 20. Trace/replay observability needs decision-level fields

**Gap:**  
Adding `ProtectedAnchors` to `QueryPlan` is useful but insufficient. The failure required understanding judge and retry decisions.

**Recommendation:**  
Trace:

- raw question used for extraction
- composed retrieval question
- extracted anchors
- resolved anchors
- concept roles
- requiredness decisions
- planner merge conflicts
- anchor matches per row/top-K
- judge outcome and missing concepts
- retry suppression/allow reason
- original retry variant
- anchor-augmented retry query
- merge decision: replace/supplement/skip
- pinned rows, added rows, evicted rows

Keep trace schema backward-compatible:

- new fields optional
- old traces replay with empty/nil anchor fields
- trace round-trip test

---

## 21. Privacy/logging implications are missing

**Gap:**  
Raw questions and anchors may include private source keys, handles, collection names, quoted text, or sensitive identifiers.

**Recommendation:**  

- Confirm trace retention/redaction policy.
- Avoid logging full raw questions in public CI artifacts.
- Prefer synthetic fixtures for checked-in traces.
- Consider redacting or hashing sensitive anchor values in debug output where appropriate.

---

# P2 — Web Chat and semantic edge cases

## 22. Multi-anchor queries need explicit behavior

**Gap:**  
The plan appears single-anchor oriented.

Examples:

- “Compare tweets from @A and @B.”
- “Synthesize posts by Kristof Poland and Jane Doe.”
- “Summarize sources x:123 and x:456.”

**Recommendation:**  

- Preserve evidence for each anchor, not merely any anchor.
- Define per-anchor quotas or coverage requirements.
- Judge should report which anchors are covered/missing.
- Tests should fail if only one side of a compare query is represented.

---

## 23. Negation and exclusion can invert anchor meaning

**Gap:**  
Protected anchors are treated as positive constraints, but users may exclude anchors.

Examples:

- “Everything except @Kristof_Poland.”
- “Tweets about Poland, not by @Kristof_Poland.”
- “Exclude src:abc.”

**Recommendation:**  

- Either explicitly mark negation/exclusion out of scope or support it.
- If supported, add `Polarity: include|exclude` to anchors.
- Do not pin excluded anchors.

---

## 24. Injected web-chat context may create spurious anchors

**Gap:**  
The plan notes web Chat sends a multi-part retrieval question. If anchor extraction runs on composed text, prior summaries or system scaffolding may introduce false handles/source keys.

**Recommendation:**  

- Extract user-supplied anchors from current raw question and resolved continuity state.
- Avoid extracting from arbitrary composed/injected retrieval text.
- Add a test where injected context includes a handle that the user did not request.

---

## 25. Quoted identifiers versus quoted content need distinction

**Gap:**  
The plan proposes exact quoted identifiers, but quoted text can be a title or topic rather than an anchor.

Examples:

- `"synthesis"` as a topic
- `"The Synthesis Essay"` as a title
- `"Kristof Poland"` as an entity

**Recommendation:**  

- Resolve quoted text against entity/tag/source/collection aliases before treating it as a hard anchor.
- If unresolved, treat it as content/title phrase, not protected anchor.
- Trace resolver confidence.

---

## 26. Rollout/blast-radius controls are missing

**Gap:**  
The changes affect core planning, judging, and retry behavior globally.

**Recommendation:**  

- Add a feature flag/config toggle for anchor-aware retry/merge during rollout.
- Monitor:
  - retry rate
  - anchored evidence preservation rate
  - no-answer rate
  - latency
  - trace replay pass rate
  - quality of unanchored synthesis/topic queries

---

# Highest-priority concrete changes

If the implementation team only addresses a small set before coding, prioritize these:

1. **Define monotonic retry merge semantics**: anchored initial rows cannot be evicted by retry results or pack caps.
2. **Make the judge anchor-aware over top-K/full pack using metadata**, not only top-row body text.
3. **Suppress or anchor-augment focused retries** so retry queries are never generic `synthesize`/`summary` only when protected anchors exist.
4. **Add runner-level regression tests** reproducing the two saved Kristof failure traces or equivalent synthetic fixtures.
5. **Handle web Chat continuity** so follow-up turns like “synthesize them” retain prior resolved anchors.
6. **Support plain named entities, source collections, tags/hashtags, and typed anchor relations**, not only handles and underscore aliases.
7. **Add trace fields for concept roles, judge decisions, retry queries, and merge outcomes** so future failures are replayable and diagnosable.