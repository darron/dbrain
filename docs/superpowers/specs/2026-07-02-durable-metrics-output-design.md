# Durable Metrics Output Design

## Status

Proposed. This is a build-ready design for an opt-in local metrics file that
captures production timing data without requiring Prometheus, Grafana,
OpenTelemetry, a daemon, or a remote collector.

## Summary

`dbrain` should support an append-only JSON Lines metrics sink. When enabled,
commands write structured timing events to a local file, defaulting to the
configured log directory. The first implementation should focus on the paths
that answer the immediate local-model question:

- `sync all` run timing
- per-stage `sync all` timing
- categorization item/source timing
- source summary timing where the source worker performs a model summary
- model/provider/concurrency/timeout configuration attached to each relevant
  event

The format must be stable enough for `jq`, scripts, regression fixtures, and
future benchmark comparisons. It must not include prompts, source text,
summaries, tags, OCR text, transcripts, URLs, titles, API keys, or other
content-bearing fields.

## Problem

The current system has useful but insufficient timing signals:

- `syncjob.Stats` records stage durations in memory and prints a human summary.
- Scheduler logs print run duration and a few stage counts.
- Categorization progress logs can be manually inspected, but they are not a
  durable schema.
- `cmd/devtools/model_bakeoff` can produce read-only comparison reports, but it
  does not observe normal production sync runs.
- Research traces already carry compact metrics, but that is specific to the
  research harness.

This means backend comparisons such as Ollama vs LM Studio vs oMLX currently
depend on parsing logs and reconstructing timings after the fact. That is too
fragile. The system needs durable, machine-readable operational evidence.

## Goals

1. Make production timing comparisons possible after the run has finished.
2. Keep the sink local-first and opt-in.
3. Use a plain file format that can be parsed without new infrastructure.
4. Record enough provider/model/config context to compare local LLM runners.
5. Avoid capturing private corpus content.
6. Preserve the existing console output and scheduler status behavior.
7. Keep the first build small enough to ship and test.

## Non-Goals

- No Prometheus endpoint.
- No OpenTelemetry exporter.
- No background metrics daemon.
- No database schema migration for metrics.
- No web dashboard in v1.
- No raw prompt, evidence, summary, OCR, transcript, URL, or title capture.
- No automatic performance judgment. Metrics are evidence, not policy.

## Design Choice

### Approach 1: Append-Only JSONL Metrics Sink

This is the recommended approach.

Each metric event is one JSON object written as one line. The file can be
tailed while a run is active, concatenated across days, committed into test
fixtures when sanitized, or processed with `jq`.

Pros:

- trivial local deployment
- survives process restarts
- easy to append from CLI and scheduler paths
- supports both coarse run events and detailed per-item events
- easy to parse in Go tests and shell scripts
- does not require a DB migration or service dependency

Cons:

- no built-in aggregation
- file growth needs operator attention or optional rotation later
- concurrent writers need append-safe locking inside the process

### Approach 2: Write Only Final JSON Reports

Each command could write a single summary report at completion.

Pros:

- very simple for successful runs
- natural fit for `sync all --json`

Cons:

- loses evidence if the process crashes
- weak for long-running scheduler jobs
- awkward for concurrent per-item timing
- does not help much with “what was slow while it was running?”

### Approach 3: Store Metrics In SQLite

Metrics could be persisted as local DB rows.

Pros:

- queryable with SQL
- joins naturally with existing items and sources

Cons:

- adds migration and retention policy complexity
- pollutes the authoritative memory DB with operational telemetry
- increases write load during imports/enrichment
- harder to share sanitized timing fixtures

## Recommendation

Use Approach 1. Add a small metrics package and a JSONL writer. Keep SQLite out
of v1. Keep final reports and dashboards out of v1. Add a future importer or
summarizer only after the file schema has proven useful.

## Configuration

Add a top-level `metrics` config block:

```yaml
metrics:
  enabled: false
  path: "" # default: <log_dir>/metrics.jsonl when enabled
  detail: "stage" # stage | item | model_call
  include_subject_keys: false
  strict: false
```

