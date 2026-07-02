# LM Studio Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add first-class `lmstudio/...` model support for local summary and text categorization, plus bakeoff reporting that can compare LM Studio and Ollama without hiding provider, prompt, sampler, or runtime differences.

**Architecture:** Keep the existing provider boundaries: `internal/summarizecli` owns direct summary calls, `internal/itemcategorize` owns item/source categorization calls, and `internal/modelbakeoff` owns comparison reports. Add a small provider/parity helper package so LM Studio parsing and bakeoff parameter accounting are consistent without rewriting every provider path.

**Tech Stack:** Go, `net/http`, local SQLite read-only store access, Ollama native `/api/chat`, LM Studio OpenAI-compatible `/v1/chat/completions`, existing `task` gates.

---

## Source Spec

Implement against:

- `docs/superpowers/specs/2026-06-30-lmstudio-provider-design.md`

Do not add OCR support in this implementation. Do not make normal commands start, stop, load, or unload LM Studio models.

## File Structure

- Create `internal/llmprovider/provider.go`: provider-qualified model parsing and shared provider constants.
- Create `internal/llmprovider/params.go`: dbrain Modelfile parity preset and provider-specific parameter accounting.
- Create `internal/llmprovider/provider_test.go`: parser tests.
- Create `internal/llmprovider/params_test.go`: parity parameter tests.
- Modify `internal/summarizecli/types.go`: LM Studio constants, optional inference params on `Options`, request fields.
- Modify `internal/summarizecli/provider.go`: LM Studio prefix parser wrapper.
- Modify `internal/summarizecli/env.go`: LM Studio base URL/API key env/config resolution.
- Modify `internal/summarizecli/direct_target.go`: direct LM Studio target resolution.
- Modify `internal/summarizecli/direct.go`: LM Studio direct request support and optional parity parameters.
- Modify `internal/summarizecli/direct_response.go`: provider-labeled LM Studio empty/parse errors if needed.
- Modify `internal/summarizecli/client_test.go`: direct LM Studio and parity parameter unit tests.
- Modify `internal/sourceenrich/types.go`: carry optional summary inference params through source summary options.
- Modify `internal/sourceenrich/summary.go`: pass optional inference params into `summarizecli.Run`.
- Modify `internal/itemcategorize/types.go`: LM Studio options and optional inference params.
- Modify `internal/itemcategorize/options.go`: LM Studio env/config resolution.
- Modify `internal/itemcategorize/llm.go`: LM Studio text path, image rejection, provider-qualified `Result.Model`.
- Modify `internal/itemcategorize/run_test.go`: LM Studio and provenance tests.
- Modify `cmd/devtools/model_bakeoff/main.go`: `--parity-preset` flag.
- Modify `internal/modelbakeoff/types.go`: `model_bakeoff.v2` schema and new report fields.
- Modify `internal/modelbakeoff/run.go`: provider metadata, parity parameter injection, prompt/reasoning/runtime status fields.
- Modify `internal/modelbakeoff/report.go`: render new report fields.
- Modify `internal/modelbakeoff/report_test.go`: report/schema tests.
- Modify `config.yaml.sample`: `lmstudio` config block.
- Modify `README.md`: model backend docs and env table.
- Modify `COMMANDS.md`: bakeoff examples if the file has model-bakeoff/devtool coverage.
- Modify `internal/app/env_docs.go`: generated env docs source.
- Modify `skills/dbrain-model-bakeoff/SKILL.md`: LM Studio and `--parity-preset` examples.
- Modify `CHANGELOG.md`: user-visible provider/config/bakeoff entry.

## Task 1: Shared Provider And Parity Helpers

**Files:**
- Create: `internal/llmprovider/provider.go`
- Create: `internal/llmprovider/params.go`
- Test: `internal/llmprovider/provider_test.go`
- Test: `internal/llmprovider/params_test.go`

- [ ] **Step 1: Write provider parser tests**

Create `internal/llmprovider/provider_test.go`:

```go
package llmprovider

import "testing"

func TestParseModelRefProviderQualified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		provider Provider
		apiModel string
		original string
		ok       bool
	}{
		{name: "ollama slash", input: "ollama/dbrain:2026042701", provider: ProviderOllama, apiModel: "dbrain:2026042701", original: "ollama/dbrain:2026042701", ok: true},
		{name: "ollama colon", input: "ollama:qwen3.6:35b", provider: ProviderOllama, apiModel: "qwen3.6:35b", original: "ollama:qwen3.6:35b", ok: true},
		{name: "openrouter slash", input: "openrouter/google/gemini-2.5-flash", provider: ProviderOpenRouter, apiModel: "google/gemini-2.5-flash", original: "openrouter/google/gemini-2.5-flash", ok: true},
		{name: "lm studio slash", input: "lmstudio/qwen/qwen3.6-35b-a3b", provider: ProviderLMStudio, apiModel: "qwen/qwen3.6-35b-a3b", original: "lmstudio/qwen/qwen3.6-35b-a3b", ok: true},
		{name: "lm studio colon", input: "lmstudio:qwen/qwen3.6-35b-a3b", provider: ProviderLMStudio, apiModel: "qwen/qwen3.6-35b-a3b", original: "lmstudio:qwen/qwen3.6-35b-a3b", ok: true},
		{name: "plain", input: "google/gemini-2.5-flash", provider: ProviderPlain, apiModel: "google/gemini-2.5-flash", original: "google/gemini-2.5-flash", ok: false},
		{name: "empty provider", input: "lmstudio/", provider: ProviderPlain, apiModel: "", original: "lmstudio/", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseModelRef(tt.input)
			if got.Provider != tt.provider || got.APIModel != tt.apiModel || got.Original != tt.original || got.ProviderQualified != tt.ok {
				t.Fatalf("ParseModelRef(%q) = %+v", tt.input, got)
			}
		})
	}
}
```

- [ ] **Step 2: Write parity parameter tests**

Create `internal/llmprovider/params_test.go`:

