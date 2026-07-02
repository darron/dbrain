# LLM Backend Abstraction Design

## Status

Accepted direction: generalize the LM Studio work into a small backend
abstraction so local runtimes, hosted services, and other OpenAI-compatible
endpoints can be added without copying provider branches through every task
package.

This is a follow-on design to
`docs/superpowers/specs/2026-06-30-lmstudio-provider-design.md`. The LM Studio
work remains the first concrete provider addition. This document describes the
next architectural step.

## Summary

`dbrain` should split model execution into three layers:

```text
research/chat, source summary, categorization, OCR, bakeoff
        |
        v
task wrappers that own prompts, parsing, persistence, and status semantics
        |
        v
shared LLM backend registry and transport client
```

The backend layer should know how to parse provider-qualified model strings,
resolve runtime config, apply provider capability rules, send chat requests,
and return exact provenance. It should not own task prompts, retrieval,
research planning semantics, citation verification, MCP schemas, or database
persistence.

The immediate proof point is adding `omlx/<model>` as another local
OpenAI-compatible backend after LM Studio. The same transport boundary must
also keep OpenRouter and other OpenAI-compatible endpoints first-class,
especially because OpenRouter-backed Gemini remains the practical OCR path.
Future OpenAI-compatible runtimes should then be mostly provider specs or
configured backend aliases plus focused tests.

## Problem

The LM Studio implementation made `lmstudio/<model>` first-class, but it also
showed that provider knowledge is still spread across the codebase:

- `internal/llmprovider` centralizes some model parsing and parity metadata,
  but the provider list is hard-coded.
- `internal/summarizecli` still branches by provider to resolve base URLs,
  API keys, tool names, and request shape.
- `internal/itemcategorize` duplicates OpenAI-compatible request construction
  and native Ollama request construction.
- `internal/modelbakeoff` depends on provider metadata for parity reports.
- Research planner, research synthesis, Apple Notes summaries, X media
  summaries, and answer review call through `summarizecli`, so they inherit
  whichever provider behavior `summarizecli` implements.
- MCP exposes the research pack as a tool surface, but it is not the owner of
  model execution.
- OpenRouter and generic OpenAI-compatible endpoints are already important
  hosted/provider paths, especially for OCR where hosted Gemini is currently
  cheaper and faster than the available local alternatives.

If `omlx/<model>` is added by copying the LM Studio branch, each future backend
will require the same scattered edits. That is already counterproductive for a
project whose local-model strategy is intentionally experimental.

## Current State

### Provider Helpers

`internal/llmprovider` currently has:

- provider constants for Ollama, OpenRouter, and LM Studio
- provider-qualified model parsing
- empty-provider-prefix detection
- the `dbrain-modelfile` parity preset
- provider-specific parity parameter accounting for Ollama and LM Studio

This is the right starting point, but it is not yet a registry. Adding another
provider still means editing provider loops and separate task packages.

### Direct Summary Calls

`internal/summarizecli` supports three direct provider paths:

- `ollama/<model>` through native `/api/chat`
- `openrouter/<model>` through OpenAI-compatible chat completions
- `lmstudio/<model>` through OpenAI-compatible chat completions

The package also still supports the external `summarize` CLI path when a direct
provider-qualified model is not selected. That fallback must stay intact.

### Categorization Calls

`internal/itemcategorize` has separate paths for:

- native Ollama chat
- OpenRouter OpenAI-compatible chat, including image parts
- LM Studio OpenAI-compatible chat, text-only for now

The LM Studio and OpenRouter request paths are structurally similar but
duplicated. Ollama differs mainly in endpoint, image serialization, and
options placement.

### Research And MCP

The research/chat harness has the right dependency direction:

```text
MCP / web chat / CLI research
        |
        v
internal/brainresearch and internal/researchrun
        |
        v
summarizecli model calls for planner, synthesis, and answer review
```

`internal/mcpserver` delegates `dbrain_research_pack` to
`internal/brainresearch`. MCP is a transport and tool contract, not a model
backend.

The model touchpoints are:

- research planner in `internal/brainresearch/planner.go`
- research synthesis in `internal/brainresearch/synthesize_run.go`
- answer review in `internal/researchrun/review.go`

All three call `summarizecli.Run`, so the backend abstraction should sit under
them. The research packages should not learn provider-specific HTTP details.

