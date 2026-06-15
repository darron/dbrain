# Web Brain Research And Chat Proposal

Status: proposal
Date: 2026-05-01

## Summary

`dbrain` already has three related surfaces:

- evidence-only question code currently exposed as `ask`
- the MCP server, which exposes richer agent-oriented research tools
- local model-backed summarization through `summarize`/Ollama/OpenRouter

The current `ask` path is useful for seeing matching evidence, but it does not
behave like an agent using MCP. It does not plan follow-up searches, inspect
selected evidence, expand related notes, or synthesize a grounded answer. That
is why asking Codex with the dbrain MCP skill can produce better answers than
the web form.

The goal of this proposal is to add a local web research/chat surface that
duplicates the useful parts of an MCP-capable agent while staying inside the
single `dbrain` binary. MCP should remain the external-agent protocol. The web
UI should reuse the same retrieval and research primitives directly from Go
rather than launching an MCP client inside the server.

Recommended path:

1. Add a first-class web "Research" mode backed by the existing MCP research
   pack behavior.
2. Add per-question local-model synthesis over that research pack, enabled by
   default but easy to turn off.
3. Remove the old `ask` CLI/API/UI surfaces rather than preserving aliases.
4. Later add a small server-side agent loop that can call dbrain research tools
   iteratively before answering.

This should make private, local questions like "what do I know about X?" usable
from the browser without requiring a separate agent application.

## Current State

### Evidence-Only Ask

`POST /api/ask` calls `ask.Run` with `RetrieveOnly: true`.

That means:

- it returns ranked evidence rows
- it includes source/item summaries and excerpts
- it can include related evidence when requested
- it does not call a model
- it does not produce a synthesized answer
- it does not expose the richer research-pack metadata already available to MCP

This was the right first web slice because it avoided competing with source
enrichment for local model capacity. It is now too limited as the main
question-answering interface and should be replaced, not kept as a compatibility
path.

### MCP

The MCP surface already has the shape we want agents to use:

- `dbrain_research_pack`
- `dbrain_search`
- `dbrain_get`
- `dbrain_get_many`
- `dbrain_related`
- `dbrain_ask`
- `dbrain_topic_map`
- `dbrain_topic_brief`
- entity and stats tools
- `brain_research` prompt guidance

`dbrain_research_pack` is the most important existing primitive. It bundles:

- query terms and tag aliases
- evidence from items and sources
- exact tag evidence
- corpus coverage
- top tags under `coverage.top_user_tags`
- related evidence inside `evidence[]` with relationship metadata
- optional topic brief
- suggested next steps

This is closer to the evidence package an agent needs before answering a broad
question.

### Local Models

`dbrain` already knows how to call model providers through the summarize CLI
path. For this feature, the locally used Qwen 3.6 model should be the first
recommended target for private browser use. Hosted providers can remain
configurable for catch-up or higher-quality synthesis, but should not be
required.

A dedicated Ollama `Modelfile` is acceptable if it materially improves answer
discipline. The Modelfile should focus on durable system guidance: answer only
from provided evidence, cite source keys and note paths, distinguish user notes
from third-party sources, and call out weak evidence.

## Product Goal

Build a browser experience that answers questions from the local brain with the
same retrieval discipline expected from an MCP-capable agent:

- search broadly across items, sources, Apple Notes, X media transcripts, OCR,
  GitHub stars, YouTube items, and linked sources
- surface evidence quality and coverage before synthesis
- cite source keys and note paths
- allow follow-up questions in the same browser session
- keep all retrieval and local synthesis inside the `dbrain` binary
- preserve MCP for external agents rather than requiring the user to install one

The desired user experience is:

1. Ask a natural-language question in the web UI.
2. See a research pack: evidence, exact tag hits, top tags under
   `coverage.top_user_tags`, source-type coverage, and suggested follow-ups.
3. Optionally synthesize an answer locally.
4. Continue with follow-up questions that reuse the previous research context.

## Non-Goals