Environment variables:

| Environment | Config key | Default | Purpose |
| --- | --- | --- | --- |
| `DBRAIN_METRICS_ENABLED` | `metrics.enabled` | `false` | Enable durable local metrics output. |
| `DBRAIN_METRICS_PATH` | `metrics.path` | `<log_dir>/metrics.jsonl` | Output JSONL file. Relative paths resolve under `log_dir`. |
| `DBRAIN_METRICS_DETAIL` | `metrics.detail` | `stage` | Detail level: `stage`, `item`, or `model_call`. |
| `DBRAIN_METRICS_INCLUDE_SUBJECT_KEYS` | `metrics.include_subject_keys` | `false` | Include raw dbrain source/item keys in metrics. Off by default. |
| `DBRAIN_METRICS_STRICT` | `metrics.strict` | `false` | Treat write failures as command failures after startup succeeds. |

Detail levels are cumulative:

- `stage`: run and stage events only
- `item`: `stage` plus per-item/per-source categorization and summary events
- `model_call`: `item` plus shared `llmclient.Chat` call events where the
  shared client path is used

The default should be `stage` because it gives useful production evidence with
low cardinality. For local backend bakeoffs, the operator should use `item` or
`model_call`.

## File Path Rules

If `metrics.enabled=true` and `metrics.path` is empty, write to:

```text
<cfg.LogDir>/metrics.jsonl
```

If `metrics.path` is relative, resolve it under `cfg.LogDir`. If it is
absolute, use it as-is.

The writer must create the parent directory with `0755` and open the file with:

```text
O_CREATE | O_APPEND | O_WRONLY
0644
```

Opening the file should happen before pipeline work starts. If the file cannot
be opened while metrics are enabled, fail the command before doing work.

## Error Policy

Startup open errors are fatal when metrics are enabled. This catches bad paths
or permissions before a long sync begins.

Runtime write errors should:

1. record one warning to the command/scheduler log
2. disable further writes for that sink instance
3. allow the pipeline to continue when `metrics.strict=false`
4. return an error when `metrics.strict=true`

This prevents an optional observability sink from corrupting import state while
still giving an operator a strict mode for benchmarking runs.

## Schema

All events use this envelope:

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "sync.stage.completed",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:37:33.450Z",
  "command": "sync all",
  "invocation": "cli",
  "status": "ok",
  "duration_ms": 139457
}
```

Required fields:

- `schema`: always `dbrain.metrics.v1` for this version
- `event`: stable event name
- `run_id`: stable identifier shared by events from one command run
- `emitted_at`: UTC RFC3339Nano timestamp
- `command`: user-facing command family such as `sync all`
- `invocation`: `cli`, `scheduler:startup`, `scheduler:interval`, or another
  explicit caller
- `status`: `started`, `ok`, `error`, or `skipped` as applicable

Duration fields:

- use integer milliseconds
- use `duration_ms` for the primary event duration
- use `timeout_ms` for configured timeouts
- do not encode Go `time.Duration` nanoseconds in the public metrics schema

Timestamps:

- use UTC
- use RFC3339Nano
- include `started_at` and `completed_at` where both are known

Counts:

- put stage-specific counts under `counts`
- use existing counter names where they are already clear
- prefer explicit names such as `item_queued`, `source_applied`, and
  `sources_summarized`

Config context:

- put command-relevant knobs under `config`
- include provider/model/concurrency/limit/timeout/image settings when relevant
- do not include secrets or headers

Subject identifiers:

- default: emit `subject_hash`, not raw item/source keys
- if `include_subject_keys=true`, also emit `subject_key`
- never emit title, URL, text, tags, summary, transcript, or OCR content

`subject_hash` should be deterministic for local comparisons:

```text
sha256("dbrain.metrics.v1" + "\0" + subject_kind + "\0" + subject_key)
```

This is not a strong privacy boundary for enumerable keys, but it prevents
accidental raw-key leakage in normal metrics files.

## Events

### `sync.run.started`

Written when `syncjob.Run` starts.

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "sync.run.started",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:34:23.000Z",
  "command": "sync all",
  "invocation": "scheduler:interval",
  "status": "started",
  "config": {
    "summary_model": "ollama/dbrain:2026042701",
    "categorize_model": "ollama/dbrain:2026042701",
    "categorize_concurrency": 2,
    "categorize_images": true,
    "source_concurrency": 4
  }
}
```

