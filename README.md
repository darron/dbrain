# dbrain

`dbrain` is a local-first second-brain scaffold for incremental imports from X
bookmarks, GitHub stars, YouTube, and legacy `ft-bookmarks` archives, with
Markdown note rendering for Obsidian and local query over the imported corpus.

## Current Commands

- `dbrain import ft`
- `dbrain import x-bookmarks`
- `dbrain sync all`
- `dbrain import github stars`
- `dbrain import youtube`
- `dbrain entity map [query]`
- `dbrain entity generate <query>`
- `dbrain entity index`
- `dbrain topic map <topic>`
- `dbrain topic generate <topic>`
- `dbrain topic refresh [topic]`
- `dbrain topic index`
- `dbrain worker sources`
- `dbrain hydrate x`
- `dbrain transcribe x-media`
- `dbrain archive media`
- `dbrain repair notes`
- `dbrain serve mcp`
- `dbrain serve web`
- `dbrain extract links`
- `dbrain extract sources`
- `dbrain stats items`
- `dbrain stats sources`
- `dbrain stats activity`
- `dbrain stats backlog`
- `dbrain stats pipeline`
- `dbrain ask <question>`
- `dbrain search <query>`
- `dbrain get <source-key-or-id>`

On macOS, `dbrain` will automatically use `caffeinate` when the command is
available, so long-running leaf commands keep the machine awake by default.
Use `--no-caffeinate` to disable that behavior for a specific run. You can
still pass `--caffeinate` to force it explicitly.

Structured debug logging is enabled by default. Use `--no-debug` when you want
quiet CLI output.

## Dev Tasks

- `task fmt`
- `task web-install`
- `task web-build`
- `task lint`
- `task test`

## TODO

- Continue improving topic/MOC synthesis quality and better periodic refresh workflows as the corpus fills out.
- Integrate the current MCP server cleanly with agent workflows and extend it further as needed.
- Add a Tailscale-reachable query surface, likely tsnet-backed MCP and/or a small web UI, so the brain can be queried remotely while away from the machine.
- Keep breaking the web UI into smaller Svelte components with a thin shared API client layer instead of letting the browser surface collapse into one large page component.
- Improve the web note reader further with richer Markdown rendering, better code-block presentation, and cleaner outbound link handling for vault notes.
- Add URL-backed state and deeper note-to-note navigation in the web UI so searches, selected notes, and related pivots survive refreshes and remote sessions.
- Expand the web operations/dashboard view with deeper worker drill-down, richer backlog trend views, and clearer source-level drill-ins so repeated failures are easier to triage.
- Add first-class filters and browsing controls in the web UI for source type, kind, status, and recency so the corpus is easier to slice than with one text box.
- Add semantic retrieval on top of SQLite/FTS, likely embeddings plus related-item expansion.
- Add a translation stage for non-English X content, storing both original and translated text.
- Broaden media ingestion beyond the current X image/video downloads, with content-hash deduplication across repeated saves and reposted duplicates.
- Harden the YouTube pipeline for transcript-missing videos and improve the fallback/transcription path.
- Add Apple Podcasts as a first-class imported signal/source type so podcast episodes can enter the same item/extract/summary pipeline as YouTube and web sources.
- Improve provider provenance so stored summaries always record the exact backend/model used.
- Make backlog/admin summary freshness stats policy-aware instead of exact-model-aware, so switching between acceptable local/hosted summary models does not make the whole corpus look stale.
- Add explicit source-of-truth audit commands (for example `dbrain audit github-stars`, `dbrain audit youtube-watch-later`, `dbrain audit x-bookmarks`, and `dbrain audit all --json`) so imports can be reconciled against upstream services with missing IDs and enrichment status clearly separated, while treating the local DB as append-only by default instead of auto-flagging removed upstream saves/stars/likes for deletion.
- Add a pre-summary staging path for oversized extracts so giant PDFs and long documents can be chunked, pre-compressed, or locally preprocessed before hosted summary calls hit provider context limits.
- Add an oversized-X-video policy for media download/transcription. Right now large or hour-long videos time out, land in `download_status='error'`, and remain retryable on future `hydrate x` / `sync all` runs. Add byte-size and/or duration gating, prefer lower-bitrate playable variants for transcription, and classify clearly-skipped assets as `too_large` / `too_long` instead of retrying forever.
- Maybe reclassify non-actionable X media transcript outcomes like `no_audio`, `noise`, and `too_short` out of the generic failed bucket so transcription stats distinguish real pipeline errors from terminal no-content cases.
- Add an optional X thread expansion path when a bookmarked post is clearly part of a longer thread. Prefer fetching the full thread from X/GraphQL when the upstream APIs expose it, so bookmarking the first tweet can still capture the whole series. If the APIs do not expose enough thread structure reliably, fall back to a best-effort linked-post crawl without breaking the normal single-post hydrate flow.
- Add a scheduler/launchd-style mode on top of the new worker loop so enrichment can resume automatically after terminal closure or reboot.
- Keep `Obscura` (`https://github.com/h4ckf0r0day/obscura`) in mind as a possible future browser/scraping backend if headless Chrome-based extraction gets stuck again.