- Do not require an external MCP client for the web UI.
- Do not require the Ollama desktop app or any specific chat app.
- Do not make Ollama itself responsible for MCP tool use.
- Do not add a SaaS component.
- Do not add a long-running separate agent service in v1.
- Do not make hosted model calls by default for private brain chat.
- Do not remove the existing search/tag browsing UI.
- Do not turn the first version into a fully autonomous agent that can mutate
  the brain.
- Do not add write-back or upstream mutations through this interface.

## Key Decision

The web interface should not "use MCP" internally as a protocol hop.

Instead, extract or reuse the same underlying research functions that MCP calls.
The desired dependency direction is:

```text
web UI/API
    -> internal brain research package
MCP server
    -> internal brain research package
CLI research
    -> internal brain research package
```

This avoids a weird internal MCP client/server loop while still keeping the web
behavior aligned with MCP agents.

Today, much of this logic lives under `internal/mcpserver/research.go`. Move it
into a neutral package such as `internal/brainresearch` so MCP, web, and CLI
all share the same implementation. The safe migration sequence is:

1. Create `internal/brainresearch`.
2. Move reusable research-pack behavior into it.
3. Make `internal/mcpserver` delegate to it and keep MCP tests green.
4. Add web and CLI callers on top of the shared package.
5. Delete the old `ask` surfaces once replacement tests are in place.

Long term, MCP should be a transport and tool-contract layer, not the owner of
core research behavior.

## Proposed Modes

### Search

Existing keyword/tag search should remain.

Purpose:

- fast lookup
- debugging retrieval
- browsing exact hits
- editing/tagging workflows

Search is not the main conversational interface.

### Research

New default question mode. Research should replace "Ask" as the user-facing
concept in both the web UI and CLI.

The existing `ask` behavior should be removed in the coordinated Phase 1b
replacement after its useful retrieval logic is migrated. Do not keep
`dbrain ask`, `/api/ask`, the web Ask tab, or `dbrain_ask` as aliases or
wrappers.

Input:

- question
- optional source-type filters
- limit
- include related evidence
- include topic brief
- max chars per document

Output:

- query plan
- evidence
- exact tag evidence
- coverage
- top tags under `coverage.top_user_tags`
- related evidence inside `evidence[]` through `related_to` and `relationship`
  metadata
- topic brief when useful
- suggested next steps
- optional synthesized answer

This should become the natural replacement for the current evidence-only ask
panel.

### Chat

Later interactive mode.

The chat should keep local session state in browser `sessionStorage` for now,
not as durable brain content.

Each turn should be able to:

- reuse prior evidence
- run a new research pack
- inspect specific evidence with `get`
- expand related evidence
- synthesize a cited answer

The first chat implementation can be shallow: one research call plus one model
answer per user turn. A later version can add a bounded tool loop.

## Architecture

### Backend Package

Add a neutral research package, likely:

```text
internal/brainresearch
```

Responsibilities:

- build research packs
- expose query plans and coverage
- gather exact tag evidence
- gather topic briefs
- format evidence for synthesis
- provide model prompts for grounded answers
- expose typed domain structs, not MCP aliases or `map[string]interface{}`
- return semantic next-step actions such as `inspect_top_evidence`,
  `expand_related`, and `open_lookup`

Existing `internal/mcpserver` should call this package. The web server should
also call this package.

MCP-specific behavior should stay in `internal/mcpserver`, including:

- text rendering such as `formatResearchPack`
- mapping semantic next-step actions to MCP tool names like `dbrain_get_many`
- MCP output schemas and tool descriptions

### Web API

Add:

```http
POST /api/research
```

Request:

```json
{
  "question": "What do I know about agent memory?",
  "limit": 8,
  "source_types": ["apple_note", "web", "github"],
  "include_related": true,
  "related_limit": 2,
  "include_topic_brief": true,
  "max_chars_per_doc": 4000
}
```

Use the existing MCP field names as the canonical schema unless there is a
specific product reason to diverge. `max_chars_per_doc` caps any single
evidence document in the research pack. If omitted, the server default should
preserve the current MCP behavior, currently `max_chars_per_doc=700`, unless
config overrides it. The web UI may send a larger explicit value when it wants
more context. The Research web UI should always send `max_chars_per_doc=4000`
for evidence cards so browser users do not accidentally inherit the compact MCP
default.

