# Chat Harness Entity Synthesis Retrieval Plan

> **For agentic workers:** This is a design and implementation plan. Do not start
> coding until the plan has passed the requested Claude and Amp review loops and
> the human operator has accepted the direction.

**Goal:** Fix the web Chat/research harness so synthesis requests over a named
entity, author, handle, tag, or source collection preserve the matching evidence
instead of replacing it with generic documents that happen to contain request
verbs such as "synthesize" or "summary".

**Current failure:** The Kristof_Poland corpus is present and retrievable, but
the bounded research runner throws away good evidence after a focused retry.

**Scope:** Research/chat retrieval planning, runner judging, retry behavior,
trace/eval coverage, and documentation/changelog. No product-layer filtering,
no Kristof-specific hardcoding, and no write-back to upstream sources.

## Evidence

This is not a corpus absence problem.

- Production search for `Kristof_Poland` returns 11 X rows authored by
  `@Kristof_Poland`.
- `dbrain_entity_map("Kristof_Poland")` resolves one person entity:
  `x-author:kristof_poland`, with aliases `@Kristof_Poland`,
  `@kristof_poland`, `Kristof_Poland`, and `kristof_poland`.
- `dbrain_research_pack("Can you synthesize the Tweets from @Kristof_Poland -
  they're in the dbrain.")` currently returns useful Kristof_Poland X rows when
  called directly through the MCP/core pack path.
- The saved web Chat traces show the runner initially retrieved the right X
  rows, then replaced them with generic "synthesis" rows.

Trace `20260704T174350.016236000Z-65ae1fd81bd8`:

- Initial retrieval selected:
  `x:2064309615651668179`, `x:2072591564988182812`,
  `x:2072754203760418929`, `x:2064309619204186246`,
  `x:2065553533756182939`, `x:2071948517837353292`,
  `x:2072299557761737126`, `x:2072348839898501365`,
  `x:2073040589268848664`, `x:2071927323624956144`.
- The query plan made `synthesize`, `kristof`, `poland`, and `they` required
  concepts. The top Kristof rows did not contain "synthesize" or "they", so the
  judge marked the pack weak.
- The focused retry question became only `synthesize`.
- The retry selected generic source rows such as `src:1212afd25440`,
  `src:5eec568539ae`, and `src:bcd935c30482`.
- `researchrun.Run` replaced the good pack with the retry pack, and synthesis
  then truthfully answered from the bad final pack: no @Kristof_Poland tweets
  were present.

Trace `20260704T174159.767967000Z-180306535a5a` has the same pattern:

- Initial retrieval included Kristof_Poland X rows.
- The query plan made filler/request terms required: `come`, `synthesis`,
  `kristof`, `poland`, and `essays`.
- The focused retry question became `synthesis essays`.
- The retry selected generic source rows about synthesis essays and second
  brains, then replaced the original pack.

Code path:

- `internal/researchrun/run.go` builds the initial pack, judges it, then on
  retry assigns `pack = retryPack`.
- `internal/researchrun/run.go` `runRetry` rebuilds from only
  `judge.RetryVariant` for `RetryFocusedVariant`.
- `internal/researchrun/judge.go` checks missing concepts on only the top direct
  evidence row.
- `internal/brainresearch/strategy_concepts.go` currently turns any unknown
  term of length 3 or more into a required concept.
- `internal/queryterms/normalize.go` strips both `@` and `_`, so protected
  handles and underscore aliases must be extracted from the raw question before
  ordinary term normalization.
- `internal/brainresearch/planner_merge.go` can merge planner concepts back
  into deterministic concepts and preserve requiredness when keys overlap.
- `internal/researcheval/run.go` evaluates `brainresearch.Build`, not the full
  bounded runner. This is why `eval research diff --trace` can report a good
  current pack while the web runner failure remains possible.

Confidence: high. The traces, current core retrieval, and runner code all point
to destructive retry replacement after a bad required-concept decision.

## Design Principles

1. Preserve source-of-truth evidence before improving recall.
   - If an initial pack contains rows anchored to the requested entity, author,
     handle, source key, or exact tag, a retry may supplement those rows but must
     not blindly replace them.

2. Separate answer intent from evidence constraints.
   - "Synthesize", "summarize", "overview", "analysis", "explain", and similar
     terms often describe what the user wants the model to do, not what evidence
     must contain.
   - Those terms may be useful as optional signals, but they must not make an
     authored item look weak when the item matches the requested entity.

3. Protect explicit anchors.
   - Handles, exact tag aliases, source keys, author names, and known entity
     aliases are hard retrieval anchors. A focused retry must carry them forward.

4. Keep the runner bounded.
   - The fix should keep the current maximum-one-retry shape unless a separate
     design revisits bounded research loops.

5. Make the failure reproducible outside the browser.
   - Add runner-level eval/test coverage. Pack-builder eval coverage alone is
     insufficient for this class of bug.

## Proposed Fix

### 1. Extract Protected Anchors Before Normalization

