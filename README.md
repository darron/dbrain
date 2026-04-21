# dbrain

`dbrain` is a local-first second-brain scaffold for incremental imports from
`ft-bookmarks`, Markdown note rendering for Obsidian, and local query over the
imported corpus.

## Current Commands

- `dbrain import ft`
- `dbrain import github stars`
- `dbrain import youtube`
- `dbrain hydrate x`
- `dbrain extract links`
- `dbrain extract sources`
- `dbrain stats items`
- `dbrain stats sources`
- `dbrain stats activity`
- `dbrain stats backlog`
- `dbrain search <query>`
- `dbrain get <source-key-or-id>`

## Dev Tasks

- `task fmt`
- `task lint`
- `task test`

## Layout

- `data/brain.db`: local SQLite state
- `vault/items/...`: rendered Markdown notes for Obsidian
- `vault/sources/...`: rendered Markdown notes for linked sources

## Prerequisites

- `go` `1.26` to build and run `dbrain`
- a local FT archive at `~/.ft-bookmarks/bookmarks.db` or another path passed to `dbrain import ft --source ...`
- a supported browser with active logged-in sessions for cookie-backed flows
- `chrome` is the current recommended browser for both X and YouTube ingestion
- `GITHUB_TOKEN` for `dbrain import github stars`
  `dbrain` will also fall back to `./.envrc` or `./.env` when the shell
  environment does not already export it.

`dbrain` reads the FT SQLite archive directly. The `ft` CLI is not required at
runtime, but you still need `fieldtheory-cli` or another process to keep
`~/.ft-bookmarks/bookmarks.db` fresh.

For GitHub stars, use a fine-grained PAT with:

- `User permissions`: `Starring: Read`
- `Repository permissions`: `Metadata: Read`
- `Repository permissions`: `Contents: Read`

## External Tools

- `summarize`
  Required for `dbrain extract links` and YouTube source enrichment.
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
- `~/.summarize/cache/whisper-cpp/models/ggml-base.bin`
  Optional, but required by the local `whisper.cpp` fallback. A working setup is
  `whisper-cli` plus this model path.
- `task`
  Required for the top-level dev tasks: `task fmt`, `task lint`, `task test`.
- `golangci-lint`
  Required for `task lint`.
- `sqlite3`
  Optional, but useful for inspecting `data/brain.db` during debugging.

## Command Requirements

- `dbrain import ft`
  Requires the FT bookmarks SQLite database. No external binary is invoked.
- `dbrain hydrate x`
  Requires a supported browser profile with valid X cookies. Chrome/Chromium is
  the best-tested path. On macOS you may see a Keychain prompt the first time
  cookie decryption is used.
- `dbrain import youtube`
  Requires a browser profile with valid YouTube cookies, `yt-dlp`, and
  `summarize`. A working local setup may also need `uv`. For transcriptless videos, the best current setup is also
  `deno` or `node`, plus `whisper-cli` and the `ggml-base.bin` model.
- `dbrain import github stars`
  Requires `GITHUB_TOKEN`. It uses the GitHub API directly, imports the star as
  an append-only signal, stores the repo as a canonical `github` source, and
  optionally creates and summarizes a linked homepage `web` source. The default
  timeout is `2m` because local CLI-backed repo summaries can take longer than
  a normal GitHub API round trip.
- `dbrain extract links`
  Requires `summarize`. It will prefer cached FT `article_text` when available,
  but still uses `summarize` for normalization and summarization.
- `dbrain extract sources`
  Requires `summarize`. This is the global source-backlog worker for already
  known sources that still need extraction or summarization.
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
- `task fmt`
  Requires `task` and `go`.
- `task lint`
  Requires `task`, `go`, and `golangci-lint`.
- `task test`
  Requires `task` and `go`.

## Examples

```sh
go run ./cmd/dbrain import ft
go run ./cmd/dbrain import github stars --debug
go run ./cmd/dbrain import youtube --watch-later --browser chrome --profile Default --limit 10 --transcriber auto --debug
go run ./cmd/dbrain hydrate x --limit 50
go run ./cmd/dbrain hydrate x --limit 5 --debug
go run ./cmd/dbrain extract links --discover-limit 100 --limit 25 --summarize=false
go run ./cmd/dbrain extract links --discover-limit 25 --limit 10 --cli codex --length short --debug
go run ./cmd/dbrain extract sources --limit 50 --cli codex --length short --debug
go run ./cmd/dbrain stats items
go run ./cmd/dbrain stats items --source-type github_star --group-by none
go run ./cmd/dbrain stats sources --source-type github --extract-tool github-api --group-by summary-status
go run ./cmd/dbrain stats activity
go run ./cmd/dbrain stats activity --window 30m
go run ./cmd/dbrain stats backlog
go run ./cmd/dbrain search kubernetes
go run ./cmd/dbrain get x:2045912259210485815
```

The importer is incremental and replayable. Re-running `import ft` scans the
current `~/.ft-bookmarks/bookmarks.db`, upserts by stable source key, skips
unchanged rows by content hash, and only rewrites notes when an item changed or
its note is missing.

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

`import youtube` pulls authenticated YouTube signals from `Watch Later`,
history, and liked videos via `yt-dlp --cookies-from-browser`. Each feed entry
is stored as a signal item under `vault/items/youtube/...`, while the canonical
video URL is stored once in `sources` and enriched separately. Re-running the
command is idempotent: unchanged signal items are touched in the DB but not
rewritten, while linked sources are only re-extracted or re-summarized when you
force a refresh or the source freshness rules say they are stale.

YouTube source enrichment is transcript-first. `dbrain` asks `summarize` to
extract the transcript or caption text first, then performs summarization from
that extracted content via stdin. This keeps the summary grounded in the video
content instead of the watch-page chrome. When no transcript is available, the
stored extract may fall back to weaker metadata.

Use `--transcriber auto` (the default) to let `summarize` fall back to audio
transcription when captions are missing. If no transcription backend is
configured yet, those videos will be marked as skipped instead of receiving a
misleading metadata-only summary. To enable local or provider-backed audio
transcription, start with:

```sh
summarize transcriber setup
```

`hydrate x` is a separate enrichment step. It uses the existing FT tweet IDs,
loads your local X session cookies from a browser profile, fetches canonical
post data via X's web GraphQL endpoint, falls back to syndication when needed,
caches the payload and canonical post text in `brain.db`, and rewrites notes
when hydration materially changes what we know about a post.

On macOS, the first cookie-backed run may trigger a Keychain prompt so Go can
access Chrome's cookie decryption secret. Approve that prompt and re-run the
command if needed.

Use `--debug` on `hydrate x` to emit structured `slog` events for candidate
loading, per-post fetches, fallbacks, and periodic progress.

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
