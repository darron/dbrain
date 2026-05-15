# AGENTS.md

This file captures the working rules for humans and agents modifying `dbrain`.
It is intentionally opinionated. Prefer following these rules over re-deciding
them in each change.

## Purpose

`dbrain` is a local-first second-brain system:

- local SQLite is the authoritative working database
- local rendered Markdown is the human-facing working surface
- remote services are for import, inference, or durability, not the primary
  source of truth

## Core Product Rules

### Keep local-first imports import-only

Local app integrations should read and materialize evidence into dbrain. They
should not mutate the upstream app or treat upstream state as dbrain-owned.

- prefer import-only behavior for local app stores
- never add write-back, creation, or editing support unless explicitly planned
  and accepted as a separate feature
- when reading another app's SQLite database, prefer a dbrain-owned snapshot
  before decoding/indexing; if a live source connection is unavoidable, it must
  be read-only and explicitly justified in the design
- do not run write-affecting statements such as `VACUUM`, checkpoints,
  migrations, `CREATE`, `INSERT`, `UPDATE`, or `DELETE` against upstream app
  databases
- fail closed with a clear diagnostic if read-only source access cannot be
  guaranteed

### Preserve raw data

Raw imported or extracted content must remain available so it can be reprocessed
later with better models or prompts.

- do not overwrite raw source extracts with summaries
- do not overwrite raw X media transcripts with summaries
- do not overwrite raw OCR text with summaries
- store derived summaries/OCR separately from the raw text they came from

### Favor model-agnostic coverage

By default, freshness and coverage stats should accept any valid summary on the
current extracted content.

- exact model/backend provenance should still be stored
- model/tool strings should not make the whole corpus look stale after a model
  swap unless a command explicitly asks for exact-backend freshness

### Treat imports as append-only by default

The local DB is a memory store, not a mirror that auto-deletes old signals.

- if a bookmark/star/like/watch-later item disappears upstream later, do not
  remove it locally by default
- audits should answer "what are we missing from upstream?"
- audits should not default to "what should we delete locally?"

### Keep research and chat evidence-grounded

Research and chat should help inspect the local brain, not create new
authoritative memory from model prose.

- retrieval packs, cited source keys, raw extracts, notes, and summaries are
  evidence
- model answers are derived synthesis and must not be treated as evidence in
  later research/chat turns
- follow-up chat may reuse prior evidence context, pinned sources, and previous
  user questions, but not previous model answers as facts
- browser chat state should default to session-only storage unless a later
  accepted design adds durable transcripts
- synthesis should cite dbrain source keys and keep retrieval failure/no-evidence
  states distinct from model failure states

## Pipeline Semantics

### Retry only genuinely retryable work

Do not let workers hot-loop forever on terminal cases.

- empty extracts with no content to summarize should become `blocked`, not
  endlessly retryable `error`
- oversized extracts that exceed model context should become `blocked` until a
  chunking/preprocessing path exists
- source summary timeouts and model context-limit failures should become
  blocked or terminal according to policy, not retryable rows that hot-loop
- user-facing stats should separate `pending`, `blocked`, and real `failed`
  states clearly

### Keep stats aligned with actual stages

Pipeline stats should reflect the real work the system performs.

- `x_media_summary` belongs in the main `Summary` coverage view
- OCR is a distinct enrichment stage and should stay separate
- avoid conflating operational/transcription errors with follow-on summary/OCR
  errors
- keep admin/stats semantics policy-aware and easy to reason about
- worker candidate selectors and backlog/pipeline stats must share the same
  predicate so dashboards do not claim "pending=0" while a worker still scans
  hundreds of rows

### Notes and search should reflect derived item enrichments

When item-level summary/OCR is added:

- render it into the item note
- include it in search/FTS inputs
- keep the raw transcript/OCR text separately available in the same note

## Apple Notes Rules

Apple Notes is planned as a direct SQLite, materialized, import-only source.
Do not reopen settled architecture without updating the planning doc first.