Add a raw-question anchor extraction pass before `queryterms.Terms`,
`normalizeQuestionText`, or planner sanitization runs. This is mandatory because
the current normalizer turns `Kristof_Poland` into `kristof poland` and removes
the `@` from `@Kristof_Poland`.

Anchor extraction should produce a stable list of protected anchor objects, not
just extra strings in the generic term list.

Suggested fields:

```go
type ProtectedAnchor struct {
    Kind      string // handle, source_key, tag_alias, entity_alias, collection, quoted_text
    Relation  string // authored_by, about, mentions, source_key, collection, tag, unknown
    Raw       string // @Kristof_Poland
    Canonical string // kristof_poland
    ResolvedID string // x-author:kristof_poland, tag:..., collection:...
    Source    string // current_user_text, prior_turn_entity, selected_citation, fallback_question
    Confidence string // exact, alias, inferred, fallback

    // Exact/Phrase terms may satisfy the protected anchor. Expansion terms may
    // help retrieval recall, but must not prove that a row matches the anchor.
    ExactTerms     []string // @Kristof_Poland, Kristof_Poland, kristof_poland
    PhraseTerms    []string // kristof poland, kristof-poland
    ExpansionTerms []string // kristof, poland
}
```

Initial extraction rules:

- X/social handles: `(^|[^A-Za-z0-9_])@[A-Za-z0-9_]{2,32}`. Preserve only the
  handle token without the left-boundary character. The left boundary avoids
  false positives inside email addresses such as `bob@example.com`.
- Underscore aliases that look like names or handles:
  `\b[A-Za-z][A-Za-z0-9]+_[A-Za-z][A-Za-z0-9_]+\b`.
  Promote these to protected anchors only when quoted, entity-resolved, matched
  to a known author/tag/source alias, or strongly indicated by surrounding
  handle/entity language. Ordinary code/log identifiers such as `user_id` and
  `created_at` must remain content terms.
- Source keys: existing source key patterns such as `x:<digits>`,
  `src:<hex>`, `feed-entry:<id>`, and other known dbrain source-key prefixes.
  Centralize this grammar in one tested parser so punctuation-adjacent keys,
  markdown links, URLs, code blocks, and hex/timestamp lookalikes do not drift
  across packages.
- Exact quoted identifiers where the quoted value contains a source key, handle,
  underscore alias, or tag-like phrase.
- Existing exact tag aliases, including dash-normalized forms such as
  `kristof-poland`.
- Hashtags and collection aliases/slugs when they resolve to known tags or
  source collections, for example `#Kristof_Poland`, `#kristof-poland`, or a
  named source collection. Unresolved quoted values should remain phrase/content
  terms rather than hard anchors.

Important implementation details:

- Do not send protected anchors through `conceptTermAliases`; proper names and
  handles must not become `kristof_polands`.
- The anchor pass must run in `Builder.Build` before `ask.SearchText(question)`
  is called, but it must use the raw user question rather than the composed chat
  retrieval question when both are available. Web Chat currently sends a
  multi-part retrieval question as `Question`; the raw user text is already
  available to the runner via the current turn and synthesis path. Add
  `RawQuestion` or `CurrentTurnQuestion` to `brainresearch.Options`, set it from
  the latest user utterance for runner calls, and prefer it for new anchor
  extraction. Fall back to `Question` only for CLI/MCP/API callers that do not
  supply a separate raw user question.
- Do not use `ChatContinuity.OriginalQuestion` as the only raw-question source.
  In a multi-turn chat, the first user question and the latest user utterance can
  contain different anchors. New anchors come from the current turn. Prior
  anchors may be reused only through typed continuity anchors such as
  `ContextAnchor{Canonical, Relation, Source}`, not by scraping arbitrary prior
  evidence titles or model answers.
- Keep the web/server data contract explicit:
  - `RawQuestion` / `CurrentTurnQuestion`: latest user utterance before UI
    composition; source of newly introduced anchors and answer intent.
  - `SynthesisQuestion` / `AnswerQuestion`: question the model should answer.
  - `RetrievalContext`: typed continuity, pinned evidence, and prior resolved
    anchors.
  - `Question`: composed retrieval text if still needed, but not the only source
    for concepts, anchors, or required terms.
- `buildResearchStrategy` currently receives `searchQuestion`, which has already
  passed through `ask.SearchText`/`queryterms` normalization. Do not extract
  handles or underscore aliases from that normalized value.
- Pass anchors from `Builder.Build` into strategy construction by adding a
  `protectedAnchors` parameter to the builder method or a lower-level internal
  helper. Preserve the existing package-level `buildResearchStrategy(question,
  hints)` test helper as a wrapper that calls the new helper with no protected
  anchors, or update all direct test call sites deliberately. Do not let the
  package-level helper silently diverge from the builder path.
- Resolve anchors through the same canonical entity map used by search when it is
  available. The Kristof case should become `raw handle/alias ->
  x-author:kristof_poland -> aliases -> structured match predicates`. Do not
  leave entity-map integration as an optional "if cheap enough" detail.
