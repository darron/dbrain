# LM Studio Provider Design

## Status

Accepted direction: first-class `lmstudio/<model>` provider integration.

This document is a design/specification for implementation planning. It does not
change runtime behavior by itself.

## Problem

`dbrain` currently uses Ollama as its main local LLM runtime. The live
configuration points source summaries and categorization at
`ollama/dbrain:2026042701`, which is an Ollama model created from the repo
`Modelfile`. The summary model also powers other callers through
`summarizecli`, including research planning/synthesis, Apple Notes summaries,
and X media summaries. The user also runs LM Studio for other work and wants to
test whether LM Studio gives better performance without keeping the same model
loaded twice in two runtimes.

The important constraint is that this must be a fair runtime/provider test, not
an accidental prompt or model test. `dbrain` already sends task-specific prompts
for summaries, research planning, research synthesis, Apple Notes summaries,
X media summaries, and categorization. Those prompts should remain owned by
`dbrain`, not hidden inside a local LM Studio model wrapper. Fairness also
requires controlling the other behavior currently hidden in the Ollama
`Modelfile`: the durable system prompt and sampler parameters.

## Current State

Observed local runtime state on 2026-06-30:

- LM Studio CLI is installed at `/Users/darron/.lmstudio/bin/lms`.
- LM Studio server is running on port `1234`.
- LM Studio has `qwen/qwen3.6-35b-a3b` loaded with context `65536`.
- Ollama has overlapping Qwen 3.6 models downloaded but no model currently
  resident.
- `~/.config/dbrain/config.yaml` currently sets:
  - `summary.model: "ollama/dbrain:2026042701"`
  - `categorize.model: "ollama/dbrain:2026042701"`
  - `ocr.model: "openrouter/google/gemini-3.1-flash-lite-preview"`

Repo state:

- `Modelfile` uses `FROM qwen3.6:35b-a3b-nvfp4`, a durable dbrain-oriented
  `SYSTEM` prompt, and sampler parameters: `temperature 0.6`, `top_p 0.95`,
  `top_k 20`, `min_p 0`, `presence_penalty 1.5`, and `repeat_penalty 1`.
- Direct summaries route `ollama/*` to native Ollama `/api/chat` and
  `openrouter/*` to OpenAI-compatible chat completions.
- Categorization has separate native Ollama and OpenRouter paths.
- X photo OCR has native Ollama, OpenRouter, and Tesseract paths.
- `cmd/devtools/model_bakeoff` already compares source summary,
  source categorization, and item categorization across model strings.

## External Runtime Facts

LM Studio documentation confirms:

- LM Studio exposes OpenAI-compatible endpoints including `/v1/models` and
  `/v1/chat/completions`.
- OpenAI-compatible chat completions accept system messages and inference
  parameters such as `temperature`, `top_p`, `top_k`, `presence_penalty`,
  `repeat_penalty`, and `seed`.
- The local server examples assume port `1234` and base URL
  `http://localhost:1234/v1`.
- `lms load` can assign a custom model identifier with `--identifier` and can
  set context length, GPU offload, and TTL.
- LM Studio has per-model defaults and `model.yaml` for baked runtime metadata
  and defaults, but that is not a literal Ollama `Modelfile` replacement for
  dbrain task prompts.

References:

- https://lmstudio.ai/docs-md/developer/openai-compat
- https://lmstudio.ai/docs-md/developer/openai-compat/chat-completions
- https://lmstudio.ai/docs-md/cli/local-models/load
- https://lmstudio.ai/docs-md/app/advanced/per-model
- https://lmstudio.ai/docs-md/app/modelyaml

## Goals

1. Add first-class `lmstudio/<model-id>` support for local LM Studio inference.
2. Preserve accurate provider provenance in stored summaries, categorization
   results, reports, and operational output.
3. Keep dbrain task prompts, any parity base prompt, and sampler choices in
   dbrain code so Ollama and LM Studio tests compare equivalent requests.
