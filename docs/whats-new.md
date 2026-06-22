# What's New Since Cursor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only "what changed since this cursor?" surface over the
local dbrain SQLite store for humans, MCP clients, HTTP clients, and any
external agent.

**Architecture:** Implement v1 as a derived read-side event feed over existing
authoritative tables. Do not add a durable activity log until the derived feed
proves too ambiguous or too expensive. Put the reusable contract in the store
package, then expose the same response shape through CLI, HTTP, and MCP.

**Tech Stack:** Go, SQLite, Cobra CLI, net/http handlers, existing dbrain MCP
server, existing store migration registry, standard `task` verification gates.

---

Status: implemented in branch
Updated: 2026-06-21
Branch context: `whats-new-two` at `5cafaf7`, matching `origin/main` when this
plan was refreshed.
Implementation note: the final branch added an agent-facing `entities` view
after live MCP testing showed raw pipeline events were too noisy for questions
like "what should I pay attention to?" The `events` view remains the raw
chronological pipeline feed.

## Current Branch Context

The original 2026-05-11 proposal is still directionally right, but the branch
has moved enough that the build plan needs sharper boundaries.

- `docs/whats-new.md` is not implemented anywhere yet.
- The repo now has `item_enrichments` as the current-state mirror for item
  summaries, X media transcripts, and OCR. V1 review events should read this
  table instead of only legacy compatibility columns on `items`.
- `SourceActivityFeedFiltered` already implements the right pattern for
  read-side source event unions in `internal/store/source_activity*.go`.
- `stats activity` intentionally reports broad write activity from
  `items.updated_at` and `sources.updated_at`. That is not precise enough for a
  review feed.
- Public chat shares, the research runner, trace persistence, and OKF export
  have shipped since the old proposal. The new feature must keep the same
  evidence boundary: local DB and rendered evidence are authoritative; model
  synthesis, public share summaries, traces, and external agents are not new
  memory.
- The old proposal named one external agent. The refreshed feature should
  target any caller: local scripts, remote MCP clients, other agent runtimes, or
  a human using the CLI.

Confidence: high.

## Product Contract

The feature answers:

> What evidence became newly available, newly changed, or newly actionable
> since this caller last checked?

Initial user-facing examples:

```sh
dbrain whats-new --since 2026-06-21T09:00:00-06:00
dbrain whats-new --since 24h --json
dbrain whats-new --since 2d --view entities --json
dbrain whats-new --cursor eyJldmVudF9hdCI6... --limit 100 --json
dbrain whats-new --since 7d --types imports,enrichments,failures
```

Initial HTTP API:

```text
GET /api/whats-new?since=2026-06-21T15:00:00Z&limit=100
GET /api/whats-new?since=2d&view=entities
GET /api/whats-new?cursor=eyJldmVudF9hdCI6...&limit=100
GET /api/whats-new?since=24h&types=imports,enrichments,failures
```

Initial MCP tool:

```text
dbrain_whats_new(
  since?: string,
  cursor?: string,
  limit?: number,
  types?: string[],
  view?: "events" | "entities"
)
```

All surfaces should return the same logical response:

```json
{
  "view": "events",
  "cursor": {
    "event_at": "2026-06-21T15:00:00Z",
    "event_kind": "",
    "entity_kind": "",
    "entity_id": 0,
    "event_stage": ""
  },
  "next_cursor": "eyJldmVudF9hdCI6...",
  "high_watermark": "2026-06-21T15:42:12Z",
  "truncated": false,
  "counts": [
    {"key": "item_imported", "count": 3},
    {"key": "source_summarized", "count": 8}
  ],
  "events": [
    {
      "event_id": "item:9912:imported:item_imported",
      "event_kind": "item_imported",
      "event_at": "2026-06-21T15:12:03Z",
      "entity_kind": "item",
      "entity_id": 9912,
      "entity_key": "feed-entry:abc123",
      "event_stage": "imported",
      "source_type": "feed_entry",
      "title": "Example title",
      "url": "https://example.com/article",
      "note_path": "items/feed/2026/example.md",
      "summary": "",
      "tags": ["kubernetes"],
      "status": "ok",
      "message": "",
      "actionability": "review",
      "importance": 40,
      "reasons": ["new local item"]
    }
  ]
}
```

`counts` are page-local counts for the returned `events` array. They are not a
total count of every matching event since the cursor.

