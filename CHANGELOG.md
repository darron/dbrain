# Changelog

This document tracks significant changes to `dbrain`. Dates use the local
development date for the change set.

## Recent Improvements

### Architecture Cleanup And Open-Source Readiness (2026-05-04)

- **Source FTS reliability**: Source FTS delete/insert failures now propagate instead of silently succeeding, with regression tests around source tag reindexing failures.
- **Sync limits**: `dbrain sync all` now supports separate `--x-media-limit` and `--x-photo-ocr-limit` controls while preserving `--x-limit` as the default fallback.
- **Research temp files**: Brain research planner and synthesis inputs now use the configured dbrain temp directory instead of the process temp directory.
- **MCP metadata**: MCP initialize responses now use build-derived dbrain version metadata instead of a hardcoded server version.
- **Summarize provenance**: Failed `summarize` version probes are no longer cached as empty tool versions, preventing later valid probes from losing summary provenance in long test or agent runs.
- **Web/MCP/store/source/retrieval structure**: Split the web server into focused route files, separated chat transcript HTTP handling from Markdown/research-pack/evidence helper rendering, and separated archive media handlers from S3 proxy, archived-asset lookup/URL, and response header/error helpers, separated MCP transport/protocol/tools/tool-family handlers/result-formatting/filtering/schema families/resource catalogs/resource readers/prompt handlers/helpers and eval case/retrieval/report helpers, moved store open/schema/schema-init/search/search-scan/tag-search/search-count/predicate/item-read/item-scan/item-write/item-write-enrichment/time-helper/item-enrichment/item-FTS/source-enrichment/source-failure/X-hydration/X-media-transcription/media/media-archive/stats/stats-pipeline/pipeline-X-media-OCR/source-activity-scan/source-activity-SQL and source schema/link/enrichment/lookup/evidence/relation/tag/search/repair code into focused files, split runtime environment scalar/bool/list/env-file/YAML config helpers, split link extraction candidate collection/URL normalization/source classification helpers, split X hydration orchestration from fetch policy, quote-tree persistence, client/cookie handling, GraphQL and syndication fetch paths, TweetResult request metadata, GraphQL/syndication snapshot parsing, and X bookmark GraphQL fetch/parse/item helpers, extracted source-enrichment options, workers, persistence, summary execution/prompt/policy/content-policy/media-policy, failure persistence/classification/preflight, extract-validation, YouTube fallback/transcriber helpers, process/fallback flow, HTTP reader, Wayback, Sucuri protected fetch, WordPress recovery, and HTML extraction code, split syncjob types/options/progress/merge/X-frontier/stage-execution/runner-hook helpers, split ask query hints/evidence/scoring/prompt/entity/entity-scoring/excerpt/excerpt-window helpers, split brain research pack types/strategy/concept/variant/evidence ranking/scoring/topic/coverage/exact-tag/next-step helpers, planner parse/sanitize/merge helpers, and synthesis run/prompt-input/budget/evidence-format helpers, split MCP get payload/section/evidence-window/related/format helpers, split MCP HTTP lifecycle/POST handling from path/origin helpers, split MCP research pack building from readable formatting, split remote serve orchestration from handler assembly, listen/error helpers, tsnet node, identity logging, and URL rendering helpers, split app-level tsnet status/reset command wiring from status/probe/cert/status-health/status-types/flag/reset/probe-URL/IP-lookup/HTTP-TLS helpers, split Apple Notes reader row-loading/document/attachment-row/attachment-path helpers plus run-setup/run-progress/run-apply/snapshot/attachment helpers from orchestration and decode flows, split entity indexing derivation/relationships/identity-token/builder/path helpers, split topic map graph/entity-scoring/pivot/format/type/synthesis and topic signal phrase/stopword helpers, split vault item rendering/item-frontmatter/item-source sections/media/quoted-post/entity-write/entity-render/topic rendering/topic index/frontmatter/YAML/text/option helpers, split GitHub importer transport/item/source/extract helpers, split YouTube importer feed/process cleanup/item/enrichment/browser helpers, split Safari tabs run/query/item/device/progress/snapshot-DB and app output helpers, split X photo OCR orchestration/persistence/provider/options/helpers and compare sample/input/run/scoring/report helpers, split restore-pruned media devtool query/loading helpers, split X media transcription option/media/audio-command/transcript/summary-input/summary-error/persistence helpers, split media download policy/HTTP/path helpers, split SQLite archive archive/restore/SQLite/file/progress helpers, split media archive option/archive-result/prune/note-refresh helpers, split summarize CLI direct-provider/direct-input/direct-target/direct-response/command/version/env helpers, split item categorization batch/content/photo/LLM-transport/LLM-response/config/tag helpers, and moved sync flag binding, sync env resolution, option assembly, summary rendering, stats command bodies/output rendering, stats pipeline output rendering, sync UI formatting/stage animation/log-line mechanics, topic map/generate/refresh/index helpers, research output rendering, SQLite archive/restore command helpers, Apple Notes import output/debug subcommands, serve MCP/remote/web wiring, repair source reset command flow, source categorization command wiring, and categorize analysis counting/draft helpers out of larger command bodies without changing route, tool, store, sync, retrieval, X hydration, remote serving, archive, or enrichment behavior.
- **Schema migrations**: Added a recorded SQLite baseline migration runner with deterministic column backfills, `schema_migrations`, `PRAGMA user_version`, and tests for fresh create, idempotent reopen, existing current-schema adoption, and read-only opens.
- **Open-source review**: Added architecture cleanup, current web route capability, and dependency-license review docs for pre-release cleanup planning.
- **Project license**: Added the MIT root license and README license section; updated the license review to keep third-party notices as the remaining license-publication task.
- **Location**: `internal/store/`, `internal/sourceenrich/`, `internal/runtimeenv/`, `internal/linkextract/`, `internal/app/sync.go`, `internal/syncjob/`, `internal/xapi/`, `internal/ask/`, `internal/brainresearch/`, `internal/remote/`, `internal/entities/`, `internal/topics/`, `internal/vault/`, `internal/githubimport/`, `internal/youtubeimport/`, `internal/safaritabs/`, `internal/xphotoocr/`, `internal/xmediatranscribe/`, `internal/sqlitearchive/`, `internal/mediaarchive/`, `internal/summarizecli/`, `internal/itemcategorize/`, `internal/mcpserver/`, `internal/mcpeval/`, `cmd/devtools/restore_pruned_pending_x_media/`, `web/`, `README.md`, `docs/`

