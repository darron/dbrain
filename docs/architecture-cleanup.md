# Architecture Cleanup Review

Status: draft for review
Date: 2026-05-04
Source of truth: current code in this checkout. Existing docs are background only
where they match implementation.

This document reviews the current `dbrain` architecture and proposes cleanup
work before open sourcing. It is intended to be evaluated by humans and other
LLMs, so it calls out code evidence, risks, and concrete cleanup directions.

## Reviewer Instructions

When reviewing this document, check it against the code, not against older
planning docs. In particular:

- Identify claims that are not supported by the current implementation.
- Identify cleanup items that are over-prioritized, under-prioritized, or
  missing.
- Challenge any proposed abstraction that does not reduce real complexity.
- Preserve the product rules: local-first storage, import-only upstream
  integrations, append-only memory by default, raw evidence preserved separately
  from summaries/OCR/transcripts, and evidence-grounded research/chat.

Suggested prompt for a second LLM:

> Review `docs/architecture-cleanup.md` against this repository's current code.
> Treat code as truth and docs as potentially stale. Return concrete corrections,
> missed architecture risks, and a revised priority order. Do not propose broad
> rewrites unless they reduce specific complexity in the current code.

## Current Architecture From Code

`dbrain` is a Go-first, single-binary local second-brain system with an embedded
Svelte web UI.

The main control flow is:

- `cmd/dbrain/main.go` delegates to `internal/app`.
- `internal/app` owns Cobra command wiring, config loading, command flags,
  environment help, and top-level orchestration.
- `internal/config` owns filesystem layout for config, data, cache, temp, logs,
  database, and vault paths.
- `internal/runtimeenv` resolves runtime settings from environment variables,
  `.envrc`, `.env`, and `config.yaml`.
- `internal/model` defines shared item, source, media, and search-result types.
- `internal/store` owns SQLite schema creation/migration, repositories, FTS,
  pipeline predicates, stats, search, source records, media records, and many
  source-specific query policies.
- `internal/itemhash` calculates item content hashes for deduplication and
  change detection.
- `internal/vault` renders local Markdown projections for items and sources.
- Importers materialize upstream evidence into local rows and notes:
  `internal/xapi`, `internal/applenotes`, `internal/safaritabs`,
  `internal/githubimport`, and `internal/youtubeimport`.
- Source-specific helpers such as `internal/xpost` keep upstream parsing and
  normalization out of the top-level orchestrators.
- Enrichment packages add derived local work:
  `internal/linkextract`, `internal/sourceenrich`,
  `internal/xmediatranscribe`, `internal/xphotoocr`,
  `internal/itemcategorize`, `internal/linkadd`, `internal/mediadownload`,
  and repair packages such as `internal/noterepair`.
- External inference and vocabulary helpers include `internal/summarizecli`,
  `internal/summaryconfig`, `internal/categoryvocab`, and
  `internal/queryterms`.
- Archive-related packages include `internal/mediaarchive` for media archives
  and `internal/sqlitearchive` for SQLite archive storage; archive access also
  appears in `web/archive_media.go` and image categorization paths.
- `internal/syncjob` orchestrates multi-stage `sync all` runs.
- `internal/worker` runs the source-enrichment worker loop.
- `internal/ask` is the lower-level retrieval and evidence-ranking engine.
- `internal/brainresearch` builds higher-level research packs on top of `ask`.
- On-demand derived views such as `internal/entities` and `internal/topics`
  support entity expansion and topic maps for retrieval/research flows.
- `internal/mcpserver` exposes a read-only MCP surface over stdio and
  JSON-RPC-over-HTTP POST; the HTTP handler can also be mounted through tsnet.
- `web` exposes the embedded Svelte UI and JSON/SSE APIs. The current web API is
  read/write.
- `internal/remote` serves the web UI and MCP over tsnet/Tailscale.
- `internal/mcpeval` is an MCP retrieval evaluation harness used by tests and
  development tooling rather than the main runtime path.

The core state model is:

- SQLite is the authoritative working database.
- Rendered Markdown in the vault is the human-facing working surface.
- Remote services are used for import, inference, archive, or durability.
- Source apps and websites are not the source of truth after evidence is
  imported.

## What Looks Healthy

Several architectural choices are aligned with the intended product:

- The system is still a single Go binary at the control plane.
- Local SQLite and local Markdown remain central.
- MCP uses `store.OpenReadOnly`, and tests verify read-only search behavior.
- Apple Notes and Safari tabs are implemented as snapshot-based, import-only
  sources.
- X quoted posts are first-class items with explicit quoted-post links.
- Source summary freshness supports model-agnostic coverage by default through
  `source_summary_versions`.
- `syncjob` uses bounded quote/frontier follow-up passes instead of unbounded
  recursion.
- `.gitignore` excludes local data, vault, cache, CLI build output, local env,
  and temp directories.
- The generated environment docs in `internal/app/env_docs.go` correctly call
  remote web read/write and MCP read-only.

## Main Cleanup Themes

The architecture is functional, but the main pressure points are:

- `internal/store` has become the schema, repository layer, search layer,
  pipeline predicate registry, stats engine, and source-specific policy holder.
- `internal/sourceenrich/run.go` now contains the public entry points, while
  `process.go` and `fallback_flow.go` hold the main per-source fallback flow.
  Option defaults, candidate selection, worker concurrency, persistence/note
  rendering, summary execution/freshness/prompt/skip policy, failure
  persistence/classification/preflight, extract validation, YouTube audio
  fallback, HTTP reader, Wayback, Sucuri protected fetch, WordPress recovery,
  HTML extraction, and progress logging live in focused files.
