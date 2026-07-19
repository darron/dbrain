# dbrain MCP

`dbrain serve mcp` exposes the local `dbrain` corpus over MCP so agents can
search, inspect, and research against the collector's saved material.

The MCP server is read-only. SQLite remains the working source of truth, and
the server returns DB-backed evidence by default instead of relying on rendered
Markdown freshness.

The default transport is stdio for local clients that launch `dbrain` as a
subprocess. A parallel stateless Streamable HTTP transport is available for
long-running local daemon use, especially when fronted by Tailscale Serve for
remote agents.

## Surface

The server provides:

- **Tools**: `dbrain_audit`, `dbrain_search`, `dbrain_get`, `dbrain_get_many`,
  `dbrain_research_pack`, `dbrain_related`, `dbrain_entity_map`,
  `dbrain_topic_map`, `dbrain_topic_brief`, `dbrain_whats_new`,
  `dbrain_stats_items`, `dbrain_stats_sources`, `dbrain_stats_activity`,
  `dbrain_stats_backlog`, `dbrain_okf_search`, and `dbrain_okf_get`.
- **Resources**: `dbrain://mcp/overview`, `dbrain://stats/activity`,
  `dbrain://stats/backlog`, `dbrain://stats/items`, and
  `dbrain://stats/sources`.
- **Resource templates**: `dbrain://item/{lookup}`,
  `dbrain://source/{lookup}`, `dbrain://search/{query}`,
  `dbrain://entity/{query}`, `dbrain://topic/{query}`,
  `dbrain://topic-note/{query}`, `dbrain://research/{query}`, and queryable
  `dbrain://stats/...` templates.
- **Prompts**: `brain_research`, `brain_browse`, `brain_entity_browse`,
  `brain_topic_map`, `brain_topic_brief`, and `brain_status`.

If a client needs to discover the MCP surface from inside the protocol, start
with:

- Resource: `dbrain://mcp/overview`
- Prompt: `brain_status` or `brain_research`

## Research Semantics

MCP research should answer from the collector's saved corpus. The corpus
reflects what that person found valuable, interesting, or noteworthy, including
their own selection bias. Agents should characterize the saved evidence
faithfully and cite it. Accuracy matters more than appearing objective: agents
should separate supported facts, source claims, opinions, and uncertainty, and
flag weak or conflicting evidence plainly. They should not criticize the corpus
for not being unbiased or inject external balance, alternate viewpoints, or
model prior knowledge unless the user explicitly asks for that.

The MCP surface is read-only and retrieval-oriented. It does not expose the old
`dbrain_ask` synthesis tool; agents should use `dbrain_research_pack` for broad
questions, then inspect cited evidence with `dbrain_get_many` or `dbrain_get`.
The tool list includes `outputSchema` metadata so MCP clients can reason about
the structured payloads without learning them from examples.

Evidence excerpts are query-aware. When a match appears deep in a raw source
extract, item OCR text, or media transcript, the retrieved evidence window is
centered near the match instead of blindly returning the start of the raw
document. Item-level derived summaries are also surfaced as summaries in
retrieval evidence, while raw OCR, transcript, and text remain available through
`dbrain_get`.

## Core Tools

### `dbrain_audit`

Use `dbrain_audit` for production-health claims. It accepts only
`profile: "fast"|"standard"`; omitted profile defaults to `fast`.

- `fast` runs the complete local fast registry under a fixed ten-second
  deadline. Concurrent fast calls share one process-wide run.
- `standard` reads the newest persisted exact-profile standard report and
  never starts an audit or network request.
- Both profiles return `{"report":...,"freshness":...}`. A missing standard
  report returns `report: null` with `freshness.status: "unknown"` and
  `reason: "not_found"`.

The tool does not accept deep scans, category filters, time windows, paths,
URLs, endpoints, source identifiers, archive keys, or download limits. Output
is the stable privacy-validated `dbrain.audit.v1` report plus freshness and is
capped at 256 KiB. Continue using `dbrain_stats_*` for exploratory counts, not
as a substitute for whole-system health.

