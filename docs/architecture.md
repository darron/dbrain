# dbrain Architecture

Date: 2026-05-05

`dbrain` is a local-first second-brain system. It imports evidence from local
apps and web services, stores that evidence in local SQLite, renders local
Markdown notes for human review, and exposes retrieval/research surfaces over
CLI, web, and MCP.

This document describes the current architecture. For the cleanup audit history,
see `docs/architecture-cleanup.md`.

## Core Principles

- SQLite is the authoritative working database.
- Rendered Markdown is the human-facing working surface.
- Imports are append-only by default: upstream disappearance does not delete
  local memory during ordinary sync.
- Local app integrations are import-only. They read upstream data and
  materialize evidence into dbrain; they do not manage upstream app state.
- Raw imported/extracted evidence remains separate from derived summaries, OCR,
  transcripts, tags, and categories.
- Model answers are derived synthesis. They are not stored as authoritative
  evidence for later research turns.
- Remote services are optional import, inference, archive, or durability tools,
  not the primary source of truth.

## State Model

Primary local state:

- `brain.db`: SQLite working database.
- `vault/items/...`: rendered item notes.
- `vault/sources/...`: rendered source notes.
- `vault/entities/...`: derived entity notes and index.
- `vault/topics/...`: generated topic notes and topic index.
- `media/...`: content-addressed local media files when retained.
- `data/chat-transcripts/...`: non-indexed diagnostic chat transcripts.
- `tmp`, `cache`, and `logs`: local working directories.

SQLite owns canonical rows for items, sources, media assets, relationships,
pipeline state, extracted text, derived enrichments, tags, and archive metadata.
Markdown notes are projections of that state and can be repaired or regenerated.

## High-Level Flow

```text
upstream apps/services
        |
        v
importers  ->  SQLite rows  ->  Markdown projections
        |             |
        |             v
        |       enrichment workers
        |             |
        v             v
raw evidence    derived summaries/OCR/transcripts/tags
        \             /
         v           v
      search, research, web, MCP, topic/entity views
```

## Main Packages

### Command And Runtime

- `cmd/dbrain`: binary entry point.
- `internal/app`: Cobra command wiring, CLI flags, top-level command execution,
  sync/stats/import/serve command adapters, and human CLI output.
- `internal/config`: filesystem layout and path resolution.
- `internal/runtimeenv`: compatibility layer for environment, `.envrc`, `.env`,
  and `config.yaml` runtime lookups.
- `internal/version`: build/version metadata.

### Storage And Projection

- `internal/model`: shared item, source, media, status, and search DTOs.
- `internal/store`: SQLite schema/migrations, repositories, search/FTS,
  pipeline predicates/stats, source/item/media persistence, archive state, and
  activity feeds.
- `internal/projection`: synchronous item/source note refresh boundary.
- `internal/vault`: Markdown rendering for items, sources, entities, and topics.
- `internal/itemhash`: stable item content hashing for change detection.

### Importers

- `internal/xapi`: native cookie-backed X bookmark import and post hydration,
  quoted-post persistence, media metadata, GraphQL and fallback fetch paths.
- `internal/applenotes`: snapshot-based Apple Notes direct SQLite import,
  note/attachment materialization, summary and attachment enrichment hooks.
- `internal/safaritabs`: snapshot-based Safari/iCloud CloudTabs import.
- `internal/githubimport`: GitHub stars import and source enrichment.
- `internal/youtubeimport`: YouTube watch-later/liked feed import.
- `internal/linkadd`: manual URL intake.

### Enrichment

- `internal/linkextract`: URL discovery from items and optional source
  enrichment.
- `internal/sourceenrich`: source extraction, fallback flow, summary execution,
  failure classification, retry policy, and note refresh.
- `internal/xmediatranscribe`: X video/audio transcript materialization and
  media-summary generation.
- `internal/xphotoocr`: X photo OCR and vision extraction.
- `internal/itemcategorize`: item/source categorization and tag/category
  generation.
- `internal/mediadownload`: X media download policy, transfer, and
  content-addressed file placement.
- `internal/mediaarchive`: media archive/upload/prune coordination.
- `internal/sqlitearchive`: SQLite archive/restore coordination.
- `internal/noterepair`: note/projection repair helpers.

### Retrieval And Research

- `internal/queryterms`: query normalization, chat prompt search-text selection,
  stopwords, and tag-query aliases.
- `internal/retrieval`: shared retrieval DTOs and reusable payload mechanics,
  including content sections, related-document metadata, query windows, and
  compact item/source metadata maps.
- `internal/ask`: lower-level evidence retrieval, ranking, excerpts, entity
  expansion, prompt input writing, and retrieval response assembly.