### OCR Model Comparison Devtool (2026-05-02)

- **Read-only OCR bakeoff**: Added `cmd/devtools/ocr_model_compare` to sample downloaded X photos, run the configured OCR model beside candidates such as `ollama/deepseek-ocr:3b`, and write Markdown/JSON reports with timings, output sizes, errors, and baseline word-overlap signals without changing stored OCR state; `--download-missing` can fetch pruned corpus images into temp files for the audit only.
- **Location**: `cmd/devtools/ocr_model_compare/`, `internal/xphotoocr/`, `README.md`

### Brain Research Pack Surfaces (2026-04-30)

- **Shared research core**: Added `internal/brainresearch` so MCP, web, and CLI research flows share one retrieval-pack builder with query/tag plans, exact-tag evidence, corpus coverage, semantic next steps, and optional topic briefs.
- **CLI and web**: Added `dbrain research` and `/api/research`; the web Explore page now uses a Research tab for evidence packs.
- **Local synthesis**: Added `/api/research/synthesize` as an SSE endpoint plus default-on web and CLI synthesis over research packs, with `--retrieval-only` for evidence-only CLI runs and explicit model/config checks to avoid silent hosted fallback.
- **Accuracy framing**: Research synthesis and MCP prompts now frame the corpus as intentionally selective while prioritizing factual accuracy, source-claim separation, and explicit uncertainty over performative objectivity.
- **Citation navigation**: Research synthesis now turns both bracketed citations and bare source IDs in generated source lists into clickable detail lookups.
- **Citation key handling**: Research citation links now preserve colon-delimited IDs such as `src:apple-note:default:<id>` and `src:rcmp:<id>` instead of linking only the first segment.
- **Citation prompt**: Research synthesis now tells local models to cite exact source keys from the research pack, including `apple-note:*` keys, instead of inventing or shortening prefixes.
- **Citation lookup repair**: Research citation clicks normalize common model-prefixed forms such as `src:apple-note:*` and `src:src:*` back to real dbrain lookup keys.
- **Apple Notes detail view**: Apple Note details now show the full decoded note body inside dbrain, keep indexed attachment text separate, and no longer offer a broken `apple-notes://` external open link.
- **Research UI flow**: Research now keeps typing as a draft until explicit submit, removes the graph/list explorer from the Research view, and shows compact clickable evidence below the synthesis instead.
- **Mobile Research layout**: The Explore Research view now uses normal page scrolling on phones, keeps controls compact, stacks the detail panel below results, auto-scrolls selected evidence into view, and wraps long source-key/citation chips so mobile browsers do not overflow horizontally.
- **Browser-session chat**: Added a Chat mode to the web Explore page that runs a fresh local research/synthesis turn for each follow-up, persists only in browser `sessionStorage`, reuses prior evidence context, never treats previous model answers as evidence, and keeps per-turn evidence collapsed while source-key citations expose inline pin controls.
- **Chat retrieval tuning**: Follow-up chat retrieval now carries compact prior evidence titles as query-focus context while keeping full prior evidence only for synthesis, improving clarified searches without letting broad prior tags or summaries dominate retrieval.
- **Chat query repair**: Chat retrieval no longer injects raw prior source keys into the search text, only adds prior questions/evidence metadata for likely follow-ups, avoids anchoring corrective turns to prior bad evidence, and treats escaped newlines, punctuation, source-key fragments, common inflections, and words such as "kids" as normalized retrieval terms.
- **Research harness**: Research packs now expose query variants and required concept groups, use the configured local model by default for bounded query planning, run the variants through retrieval, and rerank merged evidence by concept coverage so ambiguous factual searches prefer directly matching source clusters over broad near-misses; deterministic planning remains the fallback and explicit opt-out path.
- **Research latency**: Research coverage counts now use FTS-backed indexes when available, variant retrieval skips repeated broad tag-table scans, and evidence packing prefers stored summaries/snippets over raw extracts so web Chat reaches local synthesis instead of timing out on large source bodies.
- **Research fallback cleanup**: Deterministic query planning now strips corpus-framing phrases like "in my research", modal preference words like "should/favored", and plural `models` noise so planner timeouts fall back to cleaner searches such as `model hermes agent`, with extra model-stack/model-name variants for agent model-selection questions.
- **Chat transcript export**: Web Chat can save the current browser-session conversation, retrieval questions, citations, pins, and research packs as a local non-indexed Markdown diagnostic transcript under `data/chat-transcripts/` for later prompt/retrieval review.
- **Ask removal**: Removed the old `dbrain ask`, `/api/ask`, and `dbrain_ask` MCP surfaces instead of preserving aliases.
- **Regression coverage**: Added tests for source exact-tag evidence, `/api/research`, research synthesis budgeting/SSE behavior, removed Ask routes/tools, and a source-enrichment progress logger race.
- **Location**: `internal/brainresearch/`, `internal/queryterms/`, `internal/app/`, `internal/mcpserver/`, `web/`, `README.md`, `skills/dbrain-mcp/`

