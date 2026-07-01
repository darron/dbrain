package itemcategorize

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/model"
)

func TestMergeUserTagsPreservesExistingAndDedupesGenerated(t *testing.T) {
	result := Result{
		Tags:       []string{"canada", "public-safety", "canada"},
		Categories: []string{"Canadian Politics", "public-safety"},
	}

	got := MergeUserTags("existing, canada\nlocal", result)
	want := "existing,canada,local,public-safety,Canadian Politics"
	if got != want {
		t.Fatalf("MergeUserTags() = %q, want %q", got, want)
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	Temperature   *float64      `json:"temperature,omitempty"`
	TopP          *float64      `json:"top_p,omitempty"`
	TopK          *int          `json:"top_k,omitempty"`
	RepeatPenalty *float64      `json:"repeat_penalty,omitempty"`
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Think    *bool           `json:"think,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

func TestCallOpenRouterSendsVersionedUserAgent(t *testing.T) {
	t.Parallel()

	var capturedUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUserAgent = r.Header.Get("User-Agent")
		if got := r.Header.Get("Authorization"); got != "Bearer test-openrouter-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	_, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", "google/gemini-test", Options{
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		UserAgent:      "dbrain/test-sha",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callOpenRouter: %v", err)
	}
	if capturedUserAgent != "dbrain/test-sha" {
		t.Fatalf("User-Agent = %q, want %q", capturedUserAgent, "dbrain/test-sha")
	}
}

func requireJSONNumberOption(t *testing.T, options map[string]any, key string, want float64) {
	t.Helper()
	got, ok := options[key].(float64)
	if !ok || got != want {
		t.Fatalf("options[%s] = %#v, want %v", key, options[key], want)
	}
}

func TestBuildSourceContentBundleIncludesSourceEvidence(t *testing.T) {
	t.Parallel()

	bundle := buildSourceContentBundle(model.SourceDocument{
		SourceKey:     "src:test-source",
		SourceType:    "web",
		CanonicalURL:  "https://example.com/article",
		Domain:        "example.com",
		SiteName:      "Example",
		Title:         "Source Title",
		Description:   "Source description.",
		SummaryText:   "Source summary.",
		ExtractedText: strings.Repeat("extract ", 20),
	})

	for _, want := range []string{
		"record_kind: source",
		"source_type: web",
		"url: https://example.com/article",
		"title: Source Title",
		"Description:\nSource description.",
		"Summary:\nSource summary.",
		"Extracted text:\nextract",
	} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("source bundle missing %q:\n%s", want, bundle)
		}
	}
}

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

func TestCallOMLXTextCategorization(t *testing.T) {
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"omlx\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	t.Setenv("DBRAIN_OMLX_BASE_URL", server.URL)
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:   "omlx/qwen3.5-coder",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "qwen3.5-coder" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "omlx/qwen3.5-coder" {
		t.Fatalf("result model = %q", result.Model)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "omlx" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCallConfiguredAliasTextCategorization(t *testing.T) {
	root := t.TempDir()
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"localai\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    base_url: `+server.URL+`/v1
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		RootDir: root,
		Model:   "localai/test-model",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "test-model" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "localai/test-model" {
		t.Fatalf("result model = %q", result.Model)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "localai" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCallConfiguredAliasRegistryErrorDoesNotFallbackToOpenRouter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := callLLM(context.Background(), "content bundle", nil, Options{
		RootDir:       root,
		Model:         "localai/test-model",
		OpenRouterKey: "should-not-fallback",
		Timeout:       2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "llm_backends.localai base_url is required") {
		t.Fatalf("expected alias config error, got %v", err)
	}
}

func TestCallLLMPlainModelStillRoutesOpenRouter(t *testing.T) {
	t.Parallel()

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"hosted\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:          "google/gemini-test",
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "google/gemini-test" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "google/gemini-test" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestCallLLMOpenRouterImagesStillSendImageParts(t *testing.T) {
	t.Parallel()

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"vision\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	_, err := callLLM(context.Background(), "content bundle", [][]byte{{1, 2, 3}}, Options{
		Model:          "openrouter/google/gemini-test",
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %+v", captured.Messages)
	}
	parts, ok := captured.Messages[1].Content.([]any)
	if !ok {
		t.Fatalf("expected multimodal content parts, got %#v", captured.Messages[1].Content)
	}
	var sawImage bool
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if ok && m["type"] == "image_url" {
			sawImage = true
		}
	}
	if !sawImage {
		t.Fatalf("expected image_url part, got %#v", parts)
	}
}

func TestCallLLMRejectsImagesForOMLX(t *testing.T) {
	_, err := callLLM(context.Background(), "content bundle", [][]byte{{1, 2, 3}}, Options{
		Model:   "omlx/qwen3.5-coder",
		Timeout: 2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "omlx") || !strings.Contains(err.Error(), "omlx/qwen3.5-coder") {
		t.Fatalf("expected oMLX image rejection with model, got %v", err)
	}
}

func TestProviderOverridesExplicitValuesWin(t *testing.T) {
	t.Parallel()

	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("legacy OpenRouter endpoint should not be used")
	}))
	defer legacy.Close()

	var capturedAuth string
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"override\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer override.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:          "openrouter/google/gemini-test",
		OpenRouterBase: legacy.URL,
		OpenRouterKey:  "legacy-key",
		ProviderOverrides: map[llmprovider.Provider]llmprovider.ProviderOverrides{
			llmprovider.ProviderOpenRouter: {
				BaseURL: override.URL,
				APIKey:  "override-key",
			},
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if capturedAuth != "Bearer override-key" {
		t.Fatalf("auth = %q", capturedAuth)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "override" {
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

func TestCallLLMRejectsEmptyLMStudioModel(t *testing.T) {
	t.Parallel()

	_, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:         "lmstudio/",
		OpenRouterKey: "should-not-fallback",
		Timeout:       2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported LM Studio model "lmstudio/"`) {
		t.Fatalf("expected unsupported LM Studio model error, got %v", err)
	}
}

func TestResolveOptsLoadsLMStudioConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
lmstudio:
  base_url: http://10.0.0.7:1234
  api_key: studio-key
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBRAIN_LMSTUDIO_BASE_URL", "")
	t.Setenv("DBRAIN_LMSTUDIO_API_KEY", "")

	opts := Options{Model: "lmstudio/qwen/qwen3.6-35b-a3b"}
	if err := resolveOpts(context.Background(), config.Config{
		RootDir:        root,
		CategoriesPath: filepath.Join(root, "categories.yaml"),
	}, &opts); err != nil {
		t.Fatalf("resolveOpts: %v", err)
	}
	if opts.LMStudioBase != "http://10.0.0.7:1234/v1" {
		t.Fatalf("LMStudioBase = %q", opts.LMStudioBase)
	}
	if opts.LMStudioKey != "studio-key" {
		t.Fatalf("LMStudioKey = %q", opts.LMStudioKey)
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

	var captured ollamaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}`))
	}))
	defer server.Close()

	result, err := callOllama(context.Background(), "content bundle", nil, "dbrain:2026042701", "ollama/dbrain:2026042701", Options{
		OllamaBase: server.URL,
		OllamaKey:  "ollama",
		Timeout:    2 * time.Second,
		InferenceParams: llmprovider.DbrainParityForProvider(
			llmprovider.ProviderOllama,
		),
	})
	if err != nil {
		t.Fatalf("callOllama: %v", err)
	}
	if result.Model != "ollama/dbrain:2026042701" {
		t.Fatalf("result model = %q", result.Model)
	}
	if len(captured.Options) != 5 {
		t.Fatalf("expected all Modelfile options, got %#v", captured.Options)
	}
	requireJSONNumberOption(t, captured.Options, "temperature", 0.6)
	requireJSONNumberOption(t, captured.Options, "top_p", 0.95)
	requireJSONNumberOption(t, captured.Options, "top_k", 20)
	requireJSONNumberOption(t, captured.Options, "min_p", 0)
	requireJSONNumberOption(t, captured.Options, "repeat_penalty", 1)
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
