# dbrain

`dbrain` is a local-first second-brain scaffold for incremental imports from
`ft-bookmarks`, Markdown note rendering for Obsidian, and local query over the
imported corpus.

## Current Commands

- `dbrain import ft`
- `dbrain hydrate x`
- `dbrain extract links`
- `dbrain search <query>`
- `dbrain get <source-key-or-id>`

## Layout

- `data/brain.db`: local SQLite state
- `vault/items/...`: rendered Markdown notes for Obsidian
- `vault/sources/...`: rendered Markdown notes for linked sources

## Examples

```sh
go run ./cmd/dbrain import ft
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

Source freshness is tracked explicitly. Each source row stores `extracted_at`,
`summarized_at`, the summary prompt version, the content hash used for the
current summary, and the `summarize` tool version used for extraction and
summarization. Successful summaries are also appended to
`source_summary_versions`, so you can keep a history of summary outputs across
content changes, prompt changes, and summarize upgrades.