### `sync.stage.completed`

Written after each enabled `sync all` stage completes or errors.

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "sync.stage.completed",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:39:52.846Z",
  "command": "sync all",
  "invocation": "scheduler:interval",
  "stage": "categorize",
  "status": "ok",
  "started_at": "2026-07-02T22:37:33.389Z",
  "completed_at": "2026-07-02T22:39:52.846Z",
  "duration_ms": 139457,
  "counts": {
    "item_queued": 5,
    "item_applied": 5,
    "source_queued": 1,
    "source_applied": 1,
    "succeeded": 6,
    "skipped": 0,
    "errors": 0
  },
  "config": {
    "categorize_model": "ollama/dbrain:2026042701",
    "concurrency": 2,
    "limit": 0,
    "include_images": true,
    "timeout_ms": 90000
  }
}
```

### `sync.run.completed`

Written when `syncjob.Run` exits, including error exits when the run reached
the metrics sink.

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "sync.run.completed",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:41:38.350Z",
  "command": "sync all",
  "invocation": "scheduler:interval",
  "status": "ok",
  "started_at": "2026-07-02T22:34:23.000Z",
  "completed_at": "2026-07-02T22:41:38.350Z",
  "duration_ms": 435350,
  "counts": {
    "stages_completed": 7,
    "stages_error": 0
  }
}
```

### `categorize.item.completed`

Written at `metrics.detail=item` or higher when one item categorization finishes.

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "categorize.item.completed",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:38:26.120Z",
  "command": "sync all",
  "invocation": "scheduler:interval",
  "status": "ok",
  "subject_kind": "item",
  "subject_hash": "sha256:2d7f...",
  "duration_ms": 53000,
  "model": "ollama/dbrain:2026042701",
  "provider": "ollama",
  "api_model": "dbrain:2026042701",
  "transport": "ollama_chat",
  "tool": "ollama-direct",
  "output": {
    "categories": 4,
    "tags": 9
  },
  "config": {
    "include_images": true,
    "timeout_ms": 90000
  }
}
```

For errors, set `status="error"` and include:

```json
{
  "error": {
    "class": "timeout",
    "message": "context deadline exceeded"
  }
}
```

`message` must be capped to 300 characters and must not include request bodies.

### `categorize.source.completed`

Same as `categorize.item.completed`, with:

```json
{
  "event": "categorize.source.completed",
  "subject_kind": "source"
}
```

### `summary.source.completed`

Written at `metrics.detail=item` or higher when the source worker creates a
new model-backed source summary.

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "summary.source.completed",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:35:30.837Z",
  "command": "sync all",
  "invocation": "scheduler:interval",
  "status": "ok",
  "subject_kind": "source",
  "subject_hash": "sha256:6b4a...",
  "duration_ms": 45648,
  "model": "ollama/dbrain:2026042701",
  "tool": "ollama-direct",
  "output": {
    "summary_chars": 1180
  },
  "config": {
    "timeout_ms": 120000,
    "length": "standard"
  }
}
```

### `llm.call.completed`

Written only at `metrics.detail=model_call`. This event belongs in the shared
`internal/llmclient.Chat` path. It will not cover legacy external `summarize`
CLI calls unless those are later wrapped separately.

