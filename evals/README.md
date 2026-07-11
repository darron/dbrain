# Research And MCP Evals

`dbrain eval research` runs full research/chat harness regression checks against
`internal/brainresearch`. Use it for the main Chat quality loop: query-plan
shape, planner-disabled baselines, retrieval signals, prepared synthesis status,
and citation source-key coverage.

Create a starter file:

```sh
./bin/dbrain eval research --write-example evals/local/research.json
```

Run it:

```sh
./bin/dbrain eval research --file evals/local/research.json
./bin/dbrain eval research --file evals/local/research.json --json
```

Turn saved traces or chat transcripts into reviewed case proposals:

```sh
./bin/dbrain eval research propose --from-trace data/research-runs/<run-id>
./bin/dbrain eval research propose --from-transcript data/chat-transcripts/<file>.md
./bin/dbrain eval research propose --from-transcript data/chat-transcripts/<file>.md --output evals/local/research.json --apply
```

Transcript proposals do not infer answer-text assertions by default because
saved model answers are not evidence. Use `--include-answer-text` only for
human-reviewed answer-text checks. Trace proposals can include citation
source-key assertions from the saved synthesis trace.

Compare a saved trace against a fresh pack build:

```sh
./bin/dbrain eval research diff --trace data/research-runs/<run-id>
```

Useful research assertions:

- `disable_planner` runs the deterministic baseline.
- `planner_model` exercises a specific planner model when configured.
- `expect_planner`, `expect_query_family`, `expect_query_terms`,
  `expect_query_variants`, `forbid_query_variants`, `expect_concepts`, and
  `forbid_concepts` guard query planning behavior.
- `min_retrieval_signals` verifies that returned evidence carries scoring
  provenance.
- `expect_relevance_excluded_source_keys`, `expect_prompt_admitted_source_keys`,
  `expect_budget_dropped_source_keys`, and their `forbid_...` forms assert the
  exact pre-synthesis evidence lifecycle without treating model prose as
  evidence.
- `expect_answer_cited_source_keys` and `forbid_answer_cited_source_keys`
  require `run_with_runner` because only a completed synthesis can establish
  which prompt sources the answer actually cited.
- `expect_citation_source_keys` remains supported for existing cases. Results
  report `citation_source_keys_mode`: `prompt_admitted` for no-call preparation
  and `answer_cited` after runner synthesis. New cases should use the explicit
  stage assertions above.

Important local research cases should usually exist in two forms: one planner
enabled case for normal behavior and one `disable_planner` baseline case so
planner outages do not collapse retrieval quality.

Useful `expect_query_family` values:

- `entity_topic_overview`
- `person_news_event_lookup`
- `model_tool_selection`
- `software_project_lookup`
- `comparison`
- `timeline_history`
- `media_transcript_ocr_lookup`
- `corrective_followup`
- `exact_title_source_lookup`

## MCP Retrieval Evals

`dbrain eval mcp` runs read-only retrieval regression checks against the same
retrieve-only path used by the MCP research tools.

Use checked-in files in this directory as templates. Keep corpus-specific evals
under `evals/local/*.json`; those files are ignored by git because source keys,
saved URLs, and expected text are specific to one person's brain database.

Create a starter file:

```sh
./bin/dbrain eval mcp --write-example evals/local/mcp.json
```

Run it:

```sh
./bin/dbrain eval mcp --file evals/local/mcp.json
./bin/dbrain eval mcp --file evals/local/mcp.json --json
```

Use `evals/mcp.example.json` as the shareable starter template. Real local eval
files should include exact source keys and short phrases from your own database.

Useful case types:

- A specific entity or tag query should return a known source key near the top.
- Exact tag examples should include at least one known saved item carrying the
  hyphenated user tag, especially for broad entity questions where linked
  source documents may outrank saved items.
- OCR-only evidence should surface text that exists in image OCR, not only post text.
- Video/audio evidence should surface transcript text when that is the strongest match.
- Known difficult source domains should continue returning summarized source rows.
- Broad topic queries should return a minimum amount of evidence and avoid known noisy rows.

Example local recipes:

- Entity/tag: query a named person or project and assert one or more known
  source keys plus `min_exact_tag_evidence` and
  `expect_any_exact_tag_evidence_source_keys` for representative tagged items.
- OCR: query a phrase that only exists in image OCR and assert the OCR item is
  the top result with `expect_top_text`.
- Transcript: query a phrase from a video/audio transcript and assert the
  transcript-backed item or source appears before loosely related text hits.
- Difficult domain: query a known recovered source from a blocked or reader
  fallback domain and assert the summarized `src:...` row is returned.
- Broad topic/noise: query a broad topic, require `min_evidence`, and use
  `forbid_source_keys` or `forbid_text` for known boilerplate/noisy rows.

Useful assertions:

- `expect_top_source_keys` catches cases where the right evidence exists but is ranked too low.
- `expect_any_source_keys` allows acceptable alternatives for evolving corpora.
- `expect_text` and `expect_top_text` verify that excerpts contain the actual supporting text.
- `min_exact_tag_evidence`, `expect_exact_tag_evidence_source_keys`,
  `expect_any_exact_tag_evidence_source_keys`, and
  `expect_exact_tag_evidence_text` verify the `dbrain_research_pack`
  exact-tag evidence lane.
- `require_top_matched_terms` and `forbid_top_missing_terms` guard multi-term query quality.

Cases intentionally assert the collector's own corpus, not global truth.
