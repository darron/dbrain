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
apps or stores. Apple Notes should therefore start as a live local memory
provider that can be queried through dbrain's MCP/retrieval path, with optional
materialization into the dbrain DB only when the user opts in.

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

- Expose Apple Notes as a local memory provider behind dbrain retrieval and MCP.
- Let `dbrain ask`, MCP search/research tools, and the web UI retrieve Apple
  Notes alongside DB-backed dbrain items and sources.
- Avoid storing Apple Notes content in the dbrain DB by default.
- Offer opt-in materialization into local SQLite/rendered Markdown for users who
  want durable indexing, topic mapping, backlinks, categorization, or offline
  search over notes.
- Keep raw note text available for local reprocessing only when the user
  explicitly opts into materialized Apple Notes storage; otherwise treat note
  content as query-time evidence.
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
- Do not require Apple Notes content to be copied into the dbrain DB for basic
  retrieval.
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
| Live Apple Events adapter via `osascript` or JXA | High | Good baseline | Local, supported by the app scripting dictionary, enough metadata for live search/get and incremental materialization. Requires Automation permission for the terminal/binary to control Notes. |
| Swift helper using `NSAppleScript` or ScriptingBridge | High | Strong candidate | Keeps the Notes bridge small and typed enough to inspect. Xcode command line tools can generate references from `sdef`; still uses Apple Events under the hood. |
| Go helper invoking Apple Events scripts | High | Acceptable first cut | Keeps orchestration in Go while isolating macOS scripting. Easier than a native bridge for v1, but string escaping and AppleScript error handling need care. |
| Direct Notes SQLite/CloudKit store | Medium to high risk | Experimental adapter | Best non-AppleScript live path. Requires Full Disk Access and tracks private schema details such as `NoteStore.sqlite`, compressed note data, deletion markers, and attachment records. |
| External Apple Notes MCP server | Medium | Reference/prototype | Useful proof that Notes can be exposed as a live tool surface. dbrain should prefer an internal provider interface over depending on a second MCP server for core retrieval. |
| Shortcuts CLI | Medium | Not default | Could work if users maintain a Shortcut, but too indirect and hard to test as the primary importer. |
| Spotlight/metadata search | Low | Avoid as source of truth | Useful for discovery/debugging, not reliable for full-fidelity note ingestion. |
| Third-party exporters | Variable | Migration/reference only | Do not make exported files the product path. Use them to learn schema behavior, test edge cases, or migrate one-time archives. |

## Existing Project Survey

No obvious Go-native Apple Notes library surfaced. The useful projects fall into
two groups: Apple Events bridges and direct database readers. Both are portable
as ideas, but neither should be copied wholesale without checking license and
maintenance risk.

