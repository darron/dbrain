package summarizecli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreferredCLIProviderUsesCLIState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	stateDir := filepath.Join(home, ".summarize")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir summarize dir: %v", err)
	}
	statePath := filepath.Join(stateDir, "cli-state.json")
	if err := os.WriteFile(statePath, []byte(`{"lastSuccessfulProvider":"claude"}`), 0o644); err != nil {
		t.Fatalf("write cli-state: %v", err)
	}

	if got := PreferredCLIProvider(); got != "claude" {
		t.Fatalf("expected claude provider, got %q", got)
	}
}

func TestPreferredCLIProviderFallsBackToCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := PreferredCLIProvider(); got != "codex" {
		t.Fatalf("expected codex fallback, got %q", got)
	}
}

func TestRunRetriesDatabaseLocked(t *testing.T) {
	root := t.TempDir()
	countPath := filepath.Join(root, "count.txt")
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
count=0
if [ -f "` + countPath + `" ]; then
  count="$(cat "` + countPath + `")"
fi
count=$((count + 1))
printf '%s' "$count" > "` + countPath + `"
if [ "$count" -eq 1 ]; then
  echo "database is locked" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"https://example.com","title":"Example","description":"","siteName":"Example","content":"body"},"summary":null}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:  binary,
		Input:   "https://example.com",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Extract.Status != "ok" {
		t.Fatalf("expected extract status ok, got %q", result.Extract.Status)
	}

	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatalf("read count: %v", err)
	}
	if string(data) != "2" {
		t.Fatalf("expected 2 attempts, got %q", string(data))
	}
}