### Other Model-Adjacent Paths

Other model calls should be inventoried and either migrated or explicitly
deferred:

- `internal/applenotes/summary.go`: uses `summarizecli`
- `internal/xmediatranscribe/summary.go`: uses `summarizecli`
- `internal/ask/run.go`: legacy answer path using `summarizecli`
- `internal/app/categorize_vocab.go`: bespoke local Ollama request
- `internal/xphotoocr`: bespoke Ollama/OpenRouter/Tesseract OCR paths
- `cmd/devtools/ocr_model_compare`: OCR-specific comparison harness

The first abstraction pass should cover text chat model calls and
categorization. OCR can use the same foundations later, but OCR has separate
image, provider-moderation, local-fallback, and raw-evidence semantics.
Existing OpenRouter/Gemini OCR must continue to work when no local backend is
configured.

## External Runtime Context

oMLX is a plausible next local backend because its public documentation
describes it as an Apple Silicon/macOS-native MLX server with smart caching,
OpenAI-compatible `/v1/chat/completions`, Anthropic-compatible `/v1/messages`,
multi-model serving, and reuse of an existing LM Studio model directory. Its
source install instructions say it serves OpenAI-compatible clients on
`localhost:8000`.

Reference: https://omlx.ai/

For `dbrain`, the important fact is not oMLX's marketing claim. The important
fact is that oMLX can be treated initially as an OpenAI-compatible local chat
backend with distinct provider provenance.

## Goals

1. Make adding a known transport-compatible backend require a provider registry
   entry, docs, and focused tests instead of provider branches across all task
   packages.
2. Preserve exact provider/model provenance in stored summaries, item/source
   categorization, research traces, bakeoff reports, and user-facing output.
3. Keep task prompts owned by task packages, not hidden in backend runtime
   configuration.
4. Keep MCP as a transport/tool surface over the research harness, not a model
   execution backend.
5. Support `omlx/<model>` as the first new provider after LM Studio.
6. Keep existing Ollama, OpenRouter, LM Studio, generic OpenAI-compatible, and
   external `summarize` CLI behavior compatible.
7. Keep tests deterministic: no normal test should require a running local LLM,
   hosted API key, network access, or machine-specific path.

## Non-Goals

- Do not remove Ollama, OpenRouter, LM Studio, or the external `summarize` CLI
  fallback.
- Do not make dbrain start, stop, load, or unload local runtime models.
- Do not turn MCP into an internal client/server hop for web chat or CLI
  research.
- Do not rewrite retrieval, citation verification, trace persistence, or MCP
  schemas as part of backend abstraction.
- Do not force OCR and VLM paths into the first implementation if that would
  blur raw evidence handling or image capability semantics.
- Do not demote hosted OpenRouter/Gemini OCR. Local model flexibility is useful,
  but OCR should keep the fastest, cheapest reliable hosted path available.
- Do not introduce plugin loading or runtime-discovered Go code. Static
  provider registration plus config-defined OpenAI-compatible aliases are
  enough for the next several backends.
- Do not claim strict parity between runtimes unless the sent prompt, sampler
  values, context behavior, and reasoning controls are explicitly recorded.

## Design

### Concepts

Use these terms consistently:

- **Provider/backend**: the named runtime or hosted service in the model string,
  such as `ollama`, `lmstudio`, `omlx`, or `openrouter`.
- **Transport**: the wire protocol used to call the backend, such as native
  Ollama chat or OpenAI-compatible chat completions.
- **Task**: what dbrain is trying to do, such as source summary, categorization,
  research planning, research synthesis, answer review, or OCR.
- **Provenance model**: the provider-qualified model string stored in results,
  such as `omlx/qwen3.5-coder`.
- **API model**: the model id sent to the backend after stripping the dbrain
  provider prefix.

### Provider Registry

Evolve `internal/llmprovider` from constants and helper functions into a
registry with built-in provider specs and config-defined OpenAI-compatible
aliases.

Representative shape:

```go
type Transport string

const (
    TransportOllamaChat Transport = "ollama_chat"
    TransportOpenAIChat Transport = "openai_chat_completions"
    TransportAnthropicMessages Transport = "anthropic_messages" // deferred
)

type ProviderSpec struct {
    ID              Provider
    DisplayName     string
    Transport       Transport
    Local           bool
    DefaultBaseURL  string
    DefaultAPIKey   string
    BaseURLEnvKeys  []string
    APIKeyEnvKeys   []string
    RequiresAPIKey  bool
    HeaderPolicy    HeaderPolicy
    ToolName        string
    ToolVersion     string
    ParamPolicy     ParamPolicy
    PromptPolicy    PromptPolicy
    ReasoningPolicy ReasoningPolicy
    Capabilities    Capabilities
}

type Capabilities struct {
    Text           bool
    Images         CapabilityStatus
    JSONMode       CapabilityStatus
    ToolCalling    CapabilityStatus
    ReasoningCtl   CapabilityStatus
}
```

`CapabilityStatus` should distinguish at least `supported`, `unsupported`, and
`model_dependent_or_unverified`. That avoids pretending every Ollama,
OpenRouter, LM Studio, or oMLX model supports images just because the transport
can carry image payloads.

The registry must also own every provider-specific policy that would otherwise
become a switch in task packages or bakeoff:

- sampler parameter policy
- prompt-parity status
- reasoning/think-mode status
- transport
- local/hosted flag
- direct tool name and tool version
- provider-specific header policy

Initial provider specs:

```text
ollama
  transport: ollama_chat
  local: true
  default_base_url: http://127.0.0.1:11434
  base_url_env: DBRAIN_OLLAMA_BASE_URL, OLLAMA_BASE_URL, OLLAMA_HOST
  default_api_key: ollama
  api_key_env: DBRAIN_OLLAMA_API_KEY, OLLAMA_API_KEY
  requires_api_key: false
  tool: ollama-direct / ollama-direct-v1

openrouter
  transport: openai_chat_completions
  local: false
  default_base_url: https://openrouter.ai/api/v1
  base_url_env: DBRAIN_OPENROUTER_BASE_URL, OPENROUTER_BASE_URL
  api_key_env: DBRAIN_OPENROUTER_API_KEY, OPENROUTER_API_KEY
  requires_api_key: true
  headers:
    referer_env: DBRAIN_OPENROUTER_REFERER, OPENROUTER_HTTP_REFERER
    title_env: DBRAIN_OPENROUTER_TITLE, OPENROUTER_X_TITLE
    user_agent_env: DBRAIN_USER_AGENT
    summary_defaults: no referer/title unless configured
    categorize_defaults: https://local.dbrain / dbrain categorize
  tool: openrouter-direct / openrouter-direct-v1

lmstudio
  transport: openai_chat_completions
  local: true
  default_base_url: http://127.0.0.1:1234/v1
  base_url_env: DBRAIN_LMSTUDIO_BASE_URL
  default_api_key: lm-studio
  api_key_env: DBRAIN_LMSTUDIO_API_KEY
  requires_api_key: false
  tool: lmstudio-direct / lmstudio-direct-v1

omlx
  transport: openai_chat_completions
  local: true
  default_base_url: http://127.0.0.1:8000/v1
  base_url_env: DBRAIN_OMLX_BASE_URL
  default_api_key: ""
  api_key_env: DBRAIN_OMLX_API_KEY
  requires_api_key: false
  tool: omlx-direct / omlx-direct-v1
```

If live oMLX testing shows a different desktop-app default port or API-key
expectation, update only the oMLX provider spec and docs.

### Configured OpenAI-Compatible Aliases

Built-in providers are not enough for the long-term goal. dbrain should also
support explicitly configured OpenAI-compatible backend aliases without adding
new task-package code.

Representative config:

```yaml
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: env:LOCALAI_API_KEY
    local: true
```

Then callers can use:

```text
localai/<model-id>
```

Rules:

- configured aliases must not override built-in provider ids
- aliases use the same model parsing as built-in providers
- aliases preserve exact provenance as `<alias>/<model-id>`
- aliases initially support only OpenAI-compatible chat completions
- aliases must declare whether they are local or hosted
- aliases use the same empty-API-key behavior as built-in providers: omit the
  `Authorization` header when no key is resolved
- aliases are optional and should not be required for existing OpenRouter,
  Ollama, LM Studio, or oMLX behavior

This is config-driven backend registration, not dynamic plugin loading. It
keeps the code path fixed while allowing operators to test vLLM, LocalAI,
llama.cpp server, a custom OpenAI-compatible proxy, or another hosted
OpenAI-compatible endpoint without changing summary, categorization, research,
or bakeoff task code.

