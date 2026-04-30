# Apple Notes Memory Provider Proposal

Status: proposal
Date: 2026-04-30

## Summary

Apple Notes is a strong next memory target for `dbrain` because it is often the
highest-signal capture surface on a Mac: quick thoughts, personal research,
meeting notes, drafts, copied excerpts, task fragments, and links all land there
before they become structured enough for a vault.

The best architecture is not necessarily "import every Apple Note into the
`dbrain` item store". The more general product goal is to retrieve from all of
the user's brains: the dbrain SQLite/vault corpus, Apple Notes, and future local
apps or stores. Apple Notes should therefore be modeled as a local memory
provider, even if the first shippable implementation materializes selected
notes into dbrain items to reuse the existing FTS, MCP, web, link extraction,
summary, and categorization pipeline.

The recommended implementation path is:

1. Prove read-only direct SQLite access and note body decoding.
2. Ship an opt-in materialized importer for allowlisted folders/accounts.
3. Add provider-index/live retrieval later if we want Apple Notes searchable
   through dbrain without copying note bodies into the main item store.

The implementation should keep the integration private-by-default:

- Do not query or materialize anything until the user explicitly opts in.
- Prefer folder allowlists over whole-account imports.
- Skip password-protected notes by default.
- Skip shared notes by default until the user explicitly enables them.
- Support note-level `DO NOT index` markers such as `#dbrain-ignore`.
- If a previously cached or materialized note later becomes excluded, purge
  indexed content and derived outputs while retaining only a minimal tombstone
  when needed.

This is a different data class from X bookmarks, GitHub stars, YouTube videos,
or linked web sources. Apple Notes are user-authored working memory. Privacy and
revocation need to be first-class product behavior, not an afterthought.

The steady-state design should not be "export Apple Notes to files, then import
those files". Exporters are useful references and emergency migration tools, but
the product path should query the current local Notes state directly and, only
when configured, store selected note data in `dbrain`.

## Validation

The local Notes app exposes a scriptable interface at:

`/System/Applications/Notes.app/Contents/Resources/Notes.sdef`

That file is readable directly from the app bundle. Installing Xcode or the
Command Line Tools is still useful because `sdef`/`sdp` can inspect the
dictionary and generate ScriptingBridge headers, but the raw dictionary is
available without waiting on those tools.

That scripting dictionary exposes enough local metadata for a first provider or
materializing importer:

- accounts
- folders
- notes
- note `id`
- note `name`
- note `body` as HTML
- note `plaintext`
- note creation date
- note modification date
- note password-protected flag
- note shared flag
- attachments with names, IDs, content identifiers, URL attachments, dates, and
  a save command

Directly reading the Notes group container from a normal terminal session is not
available without additional permissions. On this machine, enumerating
`~/Library/Group Containers/group.com.apple.notes` failed with
`Operation not permitted`, which means the direct database path is TCC-gated and
requires Full Disk Access for the terminal, IDE, or helper binary.

There are two viable live-ingestion families:

- Apple Events via the Notes scripting dictionary. This can be reached through
  `osascript`, JXA, `NSAppleScript`, or a generated ScriptingBridge-style
  helper. It requires Automation permission to control Notes, but does not
  require Full Disk Access and uses the app's public scriptable surface.
- Direct Notes database access. This avoids AppleScript/Apple Events, can be
  faster and more incremental, and can expose data not surfaced through the
  scripting dictionary. It requires Full Disk Access and depends on Apple's
  private Notes database schema and compressed note payload representation.

The LifeOS/Second Brain framing also fits this direction: Apple Notes is mostly
the Capture layer; `dbrain` can add durable organization, distillation, search,
topic mapping, and MCP retrieval over that captured material.

Reference: https://lifecontext.vip/guide/intro/second-brain

## Goals

- Import selected Apple Notes as materialized `apple_note` items first, while
  preserving a provider seam for future live/provider-indexed retrieval.
- Let `dbrain ask`, MCP search/research tools, and the web UI retrieve
  materialized Apple Notes alongside DB-backed dbrain items and sources.
- Avoid storing any Apple Notes content until the user explicitly opts into a
  scoped import.
- Offer opt-in materialization into local SQLite/rendered Markdown for users who
  want durable indexing, topic mapping, backlinks, categorization, or offline
  search over notes.
- Keep raw note text available for local reprocessing only for notes the user
  has explicitly materialized.
- Preserve enough metadata to detect updates without reprocessing every note.
- Extract links from materialized notes so URLs in Apple Notes can become normal
  linked source rows.
- Provide an explicit, reliable `DO NOT index` mechanism.
- Make dry-run output useful before any private content is stored.
- Keep the first implementation local-only and macOS-specific without affecting
  non-macOS users.

## Non-Goals

- Do not sync through iCloud web APIs.
- Do not require exported Markdown/HTML files as the steady-state integration.
- Do not import Apple Notes content unless the user explicitly opts in. The v1
  implementation may use opt-in materialization for selected folders/accounts,
  but it should not assume the whole Notes library belongs in dbrain.
- Do not make direct private Notes SQLite/CloudKit access the only supported
  path until its schema risk and permission UX are validated.
- Do not require Full Disk Access unless the user explicitly selects the
  `direct_db` adapter.
- Do not upload Apple Notes content to hosted models by default.
- Do not ingest attachment file contents by default.
- Do not auto-delete materialized local records just because a note disappears
  from Apple Notes, except when the user explicitly requests a privacy purge
  policy.