```go
package llmprovider

import "testing"

func TestDbrainModelfilePresetMatchesRepoModelfile(t *testing.T) {
	t.Parallel()

	got := DbrainModelfilePreset()
	want := map[string]any{
		"temperature":    0.6,
		"top_p":          0.95,
		"top_k":          20,
		"min_p":          0.0,
		"repeat_penalty": 1.0,
	}
	if len(got) != len(want) {
		t.Fatalf("preset length = %d, want %d: %#v", len(got), len(want), got)
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("preset[%s] = %#v, want %#v", key, got[key], wantValue)
		}
	}
	if _, ok := got["presence_penalty"]; ok {
		t.Fatal("repo Modelfile does not define presence_penalty")
	}
}

func TestParityParamsForProviders(t *testing.T) {
	t.Parallel()

	ollama := DbrainParityForProvider(ProviderOllama)
	if len(ollama.Requested) != 5 {
		t.Fatalf("ollama requested params = %#v", ollama.Requested)
	}
	if len(ollama.Sent) != 5 {
		t.Fatalf("ollama sent params = %#v", ollama.Sent)
	}
	if len(ollama.Omitted) != 0 {
		t.Fatalf("ollama omitted params = %#v", ollama.Omitted)
	}
	if ollama.Strictness != StrictnessStrict {
		t.Fatalf("ollama strictness = %q", ollama.Strictness)
	}

	lmstudio := DbrainParityForProvider(ProviderLMStudio)
	if lmstudio.Sent["temperature"] != 0.6 || lmstudio.Sent["top_p"] != 0.95 || lmstudio.Sent["top_k"] != 20 || lmstudio.Sent["repeat_penalty"] != 1.0 {
		t.Fatalf("lmstudio sent params = %#v", lmstudio.Sent)
	}
	if lmstudio.Omitted["min_p"] == "" {
		t.Fatalf("expected min_p omission reason, got %#v", lmstudio.Omitted)
	}
	if lmstudio.Strictness != StrictnessNonStrict {
		t.Fatalf("lmstudio strictness = %q", lmstudio.Strictness)
	}

	none := DbrainParityForProvider(ProviderOpenRouter)
	if len(none.Requested) != 0 || len(none.Sent) != 0 || len(none.Omitted) != 0 {
		t.Fatalf("openrouter should not receive local parity params, got %#v", none)
	}
}
```

- [ ] **Step 3: Run the new tests and verify they fail**

Run:

```sh
go test ./internal/llmprovider
```

Expected: FAIL because `internal/llmprovider` does not exist.

- [ ] **Step 4: Implement provider parsing**

Create `internal/llmprovider/provider.go`:

```go
package llmprovider

import "strings"

type Provider string

const (
	ProviderPlain      Provider = ""
	ProviderOllama     Provider = "ollama"
	ProviderOpenRouter Provider = "openrouter"
	ProviderLMStudio   Provider = "lmstudio"
)

type ModelRef struct {
	Original          string
	Provider          Provider
	APIModel          string
	ProviderQualified bool
}

func ParseModelRef(model string) ModelRef {
	original := strings.TrimSpace(model)
	if original == "" {
		return ModelRef{}
	}
	for _, provider := range []Provider{ProviderOllama, ProviderOpenRouter, ProviderLMStudio} {
		if apiModel, ok := stripProvider(original, provider); ok {
			return ModelRef{
				Original:          original,
				Provider:          provider,
				APIModel:          apiModel,
				ProviderQualified: true,
			}
		}
	}
	return ModelRef{
		Original: original,
		Provider: ProviderPlain,
		APIModel: original,
	}
}

func stripProvider(model string, provider Provider) (string, bool) {
	prefix := string(provider)
	lower := strings.ToLower(model)
	for _, sep := range []string{"/", ":"} {
		fullPrefix := prefix + sep
		if strings.HasPrefix(lower, fullPrefix) {
			apiModel := strings.TrimSpace(model[len(fullPrefix):])
			return apiModel, apiModel != ""
		}
	}
	return "", false
}
```

- [ ] **Step 5: Implement parity parameter accounting**

Create `internal/llmprovider/params.go`:

```go
package llmprovider

const (
	ParityPresetNone           = "none"
	ParityPresetDbrainModelfile = "dbrain-modelfile"

	StrictnessNone      = "none"
	StrictnessStrict    = "strict"
	StrictnessNonStrict = "non-strict"
)

type ParityParams struct {
	Requested  map[string]any
	Sent       map[string]any
	Omitted    map[string]string
	Strictness string
}

func EmptyParityParams() ParityParams {
	return ParityParams{
		Requested:  map[string]any{},
		Sent:       map[string]any{},
		Omitted:    map[string]string{},
		Strictness: StrictnessNone,
	}
}

func DbrainModelfilePreset() map[string]any {
	return map[string]any{
		"temperature":    0.6,
		"top_p":          0.95,
		"top_k":          20,
		"min_p":          0.0,
		"repeat_penalty": 1.0,
	}
}

func DbrainParityForProvider(provider Provider) ParityParams {
	requested := DbrainModelfilePreset()
	switch provider {
	case ProviderOllama:
		return ParityParams{
			Requested:  cloneAnyMap(requested),
			Sent:       cloneAnyMap(requested),
			Omitted:    map[string]string{},
			Strictness: StrictnessStrict,
		}
	case ProviderLMStudio:
		sent := map[string]any{
			"temperature":    requested["temperature"],
			"top_p":          requested["top_p"],
			"top_k":          requested["top_k"],
			"repeat_penalty": requested["repeat_penalty"],
		}
		return ParityParams{
			Requested: cloneAnyMap(requested),
			Sent:      sent,
			Omitted: map[string]string{
				"min_p": "not documented for LM Studio OpenAI-compatible chat completions; requires live verification before sending",
			},
			Strictness: StrictnessNonStrict,
		}
	default:
		return EmptyParityParams()
	}
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
```

- [ ] **Step 6: Run the helper tests and verify they pass**

Run:

```sh
go test ./internal/llmprovider
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

```sh
git add internal/llmprovider/provider.go internal/llmprovider/params.go internal/llmprovider/provider_test.go internal/llmprovider/params_test.go
git commit -m "feat: add llm provider helpers"
```

## Task 2: Direct LM Studio Summary Support

**Files:**
- Modify: `internal/summarizecli/types.go`
- Modify: `internal/summarizecli/provider.go`
- Modify: `internal/summarizecli/env.go`
- Modify: `internal/summarizecli/direct_target.go`
- Modify: `internal/summarizecli/direct.go`
- Test: `internal/summarizecli/client_test.go`

- [ ] **Step 1: Write direct LM Studio summary tests**

Append these tests to `internal/summarizecli/client_test.go`:

```go
func TestRunDirectLMStudioSummaryForLocalFileInput(t *testing.T) {
	var captured chatCompletionsRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://lmstudio.test/v1/chat/completions" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-lmstudio-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"model":"qwen/qwen3.6-35b-a3b","choices":[{"message":{"role":"assistant","content":"direct lm studio summary"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(respBody))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("DBRAIN_LMSTUDIO_BASE_URL", "http://lmstudio.test")
	t.Setenv("DBRAIN_LMSTUDIO_API_KEY", "test-lmstudio-key")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "lmstudio/qwen/qwen3.6-35b-a3b",
		Prompt:    "System prompt",
		Length:    "medium",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Text != "direct lm studio summary" {
		t.Fatalf("unexpected summary text: %q", result.Summary.Text)
	}
	if result.Summary.Model != "lmstudio/qwen/qwen3.6-35b-a3b" {
		t.Fatalf("unexpected summary model: %q", result.Summary.Model)
	}
	if result.Summary.Tool != DirectLMStudioToolName {
		t.Fatalf("unexpected summary tool: %q", result.Summary.Tool)
	}
	if result.Summary.ToolVersion != directLMStudioVersion {
		t.Fatalf("unexpected summary tool version: %q", result.Summary.ToolVersion)
	}
	if captured.Model != "qwen/qwen3.6-35b-a3b" {
		t.Fatalf("unexpected model sent to LM Studio: %q", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("expected 2 chat messages, got %d", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || !strings.Contains(captured.Messages[0].Content, "System prompt") {
		t.Fatalf("unexpected system prompt: %+v", captured.Messages[0])
	}
}

func TestRunDirectLMStudioSummaryUsesOptionalParityParams(t *testing.T) {
	var captured chatCompletionsRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"choices":[{"message":{"content":"summary"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(respBody))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("DBRAIN_LMSTUDIO_BASE_URL", "http://lmstudio.test/v1")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Body content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	params := llmprovider.DbrainParityForProvider(llmprovider.ProviderLMStudio)
	_, err := Run(context.Background(), Options{
		Input:           inputPath,
		Summarize:       true,
		Model:           "lmstudio/qwen/qwen3.6-35b-a3b",
		Timeout:         2 * time.Second,
		InferenceParams: params,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if captured.Temperature == nil || *captured.Temperature != 0.6 {
		t.Fatalf("temperature = %#v, want 0.6", captured.Temperature)
	}
	if captured.TopP == nil || *captured.TopP != 0.95 {
		t.Fatalf("top_p = %#v, want 0.95", captured.TopP)
	}
	if captured.TopK == nil || *captured.TopK != 20 {
		t.Fatalf("top_k = %#v, want 20", captured.TopK)
	}
	if captured.RepeatPenalty == nil || *captured.RepeatPenalty != 1.0 {
		t.Fatalf("repeat_penalty = %#v, want 1.0", captured.RepeatPenalty)
	}
}
```

Add this import to the test file:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```sh
go test ./internal/summarizecli -run 'TestRunDirectLMStudio'
```

Expected: FAIL because LM Studio constants, parsing, and request fields do not exist.

- [ ] **Step 3: Add LM Studio constants and request fields**

Modify `internal/summarizecli/types.go`:

```go
const DirectLMStudioToolName = "lmstudio-direct"
```

Add these constants in the existing constant block:

```go
	defaultLMStudioBaseURL   = "http://127.0.0.1:1234/v1"
	defaultLMStudioAPIKey    = "lm-studio"
	directLMStudioVersion    = "lmstudio-direct-v1"
```

Add this field to `Options`:

```go
	InferenceParams llmprovider.ParityParams
```

Add this import to `types.go`:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

Extend `chatCompletionsRequest`:

```go
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
```

Extend `ollamaChatRequest`:

```go
	Options  map[string]any `json:"options,omitempty"`
```

- [ ] **Step 4: Add LM Studio parsing and env helpers**

Modify `internal/summarizecli/provider.go`:

```go
func parseLMStudioModel(model string) (string, bool) {
	ref := llmprovider.ParseModelRef(model)
	return ref.APIModel, ref.Provider == llmprovider.ProviderLMStudio && ref.ProviderQualified
}
```

Add the import:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

Modify `internal/summarizecli/env.go`:

```go
func lmStudioBaseURLWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_LMSTUDIO_BASE_URL")
	if value == "" {
		value = defaultLMStudioBaseURL
	}
	return normalizeBaseURLWithPath(value, "/v1", defaultLMStudioBaseURL)
}

func lmStudioAPIKeyWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_LMSTUDIO_API_KEY")
	if value == "" {
		value = defaultLMStudioAPIKey
	}
	return value
}
```

Add LM Studio keys to the `envWithRuntimeConfig` key list:

```go
			{"DBRAIN_LMSTUDIO_BASE_URL"},
			{"DBRAIN_LMSTUDIO_API_KEY"},
```

Add a `runtimeValue` secret branch:

```go
		case "DBRAIN_LMSTUDIO_API_KEY":
			if _, ok := parseLMStudioModel(model); !ok {
				return "", nil
			}
			return runtimeenv.FirstNonEmptySecret(ctx, rootDir, keys...)
```

- [ ] **Step 5: Route direct summaries to LM Studio**

Modify `internal/summarizecli/direct.go`:

```go
func UsesDirectSummary(model string) bool {
	if _, ollama := parseOllamaModel(model); ollama {
		return true
	}
	if _, openrouter := parseOpenRouterModel(model); openrouter {
		return true
	}
	_, lmstudio := parseLMStudioModel(model)
	return lmstudio
}

func SummaryToolName(model string) string {
	if _, ok := parseOllamaModel(model); ok {
		return DirectSummaryToolName
	}
	if _, ok := parseOpenRouterModel(model); ok {
		return DirectOpenRouterToolName
	}
	if _, ok := parseLMStudioModel(model); ok {
		return DirectLMStudioToolName
	}
	return ToolName
}

func SummaryToolVersion(ctx context.Context, binary string, model string) string {
	if _, ok := parseOllamaModel(model); ok {
		return directOllamaVersion
	}
	if _, ok := parseOpenRouterModel(model); ok {
		return directOpenRouterVersion
	}
	if _, ok := parseLMStudioModel(model); ok {
		return directLMStudioVersion
	}
	return Version(ctx, binary)
}
```

In `runDirectSummary`, after building `chatCompletionsRequest`, apply optional parity params:

```go
		if params := opts.InferenceParams.Sent; len(params) > 0 {
			applyChatCompletionsParams(&chatReq, params)
		}
```

Use a local variable so the code can pass a populated struct:

```go
chatReq := chatCompletionsRequest{
	Model:    target.model,
	Messages: messages,
	Stream:   false,
}
requestBody := any(chatReq)
```

For native Ollama, include options:

```go
options := map[string]any(nil)
if len(opts.InferenceParams.Sent) > 0 {
	options = cloneInferenceParams(opts.InferenceParams.Sent)
}
requestBody = ollamaChatRequest{
	Model:    target.model,
	Messages: messages,
	Stream:   false,
	Think:    &think,
	Options:  options,
}
```

Add helper functions in `direct.go`:

```go
func applyChatCompletionsParams(req *chatCompletionsRequest, params map[string]any) {
	if value, ok := floatParam(params, "temperature"); ok {
		req.Temperature = &value
	}
	if value, ok := floatParam(params, "top_p"); ok {
		req.TopP = &value
	}
	if value, ok := intParam(params, "top_k"); ok {
		req.TopK = &value
	}
	if value, ok := floatParam(params, "repeat_penalty"); ok {
		req.RepeatPenalty = &value
	}
}

func cloneInferenceParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for key, value := range params {
		out[key] = value
	}
	return out
}

func floatParam(params map[string]any, key string) (float64, bool) {
	switch value := params[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	default:
		return 0, false
	}
}

func intParam(params map[string]any, key string) (int, bool) {
	switch value := params[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}
```

- [ ] **Step 6: Add LM Studio direct target resolution**

Modify `internal/summarizecli/direct_target.go`:

```go
	if lmStudioModel, ok := parseLMStudioModel(opts.Model); ok {
		return directSummaryTarget{
			model:       lmStudioModel,
			displayName: defaultDirectDisplayName(opts.Model, "lmstudio/"+lmStudioModel),
			baseURL:     lmStudioBaseURLWithEnv(opts.Env),
			apiKey:      lmStudioAPIKeyWithEnv(opts.Env),
			toolName:    SummaryToolName(opts.Model),
			toolVersion: SummaryToolVersion(ctx, opts.Binary, opts.Model),
			label:       "lmstudio",
		}, nil
	}
```

- [ ] **Step 7: Run summary tests**

Run:

```sh
go test ./internal/summarizecli -run 'TestRunDirect(LMStudio|Ollama|OpenRouter)'
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

```sh
git add internal/summarizecli
git commit -m "feat: add lm studio summaries"
```

## Task 3: LM Studio Categorization And Provider-Qualified Provenance

**Files:**
- Modify: `internal/itemcategorize/types.go`
- Modify: `internal/itemcategorize/options.go`
- Modify: `internal/itemcategorize/llm.go`
- Test: `internal/itemcategorize/run_test.go`

- [ ] **Step 1: Write categorization tests**

Append to `internal/itemcategorize/run_test.go`:

```go
func TestCallLMStudioTextCategorization(t *testing.T) {
	t.Parallel()

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-lmstudio-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"local-models\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:        "lmstudio/qwen/qwen3.6-35b-a3b",
		LMStudioBase: server.URL + "/v1",
		LMStudioKey:  "test-lmstudio-key",
		Timeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "qwen/qwen3.6-35b-a3b" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "lmstudio/qwen/qwen3.6-35b-a3b" {
		t.Fatalf("result model = %q", result.Model)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "local-models" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCallLMStudioRejectsImages(t *testing.T) {
	t.Parallel()

	_, err := callLLM(context.Background(), "content bundle", [][]byte{{1, 2, 3}}, Options{
		Model:        "lmstudio/qwen/qwen3.6-35b-a3b",
		LMStudioBase: "http://127.0.0.1:1234/v1",
		LMStudioKey:  "lm-studio",
		Timeout:      2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "lmstudio categorization with images is not supported") {
		t.Fatalf("expected unsupported image error, got %v", err)
	}
}

func TestCategorizationPreservesProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	result, err := parseCategorizationJSON(`{"categories":["ai"],"tags":["agents"],"primary_category":"ai"}`, "ollama/dbrain:2026042701", categoryvocab.Vocab{})
	if err != nil {
		t.Fatalf("parseCategorizationJSON: %v", err)
	}
	if result.Model != "ollama/dbrain:2026042701" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestCallOpenRouterPreservesProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", "openrouter/google/gemini-test", Options{
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callOpenRouter: %v", err)
	}
	if result.Model != "openrouter/google/gemini-test" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestCallOllamaPreservesProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}`))
	}))
	defer server.Close()

	result, err := callOllama(context.Background(), "content bundle", nil, "dbrain:2026042701", "ollama/dbrain:2026042701", Options{
		OllamaBase: server.URL,
		OllamaKey:  "ollama",
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callOllama: %v", err)
	}
	if result.Model != "ollama/dbrain:2026042701" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestNormalizeChatCompletionsBaseAddsV1(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"http://127.0.0.1:1234":    "http://127.0.0.1:1234/v1",
		"http://127.0.0.1:1234/v1": "http://127.0.0.1:1234/v1",
		"127.0.0.1:1234":           "http://127.0.0.1:1234/v1",
		"":                         defaultLMStudioBase,
	}
	for input, want := range tests {
		if got := normalizeChatCompletionsBase(input, "/v1", defaultLMStudioBase); got != want {
			t.Fatalf("normalizeChatCompletionsBase(%q) = %q, want %q", input, got, want)
		}
	}
}
```

Add imports:

```go
	"encoding/json"

	"github.com/darron/dbrain/internal/categoryvocab"
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```sh
go test ./internal/itemcategorize -run 'TestCallLMStudio|TestCategorizationPreservesProviderQualifiedModel'
```