```json
{
  "schema": "dbrain.metrics.v1",
  "event": "llm.call.completed",
  "run_id": "sync_20260702T223423Z_8f4c2a1b",
  "emitted_at": "2026-07-02T22:38:26.120Z",
  "command": "sync all",
  "invocation": "scheduler:interval",
  "status": "ok",
  "task": "categorize",
  "duration_ms": 53000,
  "model": "ollama/dbrain:2026042701",
  "provider": "ollama",
  "api_model": "dbrain:2026042701",
  "transport": "ollama_chat",
  "local": true,
  "request": {
    "text_parts": 2,
    "image_parts": 0,
    "input_chars": 5432
  },
  "response": {
    "output_chars": 428
  },
  "config": {
    "timeout_ms": 90000,
    "response_contract": "json_prompt_only"
  }
}
```

This event is useful but should not be required for the first production
comparison. Per-item categorization events already answer most local backend
questions with less plumbing.

## Privacy Rules

The metrics file is local, but it should still be safe by default:

- Do not write prompt text.
- Do not write user/source text.
- Do not write summaries.
- Do not write OCR text.
- Do not write transcripts.
- Do not write generated tags/categories.
- Do not write URLs.
- Do not write titles.
- Do not write HTTP headers.
- Do not write API keys or secret refs.
- Do not write raw item/source keys unless `include_subject_keys=true`.

Allowed fields:

- provider-qualified model strings
- provider names
- transport names
- tool/tool version names
- counts
- durations
- concurrency, limits, timeout, include-images flags
- status and bounded error text
- content-size metadata such as input/output character counts
- hashed subject identifiers

## Internal Architecture

### New Package: `internal/metrics`

Responsibilities:

- parse metrics config from `runtimeenv`
- resolve the output path
- open and own the JSONL writer
- expose a concurrency-safe sink interface
- normalize event timestamps and duration fields
- hash subject identifiers
- cap error messages
- provide a no-op sink when disabled

Proposed types:

```go
package metrics

type Detail string

const (
	DetailStage     Detail = "stage"
	DetailItem      Detail = "item"
	DetailModelCall Detail = "model_call"
)

type Config struct {
	Enabled            bool
	Path               string
	Detail             Detail
	IncludeSubjectKeys bool
	Strict             bool
}

type Sink interface {
	Enabled() bool
	Detail() Detail
	Emit(Event) error
	Close() error
}

type Event map[string]any
```

The actual implementation may use concrete typed event structs instead of an
`Event` map if that keeps tests simpler. The public JSON field names in this
document are the contract. Do not use a nested catch-all `fields` object unless
the schema is deliberately changed; consumers should be able to filter top-level
fields such as `event`, `stage`, `duration_ms`, `model`, and `config`.

### Context Propagation

Add a metrics context object to the command options that already move through
the pipeline:

```go
type RunContext struct {
	RunID      string
	Command    string
	Invocation string
	Sink       metrics.Sink
}
```

Add it to:

- `syncjob.Options`
- `stageOptions`
- relevant `itemcategorize.Options`
- relevant `sourceenrich.Options`
- `llmclient.Request` only for `model_call` detail

Do not use global package state for the active metrics sink. Global state would
make tests brittle and would be wrong if multiple command runs ever execute in
one process.

### Run IDs

Generate run IDs at command entry:

```text
sync_<UTC basic timestamp>_<8 hex random chars>
```

Example:

```text
sync_20260702T223423Z_8f4c2a1b
```

Tests should inject a deterministic run ID.

### CLI Integration

`dbrain sync all` should:

1. load metrics config after `loadConfig`
2. create the sink after `cfg.EnsureDirs`
3. acquire the normal `sync all` lock
4. pass `RunContext{Command: "sync all", Invocation: "cli"}` into
   `syncOptionsFromFlags`
5. close the sink after the command returns

If lock acquisition fails and metrics are enabled, emit a skipped
`sync.run.completed` event before returning the existing lock error. This makes
overlapping scheduled/CLI runs visible in the metrics file without changing the
lock semantics.

`--json` should keep its current stdout behavior. Metrics always go to the
configured metrics file, not stdout.

### Scheduler Integration

The scheduler should reuse the same sink setup for scheduled `sync all` runs.
Its invocation values should be explicit:

- `scheduler:startup`
- `scheduler:interval`
- `scheduler:manual` if a manual trigger is later added