4. Enable read-only bakeoffs comparing `ollama/dbrain:2026042701` with
   `lmstudio/qwen/qwen3.6-35b-a3b` or the confirmed LM Studio API model id on
   the same source/item targets.
5. Keep local-first behavior: no hosted calls, no background sidecars, no
   automatic mutation of external runtime state beyond optional operator-run
   LM Studio load/unload commands.

## Non-Goals

- Do not mutate the user's live dbrain config automatically.
- Do not remove Ollama support.
- Do not rewrite all provider code into a broad abstraction unless required by
  the scoped change.
- Do not require LM Studio for normal builds or tests.
- Do not publish or require a `model.yaml` artifact for v1.
- Do not claim LM Studio vision/OCR support until a real image request path is
  tested against the local server.
- Do not make dbrain start, stop, load, or unload LM Studio models implicitly in
  normal commands.
- Do not claim single-process timing is fair when two heavyweight local models
  are co-resident in Ollama and LM Studio.

## Design

### Model Naming

Add a provider-qualified model namespace:

```text
lmstudio/<model-id>
```

The provider prefix is stripped before sending the request. Examples:

```text
lmstudio/qwen/qwen3.6-35b-a3b -> qwen/qwen3.6-35b-a3b
lmstudio/dbrain-qwen          -> dbrain-qwen
```

The persisted display name remains the provider-qualified model string so
reports and stored rows distinguish LM Studio from Ollama:

```text
lmstudio/qwen/qwen3.6-35b-a3b
```

### Configuration

Add config keys:

```yaml
lmstudio:
  base_url: "http://127.0.0.1:1234/v1"
  api_key: "lm-studio"
```

Add env/config resolution:

```text
DBRAIN_LMSTUDIO_BASE_URL
DBRAIN_LMSTUDIO_API_KEY
```

`base_url` normalization should ensure a `/v1` suffix for chat completions. If
the user passes `http://127.0.0.1:1234`, dbrain should call
`http://127.0.0.1:1234/v1/chat/completions`.

`api_key` is not secret in the default local LM Studio setup, but support secret
refs consistently with other provider keys.

Do not route LM Studio through the existing `openai.*` or `OPENAI_BASE_URL`
configuration. Those should remain the generic external OpenAI-compatible
controls. A first-class LM Studio provider needs its own config surface so
preflight, error labels, provenance, and docs stay unambiguous.

The existing generic OpenAI-compatible summarize adapter can already be pointed
at an LM Studio server in some workflows. That is not sufficient for this work:
it does not give LM Studio-specific model parsing, config docs, preflight
behavior, or stored provider provenance.

### Prompt Ownership

Do not translate the repo `Modelfile` into a hidden LM Studio system prompt for
production dbrain calls.

Instead:

- Summary prompts remain in `internal/sourceenrich`.
- Research prompts remain in `internal/brainresearch`.
- Apple Notes prompts remain in `internal/applenotes`.
- X media summary prompts remain in `internal/xmediatranscribe`.
- Categorization prompts remain in `internal/itemcategorize`.

LM Studio receives these task prompts as normal chat `system` messages. This
matches how the direct Ollama path already sends system messages, and it keeps
the prompt layer auditable.

The repo `Modelfile` should be treated as an Ollama convenience wrapper. Its
sampler values and durable dbrain persona are part of the current production
Ollama setup, but v1 correctness should not depend on a local LM Studio prompt
wrapper.

The implementation must verify and document how Ollama combines a model
`SYSTEM` prompt from a `Modelfile` with a request-level `system` message sent to
`/api/chat`. The bakeoff design depends on the result:

- If the request-level system message fully overrides the `Modelfile` `SYSTEM`,
  LM Studio should receive only the same task prompt that Ollama receives.
- If Ollama composes or prepends the `Modelfile` `SYSTEM`, dbrain should make
  that durable prompt explicit for LM Studio parity runs. It should be added in
  application-controlled prompt construction, not stored as a hidden LM Studio
  prompt template.