### Apple Notes Materialized Import (2026-04-30)

- **Import command**: Added `dbrain import apple-notes` with read-only DB/WAL/SHM snapshotting, schema probing, default materialization, `--dry-run` preview mode, opt-out folder/account/shared-note exclusions, and `[[dbrain-ignore]]` support.
- **Materialization**: Apple Notes are imported as `apple_note` items, rendered to Markdown, included in search, and can be locally summarized with an Apple Notes-specific prompt; note URLs and URL attachments feed normal source discovery, and summaries are skipped on unchanged content unless `--force` is used.
- **Attachments**: The importer indexes attachment metadata and Notes-provided attachment text, extracts supported text/PDF attachment files locally, and OCRs image attachments through optional local `tesseract`; unsupported, missing, or oversized files are marked blocked.
- **Safety**: The importer opens copied snapshots instead of live Apple Notes files, skips password-protected notes by default, explains macOS Full Disk Access failures, and supports explicit `--forget-excluded` purging for notes that become excluded.
- **Sync integration**: `dbrain sync all --apple-notes` or `DBRAIN_APPLE_NOTES_ENABLED=true` includes configured Apple Notes import before link extraction/source work.
- **Operator feedback**: Apple Notes imports now print per-note progress only for notes that need work; unchanged-current rows are counted in final stats without spamming output, and applied `--limit` batches skip unchanged-current notes so repeated limited runs advance to new or stale work.
- **Summary default**: Standalone `dbrain import apple-notes` now summarizes by default; use `--summarize=false` for materialization-only imports.
- **Pipeline stats**: `dbrain stats pipeline` now includes Apple Notes materialization and item-summary coverage so imported notes are visible alongside source extraction and media summary stages, and aggregate `ALL` rows include appended item-level stages.
- **Summary prompt**: The Apple Notes summary prompt now labels note shape, such as authored notes, research link lists, checklists, logs, scratchpads, or mixed notes, so rough lists are not overread as polished prose.
- **Docs**: README and `config.yaml.sample` document Apple Notes config/env keys, command usage, and the Full Disk Access requirement.
- **Location**: `internal/applenotes/`, `internal/app/import_apple_notes.go`, `internal/syncjob/`, `internal/store/`, `README.md`, `config.yaml.sample`