Release acceptance is intentionally broader than the MCP authority. Use the
repo-local [`dbrain-production-audit`](skills/dbrain-production-audit) skill
with the installed CLI for content-free pre/post comparison, expected-commit
verification, archive restore validation, media inventory, and bounded upstream
parity. MCP cannot run those deep checks and must not infer them from stats.

### `dbrain_research_pack`

Use `dbrain_research_pack` first for broad questions. It returns retrieve-only
evidence plus:

- a compact query plan with the text query and tag aliases used
- coverage counts by kind, source type, and tag
- exact user-tag match counts
- broad item/source text-match counts
- representative `exact_tag_evidence` examples from saved items or sources
  carrying those tags
- suggested follow-up tools
- retrieval score explanations for each evidence row
- a grouped topic brief when the question is broad enough to infer a topic

The `coverage.recall_note` field warns when returned evidence is only a capped
working set relative to the larger matching corpus. That lets an agent start
from one read-only call instead of manually orchestrating `dbrain_search`,
`dbrain_topic_brief`, and follow-up note fetches.

`topic`, `include_topic_brief`, `include_related`, and `max_chars_per_doc`
control how much context the pack returns. Each evidence row's `retrieval`
block includes `matched_terms` and `missing_terms`; multi-term queries penalize
rows that miss focused terms, so broad tag matches do not outrank direct
matches on rarer query terms.

Source documents are searched as their own candidate stream, so
`source_types=["web"]` or `["youtube"]` can return direct `src:...` evidence
even when item hits would otherwise fill the candidate window.

#### Semantic retrieval contract

The configured semantic mode defaults to `off`. MCP `use_semantic=true`
force-enables effective `on`; `disable_semantic=true` force-enables effective
`off`; passing both is a tool/client error. These booleans are transport
overrides and do not mutate configuration. Effective `shadow` or `on` requires
a non-empty local Ollama embedding model and positive dimensions.

- `off` is lexical-only.
- `shadow` runs semantic exact search but keeps visible evidence, ordering, and
  synthesis lexical-identical. `query_plan.shadow_comparison` contains only
  bounded source/chunk identifiers, ranks, counts, status/reason, and
  added/removed/reordered references—never excerpts or other content.
- `on` returns RRF-fused evidence while retaining protected evidence and its
  chunk/content provenance.

The only vector backend in this foundation is SQLite-authoritative exact scan.
Semantic candidate depth defaults to 50; exact scans are capped at 25,000
active chunks by default. A larger corpus reports `too_large`, and validly
configured provider/search failures report their status/reason; both fail open
to lexical evidence. ANN, other providers, background sync, and default-on are
deferred.

Inspect `query_plan.semantic_mode`, `query_plan.retrieval_lanes`, and (only in
shadow mode) `query_plan.shadow_comparison`. Each evidence row may expose
`evidence_role`, `chunk`, `content_sections`, `retrieval.fused_score`, and
lexical/semantic lane provenance including status, reason, rank, raw distance
or score, RRF contribution, profile, backend, and generation. Exact-tag
retrieval is a separate lane; `exact_tag_evidence` is representative, not
exhaustive.

Direct `dbrain_research_pack` calls are read-only and trace-free even in shadow
mode: they do not create `data/research-runs`. The
`dbrain://research/{query}` resource retains its documented parameters and has
no additional semantic URI parameters.

### `dbrain_get`

`dbrain_get` is DB-first. By default it returns slim item/source metadata and
capped `content_sections` from SQLite, not rendered Markdown.

Supported content modes:

- `brief`: metadata only
- `evidence`: normal research context
- `raw`: raw DB extracts, transcripts, OCR, and JSON
- `rendered`: rendered Markdown note shape for clients that specifically need
  it

`max_chars_per_section` controls per-section output size, with a hard cap to
avoid accidental huge MCP responses. Pass `query` with `content_mode=evidence`
to window each text section around matching terms instead of returning the
beginning of long extracts.