## Layout

- `data/brain.db`: local SQLite state
- `vault/items/...`: rendered Markdown notes for Obsidian
- `vault/sources/...`: rendered Markdown notes for linked sources
- `vault/entities/...`: derived entity notes and entity index
- `vault/topics/...`: generated topic/MOC notes

## Prerequisites

- `go` `1.26` to build and run `dbrain`
- a supported browser with active logged-in sessions for cookie-backed flows
- `chrome` is the current recommended browser for both X and YouTube ingestion
- `GITHUB_TOKEN` for `dbrain import github stars`
  `dbrain` will also fall back to `./.envrc` or `./.env` when the shell
  environment does not already export it.

If you still use the legacy `dbrain import ft` path, you also need a local FT
archive at `~/.ft-bookmarks/bookmarks.db` or another path passed to
`dbrain import ft --source ...`. `dbrain` reads that SQLite archive directly.
The `ft` CLI is not required at runtime, but you still need `fieldtheory-cli`
or another process to keep `~/.ft-bookmarks/bookmarks.db` fresh.

For GitHub stars, use a fine-grained PAT with:

- `User permissions`: `Starring: Read`
- `Repository permissions`: `Metadata: Read`
- `Repository permissions`: `Contents: Read`

## External Tools

- `summarize`
  Required for `dbrain extract links` and YouTube source enrichment.
  `dbrain` can also route summarize-backed work to a local Ollama daemon by
  passing models like `--model ollama/qwen2.5:7b-instruct`.
- `uv`
  Recommended for `summarize` environments that shell out to Python-backed
  helpers or transcriber setup flows.
- `yt-dlp`
  Required for `dbrain import youtube`.
- `deno` or `node`
  Recommended for `yt-dlp` YouTube challenge solving. Some videos will not
  expose downloadable audio cleanly without a working JS runtime.
- `whisper-cli`
  Optional, but needed for local audio transcription fallback when a YouTube
  video has no usable captions or transcript.
- `mw`
  MacWhisper CLI. Required for `dbrain transcribe x-media`, and therefore also
  required for `dbrain sync all` unless you pass `--skip-x-media`. When
  installed, `dbrain` also prefers it over `whisper-cli` for local YouTube
  audio transcription when `--transcriber auto` is in use. You can force it
  explicitly with `--transcriber macwhisper` or
  `--transcriber macwhisper:<engine:model>`.
- `ffprobe`
  Required for `dbrain transcribe x-media`, and therefore also required for
  `dbrain sync all` unless you pass `--skip-x-media`. `dbrain` uses it to
  detect whether a downloaded X video actually contains an audio stream.
  `ffprobe` is usually installed as part of `ffmpeg`.
