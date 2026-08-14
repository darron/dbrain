---
name: dbrain-mcp
description: Use when the user asks to query, search, browse, research with, inspect, or ask questions of their local dbrain/second-brain memory via MCP, including phrases like "use my brain", "ask my brain", "search dbrain", "what changed recently", or "what does my brain know about ...".
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

Production or remote MCP must use an explicit HTTPS Streamable HTTP endpoint;
the repo-local stdio launch recipe and paths are not production configuration
truth. Require bearer authentication whenever the endpoint is not confined to
an otherwise trusted private boundary.

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

`dbrain_audit` is always available over local stdio. HTTP and tsnet expose that
health capability only when dbrain bearer auth is required and configured;
tailnet reachability by itself is insufficient. Auth-disabled HTTP/tsnet omits
the tool from discovery and rejects direct calls.

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
   Semantic quick reference: configured mode defaults to `off`; `off` is lexical-only. `use_semantic=true` forces effective `on`, `disable_semantic=true` forces effective `off`, and passing both is a tool error. `shadow` runs semantic retrieval but preserves visible evidence/order/synthesis exactly; `on` returns RRF-fused evidence. Effective `shadow`/`on` requires a local Ollama embedding model and positive dimensions. Direct MCP pack calls are always read-only and trace-free, including shadow, and never create `data/research-runs`.
2. For keyword or tag exploration, call `dbrain_search`, then inspect promising results with `dbrain_get_many` using `content_mode="evidence"` and the same `query` when there are multiple source keys, or `dbrain_get` for one source key.
3. For graph expansion, call `dbrain_related` on strong evidence items or sources.
4. For entity or topic browsing, use `dbrain_entity_map`, `dbrain_topic_map`, or `dbrain_topic_brief`.
5. For generated Open Knowledge Format bundle inspection, use `dbrain_okf_search` and `dbrain_okf_get`. These read the last exported OKF bundle from the configured OKF directory; they do not export, validate, or read live SQLite directly.
6. For recent activity or handoff review, use `dbrain_whats_new` with exactly one of `since` or `cursor`. For questions like "what's new?", "what changed recently?", "what should I pay attention to?", or "what are the most important things from the last couple of days?", pass `view: "entities"` so the server returns compact item/source groups with preferred summaries/excerpts, collapsed event kinds, tags, actionability, importance, and compact event refs. Treat grouped `summary` as a compact review excerpt, not full raw evidence; fetch details with `dbrain_get_many` or `dbrain_get` before quoting or relying on exact source text. Use the default `view: "events"` only when debugging raw pipeline chronology. Use `since` values such as `24h`, `2d`, or an RFC3339 timestamp for the first page, then preserve and pass `next_cursor` for follow-up pages only while `truncated` is true; `next_cursor` is still returned on the final page for high-watermark bookkeeping. Pagination and `limit` are event-based, so if you merge multiple `view: "entities"` pages, de-duplicate by `entity_key` and prefer the row with a stronger summary, higher importance, or later `latest_event_at`. Use `types` to focus the feed: `imports`, `enrichments`, `failures`, or `all`. Blocked rows are review events surfaced through the failure/status fields rather than a separate `types` filter.
7. For authoritative production-health claims, call `dbrain_audit`. Omit
   `profile` (or use `fast`) for a complete bounded local check under the fixed
   ten-second deadline. Use `profile: "standard"` only to read the newest
   persisted exact-profile standard report; it never starts network work. A
   missing/stale report is unknown according to the returned `freshness`
   envelope. Never try to pass deep, categories, time windows, paths, URLs,
   identifiers, endpoints, archive keys, or limits; the MCP schema intentionally
   exposes none of them. Use `dbrain_stats_activity`, `dbrain_stats_backlog`,
   `dbrain_stats_items`, or `dbrain_stats_sources` only for exploratory counts.

## Research Practice

