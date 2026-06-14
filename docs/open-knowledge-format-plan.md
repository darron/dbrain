# Open Knowledge Format Markdown Extension Plan

Status: proposal
Date: 2026-06-14
Confidence: moderate-high for the `dbrain` architecture, moderate for OKF
stability because OKF v0.1 is still a draft.

## Summary

`dbrain` should support Open Knowledge Format (OKF) by adding a dedicated
OKF export projection, not by declaring the existing vault to be OKF.

The existing vault is close to OKF in spirit: it is local Markdown with YAML
frontmatter, generated from SQLite, browsable by humans, and useful to agents.
But it is not OKF-conformant today:

- item/source/entity/topic notes do not include the required OKF `type`
  frontmatter field
- current `index.md` files are normal vault notes with frontmatter, while OKF
  reserves `index.md` for directory indexes
- current note relationships are partly Obsidian wiki links, raw URLs, note
  paths, and database source keys, not standard Markdown concept links
- the vault is an operational projection of `brain.db`, not a clean exchange
  bundle with stable concept IDs and portable indexes

The right direction is therefore:

1. Keep SQLite as the authoritative working database.
2. Keep the current vault as the human-facing Obsidian/local Markdown surface.
3. Add `internal/okf` as a second Markdown projection that can export selected
   `dbrain` evidence as an OKF bundle.

The MVP should export a spec-conformant bundle from current items and sources,
with generated `index.md` files, Markdown cross-links, source citations, and a
validator. Importing OKF bundles is explicitly out of scope; `dbrain` should
keep using its current importers and intake paths for data acquisition.

## Sources Reviewed

External OKF materials:

- Google Cloud announcement:
  `https://cloud.google.com/blog/products/data-analytics/how-the-open-knowledge-format-can-improve-data-sharing/`
- OKF v0.1 draft spec:
  `https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md`
- Google reference repo and OKF README:
  `https://github.com/GoogleCloudPlatform/knowledge-catalog/tree/main/okf`
- Reference sample bundles:
  `okf/bundles/ga4`, `okf/bundles/stackoverflow`,
  `okf/bundles/crypto_bitcoin`
- Reference producer/consumer code:
  `okf/src/enrichment_agent/bundle/document.py`,
  `okf/src/enrichment_agent/bundle/index.py`,
  `okf/src/enrichment_agent/viewer/generator.py`,
  `okf/src/enrichment_agent/tools/bundle_tools.py`
- Reference agent prompts:
  `okf/src/enrichment_agent/prompts/enrichment_instruction.md`,
  `okf/src/enrichment_agent/prompts/web_ingestion_instruction.md`
- Sample recipes:
  `okf/samples/ga4_merch_store/README.md`,
  `okf/samples/stackoverflow/README.md`,
  `okf/samples/crypto_bitcoin/README.md`
- Reference tests:
  `okf/tests/test_document.py`, `okf/tests/test_index.py`,
  `okf/tests/test_bundle_tools.py`, `okf/tests/test_viewer.py`

The X announcement URL was not fetchable from this environment. This plan is
grounded in the Google Cloud post, the spec, and the public reference repo.

Local `dbrain` materials:

- [docs/architecture.md](architecture.md)
- [docs/research-harness.md](research-harness.md)
- [docs/web-brain-research.md](web-brain-research.md)
- [internal/vault](../internal/vault)
- [internal/projection/renderer.go](../internal/projection/renderer.go)
- [internal/retrieval](../internal/retrieval)
- [internal/ask](../internal/ask)
- [internal/brainresearch](../internal/brainresearch)
- [internal/mcpserver](../internal/mcpserver)
- [internal/store](../internal/store)
- [internal/app/root.go](../internal/app/root.go)
- [internal/config/config.go](../internal/config/config.go)

## What OKF Actually Requires

OKF v0.1 is intentionally small. A conformant bundle is a directory tree of
Markdown files where:

- each non-reserved `.md` file is a concept document
- each concept document starts with parseable YAML frontmatter
- each concept frontmatter has a non-empty `type`
- `index.md` and `log.md` are reserved filenames with special meanings
- concept links use ordinary Markdown links
- consumers tolerate unknown fields, unknown types, broken links, missing
  optional fields, and missing indexes