| Project | Approach | Language | Takeaway |
| --- | --- | --- | --- |
| [`antoniorodr/memo`](https://github.com/antoniorodr/memo) | AppleScript-backed CLI for Notes and Reminders | Python | Good reference for terminal UX and Markdown-ish note display. It depends on AppleScript, so it does not solve the "avoid AppleScript" path. Apache-2.0. |
| [`angelespejo/apple-notes-cli`](https://github.com/angelespejo/apple-notes-cli) | Bash wrapper around `osascript` | Shell | Useful as a minimal CRUD/permission reference only. It is GPL-3.0 and not an ingestion-quality data path. |
| [`more-io/claude-apple-bridges`](https://github.com/more-io/claude-apple-bridges) | Swift CLI using `NSAppleScript` | Swift | Good model for a small compiled bridge binary. MIT licensed, but still Apple Events/AppleScript-family access. |
| [`cardmagic/notes`](https://github.com/cardmagic/notes) | Reads `~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite` and builds its own index | TypeScript | Best reference for a non-AppleScript live adapter. It requires Full Disk Access and private-schema handling. It also demonstrates incremental indexing and PDF attachment extraction. |
| [`kzaremski/apple-notes-exporter`](https://github.com/kzaremski/apple-notes-exporter) | Direct DB exporter to HTML/Markdown/JSONL/Text | Go | Useful because it is Go and handles the Notes database, but it is export-oriented and GPL-3.0. Treat as a reference, not vendored code. |
| [`threeplanetssoftware/apple_cloud_notes_parser`](https://github.com/threeplanetssoftware/apple_cloud_notes_parser) | Direct database/parser tooling | Python | Mature forensic-style parser reference for schema behavior and attachments. Better as validation material than as a runtime dependency. |
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
- Decodes `ZICNOTEDATA.ZDATA` by gzip/zlib decompression plus protobuf parsing.
- The protobuf schema is copied from prior Apple Notes parser/liberator work and
  exposes note text, formatting runs, links, checklist details, and attachment
  markers.
- Exposes MCP resources plus tools for `get-all-notes`, `search-notes`, and
  `read-note`.

Useful lessons:

- Direct SQLite is feasible and is the strongest non-AppleScript path.
- The core body decode is portable to Go: read blob, gzip/zlib decompress,
  parse protobuf, take `document.note.note_text`, optionally inspect
  `attribute_run` for links/checklists/attachments.
- Stable IDs should use database/cloud identifiers rather than titles.
- Direct DB makes batch indexing and changed-note reindexing much easier than
  JXA because metadata can be queried in bulk.

Gaps to avoid in dbrain:

- The README explicitly lists missing handling for encrypted notes, pinned-note
  filtering, cloud sync status, attachment content retrieval, checklist status,
  and write support.
- SQL `LIKE` against `ZICNOTEDATA.ZDATA` is not a good content search strategy
  because the body is compressed/protobuf. dbrain should decode first and index
  normalized plaintext into its own FTS/provider index.
- It does not implement provider-local indexing, content hashes, upsert,
  privacy markers, or materialization into a durable corpus.
- Direct schema ownership becomes dbrain's responsibility; Apple can change
  private Notes tables/columns across macOS releases.

The strongest non-export direction is therefore an adapter interface with two
live implementations:

- `apple_events`: lower permission blast radius, uses Notes'
  scriptable interface.
- `direct_db`: experimental candidate, non-AppleScript, likely higher fidelity
  and faster incremental scans, requires Full Disk Access and private schema
  ownership.

Export-oriented tools should stay outside the steady-state sync path. If they
are used at all, they should be one-shot migration or diagnostic helpers.

Given the desire to avoid operating on exports and avoid AppleScript if
possible, the next implementation decision should be a focused `direct_db`
feasibility spike. If it can reliably enumerate folders/accounts, decode note
body text, identify locked/shared/deleted notes, and detect changes on the
current macOS version, then `direct_db` can be the first real provider. If that
spike is brittle, an Apple Events provider remains the safer baseline while the
direct parser matures.

## Dbrain Implementation Shape

The right reimplementation is not "run one of these MCP servers next to
dbrain". The right shape is to absorb the useful pieces behind dbrain's own
retrieval/MCP surface:

- A read-only `apple_notes` provider implements `Probe`, `Search`, `Get`, and
  optional `Index`.
- The provider returns evidence with `provider=apple_notes`, stable provider
  IDs, title, snippet, folder/account path, modification dates, and storage
  mode.
- dbrain MCP tools fan out to DB-backed items/sources plus configured providers,
  merge results, and preserve provenance.
- `dbrain_get` or an equivalent retrieval path can resolve an Apple Notes
  provider ID back through the provider without requiring the note to be a
  dbrain item.
- Write operations such as creating or editing Notes should be out of scope for
  v1. Retrieval and indexing are enough, and write access increases permission
  and safety risk.

Suggested internal interface:

```go
type Provider interface {
    Probe(context.Context) (ProviderStatus, error)
    Search(context.Context, ProviderQuery) ([]ProviderResult, error)
    Get(context.Context, ProviderID) (ProviderDocument, error)
}

type Indexer interface {
    Index(context.Context, ProviderIndexOptions) (ProviderIndexStats, error)
}
```

The Apple Notes provider can have two adapters behind that interface:

- `direct_db`: reads the local Notes database, decodes note bodies, and indexes
  normalized plaintext. This is the preferred spike because it avoids
  AppleScript and supports efficient batch/change detection, but it requires
  Full Disk Access and private schema ownership.
- `apple_events`: uses the Notes scripting dictionary through JXA/`osascript`
  or a small Swift helper. This is the safer fallback when Full Disk Access is
  undesirable or the private schema changes.

### Batch Indexing And Materialization

Batch indexing should be distinct from materialization.

For `provider_index`, dbrain should create a provider-local index/cache rather
than normal item rows. A first SQLite-backed schema is enough:

- `provider_documents`: provider, provider document ID, external ID, title,
  account, folder path, created/modified timestamps, content hash, flags,
  last-seen time, deleted/excluded status, and optional normalized text.
- `provider_documents_fts`: SQLite FTS table over title, snippet, folder path,
  and normalized text when that privacy level is enabled.
- `provider_sync_state`: provider, adapter, scope/config hash, last full scan,
  last high-water timestamp, parser version, and last error.

The batch command should support a dry-run first:

```sh
dbrain providers apple-notes probe
dbrain providers apple-notes index --mode provider_index --folder Research --dry-run
dbrain providers apple-notes index --mode provider_index --folder Research --apply
dbrain import apple-notes --folder Research --apply
```

`provider_index` gives the MCP/web/search layers a fast Notes retrieval surface
without copying Notes into the main item store. `materialized` import is still
useful, but it should remain a separate opt-in path that creates `apple_note`
items, rendered Markdown, links, summaries, categories, and normal dbrain FTS.

### Incremental Re-indexing

There is no public Apple Notes change feed suitable for this use case, so
incremental indexing should be polling-based.

For `direct_db`:

- Track the Notes database mtime as a cheap "anything changed?" guard.
- Track a high-water mark from Apple Notes modification timestamps.
- Query changed candidate notes by modification timestamp, then decode and hash
  body content before rewriting provider index rows.
- Still perform a lightweight full metadata scan on each run or periodically,
  because deletions, folder moves, shared/locked status changes, and ignore
  markers may not be captured safely by a simple `modified_at > high_water`
  query.
- Store parser/schema version in sync state so parser fixes can force a full
  re-index.
- Keep a manual `--force`/`repair` path that clears provider state and rebuilds
  from the source database.

For `apple_events`:

- Enumerate the configured accounts/folders.
- Compare stable note IDs, modification dates, and content hashes.
- Fetch bodies only for new or modified notes when possible.
- Treat title as display metadata only; it is not stable identity.

If a note gains `#dbrain-ignore`, enters an excluded folder, becomes locked, or
is otherwise excluded, privacy rules should purge provider-index text/FTS rows
and materialized item content. This is more important than preserving raw data
for this source type.

### Go Feasibility

The required pieces are practical in Go, but the direct database parser would be
owned by dbrain.

- SQLite access is already in the repo through `modernc.org/sqlite`, and dbrain
  already uses SQLite FTS5 for item/source search.
- gzip/zlib decompression is available in the Go standard library.
- protobuf decoding is available through `google.golang.org/protobuf`; direct
  DB support would need generated Go from the Notes `notestore.proto` schema or
  a deliberately minimal handwritten decoder. Generated code is the more
  maintainable choice.
- Apple Events can be reached without a third-party Go package by invoking
  `/usr/bin/osascript -l JavaScript` from Go. A Swift helper using
  `NSAppleScript` or ScriptingBridge is also viable if stronger typing or
  better permission UX is needed.
- There is no need for LanceDB or local transformer embeddings in v1. SQLite
  FTS gives a much smaller dependency surface, and embeddings can be added later
  through the same provider index if needed.

The main implementation risk is not library availability. It is the private
Notes schema: table names, column names, compressed payload layout, timestamp
fields, and attachment relationships are not a stable public API.

### Web Interface Exposure

If Apple Notes are materialized as `source_type=apple_note`, the existing web
interface can expose them through the same item/search/detail paths once the
renderer and filters know the new source type.

If Apple Notes stay in `provider_index`, the web interface needs a provider
search/detail path instead:

- search results can include `provider=apple_notes`, `materialized=false`, and
  an opaque provider document ID;
- opening a result calls the provider `Get` path or reads the provider index,
  depending on privacy mode;
- the UI should visibly distinguish live/provider-indexed notes from
  materialized dbrain items.

Both paths are feasible. Materialization is simpler for the current web UI, but
provider-indexed Notes better match the privacy model.

### Direct SQLite Recommendation

Directly reading the Notes SQLite database is worth pursuing, but only as a
read-only adapter with clear guardrails:

- Do not write to Apple's Notes database.
- Do not search `ZICNOTEDATA.ZDATA` directly; decode gzip/protobuf payloads and
  index normalized plaintext into dbrain's own provider index.
- Do not make direct DB the only adapter until the probe handles permissions,
  locked notes, shared notes, deleted notes, folder/account mapping, and schema
  drift diagnostics.
- Keep Apple Events as a fallback adapter because it uses the app's scriptable
  surface and has a lower permission blast radius.

So the recommended first build is: `direct_db` feasibility spike, then a
read-only provider index, then MCP/ask/web fan-out, then optional
materialization.

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

- The first user-visible integration can be `dbrain ask` and MCP retrieval
  fanning out to the local dbrain corpus plus Apple Notes.
- Query-time Apple Notes evidence should be marked as `provider=apple_notes`,
  `materialized=false`, and should not be persisted unless configured.
- Optional materialization can later store selected notes as `apple_note` items
  using the data model below.
- The same allowlist/exclusion/ignore-marker policy must apply to both live
  retrieval and materialization.

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

Recommended default:

- Start with `live_only` for probing and correctness.
- Add `provider_index` when query latency or recall requires it.
- Keep `materialized` as an explicit user choice for users who want Apple Notes
  to participate in all dbrain corpus features.

If `provider_index` stores full note text or snippets, the same privacy purge
rules as materialized notes must apply. If it stores embeddings only, still
clear them when a note is excluded because embeddings are derived from private
content.

## MCP Shape

The dbrain MCP server should remain the single agent-facing entry point.
Instead of requiring users to connect both `dbrain` and an Apple Notes MCP
server to every client, dbrain can fan out internally and return unified
evidence.

Possible MCP additions:

- `dbrain_search` accepts `source_types: ["apple_note"]` and/or
  `providers: ["apple_notes"]`.
- `dbrain_research` includes live provider hits alongside DB-backed item/source
  hits, with clear provenance.
- `dbrain_get` can read a live provider result by opaque provider ID during the
  same session or by re-querying the provider.
- `dbrain_stats_backlog` or a new health tool can report provider permission
  state without exposing content.

The agent-facing response should distinguish three evidence classes:

- DB-backed dbrain item/source evidence.
- Live Apple Notes evidence that was not stored.
- Materialized Apple Notes evidence that now exists as normal dbrain items.

An external MCP server such as `mcp-apple-notes` is useful as a prototype, but
making dbrain an MCP client of another local MCP server adds process management,
tool schema mapping, error handling, and provenance complexity. A direct
internal provider interface is simpler for production. If needed, an MCP-client
provider can be added later for arbitrary third-party memory servers.

## Proposed CLI

```sh
dbrain providers apple-notes probe
dbrain ask --include-provider apple_notes "What do all my notes and saved sources say about X?"
dbrain import apple-notes --dry-run
dbrain import apple-notes --folder "dbrain" --apply
dbrain import apple-notes --folder "Projects" --folder "Research" --apply
dbrain import apple-notes --account "iCloud" --folder "Research" --apply
dbrain import apple-notes --adapter apple_events --folder "Research" --apply
dbrain import apple-notes --adapter direct_db --folder "Research" --dry-run
dbrain import apple-notes --all --dry-run
dbrain import apple-notes --all --apply
```

Suggested first flags:

- `--include-provider apple_notes`
  Query Apple Notes live alongside the normal dbrain corpus for commands that
  support provider fan-out.
- `--dry-run`
  Print counts, accounts/folders matched, skipped counts, and sample titles
  without storing note bodies.
- `--adapter`
  Select the live Notes adapter. Suggested values: `auto`, `apple_events`, and
  `direct_db`. Default `auto` should prefer the lowest-permission working
  adapter unless config says otherwise.
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
enabled with config. Live retrieval and materialization should be configured
separately:

```yaml
apple_notes:
  enabled: true
  adapter: apple_events
  live_retrieval: true
  index_mode: live_only
  materialize: false
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

- `source_key`: `apple-note:<stable-hash>`
- `external_id`: adapter-specific stable note ID. For `apple_events`, this is
  the Notes scripting `id`; for `direct_db`, use the best stable database/cloud
  identifier available rather than a title/path.
- `canonical_url`: a local pseudo URL such as `apple-notes://<stable-hash>`, or
  a real Notes deep link if one is available from the adapter
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

## Live Retrieval And Incremental Sync

Live retrieval and optional materialization should be polling-based. Apple Notes
does not expose a stable public change feed suitable for `dbrain`.

1. Enumerate configured accounts/folders.
2. Read note IDs, titles, folder paths, modification dates, flags, and plaintext.
3. Compute a content hash.
4. For live-only retrieval, return scoped evidence without writing note content.
5. For provider indexing, update the provider-local index/cache.
6. For materialization, upsert changed notes as dbrain items and update
   `last_seen_at` for visible notes.

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

- `provider=apple_notes` should work in MCP, resources, prompts, search, web
  filters, stats, and topic tools.
- `source_type=apple_note` should work when Apple Notes are materialized.
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
   Add a macOS-only adapter probe that enumerates available accounts/folders
   and reports counts without storing content. It should detect whether
   `apple_events` is usable, whether `direct_db` has Full Disk Access, and which
   permissions are missing.

2. **Direct DB feasibility spike**
   Prototype the non-AppleScript path against a small local Notes corpus. The
   spike must prove body decoding, folder/account mapping, locked/shared flags,
   deletion markers, modification timestamps, and a forced full-rescan path. If
   the parser cannot meet those basics cleanly, keep it experimental rather than
   making it the default.

3. **Memory provider interface**
   Define a small internal provider shape for `Probe`, `Search`, `Get`, and
   optional `Index`. Search results should include title, snippet, score,
   provider ID, folder path, dates, and enough provenance to fetch the full note
   when needed. Keep this as live retrieval, not an export directory.

4. **Apple Notes provider**
   Implement either `apple_events` first for lower permission risk, or
   `direct_db` first if the non-AppleScript path proves reliable enough during
   prototype testing. The adapter choice should be config/flag-driven so both
   can coexist.

5. **MCP and ask fan-out**
   Teach dbrain retrieval to query DB-backed items/sources and live memory
   providers in parallel, merge/rank evidence, and show provenance. This is the
   first useful product milestone even without materializing notes.

6. **Optional materialization**
   Add `dbrain import apple-notes` with folder/account allowlists, ignore
   markers, shared/locked-note defaults, item upsert, note rendering, FTS, and
   JSON stats for users who want Apple Notes copied into dbrain.

7. **Privacy purge**
   Add tombstone/purge behavior for notes that become excluded after provider
   indexing or materialization. Include tests proving raw text, summaries,
   rendered notes, provider-cache content, and FTS content are removed.

8. **Sync integration**
   Add opt-in `sync all` integration behind config and `--skip-apple-notes`.
   Keep materialization disabled by default.

9. **Categorization and summaries**
   Add an Apple Notes-specific summary/categorization prompt path if generic
   item summarization is too source-agnostic. This should apply only to
   materialized notes unless a separate ephemeral summarization path is added.

10. **Attachment metadata**
   Import attachment metadata and URL attachments. Defer file extraction/OCR/PDF
   processing to a later explicit opt-in phase.

## Testing Plan

- Unit test parsing of Apple Events adapter JSON output if that adapter is
  built.
- Unit test parsing of direct DB adapter records if the direct adapter is built.
- Unit test include/exclude folder matching.
- Unit test ignore marker detection in plaintext and HTML-derived text.
- Unit test source key stability.
- Unit test content hash stability and change detection.
- Unit test purge/tombstone behavior for newly excluded notes.
- Store tests proving materialized `apple_note` items enter search/FTS and MCP
  retrieval.
- Retrieval tests proving live Apple Notes provider evidence can be merged with
  DB-backed dbrain evidence without persisting note bodies.
- CLI tests for dry-run/apply safety behavior.
- Manual macOS integration test with a small test Notes folder.
- Manual direct DB permission test that proves missing Full Disk Access fails
  with a clear diagnostic, if that adapter is built.

CI should not depend on the real Notes app or a real user note database.

## Acceptance Criteria

- `dbrain import apple-notes --dry-run --folder Test` reports matched/skipped
  counts without storing note bodies.
- `dbrain ask --include-provider apple_notes ...` can retrieve scoped Apple
  Notes evidence and DB-backed dbrain evidence in one answer without
  materializing note bodies.
- If `index_mode=provider_index`, changed or excluded notes update or purge the
  provider-local index without creating dbrain item rows.
- `dbrain import apple-notes --folder Test --apply` imports visible notes from
  that folder as `apple_note` items.
- Re-running the importer reports unchanged notes without rewriting them.
- Editing a note updates the item and rendered Markdown on the next import.
- Adding `#dbrain-ignore` to a previously cached or materialized note purges
  indexed content when `--forget-excluded` is enabled.
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
- Should the first production milestone be live MCP/ask retrieval only, with
  materialization deliberately deferred?
- Should dbrain support MCP-client providers for third-party local memory MCP
  servers, or keep Apple Notes as a direct internal provider first?
- Should live Apple Notes retrieval maintain a small local index for speed, and
  if so, what is allowed in that index: embeddings only, snippets, or full note
  bodies?
- Should `apple_events` be the first built adapter because it is lower
  permission, or should `direct_db` be first because it avoids AppleScript and
  can be more incremental?
- Is Full Disk Access acceptable for a clearly marked experimental direct DB
  adapter?
- How much of the private Notes database parser should be owned in Go versus a
  small Swift helper or external helper process?
- Should we support a one-time migration path from existing exporters, or avoid
  export-shaped workflows entirely until live sync is working?
