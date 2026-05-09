# RSS and Atom Feed Ingestion Proposal

Status: proposal
Date: 2026-05-09

## Summary

`dbrain` should support subscribed web feeds as another local-first import
source. A user should be able to add an RSS or Atom URL from the CLI or the web
URL-add surface, then have `sync all` check configured feeds and materialize new
or changed feed entries into the normal item/source pipeline.

The importer should be conservative:

- subscribe to feeds explicitly
- include feed checks in `sync all` by default
- fetch feeds with HTTP conditional requests when possible
- only materialize a feed entry once while its normalized content is unchanged
- update the existing local item when the same feed entry changes
- preserve raw feed and entry data for later reprocessing
- never delete local memory because an entry disappears from the feed
- queue canonical article/source URLs for the existing extraction, summary,
  categorization, search, and note-rendering stages

RSS is inconsistently implemented in the wild, and Atom/JSON Feed do not remove
all ambiguity. The implementation should therefore use a battle-tested feed
parser, but keep subscription state, HTTP caching, identity, dedupe, update
policy, and dbrain materialization in dbrain-owned code.

## Parser Dependency

Use `github.com/mmcdole/gofeed` as the feed parser dependency.

Rationale:

- MIT licensed.
- Native Go package that fits the single-binary/Homebrew direction.
- Universal parser normalizes RSS, Atom, and JSON Feed into one feed/item model.
- Supports RSS 0.90 through 2.0, Atom 0.3 and 1.0, and JSON Feed 1.0 and 1.1.
- Handles common malformed-feed cases on a best-effort basis, including bad
  XML, namespace oddities, missing tags, and incorrect date formats.
- Preserves feed extensions so dbrain can inspect namespaced metadata later
  without blocking the first implementation.

Do not use `gofeed.ParseURL` as the main fetch path. It is fine for tests or
throwaway tooling, but production ingestion needs a dbrain-owned HTTP fetcher so
we can control:

- User-Agent
- `Accept`
- timeout
- redirect policy
- maximum response size
- gzip/charset handling
- `If-None-Match`
- `If-Modified-Since`
- raw response body hashing
- response metadata persistence

References:

- https://github.com/mmcdole/gofeed
- https://pkg.go.dev/github.com/mmcdole/gofeed

## Goals

- Add feed subscriptions from the CLI.
- Add feed subscriptions from the web URL-add flow.
- Check subscribed feeds from `sync all`.
- Let scheduled `sync all` keep feeds current for launchd-backed installs.
- Avoid duplicate items for unchanged feed entries.
- Detect changed feed entries and update the existing local item.
- Preserve raw feed-entry data separately from summaries and extracted article
  text.
- Link feed entries to normal `sources` rows so existing extraction and
  enrichment continue to work.
- Report feed status and import stats clearly.
- Keep tests independent of network access and personal configuration.

## Non-Goals

- No full feed-reader read/unread state in the first implementation.
- No automatic deletion when an item disappears from a feed.
- No WebSub/push feed support in the first implementation.
- No OPML import/export in the first implementation.
- No authenticated/private feeds in the first implementation unless the URL
  itself is already enough to fetch the feed.
- No media enclosure downloader in the first implementation.
- No attempt to repair websites that do not expose feeds.

## User Model

Feeds are subscriptions. Feed entries are imported evidence. Linked article URLs
are normal web sources.

Example flow:

1. User adds `https://example.com/feed.xml`.
2. dbrain fetches and parses the feed.
3. dbrain stores or updates the feed subscription row.
4. dbrain materializes each new or changed entry as a `feed_entry` item.
5. dbrain queues the entry's canonical article URL as a `sources` row.
6. Existing source extraction, link extraction, source summaries, item/source
   categorization, search, research, MCP, and note rendering handle the rest.

This keeps feed ingestion as an import stage instead of creating a parallel
reader database.

## CLI

Add a top-level `feed` command group:

```text
dbrain feed add <url> [--title <title>] [--tag <tag>] [--poll-interval 1h] [--disabled] [--check]
dbrain feed list [--json]
dbrain feed status [<feed-key-or-url>] [--json]
dbrain feed check [<feed-key-or-url>] [--limit N] [--force] [--enrich]
dbrain feed disable <feed-key-or-url>
dbrain feed enable <feed-key-or-url>
```

Behavior:

- `feed add` stores the subscription and fetches enough metadata to verify it is
  a parseable feed unless `--disabled` is passed.