Each scheduled run gets a fresh run ID. The long-running scheduler must not keep
one shared run ID for the process lifetime.

### Stage Integration

`syncjob.Run` should emit:

- `sync.run.started`
- `sync.stage.completed` after each enabled stage
- `sync.run.completed`

The cleanest implementation is to add a small helper in `internal/syncjob` that
converts each stage struct into a metrics event. It should not rely on parsing
the existing human-readable progress strings.

Stage event conversion should cover these logical output stages:

- `apple_notes`
- `safari_tabs`
- `x_bookmarks`
- `x`
- `links`
- `x_media`
- `x_photo_ocr`
- `github`
- `youtube`
- `feeds`
- `sources`
- `categorize`
- `media_archive`
- `okf_export`

The existing plan stage `x_frontier` can run X bookmarks, X hydration, and link
extraction together, sometimes across multiple settle passes. Metrics should
not invent one ambiguous `x_frontier` stage. Emit logical events for the
populated `Stats.XBookmarks`, `Stats.X`, and `Stats.Links` fields using their
aggregated durations and counts.

If a logical stage has no natural start/completed timestamps today, use the
existing duration and the event emission time for `completed_at`. A later
improvement can add start timestamps to every stage struct.

### Categorization Detail Integration

Add `Duration time.Duration` and resolved provenance fields to:

- `itemcategorize.ItemResult`
- `itemcategorize.SourceResult`

Measure around `Run` and `RunSource` in `Batch` and `BatchSources`. Emit item
and source events from the existing `OnResult` and `OnSourceResult` hooks in
`syncjob.executeCategorizeStage` when the sink detail is `item` or
`model_call`.

The categorization events should include:

- `duration_ms`
- status
- subject kind/hash/key
- configured model
- resolved result model
- provider/API model/transport when available
- include-images flag
- timeout
- number of generated categories and tags, not their values

If the current `itemcategorize.Result` does not expose provider/API
model/transport, add those fields from the shared `llmclient.Response` rather
than reparsing model strings in the metrics layer.

### Source Summary Detail Integration

Add a lightweight callback to `sourceenrich.Options`:

```go
OnSourceResult func(SourceResult)
```

where `SourceResult` is an exported, content-safe result containing:

- source ID/key for hashing
- duration
- status/error
- whether extraction happened
- whether summary was created
- summary model/tool/tool version
- summary character count

`processSingleSource` already centralizes source enrichment. Measure at that
boundary and populate the result without exposing content. Emit
`summary.source.completed` only when a summary was actually created.

This can be implemented after categorization if the first build needs to be
smaller, but the spec treats it as part of the target feature because source
summary timing is one of the model-runner comparison paths.

### LLM Call Detail Integration

For `metrics.detail=model_call`, add optional metrics fields to
`llmclient.Request`:

```go
Metrics metrics.RunContext
```

`llmclient.Chat` should:

1. start a timer before provider resolution
2. resolve the provider target
3. send the request
4. emit `llm.call.completed` with status, duration, provider, transport,
   model, text/image part counts, approximate input chars, output chars, and
   timeout

This event should not include request/response bodies.

Because `llmclient` is a shared lower-level package, this must be optional and
no-op by default.

## Example Queries

Average categorize stage duration by configured model:

```bash
jq -s '
  [.[] | select(.event == "sync.stage.completed" and .stage == "categorize")]
  | group_by(.config.categorize_model)
  | map({
      model: .[0].config.categorize_model,
      runs: length,
      avg_duration_ms: ((map(.duration_ms) | add) / length),
      avg_successes: ((map(.counts.succeeded // 0) | add) / length)
    })
' ~/.local/share/dbrain/logs/metrics.jsonl
```

Categorization throughput per run:

```bash
jq '
  select(.event == "sync.stage.completed" and .stage == "categorize")
  | {
      run_id,
      model: .config.categorize_model,
      concurrency: .config.concurrency,
      duration_s: (.duration_ms / 1000),
      succeeded: .counts.succeeded,
      seconds_per_success: ((.duration_ms / 1000) / (.counts.succeeded // 1))
    }
' ~/.local/share/dbrain/logs/metrics.jsonl
```

