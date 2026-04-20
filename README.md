# dbrain

`dbrain` is a local-first second-brain scaffold for incremental imports from
`ft-bookmarks`, Markdown note rendering for Obsidian, and local query over the
imported corpus.

## Current Commands

- `dbrain import ft`
- `dbrain import youtube`
- `dbrain hydrate x`
- `dbrain extract links`
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

## Examples

```sh
go run ./cmd/dbrain import ft
go run ./cmd/dbrain import youtube --watch-later --browser chrome --profile Default --limit 10 --transcriber auto --debug
go run ./cmd/dbrain hydrate x --limit 50
go run ./cmd/dbrain hydrate x --limit 5 --debug
go run ./cmd/dbrain extract links --discover-limit 100 --limit 25 --summarize=false
go run ./cmd/dbrain extract links --discover-limit 25 --limit 10 --cli codex --length short --debug
go run ./cmd/dbrain search kubernetes
go run ./cmd/dbrain get x:2045912259210485815
```

The importer is incremental and replayable. Re-running `import ft` scans the
current `~/.ft-bookmarks/bookmarks.db`, upserts by stable source key, skips
unchanged rows by content hash, and only rewrites notes when an item changed or
its note is missing.

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

When a linked source already has cached FT `article_text`, `extract links`
prefers that local content instead of live-fetching the URL again. The cached
body is stored as the source extract, then summarized through `summarize` from
stdin so the local copy stays authoritative until you explicitly refresh it.

Source freshness is tracked explicitly. Each source row stores `extracted_at`,
`summarized_at`, the summary prompt version, the content hash used for the
current summary, and the `summarize` tool version used for extraction and
summarization. Successful summaries are also appended to
`source_summary_versions`, so you can keep a history of summary outputs across
content changes, prompt changes, and summarize upgrades.