- `internal/brainresearch`: higher-level research packs, strategy planning,
  concept coverage, evidence reranking, topic briefs, and synthesis input.
- `internal/entities`: on-demand entity derivation/search and generated entity
  notes.
- `internal/topics`: topic maps, topic notes, graph construction, and topic
  synthesis helpers.

### Serving Surfaces

- `web`: embedded Svelte UI, JSON APIs, SSE research synthesis, mutation
  routes, archive media helpers, and static asset serving.
- `internal/mcpserver`: read-only MCP tools/resources/prompts over stdio and
  JSON-RPC-over-HTTP POST.
- `internal/remote`: tsnet/Tailscale serving for web and MCP.
- `internal/mcpeval`: development/test harness for MCP retrieval quality.

### External Inference And Tooling

- `internal/summarizecli`: `summarize` command execution plus direct
  Ollama/OpenRouter provider paths.
- `internal/summaryconfig`: summary provider/model configuration helpers.
- `internal/categoryvocab`: category/tag vocabulary support.

## Sync Pipeline

`internal/syncjob` owns `dbrain sync all`. It now runs through an explicit
ordered internal stage plan while preserving the public flat `syncjob.Options`
shape used by CLI/tests.

The ordinary sync flow is:

1. Import local/user signals such as Apple Notes and Safari tabs when enabled.
2. Import X bookmarks, hydrate X posts, and run bounded quoted-post follow-up
   passes.
3. Discover links from newly changed items and queue/enrich sources.
4. Transcribe X media and OCR X photos.
5. Import GitHub stars and YouTube feed entries.
6. Drain source enrichment work.
7. Categorize newly covered items/sources.
8. Archive/prune media only after terminal local-media enrichment coverage.

The stage plan is bounded. It should not grow unbounded recursive loops for X
quotes, source retries, media downloads, or derived repair work.

## Evidence And Derived Data

Raw evidence includes imported note text, item text, source extracted text, X
post JSON/text, X media transcripts, OCR text, and media metadata.

Derived data includes source summaries, item summaries, item/source categories,
tags generated by models, entity/topic views, research packs, and synthesis
answers.

Current transitional storage:

- `item_enrichments` mirrors current item summary, OCR, and X media transcript
  roles while compatibility columns remain in place.
- Source summaries have version records and model/tool provenance.
- Model-generated tags currently share the broad tag storage surface; tag
  provenance remains a design backlog item.

## Trust Boundaries

CLI commands run with local user permissions and can read/write the local dbrain
database, vault, media, archive metadata, and temp/cache/log directories.

The web UI is a trusted local administration surface. When served through
`dbrain serve remote`, it is exposed through tsnet/Tailscale. There is no
dbrain-specific login or per-route authorization layer. Treat access to remote
web as access to a read/write administration UI.

MCP tools/resources are read-only with respect to dbrain state, but they expose
local corpus content to connected clients. Use trusted MCP clients.

Model-backed flows may send local evidence to the configured model provider.
Local Ollama calls stay on the configured Ollama endpoint. Hosted providers
such as OpenRouter may receive source extracts, note text, item text,
transcripts, OCR text, tags, or images depending on the command.

Archive features use configured S3-compatible storage. Archived media and
SQLite snapshots can contain personal content.

Local maintenance commands can delete, prune, purge, replace, or reset local
state when explicitly requested. See `docs/maintenance-operations.md` for the
current audit table and guardrails.

## Migration Policy

SQLite startup uses an ordered migration registry. Version 1 is the adoption
baseline for the schema that existed when migrations were introduced; later
schema changes are separate ordered migrations. The current schema version is
the highest registered migration version, not always version 1.

Operational expectations:

- `OpenReadOnly` must not run migrations.
- Fresh creates and idempotent reopens are covered by tests.
- Future schema-changing migrations should include representative upgrade tests
  and raw-evidence preservation tests.
- Users should back up `brain.db`, `brain.db-wal`, and `brain.db-shm`, or run
  `dbrain sqlite archive`, before running binaries with new schema migrations.
- Downgrades are handled by restoring a pre-upgrade database backup and running
  the matching older binary; automatic down migrations are not supported.

## Related Documents

- `README.md`: user-facing setup, safety model, command reference, and license.
- `docs/schema-migrations.md`: schema migration, backup, restore, and downgrade
  policy.
- `docs/maintenance-operations.md`: local delete, purge, prune, restore, and
  reset operation audit.
- `docs/release-build.md`: release build checklist and embedded web asset
  policy.
- `docs/web-route-capabilities.md`: current web route capability matrix.
- `docs/tsnet-transport.md`: remote/tsnet serving model.
- `docs/open-source-license-review.md`: dependency-license audit notes.
- `docs/architecture-cleanup.md`: cleanup audit history and remaining backlog.