For the cleanest provider/runtime comparison, a follow-up strict-parity bakeoff
may compare the same base Qwen model in both runtimes with all dbrain prompts
and sampler parameters sent explicitly by dbrain. The current-production
replacement bakeoff may still compare `ollama/dbrain:2026042701` against LM
Studio, but its report must state whether the Ollama `Modelfile` prompt was
active.

### Inference Parameters

Fair bakeoffs cannot rely on runtime defaults. The current direct summary and
categorization paths do not send sampler parameters, which means Ollama inherits
the `Modelfile` defaults while LM Studio inherits whatever per-model defaults
are configured in the app.

Add explicit local inference parameters for LM Studio and for bakeoff parity
paths. The dbrain local parity defaults should match the current `Modelfile`:

```yaml
temperature: 0.6
top_p: 0.95
top_k: 20
min_p: 0
presence_penalty: 1.5
repeat_penalty: 1
```

Direct Ollama requests should send the supported values through the native chat
options mechanism. LM Studio requests should send the supported
OpenAI-compatible chat-completions fields. If a parameter is not supported by
one runtime, omit it from both strict-parity requests or label it as omitted in
the bakeoff report; do not silently call the run fair.

These explicit local defaults should apply to the local direct paths used for
Ollama/LM Studio parity work. Do not change OpenRouter sampler behavior in this
feature unless a separate design accepts that blast radius. Do not silently
impose the dbrain `Modelfile` sampler preset on unrelated existing Ollama model
runs that are not part of LM Studio parity work.

Bakeoff reports should include the sampler parameters dbrain sent, model ids,
runtime labels, and context lengths where available. Operator-configured LM
Studio per-model defaults can still exist, but they should be treated as runtime
tuning and not as the authoritative source of dbrain prompt or sampler parity.

### Summary And Research Flow

Extend `internal/summarizecli`:

- Parse `lmstudio/` and `lmstudio:` model prefixes.
- Treat LM Studio as a direct summary provider.
- Send requests to `<lmstudio.base_url>/chat/completions`.
- Use the existing `messages` shape with dbrain's system prompt plus user
  content.
- Send the explicit local inference parameters used for parity runs.
- Parse OpenAI-compatible `choices[0].message.content`.
- Record:
  - `Tool: "lmstudio-direct"`
  - `ToolVersion: "lmstudio-direct-v1"`
  - `Model: "lmstudio/<model-id>"`

This covers source summaries, research planner calls, research synthesis, Apple
Notes summaries, X media summaries, answer review, and any other caller that
uses `summarizecli.Run` with a provider-qualified local model.

### Categorization Flow

Extend `internal/itemcategorize`:

- Parse `lmstudio/` and `lmstudio:` model prefixes.
- Add an LM Studio chat-completions path using the same JSON-only system prompt
  as Ollama/OpenRouter.
- Use `lmstudio.base_url` and `lmstudio.api_key`.
- Send the explicit local inference parameters used for parity runs.
- Preserve exact provider-qualified model provenance in results.

The existing categorization parser currently receives prefix-stripped provider
model names. LM Studio must not copy that behavior blindly. For v1,
`itemcategorize.Result.Model` is the authoritative categorization provenance
field and must retain the provider-qualified model string, for example:

```text
lmstudio/qwen/qwen3.6-35b-a3b
```

If the implementation adds `Tool` and `ToolVersion` fields to categorization
results, they should mirror the summary path, for example `lmstudio-direct` and
`lmstudio-direct-v1`; `Result.Model` should still remain provider-qualified.

Initial categorization should be text-first. If `IncludeImages` is set, the
implementation may support LM Studio image content only after a fixture-backed
unit test and a live smoke test confirm that the local LM Studio server handles
OpenAI-compatible image payloads for the selected model. If not confirmed,
return a clear unsupported-model/path error for `lmstudio/*` with images rather
than silently dropping images.

### OCR Flow

Do not include LM Studio OCR in the first implementation unless image support is
proved separately.

