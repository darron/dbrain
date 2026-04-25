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

## Pipeline Semantics

### Retry only genuinely retryable work

Do not let workers hot-loop forever on terminal cases.

- empty extracts with no content to summarize should become `blocked`, not
  endlessly retryable `error`
- oversized extracts that exceed model context should become `blocked` until a
  chunking/preprocessing path exists
- user-facing stats should separate `pending`, `blocked`, and real `failed`
  states clearly

### Keep stats aligned with actual stages

Pipeline stats should reflect the real work the system performs.

- `x_media_summary` belongs in the main `Summary` coverage view
- OCR is a distinct enrichment stage and should stay separate
- avoid conflating operational/transcription errors with follow-on summary/OCR
  errors
- keep admin/stats semantics policy-aware and easy to reason about

### Notes and search should reflect derived item enrichments

When item-level summary/OCR is added:

- render it into the item note
- include it in search/FTS inputs
- keep the raw transcript/OCR text separately available in the same note

## X-Specific Rules

### Native X bookmarks

Prefer the native cookie-backed X bookmark importer over the legacy FT import
path whenever possible.

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

### Prefer Go-first solutions

Prefer implementations that keep behavior inside the `dbrain` binary.

- small helper CLIs are acceptable when necessary, especially on macOS
- if a helper is required, keep the orchestration and state transitions in Go

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
- avoid merged counters that hide the real cause of work or failure
- avoid stages that look "pending forever" because semantics are unclear
