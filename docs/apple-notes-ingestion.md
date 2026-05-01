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
provider, even if the first shippable implementation materializes visible
notes into dbrain items to reuse the existing FTS, MCP, web, link extraction,
summary, categorization, and attachment-processing pipeline.

The recommended implementation path is:

1. Prove direct SQLite snapshot access and note body decoding.
2. Ship an opt-out materialized importer that imports all visible Notes by
   default, with explicit account/folder/marker exclusions.
3. Run that importer from the normal CLI path periodically, including optional
   `sync all` integration when configured.
4. Index attachment metadata and Notes-provided attachment text first, then add
   file-content extraction for supported PDFs and images through dbrain's
   existing document/OCR paths once attachment bytes can be safely resolved.

This proposal assumes `dbrain` remains a local open-source CLI application. V1
should be a single Homebrew-distributable binary that the user runs manually or
from existing local scheduling habits. Do not introduce a background daemon,
FSEvents watcher, launchd service, SaaS component, or separate helper/app bundle
for the first implementation.

The implementation should be local-by-default and exclusion-driven:

- Import visible Notes by default once the Apple Notes importer is explicitly
  run or enabled in config.
- Support opt-out account/folder exclusions and note-level markers.
- Skip password-protected notes by default.
- Include shared notes by default.
- Support note-level `DO NOT index` markers such as `[[dbrain-ignore]]`.
- If a previously cached or materialized note later becomes excluded, purge
  indexed content and derived outputs while retaining only a minimal tombstone
  when needed.
- Highest priority: never corrupt Apple's Notes database. The importer should
  copy the Notes SQLite DB/WAL/SHM triplet into a dbrain-owned snapshot before
  opening SQLite, must not hardlink live files, and must not write
  PRAGMAs/checkpoints/VACUUM against the source.

This is a different data class from X bookmarks, GitHub stars, YouTube videos,
or linked web sources. Apple Notes are user-authored working memory. Privacy and
revocation need to be first-class product behavior, not an afterthought.

The steady-state design is direct SQLite import. It should not export Apple
Notes to files, invoke Notes exporters, or use Apple Events as the primary or
fallback path.

## Validation

The local Notes app exposes a scriptable interface at:

`/System/Applications/Notes.app/Contents/Resources/Notes.sdef`

That file is readable directly from the app bundle. Installing Xcode or the
Command Line Tools is still useful because `sdef`/`sdp` can inspect the
dictionary and generate ScriptingBridge headers, but the raw dictionary is
available without waiting on those tools.

That scripting dictionary exposes metadata, but it is not the chosen product
path:

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

The chosen ingestion path is direct Notes database access. This avoids
AppleScript/Apple Events, can be faster and more incremental, and can expose
data not surfaced through the scripting dictionary. It requires Full Disk
Access and depends on Apple's private Notes database schema and compressed note
payload representation.

The LifeOS/Second Brain framing also fits this direction: Apple Notes is mostly
the Capture layer; `dbrain` can add durable organization, distillation, search,
topic mapping, and MCP retrieval over that captured material.

Reference: https://lifecontext.vip/guide/intro/second-brain

## Goals

- Import Apple Notes as materialized `apple_note` items from SQLite.
- Let `dbrain ask`, MCP search/research tools, and the web UI retrieve
  materialized Apple Notes alongside DB-backed dbrain items and sources.
- Import all visible Notes by default after the command/config is enabled, with
  opt-out exclusions for accounts, folders, note markers, locked notes, and
  later privacy purges.
- Materialize into local SQLite/rendered Markdown for durable indexing, topic
  mapping, backlinks, categorization, summaries, and offline search.
- Keep raw note text available for local reprocessing for imported notes.
- Preserve enough metadata to detect updates without reprocessing every note.
- Extract links from materialized notes so URLs in Apple Notes can become normal
  linked source rows.
- Index useful attachment metadata and text, then supported attached files such
  as PDFs and images through existing dbrain extraction/OCR/summarization
  pipelines when file resolution is safe.
- Summarize imported Notes locally with an Apple Notes-specific prompt.
- Provide an explicit, reliable `DO NOT index` mechanism.
- Make dry-run output useful before any private content is stored.
- Keep the first implementation local-only, CLI-shaped, and macOS-specific
  without affecting non-macOS users.

## Non-Goals

- Do not sync through iCloud web APIs.
- Do not require exported Markdown/HTML files as the steady-state integration.
- Do not invoke Notes exporters as part of normal import.
- Do not use AppleScript, Apple Events, Shortcuts, or a fallback adapter.
- Do not introduce event capture, FSEvents-triggered indexing, launchd
  orchestration, or a background service in v1.
- Do not require a separate signed helper, app bundle, or SaaS component in v1.
- Do not write to Apple's Notes SQLite database, ever.
- Do not upload Apple Notes content to hosted models by default.
- Do not auto-delete materialized local records just because a note disappears
  from Apple Notes, except when the user explicitly requests a privacy purge
  policy.
- Do not implement provider-index/live retrieval, write-back, or note creation
  in v1.
- Do not try to mirror Apple Notes folder/tag behavior exactly.

## Integration Options

