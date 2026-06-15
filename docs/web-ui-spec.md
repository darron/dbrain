# Web UI Spec

Status: historical first-slice design. The implemented web UI is now broader
than this original read-only spec: it includes write routes for tags, link
addition, chat transcript export, archive media helpers, and model-backed
research/synthesis. See `docs/web-route-capabilities.md` for the current route
and capability map.

## Summary

Add a local read-only web surface for `dbrain` using a Go-served JSON API and an
embedded Vite-bundled Svelte frontend. The first slice should help browse and
query the local brain while `worker sources` continues running in a separate
process.

## Current State

- `dbrain` has CLI commands for `search`, `get`, `ask`, and `stats`.
- `dbrain serve` currently exposes only `mcp` over stdio.
- There is no browser UI or HTTP API.
- SQLite is already configured for `WAL` mode with a busy timeout, which is
  suitable for a read-mostly web process alongside the source worker.

## Goals

- Add `dbrain serve web` with an HTTP listener for local use.
- Serve an embedded Svelte frontend from the Go binary.
- Expose read-only JSON endpoints for:
  - search
  - get item/source details
  - backlog stats
  - activity stats
  - source activity with lightweight filters
  - evidence-only ask
- Keep the first UI intentionally small and operationally safe.

## Non-Goals

- No auth/session system in the first slice.
- No write/mutation APIs.
- No synthesized answer generation through the web UI in the first slice.
- No Tailscale/`tsnet` integration in this change.
- No SPA-style client routing beyond a single shell page.

## Locked Product Decisions

- Go owns the listener, routes, JSON API, and static asset serving.
- Svelte owns the interactive page shell and read-only query UI.
- The first `ask` API path will force retrieval-only behavior to avoid competing
  with source enrichment for summarize/provider capacity.
- The browser UI will be a single page mounted at `/`.

## Architecture

### Package Boundary

- `internal/app`
  Adds `serve web` cobra command wiring.
- `web`
  Owns the HTTP server, API handlers, embedded asset serving, and UI bootstrap.
- `web/ui`
  Owns the Svelte/Vite frontend source and built `dist` assets.
- `internal/store`, `internal/ask`
  Remain the single behavior path for retrieval and stats logic.

### Routes

- `GET /`
  Serves the embedded frontend shell.
- `GET /api/bootstrap`
  Returns app metadata plus initial backlog/activity stats.
- `GET /api/search?q=<query>&limit=<n>`
  Returns search results from the local brain.
- `GET /api/get?lookup=<source-key-or-id>`
  Returns item or source metadata plus rendered note content.
- `GET /api/stats/backlog`
  Returns current backlog stats.
- `GET /api/stats/activity?window=<duration>`
  Returns recent activity stats.
- `GET /api/stats/source-activity?limit=<n>&source_type=<type>&domain=<domain>`
  Returns recent source successes/failures plus repeated-failure hotspots,
  failure facet buckets, and optionally filtered by source type, domain,
  status, stored failure kind, message text, time window, failure-table sort,
  and failure-table offset.
- `POST /api/research`
  Accepts a question and returns a research pack with evidence.
- `POST /api/research/synthesize`
  Accepts a research pack and streams a local synthesized answer over SSE.

## API Shape

### `GET /api/bootstrap`

```json
{
  "app": {
    "name": "dbrain"
  },
  "backlog": {},
  "activity": {}
}
```

### `GET /api/search`

```json
{
  "query": "agent memory",
  "results": []
}
```

### `GET /api/get`

```json
{
  "kind": "item",
  "item": {},
  "source": null,
  "note_content": "# ..."
}
```

### `POST /api/research`

```json
{
  "question": "What do I have on agent memory?",
  "limit": 8,
  "source_types": ["github", "web"],
  "include_related": true,
  "related_limit": 2
}
```

Response:

```json
{
  "question": "What do I have on agent memory?",
  "answer": "",
  "evidence": []
}
```

## UI Shape

- Header with app title and current backlog/activity cards.
- Operations panel for recent source successes/failures with triage filters and
  repeated-failure hotspot summaries, including click-through pivots into a
  prefiltered failure view, facet chips for failure kind, status, and domain,
  a paged failure table, and compact success/failure trend buckets.
- Search panel for keyword search.
- Ask panel for evidence-only retrieval.
- Detail panel showing the selected note metadata and rendered markdown source.

## Implementation Phases

1. Add `serve web` command and HTTP server package.
2. Add read-only JSON endpoints and backend tests.
3. Scaffold Svelte/Vite app and wire API calls.
4. Build and embed frontend assets.
5. Update docs with web serving instructions.

## Testing Plan

- Go tests for HTTP handlers using `httptest`.
- Build verification that embedded assets compile into the binary.
- Existing `task test-ci`, `task lint`, and `task build`.

## Acceptance Criteria

- `go run ./cmd/dbrain serve web` starts a local HTTP server.
- `/api/search`, `/api/get`, `/api/stats/backlog`, `/api/stats/activity`, and
  `/api/research` respond successfully against the local DB.
- The embedded frontend can search, research local evidence, inspect a
  selected note, and show backlog/activity plus recent source-activity status,
  failure-table paging/sorting, and source trend buckets.
