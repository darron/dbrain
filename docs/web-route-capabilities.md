# Web Route Capabilities

Date: 2026-08-02

The web UI is a local administration surface. When mounted through
`dbrain serve remote`, the same routes are exposed through tsnet/Tailscale.
By default, remote access depends on Tailscale ACLs, node tags, and the
server's shared mutation Origin checks. Optional GitHub OAuth can add a dbrain
session gate for the web UI when configured. Funnel startup requires that
session gate for web and bearer authentication for MCP; it does not permit an
auth-disabled selected surface.

## Capability Matrix

| Route | Methods | Capability | Notes |
| --- | --- | --- | --- |
| `/` and embedded assets | `GET`, `HEAD` | Static UI | Serves `web/ui/dist` from the Go binary. |
| `/api/bootstrap` | `GET` | Read DB | Returns app name, FTS status, backlog/activity, and source activity. |
| `/api/search` | `GET` | Read DB | Searches item/source FTS and metadata. |
| `/api/get` | `GET` | Read DB; read local note files | Returns item/source details, linked records, rendered note content, and note read errors. Item media refs omit local paths and archive bucket/key values. |
| `/api/whats-new` | `GET` | Read DB | Returns a cursor-paged review feed for recent imports, enrichments, failures, and blocked pipeline work. Requires exactly one of `since` or `cursor`; pass `view=entities` for compact grouped item/source review. |
| `/api/stats/backlog` | `GET` | Read DB | Uses current source summary prompt/tool metadata for backlog freshness. |
| `/api/stats/activity` | `GET` | Read DB | Returns recent activity for the requested time window. |
| `/api/stats/source-activity` | `GET` | Read DB | Returns recent source events, failure facets, and repeated-failure rows. |
| `/api/audit/latest` | `GET` | Authenticated audit-report read | Returns the newest persisted exact-profile fast or standard report plus recomputed freshness. Never starts work. Absent when web auth is disabled. |
| `/api/audit/history` | `GET` | Authenticated audit-report read | Returns 1–100 compact exact-profile history entries; defaults to 20 and omits full checks/boundary payloads. Absent when web auth is disabled. |
| `/api/audit/run` | `POST` | Authenticated bounded audit run; report write | Starts only fast or standard under one process-wide coordinator, with a 4 KiB strict JSON body and shared Origin guard. Persists before completion. Absent when web auth is disabled. |
| `/api/audit/runs/<audit_id>` | `GET` | Authenticated in-process run read | Returns running/completed/failed state for an opaque process-run handle. Completed embeds the immutable report with its distinct report audit ID; failed exposes only a fixed error code. |
| `/api/ask` | any | Removed endpoint | Always returns `404` with `endpoint removed`. |
| `/api/research` | `POST` | Read DB; possible model call | Builds a research pack. Model-assisted planning is enabled unless the request sends `disable_planner: true`; it falls back to deterministic planning if no planner model resolves or the planner fails. |
| `/api/research/synthesize` | `POST` | Model call; temp file | Streams an SSE answer from a supplied research pack. Uses the configured dbrain temp directory for prompt input files. |
| `/api/chat/transcripts` | `POST` | Write local file | Saves a non-indexed Markdown diagnostic transcript under `data/chat-transcripts/` and returns a data-directory-relative path. |
| `/api/chat/shares` | `GET`, `POST` | Read/write DB | Lists the authenticated or local owner's public chat shares and creates or refreshes a share from a completed chat answer. |
| `/api/chat/shares/{slug}` | `DELETE` | Delete DB row | Deletes only the authenticated or local owner's matching share. Missing and foreign-owner slugs both return `204` so the route does not expose whether another owner's share exists. Subject to the shared Origin guard. |
| `/api/links` | `POST` | Write DB; optional remote fetch/model call | Adds one or more URLs. When `defer: true`, validated captures are durably queued and the route returns `202` before feed discovery or enrichment; otherwise existing synchronous behavior applies. When `enrich` is true, the synchronous request can fetch remote content and summarize it. |
| `/api/tag` | `POST` | Write DB; update FTS | Updates item or source tags. Source tag writes re-sync source FTS. |
| `/api/media/signed-url` | `GET` | Read DB; archive access | Returns a short-lived archive URL, proxy URL, media type, and expiry without exposing bucket/key or local source path. |
| `/media/asset/<id>` | `GET`, `HEAD` | Read DB; archive access | Proxies archived media from configured S3-compatible storage. Supports range requests. |
| `/share/{slug}` | `GET`, `HEAD` | Public share read | Renders a stored public chat answer without booting or linking the protected application surface. Mutation methods are not accepted. |

The bounded `dbrain audit all --profile deep` archive-download and full media
inventory checks are CLI-only. This capability is not exposed by a web route,
MCP tool, or browser control, and adding it would require a separate capability
and authentication review.

Scheduled fast and standard audits persist private report/history and
transition state for authenticated consumers. The admin GET routes only read
that history; page loads cannot start an audit. Authenticated POST may start a
separate bounded fast or standard run without invoking scheduler alert,
webhook, or metric side effects. The scheduled and on-demand paths share one
report-store instance when composed in the same process. The audit remains
read-only and receives archive-list authority only; the separate SQLite archive
scheduler is the only sibling with object-write authority.

The authenticated System page performs only the three saved-report GETs on
initial load: standard latest, standard history, and fast latest. Fast and
standard run POSTs require an explicit operator action. Polling uses only the
opaque locally accepted process-run ID, is sequential with bounded backoff, and
is aborted when the page is destroyed. The standard exact-profile report alone
controls the health headline; fast results, source-backlog drain state, and
source-arrival quiet periods cannot replace or recover it.

## Open-Source Notes

- Treat local `serve web` and remote `serve remote --web` as trusted write
  surfaces, not as read-only viewers.
- Funnel refuses to start the web surface without `auth.enabled=true`. Do not
  expose remote web through another public proxy unless dbrain-level
  authentication is configured and the full route surface has been reviewed
  for that deployment.
- All non-`GET`/non-`HEAD` application requests share one Origin guard. Missing
  Origin remains allowed for CLI clients; a supplied Origin must match the
  direct request origin. Browser-extension origins are limited to `/api/links`.
  The guard deliberately does not trust forwarded-host headers.
- Audit routes exist only inside the browser-session-authenticated application
  mux. They are not mounted under `/share/`, are not included in the
  doctor-only service-auth allowlist, and explicit `/api/audit` sinks return
  `404` rather than the SPA when web authentication is disabled.
- Combined remote web/MCP startup rejects configurable MCP paths that overlap
  `/api/audit`, `/share`, or their parent/descendant namespaces, so MCP dispatch
  cannot shadow an authenticated audit route or appear beneath public shares.
- Bootstrap, transcript-save, detail media, and signed media URL responses avoid
  absolute host paths and archive bucket/key details. Detail responses still
  include note-relative paths and note-read diagnostics for operator
  troubleshooting.
- If a read-only web mode is added later, enforce it in the Go route layer so
  mutation endpoints are unavailable server-side.