- `internal/syncjob` now separates public types, option defaults, progress
  formatting, stage-stat merging, stage execution helpers, runner hooks, and X
  frontier helpers from `run.go`. The main `Run` body is now an ordered
  coordinator, but `syncjob.Options` still has a wide flat option surface. The
  `sync all` CLI adapter now has focused files for flag binding, root-env
  resolution, option assembly, progress UI, and summary output.
- Retrieval concepts are split across `ask`, `brainresearch`, `mcpserver/get.go`,
  web handlers, entities, and topics.
- The web docs drifted: the current web surface is read/write, while
  `docs/web-ui-spec.md` still describes an older read-only design.
- Web and MCP research paths currently enable model-assisted planning by default
  unless the client sends `disable_planner: true`; this can create surprising
  model calls when a planner model is configured.
- Several data concepts are represented by transitional fields: X media
  transcripts are stored in item article text with a sentinel title, OCR is in
  item-level fields, and model/category tags share the `user_tags` storage path.

## Priority Cleanup Plan

### Completed In The 2026-05-04 Cleanup Pass

- `docs/web-route-capabilities.md` now documents the current read/write web
  route surface, model-call paths, local-file writes, and archive metadata
  exposure. `docs/web-ui-spec.md` is marked as a historical first-slice design.
- `README.md` now has a top-level safety and trust model section covering
  private local state, import-only upstream behavior, read/write web and remote
  trust boundaries, model-call surfaces, archive storage exposure, and local
  destructive/reset maintenance paths.
- `docs/open-source-license-review.md` records the dependency-license scan,
  MIT project-license decision, third-party notice inventory, and remaining
  release-archive license checks.
- Web bootstrap, chat transcript save, detail media, note-read error, signed
  media URL, and MCP search/get/resource responses no longer expose root/vault/DB
  paths, absolute transcript paths, archive bucket/key values, or local media
  source paths.
- Source FTS delete/insert failures now return wrapped errors, with regression
  tests around source tag reindexing.
- `dbrain sync all` now has separate `--x-media-limit` and
  `--x-photo-ocr-limit` flags; both fall back to `--x-limit` when left at 0.
- Brain research planner and synthesis temp prompt files now use the configured
  dbrain temp directory.
- MCP initialize responses now use build-derived `internal/version` metadata
  instead of a hardcoded server version.
- `web/server.go` has been split into focused static, read, stats, research,
  chat transcript, mutation, utility, and API type files while preserving
  `web.NewHandler` as the route entry point.
- Chat transcript saving now keeps HTTP request handling separate from
  transcript Markdown rendering, research-pack formatting, evidence rendering,
  and filename/text helpers.
- Archive media serving now keeps web handlers separate from S3 proxy setup,
  archived-asset lookup/URL helpers, and archive response header/error helpers,
  with helper coverage for ID parsing, signed URL TTLs, and response headers.
- MCP stdio transport and JSON-RPC protocol dispatch now live outside
  `internal/mcpserver/server.go`, making transport, protocol, and tool behavior
  easier to review separately.
- MCP tool dispatch, tool schemas, and tool result/formatting helpers have also
  been split out of `internal/mcpserver/server.go`.
- MCP tool dispatch now stays in a small dispatcher while search, get/get-many,
  graph/entity/topic/related, and stats tool handlers live in focused files.
- MCP tool definitions and schema helpers are split from output schema builders,
  and MCP resource catalogs, URI dispatch, concrete resource readers, stats
  readers, and query/JSON helpers now live in focused files.
- MCP output schemas are now split by core lookup/search, research pack, graph,
  and stats schema families.
- MCP prompt handling now separates JSON-RPC handlers, prompt catalog
  definitions, prompt template rendering, and argument coercion helpers.
- MCP tool result/error shaping, search result formatting, graph formatting,
  stats formatting, source-type filtering, and small utility helpers are split
  out of the former helper catchall.
- `internal/mcpeval/run.go` now remains the eval report runner while case DTOs,
  case loading/examples, per-case assertions, retrieval routing, and utility
  helpers live in focused files.
- MCP `dbrain_get` payload assembly now separates lookup coordination, content
  section selection, evidence query-windowing, related item/source sections,
  slim metadata, and text formatting while preserving the existing tool schema
  and JSON fields.
- `internal/store/store.go` has been fully decomposed while keeping
  `store.Store` as the public handle: schema/bootstrap logic moved into
  `schema.go`; item/source search, tag search, match counts, and FTS helpers
  moved into focused search/FTS files; and item/X write paths moved into
  focused files.
- Store open/read-only setup now lives in `internal/store/open.go`, long SQL
  candidate predicates live in `internal/store/predicates.go`, and source
  enrichment progress tracking/logging lives in `internal/sourceenrich/progress.go`.
- Item row decoding and shared item column selection now live in
  `internal/store/item_scan.go`; item lookup/list/tag helpers now live in
  `internal/store/item_read.go`.
- Item writes now live in `internal/store/item_write.go`; item enrichment-field
  preservation and DB time formatting now live outside the upsert transaction;
  item enrichment queries, summary writes, OCR writes, purge, item FTS sync, X
  hydration candidate/save/link/invalidation paths, X media transcription state,
  and item link metadata helpers are split into focused store files.
- Media store behavior now has focused schema, download persistence, item
  reference, archive/prune candidate, archive write, archive lookup, X hydration
  media sync, X hydration media decode/merge, and raw media extraction files.
- Source enrichment store behavior now separates candidate selection,
  extraction persistence, preferred local extract lookup, and summary
  persistence into focused files.
- Store stats now separate DTOs, count queries, activity summaries, backlog
  summaries, shared count helpers, pipeline assembly, item-level pipeline rows,
  X media/OCR pipeline row helpers, pipeline aggregation helpers, source
  activity feed assembly, source activity row scanning, source activity SQL
  builders, source activity SQL union bodies, and trend shaping.