- `~/.summarize/cache/whisper-cpp/models/ggml-base.bin`
  Optional, but required by the local `whisper.cpp` fallback. A working setup is
  `whisper-cli` plus this model path.
- `task`
  Required for the top-level dev tasks: `task fmt`, `task lint`, `task test`.
- `golangci-lint`
  Required for `task lint`.
- `sqlite3`
  Optional, but useful for inspecting `data/brain.db` during debugging.
- `caffeinate`
  Optional macOS helper. When available, `dbrain` uses it automatically for
  long-running leaf commands unless you pass `--no-caffeinate`.

## Optional Media Archive Env

To automatically offload finalized media to S3-compatible storage at the end of
`dbrain sync all`, export:

- `DBRAIN_AUTO_ARCHIVE_MEDIA=1`
- `DBRAIN_R2_BUCKET=<bucket>`
- `DBRAIN_R2_ENDPOINT=https://<account>.r2.cloudflarestorage.com`
- `DBRAIN_R2_ACCESS_KEY_ID=<key>`
- `DBRAIN_R2_SECRET_ACCESS_KEY=<secret>`

Optional:

- `DBRAIN_R2_REGION=auto`
- `DBRAIN_R2_SESSION_TOKEN=<token>`
- `DBRAIN_ARCHIVE_PROVIDER=cloudflare_r2`
- `DBRAIN_R2_PUBLIC_BASE_URL=https://...` when archived media should render as
  anonymously readable URLs in notes. Leave this unset for authenticated-only
  buckets.

`sync all` only runs the archive stage automatically when
`DBRAIN_AUTO_ARCHIVE_MEDIA=1` or `--archive-media` is set. The archive stage
uploads eligible media after OCR/transcription reaches a terminal state, marks
the object as archived in the DB, and prunes the local file once every row
sharing that same `local_path` is safely archived.

## Command Requirements

- `dbrain import ft`
  Legacy import path. Requires the FT bookmarks SQLite database. No external
  binary is invoked.
- `dbrain import x-bookmarks`
  Direct X bookmark import path. Requires a supported browser profile with
  valid X cookies. Chrome/Chromium is the best-tested path.
- `dbrain sync all`
  Runs the regular incremental refresh pipeline in one command: direct X
  bookmark import, X hydration, X media audio transcription, X photo OCR,
  tweet-link discovery/enrichment, GitHub stars import, YouTube import, and an
  optional source-backlog worker batch. It can also optionally append a media
  archive stage that uploads finalized local media to configured S3-compatible
  storage and prunes local copies. The X media and X photo OCR stages use the
  same X batch limit as `hydrate x` (`--x-limit`). In the default
  configuration this combines the requirements of X bookmark import, X
  hydration, X media transcription, X photo OCR, link/source enrichment, and
  YouTube import, so a practical local setup usually includes a supported
  Chrome/Chromium profile with valid cookies plus `mw`, `ffprobe`,
  `summarize`, and `yt-dlp`. It supports `--skip-*` flags when you only want
  part of the pipeline.
- `dbrain archive media`
  Optional manual archive/prune pass for finalized media. It can either just
  mark/prune already-uploaded media or upload directly to an S3-compatible
  bucket first when `--upload` or archive-upload env vars are configured.
- `dbrain hydrate x`
  Requires a supported browser profile with valid X cookies. Chrome/Chromium is
  the best-tested path. On macOS you may see a Keychain prompt the first time
  cookie decryption is used.
- `dbrain transcribe x-media`
  Requires `mw` and `ffprobe`. `mw` performs the transcription and `ffprobe`
  checks whether a downloaded X video or animated GIF has an audio stream worth
  transcribing. Normal runs skip already classified items; use `--force` when
  you explicitly want to retry failures or reprocess existing transcript items.
