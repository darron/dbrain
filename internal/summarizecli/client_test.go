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

	"github.com/darron/dbrain/internal/llmprovider"
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

func TestVersionDoesNotCacheMissingBinary(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	if got := Version(context.Background(), binary); got != "" {
		t.Fatalf("expected missing binary version to be empty, got %q", got)
	}

	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	if got := Version(context.Background(), binary); got != "test-1.0.0" {
		t.Fatalf("expected discovered version after fake binary install, got %q", got)
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

type chatCompletionsRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	Temperature   *float64      `json:"temperature,omitempty"`
	TopP          *float64      `json:"top_p,omitempty"`
	TopK          *int          `json:"top_k,omitempty"`
	RepeatPenalty *float64      `json:"repeat_penalty,omitempty"`
}

type ollamaChatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Think    *bool          `json:"think,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func TestRunCLIHonorsTimeout(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
sleep 10
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"https://example.com","title":"Example","description":"","siteName":"Example","content":"body"},"summary":null}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	start := time.Now()
	_, err := Run(context.Background(), Options{
		Binary:  binary,
		Input:   "https://example.com",
		Timeout: 100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") &&
		!strings.Contains(err.Error(), "context canceled") &&
		!strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("expected process timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("expected cli summarize to honor timeout quickly, took %s", elapsed)
	}
}

func TestRunDirectOllamaSummaryForLocalFileInput(t *testing.T) {
	var captured ollamaChatRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ollama" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"model":"qwen3.6:35b","message":{"role":"assistant","content":"direct local summary"},"done":true}`
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
	if captured.Think == nil || *captured.Think {
		t.Fatalf("expected direct ollama request to disable thinking, got %#v", captured.Think)
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
		if got := r.Header.Get("User-Agent"); got != "dbrain/test-sha" {
			t.Fatalf("unexpected user-agent header: %q", got)
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
	t.Setenv("DBRAIN_USER_AGENT", "dbrain/test-sha")

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

func TestRunDirectOpenRouterSummaryHonorsTimeout(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})

	t.Setenv("DBRAIN_OPENROUTER_BASE_URL", "https://openrouter.test")
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "test-openrouter-key")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	start := time.Now()
	_, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "openrouter/qwen/qwen3.5-27b",
		Timeout:   100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected direct summary to honor timeout quickly, took %s", elapsed)
	}
}