- `feed add --check` immediately imports available entries.
- `feed check` fetches one feed or all enabled feeds.
- `feed check --force` ignores stored ETag/Last-Modified and reparses the body.
  It still dedupes entries by identity and content hash.
- `feed check --enrich` queues and processes source enrichment for newly queued
  article URLs, matching the spirit of `link add --enrich`.

## Web

Extend the existing URL-add flow instead of adding a separate first-pass UI.
Feed management should stay CLI-only in the first implementation.

When `/api/links` receives a URL:

- If the URL is clearly a web feed by content type, parse result, or feed
  autodiscovery result, add it as a feed subscription.
- If the URL is a normal article/page, keep the current `linkadd` behavior.
- If a normal page advertises feeds with `<link rel="alternate">`, use the
  shared autodiscovery helper to return feed candidates. The first
  implementation can expose this through API output without building a full feed
  management UI.

The web UI may eventually expose a compact feed management view:

- URL
- title
- enabled/paused status
- last checked
- last successful fetch
- next eligible check
- last error
- created/updated/unchanged entry counts

The first implementation should keep management CLI-only if URL-add
subscription works and status is available through the CLI.

## Configuration

Feed subscriptions should live in SQLite, not primarily in `config.yaml`.
Configuration should control defaults and whether feed checks are included in
`sync all`.

Suggested config:

```yaml
feeds:
  enabled: true
  default_poll_interval: 1h
  user_agent: "dbrain/<version> (+https://github.com/darron/dbrain)"
  accept_markdown: true
  max_body_bytes: 10485760
  request_timeout: 30s
```

Suggested env vars:

```text
DBRAIN_FEEDS_ENABLED
DBRAIN_FEEDS_DEFAULT_POLL_INTERVAL
DBRAIN_FEEDS_USER_AGENT
DBRAIN_FEEDS_ACCEPT_MARKDOWN
DBRAIN_FEEDS_MAX_BODY_BYTES
DBRAIN_FEEDS_REQUEST_TIMEOUT
```

`sync all` should include feeds by default. When no feed subscriptions exist, it
should report `No feeds configured.` and continue without treating that as an
error.

```text
dbrain sync all --skip-feeds
dbrain sync all --feed-limit 50
dbrain sync all --feed-item-limit 500
```

The scheduler should not need feed-specific scheduling logic at first. If
`scheduler.sync_all.enabled` is true, scheduled runs will check feeds
periodically through the normal stage plan unless `--skip-feeds` or its
scheduled config equivalent is set.

## Data Model

### `feeds`

Stores one subscription per normalized feed URL.

Suggested columns:

- `id`
- `feed_key` unique, stable local key such as `feed:<sha256-normalized-url>`
- `url` original submitted URL
- `normalized_url` unique normalized feed URL
- `resolved_url` last final URL after redirects
- `site_url`
- `title`
- `description`
- `language`
- `status` one of `enabled`, `paused`, `error`, `dead`
- `poll_interval_seconds`
- `next_fetch_after`
- `fetch_etag`
- `fetch_last_modified`
- `fetch_body_hash`
- `last_checked_at`
- `last_fetched_at`
- `last_changed_at`
- `last_success_at`
- `last_error`
- `error_count`
- `created_at`
- `updated_at`
- `raw_json` latest normalized feed metadata
- `user_tags`

Indexes:

- unique `feed_key`
- unique `normalized_url`
- `status, next_fetch_after`
- `last_success_at`

### `feed_entries`

Stores one logical entry per feed-local identity.

Suggested columns:

- `id`
- `feed_id`
- `entry_key` unique, stable key such as
  `feed-entry:<sha256-feed-id-and-identity>`
- `identity_key`
- `guid`
- `guid_is_permalink`
- `link`
- `normalized_link`
- `title`
- `author`
- `published_at`
- `updated_at`
- `summary_html`
- `summary_text`
- `content_html`
- `content_markdown`
- `content_text`
- `enclosures_json`
- `extensions_json`
- `raw_json`
- `content_hash`
- `version`
- `item_id` linked `items.id`
- `source_id` linked `sources.id` for the canonical URL when available
- `first_seen_at`
- `last_seen_at`
- `last_changed_at`
- `created_at`
- `updated_at`

Indexes:

- unique `feed_id, identity_key`
- unique `entry_key`
- `feed_id, last_seen_at`
- `normalized_link`
- `item_id`
- `source_id`

### `feed_entry_versions`

Optional but recommended because feed entry content changes are exactly the sort
of raw data dbrain should preserve for later reprocessing.

Suggested columns:

- `id`
- `feed_entry_id`
- `version`
- `content_hash`
- `raw_json`
- `content_markdown`
- `content_html`
- `content_text`
- `summary_html`
- `summary_text`
- `observed_at`

The current entry row stores the latest version. Version rows preserve prior
observations.

### Materialized `items`

Each current feed entry should have one materialized `items` row.

Suggested mapping:

- `source_type`: `feed_entry`
- `source_key`: same as `feed_entries.entry_key`
- `external_id`: `feed_entries.identity_key`
- `canonical_url`: entry canonical link when present, otherwise feed URL
- `title`: entry title
- `author_name`: entry author
- `published_at`: entry published time if present
- `synced_at`: current feed-check time
- `text`: normalized entry text, preferring Markdown entry content when present
- `links_json`: canonical article URL plus any extracted links from entry HTML
- `content_hash`: entry content hash
- `raw_json`: normalized entry JSON plus feed metadata needed to understand it
- `last_seen_at`: current feed-check time

When the feed entry content hash changes, update this same item row rather than
creating a duplicate.

### Materialized `sources`

If the feed entry has a canonical link, upsert it through the same normalization
path used by `linkadd`. Then create or retain an `item_source_links` row from
the feed entry item to the canonical source.

Do not treat the feed XML URL as the primary article source. The feed is the
subscription source; the entry link is the evidence source users expect to open,
extract, summarize, and cite.

## Entry Identity and Dedupe

Feed entry identity must be feed-local. Different feeds can syndicate the same
article, and dbrain should be able to remember both observations while linking
them to the same canonical `sources` row.

Identity priority:

1. Stable item GUID/ID, if present.
2. Normalized canonical item link, if present.
3. Hash of feed key, title, published date, and first useful content text.

Rules:

- Normalize whitespace and obvious URL variants before hashing.
- Do not include feed position/order in identity or content hash.
- Do not include fetch time in identity or content hash.
- Do not trust GUID blindly across feeds; scope it to `feed_id`.
- If GUID changes but normalized link matches an existing entry in the same
  feed, treat it as the same entry and record the new GUID as observed metadata.
- If link changes but GUID matches, treat it as the same entry and update the
  source link if the new canonical URL is better.
- If both GUID and link are missing, use the fallback hash and accept that some
  broken feeds may create duplicates when titles or bodies churn.

Content hash inputs:

- title
- canonical link
- author
- published/updated timestamps when present
- content Markdown
- content HTML/text
- summary HTML/text
- enclosures
- stable extension fields that affect user-visible entry content

Content hash exclusions:

- feed fetch time
- feed entry position
- transient tracking query parameters after URL normalization
- feed-level title/description/site changes
- parser-generated ordering differences

## Entry Content Preference

If a feed entry exposes a Markdown representation, prefer it for dbrain's
normalized entry text and content hash.

RSS and Atom do not define one universal Markdown field, so this should be
implemented as a best-effort convention rather than a hard dependency. The
importer should recognize obvious Markdown content from:

- extension fields whose names clearly indicate Markdown, such as
  `content_markdown`, `markdown`, or `md`
- Atom content with a Markdown-ish media type such as `text/markdown` when
  available through parser metadata
- JSON Feed extension fields that carry Markdown content

Feed URL and feed autodiscovery fetches should not request Markdown. Keep those
requests conventional so RSS, Atom, and JSON Feed endpoints are not confused by
an agent-oriented content negotiation header.

When fetching linked article/source URLs, dbrain should send an `Accept` header
that prefers Markdown while still accepting HTML and fallback responses. A
starting point:

```text
Accept: text/markdown;q=1.0, text/html;q=0.8, application/xhtml+xml;q=0.7, */*;q=0.1
```

For linked article/source fetches, a `text/markdown` response should be
preserved as extracted raw content without first round-tripping through HTML
extraction.

If a response is `text/markdown`, store the Markdown directly and record
response metadata such as `Content-Type`, `Vary: Accept`, and provider-specific
headers like Cloudflare's `x-markdown-tokens` when present. This lets dbrain
distinguish origin-provided Markdown from HTML-derived extraction later.

This should be implemented in the shared source extraction path, not only in the
feed importer, because Markdown responses are likely better summary inputs for
any manually added URL or imported source.

Rules:

- Preserve the original raw feed data even when Markdown is selected.
- Store Markdown separately from HTML and plain text.
- Use Markdown as the preferred source for item `text` and search input.
- Use Markdown as the preferred source for feed-entry note rendering.
- Fall back to HTML-derived text, then summary/description text, when Markdown
  is absent.