- `dbrain import youtube`
  Requires a browser profile with valid YouTube cookies, `yt-dlp`, and
  `summarize`. When `--profile` is omitted, `dbrain` will try the bare browser
  cookie source first and then retry discovered local Chromium-style profiles
  such as `Default` and `Profile N`. A working local setup may also need `uv`. For transcriptless videos, the best current setup is also
  `deno` or `node`, plus `whisper-cli` and the `ggml-base.bin` model.
- `dbrain import github stars`
  Requires `GITHUB_TOKEN`. It uses the GitHub API directly, imports the star as
  an append-only signal, stores the repo as a canonical `github` source, and
  optionally creates and summarizes a linked homepage `web` source. The default
  timeout is `2m` because local CLI-backed repo summaries can take longer than
  a normal GitHub API round trip.
- `dbrain extract links`
  Requires `summarize`. It will prefer cached FT `article_text` when available,
  but still uses `summarize` for normalization and summarization. Use
  `--concurrency` to run multiple source extract/summarize jobs in parallel
  after discovery.
- `dbrain extract sources`
  Requires `summarize`. This is the global source-backlog worker for already
  known sources that still need extraction or summarization. Use
  `--concurrency` to run multiple source extract/summarize jobs in parallel.
- `dbrain worker sources`
  Requires `summarize`. This is the long-running source-backlog worker: it
  repeatedly runs `extract sources`-style batches until the queue is drained,
  and can optionally keep polling for new source work with `--watch`. It also
  supports bounded parallelism via `--concurrency`. Use `--limit` to cap the
  total number of sources processed in a single worker run, and `--batch-limit`
  to control per-cycle batch size.
- `dbrain topic map`
  No external tools required. Builds a topic graph from the local brain using
  search plus the item/source link graph.
- `dbrain entity map`
  No external tools required. Derives stable entities from local item/source
  metadata and searches them by name, key, alias, or domain.
- `dbrain entity generate`
  No external tools required. Writes matching entity notes under
  `vault/entities/...` and refreshes the entity index.
- `dbrain entity index`
  No external tools required. Re-derives all entities, writes their notes, and
  rebuilds `vault/entities/index.md`.
- `dbrain topic generate`
  No external tools required. Writes a synthesized topic/MOC note under
  `vault/topics/...` from the local brain, including sections like `Summary`,
  `What This Topic Is`, `Main Angles`, entity pivots, open questions, and the
  supporting note graph when that evidence exists.
- `dbrain topic refresh`
  No external tools required. Rebuilds generated topic notes from their stored
  frontmatter settings and refreshes the topic index.
- `dbrain topic index`
  No external tools required. Rebuilds the browsable topic index note from the
  generated topic note set.
- `dbrain stats items`
  No external tools required. Reads item counts from `brain.db`.
- `dbrain stats sources`
  No external tools required. Reads source counts from `brain.db`.
- `dbrain stats activity`
  No external tools required. Shows the latest item/source write timestamps plus
  recent write counts inside a configurable time window.
- `dbrain stats backlog`
  No external tools required. Shows remaining queued work by pipeline stage and
  whether the current queues are drained.
- `dbrain ask`
  Retrieval is read-only and works directly from `brain.db`. By default it also
  synthesizes an answer through `summarize`; use `--retrieve-only` when you want
  evidence only and no model call.
- `dbrain repair notes`
  No external tools required. Rebuilds rendered Markdown notes from `brain.db`,
  which is useful if antivirus or sync tooling removed files from `vault/`.
- `dbrain serve mcp`
  No external tools required. Serves the local brain over MCP stdio with
  read-only tools, resources, and prompts for search, note access, and pipeline
  status.
- `dbrain serve web`
  No external tools required. Serves the local brain over HTTP with a read-only
  JSON API and an embedded Svelte UI for search, evidence retrieval, note
  inspection, and a filterable recent source-activity dashboard with failure
  hotspots and failure-kind pivots while workers are running.
- `task web-install`
  Requires `npm`. Installs the Svelte/Vite dependencies used to rebuild the web
  UI source.