Response:

```json
{
  "schema_version": "research_pack.v1",
  "question": "What do I know about agent memory?",
  "mode": "evidence_only",
  "query_plan": {},
  "coverage": {},
  "evidence": [],
  "exact_tag_evidence": [],
  "next_steps": []
}
```

`mode` should preserve current MCP semantics: `evidence_only` or
`topic_brief_and_evidence`. Related evidence should stay in the single ranked
`evidence[]` lane with `related_to` and `relationship` metadata rather than a
separate top-level `related_evidence[]` field. Top tags should stay under
`coverage.top_user_tags`. `topic_brief` should become a typed object before the
logic leaves `internal/mcpserver`. The research-pack response should carry
`schema_version`; `prompt_version` belongs to synthesis events because the
retrieval-only pack does not run a model prompt.

`topic_brief` should be omitted when absent. When present, it should be a typed
object, for example:

```json
{
  "topic": "agent memory",
  "source_types": ["apple_note", "web"],
  "seed_limit": 8,
  "related_limit": 2,
  "summary": "Short topic overview from the local graph.",
  "pivots": [],
  "entities": [],
  "nodes": [],
  "edges": []
}
```

`next_steps[]` should contain semantic actions, not MCP tool names:

```json
[
  {
    "action": "inspect_top_evidence",
    "label": "Inspect strongest evidence",
    "params": {
      "lookups": ["src:example"],
      "content_mode": "evidence",
      "query": "agent memory"
    }
  },
  {
    "action": "expand_related",
    "label": "Expand related evidence",
    "params": {
      "lookup": "src:example"
    }
  }
]
```

HTTP semantics:

- `200`: valid request, including no-evidence responses with empty evidence
  arrays and populated next-step suggestions
- `400`: invalid JSON or empty question
- `422`: invalid filters or options
- `500`: store/internal failure
- error bodies should be structured, not just `{"error":"..."}`

### Streaming API

Phase 1 should implement `POST /api/research` as normal JSON and may return
evidence-only results.

Phase 2 should add a separate streaming endpoint instead of overloading the JSON
response:

```http
POST /api/research/synthesize
```

Request:

```json
{
  "question": "What do I know about agent memory?",
  "research_pack": {},
  "model": "",
  "max_evidence_chars": 24000
}
```

`model: ""` means "use the configured default model". The final event should
echo the resolved model name so the UI can show what actually answered.
`research_pack` should be the full response body returned by `POST
/api/research`, not only the evidence array or a client-rebuilt subset.
`max_evidence_chars` caps the total evidence text sent to synthesis so large
research packs do not exceed the local model context window. If omitted, the
server default should be `max_evidence_chars=24000`, unless config overrides
it. The endpoint should enforce a hard request-body size limit. The pack should
include `schema_version`, but `pack_hash` is out of scope for v1. The v1
endpoint should remain stateless and should not imply server-issued pack
handles.

Response format: `text/event-stream`.

Phase 2 should use a pragmatic final-answer SSE shape unless provider-native
streaming is added first. The endpoint still uses SSE for progress, heartbeats,
and a future-compatible event contract, but it should not claim token streaming
until the model adapter can emit tokens incrementally.

Events:

- `start`: synthesis schema version, resolved model, prompt version, evidence
  budget, truncation metadata available before generation, and any upfront
  warnings
- `heartbeat`: comment or event frame every 5 seconds during model warmup or
  long synthesis, unless config overrides the interval
- `answer`: final answer text; emitted once in the request/response model path
- `token`: incremental answer text only when provider-native streaming exists
- `citation`: optional structured citation metadata if emitted separately from
  text
- `done`: final `answer_status`, `answer_warnings`, truncation metadata,
  citations, token/char counts, `prompt_version`, and model/tool provenance
- `error`: terminal synthesis error when answer generation cannot continue

Successful final-answer sequence:

```text
start -> heartbeat* -> answer -> citation* -> done
```

Provider-native token sequence, once supported:

```text
start -> heartbeat* -> token* -> citation* -> done
```

Failure after the stream starts:

```text
start -> heartbeat* -> error
```

`answer` and `token` are mutually exclusive within one stream. A stream that
emits `error` is terminal and should not also emit `done`.

`answer_status` values across `done`, `error`, and structured pre-stream error
payloads should be explicit: `ok`, `ok_truncated`, `no_evidence`,
`unavailable`, or `error`. Normal successful synthesis emits `done` with `ok`
or `ok_truncated`. Explicit no-evidence explanations emit `done` with
`no_evidence`. Model unavailable before the stream starts returns HTTP `503`
with `answer_status=unavailable`; model failure after stream start emits
`error` with `answer_status=error`.

If `max_evidence_chars` is exceeded, include the highest-ranked evidence first,
drop or trim lower-ranked evidence, return `answer_status=ok_truncated`, and
include an `evidence_truncated` warning. Initial `answer_warnings` values should
include `evidence_truncated`, `model_unavailable`, `model_error`, and
`no_evidence`.

Machine-readable citations should be emitted as `citation` events and repeated
in `done.citations`; `done.citations` is the UI source of truth. Inline
citations in `answer` text are for readability.

The browser flow can call `/api/research` first to render evidence immediately,
then call `/api/research/synthesize` when synthesis is enabled. This keeps the
research-pack contract simple and makes streaming synthesis explicit.

The client should use `fetch` with a streaming response reader for POST + SSE,
not browser `EventSource`. If the client disconnects, request context
cancellation must reach the model call. Errors before the first byte should
return normal HTTP `4xx`/`5xx`; errors after the stream starts should emit an
`error` event and close. Add a small synthesis concurrency cap and tests for
client cancellation, context cancellation, slow readers, and goroutine/temp-file
cleanup.

Synthesis HTTP semantics before the first stream byte:

- `400`: invalid body or invalid research pack
- `413`: request body exceeds the configured full-pack POST limit
- `503`: configured model unavailable
- `500`: internal synthesis setup failure
- error bodies should use the same structured envelope as `/api/research`,
  including `answer_status` when relevant

Remove `/api/ask` in the same Phase 1b change that ships the Research UI/CLI
replacement and updates `CHANGELOG.md`, `README.md`,
`skills/dbrain-mcp/SKILL.md`, MCP guidance, and CLI help. The UI should use
`/api/research` for broad natural-language questions.

The CLI should add a user-facing `dbrain research` command and remove
`dbrain ask` in the same implementation. Do not preserve aliases or wrappers;
instead, make `CHANGELOG.md`, `README.md`, `skills/dbrain-mcp/SKILL.md`, MCP
resource guidance, and CLI help updates release blockers for Phase 1b.

### Synthesis

The synthesis prompt should be stricter than a generic summary prompt:

- answer only from provided evidence
- cite each material claim with `source_key`
- include note paths in a Sources section
- call out weak or missing evidence
- distinguish user-authored notes from linked third-party sources
- distinguish raw notes, summaries, transcripts, OCR, and archived web extracts
- do not add external background unless explicitly requested

Local synthesis should use the existing configured summary/model path.

Recommended default behavior:

- synthesis enabled by default in the web UI and CLI
- a visible per-question toggle to turn synthesis off
- retrieval-only mode remains available for debugging and fast evidence review
- enforce a total synthesis evidence budget with `max_evidence_chars` or an
  equivalent token budget; prefer highest-ranked evidence and make truncation
  visible
- build the synthesis input deterministically: always include question, query
  plan, and coverage summary; include primary `evidence[]` in rank order;
  reserve `exact_tag_reserved_chars=2000` by default for at least one
  `exact_tag_evidence[]` example when present; include `topic_brief.summary`
  only when at least `topic_brief_min_remaining_chars=2000` remain, capped by
  `topic_brief_summary_max_chars=2000`; include related evidence rows last,
  identified by non-empty `related_to` or `relationship`; partially trim only
  the bottom-most included item
- return truncation metadata: `evidence_budget_chars`, `evidence_chars_used`,
  `dropped_source_keys`, `partially_trimmed_source_key`, and warnings; emit
  known metadata in `start` and repeat final metadata in `done`