The spec recommends, but does not require:

- `title`
- `description`
- `resource`
- `tags`
- `timestamp`
- structural Markdown headings such as `# Schema`, `# Examples`, and
  `# Citations`

There is one important mismatch in the reference repo: the proof-of-concept
`OKFDocument.validate()` currently requires `type`, `title`, `description`,
and `timestamp`, while the spec's conformance section requires only `type`.
The reference writer also adds producer-side augmentation guards for BigQuery
schemas and citation lists. Those are sensible producer policies, but they are
not baseline OKF conformance rules.
`dbrain` should follow the spec for MVP conformance. A stricter
reference-friendly check that also requires `title`, `description`, and
`timestamp` can be added later, but it should not be the baseline acceptance
rule for the first exporter.

## Current `dbrain` Markdown State

### Existing Strengths

`dbrain` already has much of the machinery OKF needs:

- `internal/vault` renders item, source, entity, and topic notes as Markdown.
- `internal/vault/yaml.go` writes YAML frontmatter.
- `internal/projection` centralizes item/source note refresh from SQLite.
- `internal/store` has item/source rows, source relationships, FTS, user tags,
  pipeline status, raw extracts, summaries, OCR, transcripts, and timestamps.
- `internal/retrieval` has typed evidence payloads and content sections that
  already separate raw text, summaries, OCR, transcript windows, and rendered
  notes.
- `internal/brainresearch` and `internal/ask` already distinguish evidence
  from synthesis, which aligns with the requirement that model answers not
  become authoritative source material.
- MCP and web paths already expose evidence rows with source keys, note paths,
  citations, media refs, and retrieval metadata.

### Existing Gaps

The existing vault is not a clean OKF bundle:

- `writeItemFrontmatter` and `writeSourceFrontmatter` do not write OKF `type`.
- `resource` is not used; `canonical_url` is the nearest equivalent.
- `timestamp` is not used consistently; notes expose fields such as
  `published_at`, `saved_at`, `synced_at`, `extracted_at`, and `summarized_at`.
- `description` is not guaranteed for items and is not a single OKF preview
  sentence.
- `tags` currently mixes local operational labels such as `source/x`,
  `category/...`, and `domain/...`.
- backlinks and topic/entity links use Obsidian `[[path|label]]` syntax.
- raw outbound URLs are listed as URLs, not necessarily concept links.
- `topics/index.md` and `entities/index.md` have frontmatter, which conflicts
  with OKF's reserved-index intent for non-root indexes.
- existing note paths are useful operational references but not necessarily
  good portable concept IDs.

This does not mean the current vault is wrong. It means the vault solves a
different problem: local review and repair. OKF should be an exchangeable
projection over the same underlying evidence.

## Product Decision

Add an OKF bundle projection alongside the vault.

For an explicit root, the generated OKF bundle should be a sibling projection
of the generated vault:

```text
<root>/okf/current/
```

For the default XDG install, the generated OKF bundle should live next to the
default vault under the resolved `DataDir`:

```text
<data-dir>/okf/current/
```

Which normally resolves to:

```text
~/.local/share/dbrain/okf/current/
```

Rationale:

- it avoids corrupting current vault semantics
- it avoids treating every existing `index.md` as OKF infrastructure
- it lets export profiles include or omit raw evidence without changing the
  user's working notes
- it allows wholesale regeneration and atomic replacement of the OKF bundle
  without risking the vault
- it keeps OKF bundles shareable as directories, zip files, or git worktrees
- in explicit-root mode, `vault/` and `okf/` are sibling generated projections
  over `brain.db`; in XDG/default mode, both live as siblings under the
  resolved data directory
- the `current/` subdirectory reserves room for future generated bundle variants
  such as `portable/`, `public/`, or archived snapshots without renaming the
  base `okf/` directory

Configuration can later expose:

```yaml
okf:
  output_dir: /path/to/bundle
  default_profile: private
```

But the MVP can start with CLI flags and no config migration.

When implementation needs a configured path, add an OKF path alongside
`VaultDir` rather than deriving explicit-root output from `DataDir`. The
explicit-root default should be `<root>/okf/current/`; the default/XDG path
should be `<data-dir>/okf/current/`.

