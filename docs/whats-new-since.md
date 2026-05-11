# What's New Since X

Status: proposal
Date: 2026-05-11

## Summary

`dbrain` should expose a stable "what changed since this cursor?" surface for
humans, local agents, and remote bots such as Hermes. The first version should
let a caller provide a timestamp cursor and receive a bounded, evidence-grounded
review feed of newly imported or newly enriched local memory.

The useful question is not only "what rows were inserted?" It is:

> What evidence became newly available or newly actionable since the last time
> this reviewer looked?

That includes:

- new items imported from Apple Notes, Safari tabs, X, feeds, GitHub, YouTube,
  and future sources such as Apple Podcasts
- existing items whose raw content changed
- newly created or newly extracted linked sources
- newly completed source summaries
- newly completed item summaries, X media transcripts, and photo OCR
- newly categorized items or sources
- terminal failures or blocked states that need operator attention

The surface should be local-first and read-only. Hermes can review, decide, and
propose actions, but this feature should not make model prose authoritative
memory.

## Goals

- Provide a simple CLI/API/MCP question: "what is new since `<cursor>`?"
- Make the answer stable enough for a bot to page through without missing or
  duplicating events.
- Preserve exact evidence references: item/source keys, note paths, URLs,
  titles, timestamps, summaries, tags, and failure states.
- Support a human-readable digest and a structured JSON form.
- Let callers ask for only import events, only enrichment completions, only
  failures, or all reviewable changes.
- Avoid a parallel queue that can get out of sync with the SQLite source of
  truth.

## Non-Goals

- No upstream mutation.
- No durable "Hermes acted on this" workflow in the first version.
- No automatic deletion or archive decisions.
- No model synthesis stored as evidence.
- No requirement that every importer write a bespoke activity row before this
  can be useful.

## User Model

Examples:

```sh
dbrain whats-new --since 2026-05-11T07:00:00-06:00
dbrain whats-new --since 2026-05-11T13:00:00Z --json
dbrain whats-new --cursor cursor_01HX... --limit 100 --json
dbrain whats-new --since 2h --types imports,enrichments,failures
```

API:

```text
GET /api/whats-new?since=2026-05-11T13:00:00Z&limit=100
GET /api/whats-new?cursor=cursor_01HX...&limit=100
```

MCP/Hermes tool:

```text
dbrain_whats_new(since?: string, cursor?: string, limit?: number, types?: string[])
```

The response should include:

- `cursor`: the requested lower bound
- `next_cursor`: a cursor to use for the next poll
- `high_watermark`: the maximum event timestamp included, mainly for display
- `events`: ordered review events
- `truncated`: whether more events remain
- `counts`: event counts by kind/source/status

## Cursor Semantics

The first implementation can accept timestamps, but internally it should treat a
cursor as:

```text
(event_at, event_kind, entity_kind, entity_id, event_stage)
```

Rationale:

- multiple events can share the same second or millisecond
- SQLite text timestamps are common in the current schema
- bots need deterministic pagination
- a timestamp-only cursor risks skipping events created at the exact boundary
- cursor ordering must use numeric `entity_id`, not a string that embeds an
  integer, because lexicographic string order makes `item:12` sort before
  `item:5`

Ordering:

```text
ORDER BY event_at ASC, event_kind ASC, entity_kind ASC, entity_id ASC, event_stage ASC
```

The next cursor should point to the last returned event, not simply "now".
Using "now" loses events that are written while the request is running.
If the response has zero events, return the input cursor unchanged as
`next_cursor`.

`high_watermark` is intentionally redundant with `next_cursor` when
`truncated=false`. It exists so humans and simple clients can display "covered
through this time" without parsing the cursor. `next_cursor` remains the
authoritative continuation token.

Timestamp input should remain ergonomic:

- RFC3339 absolute timestamps
- `Z`/UTC timestamps
- local offset timestamps
- relative durations such as `2h`, `24h`, `7d`

