package llmprovider

import (
	"strings"
	"testing"
)

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
		{name: "omlx slash", input: "omlx/qwen3.5-coder", provider: ProviderOMLX, apiModel: "qwen3.5-coder", original: "omlx/qwen3.5-coder", ok: true},
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

func TestDefaultRegistryParsesBuiltInProviderSpecs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model     string
		provider  Provider
		apiModel  string
		transport Transport
		local     bool
	}{
		{"ollama/qwen3.6:35b", ProviderOllama, "qwen3.6:35b", TransportOllamaChat, true},
		{"openrouter/google/gemini-2.5-flash", ProviderOpenRouter, "google/gemini-2.5-flash", TransportOpenAIChat, false},
		{"lmstudio/qwen/qwen3.6-35b-a3b", ProviderLMStudio, "qwen/qwen3.6-35b-a3b", TransportOpenAIChat, true},
		{"omlx/qwen3.5-coder", ProviderOMLX, "qwen3.5-coder", TransportOpenAIChat, true},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()
			ref := ParseModelRef(tt.model)
			if !ref.ProviderQualified || ref.Provider != tt.provider || ref.APIModel != tt.apiModel {
				t.Fatalf("ParseModelRef(%q) = %+v", tt.model, ref)
			}
			if ref.Spec == nil {
				t.Fatalf("missing provider spec for %q", tt.model)
			}
			if ref.Spec.Transport != tt.transport || ref.Spec.Local != tt.local {
				t.Fatalf("spec = %+v", ref.Spec)
			}
		})
	}
}

func TestDefaultRegistryOMLXAllowsModelDependentImages(t *testing.T) {
	t.Parallel()

	ref := ParseModelRef("omlx/Qwen3.6-35B-A3B-MLX-4bit")
	if ref.Spec == nil {
		t.Fatal("missing oMLX provider spec")
	}
	if ref.Spec.Capabilities.Images != CapabilityModelDependentOrUnverified {
		t.Fatalf("oMLX image capability = %q", ref.Spec.Capabilities.Images)
	}
}

func TestRegistryParsesConfiguredOpenAICompatibleAlias(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.Register(ProviderSpec{
		ID:             Provider("localai"),
		DisplayName:    "localai",
		Transport:      TransportOpenAIChat,
		Local:          true,
		DefaultBaseURL: "http://127.0.0.1:8080/v1",
		Capabilities:   TextOnlyCapabilities(),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ref := reg.ParseModelRef("localai/meta-llama")
	if !ref.ProviderQualified || ref.Provider != Provider("localai") || ref.APIModel != "meta-llama" {
		t.Fatalf("alias ref = %+v", ref)
	}
	if ref.Spec == nil || ref.Spec.Transport != TransportOpenAIChat || !ref.Spec.Local {
		t.Fatalf("alias spec = %+v", ref.Spec)
	}
}

func TestRegistryRejectsConfiguredAliasOverBuiltIn(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	err := reg.Register(ProviderSpec{ID: ProviderOllama, Transport: TransportOpenAIChat})
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate provider error, got %v", err)
	}
}

func TestEmptyProviderRefIncludesOMLXAndAliases(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.Register(ProviderSpec{ID: Provider("localai"), Transport: TransportOpenAIChat, DefaultBaseURL: "http://127.0.0.1:8080/v1"}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"omlx/", "omlx:", "localai/", "localai:"} {
		provider, ok := reg.EmptyProviderRef(model)
		if !ok {
			t.Fatalf("expected empty provider for %q", model)
		}
		if provider != Provider(strings.TrimRight(model, "/:")) {
			t.Fatalf("provider for %q = %q", model, provider)
		}
	}
}

func TestEmptyProviderRef(t *testing.T) {
	t.Parallel()

	provider, ok := EmptyProviderRef("lmstudio/")
	if !ok || provider != ProviderLMStudio {
		t.Fatalf("EmptyProviderRef(lmstudio/) = (%q,%v)", provider, ok)
	}
	if provider, ok := EmptyProviderRef("lmstudio/qwen"); ok {
		t.Fatalf("expected non-empty ref not to match, got (%q,%v)", provider, ok)
	}
}
