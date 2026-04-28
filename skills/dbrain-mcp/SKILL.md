---
name: dbrain-mcp
description: Use when the user asks to query, search, browse, research with, inspect, or ask questions of their local dbrain/second-brain memory via MCP, including phrases like "use my brain", "ask my brain", "search dbrain", or "what does my brain know about ...".
metadata:
  short-description: Query the local dbrain memory through MCP
---

# dbrain MCP

Use the read-only dbrain MCP server as the preferred interface to the user's local second brain.

## Setup Check

Before relying on MCP tools, verify the server is configured for the active agent environment.

1. Build the binary from the dbrain repo:

```bash
task build
```

2. Install this skill for Codex by copying `skills/dbrain-mcp` to the Codex skills directory:

```bash
mkdir -p ~/.codex/skills
cp -R skills/dbrain-mcp ~/.codex/skills/dbrain-mcp
```

3. Add the MCP server to `~/.codex/config.toml`:

```toml
[mcp_servers.dbrain]
command = "/path/to/dbrain/bin/dbrain"
args = ["--no-caffeinate", "--no-debug", "--root", "/path/to/dbrain", "serve", "mcp"]
```

Use absolute paths so the MCP server starts correctly from any agent working directory.

4. Restart the agent session after changing MCP config. Existing sessions may not discover newly configured `dbrain_*` tools.

5. Smoke-test the server if tools are unavailable:

```bash
./bin/dbrain --no-caffeinate --no-debug --root "$(pwd)" serve mcp
```

## Default Workflow

1. For broad research questions, call `dbrain_research_pack` first. It returns retrieve-only evidence, the text/tag query plan, exact tag coverage, corpus match counts, per-evidence retrieval score signals, suggested next tools, and may include a topic brief.
2. For direct Q&A, call `dbrain_ask` with `retrieve_only=true` first. Only set `retrieve_only=false` when the user explicitly wants synthesized prose and model use is acceptable.
3. For keyword or tag exploration, call `dbrain_search`, then inspect promising results with `dbrain_get_many` using `content_mode="evidence"` and the same `query` when there are multiple source keys, or `dbrain_get` for one source key.
4. For graph expansion, call `dbrain_related` on strong evidence items or sources.
5. For entity or topic browsing, use `dbrain_entity_map`, `dbrain_topic_map`, or `dbrain_topic_brief`.
6. For operational status, use `dbrain_stats_activity`, `dbrain_stats_backlog`, `dbrain_stats_items`, or `dbrain_stats_sources`.

## Research Practice

- Cite source keys like `[x:...]` or `[src:...]` and include title, URL, or note path when useful.
- Answer from the collector's saved corpus. The saved items reflect what the person found valuable, interesting, or noteworthy; do not inject external balance, alternate viewpoints, or model-background knowledge unless the user asks for it.
- Treat MCP evidence as pointers into the local memory, not as complete global truth. Read `coverage.recall_note`, `coverage.exact_tag_matches`, and item/source text match counts before deciding whether the returned evidence is enough.
- Fetch details with `dbrain_get_many` or `dbrain_get` before making specific claims. `content_mode="evidence"` includes capped DB sections and limited linked context such as quoted posts and linked sources; pass the original `query` so long extracts, OCR text, transcripts, and linked context are windowed around matches instead of leading boilerplate. Image OCR appears as `ocr_text`, X video/audio transcripts appear as `x_media_transcript`, and related item snippets include distinct transcript/OCR blocks when present. Use `content_mode="brief"` for metadata only, `content_mode="raw"` when raw extracts/transcripts/OCR/JSON are needed, and `content_mode="rendered"` only when the rendered Markdown note shape matters.
- Read each evidence row's `retrieval` block when judging relevance. The score is heuristic, but the signals explain whether a result matched title text, summaries/excerpts, exact user tags, entities, or graph-related evidence. Excerpts are query-windowed when possible, so a raw extract excerpt should usually start near the term match instead of at site boilerplate.
- Source documents are first-class evidence. If a question asks for web, YouTube, or linked-source material, use `source_types` and expect direct `src:...` results rather than only item backlinks.
- Use `user_tags` as retrieval hints. Tags can match searches, disambiguate broad topics, and indicate the user's own categorization, but they do not replace source text.
- For named entities, search the likely hyphenated tag alias too, for example `Mark Carney` should include `mark-carney`. `dbrain_search` and `dbrain_research_pack` report exact tag aliases/counts so you can see whether the tag path hit.
- Prefer `dbrain_research_pack` over several primitive searches. Its suggested `dbrain_get` / `dbrain_get_many` next-step arguments include the query when available; preserve it unless you intentionally want un-windowed leading sections. If the pack is weak, then run narrow follow-up searches or `dbrain_related` using the pack's suggested next tools.
- Do not mutate dbrain state unless the user explicitly asks. The MCP server is intended to be read-only.

## Fallback

If MCP tools are not available in the current Codex session, use the local CLI from the dbrain repo:

```bash
./bin/dbrain ask "QUESTION" --retrieve-only
./bin/dbrain serve mcp
```

If the MCP config was just installed, a new Codex session may be required before the `dbrain_*` MCP tools are discoverable.

## Quality Checks

When improving dbrain MCP retrieval, use corpus-local eval cases instead of hard-coding private fixture data:

```bash
./bin/dbrain eval mcp --write-example evals/mcp.json
./bin/dbrain eval mcp --file evals/mcp.json
```

Eval cases can assert expected source keys, acceptable source-key alternatives, minimum evidence count, expected/forbidden evidence text, source-type filters, related-evidence expansion, and a rough latency budget.