func TestRunPlainSummaryModelStillUsesExternalSummarizeCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-plain-cli"
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
if [ "$model" != "google/gemini-plain" ]; then
  echo "unexpected model: $model" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"google/gemini-plain"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"external cli summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:    binary,
		Input:     "README.md",
		Summarize: true,
		Model:     "google/gemini-plain",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Tool != ToolName || result.Summary.ToolVersion != "test-plain-cli" {
		t.Fatalf("expected external summarize CLI tool, got %+v", result.Summary)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func requireJSONNumberOption(t *testing.T, options map[string]any, key string, want float64) {
	t.Helper()
	got, ok := options[key].(float64)
	if !ok || got != want {
		t.Fatalf("options[%s] = %#v, want %v", key, options[key], want)
	}
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

func TestRunDefaultsSummaryLanguageToEnglish(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
prev=""
language=""
for arg in "$@"; do
  if [ "$prev" = "--language" ]; then
    language="$arg"
  fi
  prev="$arg"
done
if [ "$language" != "en" ]; then
  echo "unexpected language: $language" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"test"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:    binary,
		Input:     "README.md",
		Summarize: true,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Status != "ok" {
		t.Fatalf("expected summary status ok, got %q", result.Summary.Status)
	}
}

func TestRunAllowsSummaryLanguageOverride(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
prev=""
language=""
for arg in "$@"; do
  if [ "$prev" = "--language" ]; then
    language="$arg"
  fi
  prev="$arg"
done
if [ "$language" != "auto" ]; then
  echo "unexpected language: $language" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"test"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:    binary,
		Input:     "README.md",
		Summarize: true,
		Language:  "auto",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Status != "ok" {
		t.Fatalf("expected summary status ok, got %q", result.Summary.Status)
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
printf '%s\n' '{"input":{"model":"openai/qwen3.6:35b"},"extracted":{"url":"https://example.com","title":"Example","description":"","siteName":"","content":"body"},"summary":"summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:    binary,
		Input:     "https://example.com",
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

func TestEnvWithRuntimeConfigSeparatesOllamaAndOpenAIKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
ollama:
  base_url: http://10.0.0.6:11434
  api_key: local-key
openai:
  base_url: https://openai-compatible.example/v1
  api_key: hosted-key
  use_chat_completions: true
openrouter:
  api_key: router-key
lmstudio:
  base_url: http://10.0.0.7:1234
  api_key: studio-key
summary:
  language: English
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DBRAIN_OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("DBRAIN_OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_USE_CHAT_COMPLETIONS", "")
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("DBRAIN_LMSTUDIO_BASE_URL", "")
	t.Setenv("DBRAIN_LMSTUDIO_API_KEY", "")
	t.Setenv("DBRAIN_SUMMARY_LANGUAGE", "")
	t.Setenv("DBRAIN_OUTPUT_LANGUAGE", "")
	t.Setenv("SUMMARIZE_LANGUAGE", "")

	env, err := envWithRuntimeConfig(context.Background(), root, nil, "ollama/qwen")
	if err != nil {
		t.Fatalf("envWithRuntimeConfig: %v", err)
	}
	if got := env["DBRAIN_OLLAMA_BASE_URL"]; got != "http://10.0.0.6:11434" {
		t.Fatalf("expected Ollama base URL from config, got %q", got)
	}
	if got := env["DBRAIN_OLLAMA_API_KEY"]; got != "local-key" {
		t.Fatalf("expected Ollama API key from config, got %q", got)
	}
	if got := env["OPENAI_BASE_URL"]; got != "https://openai-compatible.example/v1" {
		t.Fatalf("expected OpenAI base URL from config, got %q", got)
	}
	if got := env["OPENAI_USE_CHAT_COMPLETIONS"]; got != "true" {
		t.Fatalf("expected OpenAI chat-completions flag from config, got %q", got)
	}
	if got := env["DBRAIN_SUMMARY_LANGUAGE"]; got != "English" {
		t.Fatalf("expected summary language from config, got %q", got)
	}

	env, err = envWithRuntimeConfig(context.Background(), root, nil, "openrouter/qwen/qwen3.5-27b")
	if err != nil {
		t.Fatalf("envWithRuntimeConfig openrouter: %v", err)
	}
	if got := env["DBRAIN_OPENROUTER_API_KEY"]; got != "router-key" {
		t.Fatalf("expected OpenRouter API key from config, got %q", got)
	}

	env, err = envWithRuntimeConfig(context.Background(), root, nil, "lmstudio/qwen/qwen3.6-35b-a3b")
	if err != nil {
		t.Fatalf("envWithRuntimeConfig lmstudio: %v", err)
	}
	if got := env["DBRAIN_LMSTUDIO_BASE_URL"]; got != "http://10.0.0.7:1234" {
		t.Fatalf("expected LM Studio base URL from config, got %q", got)
	}
	if got := env["DBRAIN_LMSTUDIO_API_KEY"]; got != "studio-key" {
		t.Fatalf("expected LM Studio API key from config, got %q", got)
	}

	env, err = envWithRuntimeConfig(context.Background(), root, nil, "openai/gpt-test")
	if err != nil {
		t.Fatalf("envWithRuntimeConfig openai: %v", err)
	}
	if got := env["OPENAI_API_KEY"]; got != "hosted-key" {
		t.Fatalf("expected OpenAI API key from config, got %q", got)
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

func TestRunDirectOMLXSummaryForLocalFileInput(t *testing.T) {
	var captured chatCompletionsRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://omlx.test/v1/chat/completions" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"model":"qwen3.5-coder","choices":[{"message":{"role":"assistant","content":"direct omlx summary"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(respBody))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("DBRAIN_OMLX_BASE_URL", "http://omlx.test")
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "omlx/qwen3.5-coder",
		Prompt:    "System prompt",
		Length:    "medium",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Text != "direct omlx summary" {
		t.Fatalf("unexpected summary text: %q", result.Summary.Text)
	}
	if result.Summary.Model != "omlx/qwen3.5-coder" {
		t.Fatalf("unexpected summary model: %q", result.Summary.Model)
	}
	if result.Summary.Tool != llmprovider.ToolOMLXDirect {
		t.Fatalf("unexpected summary tool: %q", result.Summary.Tool)
	}
	if result.Summary.ToolVersion != llmprovider.ToolVersionOMLXDirect {
		t.Fatalf("unexpected summary tool version: %q", result.Summary.ToolVersion)
	}
	if captured.Model != "qwen3.5-coder" {
		t.Fatalf("unexpected model sent to oMLX: %q", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("expected 2 chat messages, got %d", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || !strings.Contains(captured.Messages[0].Content, "System prompt") {
		t.Fatalf("unexpected system prompt: %+v", captured.Messages[0])
	}
}

func TestRunDirectConfiguredAliasSummaryForLocalFileInput(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    base_url: http://localai.test/v1
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured chatCompletionsRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://localai.test/v1/chat/completions" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"direct alias summary"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(respBody))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	result, err := Run(context.Background(), Options{
		RootDir:   root,
		Input:     inputPath,
		Summarize: true,
		Model:     "localai/test-model",
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Text != "direct alias summary" {
		t.Fatalf("unexpected summary text: %q", result.Summary.Text)
	}
	if result.Summary.Model != "localai/test-model" {
		t.Fatalf("unexpected summary model: %q", result.Summary.Model)
	}
	if result.Summary.Tool != "localai-direct" {
		t.Fatalf("unexpected summary tool: %q", result.Summary.Tool)
	}
	if result.Summary.ToolVersion != "localai-direct-v1" {
		t.Fatalf("unexpected summary tool version: %q", result.Summary.ToolVersion)
	}
	if captured.Model != "test-model" {
		t.Fatalf("unexpected model sent to alias backend: %q", captured.Model)
	}
}

func TestRunConfiguredAliasRegistryErrorDoesNotFallBackToCLI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
printf '%s\n' '{"input":{"model":"localai/test-model"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"unexpected cli fallback"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Body content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	_, err := Run(context.Background(), Options{
		RootDir:   root,
		Binary:    binary,
		Input:     inputPath,
		Summarize: true,
		Model:     "localai/test-model",
		Timeout:   2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "llm_backends.localai base_url is required") {
		t.Fatalf("expected alias config error, got %v", err)
	}
}

func TestRunDirectOllamaSummaryUsesOptionalParityParams(t *testing.T) {
	var captured ollamaChatRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		respBody := `{"message":{"content":"summary"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(respBody))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	t.Setenv("DBRAIN_OLLAMA_BASE_URL", "http://ollama.test")

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Body content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	params := llmprovider.DbrainParityForProvider(llmprovider.ProviderOllama)
	_, err := Run(context.Background(), Options{
		Input:           inputPath,
		Summarize:       true,
		Model:           "ollama/qwen3.6:35b",
		Timeout:         2 * time.Second,
		InferenceParams: params,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
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
	if captured.TopK != nil {
		t.Fatalf("top_k = %#v, want omitted", captured.TopK)
	}
	if captured.RepeatPenalty != nil {
		t.Fatalf("repeat_penalty = %#v, want omitted", captured.RepeatPenalty)
	}
}

func TestRunRejectsEmptyLMStudioModel(t *testing.T) {
	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Body content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	_, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "lmstudio/",
		Timeout:   2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported LM Studio model "lmstudio/"`) {
		t.Fatalf("expected unsupported LM Studio model error, got %v", err)
	}
}

func TestRunRejectsEmptyOMLXModel(t *testing.T) {
	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Body content"), 0o644); err != nil {
		t.Fatalf("write summary input: %v", err)
	}

	_, err := Run(context.Background(), Options{
		Input:     inputPath,
		Summarize: true,
		Model:     "omlx/",
		Timeout:   2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported oMLX model "omlx/"`) {
		t.Fatalf("expected unsupported oMLX model error, got %v", err)
	}
}