## Output Identity And Safety

For generic OKF consumers, the concept identity is the Markdown file path. The
`dbrain_concept_id` field is a producer extension for dbrain-aware consumers;
it is not a substitute for stable, deterministic, collision-safe paths.

Path rules:

- derive output paths from unique database identity, primarily `source_key`, not
  from title or existing vault `note_path` alone
- use friendly path components only after adding a deterministic collision
  guard such as a short hash of the source key
- build a pre-write manifest of every output path, `dbrain_concept_id`, and
  source key before writing any Markdown
- fail closed if two concepts map to the same output path, including
  case-folding and normalization collisions that matter on macOS filesystems
- reject absolute paths, `..`, empty segments, reserved concept filenames
  `index.md` and `log.md`, and overlong path components generated from
  untrusted titles, URLs, repo names, or note paths
- verify every write target stays under the bundle root after cleaning and
  resolving symlinks
- emit bundle-internal links with forward slashes regardless of host OS

Export should read from a single SQLite transaction or snapshot so documents,
relationships, and indexes describe one consistent database view.

Regeneration should be atomic and non-destructive:

- acquire an export lock before reading and writing
- write the new bundle into a staging directory under the OKF output directory
- validate the staged bundle
- atomically swap or rename the staged bundle into `current/`
- keep or restore the previous bundle if staging or validation fails
- never follow symlinks out of the configured OKF directory while writing

Filtered exports from `--limit` or `--source-type` may intentionally omit linked
concepts. The validator should classify those as omitted-by-filter links rather
than accidental broken links.

## Deterministic Output Rules

Use stable-concept export semantics for MVP. Concept frontmatter should contain
content identity, content provenance, and stable dbrain extension fields. Mutable
operational state should not churn frontmatter.

Determinism requirements:

- do not put `last_seen_at`, worker queue status, stale/current flags, transient
  error strings, or export timestamps in per-concept frontmatter
- include operational status in the body only when it explains missing or
  blocked content
- write frontmatter from ordered structs or explicit `yaml.Node` values, not
  Go maps with undefined iteration order
- sort and deduplicate tags, relationships, citations, media refs, and indexes
- query rows with a total deterministic order such as output path then source
  key; never rely on update time alone
- normalize timestamps to UTC RFC3339 with a fixed precision
- escape Markdown link text and link destinations deterministically
- ensure two consecutive exports of the same fixture produce byte-identical
  concept files and indexes

## Concept Taxonomy

OKF does not register central type names. `dbrain` should use descriptive,
stable type strings and keep the original source type as producer-defined
frontmatter.

Recommended type mapping:

| `dbrain` row/view | OKF `type` | Notes |
|---|---|---|
| item row | `Item` | Imported local signal such as X, Apple Notes, Safari tabs, GitHub, YouTube, feed, or manual link. |
| source row | `Source` | Extracted/summarized linked source. |
| entity note | `Entity` | Derived entity view, not raw evidence. |
| topic note | `Topic` | Derived topic view, not raw evidence. |
| bundle metadata | `Bundle Metadata` | Root-level generated metadata concept for the export run. |

Do not use only `Reference` for every source. OKF sample bundles use
`Reference` for referenceable supporting docs, but `dbrain` sources are broader:
articles, GitHub repos, YouTube pages, X articles, feed entries, and other
external evidence. `Source` is more honest, and `dbrain_source_type` preserves
the finer origin distinction.

## Frontmatter Mapping

Every exported concept should include the spec-friendly fields first, followed
by `dbrain` extension fields.

### Common Fields

```yaml
---
type: Source
title: Example title
description: One sentence suitable for indexes and previews.
resource: https://example.com/canonical
tags:
  - source/web
  - domain/example.com
timestamp: "2026-06-14T12:00:00Z"
dbrain_concept_id: "source/src%3Aexample"
dbrain_kind: source
dbrain_source_key: "src:..."
dbrain_source_type: web
dbrain_note_path: sources/web/example.md
---
```

Rules:

- `type`: required. Use the concept taxonomy above.
- `title`: use the stored title; fall back to URL, source key, entity name, or
  topic string.