Evidence mode includes a capped DB graph expansion for context such as quoted X
posts, linked sources, and source backlinks. X media enrichments are exposed as
first-class evidence sections: image OCR appears as `ocr_text`, and video/audio
transcript text stored by the X media transcription stage appears as
`x_media_transcript` instead of generic article text.

### `dbrain_get_many`

Use `dbrain_get_many` after a search or research pack when an agent needs to
inspect several evidence rows in one MCP round trip. It uses the same
DB-backed content modes and section caps as `dbrain_get`, and returns partial
per-lookup errors without failing the whole batch.

Suggested follow-up arguments from `dbrain_research_pack` include the research
query so detail fetches keep the same query-windowing behavior.

### `dbrain_okf_search` and `dbrain_okf_get`

Use the OKF tools only when an agent needs to inspect the generated Open
Knowledge Format bundle. Normal research should stay DB-first with
`dbrain_research_pack`, `dbrain_search`, and `dbrain_get`.

`dbrain_okf_search` searches concepts from the configured OKF directory and
returns concept path, OKF type, title, description, dbrain concept id, source
key, source type, and a small snippet. An empty query lists top bundle concepts.

`dbrain_okf_get` reads one OKF concept by bundle path, `dbrain_concept_id`, or
`dbrain_source_key`. It returns parsed concept metadata and capped body text;
set `include_markdown=true` only when the full rendered Markdown shape matters.

These MCP tools are read-only and do not regenerate, validate, or export the
bundle. Refresh it with `dbrain okf export`, `sync all --okf-export`, or
`DBRAIN_OKF_EXPORT_ENABLED=true` before relying on it for current output.

### `dbrain_whats_new`

Use `dbrain_whats_new` when an agent needs to review what changed locally since
a timestamp or prior review cursor before deciding what to inspect next. Pass
exactly one of `since` or `cursor`. `since` accepts RFC3339 timestamps,
local-offset RFC3339 timestamps, or relative durations such as `24h` and `7d`.

The tool returns the same structured page shape as CLI JSON and
`GET /api/whats-new`: `view`, `events`, `entities`, `counts`, `next_cursor`,
`high_watermark`, and `truncated`. Use `view: "entities"` for compact grouped
review when answering questions like "what should I pay attention to?" or
"what's new over the last couple of days"; it suppresses raw event rows and
returns one grouped record per item/source on the page with compact preferred
summaries/excerpts and collapsed event kinds. Fetch details with
`dbrain_get_many` or `dbrain_get` before quoting or relying on full raw
evidence. Use the default `view: "events"` when debugging raw pipeline
chronology. `types` can limit the feed to `imports`, `enrichments`, `failures`,
or `all`. Pagination and `limit` are event-based in both views; when combining
multiple `view: "entities"` pages, de-duplicate by `entity_key` and prefer the
row with a stronger summary, higher importance, or later `latest_event_at`.
Continue pagination only while `truncated` is true; `next_cursor` is still
returned on the final page for high-watermark bookkeeping. The feed is DB-only
and read-only; it does not run imports, enrichment, model calls, or note
rendering.

## Tags

Item and source `user_tags` are indexed for search and returned in MCP search
and evidence payloads. They are research hints: agents can search by tag names,
use tags to disambiguate broad questions, and treat tag matches as stronger
retrieval signals without replacing the underlying source text.

Source tags describe the linked source itself. Backlink tags describe the saved
item that referenced the source, so both can be useful and they may differ.

Multi-word research questions also check the matching hyphenated tag alias. For
example, `Mark Carney` checks `mark-carney`. `dbrain_search` reports the tag
aliases it checked plus exact tag counts, and `dbrain_search` /
`dbrain_research_pack` append exact-tag hits to normal text-search evidence
before deduping.

## Workflows

- **Research**: `dbrain_research_pack` first, then `dbrain_get_many`,
  `dbrain_get`, or `dbrain_related` for deeper inspection.