The `entities` view suppresses raw event rows and returns compact item/source
groups in `entities`. It is the preferred mode for agent review questions such
as "what's new?" or "what should I pay attention to?" Entity summaries are
compact excerpts; callers should fetch details with `dbrain_get_many` or
`dbrain_get` before quoting or relying on exact raw evidence. Pagination and
`limit` remain event-based in both views, so a caller merging multiple
`entities` pages should de-duplicate rows by `entity_key`.

## Non-Goals

- Do not mutate upstream apps or services.
- Do not add an agent-owned acknowledgement, claim, or action workflow in v1.
- Do not treat model prose, public share summaries, traces, or agent digests as
  evidence.
- Do not add a durable `review_events` table in v1.
- Do not add a new daemon, worker, sidecar, or scheduler.
- Do not create an admin web UI in v1. The JSON endpoint is enough for the
  existing web app or external clients to consume later.
- Do not emit generic `updated_at` events unless the timestamp clearly means
  new evidence or actionability.

## Cursor Semantics

The cursor must be deterministic and lossless across pagination. Timestamp-only
pagination is not acceptable because multiple events can share the same second
or millisecond.

All event timestamps used for sorting and cursor comparison must be normalized
to UTC RFC3339 text inside the review-event SQL union. The database contains
TEXT timestamp columns, and historical rows can contain local-offset RFC3339
strings. Raw string comparison across mixed offsets is wrong. The derived feed
must compare normalized values such as `2026-06-21T15:00:00Z`.

Cursor fields:

```go
type ReviewCursor struct {
	EventAt    time.Time `json:"event_at"`
	EventKind  string    `json:"event_kind"`
	EntityKind string    `json:"entity_kind"`
	EntityID   int64     `json:"entity_id"`
	EventStage string    `json:"event_stage"`
}
```

Ordering:

```sql
ORDER BY event_at ASC, event_kind ASC, entity_kind ASC, entity_id ASC, event_stage ASC
```

Boundary predicate:

```sql
WHERE
  event_at > ?
  OR (
    event_at = ?
    AND (
      event_kind > ?
      OR (
        event_kind = ?
        AND (
          entity_kind > ?
          OR (
            entity_kind = ?
            AND (
              entity_id > ?
              OR (
                entity_id = ?
                AND event_stage > ?
              )
            )
          )
        )
      )
    )
  )
```

Implementation requirements:

- Encode cursor tokens as base64url JSON. Do not invent an opaque database row
  id.
- `--since` creates a lower-bound cursor with only `event_at` set.
- Bind cursor timestamps to SQLite as normalized RFC3339 strings, not as
  `time.Time` values.
- A returned `next_cursor` points to the last event actually returned.
- If no events are returned, `next_cursor` must encode the input lower-bound
  cursor. For `--since 24h`, that means the parsed absolute lower-bound cursor,
  not the literal string `24h`.
- `high_watermark` is display-only. It is not a continuation token.
- Fetch `limit + 1`; if the sentinel row exists, set `truncated=true`, drop
  the sentinel, then compute `counts`, `high_watermark`, and `next_cursor` from
  the trimmed page.

## Time Parsing

Accepted `--since` and HTTP/MCP `since` values:

- RFC3339 timestamps such as `2026-06-21T15:00:00Z`
- timestamps with local offsets such as `2026-06-21T09:00:00-06:00`
- relative durations ending in `m`, `h`, or `d`, such as `30m`, `24h`, or `7d`

Do not accept ambiguous date-only strings in v1.

## V1 Event Kinds

Only emit events from timestamps that already mean new evidence or new
operator-visible actionability.

| Group | Event kind | Source of truth | Timestamp | Notes |
| --- | --- | --- | --- | --- |
| imports | `item_imported` | `items` | `imported_at` | Local item became visible. |
| imports | `item_updated` | `feed_entries` joined to `items` | `last_changed_at` | Only for feed entries where `version > 1`; do not use generic `items.updated_at`. |
| imports | `source_created` | `sources` | `created_at` | Linked source row became visible. |
| enrichments | `source_extracted` | `sources` | `extracted_at` | `extract_status in ('ok','empty')`. |
| enrichments | `source_summarized` | `sources` | `summarized_at` | `summary_status = 'ok'`. |
| enrichments | `item_summarized` | `item_enrichments` joined to `items` | `completed_at` | `role='summary'` and `status='ok'`. This includes X media summaries stored as item summaries. |
| enrichments | `x_media_transcribed` | `item_enrichments` joined to `items` | `completed_at` | `role='x_media_transcript'` and `status='ok'`. |
| enrichments | `x_photo_ocred` | `item_enrichments` joined to `items` | `completed_at` | `role='ocr'` and `status='ok'`. |
| failures | `source_failed` | existing source activity failure union | failure timestamp | Include `error`, `dead`, and `gone` source extract states plus source summary `error`. |
| failures | `item_enrichment_failed` | `item_enrichments` joined to `items` | `updated_at` | Roles `summary`, `ocr`, `x_media_transcript` with `status='error'`. |
| failures | `blocked` | `sources` and `item_enrichments` | stage timestamp or `updated_at` | Include source summary `blocked/skipped`, item summary/OCR `blocked/skipped`. |