- `description`: one sentence. Prefer a stored description or first sentence of
  a summary. If unavailable, synthesize a deterministic sentence from metadata,
  not from a model call.
- `resource`: use the canonical external URL when one exists. For concepts with
  no external URL, use a stable local URI such as
  `dbrain://item/<url-escaped-source-key>` or omit `resource` and keep
  `dbrain_source_key`.
- `dbrain_concept_id`: stable producer identity derived from the source key or
  entity/topic key. Generic OKF consumers identify concepts by path; this field
  is a dbrain extension for dbrain-aware lookup, diagnostics, and possible
  future round-trip support.
- `tags`: include normalized user tags and stable operational tags. Avoid
  leaking empty labels.
- `timestamp`: use the last meaningful content timestamp, not the export time.
  Prefer source/content timestamps such as `published_at`, `saved_at`, or a
  stable upstream updated time. Do not use worker status timestamps,
  `last_seen_at`, extraction run time, summary run time, or export time.
- volatile operational state belongs outside frontmatter. Queue states,
  stale/current flags, retryable/blocked status, transient error strings, and
  local provider diagnostics may appear in body sections only when they explain
  missing content, and only in private output.
- unknown extra fields are legal under OKF; consumers should tolerate the
  `dbrain_*` extension fields emitted by this exporter.

Bundle-level metadata such as `okf_version`, `okf_profile`, `exported_at`, and
producer version should live in a root-level `bundle.md` concept, not every
concept frontmatter. Regenerating an unchanged concept should produce identical
bytes.

Recommended deterministic description templates:

| Concept | Fallback description template |
|---|---|
| X item | `Saved X item from <author/title>.` |
| Apple Note item | `Imported Apple Note titled "<title>".` |
| Safari tab item | `Imported Safari tab for <host or title>.` |
| GitHub item | `Imported GitHub signal for <repo/title>.` |
| YouTube item | `Imported YouTube signal for <title>.` |
| Feed/manual item | `Imported item from <source type or domain>.` |
| Source | `Linked source from <domain or source type>.` |
| Entity | `Derived entity from local dbrain references.` |
| Topic | `Derived topic view over local dbrain evidence.` |

### Item Extension Fields

Recommended item fields:

```yaml
dbrain_kind: item
dbrain_concept_id: "item/x%3A204..."
dbrain_source_key: "x:204..."
dbrain_source_type: x_bookmark
dbrain_external_id: "204..."
dbrain_note_path: items/x/2026/204....md
author_handle: example
author_name: Example Person
published_at: "2026-06-01T10:00:00Z"
saved_at: "2026-06-02T10:00:00Z"
```

### Source Extension Fields

Recommended source fields:

```yaml
dbrain_kind: source
dbrain_concept_id: "source/src%3A..."
dbrain_source_key: "src:..."
dbrain_source_type: web
dbrain_note_path: sources/web/example.md
normalized_url: https://example.com/page
domain: example.com
site_name: Example
summary_model: openrouter/...
summary_prompt_version: source-summary-v...
```

### Derived Concept Fields

Entities and topics are useful navigation surfaces, but they are derived. Mark
that plainly:

```yaml
dbrain_kind: topic
dbrain_derived: true
dbrain_evidence_count: 42
```

This keeps research/chat aligned with the rule that source evidence, raw
extracts, notes, transcripts, OCR, and summaries are evidence, while model
answers and generated topic/entity prose are derived synthesis.

## Body Shape

The OKF body should be structural Markdown. Do not simply dump the existing
vault note unchanged.

Recommended item body:

```markdown
# Overview

Short human-readable context for this imported item.

# Source

- Source key: `x:...`
- Source type: `x_bookmark`
- URL: https://...
- Author: ...
- Saved: ...

# Derived Summary

...

# Raw Evidence

## Canonical X Post

...

## OCR / Vision Extract

...

## Media Transcript

...

# Media

- Original item: https://x.com/example/status/204...
- Media source: https://pbs.twimg.com/media/...
- Expanded media URL: https://x.com/example/status/204.../photo/1
- Archived media: https://cdn.example.com/media/...

# Related Concepts

- [Linked source title](../../../sources/web/example.md) - linked source
- [Quoted post](./quoted-child.md) - quoted post

# Citations

[1] [Original URL](https://...)
```