- keep `exact_tag_reserved_chars`, `topic_brief_min_remaining_chars`, and
  `topic_brief_summary_max_chars` as server-side synthesis budget settings
  with the defaults listed above; do not expose them in the v1 request schema
  unless practical testing shows per-request tuning is needed
- if the configured local model is unavailable, cold-starting, or errors, keep
  the already-rendered research pack visible and report
  `answer_status=unavailable` or `answer_status=error` through the synthesis
  error contract rather than failing evidence retrieval
- never silently fall back from local synthesis to hosted inference; hosted
  synthesis requires explicit user/config opt-in and must report provider/model
  provenance
- emit synthesis progress and final answers through `/api/research/synthesize`
- remember the user's preference in browser state later if desired

If a dedicated Ollama Modelfile improves answer discipline, keep it in a
contrib-style path such as `contrib/Modelfile.qwen-research`. It should not
affect `task build`; it is a local model setup aid, not a Go build dependency.

### Tool Loop

A later chat loop can expose internal tools to the model with a small schema:

- `research_pack(question, filters)`
- `search(query, filters)`
- `get(lookup, query, content_mode)`
- `get_many(lookups, query, content_mode)`
- `related(lookup)`
- `topic_brief(topic)`

This does not need to speak MCP internally. The tool schemas can mirror MCP so
behavior remains consistent.

Bounds:

- max tool calls per turn
- max evidence chars per turn
- timeout per turn
- read-only tools only
- no mutation tools
- no hidden long-running background jobs

## UI Shape

The home page should keep fast search available and replace the current Ask
surface with Research:

- `Search`: keyword/tag lookup
- `Research`: evidence pack plus optional answer
- `Chat`: later follow-up interface

The existing "Ask" tab should be removed in favor of "Research".

Research result layout:

- answer card when synthesized, including visible SSE progress while generating
- query plan card
- recall/coverage warning card
- exact tag matches
- top tags from `coverage.top_user_tags`
- evidence list
- topic brief pivots when available
- next-step buttons that run follow-up searches or open evidence

No-results layout:

- clear "no evidence found" state
- the query plan and terms that were tried
- active filters that may have narrowed results
- suggested reformulations or next steps
- no model synthesis call unless the user explicitly asks for a no-evidence
  explanation
- a visible "Explain this result" action that calls synthesis with the empty
  research pack and asks for a grounded explanation of what was searched and
  how to broaden the query

Evidence rows should make provenance obvious:

- item vs source
- source type
- user tags
- note path
- source key
- whether text is summary, raw excerpt, transcript, OCR, or attachment text
- `summary_kind`, `excerpt_kind`, or tagged `sections[]` provenance so the
  synthesis prompt can distinguish evidence types truthfully

## Privacy And Safety

- Read-only brain access.
- No write-back to imported sources.
- No hosted model calls unless explicitly configured.
- Show the model/provider used for synthesis.
- Include a visible warning when synthesis is disabled or unavailable.
- Store Phase 3 chat context in browser `sessionStorage`, not server state or
  the dbrain DB.
- Do not store chat transcripts as durable brain content by default.
- Follow-up context should reuse prior packs/evidence/lookups, not prior model
  answers as evidence.
- If transcripts are later stored durably, treat them as local brain content
  with an explicit opt-in.

## Implementation Phases

### Phase 1a: Shared Research Package And API

- Create `internal/brainresearch`.
- Move reusable research-pack logic out of `internal/mcpserver/research.go`.
- Make MCP call `internal/brainresearch` and keep existing MCP behavior green.
- Add `POST /api/research` without changing the web UI yet.
- Centralize or freeze query normalization shared by `ask.Hints()` and current
  research term/topic normalization before extraction.
- Fix exact-tag examples so source-tag-only matches can surface as evidence, not
  only item-tag matches.
- Add handler tests for query plans, evidence, exact tag evidence, related
  evidence, source-type filters, coverage, and no-results responses.

### Phase 1b: Web Research UI And Ask Removal