- The former `internal/store/sources.go` catchall has been decomposed into
  focused source schema, source link/upsert, enrichment persistence, scan/read,
  lookup/evidence/relation/tag helpers, search/FTS/scanning, predicate, repair
  filters, X article repair reset, and X article preview files.
- SQLite startup now runs through an ordered migration registry in
  `internal/store/migrations.go`. The checked-in current schema is recorded as
  baseline version 1 in `schema_migrations` and `PRAGMA user_version`; tests
  cover fresh create, idempotent reopen, adopting the existing current schema
  without migration metadata, and keeping `OpenReadOnly` migration-free.
  Startup/read-only pragmas and table-existence checks now live separately from
  current schema definition and column backfill helpers.
- Store source-extraction predicates now keep backlog/staleness SQL builders
  separate from stored failure state and failure-kind classification helpers.
- Source enrichment extraction, summary, and worker-candidate predicates now go
  through a named source-enrichment policy, with regression coverage that keeps
  backlog counts aligned with extraction-only and summarize-enabled worker
  selectors.
- Source extract statuses, source summary statuses, and source failure-kind
  strings now have shared constants in `internal/model/source_status.go`, and
  the source enrichment and core store policy paths use them instead of
  open-coded strings.
- Pipeline aggregate and item-level stage kinds now have named store constants
  at stats row assembly points.
- `internal/sourceenrich/run.go` has been narrowed to public entry points.
  Source summary execution/freshness/prompt/skip policy, summary content/media
  skip-policy helpers, extraction failure persistence/classification/preflight,
  extract validation and cleanup, YouTube audio transcription fallback, option
  defaults, worker concurrency,
  audio transcriber command helpers,
  persistence/note rendering, selection helpers, progress tracking,
  process/fallback flow, HTTP reader, Wayback, Sucuri protected fetch, WordPress
  recovery, and HTML extraction now live in focused files while preserving the
  existing fallback order.
- `internal/syncjob/run.go` has been narrowed into an ordered stage
  coordinator. Public options/stats types, default option normalization,
  progress formatting, stage-stat merging, local import stages, X
  bookmark/hydrate/link frontier helpers, media/import/source/archive stages,
  categorization, and runner hooks now live in focused files while preserving
  the current `sync all` order and bounded X follow-up passes.
- `internal/app/sync.go` has been narrowed to command execution. Sync flag
  binding, root-env/config resolution, sync flag env resolution,
  `syncjob.Options` assembly, progress UI, and summary table rendering now live
  in focused `internal/app/sync_*.go` files, with tests covering root `.env`
  sync option resolution.
- App-level stats command wiring now separates root command construction,
  count/activity command bodies, backlog/pipeline command bodies, general stats
  output, and pipeline table rendering. Sync progress UI construction,
  stage/animation rendering, progress parsing, and log-line mechanics are
  separated.
- Apple Notes import command wiring, progress/stat output, and debug
  probe/snapshot/decode subcommands are now separated in `internal/app`.
- Safari tabs import command wiring now keeps command execution separate from
  progress/stat output rendering.
- Apple Notes reader code now separates low-level DB row value coercion,
  object/note-data row loading, note document assembly, attachment-row decoding,
  attachment path/URL coercion, and link/tag/identity text helpers from the
  snapshot decode flow.
- Apple Notes import runtime now separates `Run` orchestration from stats/types,
  run setup/progress event construction, work planning, progress dispatch,
  exclusion purge handling, item render/summary apply helpers, summary
  execution, item materialization, attachment enrichment substeps, and
  snapshot/probe filesystem and read-only SQLite helpers.
- Serve command wiring is split by MCP, remote tsnet, and plain web surfaces
  while preserving existing flags and defaults.
- SQLite archive/restore app command wiring now keeps command bodies separate
  from S3 option/env resolution and restore confirmation helpers.
- App-level `tsnet` state commands now keep command wiring separate from status
  computation, status DTO/dependency types, endpoint health aggregation,
  endpoint probing, certificate-state reads, flag override handling, reset
  confirmation, probe URL construction, tailnet IP lookup, and HTTP/TLS probe
  execution.
- Categorization command wiring now separates item command, source command,
  analysis command, analysis token counting, and draft YAML rendering surfaces.
- Repair command wiring now keeps FTS/note repair commands separate from source
  reset lookup, preview, confirmation, and output flow.
- `internal/runtimeenv` now keeps public scalar lookup in a small facade while
  bool/list helpers, env-file loading, YAML config decoding, and config key-path
  expansion live in focused files.
- `internal/linkextract/run.go` now remains the link-discovery/source-enrichment
  coordinator while option/stat DTOs, candidate collection, URL normalization,
  source classification, and hashing/slug/logging helpers live in focused files,
  with direct coverage for common URL canonicalization cases.
- MCP HTTP serving now keeps the server lifecycle and POST handler separate from
  path, origin, and endpoint URL helpers.
- MCP research-pack handling now keeps tool argument decoding and pack building
  separate from human-readable pack formatting.
- `internal/remote/server.go` now remains the remote serve lifecycle
  coordinator while handler assembly, listen/error lifecycle helpers, the tsnet
  node adapter, request identity logging/user auth URL logging, and advertised
  URL rendering live in focused files.
- `internal/ask/run.go` now remains the retrieval facade while query hints,
  evidence shaping, scoring, prompt input writing, entity expansion, entity
  query/scoring policy, excerpt assembly, excerpt windowing/scoring, and small
  utilities live in focused package files.