Do not include these in v1:

- `categorized`: current item/source categorization does not expose a
  trustworthy `categorized_at` timestamp. Include tags on other events.
- media archive/prune events: they are durability/bookkeeping, not reviewable
  evidence.
- public chat share create/update events: shares are derived presentation, not
  corpus evidence.
- OKF export events: OKF is an output bundle. The DB remains the source of
  truth for review events.
- X media transcript non-content terminal states such as `no_audio`, `noise`,
  `too_short`, and `empty` unless a later product decision asks to surface
  terminal no-content outcomes separately. They should not be mixed into
  actionable failures.

## Type Filters

The `types` filter accepts groups, not raw SQL stage names.

| Filter | Expands to |
| --- | --- |
| `imports` | `item_imported`, `item_updated`, `source_created` |
| `enrichments` | `source_extracted`, `source_summarized`, `item_summarized`, `x_media_transcribed`, `x_photo_ocred` |
| `failures` | `source_failed`, `item_enrichment_failed`, `blocked` |
| `all` | all v1 event kinds |

Default: `all`.

Reject unknown filters with a clear error on CLI, HTTP, and MCP.

## Actionability and Importance

Every event should include deterministic hints for agents and humans. These are
sorting/grouping hints only; they must not hide events.

Actionability:

- `review`: new item, changed item, source summary, item summary, transcript, or
  OCR is ready to inspect.
- `background`: source created or extracted but not summarized yet.
- `blocked`: blocked or skipped stage needs a different path or policy.
- `failure`: retryable error or terminal source failure worth attention.

Initial importance scoring:

| Signal | Points |
| --- | ---: |
| `actionability` is `failure` or `blocked` | +50 |
| event has non-empty summary text | +30 |
| event has non-empty OCR/transcript/raw item enrichment text | +25 |
| event has user tags or categories | +20 |
| source type is high-intent (`apple_note`, `safari_tab`, `x_bookmark`, `github_star`, `feed_entry`) | +10 |
| event is only `source_created` | -10 |

Clamp to `0..100`.

## File Structure

Create or modify these files:

- Create `internal/store/review_events_types.go`: DTOs, filter structs, cursor
  structs, constants, type filter normalization.
- Create `internal/store/review_events_cursor.go`: since parsing, cursor token
  encoding/decoding, cursor boundary helpers.
- Create `internal/store/review_events_sql.go`: v1 SQL union bodies.
- Create `internal/store/review_events_queries.go`: WHERE, ORDER BY, limit,
  count, and filter query builders.
- Create `internal/store/review_events_scan.go`: scanners and event decoration.
- Create `internal/store/review_events.go`: public store methods.
- Modify `internal/store/migrations.go`: add migration 11 for review-feed
  timestamp indexes.
- Modify `internal/store/schema.go`, `internal/store/source_schema.go`,
  `internal/store/item_enrichment_schema.go`, and `internal/store/feed_schema.go`
  only as needed to keep fresh DB schema aligned with migration 11.
- Create `internal/store/review_events_test.go`: store-level cursor, filtering,
  event-kind, and pagination tests.
- Create `internal/app/whats_new.go`: Cobra command.
- Create `internal/app/whats_new_output.go`: human output renderer.
- Modify `internal/app/root.go`: register `newWhatsNewCommand`.
- Modify `internal/app/app_test.go`: CLI tests.
- Create `web/whats_new_handlers.go`: HTTP handler and query parsing.
- Modify `web/server.go`: register `/api/whats-new`.
- Modify `web/server_test.go` and `web/auth_test.go`: endpoint behavior and
  auth coverage.
- Create `internal/mcpserver/tools_whats_new.go`: tool implementation.
- Create `internal/mcpserver/tool_schemas_whats_new.go`: MCP output schema.
- Create `internal/mcpserver/tool_format_whats_new.go`: text formatter.
- Modify `internal/mcpserver/tool_definitions.go`: advertise
  `dbrain_whats_new`.
