# Research And Chat Harness

Status: current-state documentation and implementation plan
Date: 2026-05-23

## Summary

`dbrain` has a local-first research harness, more precisely a
research/retrieval harness. It is not a generic LLM benchmark harness, and it
should not be described that way. The core system takes a natural-language
question, builds a bounded retrieval strategy, gathers and reranks evidence
from the local corpus, packages that evidence with coverage and planner
metadata, and asks a configured model to synthesize a cited answer from that
evidence only for the user-facing Chat path.

The important implementation boundary is:

```text
CLI research
web Chat
web Research API
MCP dbrain_research_pack
        |
        v
internal/brainresearch
        |
        v
internal/ask + internal/store + internal/entities + internal/topics
```

MCP is a transport and tool surface. It is not the owner of the core research
behavior. The reusable harness is `internal/brainresearch`.

The strongest next step is not "add more model magic." It is to make the
agent-side research loop native, observable, and testable. The implementation
plan below uses the current external MCP-agent workflow as the target shape:
trace every run, turn transcripts into evals, keep planner-disabled retrieval
strong, make Chat the default web surface, add a bounded server-side research
state machine, show progress while the harness works, improve long-source
evidence, and only then add optional local semantic retrieval.

Product decisions for the next version:

- Web Chat is the default user interface for research. The separate Research
  tab can go away, while the server-side research APIs and core packages remain
  because Chat and evals still use them.
- User-facing research always synthesizes an answer. Retrieval-only paths remain
  useful for debugging and eval plumbing, but not as the main product mode.
- Traces are saved by default for now so harness changes have evidence.
- Evals are not a side project. They are the main feedback loop for improving
  retrieval, synthesis, and citation behavior over time.
- The user's saved dbrain corpus is evidence for the user's own research. The
  harness should not try to "balance" or disprove that corpus unless the user
  explicitly asks for adversarial analysis, bias review, or external
  verification.

## Evidence Used For This Document

This document is grounded in the current repo and in dbrain MCP evidence.

Repo surfaces checked:

- `internal/brainresearch`: research pack construction, query strategy,
  planner merge, evidence scoring, topic brief attachment, synthesis input, and
  synthesis execution.
- `internal/ask`: lower-level retrieve-only item/source search, tag expansion,
  entity expansion, related evidence, ranking, and excerpts.
- `internal/queryterms`: natural-language and chat follow-up query cleanup.
- `internal/mcpserver`: MCP tool definitions and delegation to
  `internal/brainresearch`.
- `internal/mcpeval`: retrieval regression cases for MCP/research behavior.
- `web` and `web/ui`: web Research, Chat, transcript save, and session
  storage behavior.
- `README.md`, `COMMANDS.md`, `MCP.md`, `evals/README.md`, and
  `docs/web-brain-research.md`.

MCP corpus signals used:

- [`darron/dbrain`](https://github.com/darron/dbrain): confirms current command
  and MCP semantics from the saved repo source.
- [`Harness engineering: leveraging Codex in an agent-first
  world`](https://openai.com/index/harness-engineering/): useful frame that
  harness quality comes from environment design, constraints, tests, and
  feedback loops.
- [`Equipping agents for the real world with Agent
  Skills`](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills):
  useful frame for progressive disclosure, skills, and MCP as complementary
  capabilities.
- [`What to Learn, Build, and Skip in AI Agents
  (2026)`](https://x.com/i/article/2048881094637080576): useful frame for
  context engineering, orchestrator/subagent discipline, evals from traces, and
  file-system-as-state.
- [`Meta-Meta-Prompting: The Secret to Making AI Agents
  Work`](https://x.com/i/article/2052898104039657472): useful frame for thin
  harness/fat skills, deep retrieval, model agnosticism, and turning repeated
  workflows into tested skills.
- [`OpenProse - Engineer your agents`](https://prose.md/): useful frame for
  contracts, durable traces, and portable harness execution.
- Internal Apple Notes design note, `Large Source Chunking`: non-public local
  note used for deterministic chunking, raw evidence preservation, and not
  treating summaries as raw evidence. If this document is published outside the
  repo, replace this with a public design note or move the detailed chunking
  rationale into this document.

The external-source material above should be treated as design inspiration and
saved-corpus context, not as proof that dbrain already implements those ideas.

## Current Harness

### What It Is

The harness is a shared research pipeline:

1. Normalize the question into searchable text and terms.
2. Build a deterministic query strategy.
3. Optionally call a configured model for a bounded query plan.
4. Merge model-assisted concepts and variants into the deterministic strategy.
5. Run retrieve-only searches for each query variant.
6. Rerank evidence by retrieval score, query-variant signal, and concept
   coverage.
7. Add exact-tag evidence and corpus coverage.
8. Optionally attach a topic brief.
9. Emit a structured `research_pack.v1`.
10. Synthesize a cited answer from the pack for the user-facing Chat path.

The pack schema is typed in `internal/brainresearch/types.go`. It includes:

- `query_plan`: text query, query terms, tag queries, query variants, concepts,
  planner name, planner model/error, filters, limits, and topic flags.
- `coverage`: evidence count, kind/source-type buckets, top user tags, exact
  tag matches, corpus match counts, topic graph counts, and a recall note.
- `evidence`: ranked item/source evidence rows from `internal/ask`.
- `exact_tag_evidence`: representative exact user-tag matches.
- `topic_brief`: optional topic graph summary and pivots.
- `next_steps`: suggested follow-up tool actions such as inspecting top
  evidence or expanding related context.

This is a good "harness" in the local research sense: it standardizes input,
query planning, retrieval, evidence packaging, and answer synthesis. It is not
a generic "LLM harness" because it does not benchmark arbitrary models or run a
general agent runtime.

Retrieval-only output remains useful as an internal/debug surface, but the
forward product path is Chat with synthesis.

### Query Normalization

`internal/queryterms` is the first defensive layer. It strips obvious filler,
normalizes terms, drops source-key fragments, builds tag aliases, and handles
chat-shaped prompts.

For chat, `SearchText` looks for structured headings such as:

- `Current question:`
- `Recent user questions:`
- `Prior evidence titles for query focus:`
- `Pinned evidence keys:`

The search text intentionally ignores prior evidence sections when they would
pollute the query. This matters because a follow-up should not accidentally
turn old evidence titles, tags, summaries, or previous model answers into new
facts.

### Deterministic Strategy

`internal/brainresearch.buildDeterministicResearchStrategy` turns normalized
terms into concepts and query variants. The current strategy already has
specialized behavior for some important families:

- people/event questions such as "Calgary father killed two kids"
- generic technical abbreviation questions such as `K8s Helm alternatives`
- model-selection questions such as `Hermes agent` model stack searches

The deterministic path is not a backup path. It is the reliability baseline.
Prior transcript tuning showed that planner timeouts are a real failure
surface, so `--no-planner` retrieval must stay useful on its own.

### Optional Model Planner

The planner prompt in `internal/brainresearch/planner.go` asks the configured
model to return compact JSON only. It explicitly says not to answer the user's
question. The model can add:

- concepts
- aliases
- alternate phrasing
- title-like variants
- abbreviation expansions

The code sanitizes, parses, bounds, and merges the plan with deterministic
concepts. Model-only expansion concepts are treated cautiously: they help
search, but should not become hard constraints just because a model invented a
new abstraction.

Planner behavior is visible in the pack via `query_plan.planner`,
`query_plan.planner_model`, `query_plan.planner_error`, `query_variants`, and
`concepts`.

### Evidence Collection And Scoring

The research harness calls `ask.Run` in retrieve-only mode for each query
variant. `ask.Run` searches items and sources, expands user tags unless
disabled, uses entity expansion, builds evidence rows, ranks candidates, and
can append related evidence.

The research layer then adds its own score adjustments:

- query variant signal
- concept matches
- missing required concepts
- bonus when all required concepts match

Each evidence row can carry retrieval signals and matched/missing terms. That
is important: it lets clients and humans see whether a row won because it
matched the real query, because it had a broad tag, or because it was merely
related.

### Coverage And Exact Tags

Coverage is a first-class part of the pack. It is deliberately not just
`len(evidence)`.

The pack reports kind/source-type buckets, top user tags, exact tag matches,
item/source corpus match counts, displayed limits, related limits, and topic
graph counts. The recall note is blunt that returned evidence is a capped
working set, not the full corpus.

The exact-tag evidence lane is especially useful for broad entity questions.
Without it, linked source documents can outrank saved items carrying the user's
own tag, which hides why the material was saved.

### Topic Briefs

When a broad topic can be inferred, or when explicitly requested, the harness
uses `internal/topics` to attach a topic brief. The brief includes graph nodes,
edges, pivots, entities, and a summary.

This is currently a sidecar overview, not an iterative planner. It helps the
human or agent see the shape of a topic, but the research flow does not yet
loop over topic pivots and rerun targeted follow-up retrieval.

### Synthesis

Synthesis is separate from retrieval in the implementation, but the forward
user-facing product path always synthesizes. `research_pack.v1` can still be
returned without a synthesis call for debugging, evals, MCP primitive use, and
API compatibility.

When synthesis is enabled, `PrepareSynthesis` validates the pack version,
requires a configured model when evidence exists, builds a capped evidence
input, records truncation metadata, and precomputes citations from included
evidence rows.

The existing synthesis types are a useful foundation for traces.
`PreparedSynthesis` already carries schema version, prompt version, truncation
metadata, citations, warnings, and answer status before the model call.
`SynthesisResult` carries the answer, answer status, warnings, truncation
metadata, citations, prompt version, model, tool, and tool version after
synthesis. Trace code should reuse those types directly instead of inventing
parallel synthesis metadata.

The synthesis prompt requires the model to:

- answer from the provided dbrain research pack only
- avoid outside knowledge
- cite material claims with exact source keys
- distinguish user-authored notes from linked third-party sources
- distinguish summaries, excerpts, transcripts, OCR, raw notes, and web
  extracts where relevant
- say when evidence is weak or missing

This is the correct discipline. Model prose is derived synthesis, not evidence.

### MCP Surface

The MCP server exposes read-only tools. The key research tool is
`dbrain_research_pack`, which delegates to `internal/brainresearch.Build`.
Other tools such as `dbrain_search`, `dbrain_get`, `dbrain_get_many`,
`dbrain_related`, topic/entity tools, and stats tools are direct corpus tools.

The important LLM boundary is:

- `dbrain_search`, `dbrain_get`, `dbrain_get_many`, `dbrain_related`, and stats
  tools do not inherently use an LLM.
- `dbrain_research_pack` may use a configured model for optional query
  planning unless disabled.
- Synthesis happens in CLI/web research and chat, not in the primitive
  read-only MCP retrieval tools.

### CLI Surface

`dbrain research <question>` builds a research pack and then synthesizes a
grounded answer unless `--retrieval-only` is set.

Useful flags include:

- `--source-type`
- `--include-related`
- `--topic` / `--topic-brief` / `--no-topic-brief`
- `--planner-model`
- `--planner-timeout`
- `--planner` / `--no-planner`
- `--model`
- `--max-evidence-chars`
- `--json`

The CLI is the best current smoke path for checking planner behavior,
runner behavior, and retrieval-debug behavior outside the browser.

### Web Research

`POST /api/research` builds a pack with `brainresearch.Build`.
`POST /api/research/synthesize` streams the synthesis answer over SSE.

The web API uses the same core package as CLI and MCP. This is the right shape:
the web server does not run an internal MCP client to talk to itself.

The separate browser Research tab should be removed from the primary UI. It has
been useful while building the harness, but Chat is the surface that is actually
used and should become the default. The underlying research API and core code
should stay in place because Chat, CLI diagnostics, MCP, and evals still depend
on the same pack-building and synthesis path.

### Web Chat

The current web chat is a shallow research loop:

1. Build a retrieval question from the current user question, selected prior
   questions, and compact prior evidence title/type hints.
2. Call web Research to get a fresh pack.
3. Merge current evidence with pinned/recent prior evidence.
4. Synthesize an answer over the merged pack.
5. Keep recent turns and pinned evidence in browser `sessionStorage`.

Important safety properties:

- It does not use prior model answers as facts.
- It stores only the last eight turns in session storage.
- It keeps up to 24 pinned evidence keys.
- Saved transcripts go under `data/chat-transcripts/...` as non-indexed
  diagnostics.

The chat is useful, but it is not yet a real multi-step research agent. It does
one research call and one synthesis call per turn, with client-side continuity.
Going forward, this is the primary web interface for research.

### Agent-Side Research Loop

An MCP-capable agent such as Codex currently performs a richer research loop on
top of the dbrain tools. That loop is partly tool use and partly agent working
method. It is worth documenting because it shows what the native harness should
eventually make explicit, observable, and testable.

The practical agent loop is:

1. Frame the task and decide what would count as enough evidence.
2. Start with `dbrain_research_pack` for broad questions.
3. Read `query_plan`, `coverage.recall_note`, `exact_tag_evidence`,
   retrieval signals, matched terms, and missing terms before trusting the
   evidence ranking.
4. Inspect high-signal rows with `dbrain_get_many` using
   `content_mode="evidence"` and the original query so raw extracts,
   transcripts, OCR, and backlink context are windowed around the match.
5. Expand from strong items or sources with `dbrain_related` when links,
   quotes, backlinks, or source relationships look relevant.
6. Use `dbrain_search`, `dbrain_topic_map`, `dbrain_topic_brief`, or
   `dbrain_entity_map` only when the first pack is weak, broad, or obviously
   missing an alias/topic/entity lane.
7. Separate raw evidence from derived summaries and separate source claims
   from conclusions.
8. Check for conflicts, weak evidence, missing evidence, and over-broad
   matches before synthesizing.
9. Answer with source keys, note paths or URLs where useful, and explicit
   uncertainty when the corpus does not support a stronger answer.

The agent also keeps a transient scratchpad in its context: current hypothesis,
candidate source keys, rejected/noisy source keys, aliases to try, facts that
are actually supported, and gaps still open. Today dbrain does not persist that
scratchpad as structured research state. It only sees the tool calls and the
final answer unless the user saves a transcript.

This suggests a useful component model for the native harness:

- **Planner**: turns the user question into deterministic and model-assisted
  query variants, aliases, topic hints, and required concepts.
- **Retriever**: runs lexical/tag/entity/source searches and returns candidate
  evidence with lane provenance.
- **Inspector**: fetches deeper content windows for selected rows, including
  raw extracts, OCR, transcripts, backlinks, and quoted/linked context.
- **Expander**: follows related sources, item backlinks, entities, and topic
  graph pivots when coverage is weak or a strong row points outward.
- **Judge**: decides whether enough evidence exists, whether the top rows match
  the real question, and whether another bounded pass is justified.
- **Synthesizer**: writes the answer from included evidence only.
- **Verifier**: checks source-key citations, no-evidence behavior, truncation
  warnings, and whether unsupported claims slipped into the answer.
- **Tracer**: records every query, row, score, decision, model call, citation,
  and warning so bad answers can become eval cases.

The current code has pieces of Planner, Retriever, Synthesizer, and a small
amount of Verifier. Inspector and Expander mostly exist as separate MCP tools
that an external agent can call manually. Judge and Tracer are the biggest
missing native components.

### Eval Harness

`internal/mcpeval` and `dbrain eval mcp` are the closest existing thing to a
traditional eval harness in this area. They run retrieval regression cases
against the MCP/research retrieval path. The next step is `dbrain eval
research`, which should evaluate the full Chat/research runner: retrieval,
trace shape, synthesis status, citation validity, and transcript-derived
regressions.

Current eval cases can assert:

- minimum evidence count
- expected source keys
- acceptable alternative source keys
- expected top source keys
- forbidden source keys
- expected or forbidden text
- exact-tag evidence
- top matched terms
- forbidden missing terms
- rough latency budgets

This is valuable, but it is still mostly a retrieval regression harness. It
does not yet provide full trace replay, synthesis grading, citation checking,
or automatic transcript-to-eval generation.

## What Is Already Strong

### Shared Core

The single most important architectural win is that CLI, web, and MCP now share
`internal/brainresearch`. This prevents three subtly different research systems
from drifting apart.

### Local-First Semantics

The harness reads the local SQLite/vault corpus and can use local Ollama models.
Hosted providers are configurable, but the design does not require a SaaS brain
or an external agent service.

### Bounded Planning

The model planner is constrained to compact JSON. It is asked to plan retrieval,
not answer the question. The output is bounded, sanitized, and merged into
deterministic terms instead of becoming an untrusted free-form execution plan.

### Transparent Pack Metadata

The pack exposes query terms, tag aliases, variants, concepts, planner metadata,
coverage, exact tags, and retrieval signals. That makes bad answers debuggable
in principle.

### Grounded Synthesis

Synthesis is explicitly answer-from-evidence-only. It has citation metadata,
truncation metadata, no-evidence handling, and source-key discipline.

The implementation already has typed `PreparedSynthesis`, `SynthesisResult`,
and `TruncationMetadata` structures, so the trace work can reuse existing
synthesis metadata rather than starting from scratch.

### Chat Does Not Promote Answers To Evidence

This is essential. Prior model answers are not fed back as source facts. Chat
continuity reuses prior questions and evidence rows, not previous prose as
truth.

### Retrieval Evals Exist

The `dbrain eval mcp` path gives the project a real place to encode retrieval
regressions. `dbrain eval research` should build on that foundation and become
the center of future Chat/harness hardening.

## Where It Is Weak

### Run Traces Are Too Thin

When a research answer is wrong, the system has enough internal state to explain
why, but it does not preserve all of that state as a durable local trace by
default.

Missing or under-captured details include:

- planner input
- raw planner output
- sanitized planner output
- per-variant search results before dedupe
- candidate scores before and after research reranking
- reasons rows were dropped
- topic-brief inference decisions
- exact synthesis input after truncation
- citation coverage checks
- model/tool versions across planner and synthesis

Saved chat transcripts are useful, but they are not complete research traces.

### Deterministic Retrieval Still Needs More Coverage

The deterministic path has improved, but it is still a set of targeted
heuristics and tests. It needs a larger case library, especially for:

- model/tool selection questions
- named entities with multiple aliases
- crime/news/event queries
- "what do I know about X?" broad-topic questions
- comparison questions
- corrective follow-ups
- media/OCR/transcript-heavy questions
- queries where the right answer is a source document rather than a saved item

Planner-disabled retrieval should be a release gate, not a niche debugging
mode.

### Chat Is Not Yet A Bounded Research Loop

Chat currently performs one research call and one synthesis call per turn. It
can merge prior evidence, but it cannot decide to inspect top evidence, expand
related context, retry with a narrower variant, or ask for a topic brief based
on weak coverage.

The MCP skill already tells external agents to do that manually:

1. call `dbrain_research_pack`
2. inspect strong evidence with `dbrain_get_many`
3. expand with `dbrain_related`
4. use topic/entity maps when appropriate

The web chat does not yet internalize that workflow.

The deeper gap is that the agent-side scratchpad is not a first-class state
object. A good agent tracks what it tried, what looked noisy, which aliases are
still worth testing, which evidence rows were inspected, and why it stopped.
The current native harness exposes query plans and final evidence, but it does
not preserve the full decision trail.

It also does not show enough visible progress while the harness is working.
When planning, retrieval, synthesis, or a model call takes time, the web UI can
look hung even if the backend is doing useful work. The next Chat UI should show
the runner state as a compact live timeline.

### Retrieval Is Still Mostly Lexical

The current stack is good for local FTS, exact tags, summaries, OCR text, media
transcripts, and source text windows. It is weaker when the user asks a
semantic question whose answer uses different language than the saved source.

The optional model planner partially helps by adding aliases and variants. But
that is not the same as semantic retrieval or reranking.

### Long Source Handling Is Not Yet A Research Primitive

The synthesis path has a character budget and truncation metadata. That is
better than silent overflow, but it still means the research pack can lose
important detail from long sources.

The local large-source chunking note points in the right direction:
deterministic chunking, raw extract preservation, chunk summaries as derived
cache, and search over raw text plus derived chunks. That should become part of
the research harness rather than only a summarization concern.

### Eval Coverage Is Retrieval-Only And Mostly Manual

`dbrain eval mcp` is useful, but it does not yet cover:

- planner-on versus planner-off parity
- query-plan assertions
- planner timeout behavior
- synthesis answer status
- citation validity
- whether cited source keys actually appear in the pack
- whether answers cite every material claim
- transcript replay
- regression extraction from saved chat transcripts

Without this, the harness can improve by anecdote but still regress in routine
queries.

The next eval surface should be `dbrain eval research`. MCP evals should remain
available for read-only tool and retrieval-pack behavior, but full harness
quality should be measured through the research runner: trace shape, query
planning, inspected evidence, synthesis status, citation validity, and
regressions extracted from real Chat transcripts.

### Evidence Quality Is Not Yet Explicit Enough

Evidence rows carry useful metadata, but the UI and synthesis could do more to
separate:

- raw source text
- source summary
- item summary
- user-authored Apple Notes text
- OCR
- media transcript
- source backlink context
- exact user tag evidence
- graph-related evidence

Those are not equivalent kinds of support. A model answer should know the
difference, and a user should be able to see the difference quickly.

### Corpus Evidence Boundary Needs To Stay Explicit

The user's dbrain corpus is not a neutral public encyclopedia. It is a saved
working corpus. For normal research questions, those saved notes, links,
extracts, summaries, OCR blocks, transcripts, and tags are the evidence to
inspect.

The harness should not automatically add external "balance," bias correction,
or counterargument hunting. That is a different mode. If the user asks "is this
biased?", "prove this wrong," "verify this externally," or "what evidence
contradicts this?", then the harness can run an adversarial or external
verification workflow. Otherwise, the job is to answer from the local corpus and
be clear about what the corpus does or does not contain.

## Implementation Plan

The plan is to move from a good one-shot research pack builder to a traceable,
bounded research runner. The runner should encode the careful MCP-agent loop in
Go while preserving the current read-only, local-first, evidence-grounded
semantics.

### Target Architecture

Add a server-side research runner around the existing `internal/brainresearch`
builder:

```text
ResearchRun
  begin_trace
    -> plan
    -> retrieve
    -> emit_progress
    -> inspect_top
    -> emit_progress
    -> judge_coverage
    -> optional_expand_or_retry
    -> emit_progress
    -> prepare_synthesis
    -> synthesize
    -> verify
    -> persist_trace
```

The runner should be bounded and deterministic in shape:

- no mutations
- maximum one retry in the first version
- maximum one related expansion in the first version
- maximum one top-evidence inspection batch in the first version
- fixed evidence and character budgets
- explicit stop reason
- trace events for every state transition and major decision
- visible progress events for the web UI
- synthesis attempted for every normal user-facing run
- Markdown trace plus JSON sidecar saved by default

Useful stop reasons:

- `enough_evidence`
- `no_evidence`
- `weak_evidence_budget_exhausted`
- `planner_failed_deterministic_used`
- `max_steps_reached`
- `synthesis_unavailable`
- `synthesis_failed`
- `verification_failed`

Use a separate package for the runner:

```text
internal/researchrun
```

Keep `internal/brainresearch.Build` as the stable pack builder. The runner
should compose it rather than burying the pack builder inside a chat-specific
flow.

### Phase 1: Trace The Current System By Default

First add durable diagnostics to the current one-shot flow and save them by
default. This gives future work a regression/debugging substrate before the
runner changes behavior.

Add a trace model:

```go
type ResearchTrace struct {
    SchemaVersion      string
    RunID              string
    Surface            string // cli, web_chat, web_research_api, mcp
    Question           string
    StartedAt          time.Time
    CompletedAt        time.Time
    Events             []ResearchTraceEvent
    Pack               brainresearch.Pack
    PreparedSynthesis  *brainresearch.PreparedSynthesis
    Synthesis          *brainresearch.SynthesisResult
    StopReason         string
}
```

Do not duplicate synthesis metadata into a new shape unless the runner needs
additional fields. Embed or reference `brainresearch.PreparedSynthesis` and
`brainresearch.SynthesisResult` so trace output cannot drift from the actual
synthesis path.

Trace events should capture:

- original question
- chat retrieval question, when applicable
- normalized search text and terms
- deterministic concepts and variants
- planner request metadata
- raw planner response, when available
- sanitized planner result
- merged query plan
- per-variant candidate counts and selected source keys
- dedupe decisions, if practical
- score changes and retrieval signals
- final pack
- synthesis prepared input metadata
- truncation metadata
- citations
- model/tool/prompt versions
- timings per stage

Add file-backed trace persistence under `data/research-runs/`. Each run should
write:

- `run.md`: human-readable timeline, question, answer, key evidence, warnings,
  stop reason, and links to local note paths where useful
- `run.json`: machine-readable trace sidecar with the complete structured trace

Do not index traces into the brain by default. They are diagnostics and eval
seed material, not source evidence.

Everything under `data/` is local runtime state, not source material to import
back into the brain. New trace directories should follow the existing
`data/chat-transcripts/` convention: private, gitignored, and non-indexed by
default.

Trace writes must be concurrency-safe. Use unique run IDs, one directory per
run, write files to temporary names, and atomically rename them into place after
the JSON and Markdown are complete. Do not add a shared SQLite or JSON index in
the first trace version. If an index is added later, treat it as derived and
repairable from the per-run files.

Default trace saving needs an explicit disk policy. Start with a conservative,
configurable retention policy such as keeping at least the latest 500 runs and
offering a 180-day age-based prune rule, plus a `--keep-all-traces` or config
override during active tuning. Any pruning command should report how many trace
directories were deleted.

User surfaces:

- CLI: save traces by default; provide `--no-trace` only for rare noise control
- web Chat: save traces by default alongside transcript export
- MCP/agent use: expose trace path when a traced run is executed through a
  runner surface
- web UI: remove the Research tab from primary navigation in this phase while
  preserving the underlying APIs used by Chat and evals

Required tests:

- trace serialization is deterministic enough to assert shape
- traces redact or omit local-only secrets
- default traced runs write both Markdown and JSON files
- `--no-trace` suppresses trace writing
- synthesized Chat traces include the answer, citation metadata, and warnings
- concurrent traced runs create distinct complete run directories
- retention pruning respects configured keep counts/ages and never deletes the
  active run
- Research tab is absent from primary navigation while Chat remains available
- prior model answer text cannot appear as an evidence row or synthesis-input
  fact in the next traced Chat turn

### Phase 2: Make Evals Match The Harness

Add `dbrain eval research` so evals can test the full research harness, not
only source-key retrieval. `dbrain eval mcp` should remain for MCP tool and
retrieval-pack regressions, but the main quality loop for Chat should live in
the research eval command.

Reuse existing eval concepts where practical, but add research-run assertions
for:

- `disable_planner`
- `planner_model`
- `expect_planner`
- `expect_query_terms`
- `expect_query_variants`
- `forbid_query_variants`
- `expect_concepts`
- `forbid_concepts`
- `expect_planner_error_contains`
- `min_retrieval_signals`
- `expect_answer_status`
- `expect_citation_source_keys`
- `forbid_citation_source_keys`

Important local cases should run both ways:

- planner enabled
- planner disabled

Planner-enabled behavior can be better, but planner-disabled behavior should
still meet a minimum useful baseline.

Add transcript/trace proposal tooling:

```sh
dbrain eval research propose --from-transcript data/chat-transcripts/...
dbrain eval research propose --from-trace data/research-runs/...
dbrain eval research --file evals/local/research.json
```

The proposal output should be conservative. It can suggest source keys,
expected top keys, expected text, forbidden noisy keys, and planner-off cases,
but a human should review before saving to `evals/local/*.json`.

Build a minimal Harness Lab alongside this eval work instead of waiting for the
polished UI phase. The first useful version can be small:

- load a saved trace JSON
- rerun `brainresearch.Build` with the same question/options
- diff old versus new evidence source keys
- show added, removed, and reordered evidence rows
- link to "propose eval case" for that trace

This gives deterministic strategy work a visible feedback loop while the full
runner is still being built.

Required tests:

- eval cases can assert query plan fields
- eval cases can disable the planner
- eval report shows planner metadata and top retrieval signals
- transcript proposal does not write cases without explicit output/apply flags
- trace proposal can generate reviewed research cases from default-saved traces
- synthesis and citation assertions fail when source-key coverage regresses
- minimal Harness Lab can load a trace and show an evidence-key diff

### Phase 3: Promote Deterministic Strategy To A Maintained Surface

Organize deterministic strategy by query family rather than scattered
heuristics.

Initial families:

- entity/topic overview
- person/news/event lookup
- model/tool selection
- software-project lookup
- comparison
- timeline/history
- media transcript/OCR lookup
- corrective follow-up
- exact title/source lookup

Each family should have:

- query-term cleanup rules
- variant generation rules
- concept requirements
- regression cases
- examples in `evals/README.md`

The implementation can stay in `internal/brainresearch` and
`internal/queryterms`, but the tests should name the query family so future
changes do not become a pile of unrelated string tweaks.

Required tests:

- each family has planner-disabled regression coverage
- corrective follow-ups avoid prior bad evidence
- broad entity questions still expose exact-tag evidence
- media/OCR/transcript queries hit claim-bearing text, not only generic titles

### Phase 4: Add The Bounded Research Runner

Implement the server-side runner once tracing and evals exist.

Make the Judge a typed, isolated decision component rather than burying retry
rules inside the runner loop:

```go
type JudgeResult struct {
    Verdict         string // enough_evidence, no_evidence, weak_evidence
    Reason          string
    MissingConcepts []string
    WeakRows        []string
    RetryAction     string // none, focused_retry, related_expansion
}
```

Inputs should be the final query plan, required concepts, top evidence rows,
retrieval signals, matched/missing terms, exact-tag evidence, and the recall
note. Outputs should drive the stop reason, retry decision, and trace warning
without requiring the rest of the runner to know the scoring details.

First runner behavior:

1. Build the initial pack.
2. Inspect top evidence with the same evidence-windowing used by
   `dbrain_get_many`.
3. Judge whether the top rows satisfy enough required concepts and whether the
   pack has direct evidence, exact-tag evidence, or only weak/related evidence.
4. If weak and budget remains, retry one focused deterministic variant or
   expand related evidence for the strongest row.
5. Prepare synthesis from the final evidence set.
6. Run synthesis for every normal user-facing run.
7. Verify deterministic citation rules.
8. Persist the Markdown trace and JSON sidecar by default.

Do not give the model an open-ended tool loop. The model may help planning and
synthesis, but the state machine decides which retrieval/inspection actions are
allowed.

User surfaces:

- CLI: `dbrain research --runner` or make the runner default after parity is
  proven
- web Chat: use the runner server-side instead of the current Svelte-only
  orchestration, and stream progress events while it works
- web Research API: keep the API/core behavior for Chat, CLI diagnostics, MCP,
  and evals, but do not reintroduce the separate navigation tab
- MCP: keep `dbrain_research_pack` stable; optionally add a separate
  `dbrain_research_run` later if agents need the full traced runner output

Required tests:

- max-step enforcement
- no mutation paths
- stop reasons
- no-evidence behavior
- weak-evidence retry behavior
- JudgeResult verdicts, missing concepts, and retry actions
- related expansion budget
- trace event ordering
- trace default-on behavior
- user-facing runs always attempt synthesis
- web Chat progress events cover planning, retrieval, inspection, synthesis,
  verification, and trace persistence
- web chat still avoids prior model answers as evidence, including in the final
  synthesis input

### Phase 5: Strengthen Citation And Answer Verification

Add deterministic checks before returning or saving a synthesized answer:

- every cited source key must exist in the final pack
- cited source key prefixes must be exact
- citation list should match source keys used in the answer where practical
- no-evidence packs must not produce normal answers
- truncated packs should surface warnings clearly

Hard-gate failures should have explicit user-visible behavior:

- Chat must not render a verification-failed answer as a normal completed
  answer.
- The user should see a compact failure state explaining the failed gate, such
  as missing citations, citation keys not present in the pack, or no-evidence
  synthesis.
- The rejected answer, if one exists, can be stored in the trace for debugging
  but should be clearly marked as rejected and should not be shareable as a
  normal answer.
- The runner should stop with `verification_failed` when a hard gate fails after
  synthesis.
- Soft-gate failures should return the answer with visible warnings and trace
  events, not silently disappear.

Then add optional model-based answer review:

- answer supported by evidence?
- uncited material claims?
- contradictions with evidence?
- overconfident wording where evidence is weak?

Use local models by default for review. Deterministic citation checks should be
the release gate; model review should be advisory until it has eval coverage.

Required tests:

- invalid source-key citations are rejected or warned
- no-evidence synthesis cannot return a normal completed answer
- truncated evidence returns visible warnings
- answer review failures are represented in trace output

### Phase 6: Improve Long-Source And Evidence Fidelity

Implement chunk-level retrieval for large sources as a retrieval feature, not
only as a summarization feature.

Requirements:

- preserve raw extracted text unchanged
- store chunk summaries separately from raw text
- keep chunk hashes/provenance
- index chunk headings, raw chunk windows, and chunk summaries
- return chunk-scoped evidence windows in research packs
- cite the parent source key and chunk metadata
- keep summaries marked as derived, never raw evidence

Also expose evidence kind more explicitly:

- raw source text
- source summary
- item summary
- user-authored Apple Notes text
- OCR
- media transcript
- source backlink context
- exact user tag evidence
- graph-related evidence

Required tests:

- long source query returns the relevant chunk window
- raw text remains available and separate from chunk summaries
- synthesis input labels chunk summaries as derived
- UI/API payloads expose enough evidence-role metadata for display

### Phase 7: Add Optional Local Hybrid Retrieval

Only after the lexical harness is traceable and eval-backed, add local semantic
retrieval as a second lane.

Plain-English version: current search is mostly "find these words, tags, or
nearby exact phrases." A semantic lane means "find passages that mean the same
thing even when they use different words." The risk is that semantic search can
feel impressive while quietly returning vague near-matches. That is why it
should wait until traces and evals can prove whether it helps or hurts.

Operational trigger: do not start this phase until Chat is using the runner,
default trace saving is working, Harness Lab can compare trace reruns, and
`dbrain eval research` has at least 25 reviewed local cases with planner-off
coverage across the major query families. Lexical-only evals should be stable
before adding a second retrieval lane.

Pragmatic shape:

1. Keep SQLite FTS as the baseline.
2. Add local embeddings for item/source/chunk text when configured.
3. Store vectors locally.
4. Retrieve from both lexical and vector lanes.
5. Merge and rerank with existing concept coverage and source-type metadata.
6. Keep `--no-semantic` or equivalent for deterministic lexical debugging.

This should be local-first. Hosted embedding providers can be optional, but
should not become the default route for private brain search.

Required tests:

- semantic lane can be disabled
- lexical-only evals still pass
- merged results keep lane provenance
- vector hits do not outrank exact lexical/tag evidence without a reason

No first embedding model or vector store needs to be chosen yet. The decision
should be deferred until the runner and `dbrain eval research` can compare
lexical-only results against hybrid results on real local traces.

### Phase 8: Make The UI And Skill Reflect The Runner

Chat should already be the default web surface by this point. This phase makes
the runner state and eval workflow visible enough for routine tuning.

While Chat is running, the UI should expose harness state without making normal
use noisy:

- planner used and planner error
- query variants tried
- required concepts matched/missing
- why top evidence won
- inspected evidence rows
- retry/expansion decision
- exact-tag evidence lane
- raw versus derived evidence badges
- OCR/transcript/source-summary labels
- truncation warnings
- stop reason
- "rerun without planner"
- "turn this into eval case"

The visible progress timeline should at minimum show:

- planning query
- retrieving evidence
- inspecting top evidence
- retrying or expanding, when it happens
- synthesizing answer
- verifying citations
- saving trace

Expand the minimal Harness Lab into a fuller diagnostic/eval surface for
comparing old and new behavior. This should not replace Chat. It should let a
developer or power user:

- load a saved trace or transcript
- rerun it against the current harness
- compare old answer versus new answer
- compare old evidence versus new evidence
- inspect citation differences
- promote a trace into a reviewed `dbrain eval research` case

The installed `dbrain-mcp` skill is part of the practical harness because it
teaches external agents how to use MCP tools correctly.

When research semantics change:

- update `skills/dbrain-mcp/SKILL.md`
- refresh the installed copy when needed
- keep MCP prompt/resource guidance aligned
- keep this document current
- add or update eval cases

Required tests:

- web UI can render runner trace summaries
- web Chat displays progress while the runner is active
- Research tab remains absent from primary navigation while Chat remains
  available
- Harness Lab can load a trace and show old/new response and evidence
  comparison
- mobile layout does not overflow long source keys or URLs
- skill guidance stays aligned with MCP tool behavior
- public/shared chat pages do not expose trace-only local internals

## Initial Runner Defaults

These are conservative first-release defaults. They should be treated as
starting hypotheses and adjusted with `dbrain eval research` evidence.

- Retrieval passes: maximum 2 total. Run the initial retrieval pass, then allow
  one focused retry or one related-evidence expansion if coverage is weak.
- Top-evidence inspection: inspect up to 5 rows from the first pass.
- Related expansion: expand from at most 1 strongest row and include at most 8
  related rows.
- Synthesis attempts: 1 normal attempt. Do not loop synthesis repeatedly in the
  first version.
- Planner fallback: if model planning fails or times out, continue with the
  deterministic plan and record `planner_failed_deterministic_used`.
- Weak-evidence retry trigger: retry only when at least one of these is true:
  no evidence was found, the top rows miss required concepts, the best rows are
  only weak/tag/related matches for a question that needs text evidence, or the
  recall note says direct evidence was not found.
- Citation hard gates: every cited source key must exist in the final evidence
  pack; an evidence-backed answer must cite at least one source key; a
  no-evidence answer must not pretend to be a normal answer.
- Citation soft gates: factual paragraphs should generally have citations, and
  uncited factual claims should produce trace warnings. Treat this as warning
  first, then promote it to a hard gate after evals prove the rule is stable.
- Timeout policy: use the existing planner/model timeouts first. Add
  runner-level timeout only when traces show real hangs rather than slow but
  productive work.

The point is not that these numbers are perfect. The point is that they make
the first runner bounded enough to ship, trace, compare, and tune.

## Success Criteria

The harness is materially better when these are true:

- A bad chat answer can be reduced to a trace showing the exact planner,
  retrieval, ranking, truncation, and synthesis decisions.
- A saved transcript can become a reviewed local eval case in minutes.
- Important `dbrain eval research` cases pass both with and without model
  planning.
- Web Chat uses the same server-side runner as CLI research, with surfaced stop
  reasons instead of hidden client-only control flow.
- Chat shows progress while planning, retrieving, inspecting, synthesizing,
  verifying, and saving traces.
- Traces are saved by default as readable Markdown plus JSON sidecars.
- Trace retention is explicit, configurable, and safe under concurrent Chat/CLI
  runs.
- Research packs can retrieve the relevant chunk of a long source, not only the
  source summary or first excerpt.
- Corrective chat follow-ups stop anchoring on prior bad evidence.
- The UI can explain why evidence was selected without requiring a debugger.
- Source-key citation errors are caught mechanically.
- Model answers remain derived synthesis and never become authoritative
  evidence for later turns.
- The Harness Lab can compare an old answer/trace against a new harness run and
  promote useful cases into evals.

## Non-Goals

- Do not turn this into a generic autonomous agent framework.
- Do not make MCP tools mutate dbrain state.
- Do not treat chat answers as source evidence.
- Do not replace local FTS with opaque semantic retrieval.
- Do not require hosted inference for private local research.
- Do not tune the harness against global truth. Tune it against the user's
  saved corpus and explicit eval cases.
- Do not automatically run bias correction, external counterargument hunting, or
  global verification unless the user asks for that mode.

## Decisions And Remaining Questions

Resolved decisions:

- Traces start as files, not a SQLite trace index.
- Traces use human-readable Markdown plus JSON sidecars.
- Full harness evals live under `dbrain eval research`.
- The first runner should live in a separate `internal/researchrun` package.
- Chat is the default web research surface; remove the Research tab from normal
  navigation.
- User-facing runs synthesize by default.
- Traces are saved by default while the harness is being tuned.
- Trace writes must be concurrency-safe and use per-run directories with atomic
  file replacement.
- Trace retention must be explicit and configurable from the first trace
  implementation.
- Minimal Harness Lab belongs with eval work, not after the whole runner is
  complete.
- Citation verification failures must be visible to the user and must not
  produce normal shareable answers.

Remaining questions:

- Which local embedding model and vector store should become the first optional
  semantic lane? Recommendation: do not choose yet. Build traces, evals, and the
  runner first, then compare lexical-only against hybrid retrieval on real
  local traces.
- Do the initial runner defaults above create enough useful answers without
  spending too much time on weak queries? Recommendation: ship those conservative
  defaults first, then let `dbrain eval research` and Harness Lab comparisons
  decide which thresholds need to move.