- Implement resolution behind a bounded interface such as
  `AnchorResolver.ResolveAnchors(ctx, anchors)`, with a timeout, cacheability,
  deterministic fake implementation for tests, and graceful degradation. If
  resolution fails, keep raw anchors; never drop anchors because the resolver
  timed out or errored.
- Add protected anchors to `QueryPlan`, or add a backward-compatible trace field
  if changing the JSON shape is too broad. Prefer `QueryPlan.ProtectedAnchors`
  if the API compatibility risk is acceptable, because `researchrun.Judge` needs
  this information.

### 2. Add Intent-Aware Query Concepts

Add a small, explicit classification layer in `internal/brainresearch` for
query terms:

- `anchor`: source keys, handles, exact tags, author/entity aliases, quoted
  exact-ish identifiers, and high-confidence person/org/project names.
- `content`: topical terms that evidence should actually contain.
- `intent`: request verbs and output-shape words such as `synthesize`,
  `synthesis`, `summarize`, `summary`, `overview`, `analysis`, `explain`,
  `answer`, `brief`, `themes`, and `recap`.
- `frame`: corpus/UI scaffolding and conversational filler such as `dbrain`,
  `brain`, `current`, `question`, `please`, `can`, `come`, `up`, `they`, `re`,
  `there`, `in`, `prior`, `evidence`, `titles`, `recent`, `user`,
  `questions`, and `focus`.

Implementation shape:

- Extend `QueryConcept` with a backward-compatible role field such as
  `Role string 'json:"role,omitempty"'`, or store the role in a parallel
  internal map and expose it in trace output. Prefer extending `QueryConcept`
  because the judge and eval layers need the classification.
- Treat intent concepts as `Required:false` when at least one anchor or content
  concept exists.
- Prepend anchor concepts ahead of content/intent/frame concepts before applying
  planner concept caps such as `maxPlannerConcepts`. Anchors must not be
  truncated away just because a verbose composed query produced many frame or
  intent concepts.
- Drop frame terms before concept creation where possible. They should not
  appear in required concepts or focused retry variants.
- Do not globally remove intent words. A query whose only meaningful topic is
  "synthesis essays" should still be able to search for synthesis essays.
- After model-planner merge, reapply concept-role policy so planner output
  cannot re-upgrade `synthesize` or `summary` into hard constraints when other
  anchors exist.
- Demote intent/frame concepts in strategy construction, but do not assume that
  this alone cleans `Retrieval.MissingTerms`. The ask retrieval scorer populates
  `Retrieval.MissingTerms` from raw query terms, not from
  `brainresearch.QueryConcept.Required`.
- Therefore use two enforcement points:
  - Query planning should keep intent/frame concepts out of preferred/focused
    variants when stronger anchors or content concepts exist.
  - The judge must filter `Retrieval.MissingTerms` through concept roles before
    deciding weak evidence or focused retry.
- Add the post-merge policy call explicitly in `Builder.buildResearchStrategy`,
  after `mergeQueryConcepts` and before `preferredConceptQuery` adds the
  `model_concept_terms` variant.

Minimum targeted examples:

- `Can you synthesize the Tweets from @Kristof_Poland - they're in the dbrain.`
  should preserve `Kristof_Poland` / `@Kristof_Poland` as an anchor and should
  not require `synthesize`, `they`, or `re`.
- `Can you come up with a synthesis of Kristof_Poland essays?` should preserve
  `Kristof_Poland` as an anchor and should not require `come` or `up`. Whether
  `essays` remains required is a policy choice; if it remains required, retry
  must still carry the Kristof anchor.
- `Find notes about synthesis essays` may still require `synthesis` and
  `essays`, because there is no stronger non-intent anchor.

### 3. Preserve Exact Anchors Through Planning

Use the pre-normalization anchor list during strategy construction and variant
generation. Do not try to reconstruct handles from normalized terms after the
underscore and at-sign have already been removed.

Use anchors in four places:

- Query variants: include anchor-only and anchor-plus-content variants before
  generic intent variants. Update `preferredConceptQuery`,
  `preferredConceptQueryExcluding`, and `focusedConceptVariants` so optional
  intent/frame concepts do not re-enter query strings when stronger anchors or
  content concepts exist.
- Scoring: rows matching a protected anchor should not be penalized for missing
  intent concepts.
- Trace output: include protected anchors and concept roles in
  `query_plan_built` or a nearby trace event so future failures are diagnosable.
- Judge/retry: pass protected anchors to `researchrun.Judge` through
  `JudgeOptions`, or include them on `Pack.QueryPlan` and have the judge read
  them from the pack. Do not leave the judge to infer anchors from raw strings.
- Merge acceptance: anchors tighten which retry rows are accepted, but they do
  not decide whether the initial pack is preserved. Initial direct evidence is
  always preserved.

Do not make this Kristof-specific. The same behavior should work for any X
handle, Apple Notes author/account alias, exact tag, source key, or stored entity
alias.

### 4. Make Judge Evaluate Missing Content, Not Missing Intent

Update `internal/researchrun/judge.go` so `MissingConcepts` only includes
concepts that are still meaningful hard evidence requirements.

Recommended behavior:

- Judge anchor satisfaction through structured metadata before row text. A row
  authored by `@Kristof_Poland` may not contain that handle in title/body text,
  but it still satisfies an authored-by anchor when metadata says
  `Author=@Kristof_Poland` or `EntityMatches`/source metadata contains
  `x-author:kristof_poland`.
- Anchor satisfaction should use the shared anchor matcher against source key,
  external platform ID, author handle, resolved entity ID, tag/collection
  metadata, canonical URL/permalink, and only then title/body/excerpt text.
  `ExpansionTerms` such as `kristof` or `poland` may boost retrieval but must not
  satisfy the protected anchor by themselves.
- Trace per-anchor support counts, not just a single boolean. A compare or
  multi-anchor query must reveal which anchors are covered and which are still
  missing.
- The judge-side role filter is required, not defensive. The retrieval layer can
  still place intent/frame terms in `Retrieval.MissingTerms` if a query variant
  contains those terms. Before judging weak evidence, map each missing term back
  to `QueryPlan.Concepts` and keep only missing `content` or `anchor` concepts.
  A missing term matches a concept if it equals `concept.Key` or any normalized
  entry in `concept.Terms`; unmapped terms such as `re` from "they're" are
  dropped for judge/retry purposes.
- Note the bounded edge case: very short content terms such as `ai`, `go`, or
  `s3` may appear in `Retrieval.MissingTerms` even though current concept
  construction drops sub-3-rune terms. Dropping unmapped terms is safe because it
  preserves the initial pack rather than destroying evidence; optional hardening
  can keep unmapped raw-question terms of length 3 or more.
- Update `focusMissingConcepts`. It currently keeps `synthesize` because
  `synthesize` appears in the original synthesis question. It must filter by
  content-role concepts, not only by whether a word appears in the user's
  question.
- If a pack contains direct rows matching a protected anchor, do not mark the
  pack weak solely because the top row lacks an output-shape term.
- For entity/topic overview queries, inspect `min(3, len(directRows))` direct
  rows rather than only the first row. The current first-row-only judge is
  brittle for collection synthesis because one valid row can miss a content term
  that another valid row covers. Aggregate missing content by intersection:
  only concepts missing from all inspected rows should drive a weak verdict or
  focused retry.
- When protected anchors exist, the inspected set should prefer
  anchor-matching rows over arbitrary first rows. If rows 1-3 are generic but
  rows 4-10 are anchored evidence, the judge must not conclude that the anchored
  pack is absent.
- If there is only exact-tag evidence and no direct rows, judge the exact-tag
  rows with the same role-filtered missing-term logic. Rows with nil
  `Retrieval` should not manufacture missing terms.
- Keep genuine weak-evidence behavior: if the user asks for `Kristof_Poland
  essays` and the corpus only has tweets, the answer may say that no essays were
  found, but it should still cite the matching Kristof_Poland rows and explain
  the type mismatch instead of replacing them with generic essay-writing docs.
- Wire the judge explicitly. Options:
  - Add `ProtectedAnchors []brainresearch.ProtectedAnchor` and concept-role
    metadata to `JudgeOptions`.
  - Or add the anchors/roles to `Pack.QueryPlan` and make `Judge` read them from
    the pack.
  The implementation must choose one. The preferred path is `Pack.QueryPlan`,
  because traces, evals, and judge decisions all need the same metadata.

### 5. Make Focused Retry Anchor-Preserving And Non-Destructive

Change `RetryFocusedVariant` from "replace pack with missing terms" to "try to
fill missing content while preserving all initial direct evidence".

Rules:

- If the only missing concepts are intent/frame concepts, skip retry.
- If there are missing content concepts and protected anchors, build the retry
  question from `anchors + missing content concepts`, not missing concepts
  alone.
- Assign ownership explicitly: `Judge` should identify missing content concepts
  and set `RetryAction`, but `researchrun.runRetry` should compose the final
  retry question from the initial pack's `QueryPlan.ProtectedAnchors` plus
  `judge.MissingConcepts`. Change the call to `runRetry(initialPack, judge)` or
  equivalent. Do not rely on `JudgeResult.RetryVariant` alone for anchored
  retries.
- `JudgeResult.RetryVariant` may remain for no-anchor focused retries, but when
  anchors exist the authoritative retry input is the initial pack's protected
  anchors plus missing content concepts.
- If there are no protected anchors, the existing focused retry query may
  continue, subject to the intent/frame filtering above, but the replacement
  behavior must not continue. Merge-not-replace is unconditional for focused
  retries.
- Route `RetryRelatedExpansion` through the same merge helper as focused retry
  once `MergeRetryPack` exists. Related expansion may add useful rows, but it
  should not discard the initial pack either.
- Do not rely on `focusedConceptVariants` to recover anchors. A retry question
  like `synthesize` or `synthesis essays` has too few required concepts for
  focused windows and no anchor to recover. The retry question itself must carry
  protected anchors.