Recommended source body:

```markdown
# Overview

Short summary or description.

# Source

- URL: https://...
- Domain: `example.com`

# Derived Summary

...

# Extracted Text

...

# Referenced By

- [Saved item title](../../items/x/2026/204....md)

# Citations

[1] [Canonical source](https://...)
```

Rules:

- Keep raw imported/extracted text separate from summaries.
- Do not overwrite raw evidence with summaries.
- Put derived summaries under clearly labelled sections.
- Preserve provenance for OCR/transcripts/model-derived text.
- Do not emit operational statuses by default. Include blocked/missing status
  details in body text only when they explain absent raw evidence or summaries.
- For media, include every relevant URL available: the owning item/tweet URL,
  the media source/remote URL, the expanded post-media URL, and the stored
  uploaded/archive URL (`archive_url` when `archive_status = archived`). The
  OKF MVP should link to URLs already tracked in SQLite rather than copying
  media files into the bundle or emitting local filesystem paths. If an
  uploaded URL is unavailable, still include the media status and original
  remote/expanded URLs.
- Include numbered citations under `# Citations`.
- Use ordinary Markdown links for concept relationships.
- Do not emit Obsidian `[[...]]` links in OKF bundles.

## Link Strategy

OKF supports both bundle-root absolute links and file-relative links. The spec
recommends bundle-root links starting with `/`, while the reference enrichment
prompt prefers relative links because they render correctly on GitHub.

`dbrain` should default to relative links for GitHub/plain-file usability.
Bundle-root absolute links can be reconsidered later if another consumer needs
them, but they are not part of the MVP.

Examples:

- item to source:
  `../../../sources/web/example.md`
- source backlink to item:
  `../../items/x/2026/204....md`
- topic to item/source:
  `../items/x/2026/204....md`
- entity to source:
  `../../sources/github/repo.md`

The exporter should compute links from SQLite relationships and the OKF path
manifest, not by scraping rendered vault text.

Missing links are allowed by OKF, but generated `dbrain` links should be
validated so broken internal links are unusual and visible.

## Bundle Layout

Recommended MVP layout:

```text
current/
+-- index.md
+-- bundle.md
+-- items/
|   +-- index.md
|   +-- x/
|   |   +-- index.md
|   |   +-- 2026/
|   |       +-- index.md
|   |       +-- 204....md
|   +-- apple-notes/
|   +-- github/
|   +-- youtube/
|   +-- safari-tabs/
|   +-- feed/
+-- sources/
|   +-- index.md
|   +-- web/
|   +-- github/
|   +-- youtube/
|   +-- x_article/
```

Derived `entities/` and `topics/` directories belong in Phase 3, after the
item/source export contract is stable.

No generated `index.md` should contain frontmatter. Put dbrain producer
metadata in `bundle.md` instead:

```yaml
---
type: Bundle Metadata
title: dbrain OKF Bundle
description: Metadata for a generated dbrain OKF export.
okf_version: "0.1"
okf_profile: private
exported_at: "2026-06-14T18:00:00Z"
dbrain_version: "..."
---
```

`bundle.md` is a dbrain producer metadata concept. It is not the spec's
bundle-version declaration mechanism. The MVP should keep root `index.md` as a
plain reserved index without frontmatter; if a future OKF consumer requires a
different version declaration shape, add it deliberately then.

Each index should group entries by OKF `type` and include the concept
description:

```markdown
# Source

* [Example source](sources/web/example.md) - Short description.

# Item

* [Example post](items/x/2026/204....md) - Short description.
```

Do not generate `log.md` in the MVP. It is an OKF reserved filename, and the
plan does not need a producer-specific history format yet.

## Export Profiles

An OKF bundle can be private/local or shared. Those are not the same product.

Recommended profiles:

| Profile | Default? | Contents |
|---|---:|---|
| `private` | yes | Full local evidence: summaries, extracts, Apple Notes, note text, OCR, transcripts, relationships, local dbrain keys. |
| `portable` | no | Full concept metadata and summaries, but raw long extracts/transcripts may be capped. |
| `public` | no | External URLs, titles, descriptions, selected summaries, no local note paths, no private Apple Notes, no raw transcripts/OCR unless explicitly allowed. |