### Model Parsing

`llmprovider.ParseModelRef` should iterate registry entries rather than a
hard-coded provider slice. It should preserve current behavior:

- accept `provider/model` and `provider:model`
- strip provider prefix only for recognized providers
- preserve the original model string for provenance
- fail closed on recognized empty prefixes such as `omlx/`
- leave unqualified model strings as plain model refs

The parser should expose enough metadata for callers:

```go
type ModelRef struct {
    Original          string
    Provider          Provider
    APIModel          string
    ProviderQualified bool
    Spec              *ProviderSpec
}
```

If a pointer in `ModelRef` creates awkward copies or nil handling, expose a
`Spec(ref.Provider)` lookup instead.

Phase 1 should make `ParseModelRef` the authoritative provider parser. Remove
or reduce the duplicate Ollama/OpenRouter parsers in `summarizecli` and
`itemcategorize` after adding parity tests that prove the registry parser keeps
the same behavior for:

- `ollama/<model>` and `ollama:<model>`
- `openrouter/<model>` and `openrouter:<model>`
- `lmstudio/<model>` and `lmstudio:<model>`
- empty provider prefixes such as `ollama/`, `openrouter:`, and `lmstudio/`
- unqualified model names

Unqualified model names stay task-specific:

- summary paths preserve the existing external `summarize` CLI fallback
- categorization preserves the existing plain-model-as-OpenRouter fallback
- bakeoff records plain models with empty provider/transport metadata unless a
  task-specific fallback resolves them to OpenRouter

### Runtime Config Resolution

Move provider-specific URL/API-key resolution out of task packages.

Add a resolver that takes:

- root dir
- explicit environment map
- model string
- optional per-call overrides

and returns a resolved backend target:

```go
type Target struct {
    Ref         ModelRef
    Spec        ProviderSpec
    BaseURL     string
    APIKey      string
    Headers     map[string]string
    DisplayName string
}
```

The resolver should:

- normalize OpenAI-compatible base URLs to the provider's required suffix
- normalize Ollama native base URLs without `/v1` for `/api/chat`
- load secret refs only for the selected provider
- enforce required API keys only for providers that need them
- attach OpenRouter-specific `HTTP-Referer`, `X-Title`, and user-agent headers
- avoid reading provider secrets for unrelated providers
- omit the `Authorization` header entirely when the resolved API key is empty
- preserve current OpenRouter header defaults by task: summary sends
  referer/title only when configured; categorization keeps its current
  `https://local.dbrain` and `dbrain categorize` defaults unless explicitly
  overridden

This removes the repeated provider config logic currently living in
`summarizecli` and `itemcategorize`.

The migration has to handle two current configuration shapes:

- `summarizecli` resolves provider settings from an env map plus runtime config
  via `envWithRuntimeConfig`.
- `itemcategorize.Options` carries explicit provider fields such as
  `OpenRouterBase`, `OpenRouterKey`, `OllamaBase`, `LMStudioBase`, and related
  header values.

`ResolveTarget` should accept both runtime config/env and explicit per-call
overrides. `itemcategorize.Run` and `RunSource` should keep their public options
compatible by translating existing provider fields into resolver overrides
rather than forcing all callers to change at once.

### Shared LLM Client

Add a small task-neutral client package, likely `internal/llmclient`.

The client should expose one primary operation for chat-style model calls:

```go
type Request struct {
    Model            string
    Messages         []Message
    SamplerParams    map[string]any
    ResponseContract ResponseContract
    Timeout          time.Duration
    Task             Task
}

type Message struct {
    Role  string
    Parts []ContentPart
}

type ContentPart struct {
    Type      ContentType
    Text      string
    ImageData []byte
    MIMEType  string
}

type Response struct {
    Text            string
    RawJSON         string
    Model           string // provider-qualified provenance model
    Provider        llmprovider.Provider
    APIModel        string
    Transport       llmprovider.Transport
    Tool            string
    ToolVersion     string
    ParamAccounting llmprovider.ParamAccounting
}
```

`Messages` is the canonical prompt shape. Task packages may use local helper
functions to create a system message and user message, but `llmclient.Request`
should not carry parallel `SystemPrompt` and `UserText` fields that can
contradict `Messages`.