Expected: FAIL because LM Studio categorization options/path do not exist.

- [ ] **Step 3: Add LM Studio options and request fields**

Modify `internal/itemcategorize/types.go`:

```go
	defaultLMStudioBase = "http://127.0.0.1:1234/v1"
	defaultLMStudioKey  = "lm-studio"
```

Add to `Options`:

```go
	LMStudioBase    string
	LMStudioKey     string
	InferenceParams llmprovider.ParityParams
```

Add import:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

Extend `chatRequest`:

```go
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
```

Extend `ollamaRequest`:

```go
	Options  map[string]any `json:"options,omitempty"`
```

- [ ] **Step 4: Resolve LM Studio config**

Modify `internal/itemcategorize/options.go`:

```go
	if strings.TrimSpace(opts.LMStudioBase) == "" {
		opts.LMStudioBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_LMSTUDIO_BASE_URL"), defaultLMStudioBase)
	}
	opts.LMStudioBase = normalizeChatCompletionsBase(opts.LMStudioBase, "/v1", defaultLMStudioBase)
	if _, ok := parseLMStudioModel(opts.Model); ok && strings.TrimSpace(opts.LMStudioKey) == "" {
		value, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_LMSTUDIO_API_KEY")
		if err != nil {
			return err
		}
		opts.LMStudioKey = firstNonEmpty(value, defaultLMStudioKey)
	}
```