- Modify `internal/mcpserver/tools.go`: dispatch `dbrain_whats_new`.
- Modify `internal/mcpserver/server_test.go`: tools list and tool call tests.
- Modify `MCP.md`, `COMMANDS.md`, `README.md`, `docs/web-route-capabilities.md`,
  `docs/schema-migrations.md`, and `CHANGELOG.md`.

## Task 1: Store Types and Cursor Codec

**Files:**
- Create: `internal/store/review_events_types.go`
- Create: `internal/store/review_events_cursor.go`
- Test: `internal/store/review_events_test.go`

- [ ] **Step 1: Write failing cursor tests**

Add tests covering:

```go
func TestReviewCursorTokenRoundTrip(t *testing.T) {}
func TestParseReviewSinceAcceptsRFC3339OffsetUTCAndDurations(t *testing.T) {}
func TestParseReviewSinceRejectsAmbiguousDateOnly(t *testing.T) {}
func TestNormalizeReviewEventTypesRejectsUnknownValues(t *testing.T) {}
```

Expected failures before implementation:

```text
undefined: ReviewCursor
undefined: ParseReviewSince
undefined: NormalizeReviewEventTypes
```

- [ ] **Step 2: Implement DTOs and constants**

Define the public response shape in `internal/store/review_events_types.go`:

```go
type ReviewEvent struct {
	EventID       string    `json:"event_id"`
	EventKind     string    `json:"event_kind"`
	EventAt       time.Time `json:"event_at"`
	EntityKind    string    `json:"entity_kind"`
	EntityID      int64     `json:"entity_id"`
	EntityKey     string    `json:"entity_key"`
	EventStage    string    `json:"event_stage"`
	SourceType    string    `json:"source_type"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	NotePath      string    `json:"note_path"`
	Summary       string    `json:"summary"`
	Tags          []string  `json:"tags"`
	Status        string    `json:"status"`
	Message       string    `json:"message,omitempty"`
	Actionability string    `json:"actionability"`
	Importance    int       `json:"importance"`
	Reasons       []string  `json:"reasons"`
}

type ReviewCursor struct {
	EventAt    time.Time `json:"event_at"`
	EventKind  string    `json:"event_kind"`
	EntityKind string    `json:"entity_kind"`
	EntityID   int64     `json:"entity_id"`
	EventStage string    `json:"event_stage"`
}

type ReviewEventFilter struct {
	Cursor ReviewCursor
	Limit  int
	Types  []string
}

