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
- `internal/sourceenrich/run.go` contains candidate handling, fetch policy,
  fallback extraction, failure classification, summarization, concurrency,
  progress, persistence, and note rendering in one large package file.
- `internal/syncjob/run.go` and `internal/app/sync.go` have very wide option
  surfaces and duplicate stage/config interpretation.
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
- `docs/open-source-license-review.md` records the dependency-license scan and
  the remaining root license/notice decisions.
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
- MCP stdio transport and JSON-RPC protocol dispatch now live outside
  `internal/mcpserver/server.go`, making transport, protocol, and tool behavior
  easier to review separately.
- MCP tool dispatch, tool schemas, and tool result/formatting helpers have also
  been split out of `internal/mcpserver/server.go`.
- `internal/store/store.go` has been narrowed by moving schema/bootstrap logic
  into `internal/store/schema.go` and item/source search plus FTS helpers into
  `internal/store/search.go`, while keeping `store.Store` as the public handle.
- Store open/read-only setup now lives in `internal/store/open.go`, long SQL
  candidate predicates live in `internal/store/predicates.go`, and source
  enrichment progress tracking/logging lives in `internal/sourceenrich/progress.go`.

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
   - In README, separate current behavior from TODO/planned behavior. Consider
     moving large TODO blocks into issues or a roadmap doc.

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
   - Document the remote trust model next to `serve remote`: Tailscale ACLs
     govern access, and remote web should not be treated as a public or
     unauthenticated read-only viewer.
   - Explicitly decide whether read-only web is in scope. If it is not, remove
     stale docs that imply such a mode exists.
   - If a read-only mode is later added, wire it at the route layer so mutations
     are unavailable server-side, not merely hidden in the UI.
   - Add a user-visible planner toggle or document the default planner behavior
     in README and first-run/web UI copy. Cover both web and MCP research.

3. Audit tracked artifacts, local-data hygiene, and host metadata exposure.

   Evidence:
   - `.gitignore` excludes `/data/`, `/vault/`, `/tmp/`, `.envrc`, `.gocache/`,
     `.gomodcache/`, `/bin/`, and `web/ui/node_modules/`.
   - `web/ui/dist` is tracked and embedded by `//go:embed all:ui/dist` in
     `web/server.go`.
   - `internal/mediaarchive` and `internal/sqlitearchive` use S3-compatible
     archive configuration and credentials.
   - Web APIs return local or storage-specific metadata in places such as
     `/api/bootstrap`, transcript-save responses, note-read errors, and archive
     media/signed-url responses.

   Cleanup:
   - Confirm the tracked web build is intentional for Go embedding and releases.
   - Add a short "private local data" section to README before open source.
   - Confirm no sample docs or tests reference private absolute paths, secrets,
     tailnet hostnames, or corpus content.
   - Audit S3/R2 archive docs, tests, config examples, and defaults for leaked
     bucket names, credential assumptions, or surprising network access.
   - Review externally visible web payloads for absolute host paths, local
     source paths, archive bucket/key values, and storage identifiers. Keep
     developer diagnostics available, but avoid exposing more host-local metadata
     than the UI needs.

4. Clarify destructive or non-append-only maintenance paths.

   Evidence:
   - Product rules say imports are append-only by default.
   - `internal/store/cleanup.go` supports physical item/source deletion.
   - `internal/youtubeimport/run.go` has a `pruneHistorySignals` path for
     deprecated `youtube_history` rows.
   - Apple Notes exclusions use explicit forget/purge semantics.

   Cleanup:
   - Document every command/path that can delete or purge local memory rows.
   - Ensure destructive operations are opt-in, named clearly, and excluded from
     generic `sync all` behavior unless explicitly configured.

5. Complete the open-source license and notice pass.

   Evidence:
   - `docs/open-source-license-review.md` records the 2026-05-04 dependency
     scan and remaining review items.
   - The repository currently has no root `LICENSE` or `NOTICE` file.
   - The `./cmd/dbrain` runtime graph did not show GPL/AGPL/SSPL-style
     dependencies in the targeted scan, but the warmed module cache and `go.sum`
     include GPL-licensed lint/tooling modules.

   Cleanup:
   - Choose and add the project license before publishing.
   - Generate a third-party notice file for Go runtime dependencies and frontend
     dependencies.
   - Rerun the audit from a clean checkout and keep lint/tooling dependencies
     separate from shipped runtime dependencies.

### P1: Structural Cleanup With Low Behavior Risk

These reduce maintenance burden without requiring major schema changes.

1. Fix known source FTS error handling before broader store refactors.

   Evidence:
   - `internal/store/sources.go` returns `nil` when `DELETE FROM sources_fts`
     fails in `syncSourceFTSByIDTx`.
   - The same function also returns `nil` when the follow-up `INSERT INTO
     sources_fts` fails.

   Risk:
   - Source search indexing failures can be silently reported as success.
   - A later store refactor could obscure the small bug and make it harder to
     verify the intended FTS behavior.

   Cleanup:
   - Return wrapped errors from both FTS delete and insert failures.
   - Add a narrow regression test if practical; otherwise document why SQLite FTS
     failure injection is not worth the test complexity.