- In user-facing output, cite real external URLs — link the title or publisher to the entity's `url` field. Source keys like `[x:...]` or `[src:...]` are internal lookup handles for `dbrain_get`/`dbrain_get_many`; never print them in final answers. If an entity has no external URL, name the publisher/title in plain text, or give the note path only when the user needs the local artifact.
- Answer from the collector's saved corpus. The saved items reflect what the person found valuable, interesting, or noteworthy; do not criticize the corpus for not being unbiased, and do not inject external balance, alternate viewpoints, or model-background knowledge unless the user asks for it.
- Prioritize accuracy over appearing objective: separate supported facts, source claims, opinions, and uncertainty; flag weak or conflicting evidence plainly.
- Treat MCP evidence as pointers into the local memory, not as complete global truth. Read `coverage.recall_note`, `coverage.exact_tag_matches`, `exact_tag_evidence`, and item/source text match counts before deciding whether the returned evidence is enough.
- Fetch details with `dbrain_get_many` or `dbrain_get` before making specific claims. `content_mode="evidence"` includes capped DB sections and limited linked context such as quoted posts and linked sources; pass the original `query` so long extracts, OCR text, transcripts, and linked context are windowed around matches instead of leading boilerplate. Image OCR appears as `ocr_text`, X video/audio transcripts appear as `x_media_transcript`, and related item snippets include distinct transcript/OCR blocks when present. Use `content_mode="brief"` for metadata only, `content_mode="raw"` when raw extracts/transcripts/OCR/JSON are needed, and `content_mode="rendered"` only when the rendered Markdown note shape matters.
- Treat inline media evidence as a two-part signal. `dbrain_search` results, `dbrain_research_pack` evidence rows, `exact_tag_evidence`, and item `dbrain_get` payloads may include a `media` array with sanitized refs: `media_asset_id`, `media_type` (`photo`, `video`, `animated_gif`, `audio`), `ordinal`, `expanded_url`, `remote_url`, `archive_url`, `download_status`, `archive_status`, `width`, and `height`. The media ref tells you what attached media exists and how it can be displayed; the claim-bearing evidence is still the `snippet`/`excerpt` and the detailed `ocr_text` or `x_media_transcript` sections.
- For media-heavy questions, do not discard results just because the title or summary is generic. Search snippets and research excerpts can now come from OCR or media transcripts; inspect those match windows, then fetch the item with `content_mode="evidence"` and the same `query` to read the relevant `ocr_text` or `x_media_transcript` context. Cite the item's external URL and say whether the support came from OCR, transcript, or only attached media metadata.
- Do not treat a bare media ref as proof of what the image/video/audio contains. Photos need OCR or visual inspection; video/audio claims need transcript evidence. If `media` is present but no `ocr_text`, `x_media_transcript`, or visual inspection supports the claim, say that the saved item has attached media but the content is not yet evidenced by text.
- Read each evidence row's `retrieval` block when judging relevance. The score is heuristic, but `lanes` records lexical, semantic, exact-tag, entity, and graph provenance. For fused rows inspect `fused_score` plus lane rank, raw distance/score, RRF contribution, profile, backend, and generation. `matched_terms` and `missing_terms` show whether a row covers all query terms or only broad tags.
- Inspect `query_plan.semantic_mode`, `query_plan.retrieval_lanes`, and shadow-only `query_plan.shadow_comparison`. After a bounded retry, `query_plan.retry_shadow_comparison` independently labels that retry attempt's diagnostic. The foundation uses SQLite-authoritative exact vector scan with candidate depth 50 and a default cap of 25,000 current ready embeddings for the configured profile, counted before request filters. On supported tagged builds, segmented native USearch is available; a runtime loads its root lazily and reuses a validated in-memory root only within that process. `generation_busy` means only 250 ms shared-generation admission contention. A cold caller waits up to five seconds; `root_load_timeout` remains lexical while a detached import may warm the runtime later. `native_root_artifacts_unavailable` identifies native artifact/root-validation failure, and `runtime_readiness_unavailable` identifies a query-time readiness snapshot that cannot be read or no longer names a stable root. Those reasons, `too_large`, and validly configured provider/search failures fail open to lexical evidence with explicit status/reason. A cold import retains shared generation protection through publication/discard and cleanup, so refresh/GC can wait; the five-second caller budget cannot interrupt native loading and reader grace remains defense in depth. Other embedding providers and default-on behavior remain deferred. Different-language recall depends on the configured embedding model and must be established by lexical-versus-semantic evals for that model. Do not assume semantic retrieval ran unless the lane status says `used`, which requires native candidates to pass SQLite validation and exact reranking.
- A `shadow_comparison` is bounded and content-free. Inspect `status`, optional `reason`, full `lexical_count` / `hybrid_count`, capped ranked `lexical` / `hybrid` sides, and `added` / `removed` / `reordered` arrays. Every ranked reference contains `source_key`, optional `chunk_id`, and `rank`; it never contains excerpts.
- Inspect `evidence_role`, `chunk`, and `content_sections` when present. `derived_summary` means the row is supported by summaries or metadata. Raw evidence can appear as `raw_extract_window`, `raw_item_window`, `raw_ocr`, or `raw_transcript` on windowed evidence, and as `raw`, `ocr`, or `transcript` on selected semantic chunks. Preserve these fields when semantic fusion selects a chunk, and prefer raw/source sections for precise claims.
- Source documents are first-class evidence. If a question asks for web, YouTube, or linked-source material, use `source_types` and expect direct `src:...` results rather than only item backlinks.
- When inspecting a `src:...` source, use the source's own `user_tags` as source-centric categorization and read its `backlinks` rows too. Backlinks carry the saved item's `user_tags`, which often explain why the collector saved that source and may differ from the source's own tags.
- Use `user_tags` as retrieval hints. Item and source tags can match searches, disambiguate broad topics, and indicate the user's own categorization, but they do not replace source text. When `exact_tag_evidence` is present, treat it as representative examples for the matching tag lane, not as a complete list.
- For named entities, search the likely hyphenated tag alias too, for example `Mark Carney` should include `mark-carney`. `dbrain_search` and `dbrain_research_pack` report exact tag aliases/counts so you can see whether the tag path hit.
- Prefer `dbrain_research_pack` over several primitive searches. Inspect `query_plan.planner`, `query_plan.query_variants`, and `query_plan.concepts` to understand what the harness tried. Its suggested `dbrain_get` / `dbrain_get_many` next-step arguments include the query when available; preserve it unless you intentionally want un-windowed leading sections. If the pack is weak, then run narrow follow-up searches or `dbrain_related` using the pack's suggested next tools.
- Use `dbrain_okf_search` and `dbrain_okf_get` only when the user asks to inspect generated OKF Markdown, bundle paths, exported concepts, or exchange-format output. Prefer DB-first tools for normal research because OKF can be stale until `dbrain okf export` or `sync all --okf-export` runs. `dbrain_okf_get` accepts an OKF path, `dbrain_concept_id`, or source key; pass `include_markdown=true` only when the rendered Markdown matters.
- Do not mutate dbrain state unless the user explicitly asks. The MCP server is intended to be read-only.
- MCP exposes no semantic mutation or purge tool. The current deletion integration outside MCP is item-only: Apple Notes `--forget-excluded` synchronously deletes that item's derived retrieval chunks and embeddings and stale affected retrieval generations through the explicit indexed-content purge. Do not generalize this to every delete path or future parent kind.

## Fallback

If MCP tools are not available in the current Codex session, use the local CLI from the dbrain repo:

```bash
./bin/dbrain research "QUESTION" --retrieval-only
./bin/dbrain whats-new --since 24h --view entities --json
./bin/dbrain serve mcp
./bin/dbrain okf export --entities --topics
./bin/dbrain okf validate "$(./bin/dbrain config paths --json | jq -r .okf_dir)"
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

For OKF MCP tool coverage, run the focused protocol tests after changing OKF search/get behavior:

```bash
go test ./internal/mcpserver -run OKF
```

When a bad or surprising Chat answer appears, use the trace as the debugging
unit:

1. Open the saved trace in the Harness tab or inspect `data/research-runs/<run-id>/run.md`.
2. Compare it against the current harness with `./bin/dbrain eval research diff --trace data/research-runs/<run-id>`.
3. Generate a draft eval with `./bin/dbrain eval research propose --from-trace data/research-runs/<run-id>`.
4. Review the generated assertions before saving them under ignored `evals/local/*.json`; do not commit private source keys or local corpus expectations.
5. Keep both a model-assisted case and a planner-disabled deterministic case when the issue may involve retrieval planning, query expansion, citation preparation, or fallback behavior.