- **Graph browsing**: `dbrain_get` plus `dbrain_related`.
- **Entity browsing**: `dbrain_entity_map` or `brain_entity_browse`, then
  `dbrain_get` on the most relevant entity note.
- **Topic mapping**: `dbrain_topic_map` or `brain_topic_map`, plus
  `dbrain_get` when you want to inspect individual nodes more closely.
- **Topic briefs**: `dbrain_topic_brief` or `brain_topic_brief`, plus
  `dbrain://topic-note/{query}` when a rendered note preview is useful.
- **OKF bundle inspection**: `dbrain_okf_search` and `dbrain_okf_get` when the
  user asks about generated OKF Markdown, bundle paths, or exchange/export
  output. These tools inspect the last generated bundle, not live SQLite.
- **Pipeline monitoring and review**: `dbrain_whats_new` for a reviewable
  cursor feed of recent local evidence changes, plus `dbrain_stats_activity`,
  `dbrain_stats_backlog`, and optionally `dbrain_stats_sources`.
- **Production health**: `dbrain_audit` with the default fast profile for a
  bounded current local check, or `profile: "standard"` for the newest
  persisted exact-profile standard report.

## Eval

For local retrieval quality checks, create a corpus-specific eval file:

```sh
dbrain eval mcp --write-example evals/local/mcp.json
dbrain eval mcp --file evals/local/mcp.json
```

`evals/mcp.example.json` is a checked-in template. Keep real corpus-specific
files under `evals/local/*.json`; those files are ignored because source keys,
saved URLs, and expected text are specific to one person's brain database.

Eval cases can require a specific top source key, any top key from an
acceptable set, specific source keys anywhere in the evidence, minimum evidence
counts, expected text, expected top-result text, representative
`exact_tag_evidence` examples, forbidden source keys or noisy text,
source-type filters, related-evidence expansion, top-result matched or missing
terms, and rough latency budgets.

Exact-tag assertions exercise the same `dbrain_research_pack` path exposed to
MCP clients; other cases use the lighter retrieval-only path. This is
intentionally corpus-local: open-source users should encode their own
known-good queries rather than relying on project-specific fixture data.

## Importer Contract

MCP retrieval is intentionally source-agnostic. New importers such as Bluesky,
Apple Podcasts, RSS feeds, or read-it-later tools should become visible to MCP
without custom MCP code when they write into the shared data model:

- Create an `items` row for each saved signal with a stable `source_key`,
  `source_type`, `external_id`, canonical URL, title, source text, author
  metadata, timestamps, note path, raw JSON, and content hash.
- Store collector-assigned or model-assigned item tags in `items.user_tags` and
  source-centric tags in `sources.user_tags`; multi-word entity tags should use
  the shared hyphenated form, for example `mark-carney`.
- Store raw extracted linked-source text in `sources.extracted_text` and
  derived source summaries in `sources.summary_text`; do not replace raw text
  with summaries.
- Link items to sources through source-link records so MCP graph expansion can
  move from a post/bookmark/episode to the referenced article, repository,
  paper, or transcript source.
- Store media-derived text in the current item enrichment fields where possible:
  image text in `ocr_text`, short-form video/audio transcripts in the
  `x_media_transcript` enrichment role, and derived item summaries in
  `summary_text`. Legacy X transcript rows may still mirror text through
  `article_text` with `article_title = "X Media Transcript"`, but new importer
  code should target the enrichment roles rather than the compatibility mirror.
- Keep source-specific metadata in raw JSON or source-specific columns, but make
  the durable searchable evidence available through the common text, summary,
  tag, and link fields.

After adding a new importer, add at least one MCP eval case that proves its
strongest evidence is discoverable by text query, tag query, source-type
filter, and any important derived text such as OCR or transcript content.

## Transports

### Stdio

Stdio is the default and remains the recommended local setup for Codex, Claude,
and other agents running on the same machine. The client starts `dbrain` as a
subprocess and communicates over stdin/stdout.

```sh
dbrain serve mcp
```