Output should include both UTC and relative/local display fields where useful,
matching the `tsnet status` polish:

```json
{
  "event_at": "2026-05-11T13:42:01Z",
  "event_at_local": "2026-05-11 07:42:01 MDT",
  "event_age": "18 minutes ago"
}
```

## Event Types

Use a normalized event envelope rather than returning raw table rows.

```json
{
  "event_id": "item:feed-entry:abc123:imported",
  "event_kind": "item_imported",
  "event_at": "2026-05-11T13:42:01Z",
  "entity_kind": "item",
  "entity_id": 9912,
  "entity_key": "feed-entry:abc123",
  "event_stage": "imported",
  "source_type": "feed_entry",
  "title": "Example title",
  "url": "https://example.com/article",
  "note_path": "items/feed/2026/example.md",
  "summary": "...",
  "tags": ["cloud-native", "kubernetes"],
  "status": "ok",
  "actionability": "review",
  "reason": "new item imported and rendered"
}
```

Recommended first-pass event kinds:

- `item_imported`: new local item became visible
- `item_updated`: existing local item content changed; in the derived-query
  implementation this should initially be limited to sources with a reliable
  content-change timestamp such as feed entry `last_changed_at`
- `source_created`: linked source row became visible
- `source_extracted`: source raw extract became available
- `source_summarized`: source summary became available
- `item_summarized`: item-level summary became available
- `x_media_transcribed`: raw X media transcript became available
- `x_media_summarized`: X media transcript summary became available
- `x_photo_ocred`: photo OCR text became available
- `categorized`: item/source tags were applied or changed, only when a
  trustworthy categorization timestamp exists; otherwise tags should be included
  on the import/enrichment event that made them visible
- `blocked`: item/source reached a blocked state requiring a different path
- `failed`: retryable or terminal failure worth surfacing

The event feed should prefer one meaningful event per entity/stage, not every
write. For example, an item imported and rendered in the same transaction should
usually appear as one `item_imported` event with rendered note metadata, not two
separate events.

## Source Of Truth

There are two plausible implementation strategies.

### Option A: Derived Query View First

Build the first version as a read-side union over existing tables:

- `items.imported_at`, `items.updated_at`, `items.last_seen_at`
- `sources.created_at`, `sources.updated_at`, `sources.extracted_at`,
  `sources.summarized_at`
- `item_enrichments.created_at`, `item_enrichments.updated_at`
- feed entry `first_seen_at`, `last_seen_at`, `last_changed_at`
- media asset download/archive timestamps when relevant
- source failure timestamps already used by source activity views

Pros:

- no new write path
- immediately useful over existing DBs
- low risk of making importers more complex
- easy to compare with existing `stats activity`, `stats pipeline`, and web
  source activity behavior

Cons:

- event semantics must be carefully curated from existing stage timestamps
- it can be hard to know whether `updated_at` means "new evidence" or only
  "bookkeeping changed"
- some event kinds may be approximate until write paths become more explicit

This is the right first implementation.

The main cost of Option A is ambiguity. Some existing `updated_at` fields mix
content changes with bookkeeping. The first version should therefore only emit
events from timestamps that clearly mean new evidence or new actionability.
Defer ambiguous event kinds until a dedicated timestamp exists. In particular:

- `item_updated` should start with feed entry `last_changed_at` and any other
  source-specific content-change timestamp that already exists.
- `categorized` should be deferred unless or until item/source categorization
  writes a `categorized_at`-style timestamp, or it should be folded into the
  import/summary event that includes the new tags.
- archive/prune, retry-counter, and note-refresh bookkeeping writes should not
  produce review events.

### Option B: Durable Activity Log

Add a normalized append-only table:

```sql
CREATE TABLE review_events (
  id INTEGER PRIMARY KEY,
  event_key TEXT NOT NULL UNIQUE,
  event_kind TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id INTEGER NOT NULL,
  entity_key TEXT NOT NULL,
  source_type TEXT NOT NULL DEFAULT '',
  event_at TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
```