| Option | Feasibility | Recommendation | Notes |
| --- | --- | --- | --- |
| Direct Notes SQLite/CloudKit store | Medium to high risk | Required v1 path | Best non-AppleScript path and best fit for periodic CLI materialization. Requires Full Disk Access and tracks private schema details such as `NoteStore.sqlite`, compressed note data, deletion markers, and attachment records. Must snapshot the DB/WAL/SHM triplet before SQLite reads and leave source files untouched. |
| Apple Events / AppleScript / JXA | High | Reject | Scriptable and useful as prior art, but not needed for this product direction and adds Automation/TCC behavior we do not want. |
| Swift helper or extra CLI | Medium | Avoid | A separate helper may improve TCC identity, but the goal is a single binary if at all possible. |
| External Apple Notes MCP server | Medium | Reference/prototype only | Useful proof that Notes can be exposed as a tool surface. dbrain should import directly from SQLite rather than depending on a second MCP server. |
| Shortcuts CLI | Medium | Reject | Too indirect, write-capable in surprising ways, and not needed for read-only SQLite import. |
| Spotlight/metadata search | Low | Avoid as source of truth | Useful for discovery/debugging, not reliable for full-fidelity note ingestion. |
| Third-party exporters | Variable | Reference only | Do not use exported files or exporter CLIs in normal import. Use these projects only to understand schema behavior and test edge cases. |

## Existing Project Survey

No obvious reusable Go-native Apple Notes direct-DB library surfaced. The useful
projects are references for private schema behavior, protobuf decode, attachment
handling, and edge cases. Do not vendor or shell out to them in the normal
import path.