MCP protocol responses are written to stdout. Operational request logs are
written to stderr so they do not corrupt the stdio protocol. A running server
should emit one short debug line per request, including the MCP method, tool
name when present, status, and duration.

### Streamable HTTP

Use HTTP when `dbrain` should run continuously and remote agents should connect
over a private network such as Tailscale. The implementation is stateless:
there is no `Mcp-Session-Id`, no server-side session memory, and no
server-to-client notification stream.

```sh
dbrain serve mcp --transport http --addr 127.0.0.1:8743 --path /mcp
```

The HTTP endpoint supports MCP Streamable HTTP POST requests and returns
`application/json` responses. GET requests return `405 Method Not Allowed`
because dbrain does not currently provide unsolicited SSE messages.

For Tailscale Serve, bind dbrain to localhost and expose it with Tailscale
Serve:

```sh
tailscale serve --bg 8743
```

Then configure remote MCP clients with the Tailscale HTTPS URL plus `/mcp`.
Use Tailscale Serve, not Funnel, unless you intentionally want a public
internet endpoint.

Production or remote clients must use an explicit HTTPS Streamable HTTP MCP
endpoint. Require bearer authentication whenever the endpoint is not confined
to an otherwise trusted private boundary; never treat a repo/dev stdio command,
localhost URL, or path as production configuration truth.

The built-in tailnet option is usually simpler when you want `dbrain` itself to
own the tailnet node:

```sh
dbrain serve remote --web --mcp
```

This exposes the read/write web UI at `https://<hostname>.<tailnet>.ts.net/`
and read-only MCP at `https://<hostname>.<tailnet>.ts.net/mcp` from one
persistent `tsnet` node. For MCP-only tailnet serving:

```sh
dbrain serve mcp --transport tsnet --tsnet-hostname dbrain
```

Add `--tsnet-funnel` only when you intentionally want Tailscale Funnel public
exposure. Enable `mcp.auth.enabled=true` and create a bearer token first:

```sh
dbrain serve mcp --transport tsnet --tsnet-funnel
```

This does not create a second identity. It uses the same `tsnet.Server`
hostname, state directory, and Tailscale auth credentials, but switches the
listener from tailnet-only `ListenTLS` to public `ListenFunnel`. dbrain requires
TLS and a Funnel-supported port (`:443`, `:8443`, or `:10000`) before starting
that mode. See [TAILSCALE.md](TAILSCALE.md) for Funnel policy, DNS
propagation, web OAuth, MCP bearer auth, and access-log troubleshooting.

Smoke test either HTTP-over-Tailscale or built-in `tsnet` with:

```sh
curl -s https://dbrain.<tailnet>.ts.net/mcp \
  -H "Authorization: Bearer $DBRAIN_MCP_TOKEN" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Browser `GET /mcp` returns a short diagnostic and a copyable curl example. MCP
clients should use JSON-RPC over HTTP POST with
`Content-Type: application/json`.

Security defaults:

- HTTP binds to `127.0.0.1:8743` by default.
- Empty `Origin` requests are accepted for non-browser MCP clients.
- Same-host `Origin` requests are accepted.
- Other browser origins are rejected unless passed with repeatable
  `--allow-origin`.
- Optional bearer auth can be enabled with `mcp.auth.enabled=true` or
  `DBRAIN_MCP_AUTH_ENABLED=true`.
- `dbrain_audit` is always available on local stdio. HTTP and tsnet advertise
  and dispatch it only when dbrain bearer auth is required and configured;
  private/tailnet reachability alone does not grant the capability.
- Funnel refuses to start an MCP surface unless bearer auth is enabled.
- JSON-RPC batches are limited to 16 requests; larger batches are rejected
  before any member is dispatched on HTTP or stdio.

Create an MCP bearer token with:

```sh
dbrain auth mcp token add laptop
```

The raw token is shown once; SQLite stores only the token hash and fingerprint.
Authenticated HTTP clients must send `Authorization: Bearer <token>`. When
bearer auth is disabled, HTTP and tsnet MCP startup logs a warning because
Tailscale Funnel or another public proxy would make the read-only brain content
publicly reachable. Those auth-disabled transports also omit `dbrain_audit`
from `tools/list` and reject direct calls to it.

Do not configure both stdio and HTTP transports for the same agent unless you
want duplicate dbrain tools. It is fine to run one long-lived HTTP daemon for
remote agents while local Codex/Claude instances launch their own stdio
processes.

## First-Run Tailnet Setup

The simplest first run is interactive:

```sh
dbrain serve remote
```

If the tsnet node has not authenticated yet, `dbrain` prints a Tailscale login
URL. Authenticate once; the node identity is then stored under
`<data_dir>/tsnet/<hostname>`, usually
`~/.local/share/dbrain/tsnet/dbrain`.

For unattended startup, use a Tailscale auth key through a typed secret ref
instead of putting the key directly in config:

```yaml
tsnet:
  hostname: dbrain
  auth_key_ref: "op://Private/dbrain/tsnet-auth-key"