The MVP should implement `private` only. If `portable` or `public` is requested
in MVP, the command should fail with a clear "profile not implemented" error.
The renderer should still centralize profile decisions rather than scattering
privacy checks across item/source renderers.

Private export includes Apple Notes by default because it is a local/private
bundle profile. Excluding Apple Notes belongs in a later portable/public
profile or explicit source-type filter, not the MVP default.

Private export UX must be loud. Human output should state that the bundle may
include raw Apple Notes, raw extracts, OCR text, media transcripts, local
dbrain keys, archive/upload URLs, and private error diagnostics, and should not
be shared without review. A future `public` profile must be built as an
allowlist of safe fields, not a blacklist of known-private fields.

## Proposed Commands

Add a new top-level command group:

```text
dbrain okf
```

MVP:

```text
dbrain okf export --out <dir>
dbrain okf validate <dir>
```

Useful export flags:

```text
--profile private
--items
--sources
--source-type x_bookmark --source-type github
--limit 100
--include-raw
--max-raw-chars 0
--json
```

Later:

```text
--profile portable
--profile public
--entities
--topics
--conformance reference
--link-style absolute
dbrain okf index <dir>
dbrain okf visualize <dir>
```

`okf export` should be safe to rerun. MVP export should be full-regeneration
only; do not ship `--since` or partial incremental regeneration until
stale-concept deletion semantics are designed. Regeneration should use the
staging, validation, lock, and atomic-swap rules above rather than deleting the
current bundle in place.

Validation should implement spec conformance first. Reference-compatibility
checks are a later producer/CI feature and should use pinned local fixtures,
not the network or a live checkout of Google's draft repository.

## Implementation Plan

### Phase 1: Core Export Package

Add:

```text
internal/okf/
  document.go
  frontmatter.go
  ids.go
  links.go
  render_item.go
  render_source.go
  index.go
  validate.go
  export.go
```

Responsibilities:

- represent OKF documents as typed Go structs
- write frontmatter using the existing `gopkg.in/yaml.v3` dependency, not ad
  hoc string escaping
- derive stable `dbrain_concept_id` values from source keys
- derive stable OKF output paths from unique source keys, not titles alone
- build a pre-write manifest and reject duplicate or unsafe paths before
  writing Markdown
- convert relationships into Markdown links
- render items and sources without mutating current vault renderers
- generate OKF `index.md` files
- validate spec conformance
- return export stats
- shape package data structures so read/search helpers can be added later for
  `dbrain_okf_search` and `dbrain_okf_get`, while keeping MVP export and
  validation as CLI/package behavior

Likely store additions:

- list items for export, ordered by deterministic output path and source key
- list sources for export, ordered by deterministic output path and source key
- fetch source links/backlinks in batch when possible
- read export rows in one transaction or snapshot

Avoid using rendered vault Markdown as the data source. Use SQLite models and
retrieval/content-section helpers so the exporter does not inherit Obsidian
syntax.

Exit criteria:

- `internal/okf` can export item/source fixtures to a temp bundle
- validator passes the bundle
- generated concept files and indexes are deterministic across consecutive
  fixture exports
- duplicate or unsafe output paths fail before any bundle files are written
- bundle `index.md` files have no frontmatter and `bundle.md` carries export
  metadata
- no schema migration required

### Phase 2: CLI Surface

Add `internal/app/okf.go` and register it in
[internal/app/root.go](../internal/app/root.go).

Commands:

- `dbrain okf export`
- `dbrain okf validate`

Human output should show:

```text
Bundle: /path/to/current
Profile: private
Private bundle: includes raw local evidence and archive/upload URLs; review before sharing.
Items written: 123
Sources written: 456
Indexes written: 12
Broken internal links: 0
Omitted-by-filter links: 0
Errors: 0
```

JSON output should expose the same fields.

Exit criteria:

- CLI works against a temp test root
- `--limit` and `--source-type` allow smoke exports
- invalid output paths fail closed with a clear diagnostic
- non-private profiles fail with a clear "not implemented" diagnostic in MVP
- export writes to staging, validates, and atomically swaps into place
- concurrent export attempts are blocked by the export lock