`SamplerParams` is the requested flat input map for a call. Provider policy
decides which values are sent or omitted. The returned `ParamAccounting` records
requested, sent, omitted, and strictness for bakeoff/reporting. Do not use the
current `ParityParams` audit/report shape as the normal request carrier.

For the first pass, `ResponseContract` should mean prompt/parse discipline only.
Do not set provider-native JSON mode or response-format fields unless a
provider/model capability has been verified and explicitly enabled.

`Response.Model` is always the provider-qualified provenance model that task
wrappers should persist. `Response.APIModel` is the stripped backend model id
sent over the wire.

Task packages should not need to know whether the selected backend is Ollama
native or OpenAI-compatible. They should provide prompts, user content,
optional images, and the model string.

The client owns:

- model parsing
- target resolution
- HTTP request construction
- transport-specific serialization
- transport-specific response extraction
- auth headers and provider headers
- capability enforcement
- transport error wrapping
- exact backend provenance
- direct backend tool identity and tool version

The client does not own:

- source-summary prompt construction
- categorization JSON schema or parse/repair logic
- research planner prompt
- research synthesis prompt
- answer review prompt
- OCR status semantics
- database writes
- trace persistence

Direct backend tool names and versions should come from the provider registry
and preserve existing values:

- `ollama-direct` / `ollama-direct-v1`
- `openrouter-direct` / `openrouter-direct-v1`
- `lmstudio-direct` / `lmstudio-direct-v1`
- `omlx-direct` / `omlx-direct-v1`

The external `summarize` CLI fallback remains wrapper-owned and should keep its
existing tool identity behavior.

### Transport Implementations

Start with two transports:

1. `ollama_chat`
   - endpoint: `/api/chat`
   - messages: native Ollama chat messages
   - images: base64 images on user messages
   - sampler params: under `options`
   - reasoning control: keep current `think=false` behavior for known direct
     calls unless a later policy changes it

2. `openai_chat_completions`
   - endpoint: `/chat/completions` under normalized `/v1`-style base URL
   - messages: OpenAI-compatible chat messages
   - images: content parts when supported by the caller and provider policy
   - sampler params: top-level request fields where verified
   - provider headers: OpenRouter adds referer/title/user-agent; local
     providers do not need those headers

Leave `anthropic_messages` as an explicit future transport. oMLX advertises an
Anthropic-compatible `/v1/messages` endpoint, but dbrain does not need it to
support oMLX for current summary, categorization, and research tasks.

### Task Wrappers

#### summarizecli

Keep `summarizecli.Run` as the compatibility boundary for existing callers.

For recognized provider-qualified models:

- call `llmclient`
- convert `llmclient.Response` into `model.SummaryResult`
- preserve current tool names and versions where possible
- keep length/language prompt hints in `summarizecli`

For non-direct calls:

- preserve the existing external `summarize` CLI behavior
- keep `ResolveCLIProvider` semantics
- keep `resolveModelAndEnv` compatibility for legacy Ollama-as-OpenAI CLI
  flows until a separate cleanup is planned

The legacy `resolveModelAndEnv` rewrite is separate from direct provider calls.
It exists for the external `summarize` CLI path and must not be removed or
expanded as a side effect of adding `llmclient`.

This lets research, Apple Notes, X media summaries, legacy ask, and source
summaries inherit new backends without changing their prompt logic.

#### itemcategorize

Move provider-specific HTTP calls from `internal/itemcategorize` into
`llmclient`.

`itemcategorize` should own:

- content bundle construction
- category vocabulary prompt section
- JSON-only system prompt
- image inclusion decision
- categorization JSON parsing and validation
- result application to items/sources

`llmclient` should own the provider call. If a user requests images with a
backend whose image support is unsupported or unverified, return a clear error
that names the provider and model.

#### research and MCP

Do not add provider logic to `internal/brainresearch`, `internal/researchrun`,
or `internal/mcpserver`.

Research planner, synthesis, and answer review should continue to call
`summarizecli` unless a later refactor makes direct `llmclient` calls cleaner.
Because `summarizecli` becomes backend-aware through the registry, these paths
automatically support `omlx/<model>`:

- `dbrain research ... --planner-model omlx/<model>`
- `dbrain research ... --model omlx/<model>`
- `dbrain research ... --answer-review-model omlx/<model>`
- MCP `dbrain_research_pack` with `planner_model: "omlx/<model>"`

MCP callers still bring their own external model for agent reasoning. dbrain
only controls dbrain-owned model calls such as optional query planning.

#### model_bakeoff

Keep bakeoff model selection as provider-qualified model strings.

Update bakeoff metadata to include:

- provider
- transport
- API model id
- provider-local/hosted flag
- requested/sent/omitted params
- parameter strictness
- prompt parity status
- reasoning-control status

These fields should be derived from `llmprovider` registry/policy metadata, not
from separate provider switches in `internal/modelbakeoff`. The goal is that a
new OpenAI-compatible local backend does not require provider-dispatch changes
in `summarizecli`, `itemcategorize`, or `modelbakeoff`.

Add a `Transport` field to `ModelRun` and render it in Markdown reports. The
implementation must make an explicit schema-version decision:

- if `model_bakeoff.v2` has only existed on this unmerged branch, fold the
  transport field into v2 before merge
- if v2 has already been consumed outside the branch, bump to
  `model_bakeoff.v3`

Add research-specific bakeoff/eval coverage only if it stays retrieval-stable.
For research tests, exact prose should not be the primary assertion. Stable
assertions are planner mode, planner model, query family, evidence counts,
answer status, citation verification, and trace metadata.

#### categorize vocab

`dbrain categorize vocab` currently requires local Ollama. It is a text-only
local model call and is a good second migration target after summary and
categorization.

Do not block the first abstraction on this path, but the implementation plan
should explicitly either migrate it or leave a tracked deferred item. Its help
text should not imply backend flexibility until migrated.

#### OCR and VLM

Keep `internal/xphotoocr` separate in the first pass unless implementation
turns out to be trivial after `llmclient` supports images.

OCR has additional product semantics:

- raw OCR text is evidence and must be preserved
- hosted moderation failures should prefer local fallback
- Tesseract is not an LLM backend
- image transport support does not prove the selected model is a good OCR model

The shared client should be designed so OCR can adopt it later for Ollama,
OpenRouter, LM Studio VLMs, or oMLX VLMs without changing OCR status policy.

### Research-Specific Model Defaults

Current CLI flags already support separate research models:

- `--planner-model`
- `--model` for synthesis
- `--answer-review-model`

The abstraction should not require a new config section to work. A follow-up
config improvement can add:

```yaml
research:
  planner_model: ""
  synthesis_model: ""
  answer_review_model: ""
```

with env keys:

```text
DBRAIN_RESEARCH_PLANNER_MODEL
DBRAIN_RESEARCH_SYNTHESIS_MODEL
DBRAIN_RESEARCH_ANSWER_REVIEW_MODEL
```

Resolution order should be:

1. explicit CLI/tool argument
2. research-specific config/env
3. `summary.model` / `DBRAIN_SUMMARY_MODEL`
4. unavailable, with the existing no-model behavior

This is useful but separable. The first backend abstraction should not get
stuck on research config plumbing if `--model` and `--planner-model` already
cover testing.

### oMLX Provider

Add `omlx/<model>` as the first new backend using the registry.

Initial behavior:

- transport: OpenAI-compatible chat completions
- default base URL: `http://127.0.0.1:8000/v1`
- env/config keys: `DBRAIN_OMLX_BASE_URL`, `DBRAIN_OMLX_API_KEY`,
  `omlx.base_url`, `omlx.api_key`
- API key: optional by default
- text: supported
- image support: unverified/model-dependent, reject by default for image
  categorization until a real oMLX VLM smoke path is tested
- parity policy: same local OpenAI-compatible non-strict policy as LM Studio
  until oMLX sampler behavior is verified

Examples:

```yaml
summary:
  model: omlx/qwen3.5-coder

categorize:
  model: omlx/qwen3.5-coder

omlx:
  base_url: http://127.0.0.1:8000/v1
  api_key: ""
```

```sh
dbrain research "What do I know about local models?" \
  --planner-model omlx/qwen3.5-coder \
  --model omlx/qwen3.5-coder
```

## Migration Plan

### Phase 1: Registry