Slowest item categorization calls:

```bash
jq -r '
  select(.event == "categorize.item.completed")
  | [.duration_ms, .run_id, .model, .status, .subject_hash]
  | @tsv
' ~/.local/share/dbrain/logs/metrics.jsonl | sort -nr | head
```

## Testing Requirements

### Unit Tests

Add tests for `internal/metrics`:

- disabled config returns a no-op sink
- enabled config resolves default path under `cfg.LogDir`
- relative paths resolve under `cfg.LogDir`
- invalid detail fails with a clear error
- writer appends one JSON object per line
- concurrent emits produce valid JSON lines
- subject keys are omitted by default
- subject keys are included only when enabled
- error messages are capped

### Sync Job Tests

Add focused tests in `internal/syncjob`:

- `sync.run.started` and `sync.run.completed` are emitted for a successful run
- each enabled fake stage emits one `sync.stage.completed`
- categorization stage metrics include model, concurrency, timeout, counts, and
  duration in milliseconds
- an erroring stage emits an error event if partial stats exist

Use fake sinks in tests. Do not depend on a real local model service.

### App/Scheduler Tests

Add tests in `internal/app`:

- `DBRAIN_METRICS_ENABLED=true` opens the sink for `sync all`
- `metrics.enabled: true` in config works through `runtimeenv`
- `DBRAIN_METRICS_PATH` overrides the default path
- scheduler runs use `scheduler:startup` or `scheduler:interval` invocation
- `--json` stdout output is unchanged when metrics are enabled

### Categorization Tests

Add tests in `internal/itemcategorize`:

- `ItemResult.Duration` is populated on success
- `ItemResult.Duration` is populated on error
- `SourceResult.Duration` is populated on success
- `SourceResult.Duration` is populated on error

Do not call real LLMs. Use existing stubs or injectable call functions if
needed.

### Privacy Tests

Add a test that serializes representative metrics and asserts the JSON does not
contain:

- sample URL
- sample title
- sample prompt phrase
- sample summary text
- sample tag value
- raw source key when `include_subject_keys=false`

This should live close to the metrics event rendering code.

## Documentation Requirements

Update:

- `config.yaml.sample`
- `README.md` config/env table
- `COMMANDS.md` with a short “collect local metrics” section
- `CHANGELOG.md` once implementation is complete

Include a short example for local model comparison:

```yaml
metrics:
  enabled: true
  detail: "item"
  path: "local-model-metrics.jsonl"
```

Then run one backend at a time:

```bash
dbrain sync all --categorize-limit 20 --categorize-images=false
```

The docs should tell operators to avoid running Ollama, LM Studio, and oMLX
with large models resident at the same time when collecting fair local timing
data.

## Acceptance Criteria

- Metrics are completely disabled by default.
- Enabling metrics writes JSONL to a local file.
- The default metrics file is under the configured log directory.
- `sync all` CLI writes run and stage events.
- Scheduled `sync all` writes run and stage events with scheduler invocation.
- `metrics.detail=item` writes per-item and per-source categorization timing.
- Source summary timing is emitted for newly created source summaries.
- `metrics.detail=model_call` emits shared `llmclient.Chat` timing without
  request or response bodies.
- Metrics include enough model/provider/config context to compare Ollama,
  LM Studio, oMLX, OpenRouter, and configured OpenAI-compatible aliases.
- Metrics do not include corpus content by default.
- Tests cover config parsing, file writing, sync integration, categorization
  duration capture, scheduler invocation, and privacy boundaries.
- Existing `sync all --json` output remains compatible.

## Implementation Notes

Build this in three commits:

1. `internal/metrics` package, config/env docs, and writer tests.
2. `sync all`/scheduler run and stage events.
3. categorization/source-summary/model-call detail events and privacy tests.

Do not start by instrumenting every package. The feature is successful when it
answers the production question “how long did this run/stage/item/model call
take under this backend config?” for the local model paths that are currently
being compared.