- `internal/brainresearch/research.go` now remains the research-pack builder
  while pack DTOs, deterministic/model-assisted strategy helpers, evidence
  reranking/scoring, topic inference, coverage, exact-tag examples, search
  filtering, strategy concept/variant derivation, next-step suggestions, and
  utilities live in focused package files.
- `internal/brainresearch/planner.go` now remains the model-planner execution
  path while planner JSON parsing/sanitization and deterministic/model merge
  rules live in focused files.
- `internal/brainresearch/synthesize.go` now remains the public synthesis
  prepare/run facade while prepared synthesis execution, prompt-input packing,
  evidence budget/truncation accounting, citation collection, synthesis
  evidence input formatting, and small synthesis helpers live in focused files.
- App-level research command wiring now keeps retrieval/synthesis flow separate
  from human-output rendering helpers.
- `internal/summarizecli/client.go` now remains the summarize runner while
  direct Ollama/OpenRouter calls, direct input/target/response helpers, command
  retry/timeout behavior, provider selection, version probing, model/env
  resolution, and shared DTOs live in focused files.
- `internal/xapi/xapi.go` now remains the X hydration coordinator while fetch
  policy, quoted-post tree persistence, client/cookie handling, GraphQL and
  syndication fetch paths, TweetResult request metadata, GraphQL/syndication
  snapshot parsing, and shared utilities live in focused files.
- X bookmark import now separates the overlap-aware run loop from GraphQL
  request/retry code, timeline parsing, bookmark DTOs, and item materialization
  helpers while preserving the native cookie-backed GraphQL flow.
- `internal/itemcategorize/run.go` now remains the single item/source
  categorization runner while batch orchestration, DTOs, content bundles,
  photo/S3 loading, LLM transport/response parsing, option resolution, tag
  merging, and small utilities live in focused files.
- `internal/entities/entities.go` now remains the entity indexing/search facade
  while item/source derivation, relationship inference, identity-token
  normalization, builder state, path construction, and parsing/matching helpers
  live in focused files.
- `internal/topics/topicmap.go` now remains the topic map builder while graph
  node resolution, source-type filtering, entity scoring rules, pivots,
  formatting, shared DTOs, and small utilities live in focused files.
- `internal/topics/synthesis.go` now remains the topic synthesis coordinator
  while evidence collection, section rendering, signal clustering, signal
  phrase extraction, signal stopword policy, shared types, and synthesis
  utilities live in focused files.
- App-level topic command wiring now keeps the root command, map/generate
  command bodies, refresh/index command bodies, refresh definition resolution,
  and topic index rebuild helpers separate.
- `internal/vault/vault.go` now keeps path/stat helpers while item note
  rendering, item frontmatter/source Markdown sections, media/archive embeds,
  quoted-post rendering, entity note writes, entity Markdown rendering,
  YAML/text helpers, and render-option resolution live in focused files.
- Vault topic note code now separates note write coordination, Markdown
  rendering, topic index rendering, definition/frontmatter parsing, and path
  helpers, with round-trip coverage for generated topic definitions and index
  links.
- `internal/githubimport/run.go` now remains the GitHub star import coordinator
  while API transport, item materialization, repo extraction, source enrichment,
  and utility helpers live in focused files.
- `internal/youtubeimport/run.go` now remains the YouTube signal import
  coordinator while feed execution, history cleanup, item/source shaping,
  enrichment callbacks, browser-profile discovery, and utility helpers live in
  focused files.
- `internal/safaritabs/run.go` now remains the Safari tab import coordinator
  while CloudTabs query code, snapshot DB opening/validation, item
  materialization, device matching, progress, and time/hash helpers live in
  focused files.
- `internal/xphotoocr/run.go` now remains the X photo OCR worker coordinator
  while per-item persistence, model/provider routing, hosted/local OCR calls,
  option resolution, and shared result helpers live in focused files.
- `internal/xphotoocr/compare.go` now remains the compare devtool coordinator
  while sample collection, temp input/download handling, per-model execution,
  overlap scoring, and Markdown report rendering live in focused files.
- `cmd/devtools/restore_pruned_pending_x_media` now keeps restore command
  orchestration separate from pending-work SQL and ID loading helpers.
- `internal/xmediatranscribe/run.go` now remains the X media transcription
  coordinator while option normalization, media/audio eligibility, external
  command execution, transcript classification/rendering, summary input/error
  helpers, persistence, and logging helpers live in focused files.
- `internal/mediadownload/run.go` now remains the per-item media download
  coordinator while download policy, HTTP transfer, content-addressed path
  selection, and extension/type helpers live in focused files.
- `internal/sqlitearchive` now separates archive, latest-selection, restore,
  SQLite snapshot/validation, gzip/file movement, object key, and progress
  helpers while preserving the existing archive/restore APIs.
- `internal/mediaarchive/run.go` now remains the media archive coordinator while
  option normalization, archive result shaping, reference-aware local pruning,
  note refresh, and logging helpers live in focused files.

### P0: Open-Source Readiness

These should happen before publishing the repo because they affect user trust,
privacy, and first-run understanding.

1. Publish code-accurate architecture docs.

   Evidence:
   - `docs/web-ui-spec.md` says the web UI is read-only and has no mutation
     APIs.
   - `web/server.go` exposes write routes such as tag updates, link adds, and
     chat transcript saves.
   - `README.md`, `docs/tsnet-transport.md`, and `internal/app/env_docs.go`
     correctly describe remote web as read/write.

   Cleanup:
   - Add or promote a concise `docs/architecture.md` based on the architecture
     map above.
   - Keep `docs/web-route-capabilities.md` current as the code-accurate web
     route map; `docs/web-ui-spec.md` is now marked as a historical first-slice
     design.
   - In README, continue separating current behavior from TODO/planned
     behavior. Consider moving large TODO blocks into issues or a roadmap doc.