- Expand `internal/llmprovider` into a registry.
- Preserve all existing parser behavior.
- Add built-in provider specs for Ollama, OpenRouter, LM Studio, and oMLX.
- Add config-defined OpenAI-compatible alias support.
- Move sampler, prompt-parity, reasoning-mode, transport, local/hosted, header,
  and tool identity policy to provider specs or provider-spec-aware helpers.
- Add tests for parsing, empty provider prefixes, specs, base URL defaults,
  configured aliases, and parity policy.

### Phase 2: Shared Client

- Add `internal/llmclient`.
- Implement `ollama_chat` and `openai_chat_completions` transports.
- Add httptest coverage for:
  - Ollama `/api/chat`
  - OpenRouter `/chat/completions`
  - LM Studio `/v1/chat/completions`
  - oMLX `/v1/chat/completions`
  - configured OpenAI-compatible alias `/v1/chat/completions`
  - missing required API key
  - optional local API key with no `Authorization` header when empty
  - unsupported images
  - response with no choices/no content
  - resolver does not read secret refs for unrelated providers

### Phase 3: summarizecli

- Route direct provider-qualified models through `llmclient`.
- Preserve external `summarize` CLI fallback behavior.
- Preserve summary behavior for unqualified models; do not start routing plain
  summary models through the direct OpenAI-compatible client accidentally.
- Preserve current summary result fields and tool names.
- Add direct summary tests for `omlx/<model>`.
- Keep source summary, research synthesis, Apple Notes summary, X media
  summary, and legacy ask tests passing without direct changes to those
  callers.

### Phase 4: itemcategorize

- Replace provider-specific HTTP functions with `llmclient`.
- Preserve plain-model-as-OpenRouter fallback for categorization.
- Preserve categorization JSON parsing and result provenance.
- Add `omlx/<model>` text categorization tests.
- Preserve current image behavior for Ollama/OpenRouter and explicit rejection
  for unverified local OpenAI-compatible image paths. Unsupported-image errors
  should name the provider and model.

### Phase 5: Research, MCP, And Bakeoff Coverage

- Add research runner tests showing `omlx/<model>` works for synthesis through
  the direct summary path.
- Add MCP research-pack planner test with `planner_model: "omlx/<model>"`.
  Use a temp root with `omlx.base_url` pointed at an `httptest` server, assert
  the mock endpoint was hit, and assert planner metadata records `omlx/<model>`.
- Update model bakeoff provider metadata for transport and oMLX.
- Add docs and examples for backend bakeoffs across Ollama, OpenRouter, LM
  Studio, oMLX, and a configured OpenAI-compatible alias.

### Phase 5b: Docs And Config Surfaces

- Update `internal/app/env_docs.go`.
- Update `config.yaml.sample`.
- Update `README.md`.
- Update `COMMANDS.md`.
- Update `CHANGELOG.md`.
- Add or update tests that verify env-doc output includes `DBRAIN_OMLX_*`,
  any configured-backend documentation, and existing OpenRouter/OCR entries.

### Phase 6: Deferred Call-Site Cleanup

- Decide whether `categorize vocab` should move to `llmclient`.
- Decide whether `xphotoocr` should move its Ollama/OpenRouter LLM calls to
  `llmclient` while keeping Tesseract separate.
- Do not advertise oMLX OCR/VLM support until a real VLM request path is
  tested.
- Keep current OpenRouter/Gemini OCR working even if OCR migration is deferred.
- If `categorize vocab` remains Ollama-only, add/keep a test proving its help
  text still says Ollama-only.

## Testing Requirements

Required local tests for implementation:

```sh
go test ./internal/llmprovider
go test ./internal/llmclient
go test ./internal/summarizecli
go test ./internal/itemcategorize
go test ./internal/sourceenrich
go test ./internal/applenotes
go test ./internal/xmediatranscribe
go test ./internal/ask
go test ./internal/xphotoocr
go test ./internal/brainresearch
go test ./internal/researchrun
go test ./internal/mcpserver
go test ./internal/modelbakeoff
go test ./internal/app
task fmt
task lint
task test-ci
task build
```

No test should require:

- a live Ollama server
- a live LM Studio server
- a live oMLX server
- OpenRouter network access
- real provider API keys
- local corpus-specific data
- machine-specific developer home-directory paths

Use `httptest` servers and env/config overrides.

## Acceptance Criteria