### Phase 3: Derived Views

Add optional entity/topic export after the item/source shape is stable.

Entities:

- use existing entity derivation output
- mark as `dbrain_derived: true`
- link to referenced item/source concepts
- do not imply entity notes are raw evidence

Topics:

- export generated topic maps as `Topic`
- link to seed and related notes
- include graph relationships in Markdown
- keep topic synthesis clearly labelled as derived

Exit criteria:

- topic/entity concepts link to existing exported item/source concepts
- `index.md` files group them cleanly
- validator distinguishes missing optional derived concepts from errors

### Phase 4: Read-Only Consumption Surfaces

After CLI export is stable:

- add web bundle browsing only if local review of generated OKF is useful
- consider embedding or adapting the reference visualizer for a local OKF graph
  view, but do not add a CDN dependency for local/private viewing
- keep OKF export and validation as CLI/package behavior, not MCP tools
- optional MCP consumption tools are acceptable if they are read-only:
  - `dbrain_okf_search`
  - `dbrain_okf_get`

MCP `dbrain_okf_export` and `dbrain_okf_validate` are intentionally out of
scope. Agents can already use the CLI or read the generated bundle from disk
when they need operational OKF artifacts.

## Validation Plan

Tests to add:

- frontmatter serialization uses YAML mappings and preserves unknown fields
- frontmatter serialization uses ordered structs or `yaml.Node`, not unordered
  maps
- concept documents with only `type` pass spec validation
- `index.md` and `log.md` are treated as reserved files
- generated `index.md` files have no frontmatter
- root `bundle.md` carries `okf_version`, `okf_profile`, `exported_at`, and
  producer metadata
- `bundle.md` is treated as dbrain producer metadata, not as required
  per-concept frontmatter
- item export includes `type`, `title`, `description`, `resource`, `tags`,
  `timestamp`, `dbrain_concept_id`, and dbrain extension fields
- item/source frontmatter does not include volatile worker status, transient
  errors, `last_seen_at`, extraction run time, summary run time, or export time
- source export includes raw extracted text separately from derived summary
- X media transcript and OCR text are distinct sections
- media output includes all relevant tracked URLs available: owning item/tweet
  URL, media remote/source URL, expanded post-media URL, and stored
  `archive_url`; OKF output does not expose local media paths
- Markdown links are relative and resolve within the bundle
- link goldens cover both deep item-to-source links and shallower backlinks
- Markdown link escaping covers titles and URLs containing brackets,
  parentheses, backticks, newlines, and non-ASCII characters
- Obsidian wiki links are absent from OKF output
- broken generated links are counted and reported
- filtered-out links are counted separately from accidental broken links
- generated concept files and indexes are byte-identical across two consecutive
  exports of the same fixture
- output path generation rejects absolute paths, `..`, empty segments, reserved
  concept filenames, overlong components, and duplicate paths
- export writes to a staging directory, validates before swap, and preserves the
  previous bundle on validation failure
- concurrent exports cannot interleave because the export lock is held
- later reference-compatibility tests use pinned local fixtures only, with no
  network or live Google repository dependency
- CLI `okf export --limit` works on a temp root
- CLI `okf validate` rejects malformed YAML and missing `type`

For implementation changes, run the standard gates:

```text
task fmt
task lint
task test
task test-ci
```

If CLI behavior is added:

```text
task build
```

`task test-ci` is expected to be green. If it fails while implementing OKF,
diagnose and handle the failure inside the branch unless it is clearly external
infrastructure noise.

## Risks And Decisions

### Risk: Treating The Existing Vault As OKF

Bad plan. It creates `index.md` conflicts, forces Obsidian link changes, and
turns a repairable local projection into a public exchange contract. Keep OKF
separate.

### Risk: Raw Evidence Leakage

The `private` profile should be explicit in command output and includes Apple
Notes by default. A later `public` profile must strip local note paths, Apple
Notes content, private transcripts, archive/upload URLs, transient error
strings, and other local-only fields by default. Build that later public
profile as an allowlist of safe fields, not a blacklist of known-private
fields.