### Safari Tabs Import (2026-05-01)

- **Import command**: Added `dbrain import safari-tabs --device <name-or-uuid>` to snapshot Safari's local `CloudTabs.db` read-only and materialize iCloud tabs from a targeted device as `safari_tab` items.
- **Device review**: Added `dbrain import safari-tabs devices` to list visible Safari iCloud tab devices and tab counts before importing.
- **Link pipeline**: Safari tab items now feed normal link discovery, source extraction, summaries, rendering, and categorization without mutating or closing upstream Safari tabs.
- **Stats**: `dbrain stats pipeline` now reports Safari tab item materialization in the Extraction table while linked pages remain counted under their normal source types.
- **Sync integration**: `dbrain sync all --safari-tabs --safari-tabs-device <device>` or `DBRAIN_SAFARI_TABS_ENABLED=true` plus `DBRAIN_SAFARI_TABS_DEVICE=<device>` includes configured Safari tabs import before link extraction/source work.
- **Operator feedback**: Safari sync summaries now separate created, updated, unchanged, rendered, skipped, and linked rows so unchanged `CloudTabs.db` snapshots do not look like new imports every run.
- **Filters**: The importer supports `--older-than`, `--limit`, `--dry-run`, and `--show-titles` so large tab backlogs can be imported or previewed safely.
- **Location**: `internal/safaritabs/`, `internal/app/import_safari_tabs.go`, `internal/store/`

### Source Retry Controls And Failure Classification (2026-04-30)

- **Retry targeting**: `dbrain repair sources` can now filter by source type, extract status, summary status, failure kind, and minimum failure count before resetting enrichment state.
- **X article repair**: `dbrain repair sources --rehydrate-x-articles` also clears linked X item hydration markers so bad cached article previews are rebuilt by the next `hydrate x` / `sync all` run.
- **Failure accounting**: Source failure counts and first/last failure timestamps remain visible through source JSON/MCP, and rendered source notes now show extract failure count/kind metadata.
- **Terminal classification**: Repeated access-denied, timeout, unsupported-file, and generic fetch failures now become terminal `dead` source extraction outcomes after defined thresholds instead of retrying indefinitely.
- **Wayback fallback**: Repeated source extraction failures now check the Internet Archive Wayback Availability API before terminalizing, log both checks and misses, and final-attempt rows bypass the normal retry cooldown so unclassified failures stop after 5 attempts when no archive recovery succeeds.
- **Wayback quality gate**: Very short Wayback extracts and archive/browser shells now keep raw extracted text but skip summarization instead of generating plausible summaries from weak evidence.
- **Extraction throughput**: Standalone `extract links` and `extract sources` now default to four concurrent source extract/summarize jobs, matching `sync all` and `worker sources`, so one slow URL does not serialize the whole batch.
- **Failure metadata**: Consecutive failure counts are now preserved when a retry changes from an older `unknown` class into a more specific terminal class.
- **Placeholder repair loop**: Short redirect/loading placeholder extracts selected for summary repair are now marked `skipped` instead of being repeatedly summarized as successful work.
- **Summary timeout loop**: Source summary timeouts and context-limit failures now persist as `blocked` instead of retryable `error`, so oversized stored extracts do not hot-loop in `worker sources` or `sync all`.
- **Media extract guard**: Direct image/video/audio/archive URLs and binary-looking extracts are now marked `skipped` for text summarization, preventing Wayback image captures from being repeatedly sent to local LLM summarization.
- **Docs**: README now documents the failed web-source rebaseline flow using `repair sources` plus source extraction/sync retries.
- **Location**: `internal/store/`, `internal/sourceenrich/`, `internal/app/repair.go`, `internal/vault/source.go`, `README.md`

