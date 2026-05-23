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

For remote agents that support Streamable HTTP MCP, prefer the built-in
Tailscale transport when the user has enabled it:

```bash
dbrain --no-caffeinate --no-debug --root "$(pwd)" serve remote --web --mcp
```

Use the printed MCP URL in the remote agent config:

```json
{
  "mcpServers": {
    "dbrain": {
      "transport": "streamable-http",
      "url": "https://dbrain.<tailnet>.ts.net/mcp",
      "headers": {
        "Authorization": "Bearer ${DBRAIN_MCP_TOKEN}"
      }
    }
  }
}
```

Use the MCP client's supported secret or environment interpolation for the
token value; do not put raw bearer tokens in shared config files.

For any HTTP, tsnet, remote, Funnel, or reverse-proxied MCP endpoint that is not
strictly private, enable MCP bearer auth before exposing it. Stdio MCP does not
use bearer auth.

```bash
dbrain --root "$(pwd)" auth mcp token add agent-name
dbrain --root "$(pwd)" auth mcp token list
```

The add command prints the raw token once. Later list output shows token records
and fingerprints, not the full secret. Configure dbrain to require the token
with either:

```yaml
mcp:
  auth:
    enabled: true
```

```bash
export DBRAIN_MCP_AUTH_ENABLED=true
export DBRAIN_MCP_TOKEN='paste-token-from-add-command'
```

Authenticated Streamable HTTP MCP clients must send:

```text
Authorization: Bearer <token>
```

For MCP-only tailnet serving, use:

```bash
dbrain --no-caffeinate --no-debug --root "$(pwd)" serve mcp --transport tsnet --tsnet-hostname dbrain
```

Check the built-in tailnet node before debugging client config:

```bash
dbrain --root "$(pwd)" tsnet status --json
```

Important fields are `running`, `reachable`, `web_reachable`,
`mcp_reachable`, `cert_health`, `needs_login`, and `state`. `needs_login=true`
means the tsnet node has not authenticated yet; start `dbrain serve remote` and
complete the Tailscale login URL or configure an auth-key ref in dbrain config.
`state=down` with `running=true` means a dbrain process holds the state lock but
the status probes could not reach the exposed web/MCP endpoints.

If built-in tsnet is not available, run a long-lived local HTTP transport and
expose it through Tailscale Serve:

```bash
dbrain --no-caffeinate --no-debug --root "$(pwd)" serve mcp --transport http --addr 127.0.0.1:8743 --path /mcp
tailscale serve --bg 8743
```

Smoke-test any Streamable HTTP endpoint with JSON-RPC POST:

```bash
curl -s https://dbrain.<tailnet>.ts.net/mcp \
  -H "Authorization: Bearer $DBRAIN_MCP_TOKEN" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Omit the Authorization header only when bearer auth is intentionally disabled.

Do not configure both stdio and remote HTTP for the same agent unless duplicate
`dbrain_*` tools are intentional. Local Codex/Claude sessions can keep using
stdio while remote agents use the tailnet Streamable HTTP endpoint.

## Default Workflow

1. For broad research questions and direct Q&A, call `dbrain_research_pack` first. It returns retrieve-only evidence, the text/tag query plan, model-assisted query planner metadata when available, retrieval-lane status, exact tag coverage, representative `exact_tag_evidence` examples, corpus match counts, per-evidence retrieval score signals, sanitized media refs for media-backed item evidence, suggested next tools, and may include a topic brief. Model-assisted planning is enabled by default when dbrain has a configured model; pass `disable_planner=true` only when deterministic planning is specifically needed.
2. For keyword or tag exploration, call `dbrain_search`, then inspect promising results with `dbrain_get_many` using `content_mode="evidence"` and the same `query` when there are multiple source keys, or `dbrain_get` for one source key.
3. For graph expansion, call `dbrain_related` on strong evidence items or sources.
4. For entity or topic browsing, use `dbrain_entity_map`, `dbrain_topic_map`, or `dbrain_topic_brief`.
5. For operational status, use `dbrain_stats_activity`, `dbrain_stats_backlog`, `dbrain_stats_items`, or `dbrain_stats_sources`.

## Research Practice

- Cite source keys like `[x:...]` or `[src:...]` and include title, URL, or note path when useful.
- Answer from the collector's saved corpus. The saved items reflect what the person found valuable, interesting, or noteworthy; do not criticize the corpus for not being unbiased, and do not inject external balance, alternate viewpoints, or model-background knowledge unless the user asks for it.
- Prioritize accuracy over appearing objective: separate supported facts, source claims, opinions, and uncertainty; flag weak or conflicting evidence plainly.
- Treat MCP evidence as pointers into the local memory, not as complete global truth. Read `coverage.recall_note`, `coverage.exact_tag_matches`, `exact_tag_evidence`, and item/source text match counts before deciding whether the returned evidence is enough.
- Fetch details with `dbrain_get_many` or `dbrain_get` before making specific claims. `content_mode="evidence"` includes capped DB sections and limited linked context such as quoted posts and linked sources; pass the original `query` so long extracts, OCR text, transcripts, and linked context are windowed around matches instead of leading boilerplate. Image OCR appears as `ocr_text`, X video/audio transcripts appear as `x_media_transcript`, and related item snippets include distinct transcript/OCR blocks when present. Use `content_mode="brief"` for metadata only, `content_mode="raw"` when raw extracts/transcripts/OCR/JSON are needed, and `content_mode="rendered"` only when the rendered Markdown note shape matters.
- Treat inline media evidence as a two-part signal. `dbrain_search` results, `dbrain_research_pack` evidence rows, `exact_tag_evidence`, and item `dbrain_get` payloads may include a `media` array with sanitized refs: `media_asset_id`, `media_type` (`photo`, `video`, `animated_gif`, `audio`), `ordinal`, `expanded_url`, `remote_url`, `archive_url`, `download_status`, `archive_status`, `width`, and `height`. The media ref tells you what attached media exists and how it can be displayed; the claim-bearing evidence is still the `snippet`/`excerpt` and the detailed `ocr_text` or `x_media_transcript` sections.
- For media-heavy questions, do not discard results just because the title or summary is generic. Search snippets and research excerpts can now come from OCR or media transcripts; inspect those match windows, then fetch the item with `content_mode="evidence"` and the same `query` to read the relevant `ocr_text` or `x_media_transcript` context. Cite the item source key and say whether the support came from OCR, transcript, or only attached media metadata.
- Do not treat a bare media ref as proof of what the image/video/audio contains. Photos need OCR or visual inspection; video/audio claims need transcript evidence. If `media` is present but no `ocr_text`, `x_media_transcript`, or visual inspection supports the claim, say that the saved item has attached media but the content is not yet evidenced by text.
- Read each evidence row's `retrieval` block when judging relevance. The score is heuristic, but the lane and signals explain whether a result came from lexical search, exact user tags, entities, graph-related evidence, or a future semantic lane. `matched_terms` and `missing_terms` show whether a row covers all query terms or only broad tags. Excerpts are query-windowed when possible, so a raw extract excerpt should usually start near the term match instead of at site boilerplate.
- Inspect `query_plan.retrieval_lanes`. Today SQLite lexical search is the baseline and the semantic lane reports `disabled` until a validated local embedding lane exists. Do not assume semantic retrieval ran unless the lane status says `used`.
- Inspect `evidence_role`, `chunk`, and `content_sections` when present. `derived_summary` means the row is supported by summaries or metadata; `raw_extract_window`, `raw_item_window`, `raw_ocr`, or `raw_transcript` means dbrain found a query-windowed raw evidence section. Prefer raw/source sections for precise claims, especially when summaries are broad.
- Source documents are first-class evidence. If a question asks for web, YouTube, or linked-source material, use `source_types` and expect direct `src:...` results rather than only item backlinks.
- When inspecting a `src:...` source, use the source's own `user_tags` as source-centric categorization and read its `backlinks` rows too. Backlinks carry the saved item's `user_tags`, which often explain why the collector saved that source and may differ from the source's own tags.
- Use `user_tags` as retrieval hints. Item and source tags can match searches, disambiguate broad topics, and indicate the user's own categorization, but they do not replace source text. When `exact_tag_evidence` is present, treat it as representative examples for the matching tag lane, not as a complete list.
- For named entities, search the likely hyphenated tag alias too, for example `Mark Carney` should include `mark-carney`. `dbrain_search` and `dbrain_research_pack` report exact tag aliases/counts so you can see whether the tag path hit.
- Prefer `dbrain_research_pack` over several primitive searches. Inspect `query_plan.planner`, `query_plan.query_variants`, and `query_plan.concepts` to understand what the harness tried. Its suggested `dbrain_get` / `dbrain_get_many` next-step arguments include the query when available; preserve it unless you intentionally want un-windowed leading sections. If the pack is weak, then run narrow follow-up searches or `dbrain_related` using the pack's suggested next tools.
- Do not mutate dbrain state unless the user explicitly asks. The MCP server is intended to be read-only.

## Fallback

If MCP tools are not available in the current Codex session, use the local CLI from the dbrain repo:

```bash
./bin/dbrain research "QUESTION" --retrieval-only
./bin/dbrain serve mcp
```

If the MCP config was just installed, a new Codex session may be required before the `dbrain_*` MCP tools are discoverable.

## Quality Checks

When improving dbrain MCP retrieval, use corpus-local eval cases instead of hard-coding private fixture data:

```bash
./bin/dbrain eval mcp --write-example evals/local/mcp.json
./bin/dbrain eval mcp --file evals/local/mcp.json
./bin/dbrain eval research --write-example evals/local/research.json
./bin/dbrain eval research --file evals/local/research.json
./bin/dbrain eval research diff --trace data/research-runs/<run-id>
./bin/dbrain eval research propose --from-trace data/research-runs/<run-id>
```

Use `evals/mcp.example.json` as the shareable template and keep private corpus cases under ignored `evals/local/*.json`. Eval cases can assert expected source keys, acceptable source-key alternatives, expected top source keys, minimum evidence count, expected/forbidden evidence text, expected top-result text, source-type filters, related-evidence expansion, representative `exact_tag_evidence` examples from `dbrain_research_pack`, top-result matched/missing terms, and a rough latency budget.

Use `dbrain eval research` for full harness behavior: query-family planning, planner-disabled baselines, source-key citation preparation, retrieval lanes, and trace diffs. Saved web Chat traces live under `data/research-runs/` as Markdown plus JSON sidecars; promote useful failures into reviewed research eval cases instead of tuning from memory.