func TestRunDirectOllamaSummaryForLocalFileInput(t *testing.T) {
	var captured chatCompletionsRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ollama" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"model":"qwen3.6:35b","choices":[{"message":{"role":"assistant","content":"direct local summary"}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	})}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})

	t.Setenv("DBRAIN_OLLAMA_BASE_URL", "http://ollama.test")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "ollama/qwen3.6:35b",
		Prompt:    "System prompt",
		Length:    "medium",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Status != "ok" {
		t.Fatalf("expected summary status ok, got %q", result.Summary.Status)
	}
	if result.Summary.Text != "direct local summary" {
		t.Fatalf("unexpected summary text: %q", result.Summary.Text)
	}
	if result.Summary.Model != "ollama/qwen3.6:35b" {
		t.Fatalf("unexpected summary model: %q", result.Summary.Model)
	}
	if result.Summary.Tool != DirectSummaryToolName {
		t.Fatalf("unexpected summary tool: %q", result.Summary.Tool)
	}
	if result.Summary.ToolVersion != directOllamaVersion {
		t.Fatalf("unexpected summary tool version: %q", result.Summary.ToolVersion)
	}
	if captured.Model != "qwen3.6:35b" {
		t.Fatalf("unexpected model sent to ollama: %q", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("expected 2 chat messages, got %d", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || !strings.Contains(captured.Messages[0].Content, "System prompt") {
		t.Fatalf("unexpected system prompt: %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || !strings.Contains(captured.Messages[1].Content, "Body content") {
		t.Fatalf("unexpected user message: %+v", captured.Messages[1])
	}
}

func TestRunDirectOpenRouterSummaryForLocalFileInput(t *testing.T) {
	var captured chatCompletionsRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-openrouter-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "https://dbrain.test" {
			t.Fatalf("unexpected referer header: %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "dbrain" {
			t.Fatalf("unexpected title header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"model":"qwen/qwen3.5-27b","choices":[{"message":{"role":"assistant","content":"direct openrouter summary"}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	})}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})

	t.Setenv("DBRAIN_OPENROUTER_BASE_URL", "https://openrouter.test")
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "test-openrouter-key")
	t.Setenv("DBRAIN_OPENROUTER_REFERER", "https://dbrain.test")
	t.Setenv("DBRAIN_OPENROUTER_TITLE", "dbrain")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "openrouter/qwen/qwen3.5-27b",
		Prompt:    "System prompt",
		Length:    "medium",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Status != "ok" {
		t.Fatalf("expected summary status ok, got %q", result.Summary.Status)
	}
	if result.Summary.Text != "direct openrouter summary" {
		t.Fatalf("unexpected summary text: %q", result.Summary.Text)
	}
	if result.Summary.Model != "openrouter/qwen/qwen3.5-27b" {
		t.Fatalf("unexpected summary model: %q", result.Summary.Model)
	}
	if result.Summary.Tool != DirectOpenRouterToolName {
		t.Fatalf("unexpected summary tool: %q", result.Summary.Tool)
	}
	if result.Summary.ToolVersion != directOpenRouterVersion {
		t.Fatalf("unexpected summary tool version: %q", result.Summary.ToolVersion)
	}
	if captured.Model != "qwen/qwen3.5-27b" {
		t.Fatalf("unexpected model sent to openrouter: %q", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("expected 2 chat messages, got %d", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || !strings.Contains(captured.Messages[0].Content, "System prompt") {
		t.Fatalf("unexpected system prompt: %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || !strings.Contains(captured.Messages[1].Content, "Body content") {
		t.Fatalf("unexpected user message: %+v", captured.Messages[1])
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestRunTranslatesOllamaModelToOpenAICompatibleRequest(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
prev=""
model=""
for arg in "$@"; do
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  prev="$arg"
done
if [ "$model" != "openai/qwen2.5:7b-instruct" ]; then
  echo "unexpected model: $model" >&2
  exit 1
fi
if [ "$OPENAI_BASE_URL" != "http://127.0.0.1:11434/v1" ]; then
  echo "unexpected OPENAI_BASE_URL: $OPENAI_BASE_URL" >&2
  exit 1
fi
if [ "$OPENAI_API_KEY" != "ollama" ]; then
  echo "unexpected OPENAI_API_KEY: $OPENAI_API_KEY" >&2
  exit 1
fi
if [ "$OPENAI_USE_CHAT_COMPLETIONS" != "1" ]; then
  echo "unexpected OPENAI_USE_CHAT_COMPLETIONS: $OPENAI_USE_CHAT_COMPLETIONS" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"openai/qwen2.5:7b-instruct"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:    binary,
		Input:     "README.md",
		Summarize: true,
		Model:     "ollama/qwen2.5:7b-instruct",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Status != "ok" {
		t.Fatalf("expected summary status ok, got %q", result.Summary.Status)
	}
	if result.Summary.Model != "openai/qwen2.5:7b-instruct" {
		t.Fatalf("expected translated model, got %q", result.Summary.Model)
	}
}

func TestRunSuppressesCLIWhenModelProvided(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
prev=""
model=""
for arg in "$@"; do
  if [ "$arg" = "--cli" ]; then
    echo "unexpected cli flag" >&2
    exit 1
  fi
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  prev="$arg"
done
if [ "$model" != "openai/qwen3.6:35b" ]; then
  echo "unexpected model: $model" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"openai/qwen3.6:35b"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:    binary,
		Input:     "README.md",
		Summarize: true,
		Model:     "ollama/qwen3.6:35b",
		CLI:       "codex",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Status != "ok" {
		t.Fatalf("expected summary status ok, got %q", result.Summary.Status)
	}
}

func TestResolveCLIProviderModelWins(t *testing.T) {
	if got := ResolveCLIProvider("codex", "ollama/qwen3.6:35b"); got != "" {
		t.Fatalf("expected empty cli when model is set, got %q", got)
	}
	if got := ResolveCLIProvider("codex", "openrouter/qwen/qwen3.5-27b"); got != "" {
		t.Fatalf("expected empty cli when model is set, got %q", got)
	}
}

func TestResolveModelAndEnvUsesOLLAMAHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://10.0.0.5:11434")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_USE_CHAT_COMPLETIONS", "")

	model, env := resolveModelAndEnv("ollama:qwen3.5:9b", nil)
	if model != "openai/qwen3.5:9b" {
		t.Fatalf("expected translated model, got %q", model)
	}
	if env["OPENAI_BASE_URL"] != "http://10.0.0.5:11434/v1" {
		t.Fatalf("expected OLLAMA_HOST-derived base URL, got %q", env["OPENAI_BASE_URL"])
	}
}

func TestResolveModelAndEnvRespectsExistingOverrides(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_USE_CHAT_COMPLETIONS", "")

	model, env := resolveModelAndEnv("ollama/test-model", map[string]string{
		"OPENAI_BASE_URL":             "http://example.test/v1",
		"OPENAI_API_KEY":              "custom",
		"OPENAI_USE_CHAT_COMPLETIONS": "0",
	})
	if model != "openai/test-model" {
		t.Fatalf("expected translated model, got %q", model)
	}
	if env["OPENAI_BASE_URL"] != "http://example.test/v1" {
		t.Fatalf("expected override base URL, got %q", env["OPENAI_BASE_URL"])
	}
	if env["OPENAI_API_KEY"] != "custom" {
		t.Fatalf("expected override API key, got %q", env["OPENAI_API_KEY"])
	}
	if env["OPENAI_USE_CHAT_COMPLETIONS"] != "0" {
		t.Fatalf("expected override chat-completions flag, got %q", env["OPENAI_USE_CHAT_COMPLETIONS"])
	}
}

func TestNormalizeBaseURLWithV1(t *testing.T) {
	cases := map[string]string{
		"":                          defaultOllamaBaseURL,
		"127.0.0.1:11434":           "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434":    "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/v1": "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/":   "http://127.0.0.1:11434/v1",
	}

	for input, want := range cases {
		if got := normalizeBaseURLWithV1(input); got != want {
			t.Fatalf("normalizeBaseURLWithV1(%q): got %q want %q", input, got, want)
		}
	}
}

func TestNormalizeBaseURLWithAPIPath(t *testing.T) {
	cases := map[string]string{
		"":                              defaultOpenRouterBaseURL,
		"https://openrouter.ai":         "https://openrouter.ai/api/v1",
		"https://openrouter.ai/":        "https://openrouter.ai/api/v1",
		"https://openrouter.ai/api/v1":  "https://openrouter.ai/api/v1",
		"https://openrouter.ai/api/v1/": "https://openrouter.ai/api/v1",
		"openrouter.ai/api/v1":          "http://openrouter.ai/api/v1",
	}

	for input, want := range cases {
		if got := normalizeBaseURLWithPath(input, "/api/v1", defaultOpenRouterBaseURL); got != want {
			t.Fatalf("normalizeBaseURLWithPath(%q): got %q want %q", input, got, want)
		}
	}
}

func TestParseOllamaModel(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "ollama/qwen2.5:7b-instruct", want: "qwen2.5:7b-instruct", ok: true},
		{input: "ollama:qwen2.5:7b-instruct", want: "qwen2.5:7b-instruct", ok: true},
		{input: "openai/gpt-5-mini", want: "", ok: false},
		{input: "ollama/", want: "", ok: false},
	}

	for _, tc := range cases {
		got, ok := parseOllamaModel(tc.input)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("parseOllamaModel(%q): got (%q,%v) want (%q,%v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseOpenRouterModel(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "openrouter/qwen/qwen3.5-27b", want: "qwen/qwen3.5-27b", ok: true},
		{input: "openrouter:qwen/qwen3.5-27b", want: "qwen/qwen3.5-27b", ok: true},
		{input: "ollama/qwen3.6:27b", want: "", ok: false},
		{input: "openrouter/", want: "", ok: false},
	}

	for _, tc := range cases {
		got, ok := parseOpenRouterModel(tc.input)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("parseOpenRouterModel(%q): got (%q,%v) want (%q,%v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveModelAndEnvDoesNotMutateInputMap(t *testing.T) {
	input := map[string]string{"EXISTING": "value"}
	_, env := resolveModelAndEnv("ollama/test-model", input)
	if env["EXISTING"] != "value" {
		t.Fatalf("expected copied env to preserve existing value, got %q", env["EXISTING"])
	}
	if got := strings.TrimSpace(input["OPENAI_BASE_URL"]); got != "" {
		t.Fatalf("expected input map to remain untouched, got OPENAI_BASE_URL=%q", got)
	}
}
