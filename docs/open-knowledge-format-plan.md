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
`dbrain` should follow the spec for conformance and optionally provide a
stricter "reference-friendly" profile that also requires `title`,
`description`, and `timestamp`.

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

The default generated bundle should live outside the existing Obsidian vault:

```text
<data-dir>/okf/current/
```

For an explicit root, that would be:

```text
<root>/data/okf/current/
```

For the default install, that would be:

```text
~/.local/share/dbrain/okf/current/
```

Rationale:

- it avoids corrupting current vault semantics
- it avoids treating every existing `index.md` as OKF infrastructure
- it lets export profiles include or omit raw evidence without changing the
  user's working notes
- it allows wholesale regeneration and deletion of the OKF bundle without
  risking the vault
- it keeps OKF bundles shareable as directories, zip files, or git worktrees

Configuration can later expose:

```yaml
okf:
  output_dir: /path/to/bundle
  default_profile: private
```

But the MVP can start with CLI flags and no config migration.

## Concept Taxonomy

OKF does not register central type names. `dbrain` should use descriptive,
stable type strings and keep the original source type as producer-defined
frontmatter.

Recommended type mapping:

| `dbrain` row/view | OKF `type` | Notes |
|---|---|---|
| item from X bookmark | `dbrain X Item` | Imported saved/bookmarked post signal. |
| quoted X child | `dbrain X Item` | First-class item if present in SQLite. |
| Apple Note item | `dbrain Apple Note` | User-authored local memory. |
| Safari tab item | `dbrain Safari Tab` | Imported tab signal, append-only. |
| GitHub star item | `dbrain GitHub Item` | Star/watch signal. |
| YouTube item | `dbrain YouTube Item` | Watch-later/history/liked signal. |
| feed/manual item | `dbrain Item` | Generic imported item. |
| source row | `dbrain Source` | Extracted/summarized linked source. |
| entity note | `dbrain Entity` | Derived entity view, not raw evidence. |
| topic note | `dbrain Topic` | Derived topic view, not raw evidence. |
| media asset concept, later | `dbrain Media Asset` | Optional, not MVP. |

Do not use only `Reference` for every source. OKF sample bundles use
`Reference` for referenceable supporting docs, but `dbrain` sources are broader:
articles, GitHub repos, YouTube pages, X articles, feed entries, and other
external evidence. `dbrain Source` is more honest.

## Frontmatter Mapping

Every exported concept should include the spec-friendly fields first, followed
by `dbrain` extension fields.

### Common Fields

```yaml
---
type: dbrain Source
title: Example title
description: One sentence suitable for indexes and previews.
resource: https://example.com/canonical
tags:
  - source/web
  - domain/example.com
timestamp: "2026-06-14T12:00:00Z"
dbrain_kind: source
dbrain_source_key: "src:..."
dbrain_source_type: web
dbrain_note_path: sources/web/example.md
exported_at: "2026-06-14T18:00:00Z"
okf_profile: private
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
- `tags`: include normalized user tags and stable operational tags. Avoid
  leaking empty labels.
- `timestamp`: use the last meaningful content timestamp, not the export time.
  Use `updated_at`, `summarized_at`, `extracted_at`, `last_seen_at`,
  `saved_at`, or `published_at` depending on concept type.
- `exported_at`: producer-defined field for the export run time.
- unknown extra fields are legal under OKF; consumers should tolerate the
  `dbrain_*` extension fields emitted by this exporter.

### Item Extension Fields

Recommended item fields:

```yaml
dbrain_kind: item
dbrain_source_key: "x:204..."
dbrain_source_type: x_bookmark
dbrain_external_id: "204..."
dbrain_note_path: items/x/2026/204....md
author_handle: example
author_name: Example Person
published_at: "2026-06-01T10:00:00Z"
saved_at: "2026-06-02T10:00:00Z"
last_seen_at: "2026-06-14T10:00:00Z"
summary_status: current
ocr_status: current
x_media_transcript_status: current
```

### Source Extension Fields

Recommended source fields:

```yaml
dbrain_kind: source
dbrain_source_key: "src:..."
dbrain_source_type: web
dbrain_note_path: sources/web/example.md
normalized_url: https://example.com/page
domain: example.com
site_name: Example
extract_status: current
summary_status: current
extracted_at: "2026-06-12T10:00:00Z"
summarized_at: "2026-06-12T10:03:00Z"
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

