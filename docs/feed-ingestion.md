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
dbrain feed add <url> [--tags <tags>] [--poll-interval 1h] [--disabled] [--check=false]
dbrain feed list [--json]
dbrain feed status <feed-key-or-url> [--json]
dbrain feed check [<feed-key-or-url>] [--limit N] [--force]
dbrain feed disable <feed-key-or-url>
dbrain feed enable <feed-key-or-url>
```

Behavior:

- `feed add` stores the subscription without importing entries by default.
- `feed add --check` immediately imports available entries.
- `feed add --no-fetch` and `feed add --disabled` avoid initial feed
  autodiscovery/fetch work.
- `feed check` fetches one feed or all enabled feeds.
- `feed check --force` ignores stored ETag/Last-Modified and reparses the body.
  It still dedupes entries by identity and content hash.
- `feed disable` sets `enabled=false` without deleting feed, entry, item, or
  source rows.
- `feed enable` sets `enabled=true`, resets `health_status` to `ok`, clears
  failure diagnostics, and makes the feed eligible for an immediate check.

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

Do not auto-subscribe to multiple autodiscovered feeds. If a page advertises one
feed, the CLI may subscribe to that feed after reporting the resolved feed URL.
If a page advertises multiple feeds, return or print the candidates and require
the user to choose an exact feed URL.

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

The `/api/links` response contract should distinguish these outcomes:

- normal source added
- feed subscribed
- multiple feed candidates returned, with no automatic subscription

## Configuration

Feed subscriptions should live in SQLite, not primarily in `config.yaml`.
Configuration should control defaults and whether feed checks are included in
`sync all`.

Suggested config:

```yaml
feeds:
  enabled: true
  default_poll_interval: 1h
  max_concurrent_fetches: 6
  user_agent: "dbrain/<version> (+https://github.com/darron/dbrain)"
  max_body_bytes: 10485760
  request_timeout: 30s

extraction:
  accept_markdown: true