type ReviewEventPage struct {
	Cursor        ReviewCursor `json:"cursor"`
	NextCursor    string       `json:"next_cursor"`
	HighWatermark time.Time    `json:"high_watermark"`
	Events         []ReviewEvent `json:"events"`
	Truncated      bool         `json:"truncated"`
	Counts         []CountBucket `json:"counts"`
}
```

Use package constants for event kind strings and type groups. Keep the names
stable because MCP clients will depend on them.

- [ ] **Step 3: Implement cursor parsing and encoding**

`internal/store/review_events_cursor.go` should provide:

```go
func ParseReviewSince(value string, now time.Time) (ReviewCursor, error)
func EncodeReviewCursor(cursor ReviewCursor) (string, error)
func DecodeReviewCursor(token string) (ReviewCursor, error)
func NormalizeReviewEventTypes(values []string) ([]string, error)
```

Implementation detail:

- use `encoding/base64.RawURLEncoding`
- use `encoding/json`
- support `d` by converting to `24h` multiples before calling
  `time.ParseDuration`
- return clear errors such as `since must be RFC3339 or a relative duration`

- [ ] **Step 4: Run focused tests**

Run:

```sh
go test ./internal/store -run 'TestReviewCursor|TestParseReviewSince|TestNormalizeReviewEventTypes'
```

Expected: PASS.

## Task 2: Review Event SQL and Store Listing

**Files:**
- Create: `internal/store/review_events_sql.go`
- Create: `internal/store/review_events_queries.go`
- Create: `internal/store/review_events_scan.go`
- Create: `internal/store/review_events.go`
- Test: `internal/store/review_events_test.go`

- [ ] **Step 1: Write failing store tests**

Add tests covering:

```go
func TestListReviewEventsReturnsImportsAndEnrichmentsInCursorOrder(t *testing.T) {}
func TestListReviewEventsPaginatesWithoutSkippingSameTimestampEvents(t *testing.T) {}
func TestListReviewEventsResumesAfterTruncatedPageWithoutOverlapOrGap(t *testing.T) {}
func TestListReviewEventsNormalizesOffsetTimestamps(t *testing.T) {}
func TestListReviewEventsFiltersByTypeGroup(t *testing.T) {}
func TestListReviewEventsDoesNotUseGenericItemUpdatedAt(t *testing.T) {}
func TestListReviewEventsUsesItemEnrichmentCompletedAt(t *testing.T) {}
func TestListReviewEventsSurfacesActionableFailuresAndBlockedRows(t *testing.T) {}
func TestListReviewEventsIgnoresNonActionableXTranscriptTerminalStates(t *testing.T) {}
func TestListReviewEventsReturnsEmptySlices(t *testing.T) {}
func TestReviewEventImportanceDoesNotFilterReturnedEvents(t *testing.T) {}
```

Expected failure before implementation:

```text
st.ListReviewEvents undefined
```

- [ ] **Step 2: Build the normalized SQL union**

`internal/store/review_events_sql.go` should follow the
`source_activity_sql.go` style: string constants for union bodies, with model
status constants interpolated at compile time.

The union rows should normalize to these columns:

```sql
event_kind, event_at, entity_kind, entity_id, entity_key, event_stage,
source_type, title, url, note_path, summary, tags, status, message
```

Each union arm must return `event_at` as normalized UTC text, not raw stored
timestamp text. Use SQLite timestamp normalization in the SELECT list and in
the cursor boundary:

```sql
strftime('%Y-%m-%dT%H:%M:%SZ', raw_event_at) AS event_at
```

Each union arm must also require both:

```sql
raw_event_at != ''
AND strftime('%Y-%m-%dT%H:%M:%SZ', raw_event_at) IS NOT NULL
```

Important query rules:

- `item_imported` reads `items.imported_at`.
- `item_updated` reads `feed_entries.last_changed_at` joined to `items` on
  `feed_entries.item_id`, with `feed_entries.version > 1`. A freshly imported
  feed item emits `item_imported`, not `item_updated`; if a feed item is later
  changed, the changed version may appear as a separate `item_updated` event.
- `source_created` reads `sources.created_at`.
- `source_extracted` reads `sources.extracted_at` when extract status is `ok`
  or `empty`.
- `source_summarized` reads `sources.summarized_at` when summary status is
  `ok`.
- Item enrichment events read `item_enrichments.completed_at` for successful
  rows and `item_enrichments.updated_at` for failed/blocked rows.
- Source failure events can reuse the same source status logic as
  `sourceActivityFailureUnionQuery`, but the output event kind is normalized to
  `source_failed`.

- [ ] **Step 3: Build filter and pagination query helpers**

`internal/store/review_events_queries.go` should provide:

```go
func reviewEventsQuery(filter ReviewEventFilter) (string, []any)
func reviewEventCounts(events []ReviewEvent) []CountBucket
func reviewCursorWhere(cursor ReviewCursor) (string, []any)
func reviewCursorEventAtText(cursor ReviewCursor) string
```

Use `limit + 1` in the public store method rather than hiding that behavior in
the SQL helper.

`reviewCursorWhere` must bind normalized RFC3339 strings such as
`cursor.EventAt.UTC().Format(time.RFC3339)`. Do not append `cursor.EventAt`
directly to the SQL args.

- [ ] **Step 4: Implement scanners and decoration**

`internal/store/review_events_scan.go` should:

- scan tags from comma-separated `items.categories`, `items.user_tags`, or
  `sources.user_tags`
- trim whitespace and drop empty tags
- compute `EventID` as
  `<entity_kind>:<entity_id>:<event_stage>:<event_kind>`
- compute `Actionability`, `Importance`, and `Reasons`
- return empty slices, not nil, for `Tags`, `Reasons`, `Events`, and `Counts`

- [ ] **Step 5: Implement public store method**

`internal/store/review_events.go` should expose:

```go
func (s *Store) ListReviewEvents(ctx context.Context, filter ReviewEventFilter) (ReviewEventPage, error)
```

Behavior:

- default limit: 50
- max limit: 500
- default type filter: `all`
- decode/validate type groups before querying
- trim the `limit + 1` sentinel before computing page counts or `next_cursor`
- set `NextCursor` to the last returned event
- if no events are returned, set `NextCursor` to the encoded input lower-bound
  cursor
- set `HighWatermark` to the last event timestamp when events exist

- [ ] **Step 6: Run focused store tests**

Run:

```sh
go test ./internal/store -run 'TestListReviewEvents'
```

Expected: PASS.

## Task 3: Migration 11 for Review Feed Indexes

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/schema.go`
- Modify: `internal/store/source_schema.go`
- Modify: `internal/store/item_enrichment_schema.go`
- Modify: `internal/store/feed_schema.go`
- Modify: `docs/schema-migrations.md`
- Test: `internal/store/migrations_test.go`