# Related Concepts

- [Linked source title](../../sources/web/example.md) - linked source
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
- Extract status: `current`
- Summary status: `current`

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
- Include numbered citations under `# Citations`.
- Use ordinary Markdown links for concept relationships.
- Do not emit Obsidian `[[...]]` links in OKF bundles.

## Link Strategy

OKF supports both bundle-root absolute links and file-relative links. The spec
recommends bundle-root links starting with `/`, while the reference enrichment
prompt prefers relative links because they render correctly on GitHub.

`dbrain` should default to relative links for GitHub/plain-file usability, with
an optional `--link-style absolute` flag later if another consumer wants
bundle-root paths.

Examples:

- item to source:
  `../../sources/web/example.md`
- source backlink to item:
  `../../items/x/2026/204....md`
- topic to item/source:
  `../items/x/2026/204....md`
- entity to source:
  `../../sources/github/repo.md`

The exporter should compute links from SQLite relationships and note paths,
not by scraping rendered vault text.

Missing links are allowed by OKF, but generated `dbrain` links should be
validated so broken internal links are unusual and visible.

## Bundle Layout

Recommended MVP layout:

```text
current/
+-- index.md
+-- log.md
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
+-- entities/
|   +-- index.md
|   +-- person/
|   +-- org/
|   +-- project/
|   +-- site/
+-- topics/
    +-- index.md
    +-- ...
```

Root `index.md` should declare the OKF version if we decide to use the spec's
version mechanism:

```yaml
---
okf_version: "0.1"
---
```

Non-root `index.md` files should not have frontmatter.

Each index should group entries by OKF `type` and include the concept
description:

```markdown
# dbrain Source

* [Example source](sources/web/example.md) - Short description.

# dbrain X Item

* [Example post](items/x/2026/204....md) - Short description.
```

`log.md` should be optional in the MVP. If generated, it should record export
runs and major scope changes, not every row.

## Export Profiles

An OKF bundle can be private/local or shared. Those are not the same product.

Recommended profiles:

| Profile | Default? | Contents |
|---|---:|---|
| `private` | yes | Full local evidence: summaries, extracts, note text, OCR, transcripts, relationships, local dbrain keys. |
| `portable` | no | Full concept metadata and summaries, but raw long extracts/transcripts may be capped. |
| `public` | no | External URLs, titles, descriptions, selected summaries, no local note paths, no private Apple Notes, no raw transcripts/OCR unless explicitly allowed. |

The MVP should implement `private` only, but the renderer should be designed so
profile decisions are centralized rather than scattered across item/source
renderers.

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
--profile private|portable|public
--items
--sources
--entities
--topics
--source-type x_bookmark --source-type github
--since 2026-06-01T00:00:00Z
--limit 100
--include-raw
--max-raw-chars 0
--link-style relative|absolute
--json
```

Later:

```text
dbrain okf index <dir>
dbrain okf visualize <dir>
```

`okf export` should be safe to rerun. It may delete and regenerate the target
bundle only when the target is under the configured OKF output directory or
when `--replace` is explicitly passed. Avoid destructive behavior for arbitrary
paths.

## Implementation Plan

### Phase 0: Sample Current Output

Goal: prove the mapping with a tiny bundle before building broad machinery.

Tasks:

- Add a private dev-only script or temporary test fixture that renders:
  - one item with a linked source
  - one source with a backlink
  - one item with OCR/transcript content if fixtures are easy
- Compare the output against OKF spec conformance.
- Check that the generated bundle renders acceptably on GitHub/plain Markdown.
- Decide whether root `index.md` should include only `okf_version` or no
  frontmatter until the draft spec clarifies the reserved-index/version tension.

Exit criteria:

- a small fixture bundle has parseable frontmatter and a correct link graph
- raw evidence and derived summaries are visibly separate
- no existing vault output is changed

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
- write frontmatter using `gopkg.in/yaml.v3`, not ad hoc string escaping
- derive stable concept IDs from existing note paths or source keys
- convert relationships into Markdown links
- render items and sources without mutating current vault renderers
- generate OKF `index.md` files
- validate spec conformance
- return export stats

Likely store additions:

- list items for export, ordered by note path or update time
- list sources for export, ordered by note path or update time
- fetch source links/backlinks in batch when possible

Avoid using rendered vault Markdown as the data source. Use SQLite models and
retrieval/content-section helpers so the exporter does not inherit Obsidian
syntax.

Exit criteria:

- `internal/okf` can export item/source fixtures to a temp bundle
- validator passes the bundle
- generated indexes are deterministic
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
Items written: 123
Sources written: 456
Indexes written: 12
Broken internal links: 0
Errors: 0
```