### Sync Source Categorization (2026-04-30)

- **Sync pipeline**: `dbrain sync all` now categorizes uncategorized linked sources as well as items before the media archive stage.
- **Visibility**: Sync progress, debug logs, JSON stats, and the summary table now separate item and source categorization counts while preserving aggregate categorize totals.
- **Docs**: Updated sync and categorization command docs to describe the source apply path.
- **Location**: `internal/syncjob/`, `internal/app/sync.go`, `README.md`

### Source-Level Tags And Categorization (2026-04-29)

- **Source tags**: Added first-class `sources.user_tags` so linked articles, repos, videos, and papers can be categorized independently from the saved items that reference them.
- **Categorization**: Added `dbrain categorize source` and `dbrain categorize sources`; `categorize repair` and `categorize analyze` now include both item and source tags.
- **Retrieval**: Source tags are indexed for search, included in exact-tag matching/counts, returned through MCP evidence, and visible/editable on source detail pages in the web UI.
- **Docs and skill**: Updated README, MCP docs, and the dbrain MCP skill to distinguish source tags from backlink item tags.
- **Location**: `internal/store/`, `internal/itemcategorize/`, `internal/app/`, `internal/ask/`, `internal/mcpserver/`, `web/`, `MCP.md`, `README.md`

### Built-In tsnet Remote Serving (2026-04-29)

- **Remote web and MCP**: Added `dbrain serve remote` to expose the existing read/write web UI and read-only MCP endpoint from one built-in Tailscale `tsnet` node.
- **MCP compatibility**: Added `dbrain serve mcp --transport tsnet` for MCP-only tailnet serving while keeping stdio as the default local-agent transport.
- **State safety**: Added durable tsnet state under `<data_dir>/tsnet/<hostname>`, 0700 state-dir validation, advisory locking, sync-folder warnings, and guarded `dbrain tsnet status` / `dbrain tsnet reset` commands.
- **Status health**: `dbrain tsnet status` now reports active running/reachable health, probes only configured web/MCP surfaces with surface-specific status handling, respects non-default listen ports, reports `control_url`, checks certificate health, and exposes machine-readable JSON fields for automation.
- **Security**: Added typed bootstrap secret refs (`env:`, `op://`, `keychain://`), YAML-only command escape hatch with explicit opt-in, remote-only Origin checks, security headers, first-run auth URL logging, custom-control warnings, and best-effort Tailscale identity logging through `WhoIs`.
- **Remote hardening**: Added typed tsnet lock errors, Unix/Windows lock separation with a clear Windows unsupported path, injected request-log output, short-TTL cached `WhoIs` identity logging, bounded remote HTTP timeouts, and a `serve mcp --transport tsnet --mcp-path` alias.
- **Operator UX**: Browser `GET /mcp` now returns a clear JSON-RPC POST diagnostic, repeated tsnet auth log lines are deduped while preserving the login URL, and `tsnet reset` requires a literal `reset` confirmation unless `--yes` is used.
- **Docs and skill**: Expanded MCP docs and the dbrain MCP skill with built-in tailnet serving examples, remote Streamable HTTP client config shapes, first-run auth/state guidance, and `tsnet status` troubleshooting.
- **Regression coverage**: Added fake tsnet orchestration tests for startup timeout, prepared state-dir use, listen mode selection, startup unwind, shutdown order, concurrent user logs, and locked status/reset behavior.
- **Location**: `internal/remote/`, `internal/app/serve.go`, `internal/app/tsnet.go`, `config.yaml.sample`, `docs/tsnet-transport.md`, `README.md`

### Streamable HTTP MCP Transport (2026-04-29)