2. Make the web/MCP write and model-call surface explicit and reviewable.

   Evidence:
   - `web/server.go` opens a writable store and exposes mutation endpoints.
   - `internal/remote/server.go` warns that remote web is read/write.
   - `docs/tsnet-transport.md` correctly frames remote web as a trusted tailnet
     administration surface.
   - `web/server.go` sets `UseModelPlanner` to `req.UseModelPlanner ||
     !req.DisablePlanner`, so web research uses model-assisted planning by
     default unless the client opts out.
   - `internal/mcpserver/research.go` uses the same planner default, and
     `web/ui/src/lib/api.js` sends `use_model_planner: true` by default.
   - `internal/brainresearch` falls back to deterministic planning when no
     planner model resolves.

   Risk:
   - Open-source users may assume the web UI is a local viewer because older docs
     say so.
   - Remote web exposure relies on Tailscale/tsnet access control. There is no
     separate dbrain login or per-route authorization layer, so the docs must
     make clear that remote web is a trusted tailnet administration surface.
   - A research request can easily trigger model calls when a model is configured
     without an obvious first-run or per-request disclosure.

   Cleanup:
   - Keep the exact write routes documented in
     `docs/web-route-capabilities.md`.
   - Done: README documents the remote trust model next to `serve remote`:
     Tailscale ACLs govern access, and remote web should not be treated as a
     public or unauthenticated read-only viewer.
   - Explicitly decide whether read-only web is in scope. If it is not, remove
     stale docs that imply such a mode exists.
   - If a read-only mode is later added, wire it at the route layer so mutations
     are unavailable server-side, not merely hidden in the UI.
   - Done: README documents the default planner behavior for web, CLI, and MCP
     research. Remaining: add first-run/web UI copy or controls if needed.

3. Audit tracked artifacts, local-data hygiene, and host metadata exposure.

   Evidence:
   - `.gitignore` excludes `/data/`, `/vault/`, `/tmp/`, `.envrc`, `.gocache/`,
     `.gomodcache/`, `/bin/`, and `web/ui/node_modules/`.
   - `web/ui/dist` is tracked and embedded by `//go:embed all:ui/dist` in
     `web/server.go`.
   - `internal/mediaarchive` and `internal/sqlitearchive` use S3-compatible
     archive configuration and credentials.
   - Web APIs returned local or storage-specific metadata in places such as
     `/api/bootstrap`, item/detail media payloads, transcript-save responses,
     note-read errors, and archive media/signed-url responses.

   Cleanup:
   - Confirm the tracked web build is intentional for Go embedding and releases.
   - Done: README has a private local data section before command details.
   - Confirm no sample docs or tests reference private absolute paths, secrets,
     tailnet hostnames, or corpus content.
   - Audit S3/R2 archive docs, tests, config examples, and defaults for leaked
     bucket names, credential assumptions, or surprising network access.
   - Done: `/api/bootstrap`, item/detail media payloads, chat transcript save,
     note-read errors, signed media URL responses, and MCP search/get/resource
     responses avoid root/vault/DB paths, absolute transcript paths, archive
     bucket/key values, and local media source paths.
   - Continue reviewing externally visible payloads for absolute host paths or
     storage identifiers. Keep developer diagnostics available, but avoid
     exposing more host-local metadata than the UI needs.

4. Clarify destructive or non-append-only maintenance paths.

   Evidence:
   - Product rules say imports are append-only by default.
   - `internal/store/cleanup.go` supports physical item/source deletion.
   - `internal/youtubeimport/run.go` has a `pruneHistorySignals` path for
     deprecated `youtube_history` rows.
   - Apple Notes exclusions use explicit forget/purge semantics.

   Cleanup:
   - Done: README documents known local delete/purge/reset paths: media prune,
     SQLite restore, tsnet reset, Apple Notes `--forget-excluded`, YouTube
     deprecated-history cleanup, and source derived-state repair.
   - Ensure destructive operations are opt-in, named clearly, and excluded from
     generic `sync all` behavior unless explicitly configured.

5. Complete the open-source license and notice pass.

   Evidence:
   - `docs/open-source-license-review.md` records the 2026-05-04 dependency
     scan and remaining review items.
   - The repository uses the MIT License in the root `LICENSE` file.
   - `THIRD_PARTY_NOTICES.md` lists the current `./cmd/dbrain` runtime
     dependencies and frontend lockfile dependencies.
   - The `./cmd/dbrain` runtime graph did not show GPL/AGPL/SSPL-style
     dependencies in the targeted scan, but the warmed module cache and `go.sum`
     include GPL-licensed lint/tooling modules.

   Cleanup:
   - Rerun the audit from a clean checkout and keep lint/tooling dependencies
     separate from shipped runtime dependencies.
   - Regenerate `THIRD_PARTY_NOTICES.md` before release and include exact
     upstream license files for any dependency source or generated asset copied
     into release archives.

### P1: Structural Cleanup With Low Behavior Risk

These reduce maintenance burden without requiring major schema changes.

1. Keep source FTS error handling protected through broader store refactors.

   Evidence:
   - `internal/store/source_search.go` owns `syncSourceFTSByIDTx`, and the
     current implementation returns wrapped errors when either the
     `DELETE FROM sources_fts` or follow-up `INSERT INTO sources_fts` fails.
   - `internal/store/sources_test.go` includes regression tests that drop or
     replace `sources_fts` and assert source tag saves surface the FTS errors.

   Risk:
   - Source search indexing failures should never be reported as successful
     metadata writes.
   - Later store refactors could weaken this coverage if FTS sync behavior is
     moved without the regression tests.

   Cleanup:
   - Keep the delete/insert failure regression tests with any future source FTS
     split.
   - Add direct FTS rebuild/check tests if future schema changes touch
     `sources_fts`.