Add this helper to `internal/itemcategorize/options.go`:

```go
func normalizeChatCompletionsBase(raw string, suffix string, fallback string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = fallback
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimRight(value, "/")
	if strings.HasSuffix(value, suffix) {
		return value
	}
	return value + suffix
}
```

- [ ] **Step 5: Add parser wrapper and LM Studio call path**

Modify `internal/itemcategorize/llm.go`.

Add import:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

Add parser wrapper:

```go
func parseLMStudioModel(model string) (string, bool) {
	ref := llmprovider.ParseModelRef(model)
	return ref.APIModel, ref.Provider == llmprovider.ProviderLMStudio && ref.ProviderQualified
}
```

Change `callLLM`:

```go
func callLLM(ctx context.Context, bundle string, photoData [][]byte, opts Options) (Result, error) {
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		return callOllama(ctx, bundle, photoData, ollamaModel, opts.Model, opts)
	}
	if lmStudioModel, ok := parseLMStudioModel(opts.Model); ok {
		return callLMStudio(ctx, bundle, photoData, lmStudioModel, opts.Model, opts)
	}
	if openrouterModel, ok := parseOpenRouterModel(opts.Model); ok {
		return callOpenRouter(ctx, bundle, photoData, openrouterModel, opts.Model, opts)
	}
	return callOpenRouter(ctx, bundle, photoData, opts.Model, opts.Model, opts)
}
```

Change signatures:

```go
func callOllama(ctx context.Context, bundle string, photoData [][]byte, ollamaModel string, resultModel string, opts Options) (Result, error)
func callOpenRouter(ctx context.Context, bundle string, photoData [][]byte, openrouterModel string, resultModel string, opts Options) (Result, error)
```

Pass `resultModel` into `parseCategorizationJSON`.

Update the existing `TestCallOpenRouterSendsVersionedUserAgent` call site in
`internal/itemcategorize/run_test.go` from:

```go
_, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", Options{
```

to:

```go
_, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", "google/gemini-test", Options{
```

Search for any other direct `callOllama(` or `callOpenRouter(` test helper calls
and update them to the new signature before running the package tests.

Add `callLMStudio`:

```go
func callLMStudio(ctx context.Context, bundle string, photoData [][]byte, lmStudioModel string, resultModel string, opts Options) (Result, error) {
	if len(photoData) > 0 {
		return Result{}, fmt.Errorf("lmstudio categorization with images is not supported for this provider path")
	}

	reqBody := chatRequest{
		Model: lmStudioModel,
		Messages: []chatMessage{
			{Role: "system", Content: effectiveSystemPrompt(opts)},
			{Role: "user", Content: bundle},
		},
		Stream: false,
	}
	applyChatRequestParams(&reqBody, opts.InferenceParams.Sent)

	endpoint := strings.TrimRight(opts.LMStudioBase, "/") + "/chat/completions"
	raw, err := doPost(ctx, endpoint, opts.LMStudioKey, nil, reqBody, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("lmstudio categorize: %w", err)
	}

	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Result{}, fmt.Errorf("parse lmstudio response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("lmstudio categorize: no choices returned")
	}
	return parseCategorizationJSON(resp.Choices[0].Message.Content, resultModel, opts.Vocab)
}
```

Add helper:

```go
func applyChatRequestParams(req *chatRequest, params map[string]any) {
	if value, ok := floatParam(params, "temperature"); ok {
		req.Temperature = &value
	}
	if value, ok := floatParam(params, "top_p"); ok {
		req.TopP = &value
	}
	if value, ok := intParam(params, "top_k"); ok {
		req.TopK = &value
	}
	if value, ok := floatParam(params, "repeat_penalty"); ok {
		req.RepeatPenalty = &value
	}
}
```