- Still queue and summarize the linked article/source as the primary enrichment
  target. Feed entry Markdown is imported evidence and search context, not a
  replacement for the linked source extraction path.

If identity is unchanged and content hash is unchanged:

- update `feed_entries.last_seen_at`
- update feed stats
- do not update the item
- do not rerender notes
- do not requeue source extraction

If identity is unchanged and content hash changes:

- append a `feed_entry_versions` row for the previous current version
- update `feed_entries`
- update the linked `items` row
- rerender the item note
- re-run link extraction for the item
- upsert or relink the canonical source if the canonical URL changed
- do not force source re-extraction unless the source URL itself changed or a
  separate force flag requests it

If an entry disappears from a later feed response:

- do not delete the entry
- do not delete the item
- do not delete the source
- leave `last_seen_at` as the last observation time
- optionally expose stale/not-seen-since status in feed diagnostics later

## Feed Fetching and Change Detection

Feed-level change detection is an optimization. Entry-level identity and content
hashing is the source of truth for duplicate prevention.

Fetch flow:

1. Select enabled feeds with `next_fetch_after <= now`.
2. Send `If-None-Match` when `fetch_etag` is known.
3. Send `If-Modified-Since` when `fetch_last_modified` is known.
4. On `304 Not Modified`, update `last_checked_at`, clear transient errors, and
   stop.
5. On `200 OK`, read the bounded body, hash the raw bytes, parse the feed, and
   persist current response metadata.
6. If the raw body hash matches `fetch_body_hash`, update fetch metadata but
   skip entry materialization unless `--force` was requested.
7. If the raw body hash changed, materialize entries and update feed metadata.
8. On retryable failures, record the error, increment `error_count`, and compute
   a backoff-based `next_fetch_after`.
9. On terminal-looking failures, such as repeated `404` or `410`, mark the feed
   `dead` only after multiple failed checks.

Important edge cases:

- ETags may be weak, absent, unstable, or wrong.
- Some feeds change ETags every request even when the body is unchanged.
- Some feeds never return `304`.
- Some feeds return the wrong content type.
- Some feeds redirect permanently from an old URL to a new URL.
- Some feeds gzip responses or use unusual charsets.
- Some feeds are large or effectively unbounded.

Therefore:

- Always keep raw body hashing as a backstop.
- Store original URL and last resolved URL separately.
- Respect permanent redirects cautiously; do not silently collapse two existing
  feed subscriptions without an explicit merge path.
- Enforce `max_body_bytes`.
- Use a dbrain User-Agent that makes the client identifiable without leaking
  local paths or secrets.

## Feed Format Messiness

The importer should normalize enough to be useful without pretending RSS is
clean.

Known issues to handle:

- RSS GUID can be a permalink, a stable opaque ID, a changing string, or absent.
- Atom uses `id`, `updated`, and `published`, but many feeds omit or misuse
  them.
- Dates appear in many formats; if parsing fails, fall back to `first_seen_at`.
- Entry content may be in `content:encoded`, Atom content, description, summary,
  or extension fields.
- Entry content may include custom Markdown fields. Prefer Markdown when it is
  clearly present, but keep HTML/text fallbacks because there is no single
  Markdown standard across RSS and Atom feeds.
- Links can be relative; resolve them against the feed URL.
- Atom entries can have multiple links; prefer `rel=alternate` HTML links.
- Feeds may include duplicate entries in one response.
- Feeds may include entries with no useful link.
- Feeds may reorder entries on every request.
- Feeds may mutate old entries.
- Feeds may publish the same article under multiple categories or feeds.
- HTML content may contain tracking links or relative media URLs.
- Enclosures may point to audio, video, or images that are not normal articles.

The first implementation should preserve the messy raw material in `raw_json`
and implement only the minimal normalization needed for stable identity,
content hashing, source linking, and search.

## Sync Integration

Add a feed stage to the `sync all` plan after local app imports and before link
extraction/source enrichment.

Recommended order:

1. Apple Notes, when enabled.
2. Safari Tabs, when enabled.
3. Feeds, when enabled.
4. X imports/hydration/media stages.
5. Link extraction.
6. GitHub stars.
7. YouTube.
8. Source worker.
9. Categorization.
10. Media archive.

The exact placement can be adjusted, but feeds should run before the source
worker so newly discovered article sources can be extracted in the same sync
run.

Feeds are enabled for `sync all` by default. If no feed subscriptions exist, the
stage should emit a clear no-op message and continue.