```

`op://` refs execute `op read <ref>` without a shell. A macOS Keychain ref is
also supported:

```sh
security add-generic-password \
  -s dbrain \
  -a tsnet-auth-key \
  -w 'tskey-auth-...'
```

```yaml
tsnet:
  auth_key_ref: "keychain://dbrain/tsnet-auth-key"
```

`env:NAME` refs are supported when an environment-managed secret is the right
fit:

```yaml
tsnet:
  auth_key_ref: "env:TS_AUTHKEY"
```

`tsnet.auth_key_command` exists for custom secret managers, but it is
config-file-only and requires `tsnet.allow_secret_command: true`. There is no
environment-variable equivalent because command arrays should not be shell-split
from a single string.

Keep the tsnet state directory out of iCloud, Dropbox, OneDrive, and similar
sync folders. The directory contains Tailscale node and certificate state; if it
is deleted or inconsistently synced, `dbrain` may need to authenticate again or
appear as a new tailnet machine. Use `dbrain tsnet status` to inspect the
resolved hostname, state directory, and lock status.

## Client Config

A generic MCP client config looks like this:

```json
{
  "mcpServers": {
    "dbrain": {
      "command": "dbrain",
      "args": ["serve", "mcp"],
      "cwd": "/Users/darron/src/dbrain"
    }
  }
}
```

For local development, use an absolute binary path and an explicit root:

```json
{
  "mcpServers": {
    "dbrain": {
      "command": "/Users/darron/src/dbrain/bin/dbrain",
      "args": ["--root", "/Users/darron/src/dbrain", "serve", "mcp"]
    }
  }
}
```

For clients that support remote Streamable HTTP MCP servers, use the tailnet
HTTPS URL for the MCP endpoint. Exact config shape varies by client, but the
important values are the transport and URL:

```json
{
  "mcpServers": {
    "dbrain": {
      "transport": "streamable-http",
      "url": "https://dbrain.<tailnet>.ts.net/mcp"
    }
  }
}
```

Some clients use `type` instead of `transport`:

```json
{
  "mcpServers": {
    "dbrain": {
      "type": "streamable-http",
      "url": "https://dbrain.<tailnet>.ts.net/mcp"
    }
  }
}
```

Use the same URL whether it is backed by `dbrain serve remote`, `dbrain serve
mcp --transport tsnet`, or localhost `dbrain serve mcp --transport http`
fronted by Tailscale Serve.

## Skill

This repo includes a Codex skill for agents at `skills/dbrain-mcp/SKILL.md`.
To refresh the installed Codex copy from the repo, run:

```sh
mkdir -p ~/.codex/skills/dbrain-mcp
cp -R skills/dbrain-mcp/. ~/.codex/skills/dbrain-mcp/
```

The skill includes the recommended Codex MCP `~/.codex/config.toml` stanza. Use
absolute paths for both the binary and `--root`, then restart Codex so the
`dbrain_*` tools are discovered.