Either move `floatParam`/`intParam` to `internal/llmprovider` in Task 2, or duplicate them locally if Task 2 kept them package-local. Prefer moving them into `internal/llmprovider` before duplicating.

- [ ] **Step 6: Apply Ollama options only when supplied**

In `callOllama`, set request options only from `opts.InferenceParams.Sent`:

```go
	reqBody := ollamaRequest{
		Model:    ollamaModel,
		Messages: []ollamaMessage{sysMsg, userMsg},
		Stream:   false,
		Think:    &think,
	}
	if len(opts.InferenceParams.Sent) > 0 {
		reqBody.Options = llmprovider.CloneAnyMap(opts.InferenceParams.Sent)
	}
```

If `CloneAnyMap` is not exported from `llmprovider`, export it as:

```go
func CloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
```

- [ ] **Step 7: Run categorization tests**

Run:

```sh
go test ./internal/itemcategorize
```

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

```sh
git add internal/itemcategorize internal/llmprovider
git commit -m "feat: add lm studio categorization"
```

## Task 4: Bakeoff v2 Schema, Parity Flag, And Report Fields

**Files:**
- Modify: `cmd/devtools/model_bakeoff/main.go`
- Modify: `internal/modelbakeoff/types.go`
- Modify: `internal/modelbakeoff/run.go`
- Modify: `internal/modelbakeoff/report.go`
- Modify: `internal/sourceenrich/types.go`
- Modify: `internal/sourceenrich/summary.go`
- Test: `internal/modelbakeoff/report_test.go`

- [ ] **Step 1: Write modelbakeoff report/schema tests**

Append to `internal/modelbakeoff/report_test.go`:

```go
func TestRenderMarkdownIncludesProviderParityAndRuntimeContext(t *testing.T) {
	result := Result{
		SchemaVersion: SchemaVersion,
		Mode:          ModeSourceSummary,
		Models:        []string{"lmstudio/qwen/qwen3.6-35b-a3b"},
		Targets: []TargetRun{
			{
				Lookup:    "src:test",
				SourceKey: "src:test",
				Title:     "Test source",
				Runs: []ModelRun{
					{
						Model:               "lmstudio/qwen/qwen3.6-35b-a3b",
						Provider:            "lmstudio",
						APIModel:            "qwen/qwen3.6-35b-a3b",
						Status:              "ok",
						RequestedParams:     map[string]any{"temperature": 0.6, "min_p": 0.0},
						SentParams:          map[string]any{"temperature": 0.6},
						OmittedParams:       map[string]string{"min_p": "not documented"},
						ParamStrictness:     "non-strict",
						PromptParityStatus:  "unknown",
						ReasoningModeStatus: "ollama-think-disabled-lmstudio-unknown",
						RuntimeContext: RuntimeContext{
							Status: "not-collected",
						},
						Summary: &model.SummaryResult{Text: "### What It Is\n\nA concise summary."},
					},
				},
			},
		},
	}

	report := RenderMarkdown(result, 0)
	for _, expected := range []string{
		"Schema: `model_bakeoff.v2`",
		"Provider: `lmstudio`",
		"API model: `qwen/qwen3.6-35b-a3b`",
		"Param strictness: `non-strict`",
		"Prompt parity: `unknown`",
		"Reasoning mode: `ollama-think-disabled-lmstudio-unknown`",
		"Runtime context: `not-collected`",
		"`min_p`: not documented",
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("expected report to contain %q, got:\n%s", expected, report)
		}
	}
}
```

- [ ] **Step 2: Run modelbakeoff tests and verify they fail**

Run:

```sh
go test ./internal/modelbakeoff
```

Expected: FAIL because schema v2 fields do not exist.

- [ ] **Step 3: Add v2 report types**

Modify `internal/modelbakeoff/types.go`:

```go
const SchemaVersion = "model_bakeoff.v2"
```

Add to `Options`:

```go
	ParityPreset string
```

Add types:

```go
type RuntimeContext struct {
	Status        string `json:"status,omitempty"`
	Provider      string `json:"provider,omitempty"`
	APIModel      string `json:"api_model,omitempty"`
	ContextLength int    `json:"context_length,omitempty"`
	Notes         string `json:"notes,omitempty"`
	Error         string `json:"error,omitempty"`
}
```

Extend `ModelRun`:

```go
	Provider            string            `json:"provider,omitempty"`
	APIModel            string            `json:"api_model,omitempty"`
	RequestedParams     map[string]any    `json:"requested_params,omitempty"`
	SentParams          map[string]any    `json:"sent_params,omitempty"`
	OmittedParams       map[string]string `json:"omitted_params,omitempty"`
	ParamStrictness     string            `json:"param_strictness,omitempty"`
	PromptParityStatus  string            `json:"prompt_parity_status,omitempty"`
	ReasoningModeStatus string            `json:"reasoning_mode_status,omitempty"`
	RuntimeContext      RuntimeContext    `json:"runtime_context,omitempty"`
```

- [ ] **Step 4: Add CLI parity flag**

Modify `cmd/devtools/model_bakeoff/main.go`:

```go
	var parityPreset string
```

Add flag:

```go
	fs.StringVar(&parityPreset, "parity-preset", llmprovider.ParityPresetNone, "Optional parity preset: none, dbrain-modelfile")
```

Add import:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

Pass through:

```go
			ParityPreset:  parityPreset,
```

- [ ] **Step 5: Populate provider metadata and parity params**

Modify `internal/modelbakeoff/run.go`:

```go
func runModel(ctx context.Context, cfg config.Config, st *store.Store, opts Options, target TargetRun, candidateModel string) ModelRun {
	started := time.Now()
	ref := llmprovider.ParseModelRef(candidateModel)
	parity := parityParamsForRun(opts.ParityPreset, ref.Provider)
	run := ModelRun{
		Model:               candidateModel,
		Provider:            string(ref.Provider),
		APIModel:            ref.APIModel,
		Status:              "ok",
		RequestedParams:     parity.Requested,
		SentParams:          parity.Sent,
		OmittedParams:       parity.Omitted,
		ParamStrictness:     parity.Strictness,
		PromptParityStatus:  promptParityStatus(opts.ParityPreset, ref.Provider),
		ReasoningModeStatus: reasoningModeStatus(ref.Provider),
		RuntimeContext:      runtimeContextForRun(ref),
	}
```