### Risk: Path Collisions Or Path Traversal

Titles, URLs, repo names, and existing note paths are not safe filesystem
identity. Derive OKF paths from unique source keys, sanitize every component,
and fail before writing if a pre-write manifest detects a duplicate or unsafe
path.

### Risk: Non-Atomic Regeneration

Deleting and rewriting `current/` in place can leave a corrupt bundle after a
crash or failed validation. MVP export should use a lock, staging directory,
validation step, and atomic swap so the previous bundle survives failed exports.

### Risk: Spec Draft Drift

OKF v0.1 is draft. Put `okf_version` in the bundle metadata, isolate the
validator, and make the renderer tolerant of future optional fields.

### Risk: Reference Implementation Strictness

The reference validator currently requires more than the spec. MVP validation
should enforce the spec. A later reference-compatibility mode can check stricter
title/description/timestamp expectations against pinned local fixtures, but it
should not make normal OKF export depend on a moving draft implementation.

### Risk: Large Extracted Text

Full extracted source text can make huge Markdown files. MVP private export can
include it, but the renderer should already support `--max-raw-chars` so
portable/public profiles do not need a rewrite.

### Risk: Incremental Export Semantics

Incremental export sounds attractive but creates deletion/staleness questions.
MVP should use full regeneration only. Add `--since` later only with an
explicit stale-concept deletion strategy.

### Risk: Model-Derived Topic/Entity Prose

Topic and entity notes are useful, but they are derived. Mark them derived and
do not let exported topic prose become evidence in later research without its
cited item/source support.

## Acceptance Criteria For MVP

MVP is done when:

- `dbrain okf export --out <tmpdir>` writes a valid private OKF bundle with
  items and sources.
- non-private profiles fail clearly as unimplemented in MVP.
- Every concept document has parseable YAML frontmatter and non-empty `type`.
- `title`, `description`, `resource`, `tags`, and `timestamp` are populated
  whenever available.
- Every item/source concept has a stable output path and a stable
  `dbrain_concept_id` extension field.
- Pre-write manifest validation rejects duplicate, unsafe, reserved, or
  out-of-root output paths before writing Markdown.
- Per-concept frontmatter excludes volatile worker status, transient errors,
  run timestamps, and export timestamps.
- Two consecutive exports of the same unchanged fixture are byte-identical.
- Export uses a lock, staging directory, validation step, and atomic swap.
- Raw evidence and derived summaries are separate sections.
- Media references include all relevant tracked URLs available, including the
  owning item/tweet URL, media remote/source URL, expanded post-media URL, and
  uploaded/archive URL, and never expose local media paths.
- Item/source relationships are expressed as standard Markdown links.
- `index.md` files are generated at every directory level without frontmatter.
- `bundle.md` carries bundle metadata that would otherwise churn every concept.
- `dbrain okf validate <tmpdir>` reports conformance, concept counts, index
  counts, broken-link counts, and omitted-by-filter link counts.
- Human export output clearly labels `private` bundles as containing raw local
  evidence and URLs that require review before sharing.
- Existing vault rendering is unchanged.
- Tests cover at least one item-source linked fixture and one raw/derived
  evidence fixture, plus one media fixture with original, remote/expanded, and
  archived URLs.

## Recommended First Implementation Slice

Do this first:

1. Add `internal/okf` with document/frontmatter/path/link/index/validate
   helpers.
2. Implement path derivation, sanitization, pre-write manifest validation, and
   deterministic ordering.
3. Implement source export only.
4. Add golden tests for one source with summary and extracted text, including a
   two-export byte-identical assertion.
5. Implement item export and item-to-source links.
6. Add golden tests for one item with a linked source and one media fixture with
   original, remote/expanded, and archived URLs.
7. Add staged export with lock, validation, and atomic swap.
8. Add `dbrain okf export --limit N --out <dir>`.
9. Add `dbrain okf validate <dir>`.
10. Run `task fmt`, `task lint`, `task test`, `task test-ci`, and `task build`.

Do not start with OKF import, MCP search/get tools, web visualization, or schema
migrations. Export is the lowest-risk path because it proves the concept
mapping without changing the authoritative database model.
