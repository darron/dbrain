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

1. For broad research questions, call `dbrain_research_pack` first. It returns retrieve-only evidence, the text/tag query plan, coverage counts, suggested next tools, and may include a topic brief.
2. For direct Q&A, call `dbrain_ask` with `retrieve_only=true` first. Only set `retrieve_only=false` when the user explicitly wants synthesized prose and model use is acceptable.
3. For keyword or tag exploration, call `dbrain_search`, then inspect promising results with `dbrain_get`.
4. For graph expansion, call `dbrain_related` on strong evidence items or sources.
5. For entity or topic browsing, use `dbrain_entity_map`, `dbrain_topic_map`, or `dbrain_topic_brief`.
6. For operational status, use `dbrain_stats_activity`, `dbrain_stats_backlog`, `dbrain_stats_items`, or `dbrain_stats_sources`.

## Research Practice

- Cite source keys like `[x:...]` or `[src:...]` and include title, URL, or note path when useful.
- Answer from the collector's saved corpus. The saved items reflect what the person found valuable, interesting, or noteworthy; do not inject external balance, alternate viewpoints, or model-background knowledge unless the user asks for it.
- Treat MCP evidence as pointers into the local memory, not as complete global truth. Fetch details with `dbrain_get` before making specific claims.
- Use `user_tags` as retrieval hints. Tags can match searches, disambiguate broad topics, and indicate the user's own categorization, but they do not replace source text.
- For named entities, search the likely hyphenated tag alias too, for example `Mark Carney` should include `mark-carney`.
- Prefer `dbrain_research_pack` over several primitive searches. If the pack is weak, then run narrow follow-up searches or `dbrain_related` using the pack's suggested next tools.
- Do not mutate dbrain state unless the user explicitly asks. The MCP server is intended to be read-only.

## Fallback

If MCP tools are not available in the current Codex session, use the local CLI from the dbrain repo:

```bash
./bin/dbrain ask "QUESTION" --retrieve-only
./bin/dbrain serve mcp
```

If the MCP config was just installed, a new Codex session may be required before the `dbrain_*` MCP tools are discoverable.