Add helpers:

```go
func parityParamsForRun(preset string, provider llmprovider.Provider) llmprovider.ParityParams {
	switch strings.TrimSpace(preset) {
	case "", llmprovider.ParityPresetNone:
		return llmprovider.EmptyParityParams()
	case llmprovider.ParityPresetDbrainModelfile:
		return llmprovider.DbrainParityForProvider(provider)
	default:
		return llmprovider.EmptyParityParams()
	}
}

func promptParityStatus(preset string, provider llmprovider.Provider) string {
	if strings.TrimSpace(preset) == "" || preset == llmprovider.ParityPresetNone {
		return ""
	}
	switch provider {
	case llmprovider.ProviderOllama, llmprovider.ProviderLMStudio:
		return "requires-live-verification"
	default:
		return "not-applicable"
	}
}

func reasoningModeStatus(provider llmprovider.Provider) string {
	switch provider {
	case llmprovider.ProviderOllama:
		return "think-disabled"
	case llmprovider.ProviderLMStudio:
		return "unknown"
	default:
		return ""
	}
}

func runtimeContextForRun(ref llmprovider.ModelRef) RuntimeContext {
	return RuntimeContext{
		Status:   "not-collected",
		Provider: string(ref.Provider),
		APIModel: ref.APIModel,
	}
}
```

When calling summary/categorization, pass `parity`.

First modify `internal/sourceenrich/types.go`:

```go
	InferenceParams llmprovider.ParityParams
```

Add import:

```go
	"github.com/darron/dbrain/internal/llmprovider"
```

Then modify each `summarizecli.Options` literal used for source summaries to
include:

```go
			InferenceParams: opts.InferenceParams,
```

The required source-summary paths are:

- `internal/sourceenrich/summary.go` inside `summarizeFromExtract`, where the
  inline `summarizecli.Run` call saves normal production source summaries.
- `internal/sourceenrich/summary.go` inside `summarizeExtract`, which is the
  read-only bakeoff path reached by `SummarizeSourceReadOnly` in
  `internal/sourceenrich/audit.go`.
- Existing callers that build a `summarizecli.Options` value and pass it to
  `runSummarizeWithRedirectRetry`; those literals should carry
  `InferenceParams: opts.InferenceParams` so redirect retry preserves the same
  settings. The retry function itself copies `runOpts`, so no extra retry-only
  change is needed.

Now pass parity from `internal/modelbakeoff/run.go`:

```go
summary, err := sourceenrich.SummarizeSourceReadOnly(ctx, cfg, source, sourceenrich.Options{
	Model:           candidateModel,
	Length:          opts.Length,
	Language:        opts.Language,
	Timeout:         opts.Timeout,
	InferenceParams: parity,
})
```

For item/source categorization:

```go
InferenceParams: parity,
```

- [ ] **Step 6: Validate parity preset values**

In `Run`, reject unsupported presets:

```go
	switch strings.TrimSpace(opts.ParityPreset) {
	case "", llmprovider.ParityPresetNone, llmprovider.ParityPresetDbrainModelfile:
	default:
		return result, fmt.Errorf("unsupported parity preset %q", opts.ParityPreset)
	}
```

- [ ] **Step 7: Render new fields**

Modify `internal/modelbakeoff/report.go`:

```go
	fmt.Fprintf(&b, "- Schema: `%s`\n", result.SchemaVersion)
```

Inside each run section:

```go
				if run.Provider != "" {
					fmt.Fprintf(&b, "- Provider: `%s`\n", run.Provider)
				}
				if run.APIModel != "" {
					fmt.Fprintf(&b, "- API model: `%s`\n", run.APIModel)
				}
				if run.ParamStrictness != "" {
					fmt.Fprintf(&b, "- Param strictness: `%s`\n", run.ParamStrictness)
				}
				if run.PromptParityStatus != "" {
					fmt.Fprintf(&b, "- Prompt parity: `%s`\n", run.PromptParityStatus)
				}
				if run.ReasoningModeStatus != "" {
					fmt.Fprintf(&b, "- Reasoning mode: `%s`\n", run.ReasoningModeStatus)
				}
				if run.RuntimeContext.Status != "" {
					fmt.Fprintf(&b, "- Runtime context: `%s`\n", run.RuntimeContext.Status)
				}
				writeParamMap(&b, "Requested params", run.RequestedParams)
				writeParamMap(&b, "Sent params", run.SentParams)
				writeOmittedParams(&b, run.OmittedParams)
```

Add helpers:

```go
func writeParamMap(b *strings.Builder, label string, params map[string]any) {
	if len(params) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s: ", label)
	first := true
	for _, key := range sortedKeys(params) {
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(b, "`%s=%v`", key, params[key])
	}
	b.WriteByte('\n')
}

func writeOmittedParams(b *strings.Builder, omitted map[string]string) {
	if len(omitted) == 0 {
		return
	}
	b.WriteString("- Omitted params:\n")
	for _, key := range sortedStringKeys(omitted) {
		fmt.Fprintf(b, "  - `%s`: %s\n", key, omitted[key])
	}
}

func sortedKeys(params map[string]any) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
```

Add `"sort"` to the imports for `internal/modelbakeoff/report.go`.

- [ ] **Step 8: Run modelbakeoff tests**

Run:

```sh
go test ./internal/modelbakeoff ./cmd/devtools/model_bakeoff
```

Expected: PASS.

- [ ] **Step 9: Commit Task 4**

```sh
git add cmd/devtools/model_bakeoff internal/modelbakeoff internal/sourceenrich internal/summarizecli internal/itemcategorize internal/llmprovider
git commit -m "feat: add lm studio bakeoff metadata"
```

## Task 5: Config, Docs, Skill, And Changelog