JSON output should expose the same fields.

Exit criteria:

- CLI works against a temp test root
- `--limit` and `--source-type` allow smoke exports
- invalid output paths fail closed with a clear diagnostic

### Phase 3: Derived Views

Add optional entity/topic export after the item/source shape is stable.

Entities:

- use existing entity derivation output
- mark as `dbrain_derived: true`
- link to referenced item/source concepts
- do not imply entity notes are raw evidence

Topics:

- export generated topic maps as `dbrain Topic`
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
- concept documents with only `type` pass spec validation
- optional strict profile requires `title`, `description`, and `timestamp`
- `index.md` and `log.md` are treated as reserved files
- non-root `index.md` files have no frontmatter
- root `index.md` version declaration behavior is covered once chosen
- item export includes `type`, `title`, `description`, `resource`, `tags`,
  `timestamp`, and dbrain extension fields
- source export includes raw extracted text separately from derived summary
- X media transcript and OCR text are distinct sections
- Markdown links are relative and resolve within the bundle
- Obsidian wiki links are absent from OKF output
- broken generated links are counted and reported
- generated indexes are deterministic
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

## Risks And Decisions

### Risk: Treating The Existing Vault As OKF

Bad plan. It creates `index.md` conflicts, forces Obsidian link changes, and
turns a repairable local projection into a public exchange contract. Keep OKF
separate.

### Risk: Raw Evidence Leakage

The `private` profile should be explicit in command output. A later `public`
profile must strip local note paths, Apple Notes content, private transcripts,
and other local-only fields by default.

### Risk: Spec Draft Drift

OKF v0.1 is draft. Put `okf_version` in the bundle metadata, isolate the
validator, and make the renderer tolerant of future optional fields.

### Risk: Reference Implementation Strictness

The reference validator currently requires more than the spec. `dbrain` should
offer both:

- `--profile spec`: only spec conformance
- `--profile reference`: stricter title/description/timestamp checks

### Risk: Large Extracted Text

Full extracted source text can make huge Markdown files. MVP private export can
include it, but the renderer should already support `--max-raw-chars` so
portable/public profiles do not need a rewrite.

### Risk: Model-Derived Topic/Entity Prose

Topic and entity notes are useful, but they are derived. Mark them derived and
do not let exported topic prose become evidence in later research without its
cited item/source support.

## Acceptance Criteria For MVP

MVP is done when:

- `dbrain okf export --out <tmpdir>` writes a valid private OKF bundle with
  items and sources.
- Every concept document has parseable YAML frontmatter and non-empty `type`.
- `title`, `description`, `resource`, `tags`, and `timestamp` are populated
  whenever available.
- Raw evidence and derived summaries are separate sections.
- Item/source relationships are expressed as standard Markdown links.
- `index.md` files are generated at every directory level without non-root
  frontmatter.
- `dbrain okf validate <tmpdir>` reports conformance, concept counts, index
  counts, and broken-link counts.
- Existing vault rendering is unchanged.
- Tests cover at least one item-source linked fixture and one raw/derived
  evidence fixture.

## Recommended First Implementation Slice

Do this first:

1. Add `internal/okf` with document/frontmatter/link/index/validate helpers.
2. Implement source export only.
3. Add a golden test for one source with summary and extracted text.
4. Implement item export and item-to-source links.
5. Add a golden test for one item with a linked source.
6. Add `dbrain okf export --limit N --out <dir>`.
7. Add `dbrain okf validate <dir>`.
8. Run `task fmt`, `task lint`, `task test`, `task test-ci`, and `task build`.

Do not start with OKF import, MCP search/get tools, web visualization, or schema
migrations. Export is the lowest-risk path because it proves the concept
mapping without changing the authoritative database model.