### Direct SQLite only

The intended Apple Notes path is direct local SQLite ingestion from the Notes
store.

- use direct SQLite snapshots, not AppleScript, Apple Events, JXA, Shortcuts, or
  exporter CLIs
- do not add a fallback adapter unless a new accepted design explicitly changes
  this
- require and diagnose Full Disk Access when needed; do not try to automate macOS
  permissions
- copy Apple's Notes DB/WAL/SHM triplet into a dbrain-owned snapshot before
  opening SQLite for import work; do not hardlink live Notes files
- do not mutate Apple's source DB, WAL, or SHM files
- treat private Notes schema drift as expected: probe columns/entities, tolerate
  unknown fields, and store parser/schema provenance

### Import scope and privacy

Once the Apple Notes importer is explicitly run or enabled in config, it should
import visible notes by default and rely on opt-out exclusions.

- default to all visible notes, not required folder allowlists
- support excluded accounts/folders and note-level ignore markers such as
  `[[dbrain-ignore]]`
- include shared notes by default
- skip password-protected notes by default and do not retain their titles,
  snippets, attachment names, or derived metadata
- if a previously imported note becomes excluded, support an explicit
  `--forget-excluded` purge path; never imply destructive purges from `sync all`

### Notes content and attachments

Apple Notes are user-authored working memory and may contain high-signal
attachments.

- preserve decoded raw note text separately from summaries
- summarize notes locally with an Apple Notes-specific prompt
- index useful attachments where possible, especially PDFs and images
- keep raw attachment text/OCR separate from summaries
- prefer local OCR for Notes attachments; hosted OCR should require explicit
  configuration
- classify unsupported, offloaded, encrypted, too-large, or decode-failed notes
  and attachments as `blocked`, not endlessly retryable `error`

### CLI-shaped operation

Apple Notes import should stay in the spirit of dbrain's CLI.

- run from explicit CLI commands and optional configured `sync all`
- do not add FSEvents capture, launchd orchestration, resident watchers, or a
  SaaS component in v1 or without a later accepted design that revisits this
- provider-index/live retrieval and write-back/note creation are out of scope
  unless a later accepted design says otherwise

## Safari Tabs Rules

Safari tabs are another local-first, import-only evidence source. They are not
dbrain-owned browser state.

- read Safari/iCloud tab state through a dbrain-owned snapshot when possible
- remember that Safari may need to be running on the import machine before
  macOS refreshes the local `CloudTabs.db`; launching Safari can make newly
  synced tabs appear in a follow-up import within seconds, so stale imports can
  reflect stale upstream local state rather than a dbrain importer bug
- target a device explicitly for tab imports; do not assume all synced devices
  should be imported together
- never close, mutate, reorder, or otherwise manage upstream Safari tabs from
  `dbrain`
- treat Safari tab imports as append-only memory by default, even if the tab
  later disappears upstream
- keep `sync all` Safari tab import opt-in through explicit flags/config/env so
  a stale iCloud tab set does not surprise users
- report created, updated, unchanged, skipped, and linked rows separately so a
  repeated import does not look like all tabs were newly ingested

## X-Specific Rules

### Native X bookmarks

Use the native cookie-backed X bookmark importer for X bookmark ingestion.

- use cookie-backed GraphQL/session flows, not OAuth-only assumptions
- incremental sync should be overlap-based on known tweet IDs/source keys
- do not fake `saved_at` / `bookmarked_at` from `synced_at`
- preserve upstream bookmark-order metadata such as `sortIndex` or an
  equivalent sequence/rank if available

### Stable bookmark identity

`x_bookmark` imports should behave like "pull new bookmarks", not "refresh every
mutable tweet counter forever".

- ignore mutable engagement counters in bookmark item hashing
- avoid churn from likes/reposts/replies/bookmark counts changing over time

### Separate post hydration from media completeness