- `task web-build`
  Requires `npm`. Rebuilds the embedded `web/ui/dist` assets from the Svelte
  source tree.
- `task fmt`
  Requires `task` and `go`.
- `task lint`
  Requires `task`, `go`, and `golangci-lint`.
- `task test`
  Requires `task` and `go`.

## Examples

```sh
go run ./cmd/dbrain import ft
go run ./cmd/dbrain import x-bookmarks --limit 25
go run ./cmd/dbrain sync all --length short --timeout 5m
go run ./cmd/dbrain sync all --skip-x-media --length short --timeout 5m
go run ./cmd/dbrain sync all --skip-sources --length short --timeout 5m
go run ./cmd/dbrain sync all --watch --poll-interval 1m --idle-exit-after 30m --length short --timeout 5m
go run ./cmd/dbrain import github stars
go run ./cmd/dbrain import youtube --watch-later --liked --browser chrome --profile Default --limit 10 --transcriber auto
go run ./cmd/dbrain entity map "example"
go run ./cmd/dbrain entity map "example/project" --kind project --json
go run ./cmd/dbrain entity generate "example/project" --kind project
go run ./cmd/dbrain entity index
go run ./cmd/dbrain topic map "agent memory" --json
go run ./cmd/dbrain topic generate "vector database"
go run ./cmd/dbrain topic refresh
go run ./cmd/dbrain topic refresh "vector database"
go run ./cmd/dbrain topic index
go run ./cmd/dbrain worker sources --limit 100 --concurrency 4
go run ./cmd/dbrain worker sources --watch --poll-interval 1m --idle-exit-after 30m --concurrency 4 --length short --timeout 5m
go run ./cmd/dbrain hydrate x --limit 50
go run ./cmd/dbrain hydrate x --limit 5
go run ./cmd/dbrain transcribe x-media --limit 50
go run ./cmd/dbrain extract sources --limit 50 --concurrency 4 --length short --timeout 5m
go run ./cmd/dbrain --no-caffeinate extract sources --limit 50 --length short --timeout 5m
go run ./cmd/dbrain repair notes
go run ./cmd/dbrain repair notes --missing-only=false --sources
go run ./cmd/dbrain extract links --discover-limit 100 --limit 25 --concurrency 4 --summarize=false
go run ./cmd/dbrain extract links --discover-limit 25 --limit 10 --concurrency 4 --length short
go run ./cmd/dbrain extract sources --limit 50 --concurrency 4 --length short
go run ./cmd/dbrain stats items
go run ./cmd/dbrain stats items --source-type github_star --group-by none
go run ./cmd/dbrain stats sources --source-type github --extract-tool github-api --group-by summary-status
go run ./cmd/dbrain stats activity
go run ./cmd/dbrain stats activity --window 30m
go run ./cmd/dbrain stats backlog
go run ./cmd/dbrain ask "What validates Kubernetes manifests?" --retrieve-only
go run ./cmd/dbrain ask "What validates Kubernetes manifests?" --timeout 30s
go run ./cmd/dbrain ask "What validates Kubernetes manifests?" --model ollama/qwen2.5:7b-instruct --timeout 2m
go run ./cmd/dbrain ask "Show me GitHub repos about vector databases" --retrieve-only --source-type github
go run ./cmd/dbrain ask "What is Agent Memory?" --retrieve-only --include-related --related-limit 2
go run ./cmd/dbrain extract sources --limit 10 --concurrency 2 --model ollama/qwen2.5:7b-instruct --timeout 10m
go run ./cmd/dbrain serve mcp
go run ./cmd/dbrain serve web
go run ./cmd/dbrain search kubernetes
go run ./cmd/dbrain get x:2045912259210485815
```

The legacy FT importer is incremental and replayable. Re-running `import ft`
scans the current `~/.ft-bookmarks/bookmarks.db`, upserts by stable source key,
skips unchanged rows by content hash, and only rewrites notes when an item
changed or its note is missing.