Rationale: prior local OCR work showed that accepting an image payload is not
the same as being competent at OCR. The current production OCR default is
OpenRouter Gemini Flash Lite, with Ollama and Tesseract alternatives. Keeping
OCR out of v1 avoids conflating text-runtime performance testing with vision
quality testing.

An implementation plan may include an explicit follow-up task to add
`lmstudio/*` to `internal/xphotoocr` only if the selected LM Studio model is
vision-capable and a negative-control OCR test passes.

### Bakeoff Flow

Extend `cmd/devtools/model_bakeoff` through the existing model string interface.
The command should accept LM Studio model strings in all current bakeoff modes:
`source-summary`, `categorize-source`, and `categorize-item`.

There are two different bakeoff procedures:

1. Functional provider smoke: a single invocation with multiple `--model` flags
   is acceptable when memory co-residency is acceptable. It proves routing,
   parsing, and report rendering.
2. Fair 35B timing/quality comparison: run one provider/model per process and
   avoid keeping the Ollama and LM Studio heavyweight copies resident at the
   same time. Compare the resulting reports side by side.

Use the LM Studio model id returned by `/v1/models`, not necessarily the model
key passed to `lms load`. On the current machine that id is
`qwen/qwen3.6-35b-a3b`, but examples should require substituting the confirmed
API model id:

```sh
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup <representative-src-key> \
  --model ollama/dbrain:2026042701 \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-ollama.md

go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup <representative-src-key> \
  --model lmstudio/<api-model-id-from-v1-models> \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-lmstudio.md
```

Repeat the isolated procedure for `categorize-source` using a representative
source key and for `categorize-item` using a representative item key.

The report should make provider/runtime differences obvious:

- model string
- status
- duration
- output characters
- error message
- baseline overlap
- raw response snippet where already available
- sampler parameters sent by dbrain
- runtime context where available, including LM Studio model id and context
  length

The testing procedure should be serialized:

1. Capture `ollama ps`.
2. Capture `/Users/darron/.lmstudio/bin/lms ps`.
3. Confirm the LM Studio API model id with `curl -s http://localhost:1234/v1/models`.
4. Run one provider/model path at a time for timing and quality comparisons.
5. Avoid running two heavyweight local models concurrently unless that is the
   explicit functional smoke test.
6. Include current model identifiers, sampler parameters, and context lengths
   in the final report.

### Operator Setup

Document a manual LM Studio setup path:

```sh
/Users/darron/.lmstudio/bin/lms server status
/Users/darron/.lmstudio/bin/lms ps
/Users/darron/.lmstudio/bin/lms ls
/Users/darron/.lmstudio/bin/lms load <model_key_from_lms_ls> --identifier qwen/qwen3.6-35b-a3b --context-length 65536 --gpu max
curl -s http://localhost:1234/v1/models
```

The exact `lms load` model key may differ from the API identifier shown by
`/v1/models`, so documentation should tell the operator to use `lms ls` and
`curl -s http://localhost:1234/v1/models` to confirm the actual API model id.

No dbrain command should run `lms load` implicitly in v1.

## Error Handling

LM Studio provider errors should be explicit and provider-labeled:

- missing/empty model id: `unsupported LM Studio model "lmstudio/"`
- connection refused: include base URL and provider label
- non-2xx response: include status and a short response prefix
- empty choices: `lmstudio summary: response contained no choices`
- empty content: `lmstudio summary: response contained no summary text`
- unsupported image path: `lmstudio categorization with images is not supported for this provider path`

Timeout behavior should follow existing per-call timeout options.

## Testing

Unit tests:

- Parse `lmstudio/<model>` and `lmstudio:<model>` in each package that parses
  provider strings.
- Resolve LM Studio base URL and API key from env and config.
- Normalize LM Studio base URLs with and without `/v1`.
- Send a summary request to a fake OpenAI-compatible server and assert:
  - URL path is `/v1/chat/completions`
  - model id has provider prefix stripped
  - system prompt is present
  - local inference parameters are present when parity defaults are enabled
  - authorization header is set when configured
  - result records `lmstudio-direct` provenance