- **MCP transport**: Added `dbrain serve mcp --transport http` as a parallel stateless Streamable HTTP transport while keeping stdio as the default local-agent path.
- **Remote access**: Documented the Tailscale Serve pattern for exposing the localhost MCP HTTP endpoint to remote agents without granting SSH access.
- **Security**: HTTP mode binds to `127.0.0.1:8743` by default, rejects untrusted browser `Origin` headers, and stays read-only with no MCP session state.
- **Location**: `internal/mcpserver/`, `internal/app/serve.go`, `MCP.md`, `README.md`, `skills/dbrain-mcp/SKILL.md`

### Legacy Import Cleanup And MCP Docs Split (2026-04-28)

- **Removed**: Dropped the obsolete `dbrain import ft` command and the legacy FT importer package.
- **Source cache naming**: Cached item article text used for source extraction is now recorded as `item-cache` instead of `ft-bookmarks`.
- **API attribution**: Outbound API calls now send a versioned `User-Agent` header such as `dbrain/<short-sha>`; override with `DBRAIN_USER_AGENT` or `http.user_agent`.
- **Docs**: Split detailed MCP agent guidance into `MCP.md`, kept README focused on requirements and command reference, and moved examples under their command sections.
- **Location**: `internal/app/`, `internal/version/`, `internal/summarizecli/`, `internal/itemcategorize/`, `internal/xphotoocr/`, `internal/githubimport/`, `internal/store/`, `internal/linkextract/`, `MCP.md`, `README.md`

### Configuration Layout, Config File Support, And Env Documentation (2026-04-28)

- **Added**: Default installed layout now uses `~/.config/dbrain` for config and categories, and `~/.local/share/dbrain` for SQLite data, vault files, media, temp files, cache files, and logs.
- **Development override**: Added `DBRAIN_ROOT` as an environment-level equivalent to `--root` for local checkout development; explicit `--root` still wins.
- **Config resolution**: Runtime values now resolve from shell environment, then `.envrc` / `.env`, then `config.yaml`, with grouped YAML keys such as `openrouter.api_key`, `summary.model`, and `source.reader.base_url`.
- **Operator visibility**: Added `dbrain config paths` and `dbrain config env` so users and agents can inspect active storage locations and supported env/config keys directly from the CLI.
- **Help text**: All Cobra help screens now include an environment/config footer that points to `dbrain config env`; `config env` now renders a terminal-width-aware Charmbracelet table by default with `--markdown` for docs.
- **Module path**: Updated the Go module and build linker paths to `github.com/darron/dbrain`.
- **Repo hygiene**: Anchored generated-directory ignore rules so source packages such as `internal/vault` are no longer hidden by the root `vault/` ignore.
- **Docs**: Added `config.yaml.sample`, reorganized the README requirements/config/env/command sections, alphabetized the command index, documented dev tasks, and moved TODOs near the bottom with completed MCP work marked.
- **Contributor workflow**: Updated `AGENTS.md` to require changelog entries for features, fixes, CLI/config changes, pipeline changes, MCP/tooling changes, and other user-visible behavior changes.
- **Verification**: `go test ./internal/summarizecli ./internal/runtimeenv ./internal/app`
- **Location**: `internal/config/`, `internal/runtimeenv/`, `internal/app/`, `internal/summarizecli/`, `config.yaml.sample`, `README.md`

### MCP Retrieval Hardening And Agent Workflow Support (2026-04-28)

- **DB-first retrieval**: MCP detail reads now use DB-backed content modes by default instead of depending on rendered Markdown freshness.
- **Research workflow**: Added and tuned the MCP research pack path for broad corpus questions, including query plans, exact tag evidence, score explanations, topic brief integration, and follow-up suggestions.
- **Evidence quality**: Search and research now include user tags, hyphenated tag aliases, image OCR, media transcripts, linked-source context, source backlinks, and query-windowed excerpts.
- **Operational clarity**: MCP request logs are emitted on stderr so stdio protocol output stays clean, and the repo includes an agent skill at `skills/dbrain-mcp/SKILL.md`.
- **Regression coverage**: Added MCP eval and test support for retrieval behavior, tool advertisement, exact-tag evidence, and source-agnostic importer expectations.
- **Location**: `internal/mcp/`, `internal/mcpeval/`, `skills/dbrain-mcp/`, `README.md`, `Taskfile.yml`