`import github stars` uses the GitHub API instead of scraping the web UI. It
imports one append-only `github_star` item per starred repository, stores the
canonical repo as a `github` source with repo metadata plus README content, and
links the repo homepage as a separate `web` source when one exists. Re-running
the command is incremental: it fetches newest-first and stops at the first
already-seen star unless you pass `--force`. Unstars are intentionally ignored;
if a repo was starred at some point, that signal is preserved.

`stats items` only tracks imported signal items, so it can stay flat while the
source pipeline is still busy extracting and summarizing linked content. Use
`stats activity` when you want to know whether the background source work is
still moving, and `stats backlog` when you want to know how much queued work
remains before the current pipeline is actually drained.

`worker sources` is the safer long-running alternative to manually rerunning
`extract sources`. By default it keeps batching until the current source queue
is drained and then exits. With `--watch`, it stays alive, sleeps between idle
polls, and can stop automatically after an idle window via
`--idle-exit-after`.

For source enrichment, start with `--concurrency 4` and increase carefully if
your summarize backend and rate limits can handle it. Higher concurrency speeds
up backlog draining, but it also increases provider usage and simultaneous
external fetches.

When you want to test local GPU-backed summarization, pass an Ollama model with
`--model ollama/<name>`. `dbrain` translates that into summarize's
OpenAI-compatible path automatically and defaults to
`http://127.0.0.1:11434/v1`. Override the target with
`DBRAIN_OLLAMA_BASE_URL`, `OLLAMA_BASE_URL`, or `OLLAMA_HOST` if the daemon is
elsewhere. If you already export `OPENAI_BASE_URL` or `OPENAI_API_KEY`,
`dbrain` leaves those alone. When `--model` is set, it also takes precedence
over `--cli`, so local-model runs do not accidentally inherit the default CLI
provider.

For a new machine or GPU-backed A/B run, start with small scoped commands
before pointing a whole sync at Ollama. A practical progression is:

```sh
go run ./cmd/dbrain ask "What validates Kubernetes manifests?" --model ollama/qwen3.5:9b --timeout 2m
go run ./cmd/dbrain extract sources --limit 10 --concurrency 2 --model ollama/qwen3.5:9b --timeout 10m
go run ./cmd/dbrain sync all --source-limit 25 --model ollama/qwen3.5:9b --timeout 10m
```

Good starting local models to compare on a stronger Mac are `qwen3.5:9b`,
`qwen2.5:7b-instruct`, and `gemma4:e4b`. Compare wall-clock time, summary
quality, and whether long GitHub/web extracts stay coherent before switching
the default workflow over.

`ask` is the first query surface built on top of the imported brain. It pulls
top matches from local search, assembles evidence from item/source rows, and
can optionally synthesize a citation-bearing answer through `summarize`. Use
`--retrieve-only` when you want the evidence pack without spending model usage.
Use `--source-type` to narrow retrieval to specific kinds such as `github`,
`web`, or `x_bookmark`, and `--include-related` to append linked evidence from
the item/source graph. It also derives entity matches from the local corpus and
uses them to boost and expand retrieval, so queries can pivot through X
authors, GitHub owners/repos, and important sites even when the raw note text
is weak.

`serve web` is the browser-facing counterpart to the CLI query surface. It
starts a local HTTP server with read-only JSON routes for `search`, `get`,
`stats`, and retrieve-only `ask`, plus an embedded Svelte UI for browsing the
same live `brain.db` while background workers continue running in other
terminals. The homepage is intentionally retrieval-first now: two primary boxes
for `Search` and `Ask`, with result panels and note detail below. Operational
stats, recent failures, hotspots, and other backlog triage views live on
`/admin` instead of competing with the main search flow.

`entity map` derives stable entities from reliable local metadata instead of
free-text NER. The current pass creates entity notes for X authors, GitHub
owners, GitHub repos, and non-generic sites/domains. `entity generate` writes
matching notes under `vault/entities/...`, and `entity index` materializes the
full entity set plus a browsable `vault/entities/index.md`.