2. Add pipeline predicate and retry-policy guardrails.

   Evidence:
   - Worker candidate selectors and dashboard stats depend on shared source/item
     predicates staying aligned.
   - SQLite writes use busy retry behavior, while source extraction has its own
     retry cooldowns and terminal/final-attempt thresholds in
     `internal/store/source_predicates.go`,
     `internal/store/source_enrichment_candidates.go`,
     `internal/store/source_extraction.go`, and
     `internal/store/source_summary.go`.

   Cleanup:
   - Add tests that assert candidate selectors and stats use the same stage
     predicates.
   - Add focused tests for retry cooldowns, blocked/terminal statuses, and
     final-attempt thresholds before splitting source enrichment or store
     predicate code.

3. Fix small correctness and local-hygiene issues before structural splits.

   Evidence:
   - `internal/app/sync.go` wires both `XMediaLimit` and `XPhotoOCRLimit` to the
     broader X limit even though `syncjob.Options` has separate fields.
   - `internal/brainresearch/planner.go` and
     `internal/brainresearch/synthesize.go` create temporary prompt files in the
     process temp directory instead of the configured dbrain temp directory.

   Cleanup:
   - Give X media transcription and X photo OCR their own CLI/config limit
     wiring, or remove the separate option fields if they are intentionally tied
     to `XLimit`.
   - Use the configured dbrain temp directory for research planner and synthesis
     prompt files where practical.

4. Split `web/server.go` by route group.

   Evidence:
   - `web/server.go` contains static serving, search, get/detail, stats,
     research, synthesis streaming, chat transcript writes, tag writes, link
     writes, and media proxy handlers.

   Cleanup:
   - Keep one `web.NewHandler` entry point.
   - Move route implementations into focused files:
     `search.go`, `detail.go`, `stats.go`, `research.go`, `mutations.go`,
     `media.go`, and `static.go`.
   - Add an explicit route capability matrix: read-only, writes local DB, writes
     local files, model call, remote fetch, archive access.

5. Split MCP protocol, transport modes, tools, and payload builders.

   Evidence:
   - `internal/mcpserver/server.go` combines JSON-RPC protocol handling, server
     metadata, tool registry, prompts/resources, and tool dispatch.
   - `internal/mcpserver/get.go` is a large DB payload builder.
   - MCP has multiple transport shapes: stdio, local JSON-RPC-over-HTTP POST,
     and HTTP mounted through tsnet.

   Cleanup:
   - Keep the no-extra-daemon, Go-first MCP server.
   - Separate protocol transport, tool definitions, tool implementations, and
     payload builders.
   - Document the transport modes and their operational assumptions near the
     server setup.
   - Use `internal/version` for server metadata instead of hardcoded version
     strings.

6. Split the remaining monolithic store files while keeping a stable facade.

   Evidence:
   - `internal/store` already has focused files for categorization, cleanup,
     item links, item enrichment queries/writes, media schema/download/refs,
     archive state, retry, search/tag/count helpers, stats pipeline coverage,
     and source activity.
   - `internal/store/sources.go` has been eliminated; source-store behavior now
     lives in focused schema, link/upsert, enrichment persistence, scan/read,
     search/FTS, predicate, repair, and X article preview files.
   - Other packages depend directly on broad `store.Store` behavior.

   Cleanup:
   - Keep `store.Store` as the public handle initially.
   - Keep future source-store work in the focused files, and add new
     source-specific files only when a responsibility grows beyond its current
     owner.
   - Move source-specific predicates into named policy objects while preserving
     one shared predicate source for workers and dashboards.

7. Finish migration hardening and publish schema policy before open-source use.

   Evidence:
   - `internal/store/migrations.go` now establishes version 1 as the baseline
     for the current checked-in schema.
   - Because the only pre-release SQLite instance is the current development
     database, version 1 intentionally adopts the current schema rather than
     carrying historical external fixtures.
   - Future non-additive changes, backfills, index changes, FTS rebuilds, or
     data-model migrations still need dedicated migration entries instead of
     expanding the baseline.

   Risk:
   - Open-source users will eventually run different binary versions against
     real personal data.
   - Without a published backup, downgrade, and failure policy, users will not
     know what to do before or after a failed schema migration.

   Cleanup:
   - Add each future schema change as its own ordered migration instead of
     mutating the version 1 baseline.
   - Keep migration tests for fresh create, representative upgrades, idempotent
     reruns, and raw imported evidence preservation.
   - Keep `OpenReadOnly` migration-free for MCP/read-only consumers.
   - Document backup/restore expectations and downgrade policy before publishing.

8. Introduce typed status and stage constants.

   Evidence:
   - Statuses such as `ok`, `error`, `blocked`, `pending`, `dead`, `gone`, and
     source-specific failure kinds appear as raw strings across store, workers,
     importers, and stats.
   - Source extract/summary statuses and source failure-kind strings now have
     shared model constants and are used by source enrichment and core store
     policy paths, including source activity reporting. Pipeline aggregate and
     item-level stage kinds now use store constants where rows are assembled.

   Cleanup:
   - Done for source extract statuses, source summary statuses, and source
     failure kinds in source enrichment, source activity reporting, and core
     store policy paths.
   - Done for pipeline aggregate and item-level stage names at stats row
     assembly points.
   - Done for item summary, OCR, X media transcript statuses, and the
     synthesized X media transcript marker in worker persistence, stats,
     candidate selectors, source local-extract policy, and media archive gating.
   - Remaining: continue replacing reporting-only raw strings when those files
     are next touched.
   - Prefer helper predicates over open-coded string comparisons.
   - Keep database values stable to avoid a risky migration.