**Files:**
- Modify: `config.yaml.sample`
- Modify: `README.md`
- Modify: `COMMANDS.md`
- Modify: `internal/app/env_docs.go`
- Modify: `skills/dbrain-model-bakeoff/SKILL.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Update config sample**

In `config.yaml.sample`, add after the `ollama:` block:

```yaml
lmstudio:
  base_url: "http://127.0.0.1:1234/v1" # DBRAIN_LMSTUDIO_BASE_URL
  api_key: "lm-studio" # DBRAIN_LMSTUDIO_API_KEY; secret ref supported
```

- [ ] **Step 2: Update env docs source**

In `internal/app/env_docs.go`, add after Ollama:

```go
			{Key: "DBRAIN_LMSTUDIO_BASE_URL", ConfigPath: "lmstudio.base_url", Default: "http://127.0.0.1:1234/v1", Description: "LM Studio OpenAI-compatible endpoint for local model calls."},
			{Key: "DBRAIN_LMSTUDIO_API_KEY", ConfigPath: "lmstudio.api_key", Default: "lm-studio", Description: "API key label used for LM Studio local calls; supports secret refs."},
```

- [ ] **Step 3: Update README model backend docs**

In `README.md`, update the Model Backends section to include:

```markdown
Pass `--model lmstudio/qwen/qwen3.6-35b-a3b` to use a locally running LM Studio
server through its OpenAI-compatible API. Replace `qwen/qwen3.6-35b-a3b` with
the model id returned by `curl -s http://localhost:1234/v1/models`. The default
LM Studio endpoint is
`http://127.0.0.1:1234/v1`; override it with `DBRAIN_LMSTUDIO_BASE_URL` or
`lmstudio.base_url`.

LM Studio does not consume the repo `Modelfile` as an Ollama-style wrapper. For
dbrain calls, task prompts stay in the application and are sent as normal chat
system messages. LM Studio per-model defaults are runtime tuning, not the
authoritative dbrain prompt source.
```

Add env table rows:

```markdown
| `DBRAIN_LMSTUDIO_BASE_URL` | `lmstudio.base_url` | `http://127.0.0.1:1234/v1` | LM Studio OpenAI-compatible endpoint for local model calls. |
| `DBRAIN_LMSTUDIO_API_KEY` | `lmstudio.api_key` | `lm-studio` | API key label used for LM Studio local calls. |
```

- [ ] **Step 4: Update COMMANDS and skill docs**

In `COMMANDS.md`, add or update a model bakeoff example:

```sh
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup "$SOURCE_KEY" \
  --model lmstudio/qwen/qwen3.6-35b-a3b \
  --parity-preset dbrain-modelfile \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-lmstudio.md
```

In `skills/dbrain-model-bakeoff/SKILL.md`, add the same `lmstudio/*` model form and document:

```markdown
- Use `--parity-preset dbrain-modelfile` only for explicit local-provider parity checks.
- Run Ollama and LM Studio 35B tests in separate invocations when memory co-residency would bias timing.
```

- [ ] **Step 5: Update changelog**

Add a new dated subsection under `## Recent Improvements` in `CHANGELOG.md`:

```markdown
### LM Studio Provider (2026-06-30)

- Added first-class LM Studio local model support for direct summaries, text categorization, and model bakeoff reports, including provider-qualified provenance and opt-in local parity metadata.
```

- [ ] **Step 6: Run doc/env tests**

Run:

```sh
go test ./internal/app -run 'Test.*Env|Test.*README|Test.*Command'
```

If no matching tests exist for one of those names, run:

```sh
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 7: Commit Task 5**

```sh
git add config.yaml.sample README.md COMMANDS.md internal/app/env_docs.go skills/dbrain-model-bakeoff/SKILL.md CHANGELOG.md
git commit -m "docs: document lm studio provider"
```

## Task 6: Local Verification And Optional Live Smoke

**Files:**
- No planned source edits.
- Possible generated reports under `/tmp`; do not commit them.

- [ ] **Step 1: Run focused package tests**

Run:

```sh
go test ./internal/llmprovider ./internal/summarizecli ./internal/itemcategorize ./internal/modelbakeoff ./cmd/devtools/model_bakeoff
```

Expected: PASS.

- [ ] **Step 2: Run required project gates**

Run:

```sh
task fmt
task lint
task test-ci
task build
```

Expected: all PASS.

- [ ] **Step 3: Confirm local LM Studio is available if doing live smoke**

Run:

```sh
lms server status
curl -s http://localhost:1234/v1/models
```

Expected: LM Studio server is reachable and `/v1/models` includes the API model id to use after the `lmstudio/` provider prefix.

- [ ] **Step 4: Pick a representative source key without hardcoding local data**

Run:

```sh
DB_PATH="$(dbrain config paths --json | jq -r '.db_path')"
SOURCE_KEY="$(sqlite3 "$DB_PATH" "select source_key from sources where length(coalesce(extracted_text,'')) > 500 order by updated_at desc limit 1;")"
printf 'Using source key: %s\n' "$SOURCE_KEY"
```

Expected: prints a non-empty source key. If it is empty, skip live bakeoff and report that no suitable local source was available.

- [ ] **Step 5: Run LM Studio read-only smoke**

Run:

```sh
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup "$SOURCE_KEY" \
  --model lmstudio/qwen/qwen3.6-35b-a3b \
  --parity-preset dbrain-modelfile \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-lmstudio.md
```

Expected: command exits 0 and the report includes `Provider: lmstudio`, `API model`, parameter sections, prompt-parity status, and runtime-context status.

- [ ] **Step 6: Run Ollama read-only comparison only if Ollama is intentionally available**

Run:

```sh
ollama ps
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup "$SOURCE_KEY" \
  --model ollama/dbrain:2026042701 \
  --parity-preset dbrain-modelfile \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-ollama.md
```

Expected: command exits 0. If Ollama is not running or the model is not loaded, skip rather than starting or loading it implicitly.

- [ ] **Step 7: Final status check**

Run:

```sh
git status --short
git log --oneline -5
```

Expected: only intentional commits from this plan are present, and the worktree is clean.