- Send an item/source categorization request to a fake server and assert JSON
  parsing and provider-qualified `Result.Model` provenance.
- Assert categorization does not regress to passing prefix-stripped LM Studio
  model ids into `parseCategorizationJSON`.
- Assert OpenRouter preflight does not fire for `lmstudio/*`.
- Assert unsupported image categorization returns a clear error if image support
  is not implemented.

Implementation verification:

- Verify Ollama `Modelfile` `SYSTEM` plus request-level `system` behavior before
  running final bakeoffs, and record the result in docs or implementation notes.
- Confirm LM Studio accepts the explicit sampler fields that dbrain sends. Any
  unsupported parity field must be omitted from both runtimes or called out in
  the bakeoff report.

Read-only live smoke checks:

```sh
curl -s http://localhost:1234/v1/models

go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup <representative-src-key> \
  --model lmstudio/<api-model-id-from-v1-models> \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-lmstudio.md
```

Standard gates after implementation:

```sh
task fmt
task lint
task test-ci
task build
```

`task build` is included because CLI/config behavior changes materially.

## Documentation

Implementation should update:

- `config.yaml.sample`
- `README.md`
- `COMMANDS.md`
- `internal/app/env_docs.go`
- `skills/dbrain-model-bakeoff/SKILL.md` if bakeoff commands now support
  `lmstudio/*`
- `CHANGELOG.md`

This design document is spec-only and does not require a changelog entry by
itself. The implementation does.

Documentation should answer the Modelfile question directly:

> LM Studio cannot consume an Ollama `Modelfile` as the same kind of local model
> wrapper. The dbrain `Modelfile` currently bundles three concerns: base model
> selection, a durable dbrain system prompt, and sampler parameters. LM Studio
> can receive equivalent system instructions and sampler parameters per request,
> store per-model load/runtime defaults, customize prompt templates, and use
> `model.yaml` for portable model metadata/defaults. For dbrain, prompts and
> parity sampler settings stay in the application; LM Studio defaults are
> optional runtime tuning, not the authoritative prompt source.

## Acceptance Criteria

- A user can set `summary.model: lmstudio/qwen/qwen3.6-35b-a3b` and get source
  summaries through LM Studio.
- Research planner/synthesis and other `summarizecli` callers can use
  `lmstudio/*` through the same direct summary path.
- A user can set `categorize.model: lmstudio/qwen/qwen3.6-35b-a3b` and get
  text categorization through LM Studio.
- Stored summary/categorization provenance clearly says LM Studio, not Ollama
  or OpenRouter; categorization `Result.Model` keeps the `lmstudio/` prefix.
- Direct local Ollama and LM Studio parity runs send explicit sampler parameters
  or clearly label any unsupported/omitted parameter.
- The implementation verifies and documents whether Ollama request-level system
  prompts override or compose with the `Modelfile` `SYSTEM`.
- `model_bakeoff` can run LM Studio model strings in all existing modes.
- The documented fair 35B procedure uses isolated one-provider runs; mixed
  multi-model invocations are labeled as functional smoke tests unless memory
  co-residency is explicitly intended.
- Existing Ollama and OpenRouter behavior remains unchanged outside explicit
  LM Studio parity paths.
- Standard gates pass.

## Risks

- LM Studio model identifiers may differ between `lms ps`, `lms ls`, and
  `/v1/models`. The implementation and docs should prefer `/v1/models` for API
  model ids.
- Some sampler parameters may not be accepted by both runtimes. Bakeoff reports
  must capture what dbrain actually sent and avoid claiming strict parity when
  request parameters differ.
- LM Studio may accept but mishandle image payloads for a text model. OCR and
  image categorization must require explicit verification.
- Hidden per-model LM Studio prompt defaults could bias tests. The recommended
  setup is to keep LM Studio prompt defaults neutral and let dbrain send the
  task system prompt.