Hydrating X post JSON and repairing/downloading media are related but not the
same thing.

- keep that distinction clear in stats and UI
- do not assume "post hydrated" means "all media complete"

### Quoted post hydration is recursive graph repair

Quoted tweets are not just nested JSON decoration on the parent post.

- store quoted tweets as first-class `x_quote` items plus explicit
  `quoted_post` links
- expect one imported bookmark to create additional quoted children that then
  need their own hydration, link extraction, OCR, transcription, and note
  rendering
- if a quoted child already has richer direct hydration than the parent's
  snapshot, recurse from the preserved direct child hydration instead of the
  shallower parent snapshot
- do not let nested quoted-child media make the parent item look like it still
  needs media hydration
- when testing quote support, cover quote-of-quote, quoted photos, quoted
  video, syndication fallback, and deleted/not-found quoted children
- bounded follow-up hydrate passes in orchestrators are acceptable; unbounded
  recursive loops are not

### X media transcription

The transcript is the raw evidence. The summary is derived.

- transcribe only downloadable `video` / `animated_gif` items with audio
- keep the transcript as raw item text
- summarize transcripts separately
- when summarizing X media, use the X post text as framing/context and the
  transcript as primary evidence
- if the post text makes claims the transcript does not support, attribute those
  claims instead of laundering them into fact

### X photo OCR

OCR is a first-class enrichment stage for downloaded X photos.

- keep raw OCR text separate from any future semantic summary
- current preferred hosted OCR path is Gemini Flash Lite
- current preferred local fallback is `tesseract`
- local fallback matters because some images may be blocked or degraded by
  hosted moderation

## Media Rules

### On-disk media is content-addressed

Downloaded X media filenames are content-hash based. Multiple DB assets may map
to the same on-disk file.

- `media_assets` are currently unique by remote URL, not by content hash
- identical bytes may therefore be represented by multiple asset rows but only
  one on-disk file

### Cleanup must be reference-aware

Any future media GC or cleanup must be conservative.

- never delete an on-disk media file just because one item or one asset row was
  removed
- only delete a local media path if no remaining asset/link records still rely
  on it

### Archive only after terminal enrichment coverage

Archive/prune should happen only after downstream local-media work is complete.

- do not archive/prune local X media while OCR or transcription is still
  pending for the owning item
- if a restore/re-download path is added for pruned media, treat it as a repair
  tool, not the normal steady-state path

### Large video policy

Do not assume every downloadable X video should be fetched and transcribed
locally forever.

- large/hour-long videos should eventually be gated by byte size and/or duration
- prefer lower-bitrate playable variants for transcription when possible
- classify intentionally skipped large assets as something explicit like
  `too_large` / `too_long` instead of retrying forever

## Storage and Durability

Long-term direction:

- local DB and local Markdown remain authoritative working state
- remote object storage is for durability/archive, not the primary source of
  truth
- if media/DB archival is added, prefer simple S3-compatible storage semantics
  and keep restores straightforward

## Development Rules

### Prefer Go-first, single-binary solutions

Prefer implementations that keep behavior inside the `dbrain` binary and remain
friendly to local builds and Homebrew distribution.

- avoid adding extra binaries, helper apps, app bundles, XPC services, daemons,
  or long-running sidecars unless they are explicitly justified and accepted
- small helper CLIs are acceptable only when necessary, especially on macOS
- if a helper is required, keep the orchestration and state transitions in Go
- when a feature depends on external tools, keep them optional or clearly
  diagnosed rather than making the core binary unusable

### Add regression tests for bugs we fix

When a real regression or production bug is fixed, add a test that would fail if
the bug reappeared.

- prefer the narrowest test that captures the broken behavior
- if the bug crossed stage boundaries, add the smallest integration-style test
  that proves the pipeline behavior stays fixed
- do not rely on memory or chat history to keep regressions from returning
- keep tests safe for GitHub Actions: do not depend on a developer's local
  browser profiles, installed helper tools, model services, network access, or
  OS-specific paths unless the test explicitly skips or fakes those dependencies