- `llmprovider.ParseModelRef("omlx/foo")` returns provider `omlx`, API model
  `foo`, and original model `omlx/foo`.
- A configured OpenAI-compatible alias such as `localai/<model>` resolves to
  its configured endpoint, preserves `localai/<model>` as provenance, and does
  not require task-package provider branches.
- `summary.model: omlx/<model>` routes source summaries, Apple Notes summaries,
  X media summaries, research synthesis, and answer review through the oMLX
  OpenAI-compatible transport when those paths use `summarizecli`.
- Existing summary behavior for unqualified models continues to use the
  external `summarize` CLI path.
- `categorize.model: omlx/<model>` works for text-only item/source
  categorization.
- Existing unqualified categorization models continue to route through
  OpenRouter, and existing OpenRouter image categorization behavior is
  preserved.
- Existing OpenRouter/Gemini OCR continues to work without configuring any
  local backend.
- `dbrain research --planner-model omlx/<model> --model omlx/<model>` works
  against mocked local endpoints in tests and preserves provider-qualified
  provenance.
- MCP `dbrain_research_pack` remains a research-pack tool, not an LLM backend
  API; it can use `omlx/<model>` only for the optional planner model.
- Bakeoff reports include provider, transport, API model, parameter strictness,
  and prompt/reasoning parity status for oMLX.
- Bakeoff reports get a `Transport` field through an explicit schema-version
  decision.
- Existing Ollama, OpenRouter, LM Studio, generic OpenAI-compatible, and
  external `summarize` CLI behavior remains compatible.
- Adding another OpenAI-compatible backend after oMLX requires no changes to
  `internal/summarizecli`, `internal/itemcategorize`, or `internal/modelbakeoff`
  provider dispatch.
- The resolver for one provider does not load secret refs for unrelated
  providers.
- OpenAI-compatible requests omit `Authorization` when the selected provider or
  alias resolves to an empty API key.

## Risks

- **Over-abstraction:** A generic plugin system would be larger than the
  problem. Keep the registry static and transports few.
- **False capability claims:** Transport support is not model support. Images,
  JSON mode, tools, and reasoning controls must be capability-gated and
  labelled as unverified when appropriate.
- **Provenance regression:** Normalizing everything to "openai-compatible"
  would hide which runtime produced stored summaries. Always preserve
  provider-qualified model strings.
- **Research semantic drift:** Research quality should not change just because
  model transport code moved. Research evals should verify planner mode,
  evidence shape, answer status, and citation verification.
- **Config sprawl:** New provider config blocks are justified for first-class
  provenance. Configured OpenAI-compatible aliases are allowed because generic
  endpoint support is already a real use case, but they should be limited to
  explicit aliases and the OpenAI-compatible chat transport.
- **Hosted regression:** The project already relies on OpenRouter/Gemini for
  OCR. Local-backend work must not make hosted OpenAI-compatible paths harder
  to configure or accidentally require a local runtime.
- **oMLX default uncertainty:** Public docs mention `localhost:8000` for source
  serving. If the macOS app uses a different default, the provider spec and docs
  must be corrected after live inspection.

## First-Pass Decisions

- Research-specific model defaults are deferred. The first pass relies on the
  existing CLI/tool model arguments and the existing `summary.model` fallback.
- Existing OpenRouter/Gemini OCR and hosted OpenAI-compatible behavior are
  compatibility requirements for the first pass.
- Config-defined OpenAI-compatible backend aliases are in scope for the first
  implementation because generic endpoint support already matters.
- `categorize vocab` is deferred unless the implementation becomes trivial
  after `llmclient` exists. Its help text must remain honest if it stays
  Ollama-only.
- oMLX support is text-only in the first pass. VLM/OCR support requires a real
  local smoke test with an oMLX VLM model.
- Provider health checks are deferred. The first pass should keep diagnostics
  focused on clear request-time errors and deterministic unit tests.

## Recommended Implementation Boundary

Implement the registry, shared client, `omlx/<model>` support, direct summary,
text categorization, config-defined OpenAI-compatible aliases, research
inheritance through `summarizecli`, OpenRouter and hosted OpenAI-compatible
compatibility, bakeoff metadata, docs, and tests.

Defer research-specific config defaults, `categorize vocab`, OCR/VLM, and
health-check tooling unless implementation shows they are small and low-risk.