```

Suggested env vars:

```text
DBRAIN_FEEDS_ENABLED
DBRAIN_FEEDS_DEFAULT_POLL_INTERVAL
DBRAIN_FEEDS_MAX_CONCURRENT_FETCHES
DBRAIN_FEEDS_USER_AGENT
DBRAIN_FEEDS_MAX_BODY_BYTES
DBRAIN_FEEDS_REQUEST_TIMEOUT
DBRAIN_EXTRACTION_ACCEPT_MARKDOWN
```

`sync all` should include feeds by default. When no feed subscriptions exist, it
should report `No feeds configured.` and continue without treating that as an
error.

```text
dbrain sync all --skip-feeds
dbrain sync all --feed-limit 50
```

The scheduler should not need feed-specific scheduling logic at first. If
`scheduler.sync_all.enabled` is true, scheduled runs will check feeds
periodically through the normal stage plan unless `--skip-feeds` or its
scheduled config equivalent is set.

### Production health audit

`dbrain audit feeds --json` fetches every enabled configured feed through a
read-only parity path. It deliberately forces unconditional bodies so stored
ETags, Last-Modified values, and body hashes cannot hide upstream identities,
but it never writes feed health, fetch history, entries, items, or sources.
Identity matching uses the importer's primary feed-local identity plus current
GUID and normalized-link aliases so existing rows remain matchable across feed
identity evolution. Disappearing upstream entries do not imply local deletion.

The command defaults to deep and is bounded to five minutes, 10,000 feeds, and
100,000 unique identity hashes. Fetch, parse, response-size, cancellation, or
completion failures produce a content-free `unknown` result. Public feeds use
the normal no-proxy safe-network policy; when private feeds are enabled, only
the exact origins of enabled configured feeds receive private-network
authority, and redirects are revalidated. Scheduled audits, MCP, and the admin
API cannot run this deep network inventory.

`feed add --poll-interval` and `feeds.default_poll_interval` should accept Go
duration strings such as `30m`, `1h`, and `24h`. Store the resolved per-feed
value as integer seconds in SQLite and render it back as a human-readable
duration in `feed list` and `feed status`.

`feeds.max_concurrent_fetches` limits concurrent feed HTTP requests. The default
should be small enough to avoid looking like a flood to feed servers while still
making many-feed syncs practical; start with `6`.

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
- `enabled` boolean for operator intent
- `health_status` one of `ok`, `error`, `blocked`, `dead`
- `poll_interval_seconds`
- `next_fetch_after`
- `fetch_etag`
- `fetch_last_modified`
- `fetch_body_hash` hash of decoded response body bytes
- `last_checked_at`
- `last_fetched_at`
- `last_changed_at`
- `last_success_at`
- `failure_kind`
- `first_failed_at`
- `last_failed_at`
- `last_http_status`
- `last_error`
- `error_count`
- `created_at`
- `updated_at`
- `latest_normalized_json` latest normalized feed metadata, not historical raw
  evidence
- `user_tags`

Indexes:

- unique `feed_key`
- unique `normalized_url`
- `enabled, health_status, next_fetch_after`
- `last_success_at`

Feed URL normalization must be conservative. Do not strip query parameters from
feed subscription URLs because they are often semantically meaningful for feed
format, filters, or tokenized private URLs. Redact likely tokens in logs,
diagnostics, and rendered output.

Duplicate subscriptions are blocked by default when a newly fetched feed
resolves to the same `resolved_url` as an existing enabled subscription. Keep
the original submitted URL on the existing subscription. A later explicit merge
command can handle historical duplicate subscriptions if needed.

`feed enable` should set `enabled=true`, reset `health_status` to `ok`, clear
failure diagnostics, and make the feed eligible for an immediate check.

### `feed_fetches`

Preserves raw feed responses for later reparsing.

Suggested columns:

- `id`
- `feed_id`
- `observed_at`
- `request_url`
- `final_url`
- `http_status`
- `headers_json`
- `content_encoding`
- `decoded_body_hash`
- `wire_response_bytes` compressed response bytes as received
- `decoded_size_bytes`
- `parse_status`
- `parse_error`

Retain successful changed bodies and failed parse bodies with
`wire_response_bytes`. For a `200 OK` response whose decoded body hash matches
the prior fetch, write an audit row with metadata and `decoded_body_hash`; body
bytes may be omitted to avoid duplicate storage. This table is the raw evidence
store for feeds; `feeds.latest_normalized_json` is only the latest normalized
snapshot used for UI/status convenience.

### `feed_entries`

Stores one logical entry per feed-local identity.

Suggested columns:

- `id`
- `feed_id`
- `entry_key` unique, stable key such as
  `feed-entry:<sha256-feed-key-and-identity>`
- `identity_key`
- `guid`
- `guid_is_permalink`
- `link`
- `normalized_link`
- `title`
- `author`
- `published_at`
- `entry_updated_at` feed-provided entry update timestamp, when present
- `summary_html`
- `summary_text`
- `content_html`
- `content_markdown`
- `content_text`
- `enclosures_json`
- `extensions_json`
- `raw_json`
- `content_hash`
- `version` integer, starting at `1` and incremented on each content-hash
  change
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
- unique `item_id`, deliberately rejecting any bug that would attach multiple
  feed entries to the same materialized item
- `feed_id, last_seen_at`
- `normalized_link`
- `source_id`

### `feed_entry_versions`

Required because feed entry content changes are exactly the sort of raw data
dbrain should preserve for later reprocessing.

Suggested columns:

- `id`
- `feed_entry_id`
- `version`
- `content_hash`
- `raw_json`
- `link`
- `normalized_link`
- `title`
- `author`
- `published_at`
- `entry_updated_at`
- `content_markdown`
- `content_html`
- `content_text`
- `summary_html`
- `summary_text`
- `enclosures_json`
- `extensions_json`
- `observed_at`

The current entry row stores the latest version. Version rows preserve prior
observations. `feed_entries.version` mirrors the latest version number, and each
`feed_entry_versions.version` row stores the previous observed version that was
replaced.

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

Feed subscription `user_tags` should propagate to newly materialized entry
items as user tags. Later changes to feed tags should apply to future entries
and may be offered as an explicit repair command for existing entries, but the
first importer should not silently overwrite user-edited item tags.

### Materialized `sources`

If the feed entry has a canonical link, upsert it through the same normalization
path used by `linkadd`. Then create or retain an `item_source_links` row from
the feed entry item to the canonical source.

Do not treat the feed XML URL as the primary article source. The feed is the
subscription source; the entry link is the evidence source users expect to open,
extract, summarize, and cite.

If an entry's canonical link changes, retain previous `item_source_links` rows
as historical evidence and update `feed_entries.source_id` to the current
canonical source. Do not delete old linked sources merely because the feed entry
mutated. Rendering and UI should prefer `feed_entries.source_id` as the current
canonical source while still being able to show historical linked sources when
useful.

## Entry Identity and Dedupe

Feed entry identity must be feed-local. Different feeds can syndicate the same
article, and dbrain should be able to remember both observations while linking
them to the same canonical `sources` row.

Identity priority:

1. Stable item GUID/ID, if present.
2. Normalized canonical item link, if present.
3. Hash of feed key, title, published date, and deterministic fallback text.

Rules:

- Normalize whitespace and obvious URL variants before hashing.
- Do not include feed position/order in identity or content hash.
- Do not include fetch time in identity or content hash.
- Do not trust GUID blindly across feeds; scope it to `feed_id`.
- If GUID changes but normalized link matches an existing entry in the same
  feed, treat it as the same entry and record the new GUID as observed metadata.
- If link changes but GUID matches, treat it as the same entry and update the
  source link if the new canonical URL is better.
- If GUID matches one existing row and normalized link matches a different row
  in the same feed, GUID wins for the current import. Update the GUID-matched
  row, retain the link-matched row unchanged, record a conflict diagnostic, and
  require an explicit future repair command before merging rows.
- If both GUID and link are missing, use the fallback hash and accept that some
  broken feeds may create duplicates when titles or bodies churn.

Fallback text must be deterministic:

1. Prefer normalized `content_markdown`.
2. Otherwise use normalized `content_text`.
3. Otherwise use normalized `summary_text`.
4. Otherwise use normalized title.
5. Otherwise use normalized enclosure metadata.

For identity hashing, truncate fallback text to the first 2,048 UTF-8 bytes
after whitespace normalization. This keeps broken-feed identity stable enough
without letting huge bodies dominate identity.

Content hash inputs:

- title
- canonical link
- author
- published and entry-updated timestamps when present
- content Markdown
- content HTML/text
- summary HTML/text
- stable enclosure fields: normalized enclosure URL, MIME type, length, and
  title
- whitelisted extension fields that are content-bearing or identity-bearing,
  such as Markdown content, `dc:creator`, and stable media URLs

The extension whitelist should be a named constant in `internal/feedimport` and
is the authoritative definition for hashing. The initial whitelist should
include Markdown content fields, `dc:creator`, `content:encoded`, and stable
media URL fields. Additions to that whitelist are behavior changes and need
tests because they can alter content hashes and reprocessing behavior.

Content hash exclusions:

- feed fetch time
- feed entry position
- transient tracking query parameters after URL normalization
- feed-level title/description/site changes
- parser-generated ordering differences
- volatile extension fields and CDN cache-busting enclosure query parameters

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

Markdown negotiation is opportunistic. Most origins will ignore it. Enabling
`extraction.accept_markdown` can change `sources.content_hash` for normal web
sources and trigger broad re-summary work, so turning it on for an existing
corpus should be treated as an intentional migration. Preserve provenance for
the representation actually fetched instead of silently replacing origin HTML
evidence with Markdown.

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
2. Process selected feeds with a bounded worker pool using
   `feeds.max_concurrent_fetches`.
3. Validate the requested URL before opening a connection.
4. Send `If-None-Match` when `fetch_etag` is known.
5. Send `If-Modified-Since` when `fetch_last_modified` is known.
6. Re-validate every redirect target before following it.
7. On `304 Not Modified`, update `last_checked_at`, clear transient errors, and
   stop.
8. On `200 OK`, read the bounded body, hash the decoded body bytes, parse the
   feed, and persist current response metadata.
9. If the raw body hash matches `fetch_body_hash`, update fetch metadata but
   skip entry materialization unless `--force` was requested.
10. If the raw body hash changed, materialize entries and update feed metadata.
11. On retryable failures, record the error, increment `error_count`, and compute
   a backoff-based `next_fetch_after`.
12. On terminal-looking failures, such as repeated `404` or `410`, apply the
   dead-feed threshold below before marking the feed `dead`.

Manual `feed check <feed>` bypasses `next_fetch_after` and attempts the selected
feed immediately. `feed check --force` also bypasses conditional request
headers and reparses the fetched body even when the decoded body hash matches
the previous fetch.

`feed list --json` should include enough subscription metadata for scripted
backup and migration because OPML import/export is deferred.

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
- Hash decoded response body bytes for `fetch_body_hash`, not wire bytes.
- Store original URL and last resolved URL separately.
- Respect permanent redirects cautiously; do not silently collapse two existing
  feed subscriptions without an explicit merge path.
- Enforce `max_body_bytes` on decompressed bytes, not only transfer bytes.
- Cap redirects at `10`; update `resolved_url` after redirects but do not mutate
  the original submitted `url` or `normalized_url` automatically.
- Use a dbrain User-Agent that makes the client identifiable without leaking
  local paths or secrets.

### URL Safety

Feed and source fetching must fail closed for unsafe URLs.

Before opening a connection, validate that the URL:

- uses `http` or `https`
- has a non-empty host
- resolves to routable public IP addresses
- does not resolve to loopback, link-local, private RFC1918, multicast,
  unspecified, or other special-use addresses

Enforce this in the HTTP dial path against the actual IP address about to be
connected to, not only through a separate preflight DNS lookup. Repeat the same
validation for each redirect target. A redirect from a public URL to
`127.0.0.1`, `localhost`, `169.254.169.254`, a private LAN address, or another
non-public target must be blocked before the request is sent.

Self-hosted LAN feeds are out of scope for the default v1 safety policy. A
future per-feed allowlist can deliberately opt into private address ranges, but
the default must fail closed.

### Retry and Dead Feed Policy

Use deterministic retry behavior:

- initial retry delay: `15m`
- multiplier: `2`
- maximum retry delay: `24h`
- retryable failures: DNS errors, connection timeouts, request timeouts,
  temporary network failures, `429`, and `5xx`
- honor `Retry-After` for `429` and `503` when present
- blocked failures: unsupported scheme, unsafe URL, decompressed body over
  limit, and permanent parse failures for a body that keeps the same hash
- dead failures: `410` and repeated `404` over time

Mark a feed `dead` after `5` consecutive terminal-looking failures across at
least `24h`. Retryable failures should leave the feed in `error`, not `dead`,
unless a later policy explicitly changes that.

`blocked` is terminal local or policy refusal and should not hot-loop. `dead`
means the remote endpoint appears permanently gone. `error` means retryable
transient failure.

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

The first implementation should preserve raw feed responses in `feed_fetches`,
store normalized entry snapshots in `feed_entries.raw_json`, and implement only
the minimal normalization needed for stable identity, content hashing, source
linking, and search.

Even though enclosure downloading is out of scope, preserve enclosure URLs and
metadata as secondary links so podcast, video, or media-heavy feeds do not
materialize as effectively empty entries.

## Sync Integration

Add a feed stage to the `sync all` plan after local app imports and before the
source worker. The feed importer must extract links from feed entry content
inline and queue canonical sources directly; do not depend on a separate
link-extraction stage that does not exist in the current sync plan.

Recommended order:

1. Apple Notes, when enabled.
2. Safari Tabs, when enabled.
3. Feeds, when enabled.
4. X imports/hydration/media stages.
5. GitHub stars.
6. YouTube.
7. Source worker.
8. Categorization.
9. Media archive.

The exact placement can be adjusted, but feeds should run before the source
worker so newly discovered article sources can be extracted in the same sync
run.

Feeds are enabled for `sync all` by default. If no feed subscriptions exist, the
stage should emit a clear no-op message and continue.

Do not add `--feed-item-limit` in v1. Partial materialization is risky because
persisting a new ETag/body hash before all entries are applied can make older
changed entries permanently invisible. If item limiting is added later, fetch
metadata must not advance until the entry batch is fully applied.

Suggested feed sync stats:

- `FeedsChecked`
- `FeedsUnchanged`
- `FeedsNotDue`
- `FeedsChanged`
- `FeedsSkipped`
- `FeedsErrored`
- `FeedsBlocked`
- `FeedsDead`
- `EntriesSeen`
- `EntriesProcessed`
- `EntriesCreated`
- `EntriesUpdated`
- `EntriesUnchanged`
- `EntriesSkipped`
- `SourcesCreated`
- `SourcesExisting`
- `SourcesQueued`
- `NotesRendered`
- `NoteRenderErrors`
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
- published and entry-updated dates
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
Feed entry categories should normally be inherited from the linked canonical
source when one exists. If no linked source exists, categorize from the feed
entry text.

Feed title and feed URL should be denormalized into item text or rendered note
content so they are available through existing item/source search without adding
new FTS columns in v1. Feed health, due counts, blocked/dead counts, and
per-feed entry counts should get their own stats surface; feed-specific MCP
resources can be deferred.

## Implementation Plan

1. Add `github.com/mmcdole/gofeed`.
2. Add feed schema migration.
3. Add `internal/feedimport` with:
   - HTTP fetcher
   - URL safety validation before requests and after redirects
   - bounded concurrent fetch worker pool
   - conditional request support
   - body hashing
   - parser wrapper
   - shared feed autodiscovery helper used by CLI and web paths
   - feed metadata normalization
   - entry identity/content hashing
   - inline feed-entry link discovery and source queueing
   - item/source materialization
4. Add store methods for feed subscriptions, feed fetch metadata, entry upserts,
   and feed stats.
5. Add a feed-entry projection/renderer so notes include feed title/URL, linked
   source keys, discovered entry links, and current canonical source.
6. Add `dbrain feed ...` commands.
7. Add `sync all` feed stage and summary output.
8. Extend `/api/links` to detect feed URLs and return subscription results.
9. Document CLI, config, and sync behavior in README.
10. Add focused fixture-based tests.

Concurrent fetch and parse may happen in workers, but DB apply should be
serialized or lightly bounded. `feed_fetches` rows should be written in their
own earlier transaction so fetch audit evidence survives even if entry
materialization fails. For successful materialization, entry current-row update,
version append, item update, and canonical source/link update must commit as
one transaction after the fetch record exists. Note rendering should happen
after commit; a Markdown write failure should be counted and reported without
rolling back imported evidence.

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
- Raw feed response bodies are preserved for reprocessing, including changed
  body storage, same-body forced reparse, and prior version retention.
- Missing GUID/link fallback identity uses the documented deterministic text
  order and truncation.
- GUID/link conflicts follow the documented deterministic conflict policy.
- GUID changed but link stable updates existing entry.
- Link changed but GUID stable updates existing entry and source link.
- Changed entry links retain historical source links and update the current
  canonical source.
- Entry disappeared from later response is not deleted.
- `304 Not Modified` skips parsing and materialization.
- Changed ETag with identical body hash does not reprocess entries.
- Malformed feed returns a useful error and updates feed diagnostics.
- Oversized body is rejected without storing partial feed content.
- Gzip and charset handling enforce decompressed-size limits and reject gzip
  bombs.
- Unsafe feed/source URLs and redirects to unsafe targets are blocked before a
  request is sent.
- Redirect loops are bounded; permanent redirects update only intended fields.
- Retry backoff and dead-feed thresholds follow the documented policy.
- `Retry-After` is honored for `429` and `503`.
- Concurrent feed checks obey `feeds.max_concurrent_fetches`.
- Manual `feed check` runs when a feed is not due, and `--force` bypasses
  conditionals.
- `feed enable` resets `dead` or `blocked` feeds for another attempt.
- Entry, version, item, and source-link updates commit atomically.
- Note render failures do not roll back imported DB evidence.
- Feed tags propagate to newly materialized entry items without overwriting
  user-edited item tags.
- Feed title and URL surface in rendered notes and search inputs.
- Same-article entries from different feeds remain separate feed-local items but
  link to one shared canonical source row.
- Unsafe URL, oversized body, and unsupported scheme land in `blocked`, not
  `error` or `dead`.
- CLI `feed add`, `feed list`, and `feed check`.
- CLI feed autodiscovery subscribes to one discovered feed and requires explicit
  choice for multiple discovered feeds.
- `/api/links` keeps normal URL behavior and routes feed URLs to subscription.
- `sync all` includes feeds by default.
- `sync all` with no subscriptions prints a no-op message and succeeds.

Do not require network access, local browser profiles, model services, or
personal secrets in these tests.