2. Add pipeline predicate and retry-policy guardrails.

   Evidence:
   - Worker candidate selectors and dashboard stats depend on shared source/item
     predicates staying aligned.
   - SQLite writes use busy retry behavior, while source extraction has its own
     retry cooldowns and terminal/final-attempt thresholds in
     `internal/store/sources.go`.

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
     item links, item enrichments, media, archive state, retry, and stats.
   - `internal/store/store.go` and `internal/store/sources.go` remain large and
     hold schema/migration code, FTS, upsert/query logic, and source-specific
     pipeline SQL.
   - Other packages depend directly on broad `store.Store` behavior.

   Cleanup:
   - Keep `store.Store` as the public handle initially.
   - Target `store.go` and `sources.go` first: schema/migrations, item
     repository, source repository, FTS/search, and pipeline predicates.
   - Move source-specific predicates into named policy objects while preserving
     one shared predicate source for workers and dashboards.

7. Introduce typed status and stage constants.

   Evidence:
   - Statuses such as `ok`, `error`, `blocked`, `pending`, `dead`, `gone`, and
     source-specific failure kinds appear as raw strings across store, workers,
     importers, and stats.

   Cleanup:
   - Add typed constants for item enrichment statuses, source extract statuses,
     source summary statuses, and pipeline stage names.
   - Prefer helper predicates over open-coded string comparisons.
   - Keep database values stable to avoid a risky migration.

8. Make config loading a typed runtime snapshot.

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
   - `internal/syncjob/run.go` has a large `Options` struct and a hand-coded
     orchestration sequence.
   - `internal/app/sync.go` has a large CLI flag/config adapter.
   - Several stages have their own limit, force, concurrency, dry-run, progress,
     and summary semantics.

   Cleanup:
   - Land the X media/OCR limit wiring fix before this refactor.
   - Group options by stage, for example `XOptions`, `AppleNotesOptions`,
     `SafariTabsOptions`, `SourceOptions`, `ArchiveOptions`.
   - Represent `sync all` as an ordered stage plan with explicit dependencies,
     enabled predicates, run functions, and stats.
   - Keep the current order and bounded follow-up behavior until tests prove a
     safer refactor.

2. Decompose source enrichment.

   Evidence:
   - `internal/sourceenrich/run.go` is the largest single implementation file.
   - It owns candidate selection, local-cache use, HTTP/reader fetching,
     Wayback, summarize CLI calls, failure policy, concurrency, persistence, and
     note rendering.
   - `internal/sourceenrich/protectedfetch.go` has already been extracted, so
     this cleanup should focus on the remaining orchestration and policy code in
     `run.go`, not restart a solved split.

   Cleanup:
   - Split internal functions and add regression tests around failure policy and
     freshness before introducing new interfaces.
   - Extract a `SourceExtractor` interface only once the current fallback order
     is covered by tests. Likely implementations include local item cache,
     direct summarize extraction, HTTP reader, protected fetch, Wayback, YouTube
     transcript, and stored extract reuse.
   - Extract a failure policy package that maps errors and content conditions to
     retryable, blocked, dead, gone, or success states.
   - Extract summary freshness and summary execution from extraction.
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
   - `internal/ask` builds evidence for CLI/research-style answers.
   - `internal/brainresearch` wraps `ask` into research packs.
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

1. Versioned schema migrations.

   Current schema migration is mostly idempotent `CREATE TABLE` and `ALTER TABLE`
   style code. That is pragmatic for local development, but open-source users
   will benefit from explicit migration versions, migration tests, and downgrade
   guidance. The current item/source column backfill helpers iterate Go maps, so
   migration order is non-deterministic even though today's column additions are
   independent. A versioned migration path should use deterministic ordering.

2. Oversized-source preprocessing.

   Pipeline rules say oversized extracts should become blocked until chunking
   exists. A future chunking/preprocessing stage should be explicit rather than
   hidden inside summarization retry behavior.

3. Provider policy and local/hosted execution modes.

   Local inference should remain viable. Hosted OpenRouter paths are useful for
   burst/catch-up work, but model calls should be easy to audit by stage, model,
   and provider.

4. Entity/topic indexing strategy.

   `internal/entities` and `internal/topics` build derived views on demand. If
   these grow, consider a persisted derived index with explicit rebuild/repair
   commands.

5. Web UI build/release flow.

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
   - Split store implementation files by repository/predicate/stats domains.
   - Split MCP protocol/tool/payload files.
   - Group sync options by stage while preserving current command behavior.

4. Decompose source enrichment.

   Output:
   - Extractor interfaces.
   - Failure policy tests.
   - Summary policy tests.
   - Same fallback order as today.

5. Plan and migrate data-model improvements.

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
- Raw evidence preservation is covered by tests for the main item-level
  enrichment paths.
- Large files remain large only when the responsibility is inherently complex,
  not because unrelated architecture layers are mixed together.