- [ ] **Step 1: Inspect migration history before editing**

Run:

```sh
git branch --all --contains HEAD
rg -n "currentSchemaVersion|Version:|schema_migrations|review" internal/store docs
```

Expected current local maximum on this branch: migration 10,
`feed_parse_error_retry_repair`.

- [ ] **Step 2: Write failing migration test**

Add:

```go
func TestMigrationAddsReviewEventIndexesToExistingDB(t *testing.T) {}
```

The test should create an old database with `schema_migrations` rows through
version 10, open it with the new store, and assert indexes exist via:

```sql
PRAGMA index_list(items);
PRAGMA index_list(sources);
PRAGMA index_list(item_enrichments);
PRAGMA index_list(feed_entries);
```

Expected failure before migration 11: indexes are missing.

- [ ] **Step 3: Add migration 11**

Add migration:

```go
{
	Version: 11,
	Name:    "review_event_indexes",
	Run: func(s *Store) error {
		return s.ensureReviewEventIndexes()
	},
}
```

Add `ensureReviewEventIndexes` in the store package. Required indexes:

```sql
CREATE INDEX IF NOT EXISTS idx_items_imported_at ON items(imported_at);
CREATE INDEX IF NOT EXISTS idx_sources_created_at ON sources(created_at);
CREATE INDEX IF NOT EXISTS idx_sources_extracted_at ON sources(extracted_at);
CREATE INDEX IF NOT EXISTS idx_sources_summarized_at ON sources(summarized_at);
CREATE INDEX IF NOT EXISTS idx_sources_extract_last_failed_at ON sources(extract_last_failed_at);
CREATE INDEX IF NOT EXISTS idx_item_enrichments_completed_at ON item_enrichments(completed_at);
CREATE INDEX IF NOT EXISTS idx_item_enrichments_updated_at ON item_enrichments(updated_at);
CREATE INDEX IF NOT EXISTS idx_feed_entries_last_changed_at ON feed_entries(last_changed_at);
```

Do not add `items(updated_at)` for review feed semantics.

- [ ] **Step 4: Keep fresh DB schema aligned**

Add the same `CREATE INDEX IF NOT EXISTS` statements to the relevant
`ensure*Tables` path so new databases and upgraded databases converge.

- [ ] **Step 5: Update migration docs**

Update `docs/schema-migrations.md` in its existing style so it no longer
implies migration 3 is current. At minimum, document migration 11
`review_event_indexes`, the current maximum schema version, and the reason the
indexes are needed for deterministic review-feed pagination.

- [ ] **Step 6: Run migration tests**

Run:

```sh
go test ./internal/store -run 'TestMigration|TestReviewEventIndexes'
```

Expected: PASS.

## Task 4: CLI Command

**Files:**
- Create: `internal/app/whats_new.go`
- Create: `internal/app/whats_new_output.go`
- Modify: `internal/app/root.go`
- Modify: `internal/app/app_test.go`
- Modify: `COMMANDS.md`

- [ ] **Step 1: Write failing CLI tests**

Add tests covering:

```go
func TestRootCommandHelpIncludesWhatsNew(t *testing.T) {}
func TestWhatsNewCommandOutputsJSON(t *testing.T) {}
func TestWhatsNewCommandOutputsHumanDigest(t *testing.T) {}
func TestWhatsNewCommandRejectsCursorAndSinceTogether(t *testing.T) {}
func TestWhatsNewCommandRejectsUnknownTypeFilter(t *testing.T) {}
```

Expected failure before implementation: root help does not include
`whats-new`.

- [ ] **Step 2: Implement Cobra command**

Add root-level command:

```text
dbrain whats-new --since <timestamp|duration> [--cursor TOKEN] [--limit N] [--types imports,enrichments,failures] [--json]
```

Validation:

- exactly one of `--since` or `--cursor` is required
- `--limit` clamps to `1..500`
- `--types` uses `cmd.Flags().StringSliceVar`, so it supports
  comma-separated values and repeated flags
- load config through `loadConfig(root.root, root.configFile)`
- open the store with `store.Open`, not `OpenReadOnly`, so migrations/indexes
  apply for local CLI use

- [ ] **Step 3: Implement human output**

Human output should be compact and machine copyable:

```text
What's new since 2026-06-21T15:00:00Z

Counts
- item_imported: 3
- source_summarized: 8

Review
- [source_summarized] src:abc123 Example title
  https://example.com/article
  tags: kubernetes, service-mesh
  summary: ...

Failures and blocked
- [source_failed] src:def456 example.com connectivity
  Unable to connect...

Next cursor: eyJldmVudF9hdCI6...
High watermark: 2026-06-21T15:42:12Z
Truncated: false
```

Rules:

- show all returned events, not only high-importance events
- group `failure` and `blocked` separately for scanability
- truncate summary/message display to a readable length, but leave full content
  in JSON

- [ ] **Step 4: Register command**

Modify `internal/app/root.go`:

```go
rootCmd.AddCommand(
	...
	newWhatsNewCommand(opts),
	...
)
```

- [ ] **Step 5: Run CLI tests**

Run:

```sh
go test ./internal/app -run 'Test.*WhatsNew|TestRootCommandHelpIncludesCoreCommands'
```

Expected: PASS.

## Task 5: HTTP API

**Files:**
- Create: `web/whats_new_handlers.go`
- Modify: `web/server.go`
- Modify: `web/server_test.go`
- Modify: `web/auth_test.go`
- Modify: `docs/web-route-capabilities.md`

- [ ] **Step 1: Write failing HTTP tests**

Add tests covering:

```go
func TestWhatsNewEndpointReturnsReviewEvents(t *testing.T) {}
func TestWhatsNewEndpointRejectsInvalidSince(t *testing.T) {}
func TestWhatsNewEndpointRejectsCursorAndSinceTogether(t *testing.T) {}
func TestWhatsNewEndpointRequiresAuthWhenWebAuthEnabled(t *testing.T) {}
```

- [ ] **Step 2: Implement handler**

Register:

```go
appMux.HandleFunc("/api/whats-new", s.handleWhatsNew)
```

Handler requirements:

- `GET` only
- same `since`/`cursor` exclusivity as CLI
- `limit` default 50, max 500
- `types` supports comma-separated group names
- errors:
  - invalid/missing cursor or since: HTTP 400
  - store failure: HTTP 500
- response body: `store.ReviewEventPage`

- [ ] **Step 3: Update route capabilities**

Add `/api/whats-new` to `docs/web-route-capabilities.md` as read-only DB
access.

- [ ] **Step 4: Run web tests**

Run:

```sh
go test ./web -run 'TestWhatsNew|TestWebAuth|TestServer'
```

Expected: PASS.

## Task 6: MCP Tool for Any Agent

**Files:**
- Create: `internal/mcpserver/tools_whats_new.go`
- Create: `internal/mcpserver/tool_schemas_whats_new.go`
- Create: `internal/mcpserver/tool_format_whats_new.go`
- Modify: `internal/mcpserver/tool_definitions.go`
- Modify: `internal/mcpserver/tools.go`
- Modify: `internal/mcpserver/resource_definitions.go`
- Modify: `internal/mcpserver/server_test.go`
- Modify: `MCP.md`

- [ ] **Step 1: Write failing MCP tests**

Add tests covering:

```go
func TestServerToolsListIncludesWhatsNew(t *testing.T) {}
func TestServerWhatsNewToolReturnsStructuredReviewEvents(t *testing.T) {}
func TestServerWhatsNewToolRejectsInvalidTypeFilter(t *testing.T) {}
```

Expected failure before implementation: tool not advertised.

- [ ] **Step 2: Add tool definition**

Tool name:

```text
dbrain_whats_new
```

Description:

```text
Read newly imported, newly enriched, blocked, or failed local brain evidence
since a cursor or timestamp. Read-only; returns evidence handles for agents.
```

Input schema fields:

- `since`: string
- `cursor`: string
- `limit`: integer, default 50
- `types`: array of strings

Annotations:

```go
map[string]bool{"readOnlyHint": true, "idempotentHint": true}
```

- [ ] **Step 3: Add output schema**

The output schema must match `store.ReviewEventPage` and explicitly mark arrays
as arrays, not nullable objects. This matters because previous MCP stats bugs
came from empty slices serializing as `null`.

- [ ] **Step 4: Add tool implementation and formatter**

Implementation should:

- decode arguments
- enforce same cursor/since validation as CLI and HTTP
- call `s.st.ListReviewEvents`
- return `toolOKResult(formatReviewEventPage(page), page)`

Formatter should include counts, the first several events, truncation status,
and next cursor. Do not make agents parse the text; structured output is the
contract.

- [ ] **Step 5: Update MCP docs and resource guidance**

Replace caller-specific wording with generic agent wording:

1. agent stores its own cursor
2. agent calls `dbrain_whats_new`
3. agent inspects returned source keys with `dbrain_get`, `dbrain_get_many`,
   search, or `dbrain_research_pack`
4. agent stores `next_cursor` only after it has successfully processed the page

In `internal/mcpserver/resource_definitions.go`, update the agent guidance
resource so it lists `dbrain_whats_new` as the polling primitive for newly
available local evidence and keeps `dbrain_stats_activity` positioned as a
coarser operational-health view.

- [ ] **Step 6: Run MCP tests**

Run:

```sh
go test ./internal/mcpserver -run 'TestServer.*WhatsNew|TestServerInitializeAndToolsList'
```

Expected: PASS.

## Task 7: Documentation, Changelog, and Command Reference

**Files:**
- Modify: `README.md`
- Modify: `COMMANDS.md`
- Modify: `MCP.md`
- Modify: `docs/web-route-capabilities.md`
- Modify: `docs/schema-migrations.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Update README command index and agent guidance**

Document:

- `dbrain whats-new`
- cursor behavior
- type filters
- that the feature is read-only and local-first
- that agents should store their own cursor

- [ ] **Step 2: Update COMMANDS.md**

Add a command reference section with examples:

```sh
dbrain whats-new --since 24h
dbrain whats-new --since 2026-06-21T15:00:00Z --json
dbrain whats-new --cursor "$CURSOR" --limit 100 --types imports,enrichments
```

- [ ] **Step 3: Update MCP.md**

Describe `dbrain_whats_new` as the agent polling primitive. Avoid wording that
ties the workflow to one agent runtime.

- [ ] **Step 4: Update CHANGELOG.md**

Add a concise entry under Recent Improvements:

```markdown
### What's New Review Feed (2026-06-21)

- **CLI/API/MCP**: Added a read-only `whats-new` review feed for newly imported,
  enriched, blocked, or failed local evidence with deterministic cursor
  pagination.
- **Store**: Added review-feed timestamp indexes and tests for cursor ordering,
  type filters, item enrichment events, and failure/blocked semantics.
- **Location**: `internal/store/`, `internal/app/`, `internal/mcpserver/`,
  `web/`, `README.md`, `MCP.md`, `COMMANDS.md`
```

- [ ] **Step 5: Run documentation grep**

Run:

```sh
rg -n "whats-new|dbrain_whats_new|review feed|schema migration" docs README.md COMMANDS.md MCP.md CHANGELOG.md
```

Expected:

- `dbrain_whats_new` should appear in MCP docs and tool tests.

## Task 8: Full Verification

**Files:** no new files.

- [ ] **Step 1: Run standard gates**

Run:

```sh
task fmt
task lint
task test-ci
```

Expected: all pass.

- [ ] **Step 2: Rebuild**

Because this changes CLI behavior, run:

```sh
task build
```

Expected: binary builds successfully.

- [ ] **Step 3: Spot-check CLI help and JSON**

Run against a temp root:

```sh
./bin/dbrain --root "$(mktemp -d)" whats-new --since 24h --json
./bin/dbrain whats-new --help
```

Expected:

- empty DB returns an empty `events` array and an empty `counts` array, not
  `null`
- help includes `--since`, `--cursor`, `--limit`, `--types`, and `--json`

## Deferred Follow-Ups

These are real ideas, but they should not block v1.

- Durable `review_events` table with importer/enricher writes.
- Agent claim/ack state stored in dbrain.
- Admin web UI panel for the feed.
- Watchlists or priority rules in config.
- Categorization events after item/source categorization writes trustworthy
  `categorized_at` timestamps.
- Sync summary printing a review cursor after `sync all`. This is useful, but
  it should be added only after `ListReviewEvents` behavior is stable enough to
  avoid presenting a display watermark as an authoritative continuation token.

## Acceptance Criteria

- `dbrain whats-new` exists and is documented.
- `/api/whats-new` returns the same structured page shape as the CLI JSON.
- `dbrain_whats_new` is advertised through MCP and returns structured content.
- Cursor pagination does not skip or duplicate same-timestamp events.
- V1 events do not use generic `items.updated_at` as evidence of item change.
- Item summary/OCR/transcript events come from `item_enrichments`.
- Empty arrays serialize as `[]`, not `null`.
- Unknown type filters fail clearly.
- The feature is read-only across CLI, HTTP, and MCP.
- The docs use generic agent wording, not wording tied to one external agent.
- `task fmt`, `task lint`, `task test-ci`, and `task build` pass.