### Sync Categorization And Media Ordering (2026-04-28)

- **Added**: `sync all` can categorize newly discovered uncategorized items after the normal import/enrichment stages, reusing the same apply path as `dbrain categorize batch`.
- **Tag behavior**: Categorizer output merges into existing `user_tags` without duplicates instead of replacing user-entered tags.
- **Image support**: Image categorization is enabled by default for categorization commands and `sync all`; pass `--images=false` or `--categorize-images=false` for text-only models.
- **Archive ordering**: Media archive/prune now runs after categorization so local photos remain available to vision-capable categorization before they are uploaded/pruned.
- **Location**: `internal/app/sync.go`, `internal/itemcategorize/`

### Reader Fallbacks For Difficult Government Domains (2026-04-27)

- **Added**: Known-problem source domains can be routed through a reader/textifier URL before summarize-backed extraction.
- **Default behavior**: `canada.ca` is routed through `https://r.jina.ai/` by default to avoid known long hangs and forced timeout kills.
- **Config knobs**: Added `DBRAIN_SOURCE_READER_DOMAINS` and `DBRAIN_SOURCE_READER_BASE_URL`, now also configurable through `source.reader.domains` and `source.reader.base_url`.
- **Fallback behavior**: If the reader service rejects a request, `dbrain` falls back to a local browser-style direct fetch and readable HTML extraction before summarization.
- **Location**: `internal/sourceenrich/`, `README.md`

### Version Command And Build Metadata (2026-04-27)

- **Added**: `dbrain version` prints commit, short commit, build time, git status, Go version, git version, build platform, module path, and module version.
- **Automation**: `--json` returns the same metadata in structured form.
- **Build wiring**: `task build` now passes git version and build platform values into the binary while preserving Go build VCS metadata.
- **Location**: `internal/version/`, `internal/app/`, `cmd/dbrain/`, `Taskfile.yml`

## Historical Changes From Git Commits

The sections below are generated from the committed history through
`35b7ea1`. They intentionally overlap with some Recent Improvements entries
above, but keep the commit-derived timeline visible.

### MCP Retrieval, Tags, Categorization, And UI Assets (2026-04-28)

- **UI assets** (`35b7ea1`): Refreshed built web UI asset artifacts.
- **Saved-item backlink tags** (`6113ee0`): Surfaced tags from saved item backlinks on source details, improving source-node context in MCP and the web UI.
- **Exact tag examples** (`5fb4360`): Added representative exact-tag evidence examples to MCP research packs and eval expectations.
- **Actionable MCP errors** (`e26675d`): Returned structured tool errors for missing lookups, unsupported modes, and unknown tools.
- **Deterministic MCP coverage** (`3d02a51`): Added deterministic retrieval test coverage and `task test-mcp` support.
- **Focused query ranking** (`b79252e`): Improved retrieval scoring so focused query terms are preferred over broad tag-only matches.
- **MCP evals and windowed evidence** (`7abef9c`): Added retrieval eval command support and query-windowed evidence excerpts.
- **Batch get and score signals** (`e35f98c`): Added `dbrain_get_many` plus retrieval score signals for agent follow-up workflows.
- **DB-backed get** (`65e209d`): Made `dbrain_get` DB-first with content modes, section caps, related context, and recall signals.
- **Tag-aware research packs** (`df253eb`): Made research packs read-only by default and tag-aware, including hyphenated tag aliases.
- **Image categorization default** (`5004ebf`): Enabled image categorization by default for categorization commands and sync.
- **Tag vocabulary repair** (`22a3019`): Added canonical category vocabulary loading, analyze, and repair commands.

### Sync Categorization, User Tags, Versioning, And Reader Fallbacks (2026-04-27)

- **Sync categorization** (`d7a3255`): Ran categorization at the end of `sync all`, including merged tag persistence and sync progress integration.
- **User tags and categorizer** (`d20e7f2`): Added `user_tags`, item categorization commands, model-backed category suggestions, and web detail tag display.
- **Version command** (`fa9bdb6`): Added CLI build metadata reporting with text and JSON output.
- **Reader fallback** (`af4aa59`): Added source reader fallback routing for difficult domains such as `canada.ca`, plus source repair support.
- **Local Ollama Modelfile** (`7331732`): Added the dbrain Ollama Modelfile for local model experimentation.
- **Web detail enrichments** (`83345f2`): Improved web detail views for video transcripts, image text, and direct URL results.
- **Full tweet search and web links** (`6c9f6b5`): Indexed full tweet text, added links inside tweet notes, and improved web frontend detail behavior.