- Do not try to mirror Apple Notes folder/tag behavior exactly.

## Integration Options

| Option | Feasibility | Recommendation | Notes |
| --- | --- | --- | --- |
| Direct Notes SQLite/CloudKit store | Medium to high risk | Recommended v1 spike | Best non-AppleScript path and best fit for batch materialization. Requires Full Disk Access and tracks private schema details such as `NoteStore.sqlite`, compressed note data, deletion markers, and attachment records. |
| Live Apple Events adapter via `osascript` or JXA | High | Fallback adapter | Local, supported by the app scripting dictionary, enough metadata for live search/get and incremental materialization. Requires Automation permission for the terminal/binary to control Notes. |
| Swift helper using `NSAppleScript` or ScriptingBridge | High | Possible fallback helper | Keeps the Notes bridge small and typed enough to inspect. Xcode command line tools can generate references from `sdef`; still uses Apple Events under the hood. |
| Go helper invoking Apple Events scripts | High | Acceptable fallback | Keeps orchestration in Go while isolating macOS scripting. Easier than a native bridge, but string escaping and AppleScript error handling need care. |
| External Apple Notes MCP server | Medium | Reference/prototype | Useful proof that Notes can be exposed as a live tool surface. dbrain should prefer an internal provider interface over depending on a second MCP server for core retrieval. |
| Shortcuts CLI | Medium | Not default | Could work if users maintain a Shortcut, but too indirect and hard to test as the primary importer. |
| Spotlight/metadata search | Low | Avoid as source of truth | Useful for discovery/debugging, not reliable for full-fidelity note ingestion. |
| Third-party exporters | Variable | Migration/reference only | Do not make exported files the product path. Use them to learn schema behavior, test edge cases, or migrate one-time archives. |

## Existing Project Survey

No obvious reusable Go-native Apple Notes library surfaced. A Swift/C exporter
does exist, but it is export-oriented and GPL-licensed, so it is reference
material rather than a runtime dependency. The useful projects otherwise fall
into two groups: Apple Events bridges and direct database readers. Both are
portable as ideas, but neither should be copied wholesale without checking
license and maintenance risk.