| Project | Approach | Language | Takeaway |
| --- | --- | --- | --- |
| [`cardmagic/notes`](https://github.com/cardmagic/notes) | Reads `~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite` and builds its own index | TypeScript | Useful reference for direct SQLite access, incremental indexing, and PDF attachment extraction. It requires Full Disk Access and private-schema handling. |
| [`kzaremski/apple-notes-exporter`](https://github.com/kzaremski/apple-notes-exporter) | Direct DB exporter to HTML/Markdown/JSONL/Text | Swift/C | Useful because it handles the Notes database, but it is export-oriented and GPL-3.0. Treat as a reference, not vendored code. |
| [`threeplanetssoftware/apple_cloud_notes_parser`](https://github.com/threeplanetssoftware/apple_cloud_notes_parser) | Direct database/parser tooling | Ruby | Mature forensic-style parser reference for schema behavior, protobuf decoding, version drift, attachments, and encrypted notes. Better as validation material than as a runtime dependency. MIT licensed. |
| [`ydkhatri/mac_apt`](https://github.com/ydkhatri/mac_apt) | macOS/iOS forensic parser suite | Python | Useful validation reference for Apple Notes protobuf/body parsing and damaged-data edge cases. Too broad to be a runtime dependency. Apache-2.0. |
| [`dogsheep/apple-notes-to-sqlite`](https://github.com/dogsheep/apple-notes-to-sqlite) | Direct DB to SQLite | Python | Good historical reference for Dogsheep-style import shape, but it is archived/read-only and still export/index oriented. |
| [`simonw/apple-notes-to-sqlite`](https://github.com/simonw/apple-notes-to-sqlite) | Active Dogsheep-style direct DB importer | Python | Track for current macOS schema fixes and CLI import ergonomics. Reference only. |
| [`HamburgChimps/apple-notes-liberator`](https://github.com/HamburgChimps/apple-notes-liberator) | Direct DB parser/exporter | Java | Useful second opinion for protobuf body decode and attachment handling. Reference only. |
| [`RhetTbull/apple-notes-parser`](https://github.com/RhetTbull/apple-notes-parser) | Direct DB parser | Python | Useful recent reference for multi-version schema handling, tags, mentions, attachments, and filters. Reference only. |
| [`sirmews/apple-notes-mcp`](https://github.com/sirmews/apple-notes-mcp) | MCP server for Apple Notes | Python | Useful for agent-facing command semantics. dbrain should not depend on a second MCP server for core retrieval, but the tool shape is relevant. |
| [`RafalWilinski/mcp-apple-notes`](https://github.com/RafalWilinski/mcp-apple-notes) | MCP server using JXA, LanceDB, local MiniLM embeddings, and full-text search | TypeScript/Bun | Strong proof of the federated-memory pattern. It is not file-export based, but it does maintain its own local index under `~/.mcp-apple-notes`. Good reference for MCP tool shape and local embeddings. |

Jon Baumann's "Revisiting Apple Notes" series at
[ciofecaforensics.com](https://ciofecaforensics.com/) is the main public
forensic reference to validate protobuf body decode, mergeable-data attachment
objects, table/gallery handling, and schema evolution across iOS/macOS releases.

## Existing Implementation Findings

Existing Apple Notes MCP servers and exporters are useful as prior art, but
they should not define the dbrain runtime shape.

`RafalWilinski/mcp-apple-notes` proves that Apple Notes can be exposed to
agents through a small MCP surface: index, search, get, list, and create. It
also demonstrates a local secondary index with full-text and embedding search.
However, it reaches Notes through JXA/Apple Events, uses title-based access in
places, and maintains its own LanceDB store. That is useful product precedent,
not the v1 implementation path for dbrain.

`sirmews/apple-notes-mcp` is the more relevant direct SQLite reference. It
queries `NoteStore.sqlite`, uses `ZICCLOUDSYNCINGOBJECT`, `ZICNOTEDATA`, and
`Z_METADATA`, handles Apple absolute timestamps by adding `978307200`, filters
deleted rows, and includes protobuf definitions copied from prior parser work.
Its metadata/list/search paths still rely heavily on `ZSNIPPET` and raw
database fields; they are not a complete full-text indexing model for decoded
note bodies. Treat its SQL shape, timestamp handling, and body-decode attempt
as references, not as production semantics to copy blindly.

The durable lessons for dbrain are:

- Direct SQLite is feasible and is the chosen path.
- Stable identity must come from database/cloud identifiers, never titles.
- Bodies must be decoded before indexing; do not search compressed protobuf
  blobs directly.
- Batch indexing and changed-note reindexing are better served by direct SQL
  metadata scans than by Apple Events enumeration.
- Attachment handling, encrypted notes, offloaded notes, schema drift, and
  purge semantics become dbrain-owned behavior.

Export-oriented projects remain valuable validation references for schema
behavior and edge cases, especially protobuf body decode and attachments. They
should not be runtime dependencies, and the steady-state path should not invoke
AppleScript, Apple Events, Shortcuts, or exporter CLIs.

## Dbrain Implementation Shape

The right reimplementation is not "run one of these MCP servers next to
dbrain". The right shape is to absorb the useful direct-DB pieces into dbrain.
The v1 implementation should be materialization-first.

That is a pragmatic choice, not a rejection of provider architecture:

- Materialized `apple_note` items immediately reuse existing dbrain behavior:
  SQLite FTS, MCP search, `ask`, rendered Markdown, source link extraction,
  summaries, categorization, topics, and web UI detail pages.
- A separate `provider_index` requires new tables, search merge logic, MCP
  provenance plumbing, web provider detail views, and privacy-purge behavior
  before any user-visible Notes retrieval works.
- The privacy risk is real, but the chosen v1 behavior is opt-out import after
  the user runs/enables the Apple Notes importer. Mitigation comes from
  explicit exclusions, dry-run, local-only processing, local summaries, and
  purge-on-ignore.

Keep the import code organized enough that future retrieval modes can exist,
but do not build additional read paths or a full provider abstraction before
the SQLite importer.

Suggested package shape:

```go
package applenotes

type Reader interface {
    Probe(context.Context) (ProbeResult, error)
    List(context.Context, Scope) ([]NoteMeta, error)
    Read(context.Context, NoteID) (NoteDocument, error)
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error)
```

The v1 reader is `direct_db`: snapshot Apple's local Notes database, decode
bodies and attachments, and materialize notes. This avoids AppleScript and is
well-suited to periodic CLI imports, but it requires Full Disk Access and private
schema ownership.

Write operations such as creating or editing Notes should stay out of scope for
v1. Retrieval and indexing are enough, and write access increases permission and
safety risk.

### Direct SQLite Shape

The direct DB reader must never write to Apple's database. The default path
should create a dbrain-owned filesystem snapshot of the Notes SQLite triplet
before opening SQLite at all:

- `NoteStore.sqlite`
- `NoteStore.sqlite-wal`, when present
- `NoteStore.sqlite-shm`, when present

Use APFS clone/copy semantics when available, but do not use hard links because
they alias the live files. Open only the copied snapshot with SQLite, validate
it with an integrity check, and retry once or fail closed with a clear "close
Notes and rerun" diagnostic if the snapshot is inconsistent.

No code path may execute write-affecting statements against the source,
including `CREATE`, `INSERT`, `UPDATE`, `DELETE`, `ATTACH`, `VACUUM`, or
`PRAGMA wal_checkpoint`. If a future implementation ever needs a live source
SQLite connection, it must be opened with read-only semantics and the acceptance
criteria must be revisited explicitly.

Snapshots should live under dbrain's temp/state tree, be guarded by a
single-flight lock so two Apple Notes imports do not race over the same
snapshot directory, and be pruned after the run unless `snapshot` or a debug
flag asks to keep them. The `snapshot --dir` subcommand is useful for testing
large Notes databases.

Known core path:

- Database: `~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite`
- Main table: `ZICCLOUDSYNCINGOBJECT`
- Body table: `ZICNOTEDATA`
- Metadata table: `Z_METADATA`
- Stable note UUID: `ZICCLOUDSYNCINGOBJECT.ZIDENTIFIER`
- Entity discriminator: `ZICCLOUDSYNCINGOBJECT.Z_ENT`, mapped through
  `Z_PRIMARYKEY`
- Note title: `ZTITLE1`
- Folder title: `ZTITLE2`
- Account name: `ZNAME`
- Deletion marker: `ZMARKEDFORDELETION`
- Password flag: `ZISPASSWORDPROTECTED`
- Body blob: `ZICNOTEDATA.ZDATA`

`ZICCLOUDSYNCINGOBJECT` is polymorphic: notes, folders, accounts,
attachments, smart folders, and other entities can all live in the same table.
The reader should probe `Z_PRIMARYKEY` and pin queries to the observed `Z_ENT`
values for notes, folders, accounts, and attachments rather than relying on
title columns alone.

The reader should not hard-code every column name blindly. It should probe
`PRAGMA table_info` at startup and choose the best available account and date
columns. Existing reverse-engineered readers show account joins drifting across
columns such as `ZACCOUNT2`, `ZACCOUNT3`, `ZACCOUNT4`, and later variants.
Creation/modification date columns also vary, so queries should use probed
columns or `COALESCE` over known variants.

Known account-column observations include `ZACCOUNT4` in existing direct-DB
readers and `ZACCOUNT7` on current Sequoia-era schemas. Treat those as probes,
not fixed truth.

Expect additive column drift and new attachment/object types every major macOS
cycle. Recent Sequoia/iOS 18-era schemas include fields related to audio
recordings, live transcription, inline math, collapsible sections, and
highlighting, such as `ZADDITIONALINDEXABLETEXT`, `ZALTTEXT`,
`ZNEEDSTRANSCRIPTION`, `ZOUTLINESTATEDATA`, and related attachment/object
flags. V1 should capture known cheap metadata but tolerate unknown columns and
object UTIs.

Representative metadata query shape:

```sql
SELECT
  note.Z_PK AS pk,
  note.Z_ENT AS note_entity,
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
  note.ZNEEDSTOBEFETCHED AS needs_fetch,
  notedata.ZDATA AS body_blob
FROM ZICCLOUDSYNCINGOBJECT AS note
LEFT JOIN ZICCLOUDSYNCINGOBJECT AS folder
  ON note.ZFOLDER = folder.Z_PK
LEFT JOIN ZICCLOUDSYNCINGOBJECT AS acc
  ON note.<probed_account_column> = acc.Z_PK
LEFT JOIN ZICNOTEDATA AS notedata
  ON note.ZNOTEDATA = notedata.Z_PK
WHERE note.ZTITLE1 IS NOT NULL
  AND note.Z_ENT = <probed_note_entity>
  AND COALESCE(note.ZMARKEDFORDELETION, 0) != 1
  AND COALESCE(folder.ZMARKEDFORDELETION, 0) != 1;
```

Use an identity fallback chain for materialized `external_id`:
`ZIDENTIFIER`, then a CloudKit record name from `ZSERVERRECORDDATA` when it can
be decoded safely, then `x-coredata://<metadata-uuid>/ICNote/p<pk>` as a last
resort. Avoid title or local integer primary key as the primary identity;
titles are mutable and local integer IDs may be device-specific.

Apple Notes timestamps are Mac Absolute Time in floating-point seconds. Add
`978307200` seconds to convert to Unix epoch, preserving fractional seconds
where the destination schema allows it.

### Body Decode Pipeline

The body decode path is:

```text
ZICNOTEDATA.ZDATA -> sniff magic -> gzip/zlib decompress -> protobuf parse
  -> document.note.note_text + attribute_run walk
  -> plaintext + structural placeholders
```

The `threeplanetssoftware/apple_cloud_notes_parser` proto defines the core
plaintext path:

```text
NoteStoreProto.document.note.note_text
```

That schema also exposes formatting runs, links, checklist metadata, Apple
Notes tags, and attachment markers. V1 should extract plaintext and walk
`attribute_run` enough to preserve embedded-object placeholders such as
`[table omitted]`, `[drawing]`, `[scanned document]`, `[audio recording]`,
`[image]`, and `[attachment]`. Without placeholders, table- or drawing-heavy
notes can look deceptively empty after import.

`ZMERGEABLEDATA1` and related mergeable-data fields are fidelity work for
structured embedded objects such as tables, galleries, drawings, scanned
documents, and some newer attachment-side structures. They should not block v1
body import. When a later pass decodes those payloads, sniff compression magic
bytes and fail loudly on unknown framing. Top-level `ZDATA` is expected to be
gzip/zlib today; attachment-side payloads may include other framing such as
zstd.

Do not search or index `ZICNOTEDATA.ZDATA` directly. It is compressed/protobuf
data. Decode it first, normalize plaintext, then write the selected content to
dbrain's own item fields and FTS.

### Batch Materialization

The batch command should support a dry-run first:

```sh
dbrain import apple-notes probe
dbrain import apple-notes --dry-run
dbrain import apple-notes
```

Materialized notes should become normal items:

- `source_type`: `apple_note`
- `source_key`: `apple-note:<account-id>:<note-id>`
- `external_id`: stable note ID from the fallback chain
- `title`: `ZTITLE1`
- `text`: decoded plaintext
- `folder_names`: account/folder path
- `published_at`: creation date when available
- `updated_at`: modification date
- `content_hash`: hash over decoded plaintext plus relevant metadata
- `raw_json`: reader metadata, DB path fingerprint, folder/account IDs, flags,
  parser version, Apple Notes tags, structural placeholders, and attachment
  metadata. Do not store raw HTML body in v1.

Once these rows exist, existing dbrain FTS, MCP, web UI, link extraction,
categorization, summaries, and topics should work with normal source-type
plumbing.

### Incremental Re-indexing

There is no public Apple Notes change feed suitable for this use case, so
incremental indexing should be polling-based and initiated by explicit CLI runs
or existing `sync all` flow. Do not add FSEvents-triggered capture, launchd
orchestration, or a resident watcher in v1.

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
- Store macOS version, Notes.app build when available, and observed
  `ZICCLOUDSYNCINGOBJECT` column set. Warn, do not fail, when the observed
  schema changes.
- Keep a manual `--force`/`repair` path that clears Apple Notes import state and
  rebuilds notes from the source database.
- Treat cloud-only/offloaded notes with null or stub bodies as
  `blocked: not_downloaded`, not successful empty imports.

If a note gains `[[dbrain-ignore]]`, enters an excluded folder, becomes locked,
or is otherwise excluded, privacy rules should purge materialized item content,
derived summaries, rendered Markdown, and FTS rows only when
`--forget-excluded` is explicitly set. This source type should prefer privacy
revocation over the usual "preserve raw data" rule, but `sync all` must not
invoke destructive purges implicitly.

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
- Resolved PDFs can use dbrain's existing document extraction/summarization
  path when that path accepts the copied attachment file; otherwise they should
  be marked blocked rather than retried forever.
- Image OCR should prefer a local provider for Apple Notes attachments. The
  exact local OCR choice is still open, but attachment OCR should be wired as a
  normal dbrain enrichment stage that stores raw OCR separately from summaries.
- There is no need for LanceDB or local transformer embeddings in v1. SQLite FTS
  gives a much smaller dependency surface.

There is no obvious reusable Go library to vendor. Existing direct-DB exporter
references are in other languages, including the Swift/C
`kzaremski/apple-notes-exporter`, and should stay references rather than
dependencies.

The main implementation risk is not library availability. It is safe ownership
of Apple's private Notes schema: table names, column names, compressed payload
layout, timestamp fields, and attachment relationships are not a stable public
API, and the live source DB/WAL/SHM files must remain untouched.

### Distribution And Permissions

V1 should remain a single open-source CLI binary suitable for local builds and
Homebrew distribution. Do not add a separate helper, app bundle, XPC service,
or SaaS component for Apple Notes import.

For the `direct_db` path, the user must grant Full Disk Access to the process
context that opens the Notes group container. For local development this may be
Terminal, iTerm, the IDE, or another host process. For a Homebrew-installed
binary, TCC behavior can still be attributed through the launching terminal or
the binary path depending on macOS context. The CLI should diagnose permission
failure clearly rather than attempting to automate the permission flow.

Stable signed helpers can provide cleaner TCC identity, but that is explicitly
out of scope for the first CLI implementation.

### Web Interface Exposure

Materialized notes are the easiest web path. Once `apple_note` items exist, the
existing web interface can expose them through normal item/search/detail paths
after the renderer and filters know the new source type.

## Future Retrieval Modes

The first implementation should materialize visible Apple Notes as
`source_type=apple_note` items. That is the shortest path to value because it
reuses dbrain's existing SQLite FTS, rendered Markdown, MCP search, web UI,
link extraction, categorization, summaries, and topics.

There is no current need for a separate Apple Notes provider index if
materialized import works well. If a future retrieval mode is proposed, it
should be treated as a separate design because it would need its own local
cache/index, provenance model, privacy purge behavior, and web/MCP detail path.

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

For v1, MCP should treat imported notes as normal materialized dbrain evidence
with clear `source_type=apple_note` provenance.

## Proposed CLI

```sh
dbrain import apple-notes probe
dbrain import apple-notes --dry-run
dbrain import apple-notes
dbrain import apple-notes --exclude-folder "Private" --exclude-folder "Passwords"
dbrain import apple-notes --limit 25 --show-titles --dry-run
dbrain import apple-notes snapshot --dir /tmp/dbrain-notes-snapshot
dbrain import apple-notes decode --note <note-id>
dbrain sync all --skip-apple-notes
dbrain ask "What do my notes and saved sources say about X?"
```

Suggested first flags:

- `--dry-run`
  Run the full import decision path without persistence: body decode, ignore
  marker detection, blocked-state classification, and attachment
  classification. Do not print note bodies or titles by default. The default
  command behavior materializes matching notes.
- `--show-titles`
  Allow dry-run output to show sample titles. Default false because note titles
  can be private.
- `--force`
  Re-render and re-summarize matching notes even when imported content is
  unchanged. Use this after parser or prompt fixes.
- `--limit`
  Limit matched notes for iteration, fixture generation, and manual testing.
- `--account`
  Optional include filter for selected accounts such as `iCloud`, `On My Mac`,
  or an Exchange mirror. By default, all visible accounts are included.
- `--folder`
  Optional include filter for selected folders. Repeatable. Match full
  account/folder paths by default, with explicit glob support if added. By
  default, all visible folders are included.
- `--exclude-folder`
  Exclude folders by exact path or glob.
- `--exclude-account`
  Exclude accounts by exact name or glob.
- `--exclude-shared`
  Exclude shared notes. Default false because shared notes are included.
- `--include-shared`
  Include shared notes for a one-off CLI run when config disables them.
- `--include-locked`
  Attempt password-protected notes. Default false, and likely ineffective unless
  Notes has them unlocked.
- `--skip-attachments`
  Opt out of the default attachment indexing path. Import note bodies and
  attachment metadata, but skip attachment file extraction/OCR/PDF processing.
  Attachments are on by default when configured; this flag disables the
  file-content part for one run.
- `--skip-attachment-ocr`
  Opt out of default image OCR. Keep attachment metadata and any supported
  non-OCR text extraction, but skip image OCR.
- `--ocr-provider`
  Select the local OCR provider once implemented. Hosted OCR should require
  explicit configuration and should not be the default for Apple Notes.
- `--forget-excluded`
  Purge content for existing imported notes that are now excluded or marked
  `DO NOT index`. Never implied by `sync all`.
- `snapshot --dir`
  Create a snapshot copy in the requested directory and print its path without
  decoding or importing notes. Useful for debugging large Notes databases.
- `--json`
  Machine-readable stats.

`sync all` should not import Apple Notes by default. The standalone
`dbrain import apple-notes` flow remains the primary test surface, and
`sync all` calls the same importer only when explicitly enabled by
`--apple-notes` or config/env. Once enabled, it imports visible notes by default
while respecting exclusions. The v1 config makes materialization explicit:

```yaml
apple_notes:
  enabled: true
  exclude_accounts: []
  exclude_folders:
    - Private
    - Passwords
  exclude_shared: false
  index_attachments: true
  attachment_ocr: true
  attachment_max_bytes: 52428800
  tesseract_binary: tesseract
```

Startup checks should make the direct SQLite tradeoff explicit:

```text
Apple Notes import reads Apple's private Notes SQLite database.
It requires Full Disk Access for this terminal or dbrain binary and may break
after macOS Notes schema changes. Run --dry-run first.
```

## Data Model

The data model below applies to materialized Apple Notes in dbrain.

Use a new item source type:

`source_type = apple_note`

Treat each Apple Note as a primary item, not as a linked source. It is captured
user-authored material, similar in importance to an imported bookmark item.
Links inside the note can then enter the existing link extraction pipeline and
create normal `sources` rows.

Suggested item mapping:

- `source_key`: `apple-note:<account-id>:<note-id>`. Include account identity
  so restored backups or multiple accounts cannot silently merge colliding note
  IDs.
- `external_id`: best stable database/cloud identifier available from the
  direct SQLite fallback chain. Do not use a title/path as identity.
- `canonical_url`: a Notes deep link such as
  `applenotes://showNote?identifier=<note-id>` when verified for the local
  macOS version; otherwise a local pseudo URL such as
  `apple-notes://<account-id>/<note-id>`
- `title`: note `name`
- `text`: note `plaintext`
- `raw_json`: JSON envelope containing account, folder path, note ID, flags,
  including pinned/checklist/outline state when present, structural
  placeholders, `apple_note_tags`, attachment metadata, attachment-derived text
  provenance, reader name, schema fingerprint, and importer version. Do not
  store raw HTML body in v1.
- `published_at`: creation date
- `updated_at`: modification date from Apple Notes
- `last_seen_at`: import time
- `folder_names`: account/folder path
- `content_hash`: hash over plaintext, selected metadata, structural
  placeholders, and attachment metadata
- `note_path`: `items/apple-notes/<year>/<slug-or-hash>.md`
- `raw_json.apple_note_tags`: source-scoped Apple Notes hashtags/tags as a JSON
  array. Keep them searchable through rendered/search text when practical, but
  do not promote them into global `user_tags` in v1. If tag extraction is not
  available from the decoded body/schema, store an empty array and record the
  parser limitation.
- `user_tags`: normal dbrain tags only, not Apple Notes hashtags in v1

The existing item summary, categorization, FTS, search, MCP, web UI, topic, and
entity paths should work once the importer populates common item fields.

## Privacy Model

Apple Notes import is broad by default once enabled, but stricter than other
importers on revocation and sensitive skipped content.

### Default Scope

The first Apple Notes import should be explicit at the command/config level,
but it should not require folder allowlists. Once the user runs
`dbrain import apple-notes` or enables Apple Notes in config, all visible notes
are in scope by default unless excluded.

Recommended UX:

```text
Apple Notes import reads all visible notes by default.
Run with --dry-run first to inspect counts without storing note content or titles.
Shared notes are included unless excluded.
Use --exclude-folder, --exclude-account, --exclude-shared, or [[dbrain-ignore]] to opt out.
```

### Exclusions

Supported exclusion layers:

- Configured excluded accounts.
- Configured excluded folders.
- Password-protected notes by default.
- Shared notes only when `--exclude-shared` or equivalent config is set. By
  default, shared notes are included.
- Note-level plaintext markers.
- Folder-name conventions such as `/Private` or `/dbrain-ignore` when
  configured.

Recommended note-level markers:

- `[[dbrain-ignore]]`

Avoid hashtag defaults such as `#dbrain-ignore` because Apple Notes treats
hashtags as first-class tags and surfaces them in the tag UI. Keep markers
configurable for users who prefer their own conventions.

When locked notes are skipped, do not retain title, snippet, attachment names,
transcript/summary metadata, or other locked-note-derived strings. Store only
minimal identity needed to avoid repeated attempts or to honor explicit purge
state.

### Previously Indexed Notes

If a note was previously materialized and later becomes excluded or gains an
ignore marker, privacy should override the normal "preserve raw data" rule.

The materialization purge path should:

- Clear `text`.
- Clear raw body-derived content from `raw_json`.
- Clear item summaries.
- Clear attachment OCR/extract-derived fields.
- Remove the rendered note content or replace it with a privacy tombstone.
- Remove it from FTS.
- Keep only minimal identity metadata needed to avoid reimporting it, such as
  `source_key`, `external_id`, `source_type`, `last_seen_at`, and an import
  status/reason.

If the current schema cannot express this cleanly, add an explicit item-level
privacy/import status rather than overloading normal summary or extract status.

### Dry Run

Dry run should execute the full import decision path without persistence:
snapshot, body decode, ignore marker detection, blocked-state classification,
attachment classification, content hashing, and planned upsert/skip decisions.
It should avoid printing note bodies and titles by default. It can print:

- matched accounts
- matched folders
- counts by folder
- counts skipped by exclusion reason
- blocked-state counts
- attachment classification counts

Only print sample titles when `--show-titles` is explicitly set.

## Materialization And Incremental Sync

Materialization should be polling-based. Apple Notes does not expose a stable
public change feed suitable for `dbrain`, and v1 should stay in the spirit of a
CLI application: each run snapshots the current local Notes DB, imports changes
for the configured scope, reports stats, and exits.

1. Enumerate all visible accounts/folders, then apply configured exclusions and
   optional include filters.
2. Read note IDs, titles, folder paths, modification dates, flags, and plaintext.
3. Compute a content hash.
4. For materialization, upsert changed notes as dbrain items and update
   `last_seen_at` for visible notes.

This is acceptable for manual runs, cron-like local habits, and configured
`sync all`. Notes libraries are usually small enough for periodic enumeration,
and the first implementation can expose limits and progress logs if needed.

Direct DB optimization:

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

Folder matching should use full account/folder paths by default to avoid
collisions such as `Personal/Research` and `Work/Research`. Leaf-name or glob
matching can be added as explicit modes. Smart folders are saved queries rather
than containers; v1 should ignore them for `--folder` matching or report them as
`blocked: smart_folder` rather than silently importing nothing.

Quick Notes, On My Mac, iCloud, and Exchange-backed Notes accounts should be
reported separately. Exchange accounts may behave like read-only mirrors and
should not be assumed equivalent to local/iCloud Notes.

Expected non-retryable blocked states:

- `blocked: locked`
- `blocked: smart_folder`
- `blocked: not_downloaded`
- `blocked: decode_failed`
- `blocked: schema_unknown`
- `blocked: empty_decoded`
- `blocked: too_large`

These should not hot-loop as `error`. User-facing stats should separate
`notes_seen`, `notes_imported`, `notes_unchanged`, `notes_updated`,
`notes_blocked_locked`, `notes_excluded_shared`,
`notes_blocked_offloaded`, `notes_blocked_decode`,
`notes_blocked_empty`, `notes_blocked_smart_folder`, `notes_purged`,
`decode_errors`, and `db_open_errors`. Candidate selectors and backlog/stats
predicates should share the same logic.

## Attachment Handling

Attachment indexing is in scope for v1 because attachments are often the
highest-signal part of Apple Notes: saved PDFs, screenshots, scanned documents,
photos, and URL cards. The importer should still preserve raw note text and raw
attachment-derived text separately from summaries.

V1 lands attachment support in two steps. The first step is metadata and cheap
text that Notes already stores; the second step is conservative file-content
extraction for supported attachments after file resolution is proven safe.

The first attachment milestone should store attachment metadata, structural
placeholders, and any Notes-provided attachment-derived text when present:

- attachment ID
- name
- content identifier
- URL for URL attachments
- creation/modification dates
- shared flag
- UTI/content type and byte size when available
- `ZADDITIONALINDEXABLETEXT`, including audio transcripts when Apple has
  already generated them
- `ZALTTEXT` for images, math, and similar inline objects
- URL attachment target/title fields
- existing OCR/handwriting/indexable text fields when present
- cross-note link attachment UUIDs when discoverable

The file-content path should be conservative and read-only. It should not block
the body importer or local note summaries:

- Resolve attachment files from the Notes database/container without writing to
  the Notes store.
- Copy or stream attachment bytes into dbrain-controlled temp/state paths before
  extraction when needed.
- Extract text/PDF attachments locally inside the `dbrain` binary when the
  attachment can be resolved and copied to a dbrain-controlled temp path.
  Otherwise mark the attachment blocked.
- Route images and scanned-document images through OCR when attachment-file
  resolution is straightforward. Prefer a local OCR provider for Apple Notes;
  hosted OCR should require explicit configuration.
- Store raw OCR/text extraction output separately from attachment summaries.
- Classify unsupported, missing, offloaded, encrypted, too-large, or decode
  failed attachments as blocked states rather than retrying forever.

Size and type limits should be explicit from the start. Large videos, audio
recordings, proprietary documents, and unsupported embedded objects should get
clear `blocked` reasons until a dedicated extraction path exists.

## Link Extraction

Materialized Apple Notes should participate in normal item link discovery:

- URLs in plaintext should become source candidates.
- URL attachments should become source candidates.
- The Apple Note remains the backlink context for linked source notes.

This makes Apple Notes a bridge from personal capture to the existing source
enrichment pipeline.

## MCP And Search Semantics

Apple Notes should be easy to include or exclude in retrieval:

- `source_type=apple_note` should work in MCP, resources, prompts, search, web
  filters, stats, and topic tools when Apple Notes are materialized.
- Research packs should surface that Apple Notes are user-authored notes, not
  third-party sources.
- Answers should avoid treating personal notes as authoritative external facts.
  They are evidence of what the user wrote or captured.

Apple Notes summaries are in scope and should use local models by default with
a note-aware prompt:

- Preserve personal framing.
- Avoid inventing certainty.
- Distinguish tasks, ideas, diary-like material, meeting notes, and copied
  external excerpts.
- Extract action items only when explicitly present.
- Attribute copied excerpts or pasted source material when the note makes that
  context clear.

## Implementation Phases

1. **Probe**
   Add a macOS-only direct DB probe that detects Full Disk Access failures,
   snapshots the Notes DB/WAL/SHM triplet, probes schema columns/entity IDs,
   and reports account/folder/note counts without decoding or storing note
   bodies.

2. **Body decoder**
   Implement a standalone `ZDATA []byte -> plaintext` decoder using gzip/zlib
   plus protobuf parsing. Walk `attribute_run` enough to emit structural
   placeholders. Unit test it with captured non-private fixtures before wiring
   it to the importer. Add a hidden/debug command such as
   `dbrain import apple-notes decode --note <id>` for local investigation.

3. **Materialized importer**
   Add `dbrain import apple-notes` with default all-visible import,
   account/folder exclusions, optional include filters, ignore markers,
   shared-note inclusion, locked-note blocking, item upsert, note rendering,
   FTS, dry-run full decision reporting, and JSON stats. This is the first
   useful product milestone.

4. **Local note summaries**
   Add an Apple Notes-specific local summary prompt and wire summaries into the
   normal item summary path with model/tool provenance.

5. **Attachment metadata and cheap text**
   Index attachment metadata, URL attachment fields, cross-note references, and
   Notes-provided indexable/alt/OCR/transcript text where present.

6. **Attachment file-content extraction**
   Index file content where supported. Route resolvable PDFs through the
   local PDF text extractor, route resolvable images through local OCR, keep
   raw extracted/OCR text separate from summaries, and
   classify unsupported/offloaded/too-large attachments as blocked.

7. **Incremental re-import**
   Track source DB mtime, high-water modification timestamps, content hashes,
   parser version, and last-seen state. Re-runs should skip unchanged notes,
   update changed notes, and support `--force` after parser fixes. This remains
   periodic CLI polling, not FSEvents-triggered capture.

8. **Privacy purge**
   Add tombstone/purge behavior for notes that become excluded after
   materialization. Include tests proving raw text, summaries, rendered notes,
   and FTS content are removed.

9. **Sync integration**
   After the standalone importer is tested and stable, add configured
   `sync all` integration behind config and `--skip-apple-notes`. `sync all`
   should call the same importer path rather than introducing separate Notes
   semantics. Apple Notes remains disabled unless explicitly configured.

10. **MCP/search/web polish**
   Ensure `source_type=apple_note` works in search filters, MCP output,
   rendered notes, web UI filters/detail pages, and topic/search resources.

11. **Later proposals**
   Defer table reconstruction, drawing/gallery fidelity, complex audio/video
   extraction, and other fidelity improvements. Write-back and note creation
   remain out of scope.

## Testing Plan

- Unit test parsing of direct DB reader records.
- Unit test body decoding from representative `ZDATA` blobs.
- Unit test structural-placeholder extraction from `attribute_run` entries.
- Unit test schema column probing for account/date column drift.
- Unit test `Z_ENT` entity probing and polymorphic table filtering.
- Unit test filesystem snapshot creation for DB/WAL/SHM, pruning, the
  `snapshot --dir` subcommand, and single-flight locking around the snapshot
  directory.
- Unit test that snapshots are copies or APFS clones, never hard links to live
  Notes files.
- Unit test snapshot validation and fail-closed behavior when the copied
  DB/WAL/SHM set is inconsistent.
- Unit test source DB safety: no source file is opened for SQLite import work,
  write statements/checkpoints are not issued against the source, and import
  does not mutate source database/WAL/SHM mtime/hash in fixture tests.
- Unit test open failure diagnostics for missing Full Disk Access.
- Unit test `probe` does not decode note bodies, while `--dry-run` runs the
  full import decision path without persistence or default body/title output.
- Unit test include/exclude folder matching.
- Unit test ignore marker detection in plaintext and HTML-derived text.
- Unit test source key stability.
- Unit test content hash stability and change detection.
- Unit test Apple Notes tag extraction into `raw_json.apple_note_tags` when
  tags are present, and empty-array behavior when the parser cannot extract
  them.
- Unit test blocked states for locked, smart-folder, offloaded, decode-failed,
  schema-unknown, empty-decoded, and too-large notes.
- Unit test shared notes are included by default and excluded only when
  `--exclude-shared` or equivalent config is set.
- Unit test purge/tombstone behavior for newly excluded notes.
- Unit test attachment metadata and Notes-provided cheap text indexing.
- Unit test PDF text extraction handoff, image OCR handoff, and blocked
  attachment states for supported file-content extraction paths.
- Unit test Apple Notes-specific summary prompt wiring and local model
  provenance.
- Store tests proving materialized `apple_note` items enter search/FTS and MCP
  retrieval.
- CLI tests for dry-run/write safety behavior.
- Manual macOS integration test with a small test Notes folder.
- Manual direct DB permission test that proves missing Full Disk Access fails
  with a clear diagnostic.

CI should not depend on the real Notes app or a real user note database.

Fixture coverage should include at least representative cases for plaintext,
checklists, URL attachments, tables, drawings/PencilKit, galleries/scanned
documents, audio transcript fields, inline math/alt text, collapsible headings,
highlighted text, locked notes, shared notes, offloaded/cloud-only rows, and
Recently Deleted rows. Fixtures can be synthesized in a throwaway Notes account
or reused from public fixtures where licensing allows.

## Acceptance Criteria

- `dbrain import apple-notes probe` reports permission/schema/account/folder
  counts without decoding note bodies.
- `dbrain import apple-notes --dry-run` reports matched/skipped/blocked counts
  for all visible notes through the full import decision path without storing
  or printing note bodies or titles by default.
- `dbrain import apple-notes` imports visible notes as `apple_note`
  items, respecting exclusions and ignore markers.
- The importer opens only a dbrain-owned snapshot for SQLite import work and
  does not mutate the source database, WAL, or SHM files.
- Re-running the importer reports unchanged notes without rewriting them.
- Editing a note updates the item and rendered Markdown on the next import.
- Adding `[[dbrain-ignore]]` to a previously materialized note purges indexed
  content when `--forget-excluded` is enabled.
- Password-protected notes are skipped by default.
- Shared notes are included by default and can be excluded explicitly.
- Apple Notes hashtags/tags are stored in `raw_json.apple_note_tags` when
  extractable and are not promoted into global `user_tags`.
- Attachment metadata and Notes-provided attachment text are indexed. Supported
  PDFs and images are indexed through existing extraction/OCR paths when
  attachment bytes can be safely resolved, with raw extracted text kept
  separate from summaries.
- Imported notes receive local Apple Notes-specific summaries when summarization
  is enabled.
- Materialized Apple Notes appear in `search`, MCP tools,
  topic/search resources, and the web UI with `source_type=apple_note`.
- URLs inside materialized notes enter the normal link/source enrichment
  pipeline.
- `sync all` imports Apple Notes only after the standalone importer is proven
  reliable, only when explicitly configured, and never invokes
  `--forget-excluded` implicitly.
- The importer exits after each run; no watcher, daemon, or event-capture
  process is required.

## Open Questions

- Should deleted materialized Apple Notes be retained forever like other
  imports, or should this provider have a stronger cleanup affordance because
  notes are personal?
- Which local OCR provider should be the default for Apple Notes image/scanned
  document attachments?
- What byte-size and page-count limits should attachment extraction enforce on
  the first release?
- How much table/gallery/drawing fidelity is worth implementing after the
  plaintext plus placeholder importer works?
- How should cloud-only/offloaded attachment repair be triggered without using
  Apple Events or write-capable APIs?