- Retry results should be merged into the initial pack, not assigned over it.
  Preserve all initial direct evidence, but do not always pin it ahead of retry
  evidence. Ordering should be score/concept-aware:
  - anchored initial rows rank ahead of non-anchored retry rows,
  - in no-anchor cases, accepted retry rows that fill missing content may rank
    ahead of off-topic initial rows,
  - initial direct rows are never evicted from the merged pack solely because of
    `Limit`.
  Add retry rows only if they:
  - match a protected anchor,
  - fill a genuine missing content concept, or
  - are related expansions from an anchored source.
- If a retry returns rows that do not match protected anchors and do not fill
  the missing content need, discard those rows and keep the initial pack.
- Implement anchor matching in a shared helper. It should check `SourceKey`,
  external platform ID, `Author`, resolved entity IDs, collection/tag metadata,
  `URL`, canonical permalink, `UserTags`, `EntityMatches`, `Title`, `Summary`,
  and `Excerpt` against each anchor's resolved IDs, exact terms, and phrase
  terms. It may reuse the existing `researchEvidenceText(doc)` helper
  internally, but do not refer to `researchEvidenceText` as an `ask.Evidence`
  field. Source-key anchors should also allow exact source-key equality and
  known prefix matching. Never let bare expansion tokens such as `poland`
  satisfy a handle/entity anchor.
- Implement the retry merge in `internal/brainresearch`, not ad hoc inside
  `internal/researchrun`, so coverage can be rebuilt with package-local
  helpers. Preferred shape: export a small function such as
  `brainresearch.MergeRetryPack(initial Pack, retry Pack, opts MergeRetryOptions)
  (Pack, MergeRetryDecision)`. That function can reuse unexported coverage
  helpers such as `buildCoverage`/`mergeCoverage`, preserve corpus coverage
  fields, and return accepted/rejected source keys for trace events.
- `MergeRetryPack` must recompute `RecallNote` for the merged evidence with
  `recallNote(newCoverage)`. Do not inherit a stale note from the initial pack
  after changing the selected evidence rows.
- Do not accept stale `Pack.Coverage` after merging evidence. If coverage rebuild
  is deferred, the plan must explicitly mark coverage stale in the trace; the
  preferred implementation is to rebuild it.
- Merge ordering and winners:
  - `Evidence`: dedupe initial and accepted retry rows, then order by
    anchor/concept-aware score. Preserve initial direct rows, but allow accepted
    retry rows to rank ahead when they better satisfy missing content and no
    protected anchor is present. Never evict initial direct rows because of
    `Limit`. Do not naively sort initial and retry rows by `Retrieval.Score`;
    those scores were computed against different query strings. Use explicit
    anchor/content-match rules and original per-pack order as tie-breakers.
  - `ExactTagEvidence`: keep the initial pack's exact-tag evidence unless the
    retry adds exact-tag rows for the same protected anchors; append/dedupe those
    after the initial rows.
  - `Coverage`: rebuild row-derived counts from merged evidence and preserve
    corpus fields from the initial pack via `mergeCoverage`.
  - `QueryPlan`: keep the initial pack's query plan, including
    `ProtectedAnchors` and role-annotated concepts. The retry query should be
    recorded in merge trace metadata rather than replacing the plan the judge
    needs for role filtering.
  - `Topic`, `TopicBrief`, `UsedTopicBrief`, and `NextSteps`: keep the initial
    pack values unless a later accepted design says retry-specific topic
    replacement is needed.
  - Synthesis truncation may still cap text by `MaxEvidenceChars`; merged
    evidence ordering should favor anchored rows and content-satisfying retry
    rows while preserving all initial direct rows in the pack. Do not interpret
    preservation as unconditional first-position priority.
- Audit every post-merge selection boundary. If initial or exact-tag evidence
  satisfies protected anchors, at least one row per satisfied anchor must survive
  into the final synthesis context unless the configured token budget makes that
  impossible. If an anchored row is dropped before synthesis, trace the source
  key and reason such as `display_limit`, `citation_limit`, or `token_budget`.
- Add an answer-stage guard: if the final answer context contains
  protected-anchor evidence, the answer must not claim that the corpus lacks
  sources for that anchor. A deterministic/mock synthesis test or postcheck can
  enforce this without brittle prose assertions.
- Emit trace events for:
  - retry query,
  - preserved initial source keys,
  - accepted retry source keys,
  - rejected retry source keys or count,
  - final merged source keys,
  - answer-context anchor source keys and dropped-anchor reasons,
  - merge/discard reason.

This is the core product fix. A retry should improve a pack; it should not be
able to erase the corpus slice the user explicitly asked for.

### 6. Preserve Trace Artifacts Per Attempt

The saved trace currently has one `planner-input.md` and one
`planner-output.json`, and the retry overwrites the initial planner artifacts.
That made the trace harder to interpret.

Change trace artifacts to keep attempt-specific files, for example:

- `planner-initial-input.md`
- `planner-initial-output.json`
- `planner-retry-1-input.md`
- `planner-retry-1-output.json`

Keep backward compatibility in trace readers by still recognizing the old names.
Do not break existing trace JSON parsing.

The current observer interface has only:

```go
type Observer interface {
    Event(name string, data map[string]interface{})
    PlannerInput(input string)
    PlannerOutput(output string)
}
```

The plan must evolve this without breaking existing observers. Preferred shape:

- Add `Attempt string` to `brainresearch.Options`.
- `researchrun.buildPack` passes `Attempt: "initial"` for the first pack and
  `Attempt: "retry-1"` for the bounded retry.
- Add optional attempt-scoped methods behind interface assertions, for example
  `PlannerInputAttempt(attempt string, input string)` and
  `PlannerOutputAttempt(attempt string, output string)`.
- `researchtrace.Recorder` implements the new methods.
- `brainresearch.Builder` calls the attempt-aware methods when available and
  `Options.Attempt` is non-empty, then falls back to the old methods.
- Keep aggregate planner metrics populated. `researchtrace` currently reports
  planner artifact bytes, model-call counts, and planner input/output character
  counts; attempt-scoped artifacts should either sum into those aggregate fields
  or preserve the existing aggregate fields alongside per-attempt files.

Alternative: make the recorder maintain an attempt label via a `SetAttempt`
method, but this is less explicit and easier to misuse.

### 7. Keep Chat Continuity From Laundering Bad Evidence

The second screenshot shows prior bad evidence listed in chat continuity. That
is acceptable as trace context, but prior model answers and their evidence must
not become authoritative evidence for a new turn unless the user pins them or a
specific design says so.

Audit:

- `web/ui/src/lib/chat.js` has `mergeResearchPackForChat`, and
  `web/ui/src/App.svelte` imports it, but current usage needs to be verified and
  cleaned up if dead.
- The main multi-turn laundering risk is `buildChatRetrievalQuestion`, which
  includes "Prior evidence titles for query focus" for follow-up turns. If a
  previous turn produced bad generic evidence, those titles can be fed back into
  the next retrieval question and become query concepts.
- Prior evidence titles should only be included for user-pinned evidence or for
  narrow follow-up shapes where the user clearly refers back to prior evidence.
  Standalone entity/title/handle searches should not inherit prior generic
  evidence titles.
- Legitimate follow-ups still need continuity. For a sequence such as
  `Find tweets from @Kristof_Poland` followed by `Synthesize those`, carry
  forward the previously resolved `x-author:kristof_poland` as a typed
  continuity anchor with provenance `prior_turn_entity`. Do not recover that
  anchor by parsing prior model prose or arbitrary evidence-title text.
- If the current turn introduces a new protected anchor, define whether it
  replaces or adds to prior continuity anchors. The default should be: explicit
  current-turn anchors win, pronoun-only follow-ups may reuse prior anchors, and
  stale anchors do not persist indefinitely across topic shifts.
- Add an explicit JavaScript heuristic in `shouldIncludePriorExpansion` so this
  is implementable without reaching into Go internals. Minimum rule: return
  `false` when the current question matches a handle or underscore alias, using
  the same patterns as the Go anchor extractor:
  `/(^|[^A-Za-z0-9_])@[A-Za-z0-9_]{2,32}/` and
  `/\b[A-Za-z][A-Za-z0-9]+_[A-Za-z][A-Za-z0-9_]+\b/`.
- Insert this check before the current short-question shortcut
  `if (words.length <= 5) return true;`; short anchor queries are exactly where
  prior evidence title pollution is most likely.
- Consider also suppressing prior expansion for source-key patterns and exact
  tag-like standalone searches, but do not overfit this first fix. The Kristof
  failure requires handle/underscore suppression.
- Server-side chat retrieval should use the current question plus pinned user
  evidence, not prior model answer citations as facts.
- Trace output may continue to report prior evidence for debugging, but the
  retrieval builder should not treat prior bad citations as a source of truth.

### 8. Add Runner-Level Regression Tests

Add tests at the layer where the bug lives.

Pack-builder tests:

- `internal/brainresearch`: seed Kristof-like X rows plus generic source rows
  about synthesis. The pack for `Can you synthesize the Tweets from
  @Kristof_Poland - they're in the dbrain.` must rank anchored X rows ahead of
  generic synthesis rows.
- Add paired anchor extraction fixtures in Go and JS so the handle/underscore
  regexes do not drift. Include `@Kristof_Poland`, `Kristof_Poland`,
  `bob@example.com` as a non-match for handle extraction, source keys, and a
  plain title query with no anchor.
- Add false-positive fixtures for snake_case/code/log identifiers such as
  `user_id`, `created_at`, `max_retries`, SQL, JSON, and Go snippets. Underscore
  aliases become protected anchors only when quoted, entity-resolved, or matched
  to known author/tag/source aliases.
- Add display-name, hashtag, source collection, and relation fixtures:
  `Synthesize Kristof Poland's tweets`, `posts by Vitalik Buterin`,
  `#Kristof_Poland`, `#kristof-poland`, `Synthesize the Tyler Cowen collection`,
  `from @X`, and `about @X`.
- Add source-key parser fixtures for `x:<digits>`, `src:<hex>`,
  `feed-entry:<id>`, punctuation-adjacent keys, markdown links, code blocks, and
  URL substrings that should not become protected source-key anchors.