- keep tests independent of local secrets: unset or fake GitHub, OpenRouter,
  R2/S3, Keychain, and 1Password-backed credentials in tests that do not
  explicitly cover credential behavior
- when a command test is checking unrelated option plumbing, disable unrelated
  stages or use local/fake providers so preflight does not accidentally depend
  on ambient developer configuration
- when adding or changing preflight checks, add separate tests that prove both
  selected-stage failures and skipped/local-provider success paths work in a
  GitHub Actions-like environment with no personal secrets

### Guard schema migration history

SQLite migration numbers are append-only public history once a branch may have
been run locally, even if that branch or PR has not been merged yet.

- before adding a migration, inspect the current branch, `main`, and recent
  local/work-in-progress branches for existing `schema_migrations` versions and
  names
- never reuse a migration version number for different schema work; if there is
  any ambiguity, choose the next higher version
- remember that local developer DBs may already contain migration rows from
  unmerged branches, so a reused version can make fresh DB tests pass while
  existing DBs silently skip required schema creation
- when a migration adds or repairs a table/column/index used by new code, add a
  regression test that simulates an existing DB with prior migration metadata
  and proves reopening the store creates or repairs the required schema
- if migration history has already diverged, fix it with an idempotent
  follow-up repair migration rather than editing or reusing old migration
  numbers

### Keep the changelog current

When adding, fixing, or materially changing user-visible behavior, update
`CHANGELOG.md` before the change is considered complete.

- include features, bug fixes, CLI/config changes, schema/pipeline changes,
  operational behavior changes, MCP/tooling changes, and notable documentation
  changes
- use the existing dated heading style and keep entries short but specific
- mention important verification or affected areas when it helps future readers
- avoid churn while design docs or proposals are still being actively iterated;
  add or finalize the changelog entry once the plan is accepted or the
  implementation is built and tested
- if a change intentionally does not need a changelog entry, be ready to explain
  why in the final response or PR notes

### Prefer local models when practical

`dbrain` should keep working with local inference whenever practical.

- support local models through Ollama when the workload is reasonable
- keep hosted inference as the burst/catch-up path when it materially improves
  throughput
- OpenRouter is the current preferred hosted LLM path
- exact provider/model provenance should still be stored with derived outputs

### Always run the standard gates after code changes

For code changes, run:

- `task fmt`
- `task lint`
- `task test`

If CLI behavior changed materially, also rebuild and spot-check the command:

- `task build`

### Watch for user-visible operational confusion

When changing workers, stats, or dashboards:

- prefer outputs that explain what the system is actually doing
- for long-running imports/enrichment, show per-item progress when real work is
  happening; count unchanged-current rows in summaries instead of spamming them
- avoid merged counters that hide the real cause of work or failure
- avoid stages that look "pending forever" because semantics are unclear
- keep similarly named counters intentionally distinct and documented, for
  example `requested` versus `hydrated`, or `items_scanned` versus
  `sources_queued`

### Keep the web UI usable on mobile

The web UI is used remotely from phones over Tailscale, not just on desktop.

- avoid horizontal overflow from long source keys, URLs, citations, tags, and
  Markdown code blocks
- when a mobile interaction opens a detail panel, make the selected detail easy
  to reach without manual scrolling through the whole page
- prefer compact evidence lists for research/chat results and keep graph-heavy
  views optional on small screens

## Content Handling

`dbrain` is a research tool. The goal is to preserve and analyze source
material, not to make moral or editorial judgments about what is worth
collecting.

- do not add product-layer filtering based on the substance or politics of the
  content being gathered
- if a hosted tool rejects material for policy or moderation reasons, prefer a
  local fallback rather than silently losing coverage
- when a hosted provider is brittle on a class of content, keep the raw local
  input and route the enrichment through a local-capable path when possible