Importers and enrichers would record semantically meaningful events as part of
their existing transactions.

Pros:

- strongest cursor semantics
- no ambiguity around what changed
- better audit trail for bot workflows
- easier to add durable review/ack state later

Cons:

- every pipeline write path becomes responsible for event hygiene
- backfill/migration needed for old rows
- easy to create noisy duplicate events if not designed carefully

This should be a second step after the derived query proves the product shape.

## Recommended First Implementation

Build a read-only review feed over existing state:

1. Add store query:

   ```go
   ListReviewEvents(ctx, filter ReviewEventFilter) ([]ReviewEvent, ReviewCursor, error)
   ```

2. Add CLI:

   ```text
   dbrain whats-new --since <timestamp|duration> [--cursor TOKEN] [--limit N] [--json]
   ```

3. Add HTTP endpoint:

   ```text
   GET /api/whats-new
   ```

4. Add MCP tool for Hermes:

   ```text
   dbrain_whats_new
   ```

5. Add concise Markdown rendering for humans and structured JSON for bots.

6. Keep the event feed read-only. If Hermes needs to act, use existing proposal
   or operator-review mechanisms rather than writing model conclusions back as
   evidence.

7. Include `failed` and `blocked` events by default. They are often the most
   actionable events for an operator or bot.

8. Have `sync all` print the run's review-feed cursor plus a display
   high-watermark. That gives Hermes a clean bootstrap point after a known
   completed sync.

## Type Filters

The `--types` filter should use coarse groups that map to concrete event kinds.

- `imports`: `item_imported`, `item_updated`, `source_created`
- `enrichments`: `source_extracted`, `source_summarized`,
  `item_summarized`, `x_media_transcribed`, `x_media_summarized`,
  `x_photo_ocred`
- `categorization`: `categorized`
- `failures`: `blocked`, `failed`
- `all`: every event kind

If no `--types` value is provided, default to `all`.

## Query Shape

The store layer should build an event stream from small stage-specific SELECTs
and union them into a normalized shape.

Sketch:

```sql
SELECT
  'item_imported' AS event_kind,
  imported_at AS event_at,
  'imported' AS event_stage,
  id AS entity_id,
  'item' AS entity_kind,
  source_key AS entity_key,
  'item:' || source_key || ':imported' AS event_id,
  source_type,
  title,
  canonical_url,
  note_path,
  user_tags,
  '' AS status,
  '' AS message
FROM items
WHERE imported_at > :since

UNION ALL

SELECT
  'source_summarized' AS event_kind,
  summarized_at AS event_at,
  'summarized' AS event_stage,
  id AS entity_id,
  'source' AS entity_kind,
  source_key AS entity_key,
  'source:' || source_key || ':summarized' AS event_id,
  source_type,
  title,
  canonical_url,
  note_path,
  user_tags,
  summary_status AS status,
  '' AS message
FROM sources
WHERE summary_status = 'ok'
  AND summarized_at > :since

ORDER BY event_at ASC, event_kind ASC, entity_kind ASC, entity_id ASC, event_stage ASC
LIMIT :limit_plus_one;
```

The real implementation should include cursor boundary logic:

```sql
WHERE
  event_at > :cursor_event_at
  OR (
    event_at = :cursor_event_at
    AND (
      event_kind > :cursor_event_kind
      OR (
        event_kind = :cursor_event_kind
        AND (
          entity_kind > :cursor_entity_kind
          OR (
            entity_kind = :cursor_entity_kind
            AND (
              entity_id > :cursor_entity_id
              OR (
                entity_id = :cursor_entity_id
                AND event_stage > :cursor_event_stage
              )
            )
          )
        )
      )
    )
  )
```

SQLite row-value comparisons may be usable, but explicit OR conditions are
clearer and easier to test.

The implementation should add or verify indexes on every timestamp used in the
UNION predicates, including at least:

- `items(imported_at)`
- `items(updated_at)` for any event kind that can safely use it
- `sources(created_at)`
- `sources(extracted_at)`
- `sources(summarized_at)`
- `item_enrichments(updated_at)`
- feed entry `first_seen_at`, `last_seen_at`, and `last_changed_at`

Without indexes, a multi-table review feed over a large DB will become a slow
full scan.

## Review Prioritization

The feed should support a default ordering by time, but Hermes will likely need
priority hints.

Add computed fields:

- `actionability`: `review`, `background`, `blocked`, `failure`
- `importance`: rough integer score, initially deterministic
- `reasons`: short machine-readable reasons

Possible deterministic score inputs:

- source has summary text
- item/source has tags
- item came from high-intent source type such as feed, bookmark, Apple Note,
  podcast saved episode
- media transcript/OCR completed
- failure is terminal or blocked
- source has repeated failures
- source/domain matches configured watchlist later

Do not hide low-score events by default. Use scores for grouping and summaries,
not silent filtering.

## Digest Rendering

Human output should be compact:

```text
What's new since 2026-05-11 07:00 MDT

Imported items: 12
New source summaries: 8
Media transcripts/OCR: 3
Failures/blocked: 2

High-signal review
- [source_summarized] src:abc123 Example title
  https://example.com/article
  tags: kubernetes, service-mesh
  summary: ...

Failures
- [blocked] src:def456 youtube.com context_limit
  Needs chunking before summary.

Next cursor: eyJldmVudF9hdCI6...
```

The human digest may group high-signal events first, but it must not silently
hide low-score events. Lower-priority events should appear in a secondary
section or be counted with an explicit `--details` path.

JSON output should include full event objects and should not rely on parsing the
human digest.

## Hermes Workflow

Hermes should poll with a stored cursor:

1. Read cursor from its own state.
2. Call `dbrain_whats_new(cursor, limit=100)`.
3. Review each event using item/source keys as evidence handles.
4. Ask dbrain for full evidence only when needed (`get`, search, research pack,
   or source detail APIs).
5. Propose actions externally or through an explicit proposal mechanism.
6. Store `next_cursor` only after successful processing.

Important: Hermes should not use prior model summaries of the digest as durable
facts. It should use the returned keys and dbrain evidence fields.

## Edge Cases

### Clock Drift

Use DB-written UTC timestamps as the event source. Do not use caller local time
as a high watermark except to parse the initial query.

### Reprocessing Old Material

If an old source receives a new summary today, it should appear as
`source_summarized` today even if the source was imported months ago. That is
new actionable evidence.

### Changed Existing Items

If a feed entry or Apple Note changes, emit `item_updated`, not another
`item_imported`. Include `previously_seen=true` if useful.

### Noisy Bookkeeping Writes

Avoid surfacing rows that only changed because of archive pruning, retry counter
updates, or note refresh bookkeeping unless the status became actionable.

### Pagination

Fetch `limit + 1` events. If an extra row exists, return only `limit` and set
`truncated=true`.

### Privacy

The endpoint should require the same local/remote trust boundary as existing
remote APIs. It can expose sensitive local-memory summaries and URLs.

## Open Questions

- Should the first UI be CLI/API/MCP only, or should the admin web page show
  the latest review events?
- Should Hermes get a separate "claim/ack" mechanism, or should it own its
  cursor entirely?
- Should watchlists or priority rules live in config, database rows, or Hermes?

## Suggested Milestones

1. Store-only prototype with item/source import and source summary events,
   including cursor encoding/decoding tests.
2. Add timestamp indexes needed by the derived review feed.
3. CLI `dbrain whats-new --since ... --json`.
4. Add item enrichments, source failures, and blocked events.
5. Add `/api/whats-new`.
6. Add MCP tool for Hermes.
7. Add optional durable `review_events` table only if derived queries become
   too ambiguous or too expensive.