9. Make config loading a typed runtime snapshot.

   Evidence:
   - `internal/config` owns paths only.
   - `internal/runtimeenv` does stringly lookup from env, env files, and YAML.
   - Command packages and feature packages interpret config values in many
     places.
   - `internal/app/env_docs.go` is a separate generated-style list of supported
     env keys.

   Cleanup:
   - Keep `runtimeenv.Lookup` as the compatibility layer.
   - Add typed runtime config structs loaded once near command entry:
     summary, OCR, OpenRouter, Ollama, Apple Notes, Safari tabs, tsnet, archive,
     source reader, Wayback, and media proxy.
   - Generate `dbrain config env` help from the typed config metadata so docs do
     not drift.

### P2: Behavior-Preserving Architecture Improvements

These are deeper and should be staged with focused tests.

1. Convert `syncjob` into an explicit stage plan.

   Evidence:
   - `internal/syncjob/types.go` still exposes a large flat `Options` struct,
     and `internal/syncjob/run.go` still contains a hand-coded orchestration
     sequence.
   - `internal/app/sync.go` is now a narrow command runner; flag binding,
     root-env resolution, option assembly, progress UI, and summary output are
     split into focused `internal/app/sync_*.go` files.
   - Several stages have their own limit, force, concurrency, dry-run, progress,
     and summary semantics.

   Cleanup:
   - Land the X media/OCR limit wiring fix before this refactor.
   - Done: move `syncjob` public types, option defaults, progress formatting,
     merge helpers, stage execution helpers, runner hooks, and X frontier pass
     helpers out of `run.go`.
   - Done: move sync flag binding, root-env resolution, option assembly, and
     summary output rendering out of `internal/app/sync.go`.
   - Group options by stage, for example `XOptions`, `AppleNotesOptions`,
     `SafariTabsOptions`, `SourceOptions`, `ArchiveOptions`.
   - Represent `sync all` as an ordered stage plan with explicit dependencies,
     enabled predicates, run functions, and stats.
   - Keep the current order and bounded follow-up behavior until tests prove a
     safer refactor.

2. Decompose source enrichment.

   Evidence:
   - `internal/sourceenrich/run.go` remains the source enrichment entry point,
     while `process.go` and `fallback_flow.go` now make the per-source fallback
     ordering explicit.
   - Option defaults, candidate selection, worker concurrency,
     persistence/note rendering, summary execution/freshness/prompt/skip
     policy, extraction failure persistence/classification/preflight, extract
     validation/cleanup, YouTube audio fallback, HTTP reader, Wayback, Sucuri
     protected fetch, WordPress recovery, HTML extraction, and progress tracking
     are already in focused files.

   Cleanup:
   - Keep the existing failure policy, freshness, summary skip/blocking, and
     YouTube fallback regression tests with the split files.
   - Extract a `SourceExtractor` interface only once the current fallback order
     is covered by tests. Likely implementations include local item cache,
     direct summarize extraction, HTTP reader, protected fetch, Wayback, YouTube
     transcript, and stored extract reuse.
   - Done: add narrow tests around the now-explicit `process.go` fallback
     sequence for stored-extract-before-reader and
     terminal-preflight-before-reader behavior.
   - Next, introduce extractor interfaces only when the next behavior split
     needs them.
   - Keep the current fallback order and add regression tests before changing
     behavior.

3. Normalize item-level enrichment storage.

   Evidence:
   - X media transcripts are currently raw evidence but are stored through item
     article text fields with `article_title` set to an X media transcript
     sentinel.
   - X media transcript status is already tracked separately through dedicated
     status/error/timestamp columns; the transitional part is the content storage
     path.
   - OCR text, item summaries, source summaries, and user/model tags live in
     separate but uneven storage shapes.
   - Source summaries have version records; item summaries and item OCR do not
     have the same version/history model.

   Cleanup:
   - Add a generic `item_enrichments` table or equivalent access layer with
     typed roles: raw transcript, raw OCR, derived summary, model category,
     attachment extract, and so on.
   - Preserve current columns during migration and backfill gradually.
   - Keep rendered notes stable during the transition.
   - Add tests that raw evidence survives summary regeneration and model swaps.

4. Separate user tags from model/category tags.

   Evidence:
   - `user_tags` is used in search, notes, UI tagging, and categorization.
   - `internal/itemcategorize` writes model-derived category/tag outputs into
     the same broad tag field.

   Risk:
   - A user-owned label and a model-derived label have different provenance and
     trust semantics.

   Cleanup:
   - Either rename user-facing copy to "tags" and track assignment provenance,
     or split storage into `user_tags` and `model_tags`.
   - Preserve search behavior by indexing both unless the query explicitly asks
     for one provenance.

5. Move rendering behind a narrow projection boundary.

   Evidence:
   - Importers and enrichers call `vault.WriteItem`, `vault.WriteSource`, or
     related repair/render functions directly.
   - Rendering is a projection of database state but is triggered manually from
     many write paths.

   Cleanup:
   - Add a small renderer service/helper that owns item/source projection
     refresh after writes.
   - Keep synchronous rendering for now so CLI behavior remains predictable.
   - Longer term, consider marking dirty projections in the DB and repairing
     them in a controlled pass.

6. Consolidate retrieval payload construction.

   Evidence:
   - `internal/ask` now separates its retrieval facade from query hints,
     evidence shaping, scoring, prompt input writing, entity expansion, excerpt
     windowing, and small utilities.
   - `internal/brainresearch` wraps `ask` into research packs and now keeps
     strategy planning, evidence reranking, exact-tag examples, topic/coverage
     helpers, and DTOs separate from the top-level pack builder.
   - `internal/mcpserver/get.go` builds separate content sections and related
     payloads.
   - `web/server.go` has its own detail/search payloads.

   Cleanup:
   - Rename or refactor `ask` toward a lower-level `retrieval` package.
   - Define shared types for `EvidenceDocument`, `ContentSection`,
     `RelatedDocument`, and `RetrievalSignal`.
   - Keep presentation-specific formatting in MCP/web/CLI layers.
   - Normalize the maximum exposed payload surface across MCP and web so one
     surface does not accidentally reveal richer local paths, diagnostics, or
     raw content than the other.