- Replace the web Ask tab with Research.
- Display the research pack without synthesis during the first UI slice.
- Add clear no-results, loading, and error states.
- Add CLI `dbrain research` in retrieval-only mode first.
- Remove `/api/ask`, `dbrain ask`, `dbrain_ask`, and the old Ask UI once
  replacement tests and docs are updated.
- Update `CHANGELOG.md`, `README.md`, `skills/dbrain-mcp/SKILL.md`, MCP
  resources/prompts, and CLI help in the same change that removes Ask.

### Phase 2: Local Synthesis

- Add synthesis prompt and local model call over the research pack.
- Add synthesis controls in the web UI and CLI, wired to
  `/api/research/synthesize`.
- Return answer, answer status, resolved model, tool, prompt version, and
  citations.
- Add total synthesis evidence budgeting with explicit truncation warnings.
- Add `POST /api/research/synthesize` as an SSE endpoint.
- If model adapters remain request/response-only, emit progress plus a final
  `answer` event. Emit `token` events only after provider-native streaming is
  implemented.
- Add context-cancel, client-disconnect, slow-reader, heartbeat, and synthesis
  concurrency-cap behavior.
- Add tests around prompt construction and no-evidence behavior.
- Add unavailable-model tests proving evidence retrieval still succeeds.
- Add evidence-budget tests proving lower-ranked evidence is trimmed or dropped
  before higher-ranked evidence.
- Add UI toggle for per-question local synthesis, defaulting on.
- Enable CLI `dbrain research` synthesis by default with an explicit
  retrieval-only flag.

### Phase 3: Conversational Follow-Up

- Add browser session state for prior question/evidence.
- Let follow-up questions include previous evidence context.
- Add "use this evidence in follow-up" controls.
- Keep chat ephemeral in browser `sessionStorage` by default.
- Reuse prior packs, evidence, and lookups in follow-up turns; do not treat
  previous model answers as evidence.

### Phase 4: Bounded Internal Tool Loop

- Add server-side tool schemas mirroring MCP.
- Let the model choose a bounded number of read-only tool calls.
- Capture trace output so the UI can show what the model searched/opened.
- Add timeouts, max-call limits, and evidence-size limits.

## Testing Plan

- Unit tests for moved research-pack logic.
- Golden tests for `internal/brainresearch` output on stable fixtures.
- MCP parity tests proving existing research-pack JSON semantics stay stable
  across extraction, including `mode`, `coverage.top_user_tags`,
  `coverage.recall_note`, `omitempty` behavior, and next-step ordering.
- `internal/mcpeval` compatibility tests for research-pack cases. This package
  already exists and runs local retrieval eval cases against expected source
  keys, evidence counts, exact-tag examples, and forbidden/required text.
- Web handler tests for `/api/research`.
- Fixture tests proving exact-tag evidence and source-type filters work through
  the web API.
- Fixture tests proving exact-tag examples work for both item tags and source
  tags.
- Fixture tests proving related evidence is returned when requested.
- Handler tests for no-results responses and unavailable model fallback.
- HTTP error tests for invalid JSON, empty question, invalid filters/options,
  store/internal failures, invalid synthesis packs, and model-unavailable
  pre-stream failures.
- Prompt tests for synthesis input shape.
- Evidence-budget tests for deterministic synthesis truncation behavior,
  including `evidence_budget_chars`, `evidence_chars_used`,
  `dropped_source_keys`, and `partially_trimmed_source_key`.
- SSE tests for success sequence, token-streaming sequence when supported,
  post-start failure sequence, mutually exclusive `answer`/`token`, `done`
  citation metadata, and structured pre-stream errors.
- Cancellation/leak tests for client disconnect, request context cancellation,
  slow readers, and goroutine/temp-file cleanup.
- UI tests for research-pack rendering when practical.
- Integration tests asserting `/api/ask` returns 404 after removal.
- CLI tests asserting `dbrain ask` is unavailable after removal.
- MCP tool-list tests asserting the existing `dbrain_ask` tool is absent after
  removal.
- Existing gates: `task fmt`, `task lint`, `task test-ci`, and `task build`.

## Acceptance Criteria

Phase 1a is ready when:

- the canonical research-pack schema is frozen and versioned before extraction
- `internal/brainresearch` owns the reusable research-pack implementation
- shared core structs are transport-neutral and typed
- MCP delegates to `internal/brainresearch` without behavior regressions
- MCP-specific text rendering and tool-name mapping remain in
  `internal/mcpserver`
- `POST /api/research` returns query plan, evidence, coverage, exact tag
  evidence, related-evidence relationship metadata when requested, typed topic
  brief, semantic next steps, and schema version
- exact-tag examples resolve both item and source rows
- source-type filters and no-results responses are covered by handler tests
- query normalization between `ask.Hints()` and research terms is frozen or
  centralized before `internal/brainresearch` is considered stable
- existing MCP research tools still work

Phase 1b is ready when:

- the web UI can ask a broad natural-language question and show a research pack
- MCP, web, and CLI use the same core research behavior
- no-results queries render a clear empty-evidence state with reformulation
  suggestions
- `dbrain research` runs in retrieval-only mode and returns a research pack
- the old Ask UI, `/api/ask`, `dbrain ask`, and `dbrain_ask` are gone
- `CHANGELOG.md`, `README.md`, `skills/dbrain-mcp/SKILL.md`, MCP guidance, and
  CLI help no longer point at removed Ask surfaces
- the existing search UI still works

Phase 2 is ready when:

- the user can click "Synthesize" and get a local model answer
- the answer cites source keys and note paths
- weak evidence is called out rather than hidden
- the model/provider provenance is visible
- synthesis enforces a total evidence budget and reports truncation clearly
- `/api/research/synthesize` uses `text/event-stream` with explicit start,
  heartbeat, answer/token, done, and error semantics
- truncation metadata fields are emitted and tested end-to-end:
  `evidence_budget_chars`, `evidence_chars_used`, `dropped_source_keys`, and
  `partially_trimmed_source_key`
- machine-readable citations are available through `done.citations`
- model unavailability returns evidence plus a warning instead of failing the
  research request
- local-to-hosted fallback never happens without explicit opt-in

Phase 3/4 are ready when:

- follow-up questions reuse context
- chat context uses browser `sessionStorage`
- prior packs/evidence/lookups can be reused, but prior model answers are not
  treated as evidence
- internal tool calls are bounded and visible
- the behavior feels close to asking an MCP-capable agent, without requiring an
  external agent application

## Open Questions

- How much of the MCP tool description text should be reused in the web prompt?
  Use whatever improves result quality, but avoid copying protocol-specific
  wording that makes the browser flow sound like an MCP client.
- Should exact-tag examples keep a reserved synthesis budget beyond one
  representative example, or be treated as normal evidence after the reserved
  minimum?
- When provider-native streaming becomes available, should Phase 2 switch from
  final-answer SSE to token SSE immediately or keep both event paths?

## Settled Decisions

- `Research` replaces `Ask`; old Ask UI/API/CLI/MCP surfaces should be removed,
  not aliased.
- Ask removal ships in Phase 1b without aliases or wrappers, in the same change
  as changelog, README, MCP skill, MCP guidance, and CLI help updates.
- Use the current MCP research-pack field names as the canonical v1 schema
  unless a concrete product need forces a rename.
- Keep related evidence in `evidence[]` with relationship metadata for v1, not
  a separate top-level lane.
- Use full-pack POST for `/api/research/synthesize` in v1; no server-side pack
  handles or scratch state.
- Phase 2 SSE may emit progress plus one final answer while model adapters are
  request/response-only; token events require provider-native streaming first.
- Synthesis is a per-question option and defaults on.
- If synthesis is unavailable, retrieval still succeeds and the UI shows a
  warning.
- Local synthesis should not silently fall back to hosted providers.
- The first recommended local synthesis model is the locally used Qwen 3.6
  model; add a focused Ollama Modelfile if testing shows better behavior.
- If added, the optional Modelfile should live under `contrib/` and remain
  outside `task build`.
- Chat state stays in browser `sessionStorage` for now; durable transcript
  saving is deferred and out of scope for the initial chat phases.
- Core research behavior should move to `internal/brainresearch` so web, CLI,
  and MCP share the same implementation.