`topic map` is the CLI mirror of the MCP topic-map surface. It builds a compact
graph from local search seeds plus item/source graph expansion, then derives
key entities from the mapped nodes. `topic generate` uses the same graph to
write a browsable note under `vault/topics/...` with grouped entity pivots,
suggested starting notes, related notes, relationships, and Obsidian links back
into the corpus. Topic notes now persist their generation settings in
frontmatter, so `topic refresh` can rebuild them later from the local corpus
and `topic index` can regenerate a browsable directory note at
`vault/topics/index.md`.

`serve mcp` exposes the same local brain over MCP stdio. The current server is
read-only and provides:

- tools for `search`, `get`, `ask`, `entity map`, `related`, `stats items`,
  `stats sources`, `stats activity`, `stats backlog`, `topic map`,
  `topic brief`, and `research pack`
- resources for `dbrain://mcp/overview`, `dbrain://stats/activity`,
  `dbrain://stats/backlog`, `dbrain://stats/items`, and `dbrain://stats/sources`
- resource templates for `dbrain://item/{lookup}`, `dbrain://source/{lookup}`,
  `dbrain://search/{query}`, `dbrain://entity/{query}`,
  `dbrain://topic/{query}`, `dbrain://topic-note/{query}`,
  `dbrain://research/{query}`, and queryable `dbrain://stats/...` templates
- prompts for `brain_research`, `brain_browse`, `brain_entity_browse`,
  `brain_topic_map`, `brain_topic_brief`, and `brain_status`

`ask` defaults to retrieval-only in the MCP surface so agent clients do not
silently spend model usage unless they explicitly request answer synthesis.
The tool list also includes `outputSchema` metadata so MCP clients can reason
about the structured payloads without learning them from examples.

`dbrain_research_pack` is the default MCP research entry point for broad
questions. It always returns retrieve-only evidence and, when the question is
conceptual enough to infer a topic phrase, it also attaches the same grouped
topic brief used by `dbrain_topic_brief`. That lets an agent start from one
read-only call instead of manually orchestrating `ask`, `topic brief`, and
follow-up note fetches.

The MCP additions are meant to support three common agent workflows:

- research: `dbrain_research_pack` first, then `dbrain_get` and
  `dbrain_related` for deeper inspection
- graph browsing: `dbrain_get` plus `dbrain_related`
- entity browsing: `dbrain_entity_map` or `brain_entity_browse`, then
  `dbrain_get` on the most relevant entity note
- topic mapping: `dbrain_topic_map` or `brain_topic_map`, plus `dbrain_get`
  when you want to inspect individual nodes more closely
- topic briefs: `dbrain_topic_brief` or `brain_topic_brief`, plus
  `dbrain://topic-note/{query}` when a rendered note preview is useful
- pipeline monitoring: `dbrain_stats_activity`, `dbrain_stats_backlog`, and
  optionally `dbrain_stats_sources`

If a client needs to discover the MCP surface from inside the protocol, start
with:

- resource: `dbrain://mcp/overview`
- prompt: `brain_status` or `brain_research`

A generic MCP client config looks like this:

```json
{
  "mcpServers": {
    "dbrain": {
      "command": "go",
      "args": ["run", "./cmd/dbrain", "serve", "mcp"],
      "cwd": "/Users/darron/src/dbrain"
    }
  }
}
```

If you prefer the compiled binary instead of `go run`, point the client at:

```json
{
  "mcpServers": {
    "dbrain": {
      "command": "./bin/dbrain",
      "args": ["serve", "mcp"],
      "cwd": "/Users/darron/src/dbrain"
    }
  }
}
```