Suggested feed sync stats:

- `FeedsChecked`
- `FeedsUnchanged`
- `FeedsChanged`
- `FeedsSkipped`
- `FeedsErrored`
- `EntriesSeen`
- `EntriesCreated`
- `EntriesUpdated`
- `EntriesUnchanged`
- `EntriesSkipped`
- `SourcesCreated`
- `SourcesExisting`
- `SourcesQueued`
- `Errors`

The CLI summary should make unchanged-current rows cheap and clear rather than
printing per-entry noise.

## Preflight

Feed ingestion should not require secrets for public feeds.

Preflight should check:

- feed support selected but feed DB schema unavailable: hard fail
- invalid configured duration/size defaults: hard fail
- public feed subscriptions enabled with no subscriptions: informational no-op,
  not fail
- scheduled feed checks enabled through `sync all`: use normal `sync all`
  preflight

Later authenticated feed support should use the existing secret-reference model
instead of embedding credentials in config.

## Notes and Search

Feed entry notes should include:

- feed title and URL
- entry title
- author
- published/updated dates
- canonical link
- Markdown content from the feed entry when present
- summary/content text from the feed entry when Markdown is absent
- links discovered inside the entry
- linked source keys
- dbrain tags/categories once derived

Search inputs should include the entry title, author, feed title, Markdown
content when present, summary text, content text, and linked source metadata.
Raw HTML should be stored but not dumped directly into FTS without
normalization.

Feed entry items should not get their own LLM summaries in the first
implementation. The linked article/source is the primary summarization target.

## Implementation Plan

1. Add `github.com/mmcdole/gofeed`.
2. Add feed schema migration.
3. Add `internal/feedimport` with:
   - HTTP fetcher
   - conditional request support
   - body hashing
   - parser wrapper
   - shared feed autodiscovery helper used by CLI and web paths
   - feed metadata normalization
   - entry identity/content hashing
   - item/source materialization
4. Add store methods for feed subscriptions, feed fetch metadata, entry upserts,
   and feed stats.
5. Add `dbrain feed ...` commands.
6. Add `sync all` feed stage and summary output.
7. Extend `/api/links` to detect feed URLs and return subscription results.
8. Document CLI, config, and sync behavior in README.
9. Add focused fixture-based tests.

## Tests

Tests must be fixture-based and safe for GitHub Actions.

Required coverage:

- RSS 2.0 fixture with GUID and content.
- Atom fixture with ID, alternate links, published, and updated dates.
- JSON Feed fixture if enabled through `gofeed`.
- Feed with no GUID but stable link.
- Feed with no GUID and no link, using fallback identity.
- Duplicate entries in the same response.
- Reordered feed does not update unchanged entries.
- Entry content update changes one existing item, not a new item.
- Entry with Markdown content prefers Markdown for item text and search input.
- GUID changed but link stable updates existing entry.
- Link changed but GUID stable updates existing entry and source link.
- Entry disappeared from later response is not deleted.
- `304 Not Modified` skips parsing and materialization.
- Changed ETag with identical body hash does not reprocess entries.
- Malformed feed returns a useful error and updates feed diagnostics.
- Oversized body is rejected without storing partial feed content.
- CLI `feed add`, `feed list`, and `feed check`.
- `/api/links` keeps normal URL behavior and routes feed URLs to subscription.
- `sync all` includes feeds by default.
- `sync all` with no subscriptions prints a no-op message and succeeds.

Do not require network access, local browser profiles, model services, or
personal secrets in these tests.

## Resolved Decisions

- Feeds are enabled for `sync all` by default.
- `sync all` with no feed subscriptions should clearly say that no feeds are
  configured and continue.
- Feed management is CLI-only in the first implementation.
- The web URL-add path may add feed subscriptions, but no full web feed
  management UI is required in the first implementation.
- LLM summarization should target linked article/source items, not the feed
  entry itself.
- Enclosures such as videos, images, audio, or other attachments should remain
  preserved metadata in the first implementation.
- OPML import/export is not needed in the first implementation.
- Feed autodiscovery should live in a shared helper used by both CLI and web
  paths.
- Markdown entry content, when clearly available, should be preferred over HTML
  or plain text for imported feed entry text.
- Feed and feed autodiscovery fetches should not request Markdown.
- Linked article/source fetches should send an `Accept` header that prefers
  `text/markdown` while still accepting HTML and fallback responses.
- Markdown source extraction should live in the shared source extraction path so
  manually added URLs and other importers benefit too.