### Web UI, Link Injection, Source Freshness, Sync UI, And Backups (2026-04-26)

- **Web UI fixes** (`9e131a4`): Cleaned up the web app and API behavior after the alternate interface pass.
- **Graph-oriented interface** (`a72a380`): Explored a richer web interface with graph visualization and redesigned detail/list panels.
- **Ollama vision OCR** (`db75b0c`): Added Ollama vision support for X photo OCR.
- **Model-agnostic summary freshness** (`c213b5a`): Made summary freshness checks policy-aware and model-agnostic by default.
- **Summary language defaults** (`b8e864d`): Defaulted summaries to English with env/config overrides and `auto` language mode support.
- **Manual link injection** (`f7d3731`): Added `dbrain link add` and web add-link support to submit URLs directly into the source backlog.
- **Sync progress UI** (`ca0f698`): Added terminal progress UI for `sync all`.
- **SQLite archive and restore** (`a4c34a3`): Added S3-compatible SQLite archive and restore commands with confirmation UI.
- **Frontier settling and hung process handling** (`30c54c7`): Settled X quote hydration before downstream media work and killed hung summarize subprocesses.

### X Quote Hydration, Media Archive, OCR, Native Bookmarks, And Policy-Aware Stats (2026-04-25)

- **Quote semantics docs** (`2805555`, `30e8945`, `c7ec0d7`): Clarified X quote hydration semantics, counters, and future work in README/AGENTS.
- **Quoted X posts and media repair** (`c8cefd1`): Hydrated quoted posts as first-class items, added quote links, and added a devtool to restore pruned pending X media.
- **Archived media proxy** (`998ff32`): Added web proxy and signed URLs for archived media.
- **Media archival tier** (`ae896f3`): Added S3-compatible media archive state, upload/mark/prune logic, and reference-aware archive tracking.
- **Invalid summary repair** (`83a9395`): Requeued invalid source summaries for repair.
- **Cancellation hardening** (`81ebad9`): Honored cancellation across enrichment pipelines.
- **Contributor rules** (`3afad6a`): Added contributor guidance for tests, models, content handling, and pipeline semantics.
- **X transcript summaries and photo OCR** (`2d831de`): Added item enrichment fields, transcript summaries, X photo OCR, and pipeline coverage stats.
- **Native X bookmark sync** (`b2a8e62`): Replaced the legacy FT-backed X import path with native cookie-backed X bookmark sync.
- **Policy-aware stats** (`7d58b8d`): Made summary freshness stats policy-aware by default so backend swaps do not make the corpus look stale.

### Direct Summaries And X Article Extraction (2026-04-24)

- **Direct OpenRouter summaries** (`3a305d0`): Added direct OpenRouter summary path and smarter note sync behavior.
- **X article downloads and protected fetches** (`b92edf2`): Improved X article downloads, protected source fetch handling, Ollama-backed summarization, source stats, and X media transcription.

### Web, MCP, HTTP Server, Media Downloads, And Query Surfaces (2026-04-22)

- **Web cleanup** (`92fda9b`): Added web interface fixes, operational panels, activity/backlog stats, and YouTube/source reliability improvements.
- **MCP and HTTP surfaces** (`068495c`): Added the first MCP server, web HTTP server, `ask`, entity/topic/query surfaces, media downloads, source worker loops, note repair, sync job orchestration, and Svelte UI scaffolding.

### Initial Import, Source Enrichment, YouTube, GitHub, And Stats Foundation (2026-04-20)

- **Initial scaffold** (`92476a2`): Added the Go CLI, SQLite store, FT import, source extraction, summarize integration, X API helpers, models, and tests.
- **Command split and YouTube import** (`35496c9`): Split app commands into dedicated files, added get/search/hydrate/import/extract commands, item hashing, source enrichment, and YouTube import.
- **GitHub stars and stats** (`d6bb415`): Added GitHub star import, retry handling, item/source stats, source enrichment improvements, and broader test coverage.