`import youtube` pulls authenticated YouTube signals from `Watch Later` and
liked videos via `yt-dlp --cookies-from-browser`. Existing `youtube_history`
items are pruned on each run so watch-history noise does not pollute the brain.
Each feed entry is stored as a signal item under `vault/items/youtube/...`,
while the canonical video URL is stored once in `sources` and enriched
separately. Re-running the command is idempotent: unchanged signal items are
touched in the DB but not rewritten, while linked sources are only
re-extracted or re-summarized when you force a refresh or the source freshness
rules say they are stale.

If one authenticated feed is temporarily inaccessible, `dbrain` skips that
feed, counts it as an error, and continues with any other selected YouTube
feeds instead of aborting the whole sync run.

YouTube source enrichment is transcript-first. `dbrain` asks `summarize` to
extract the transcript or caption text first, then performs summarization from
that extracted content via stdin. This keeps the summary grounded in the video
content instead of the watch-page chrome. When no transcript is available, the
stored extract may fall back to weaker metadata.

Use `--transcriber auto` (the default) to let `dbrain` try local audio
transcription when captions are missing. On macOS, if the `mw` MacWhisper CLI
is installed, `dbrain` prefers that first. If `mw` is not available, it falls
back to the older `whisper-cli` + `ggml-base.bin` path. If no transcription
backend is configured yet, those videos will be marked as skipped instead of
receiving a misleading metadata-only summary. To enable local or
provider-backed audio transcription, start with:

```sh
summarize transcriber setup
```

If you want to force MacWhisper directly, use:

```sh
go run ./cmd/dbrain import youtube --watch-later --transcriber macwhisper
go run ./cmd/dbrain import youtube --watch-later --transcriber macwhisper:mlx:large-v3-turbo
```

`hydrate x` is a separate enrichment step. It uses the existing FT tweet IDs,
loads your local X session cookies from a browser profile, fetches canonical
post data via X's web GraphQL endpoint, falls back to syndication when needed,
caches the payload and canonical post text in `brain.db`, and rewrites notes
when hydration materially changes what we know about a post.

`sync all` now runs X media audio transcription immediately after `hydrate x`
using the same X batch limit. That means freshly downloaded X MP4s can be
transcribed into `X Media Transcript` item text during the same pipeline run.
Use `--skip-x-media` when you want hydration and media downloads without the
follow-up MacWhisper transcription stage.

On macOS, the first cookie-backed run may trigger a Keychain prompt so Go can
access Chrome's cookie decryption secret. Approve that prompt and re-run the
command if needed.

`hydrate x` emits structured `slog` events for candidate loading, per-post
fetches, fallbacks, and periodic progress by default. Use `--no-debug` to
quiet that output.

`extract links` is the outbound-link enrichment step. It dedupes expanded URLs
from imported X bookmarks into a separate `sources` table, extracts full text
through `summarize`, optionally creates summaries, and writes source notes under
`vault/sources/...` with backlinks to the bookmarks that referenced them.
It only enriches the sources discovered from the current bookmark scan; use
`--force` when you want to rescan older bookmarks and requeue their linked
sources in a controlled batch.

`extract sources` is the global enrichment worker for the existing `sources`
queue. Use it when `stats backlog` shows pending source extraction or summary
work but `extract links` itself has no more bookmark-link discovery to do.

When a linked source already has cached FT `article_text`, `extract links`
prefers that local content instead of live-fetching the URL again. The cached
body is stored as the source extract, then summarized through `summarize` as a
local file so the local copy stays authoritative until you explicitly refresh
it.

Source freshness is tracked explicitly. Each source row stores `extracted_at`,
`summarized_at`, the summary prompt version, the content hash used for the
current summary, and the `summarize` tool version used for extraction and
summarization. Successful summaries are also appended to
`source_summary_versions`, so you can keep a history of summary outputs across
content changes, prompt changes, and summarize upgrades.

`repair notes` is a renderer-only recovery path. It does not re-import or
re-extract anything. It reads items and sources from `brain.db` and recreates
their Markdown notes under `vault/`. By default it only writes missing notes,
which is the intended path after antivirus quarantine or accidental deletion.