| Project | Approach | Language | Takeaway |
| --- | --- | --- | --- |
| [`antoniorodr/memo`](https://github.com/antoniorodr/memo) | AppleScript-backed CLI for Notes and Reminders | Python | Good reference for terminal UX and Markdown-ish note display. It depends on AppleScript, so it does not solve the "avoid AppleScript" path. Apache-2.0. |
| [`angelespejo/apple-notes-cli`](https://github.com/angelespejo/apple-notes-cli) | Bash wrapper around `osascript` | Shell | Useful as a minimal CRUD/permission reference only. It is GPL-3.0 and not an ingestion-quality data path. |
| [`more-io/claude-apple-bridges`](https://github.com/more-io/claude-apple-bridges) | Swift CLI using `NSAppleScript` | Swift | Good model for a small compiled bridge binary. MIT licensed, but still Apple Events/AppleScript-family access. |
| [`cardmagic/notes`](https://github.com/cardmagic/notes) | Reads `~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite` and builds its own index | TypeScript | Best reference for a non-AppleScript live adapter. It requires Full Disk Access and private-schema handling. It also demonstrates incremental indexing and PDF attachment extraction. |
| [`kzaremski/apple-notes-exporter`](https://github.com/kzaremski/apple-notes-exporter) | Direct DB exporter to HTML/Markdown/JSONL/Text | Swift/C | Useful because it handles the Notes database, but it is export-oriented and GPL-3.0. Treat as a reference, not vendored code. |
| [`threeplanetssoftware/apple_cloud_notes_parser`](https://github.com/threeplanetssoftware/apple_cloud_notes_parser) | Direct database/parser tooling | Ruby | Mature forensic-style parser reference for schema behavior, protobuf decoding, version drift, attachments, and encrypted notes. Better as validation material than as a runtime dependency. MIT licensed. |
| [`ydkhatri/mac_apt`](https://github.com/ydkhatri/mac_apt) | macOS/iOS forensic parser suite | Python | Useful validation reference for Apple Notes protobuf/body parsing and damaged-data edge cases. Too broad to be a runtime dependency. Apache-2.0. |
| [`dogsheep/apple-notes-to-sqlite`](https://github.com/dogsheep/apple-notes-to-sqlite) | Direct DB to SQLite | Python | Good historical reference for Dogsheep-style import shape, but it is archived/read-only and still export/index oriented. |
| [`sirmews/apple-notes-mcp`](https://github.com/sirmews/apple-notes-mcp) | MCP server for Apple Notes | Python | Useful for agent-facing command semantics. dbrain should not depend on a second MCP server for core retrieval, but the tool shape is relevant. |
| [`RafalWilinski/mcp-apple-notes`](https://github.com/RafalWilinski/mcp-apple-notes) | MCP server using JXA, LanceDB, local MiniLM embeddings, and full-text search | TypeScript/Bun | Strong proof of the federated-memory pattern. It is not file-export based, but it does maintain its own local index under `~/.mcp-apple-notes`. Good reference for MCP tool shape and local embeddings. |

## Existing MCP Implementation Findings

The two Apple Notes MCP servers answer different parts of the problem.

### `RafalWilinski/mcp-apple-notes`

Relevant source: [`README.md`](https://github.com/RafalWilinski/mcp-apple-notes/blob/main/README.md),
[`index.ts`](https://github.com/RafalWilinski/mcp-apple-notes/blob/main/index.ts)

This project is closest to the `provider_index` model:

- Accesses Apple Notes through JXA via `run-jxa`.
- Enumerates note titles from `Application('Notes').notes()`.
- Reads note detail by title with `app.notes.whose({name: title})[0]`.
- Converts Notes HTML body with `turndown`.
- Stores indexed rows in LanceDB under `~/.mcp-apple-notes/data`.
- Uses a local `Xenova/all-MiniLM-L6-v2` feature-extraction model for vectors.
- Creates a LanceDB full-text index on `content`.
- Exposes MCP tools for `index-notes`, `search-notes`, `get-note`,
  `list-notes`, and `create-note`.
- Combines vector and full-text search results with reciprocal-rank-style
  scoring.

Useful lessons:

- Provider-local indexing is a real path. Apple Notes do not have to become
  dbrain items before retrieval works.
- The MCP-facing shape is small: index, search, get, and optionally create.
- Local embeddings are possible, but they add runtime/dependency weight that
  dbrain does not need for a first implementation because dbrain already has
  SQLite FTS.

Gaps to avoid in dbrain:

- Title-based identity is not safe. Duplicate note titles and renamed notes
  will break `get-note` and incremental indexing.
- The index path appears to append chunks rather than doing a robust upsert by
  stable note ID/content hash.
- It does not implement folder/account allowlists, shared/locked-note defaults,
  ignore markers, or privacy purge.
- It does not provide a changed-note reindex strategy.
- Its README still lists chunking, Markdown conversion, custom embeddings, and
  DB purge/control as todos.

### `sirmews/apple-notes-mcp`

Relevant source: [`README.md`](https://github.com/sirmews/apple-notes-mcp/blob/main/README.md),
[`notes_database.py`](https://github.com/sirmews/apple-notes-mcp/blob/main/src/apple_notes_mcp/notes_database.py),
[`server.py`](https://github.com/sirmews/apple-notes-mcp/blob/main/src/apple_notes_mcp/server.py),
[`notestore.proto`](https://github.com/sirmews/apple-notes-mcp/blob/main/src/apple_notes_mcp/proto/notestore.proto)

This project is closest to the `direct_db` model:

- Reads `~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite`.
- Requires Full Disk Access because the database is under the Notes group
  container.
- Queries `ZICCLOUDSYNCINGOBJECT`, `ZICNOTEDATA`, and `Z_METADATA`.
- Builds note IDs like `x-coredata://<metadata uuid>/ICNote/p<pk>`.
- Converts Apple absolute timestamps to Unix time by adding `978307200`.
- Filters rows where note/folder `ZMARKEDFORDELETION != 1`.
- Tracks metadata such as title, folder, account, snippet, locked, pinned, and
  checklist flags.
- Includes a `read-note` body decode path for `ZICNOTEDATA.ZDATA` using
  gzip/zlib decompression plus protobuf parsing.
- Its list/search metadata path still relies on `ZSNIPPET` and SQL `LIKE`
  against raw `ZDATA`, so it is not a working full-text indexing model.
- The protobuf schema is copied from prior Apple Notes parser/liberator work and
  exposes note text, formatting runs, links, checklist details, and attachment
  markers.
- Exposes MCP resources plus tools for `get-all-notes`, `search-notes`, and
  `read-note`.

Useful lessons:

- Direct SQLite is feasible and is the strongest non-AppleScript path.
- The read-note body decode is portable to Go: read blob, gzip/zlib
  decompress, parse protobuf, take `document.note.note_text`, optionally
  inspect `attribute_run` for links/checklists/attachments.
- Stable IDs should use database/cloud identifiers rather than titles.
- Direct DB makes batch indexing and changed-note reindexing much easier than
  JXA because metadata can be queried in bulk.

Gaps to avoid in dbrain:

- The README explicitly lists missing handling for encrypted notes, pinned-note
  filtering, cloud sync status, attachment content retrieval, checklist status,
  and write support.
- SQL `LIKE` against `ZICNOTEDATA.ZDATA` is not a good content search strategy
  because the body is compressed/protobuf. dbrain should decode first and index
  normalized plaintext into its own FTS or future provider index.
- It does not implement provider-local indexing, content hashes, upsert,
  privacy markers, or materialization into a durable corpus.
- Direct schema ownership becomes dbrain's responsibility; Apple can change
  private Notes tables/columns across macOS releases.

The strongest non-export direction is therefore an adapter interface with two
read adapters:

- `apple_events`: lower permission blast radius, uses Notes'
  scriptable interface.
- `direct_db`: recommended v1 spike, non-AppleScript, likely higher fidelity
  and faster incremental scans, requires Full Disk Access and private schema
  ownership.

Export-oriented tools should stay outside the steady-state sync path. If they
are used at all, they should be one-shot migration or diagnostic helpers.

Given the desire to avoid operating on exports and avoid AppleScript if
possible, the next implementation decision should be a focused `direct_db`
feasibility spike. If it can reliably enumerate folders/accounts, decode note
body text, identify locked/shared/deleted notes, and detect changes on the
current macOS version, then `direct_db` can be the first real importer adapter.
If that spike is brittle, an Apple Events adapter remains the safer baseline
while the direct parser matures.

## Dbrain Implementation Shape

The right reimplementation is not "run one of these MCP servers next to
dbrain". The right shape is to absorb the useful pieces into dbrain, but the v1
implementation should be materialization-first.

That is a pragmatic choice, not a rejection of provider architecture:

- Materialized `apple_note` items immediately reuse existing dbrain behavior:
  SQLite FTS, MCP search, `ask`, rendered Markdown, source link extraction,
  summaries, categorization, topics, and web UI detail pages.
- A separate `provider_index` requires new tables, search merge logic, MCP
  provenance plumbing, web provider detail views, and privacy-purge behavior
  before any user-visible Notes retrieval works.
- The privacy risk is real, but the right v1 mitigation is explicit opt-in,
  folder/account allowlists, dry-run, local-only defaults, and purge-on-ignore.
  Users who enable Apple Notes materialization are already choosing to copy
  selected note content into dbrain.

Keep the internal seams clean enough that a future provider index can exist,
but do not build the full provider abstraction before the first importer.

Suggested package shape:

```go
package applenotes

type Adapter interface {
    Probe(context.Context) (ProbeResult, error)
    List(context.Context, Scope) ([]NoteMeta, error)
    Read(context.Context, NoteID) (NoteDocument, error)
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error)
```

The first adapter should be:

- `direct_db`: read Apple's local Notes database in read-only mode, decode
  bodies, and materialize selected notes. This avoids AppleScript and is best
  for batch and incremental scans, but it requires Full Disk Access and private
  schema ownership.

The fallback adapter should be:

- `apple_events`: use the Notes scripting dictionary through JXA/`osascript` or
  a small Swift helper if direct DB access is unavailable or breaks on a future
  macOS release.

Write operations such as creating or editing Notes should stay out of scope for
v1. Retrieval and indexing are enough, and write access increases permission and
safety risk.

### Direct SQLite Shape

The direct DB adapter should open the source database read-only. If a live
read-only connection is blocked by SQLite locking or WAL behavior, it may copy
`NoteStore.sqlite` plus its WAL/SHM sidecars to a temporary dbrain-owned
snapshot and read the snapshot. It should never write to Apple's database.

Known core path:

- Database: `~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite`
- Main table: `ZICCLOUDSYNCINGOBJECT`
- Body table: `ZICNOTEDATA`
- Metadata table: `Z_METADATA`
- Stable note UUID: `ZICCLOUDSYNCINGOBJECT.ZIDENTIFIER`
- Note title: `ZTITLE1`
- Folder title: `ZTITLE2`
- Account name: `ZNAME`
- Deletion marker: `ZMARKEDFORDELETION`
- Password flag: `ZISPASSWORDPROTECTED`
- Body blob: `ZICNOTEDATA.ZDATA`

The adapter should not hard-code every column name blindly. It should probe
`PRAGMA table_info` at startup and choose the best available account and date
columns. Existing reverse-engineered readers show account joins drifting across
columns such as `ZACCOUNT2`, `ZACCOUNT3`, `ZACCOUNT4`, and later variants.
Creation/modification date columns also vary, so queries should use probed
columns or `COALESCE` over known variants.

Known account-column observations include `ZACCOUNT4` in existing direct-DB
readers and `ZACCOUNT7` on current Sequoia-era schemas. Treat those as probes,
not fixed truth.

Representative metadata query shape:

```sql
SELECT
  note.Z_PK AS pk,
  note.ZIDENTIFIER AS external_id,
  note.ZTITLE1 AS title,
  folder.ZTITLE2 AS folder,
  acc.ZNAME AS account,
  note.ZSNIPPET AS snippet,
  note.ZCREATIONDATE1 AS created_at_mac,
  note.ZMODIFICATIONDATE1 AS modified_at_mac,
  note.ZISPASSWORDPROTECTED AS password_protected,
  note.ZMARKEDFORDELETION AS note_deleted,
  folder.ZMARKEDFORDELETION AS folder_deleted,
  notedata.ZDATA AS body_blob
FROM ZICCLOUDSYNCINGOBJECT AS note
JOIN ZICCLOUDSYNCINGOBJECT AS folder
  ON note.ZFOLDER = folder.Z_PK
LEFT JOIN ZICCLOUDSYNCINGOBJECT AS acc
  ON note.<probed_account_column> = acc.Z_PK
LEFT JOIN ZICNOTEDATA AS notedata
  ON note.ZNOTEDATA = notedata.Z_PK
WHERE note.ZTITLE1 IS NOT NULL
  AND COALESCE(note.ZMARKEDFORDELETION, 0) != 1
  AND COALESCE(folder.ZMARKEDFORDELETION, 0) != 1;
```

Use `ZIDENTIFIER` as the materialized `external_id` when present. Avoid title
or local integer primary key as source identity; titles are mutable and local
integer IDs may be device-specific.

Apple Notes timestamps are Mac Absolute Time. Add `978307200` seconds to
convert to Unix epoch.

### Body Decode Pipeline

The body decode path is:

```text
ZICNOTEDATA.ZDATA -> gzip/zlib decompress -> protobuf parse -> note_text
```

The `threeplanetssoftware/apple_cloud_notes_parser` proto defines the core
plaintext path:

```text
NoteStoreProto.document.note.note_text
```

That schema also exposes formatting runs, links, checklist metadata, and
attachment markers. V1 should extract plaintext first. Formatting, tables,
checklist state, and attachment content can be later phases.

Do not search or index `ZICNOTEDATA.ZDATA` directly. It is compressed/protobuf
data. Decode it first, normalize plaintext, then write the selected content to
dbrain's own item fields and FTS.

### Batch Materialization

The batch command should support a dry-run first:

```sh
dbrain providers apple-notes probe
dbrain import apple-notes --folder Research --dry-run
dbrain import apple-notes --folder Research --apply
```

Materialized notes should become normal items:

- `source_type`: `apple_note`
- `source_key`: `apple-note:<ZIDENTIFIER>`
- `external_id`: `ZIDENTIFIER`
- `title`: `ZTITLE1`
- `text`: decoded plaintext
- `folder_names`: account/folder path
- `published_at`: creation date when available
- `updated_at`: modification date
- `content_hash`: hash over decoded plaintext plus relevant metadata
- `raw_json`: adapter metadata, DB path fingerprint, folder/account IDs, flags,
  parser version, and optional HTML/formatting metadata if later supported

Once these rows exist, existing dbrain FTS, MCP, web UI, link extraction,
categorization, summaries, and topics should work with normal source-type
plumbing.

### Incremental Re-indexing

There is no public Apple Notes change feed suitable for this use case, so
incremental indexing should be polling-based.

For `direct_db`:

- Track the Notes database mtime as a cheap "anything changed?" guard.
- Track a high-water mark from Apple Notes modification timestamps.
- Query changed candidate notes by modification timestamp, then decode and hash
  body content before upserting.
- Still perform a lightweight full metadata scan each run or periodically,
  because deletions, folder moves, shared/locked status changes, and exclusion
  changes are not safely captured by a simple `modified_at > high_water` query.
- Store parser/schema version in sync state so parser fixes can force a full
  re-import.
- Keep a manual `--force`/`repair` path that clears Apple Notes import state and
  rebuilds selected notes from the source database.

For `apple_events`:

- Enumerate the configured accounts/folders.
- Compare stable note IDs, modification dates, and content hashes.
- Fetch bodies only for new or modified notes when possible.
- Treat title as display metadata only; it is not stable identity.

If a note gains `#dbrain-ignore`, enters an excluded folder, becomes locked, or
is otherwise excluded, privacy rules should purge materialized item content,
derived summaries, rendered Markdown, and FTS rows. This source type should
prefer privacy revocation over the usual "preserve raw data" rule.

### Go Feasibility

The required pieces are practical in Go, but the direct database parser would be
owned by dbrain.

- SQLite access is already in the repo through `modernc.org/sqlite`, and dbrain
  already uses SQLite FTS5 for item/source search.
- gzip/zlib decompression is available in the Go standard library.
- protobuf decoding is available through `google.golang.org/protobuf`; direct
  DB support can use generated Go from `notestore.proto` or a deliberately
  minimal `protowire` decoder for plaintext. Generated code is more
  maintainable once we care about formatting, links, checklists, or
  attachments.
- Apple Events can be reached without a third-party Go package by invoking
  `/usr/bin/osascript -l JavaScript` from Go. A Swift helper using
  `NSAppleScript` or ScriptingBridge is also viable if stronger typing or
  better permission UX is needed.
- There is no need for LanceDB or local transformer embeddings in v1. SQLite FTS
  gives a much smaller dependency surface.

There is no obvious reusable Go library to vendor. Existing direct-DB exporter
references are in other languages, including the Swift/C
`kzaremski/apple-notes-exporter`, and should stay references rather than
dependencies.

The main implementation risk is not library availability. It is the private
Notes schema: table names, column names, compressed payload layout, timestamp
fields, and attachment relationships are not a stable public API.

### Web Interface Exposure

Materialized notes are the easiest web path. Once `apple_note` items exist, the
existing web interface can expose them through normal item/search/detail paths
after the renderer and filters know the new source type.

A future `provider_index` mode would need provider search/detail routes:

- search results would include `provider=apple_notes`, `materialized=false`,
  and an opaque provider document ID;
- opening a result would call provider `Get` or read the provider index;
- the UI would visibly distinguish provider-indexed notes from materialized
  dbrain items.

That is useful later, but it should not block v1.

## Recommended Architecture

`dbrain` should grow a memory provider layer rather than treating every source
as a thing that must be copied into the item store.

Provider capabilities:

- `Search`: return ranked evidence for a query without necessarily storing raw
  content in dbrain.
- `Get`: retrieve a specific memory by provider ID when the user or a retrieval
  result asks for detail.
- `Probe`: report permissions, scope, counts, and health.
- `Sync` or `Materialize`: optional durable import into dbrain's DB/vault for
  users who want local indexing, backlinks, categorization, and offline search.

For Apple Notes, that means:

- The first user-visible integration should be an opt-in materialized importer
  for selected folders/accounts.
- Materialized Apple Notes evidence should be marked with
  `source_type=apple_note` and clear account/folder provenance.
- A later provider-index/live retrieval mode can return
  `provider=apple_notes`, `materialized=false` evidence if we want searchable
  Notes without copying bodies into the main item store.
- The same allowlist/exclusion/ignore-marker policy must apply to
  materialization and any future provider mode.

This preserves the important product goal: answers can use all available memory
surfaces without forcing every surface into the same storage model.

## Provider Indexing Modes

There are three distinct persistence levels. The implementation should make
these explicit because they carry different privacy and retrieval tradeoffs.

| Mode | Stores Apple Notes content in dbrain items? | Stores provider-local index/cache? | Use case |
| --- | --- | --- | --- |
| `live_only` | No | No, except short-lived process memory | Maximum privacy and simplest semantics. Slower and weaker recall because each query must scan or ask the adapter directly. |
| `provider_index` | No | Yes, in a scoped local cache | Good retrieval without turning notes into dbrain corpus items. The index can hold embeddings, note IDs, modification dates, titles, snippets, or full normalized text depending on the configured privacy level. |
| `materialized` | Yes | Optional | Full dbrain behavior: FTS, rendered Markdown, backlinks, categorization, topics, and offline search. Highest privacy blast radius, so it must remain opt-in. |

`mcp-apple-notes` is closest to `provider_index`: it keeps an Apple Notes RAG
index under its own data directory, then serves search results through MCP. A
dbrain-native version can use the same idea without requiring exported files or
promoting Apple Notes into normal item rows.

Recommended implementation order:

- Start with dry-run/probe without storing content.
- Ship `materialized` for explicitly selected folders/accounts because it
  reuses existing dbrain infrastructure and is the shortest path to value.
- Add `provider_index` later only if we need Apple Notes retrieval without
  copying note bodies into the main item store.
- Keep `live_only` as a diagnostic or fallback mode, not the main v1 product
  path.

If `provider_index` stores full note text or snippets, the same privacy purge
rules as materialized notes must apply. If it stores embeddings only, still
clear them when a note is excluded because embeddings are derived from private
content.

## MCP Shape

The dbrain MCP server should remain the single agent-facing entry point.
Instead of requiring users to connect both `dbrain` and an Apple Notes MCP
server to every client, dbrain can fan out internally and return unified
evidence.

For materialized v1, the existing MCP surface should mostly work after search
and item rendering know `source_type=apple_note`:

- `dbrain_search` can return materialized Apple Notes as normal item hits.
- `dbrain_research` can include them with clear provenance.
- `dbrain_get` can read their rendered item content.
- backlog/stats can report Apple Notes import state if the integration is
  enabled.

Future provider-mode MCP additions:

- `dbrain_search` accepts `source_types: ["apple_note"]` and/or
  `providers: ["apple_notes"]`.
- `dbrain_research` includes live provider hits alongside DB-backed item/source
  hits, with clear provenance.
- `dbrain_get` can read a live provider result by opaque provider ID during the
  same session or by re-querying the provider.
- `dbrain_stats_backlog` or a new health tool can report provider permission
  state without exposing content.

The agent-facing response should eventually distinguish three evidence classes:

- DB-backed dbrain item/source evidence.
- Materialized Apple Notes evidence that now exists as normal dbrain items.
- Live/provider-indexed Apple Notes evidence that was not stored.

An external MCP server such as `mcp-apple-notes` is useful as a prototype, but
making dbrain an MCP client of another local MCP server adds process management,
tool schema mapping, error handling, and provenance complexity. A direct
internal provider interface is simpler for production. If needed, an MCP-client
provider can be added later for arbitrary third-party memory servers.

## Proposed CLI

```sh
dbrain providers apple-notes probe
dbrain import apple-notes --dry-run
dbrain import apple-notes --folder "dbrain" --apply
dbrain import apple-notes --folder "Projects" --folder "Research" --apply
dbrain import apple-notes --account "iCloud" --folder "Research" --apply
dbrain import apple-notes --adapter direct_db --folder "Research" --dry-run
dbrain import apple-notes --adapter apple_events --folder "Research" --apply
dbrain import apple-notes --all --dry-run
dbrain import apple-notes --all --apply
dbrain ask "What do my notes and saved sources say about X?"
```

Suggested first flags:

- `--include-provider apple_notes`
  Future provider-mode flag for querying Apple Notes without materialization.
  Not required for v1 materialized notes because they are normal dbrain items.
- `--dry-run`
  Print counts, accounts/folders matched, skipped counts, and sample titles
  without storing note bodies.
- `--adapter`
  Select the Notes adapter. Suggested values: `auto`, `apple_events`, and
  `direct_db`. Default `auto` should prefer configured policy, use `direct_db`
  for imports when Full Disk Access is available, and fall back to
  `apple_events` if the direct parser is unavailable or broken.
- `--apply`
  Required for content import unless `sync all` is running with the integration
  enabled in config.
- `--account`
  Include only selected accounts.
- `--folder`
  Include only selected folders. Repeatable. This should be the recommended
  default path.
- `--exclude-folder`
  Exclude folders by exact path or glob.
- `--all`
  Explicitly import all visible notes, still respecting exclusions and note
  markers.
- `--include-shared`
  Include shared notes. Default false.
- `--include-locked`
  Attempt password-protected notes. Default false, and likely ineffective unless
  Notes has them unlocked.
- `--include-attachments`
  Save attachment metadata and URL attachments. Default false for attachment
  file contents.
- `--forget-excluded`
  Purge content for existing imported notes that are now excluded or marked
  `DO NOT index`.
- `--json`
  Machine-readable stats.

`sync all` should not import Apple Notes by default. The integration should be
enabled with config. The v1 config should make materialization explicit:

```yaml
apple_notes:
  enabled: true
  adapter: direct_db
  materialize: true
  live_retrieval: false
  index_mode: materialized
  include_accounts:
    - iCloud
  include_folders:
    - Research
    - Projects
  exclude_folders:
    - Private
    - Passwords
    - Health
    - Finance
  ignore_markers:
    - "#dbrain-ignore"
    - "#noindex"
  include_shared: false
  include_locked: false
  include_attachments: false
  forget_excluded: false
```

If `adapter: direct_db` is enabled, startup checks should make the tradeoff
explicit:

```text
Apple Notes direct_db reads Apple's private Notes database.
It requires Full Disk Access for this terminal or dbrain binary and may break
after macOS Notes schema changes. Run --dry-run first.
```

## Data Model

The data model below applies only if Apple Notes are materialized into dbrain.
Live retrieval can return the same fields as evidence without storing them.

Use a new item source type:

`source_type = apple_note`

Treat each Apple Note as a primary item, not as a linked source. It is captured
user-authored material, similar in importance to an imported bookmark item.
Links inside the note can then enter the existing link extraction pipeline and
create normal `sources` rows.

Suggested item mapping:

- `source_key`: `apple-note:<external-id-or-hash>`. For `direct_db`, prefer
  `apple-note:<ZIDENTIFIER>` when available.
- `external_id`: adapter-specific stable note ID. For `apple_events`, this is
  the Notes scripting `id`; for `direct_db`, use the best stable database/cloud
  identifier available rather than a title/path.
- `canonical_url`: a local pseudo URL such as
  `apple-notes://<external-id-or-hash>`, or a real Notes deep link if one is
  available from the adapter
- `title`: note `name`
- `text`: note `plaintext`
- `raw_json`: JSON envelope containing account, folder path, note ID, flags,
  HTML body when available, attachment metadata, adapter name, and importer
  version
- `published_at`: creation date
- `updated_at`: modification date from Apple Notes
- `last_seen_at`: import time
- `folder_names`: account/folder path
- `content_hash`: hash over plaintext, HTML body, selected metadata, and
  attachment metadata
- `note_path`: `items/apple-notes/<year>/<slug-or-hash>.md`
- `user_tags`: normal dbrain tags, not Apple Notes tags in v1

The existing item summary, categorization, FTS, search, MCP, web UI, topic, and
entity paths should work once the importer populates common item fields.

## Privacy Model

Privacy behavior should be stricter than other importers.

### Default Scope

The first Apple Notes access should require either `--folder`, `--account`, or
`--all`. Without one of those, it should print a help message and do nothing.

Recommended UX:

```text
Apple Notes import is opt-in because notes may contain private material.
Choose one or more folders with --folder, or pass --all explicitly.
Run with --dry-run first to inspect counts without storing note content.
```

### Exclusions

Supported exclusion layers:

- Configured excluded accounts.
- Configured excluded folders.
- Password-protected notes by default.
- Shared notes by default.
- Note-level plaintext markers.

Recommended note-level markers:

- `#dbrain-ignore`
- `#noindex`

Apple Notes already treats hashtags as visible note content, and the scripting
surface exposes plaintext, so this does not require private database access.

### Previously Indexed Notes

If a note was previously materialized and later becomes excluded or gains an
ignore marker, privacy should override the normal "preserve raw data" rule.

The materialization or provider-index purge path should:

- Clear `text`.
- Clear raw HTML/body content from `raw_json`.
- Clear item summaries.
- Clear OCR/transcript-derived fields if attachments are later supported.
- Remove the rendered note content or replace it with a privacy tombstone.
- Remove it from FTS.
- Keep only minimal identity metadata needed to avoid reimporting it, such as
  `source_key`, `external_id`, `source_type`, `last_seen_at`, and an import
  status/reason.

If the current schema cannot express this cleanly, add an explicit item-level
privacy/import status rather than overloading normal summary or extract status.

### Dry Run

Dry run should avoid printing note bodies. It can print:

- matched accounts
- matched folders
- counts by folder
- counts skipped by exclusion reason
- a small sample of titles, unless `--private-dry-run` or similar is added

## Materialization And Incremental Sync

Materialization should be polling-based. Apple Notes does not expose a stable
public change feed suitable for `dbrain`.

1. Enumerate configured accounts/folders.
2. Read note IDs, titles, folder paths, modification dates, flags, and plaintext.
3. Compute a content hash.
4. For materialization, upsert changed notes as dbrain items and update
   `last_seen_at` for visible notes.
5. For a future provider index, update the provider-local index/cache instead
   of item rows.

This is acceptable for `sync all --watch` because Notes libraries are usually
small enough for periodic enumeration, and the first implementation can expose
limits and progress logs if needed.

Adapter-specific optimization:

- `apple_events` should enumerate the configured scope and compare modification
  dates/content hashes.
- `direct_db` can use the Notes database modification time, note modification
  timestamps, deletion markers, and a stored high-water mark, but it must still
  be able to force a full rescan after schema changes or parser fixes.

Deletion handling for materialized notes should follow dbrain's append-only
default:

- A missing note should not be deleted automatically.
- It can be marked as not seen in the latest run later if useful.
- A separate `repair` or `forget` command should handle explicit cleanup.
- Privacy-driven exclusions are the exception: `--forget-excluded` should purge
  content.

## Attachment Handling

Attachments are useful but should not be part of the v1 content path.

V1 should store optional attachment metadata only:

- attachment ID
- name
- content identifier
- URL for URL attachments
- creation/modification dates
- shared flag

Future phases can add attachment extraction through the selected adapter, then
route images through OCR and PDFs through text extraction. That should be
explicitly opt-in because attachments may include scans, IDs, health records, or
other sensitive data.

## Link Extraction

Materialized Apple Notes should participate in normal item link discovery:

- URLs in plaintext should become source candidates.
- URL attachments should become source candidates when attachment metadata is
  enabled.
- The Apple Note remains the backlink context for linked source notes.

This makes Apple Notes a bridge from personal capture to the existing source
enrichment pipeline.

## MCP And Search Semantics

Apple Notes should be easy to include or exclude in retrieval:

- `source_type=apple_note` should work in MCP, resources, prompts, search, web
  filters, stats, and topic tools when Apple Notes are materialized.
- `provider=apple_notes` should work consistently if a future provider-indexed
  mode is added.
- Research packs should surface that Apple Notes are user-authored notes, not
  third-party sources.
- Answers should avoid treating personal notes as authoritative external facts.
  They are evidence of what the user wrote or captured.

The summary prompt for Apple Notes should likely differ from web sources:

- Preserve personal framing.
- Avoid inventing certainty.
- Distinguish tasks, ideas, diary-like material, meeting notes, and copied
  external excerpts.
- Extract action items only when explicitly present.

## Implementation Phases

1. **Probe and dry run**
   Add a macOS-only direct DB probe that opens the Notes database read-only,
   detects Full Disk Access failures, probes schema columns, and reports
   account/folder/note counts without decoding or storing note bodies.

2. **Body decoder**
   Implement a standalone `ZDATA []byte -> plaintext` decoder using gzip/zlib
   plus protobuf parsing. Unit test it with captured non-private fixtures before
   wiring it to the importer.

3. **Materialized importer**
   Add `dbrain import apple-notes` with folder/account allowlists, ignore
   markers, shared/locked-note defaults, item upsert, note rendering, FTS, and
   JSON stats. This is the first useful product milestone.

4. **Incremental re-import**
   Track source DB mtime, high-water modification timestamps, content hashes,
   parser version, and last-seen state. Re-runs should skip unchanged notes,
   update changed notes, and support `--force` after parser fixes.

5. **Privacy purge**
   Add tombstone/purge behavior for notes that become excluded after
   materialization. Include tests proving raw text, summaries, rendered notes,
   and FTS content are removed.

6. **Sync integration**
   Add opt-in `sync all` integration behind config and `--skip-apple-notes`.
   Apple Notes remains disabled unless explicitly configured.

7. **MCP/search/web polish**
   Ensure `source_type=apple_note` works in search filters, MCP output,
   rendered notes, web UI filters/detail pages, and topic/search resources.

8. **Categorization and summaries**
   Add an Apple Notes-specific summary/categorization prompt path if generic
   item summarization is too source-agnostic. Prefer local models by default
   unless the user explicitly allows hosted processing for notes.

9. **Attachment metadata**
   Import attachment metadata and URL attachments. Defer file extraction/OCR/PDF
   processing to a later explicit opt-in phase.

10. **Future provider index**
   Add live/provider-indexed retrieval only if users need Apple Notes search
   without copying note bodies into the main dbrain item store.

## Testing Plan

- Unit test parsing of direct DB adapter records if the direct adapter is built.
- Unit test body decoding from representative `ZDATA` blobs.
- Unit test schema column probing for account/date column drift.
- Unit test read-only/open failure diagnostics for missing Full Disk Access.
- Unit test parsing of Apple Events adapter JSON output if that fallback adapter
  is built.
- Unit test include/exclude folder matching.
- Unit test ignore marker detection in plaintext and HTML-derived text.
- Unit test source key stability.
- Unit test content hash stability and change detection.
- Unit test purge/tombstone behavior for newly excluded notes.
- Store tests proving materialized `apple_note` items enter search/FTS and MCP
  retrieval.
- CLI tests for dry-run/apply safety behavior.
- Manual macOS integration test with a small test Notes folder.
- Manual direct DB permission test that proves missing Full Disk Access fails
  with a clear diagnostic, if that adapter is built.

CI should not depend on the real Notes app or a real user note database.

## Acceptance Criteria

- `dbrain import apple-notes --dry-run --folder Test` reports matched/skipped
  counts without storing note bodies.
- `dbrain import apple-notes --folder Test --apply` imports visible notes from
  that folder as `apple_note` items.
- Re-running the importer reports unchanged notes without rewriting them.
- Editing a note updates the item and rendered Markdown on the next import.
- Adding `#dbrain-ignore` to a previously materialized note purges indexed
  content when `--forget-excluded` is enabled.
- Password-protected and shared notes are skipped by default.
- Materialized Apple Notes appear in `search`, MCP tools,
  topic/search resources, and the web UI with `source_type=apple_note`.
- URLs inside materialized notes enter the normal link/source enrichment
  pipeline.

## Open Questions

- Should v1 require folder allowlists, or is `--all --dry-run` enough friction?
- Should the default ignore marker be only `#dbrain-ignore`, or should `#noindex`
  also be recognized by default?
- Should deleted materialized Apple Notes be retained forever like other
  imports, or should this provider have a stronger cleanup affordance because
  notes are personal?
- Should shared notes be excluded permanently unless explicitly included, even
  if they are in an allowed folder?
- Should raw HTML body be stored in `raw_json`, or should v1 store only
  plaintext to reduce privacy blast radius?
- Should Apple Notes summaries be local-model-only by default, regardless of the
  global summary provider?
- Should `direct_db` open the live DB read-only, or always copy a DB/WAL/SHM
  snapshot into dbrain temp state before reading?
- Should dbrain support MCP-client providers for third-party local memory MCP
  servers, or keep Apple Notes as a direct internal importer/provider first?
- Should future provider-indexed Apple Notes retrieval be embeddings-only,
  snippets, or full plaintext?
- Should an Apple Events fallback ship in v1 or wait until direct DB breaks for
  someone?
- Is Full Disk Access acceptable for a clearly marked v1 direct DB adapter?
- How much of the private Notes database parser should be owned in Go versus a
  small Swift helper or external helper process?
- Should we support a one-time migration path from existing exporters, or avoid
  export-shaped workflows entirely until live sync is working?