### P3: Larger Design Follow-Ups

These are worth planning, but they should not block open sourcing unless they
are already causing operational issues.

1. Oversized-source preprocessing.

   Pipeline rules say oversized extracts should become blocked until chunking
   exists. A future chunking/preprocessing stage should be explicit rather than
   hidden inside summarization retry behavior.

2. Provider policy and local/hosted execution modes.

   Local inference should remain viable. Hosted OpenRouter paths are useful for
   burst/catch-up work, but model calls should be easy to audit by stage, model,
   and provider.

3. Entity/topic indexing strategy.

   `internal/entities` and `internal/topics` build derived views on demand. If
   these grow, consider a persisted derived index with explicit rebuild/repair
   commands.

4. Web UI build/release flow.

   `web/ui/dist` is tracked for embedding. That is acceptable if intentional,
   but release documentation should state when maintainers need to run
   `task web-build` and how to avoid stale embedded assets.

## Specific Code Risks To Verify

These are smaller findings that deserve targeted review:

- `internal/store/cleanup.go`: audit all callers of physical delete helpers and
  document them as explicit maintenance operations.

## Proposed Cleanup Sequence

1. Update docs that are contradicted by code.

   Output:
   - Current architecture doc.
   - Revised or deprecated `docs/web-ui-spec.md`.
   - README section that distinguishes current behavior from roadmap.
   - Route/capability matrix for web and remote serving.
   - Web/MCP model-planner disclosure and web payload privacy audit.

2. Add guardrails before refactors.

   Output:
   - Source FTS error handling fixed or explicitly tracked as a blocking bug.
   - Tests around pipeline predicates versus stats.
   - Tests around source retry cooldowns, blocked/terminal statuses, and
     final-attempt thresholds.
   - Tests around raw evidence preservation for item summaries, OCR, and
     transcripts.
   - Tests around source FTS sync failure behavior if it is changed.
   - Tests around read-only web mode if added.

3. Do low-risk splits without schema changes.

   Output:
   - X media/OCR limit wiring fixed.
   - Brain research temp files moved to configured dbrain temp storage where
     practical.
   - Split `web/server.go` route files.
   - Split store implementation files by repository/predicate/stats/media/item
     enrichment/source-enrichment domains.
   - Split MCP protocol/tool/payload files.
   - Done: split `syncjob` public types, option defaults, progress formatting,
     merge helpers, stage execution helpers, runner hooks, and X frontier
     helpers while preserving current command behavior.
   - Done: split sync flag binding, root-env resolution, option assembly, and
     summary output rendering out of the `sync all` CLI command body.
   - Remaining: group `syncjob.Options` by stage and introduce an explicit
     stage plan.

4. Replace ad hoc schema setup with versioned migrations.

   Output:
   - Done: deterministic ordered migration runner and recorded version 1
     baseline.
   - Done: migration tests for fresh create, idempotent reruns, adopting the
     existing current schema, and read-only open behavior.
   - Remaining: representative future upgrade fixtures, raw evidence
     preservation tests for schema-changing migrations, and documented
     backup/restore and downgrade policy.

5. Decompose source enrichment.

   Output:
   - Done: summary execution/freshness/prompt/skip policy, failure
     persistence/classification/preflight, extract validation, YouTube audio
     fallback, process/fallback flow, HTTP reader, Wayback, Sucuri protected
     fetch, WordPress recovery, HTML extraction, and progress tracking are in
     focused files with existing regression coverage.
   - Remaining: extractor interfaces or fetch/fallback modules once the current
     fallback order is covered tightly enough to make the split low-risk.
   - Same fallback order as today.

6. Plan and migrate data-model improvements.

   Output:
   - Versioned migration plan.
   - Item enrichment model.
   - Tag provenance model.
   - Backfill and compatibility path for existing databases.

## Non-Goals For This Cleanup

- Do not add write-back to upstream applications.
- Do not introduce a SaaS service or required daemon.
- Do not replace SQLite as the local source of truth.
- Do not make model answers durable evidence for later research turns.
- Do not delete local memories as part of ordinary import sync.
- Do not rewrite the app into a heavy SPA or separate backend/frontend service
  split just for architectural cleanliness.

## Acceptance Criteria

This cleanup effort is ready for open-source review when:

- Public docs describe what the binary actually does today.
- A new contributor can identify the storage, import, enrichment, retrieval, web,
  and MCP boundaries from docs and package names.
- Web write behavior is either server-enforced read-only in a mode or documented
  as explicitly trusted/read-write.
- Web- and MCP-triggered model calls are visible in docs or UI controls,
  especially the research planner default when a model is configured.
- Web API payloads do not expose unnecessary absolute host paths, archive
  bucket/key values, local source paths, or storage identifiers.
- S3/R2 archive integrations are documented and free of hardcoded private
  credentials, bucket names, or corpus-specific assumptions.
- Pipeline dashboards and worker selectors are backed by shared predicates or
  tested equivalently.
- SQLite schema changes use ordered, versioned migrations with tests for fresh
  create, representative upgrades, and idempotent reruns.
- Raw evidence preservation is covered by tests for the main item-level
  enrichment paths.
- Large files remain large only when the responsibility is inherently complex,
  not because unrelated architecture layers are mixed together.
