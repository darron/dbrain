package llmprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTargetOMLXOmitsAuthorizationWhenKeyEmpty(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "omlx/qwen3.5-coder",
		Task:  TaskSummary,
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.BaseURL != "http://127.0.0.1:8000/v1" {
		t.Fatalf("BaseURL = %q", target.BaseURL)
	}
	if target.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", target.APIKey)
	}
	if target.AuthorizationHeader() != "" {
		t.Fatalf("AuthorizationHeader = %q", target.AuthorizationHeader())
	}
}

func TestResolveTargetOpenRouterRequiresKeyAndSummaryHeadersAreConfiguredOnly(t *testing.T) {
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	_, err := ResolveTarget(context.Background(), ResolveOptions{Model: "openrouter/google/gemini-test", Task: TaskSummary})
	if err == nil || !strings.Contains(err.Error(), "OpenRouter") || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing OpenRouter API key error, got %v", err)
	}

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "openrouter/google/gemini-test",
		Task:  TaskSummary,
		Env:   map[string]string{"DBRAIN_OPENROUTER_API_KEY": "router-key"},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Headers["HTTP-Referer"] != "" || target.Headers["X-Title"] != "" {
		t.Fatalf("summary should not default OpenRouter referer/title, got %#v", target.Headers)
	}
	if got := target.Headers["User-Agent"]; !strings.HasPrefix(got, "dbrain/") {
		t.Fatalf("expected default dbrain User-Agent for OpenRouter summary, got %q", got)
	}
}

func TestResolveTargetOpenRouterCategorizeKeepsHeaderDefaults(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "openrouter/google/gemini-test",
		Task:  TaskCategorize,
		Env:   map[string]string{"DBRAIN_OPENROUTER_API_KEY": "router-key"},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Headers["HTTP-Referer"] != "https://local.dbrain" || target.Headers["X-Title"] != "dbrain categorize" {
		t.Fatalf("categorize OpenRouter headers = %#v", target.Headers)
	}
}

func TestResolveTargetConfiguredAliasResolvesOnlySelectedSecret(t *testing.T) {
	root := t.TempDir()
	writeProviderConfig(t, root, `
openrouter:
  api_key: env:MISSING_OPENROUTER_KEY
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: env:LOCALAI_KEY
    local: true
`)
	t.Setenv("LOCALAI_KEY", "alias-secret")

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		RootDir: root,
		Model:   "localai/test-model",
		Task:    TaskSummary,
	})
	if err != nil {
		t.Fatalf("ResolveTarget should not read unrelated OpenRouter secret: %v", err)
	}
	if target.APIKey != "alias-secret" {
		t.Fatalf("APIKey = %q", target.APIKey)
	}
}

func TestResolveTargetEmptyOverrideFallsThroughToSelectedSecret(t *testing.T) {
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	root := t.TempDir()
	writeProviderConfig(t, root, `
openrouter:
  api_key: env:OPENROUTER_TEST_KEY
`)
	t.Setenv("OPENROUTER_TEST_KEY", "resolved-openrouter-key")

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		RootDir: root,
		Model:   "openrouter/google/gemini-test",
		Task:    TaskCategorize,
		Overrides: map[Provider]ProviderOverrides{
			ProviderOpenRouter: {BaseURL: "", APIKey: ""},
		},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.APIKey != "resolved-openrouter-key" {
		t.Fatalf("APIKey = %q", target.APIKey)
	}
}

func TestResolveTargetAliasUnsetSecretRefDoesNotBecomeBearerToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProviderConfig(t, root, `
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: env:MISSING_LOCALAI_KEY
    local: true
`)

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		RootDir: root,
		Model:   "localai/test-model",
		Task:    TaskSummary,
	})
	if err == nil {
		t.Fatalf("expected unresolved alias secret ref error, got target %+v", target)
	}
	if strings.Contains(err.Error(), "Bearer env:MISSING_LOCALAI_KEY") {
		t.Fatalf("secret ref leaked as bearer token: %v", err)
	}
}

func TestResolveTargetNormalizesOpenAIBaseURLIdempotently(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "omlx/qwen3.5-coder",
		Task:  TaskSummary,
		Overrides: map[Provider]ProviderOverrides{
			ProviderOMLX: {BaseURL: "http://127.0.0.1:8000/v1"},
		},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.BaseURL != "http://127.0.0.1:8000/v1" {
		t.Fatalf("BaseURL = %q", target.BaseURL)
	}
}

func TestResolveTargetNormalizesOpenRouterAPIBaseURLIdempotently(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "openrouter/google/gemini-test",
		Task:  TaskSummary,
		Env:   map[string]string{"DBRAIN_OPENROUTER_API_KEY": "router-key"},
		Overrides: map[Provider]ProviderOverrides{
			ProviderOpenRouter: {BaseURL: "https://openrouter.ai/api"},
		},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("BaseURL = %q", target.BaseURL)
	}
}

func writeProviderConfig(t *testing.T, root string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config.yaml: %v", err)
	}
}