- Test the deterministic fallback with planner disabled.
- Test planner merge cannot re-require intent terms after role policy.
- Test raw-vs-composed question behavior: when `Question` contains prior
  evidence titles but `RawQuestion` is the current user text,
  protected anchors come only from the raw user text.

Judge/retry tests:

- `internal/researchrun`: construct a pack whose top row matches an anchor but
  misses `synthesize`. Judge should return `enough_evidence` or at least no
  focused retry for `synthesize`.
- Test a genuine missing content concept still triggers focused retry.
- Test focused retry query preserves anchors when retry is appropriate.
- Test merge behavior always keeps initial direct source keys when retry returns
  generic rows, including no-anchor cases where the focused retry still runs.
- Test that a row body without the handle still satisfies the anchor when row
  metadata contains `Author=@Kristof_Poland` or
  `EntityMatches=x-author:kristof_poland`.
- Test that retry rows matching only expansion terms such as `poland` do not
  count as protected-anchor matches.
- Test merged row-derived coverage is recomputed after retry merge:
  `EvidenceCount`, `ByKind`, `BySourceType`, and `TopUserTags`. `RecallNote`
  should be consistent with the merged coverage/corpus fields but is not the
  primary assertion because it mostly reflects corpus-level counts.

Trace/eval tests:

- Extend `internal/researcheval` or add a sibling runner eval mode that executes
  `researchrun.Run`, not only `brainresearch.Build`.
- Make the runner eval CI-safe. Add either:
  - a `StopAfterJudge`/`SkipSynthesis` option that stops after retrieval, judge,
    and retry merge, or
  - an injectable fake synthesis runner in `researchrun.Options`.
  The eval must not require a local/hosted model, personal credentials, or
  ambient developer config.
- Pass an explicit `TraceEnabled: &false` for runner eval cases by default, or
  route trace output to a test temp directory. Leaving `TraceEnabled` nil enables
  traces today. CI tests must not write persistent `data/research-runs`
  artifacts.
- Add a local eval case derived from
  `20260704T174350.016236000Z-65ae1fd81bd8` with expected top source keys from
  the Kristof_Poland X rows and forbidden generic synthesis-only rows.
- Be explicit about trace reconstruction. Existing trace diff/propose flows may
  reconstruct options from the saved final retry pack, not from the user's
  original question and initial evidence. A runner eval must use the trace's
  original question/chat continuity as input and assert the final runner pack.
  Do not use `OptionsFromTrace` for runner replay when it would reconstruct from
  the bad saved final pack.
- After `brainresearch.Options.RawQuestion` exists, update normal trace diff
  replay to set `RawQuestion` from `trace.ChatContinuity.OriginalQuestion` when
  available, so trace diff uses the same anchor extraction path as the runner.
- Add at least one synthetic multi-turn runner case where `Question` contains
  prior-evidence-title lines and `RawQuestion` is the current bare handle or
  underscore alias. The Kristof production trace itself is effectively turn 1,
  so it does not prove the laundering fix.
- Add a pronoun follow-up case where turn 1 resolves `@Kristof_Poland` and turn
  2 asks `Synthesize those`; the runner should use the typed prior anchor. Add a
  topic-shift case where an explicit new current-turn anchor replaces or scopes
  prior anchors.
- Keep exact final prose out of eval assertions. Assert stable retrieval, judge
  verdict/retry decisions, stop reason for stop-after-judge mode, and absence of
  generic replacement. If the eval uses fake synthesis instead, citations and
  answer status may also be asserted against the fake output.
- If the eval reaches fake synthesis, assert the answer context contains anchored
  rows and the generated answer cannot contain a false "no sources for
  Kristof_Poland" claim when anchored evidence is present.
- Add an eval or unit test proving initial and retry planner artifacts do not
  overwrite each other.
- Add web/server or API tests proving the raw user question is passed separately
  from the composed retrieval question, and that anchor extraction uses the raw
  user question for web Chat.
- Add a small test or assertion that web Chat supplies a clean raw original
  question for anchor extraction. Future callers that cannot provide
  `RawQuestion` should rely on the documented fallback to `Question`.

Do not accept the existing `eval research propose --from-trace` output blindly
for this trace. It currently reflects the bad saved final pack, so it would
encode the wrong behavior as a reviewed case.

### 9. Update Docs And Changelog

Update:

- `docs/research-harness.md`: document intent terms, protected anchors,
  non-destructive retries, and runner-level trace evals.
- `CHANGELOG.md`: add a short user-visible fix entry when implementation lands.
- If the MCP/agent research workflow changes, update the installed dbrain MCP
  skill separately in the public-safe skill repo flow.

## Implementation Order

1. Add raw-question protected anchor extraction in `Builder.Build`, using
   `brainresearch.Options.RawQuestion` when supplied and falling back to `Question`,
   before `ask.SearchText(question)` and ordinary query-term normalization. Pass
   the anchors explicitly into strategy construction. Add the bounded
   `AnchorResolver` abstraction and deterministic fake before relying on
   entity-map enrichment in tests. Preserve or deliberately update the
   package-level `buildResearchStrategy` test helper.
