# Web Route Capabilities

Date: 2026-05-04

The web UI is a local administration surface. When mounted through
`dbrain serve remote`, the same routes are exposed through tsnet/Tailscale.
There is currently no separate dbrain login or per-route authorization layer;
remote access depends on Tailscale ACLs, node tags, and the server's
same-origin checks.

## Capability Matrix

| Route | Methods | Capability | Notes |
| --- | --- | --- | --- |
| `/` and embedded assets | `GET`, `HEAD` | Static UI | Serves `web/ui/dist` from the Go binary. |
| `/api/bootstrap` | `GET` | Read DB | Returns app name, FTS status, backlog/activity, and source activity. |
| `/api/search` | `GET` | Read DB | Searches item/source FTS and metadata. |
| `/api/get` | `GET` | Read DB; read local note files | Returns item/source details, linked records, rendered note content, and note read errors. Item media refs omit local paths and archive bucket/key values. |
| `/api/stats/backlog` | `GET` | Read DB | Uses current source summary prompt/tool metadata for backlog freshness. |
| `/api/stats/activity` | `GET` | Read DB | Returns recent activity for the requested time window. |
| `/api/stats/source-activity` | `GET` | Read DB | Returns recent source events, failure facets, and repeated-failure rows. |
| `/api/ask` | any | Removed endpoint | Always returns `404` with `endpoint removed`. |
| `/api/research` | `POST` | Read DB; possible model call | Builds a research pack. Model-assisted planning is enabled unless the request sends `disable_planner: true`; it falls back to deterministic planning if no planner model resolves or the planner fails. |
| `/api/research/synthesize` | `POST` | Model call; temp file | Streams an SSE answer from a supplied research pack. Uses the configured dbrain temp directory for prompt input files. |
| `/api/chat/transcripts` | `POST` | Write local file | Saves a non-indexed Markdown diagnostic transcript under `data/chat-transcripts/` and returns a data-directory-relative path. |
| `/api/links` | `POST` | Write DB; optional remote fetch/model call | Adds one or more URLs. When `enrich` is true, the request can fetch remote content and summarize it. |
| `/api/tag` | `POST` | Write DB; update FTS | Updates item or source tags. Source tag writes re-sync source FTS. |
| `/api/media/signed-url` | `GET` | Read DB; archive access | Returns a short-lived archive URL, proxy URL, media type, and expiry without exposing bucket/key or local source path. |
| `/media/asset/<id>` | `GET`, `HEAD` | Read DB; archive access | Proxies archived media from configured S3-compatible storage. Supports range requests. |

## Open-Source Notes

- Treat local `serve web` and remote `serve remote --web` as trusted write
  surfaces, not as read-only viewers.
- Do not expose remote web through Tailscale Funnel or a public proxy unless a
  future design adds dbrain-level authentication and route authorization.
- Bootstrap, transcript-save, detail media, and signed media URL responses avoid
  absolute host paths and archive bucket/key details. Detail responses still
  include note-relative paths and note-read diagnostics for operator
  troubleshooting.
- If a read-only web mode is added later, enforce it in the Go route layer so
  mutation endpoints are unavailable server-side.
