---
name: dbrain-model-bakeoff
description: Use when comparing dbrain summary or categorization models, running read-only model bakeoffs across source-summary, categorize-source, and categorize-item modes, or producing a speed and quality comparison for Ollama, LM Studio, oMLX, OpenRouter, or configured OpenAI-compatible backends.
metadata:
  short-description: Compare dbrain summary and categorization models
---

# dbrain Model Bakeoff

Use the read-only bakeoff devtool to compare candidate models without mutating
the local dbrain database.

## Workflow

1. Work from the dbrain repo unless the user gives another path.
2. Verify the requested runner models are available. Use only the discovery
   commands that match the requested runners:

```bash
ollama list
curl -s http://localhost:1234/v1/models
curl -s -H "Authorization: Bearer $DBRAIN_OMLX_API_KEY" http://127.0.0.1:8000/v1/models
```

3. Pick representative targets from the local DB:
   - source-summary and categorize-source need `src:...` keys with extracted text
   - categorize-item needs item keys such as `x:...` with enough saved text,
     summaries, transcripts, OCR, or linked evidence to categorize
   - avoid tiny wrapper-only items when the user wants a meaningful quality
     comparison

4. Run all relevant modes with the same target set and models:

```bash
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup src:example \
  --model ollama/model-a \
  --model ollama/model-b \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-bakeoff.md
```

```bash
go run ./cmd/devtools/model_bakeoff \
  --mode categorize-source \
  --lookup src:example \
  --model ollama/model-a \
  --model ollama/model-b \
  --timeout 5m \
  --output /tmp/dbrain-categorize-source-bakeoff.md
```

```bash
go run ./cmd/devtools/model_bakeoff \
  --mode categorize-item \
  --lookup x:example \
  --model ollama/model-a \
  --model ollama/model-b \
  --timeout 5m \
  --output /tmp/dbrain-categorize-item-bakeoff.md
```

Use provider-qualified model names. Use `ollama/<model>` for local Ollama,
`lmstudio/<api-model-id>` for local LM Studio (the id returned by
`curl -s http://localhost:1234/v1/models`), `omlx/<model>` for oMLX, and
`<alias>/<model>` for aliases configured under `llm_backends.<alias>`. When the
oMLX server has auth enabled, set `DBRAIN_OMLX_API_KEY` or configure
`omlx.api_key` before running the bakeoff.

For local backend parity checks:

```bash
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup src:example \
  --model ollama/dbrain:2026042701 \
  --model lmstudio/qwen/qwen3.6-35b-a3b \
  --model omlx/Qwen3.6-35B-A3B-MLX-4bit \
  --parity-preset dbrain-modelfile \
  --timeout 5m \
  --output /tmp/dbrain-local-backends.md
```

For a configured alias:

```bash
go run ./cmd/devtools/model_bakeoff \
  --mode categorize-source \
  --lookup src:example \
  --model localai/test-model \
  --json > /tmp/dbrain-localai-bakeoff.json
```

5. Summarize the reports for the user:
   - success and error counts by model
   - average duration and output size
   - baseline word overlap where available
   - qualitative differences in summaries, categories, tags, entities, and
     failure modes
   - paths to the generated Markdown reports

## Notes

- The devtool opens the SQLite store read-only and sets categorization apply
  mode to false.
- Summary mode may still need write access to dbrain's configured temp
  directory because the existing model runner writes transient request files.
- If sandboxing blocks temp files or local Ollama access, rerun the exact
  bakeoff command with approval instead of changing the command semantics.
- Use `--json` when a structured result is easier to post-process.
- Use `--parity-preset dbrain-modelfile` only for explicit local-provider
  parity checks.
- Reports record provider, API model, transport, local/hosted flag, parameter
  strictness, prompt parity status, and runtime-context collection status.
- Run large local models in separate invocations when memory co-residency would
  bias timing.