2. Add concept-role helpers, role metadata, and tests in `internal/brainresearch`.
3. Thread protected anchors and concept roles into `QueryPlan` or
   `JudgeOptions`; prefer `QueryPlan` so traces/evals/judge share one source.
4. Reapply concept-role policy after model-planner merge, before derived
   `model_concept_terms` variants are added.
5. Update strategy variant generation and scoring so intent/frame concepts do
   not dominate query strings or penalize anchored evidence.
6. Update `Judge` to filter `Retrieval.MissingTerms` by concept role, then use
   missing content concepts and protected anchors over a small top-N or anchored
   direct-row window.
7. Update `RetryFocusedVariant` construction so `runRetry(initialPack, judge)`
   composes anchored retry questions from `initialPack.QueryPlan.ProtectedAnchors`
   plus `judge.MissingConcepts`, then call a `brainresearch` merge helper that
   recomputes pack coverage instead of replacing packs. Focused retries always
   preserve initial direct evidence, even when no anchors were detected.
8. Preserve attempt-specific planner artifacts with `brainresearch.Options.Attempt`
   plus optional observer interface methods.
9. Tighten chat continuity so prior bad evidence titles do not become future
   query terms unless pinned or clearly needed for a follow-up; add the
   handle/underscore suppression before the short-question shortcut, plumb the
   current raw user question separately from the composed retrieval question, and
   carry prior entities only as typed continuity anchors.
10. Add CI-safe runner eval support with stop-after-judge/skip-synthesis or fake
   synthesis plus trace-disabled/temp-backed execution.
11. Add runner-level tests/evals for the Kristof_Poland failure.
12. Update docs and changelog.
13. Run targeted tests, then the standard gates.

## Verification

Minimum targeted commands:

```sh
go test ./internal/brainresearch ./internal/researchrun ./internal/researcheval ./web/...
./bin/dbrain --no-debug eval research diff --trace /Users/darron/.local/share/dbrain/research-runs/20260704T174350.016236000Z-65ae1fd81bd8
```

After adding runner-level eval support, run the new runner eval against the same
trace or derived local case. The existing diff command alone is not enough.

Standard project gates after implementation:

```sh
task fmt
task lint
task test-ci
```

If CLI behavior or eval commands change:

```sh
task build
```

## Acceptance Criteria

- The web Chat query `Can you synthesize the Tweets from @Kristof_Poland -
  they're in the dbrain.` returns Kristof_Poland X evidence and cites it.
- Raw handles and underscore aliases are preserved before normalization; trace
  output shows `@Kristof_Poland` / `Kristof_Poland` as protected anchors.
- Trace output shows the canonical resolved anchor
  `x-author:kristof_poland`, the anchor relation/provenance, and whether each
  final evidence row satisfied the anchor through metadata or text fallback.
- Web Chat anchor extraction uses the raw user question, not prior evidence
  titles from the composed retrieval question.
- Web Chat pronoun follow-ups reuse prior resolved anchors through typed
  continuity, while explicit current-turn anchors take precedence over stale
  prior anchors.
- The answer may distinguish tweets from essays, but it must not claim the
  corpus lacks Kristof_Poland rows when the initial pack found them.
- At least one anchored row per satisfied anchor survives into final synthesis
  context unless explicitly dropped for a traced token-budget reason.
- `synthesize`, `they`, `re`, `come`, and `up` do not appear as required missing
  concepts in traces for anchored entity synthesis queries.
- A focused retry cannot drop protected anchors.
- A focused retry cannot replace any initial direct pack with generic intent-term
  matches; merge-not-replace is unconditional.
- Merged retry packs have accurate `Pack.Coverage`; evidence counts, kind/source
  buckets, tag buckets, and corpus coverage fields are not left stale after
  retry merge.
- Missing `synthesize`, `summary`, `overview`, or similar output-shape terms
  cannot by themselves make anchored evidence weak.
- The failure is covered by a runner-level regression, not only by
  `brainresearch.Build`, and the runner regression is CI-safe with no model,
  credentials, or persistent trace artifacts.
- Trace output explains the anchor, concept roles, retry query, and retry merge
  decision.
- No special-case code references Kristof_Poland.

## Review Questions

Ask Claude and Amp to challenge these points specifically:

- Is `QueryPlan` the right place to expose protected anchors and concept roles,
  or should `JudgeOptions` carry them separately?
- Is top-N judging enough, or should anchor coverage be computed over the whole
  direct pack?
- Should retry rows be allowed when they do not match anchors but do fill a
  genuine content concept such as `essays`?
- Does the optional observer-method approach preserve old trace tooling while
  enabling attempt-specific planner artifacts?
- Does tightening `buildChatRetrievalQuestion` preserve useful follow-up
  behavior while preventing bad prior evidence titles from polluting standalone
  searches?
- What is the smallest runner-level eval interface that catches this bug without
  overfitting to exact answer prose?
