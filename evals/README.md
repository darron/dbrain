# MCP Retrieval Evals

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
- OCR-only evidence should surface text that exists in image OCR, not only post text.
- Video/audio evidence should surface transcript text when that is the strongest match.
- Known difficult source domains should continue returning summarized source rows.
- Broad topic queries should return a minimum amount of evidence and avoid known noisy rows.

Useful assertions:

- `expect_top_source_keys` catches cases where the right evidence exists but is ranked too low.
- `expect_any_source_keys` allows acceptable alternatives for evolving corpora.
- `expect_text` and `expect_top_text` verify that excerpts contain the actual supporting text.
- `require_top_matched_terms` and `forbid_top_missing_terms` guard multi-term query quality.

Cases intentionally assert the collector's own corpus, not global truth.
