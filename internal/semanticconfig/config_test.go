package semanticconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectiveModePrecedenceAndConflict(t *testing.T) {
	for _, configured := range []Mode{ModeOff, ModeShadow, ModeOn} {
		got, err := EffectiveMode(configured, false, false)
		if err != nil || got != configured {
			t.Fatalf("EffectiveMode(%q, false, false) = %q, %v", configured, got, err)
		}
		got, err = EffectiveMode(configured, true, false)
		if err != nil || got != ModeOn {
			t.Fatalf("EffectiveMode(%q, true, false) = %q, %v", configured, got, err)
		}
		got, err = EffectiveMode(configured, false, true)
		if err != nil || got != ModeOff {
			t.Fatalf("EffectiveMode(%q, false, true) = %q, %v", configured, got, err)
		}
	}
	got, err := EffectiveMode(ModeShadow, true, true)
	if got != "" || !errors.Is(err, ErrConflictingOverrides) {
		t.Fatalf("conflict = %q, %v", got, err)
	}
}

func TestResolveDefaultsKeepSemanticRetrievalOffAndUnconfigured(t *testing.T) {
	t.Parallel()

	got, err := Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := Config{
		Mode:                   ModeOff,
		Provider:               ProviderOllama,
		IndexBackend:           IndexBackendExact,
		CandidateDepth:         50,
		ExactFallbackMaxChunks: 25000,
		OllamaBaseURL:          "http://127.0.0.1:11434",
	}
	if got != want {
		t.Fatalf("Resolve = %#v, want %#v", got, want)
	}
}

func TestResolveUsesRuntimeEnvironmentPrecedenceAndExistingOllamaEndpoint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config.yaml"), `
research:
  semantic:
    mode: shadow
    provider: ollama
    model: yaml-model
    dimensions: 256
    index_backend: exact
    candidate_depth: 61
    exact_fallback_max_chunks: 26000
ollama:
  base_url: http://yaml-ollama.lan:11434/api
`)
	writeFile(t, filepath.Join(root, ".envrc"), strings.Join([]string{
		"DBRAIN_RESEARCH_SEMANTIC_MODEL=dotenv-model",
		"DBRAIN_RESEARCH_SEMANTIC_DIMENSIONS=512",
		"DBRAIN_RESEARCH_SEMANTIC_CANDIDATE_DEPTH=75",
		"DBRAIN_OLLAMA_BASE_URL=http://dotenv-ollama.lan:11434/v1",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(root, ".env"), strings.Join([]string{
		"DBRAIN_RESEARCH_SEMANTIC_MODEL=lower-priority-env-model",
		"DBRAIN_RESEARCH_SEMANTIC_DIMENSIONS=1024",
		"DBRAIN_RESEARCH_SEMANTIC_EXACT_FALLBACK_MAX_CHUNKS=27000",
	}, "\n")+"\n")
	t.Setenv("DBRAIN_RESEARCH_SEMANTIC_MODEL", "shell-model")
	t.Setenv("DBRAIN_RESEARCH_SEMANTIC_CANDIDATE_DEPTH", "90")

	got, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Mode != ModeShadow || got.Model != "shell-model" || got.Dimensions != 512 {
		t.Fatalf("resolved semantic config = %#v", got)
	}
	if got.CandidateDepth != 90 || got.ExactFallbackMaxChunks != 27000 {
		t.Fatalf("resolved depths = %#v", got)
	}
	if got.OllamaBaseURL != "http://dotenv-ollama.lan:11434" {
		t.Fatalf("OllamaBaseURL = %q", got.OllamaBaseURL)
	}
}

func TestResolveRejectsInvalidSemanticConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"mode", "research:\n  semantic:\n    mode: maybe\n", "mode"},
		{"hosted provider", "research:\n  semantic:\n    provider: openrouter\n", "only local Ollama"},
		{"index backend", "research:\n  semantic:\n    index_backend: approximate\n", "index_backend"},
		{"missing model", "research:\n  semantic:\n    mode: shadow\n    dimensions: 256\n", "model is required"},
		{"missing dimensions", "research:\n  semantic:\n    mode: on\n    model: embed-model\n", "dimensions must be positive"},
		{"bad dimensions", "research:\n  semantic:\n    dimensions: nope\n", "dimensions"},
		{"candidate depth", "research:\n  semantic:\n    candidate_depth: 0\n", "candidate_depth must be positive"},
		{"fallback depth", "research:\n  semantic:\n    exact_fallback_max_chunks: -1\n", "exact_fallback_max_chunks must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "config.yaml"), tt.yaml)
			_, err := Resolve(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Resolve error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestResolveDiagnosticReportsIncompleteProfileButRejectsMalformedConfiguration(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config.yaml"), "research:\n  semantic:\n    mode: on\n    model: ''\n    dimensions: 0\n")
	got, err := ResolveDiagnostic(root)
	if err != nil {
		t.Fatalf("ResolveDiagnostic incomplete profile: %v", err)
	}
	if got.Mode != ModeOn || got.Model != "" || got.Dimensions != 0 {
		t.Fatalf("diagnostic config = %#v", got)
	}
	writeFile(t, filepath.Join(root, "config.yaml"), "research:\n  semantic:\n    mode: broken\n")
	if _, err := ResolveDiagnostic(root); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("ResolveDiagnostic malformed mode error = %v", err)
	}
}

func TestResolveUsesSharedOllamaBaseURLWithoutResolvingSecrets(t *testing.T) {
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config.yaml"), `
ollama:
  base_url: http://semantic-ollama.lan:11434/v1
  api_key: op://missing/vault/key
`)
	got, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve must not resolve Ollama secrets while semantic mode is off: %v", err)
	}
	if got.OllamaBaseURL != "http://semantic-ollama.lan:11434" {
		t.Fatalf("OllamaBaseURL = %q", got.OllamaBaseURL)
	}
}

func writeFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
