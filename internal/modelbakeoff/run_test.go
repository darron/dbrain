package modelbakeoff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/llmprovider"
)

func TestParityPromptAndReasoningMetadataHelpers(t *testing.T) {
	t.Parallel()

	ollamaRef := llmprovider.ParseModelRef("ollama/dbrain:2026042701")
	ollamaParity := parityParamsForRun(llmprovider.ParityPresetDbrainModelfile, ollamaRef)
	if ollamaParity.Strictness != llmprovider.StrictnessStrict {
		t.Fatalf("ollama parity strictness = %q", ollamaParity.Strictness)
	}
	if len(ollamaParity.Sent) != 5 {
		t.Fatalf("expected all Modelfile params for Ollama, got %#v", ollamaParity.Sent)
	}

	lmStudioRef := llmprovider.ParseModelRef("lmstudio/qwen/qwen3.6-35b-a3b")
	lmStudioParity := parityParamsForRun(llmprovider.ParityPresetDbrainModelfile, lmStudioRef)
	if lmStudioParity.Strictness != llmprovider.StrictnessNonStrict {
		t.Fatalf("lmstudio parity strictness = %q", lmStudioParity.Strictness)
	}
	if _, ok := lmStudioParity.Omitted["min_p"]; !ok {
		t.Fatalf("expected min_p omission for LM Studio, got %#v", lmStudioParity.Omitted)
	}

	if got := promptParityStatusForSpec(llmprovider.ParityPresetDbrainModelfile, lmStudioRef.Spec); got != "requires-live-verification" {
		t.Fatalf("lmstudio prompt parity = %q", got)
	}
	if got := ollamaRef.Spec.ReasoningPolicy.StatusWithDirectCall; got != "think-disabled" {
		t.Fatalf("ollama reasoning mode = %q", got)
	}
	if got := lmStudioRef.Spec.ReasoningPolicy.StatusWithDirectCall; got != "unknown" {
		t.Fatalf("lmstudio reasoning mode = %q", got)
	}
}

func TestModelRunMetadataUsesProviderRegistry(t *testing.T) {
	t.Parallel()

	ref := llmprovider.ParseModelRef("omlx/qwen3.5-coder")
	parity := parityParamsForRun(llmprovider.ParityPresetDbrainModelfile, ref)
	run := newModelRunMetadata("omlx/qwen3.5-coder", ref, parity, llmprovider.ParityPresetDbrainModelfile)

	if run.Provider != "omlx" || run.APIModel != "qwen3.5-coder" {
		t.Fatalf("provider metadata = %+v", run)
	}
	if run.Transport != string(llmprovider.TransportOpenAIChat) {
		t.Fatalf("transport = %q", run.Transport)
	}
	if run.Local == nil || !*run.Local {
		t.Fatalf("local metadata = %#v", run.Local)
	}
	if run.ParamStrictness != llmprovider.StrictnessNonStrict {
		t.Fatalf("strictness = %q", run.ParamStrictness)
	}
	if run.PromptParityStatus != "requires-live-verification" || run.ReasoningModeStatus != "unknown" {
		t.Fatalf("parity metadata = %+v", run)
	}
}

func TestModelRunMetadataUsesConfiguredAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    base_url: http://127.0.0.1:8080/v1
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	reg, err := llmprovider.RegistryForRoot(root)
	if err != nil {
		t.Fatalf("RegistryForRoot: %v", err)
	}
	ref := reg.ParseModelRef("localai/test-model")
	parity := parityParamsForRun(llmprovider.ParityPresetDbrainModelfile, ref)
	run := newModelRunMetadata("localai/test-model", ref, parity, llmprovider.ParityPresetDbrainModelfile)

	if run.Provider != "localai" || run.APIModel != "test-model" {
		t.Fatalf("provider metadata = %+v", run)
	}
	if run.Transport != string(llmprovider.TransportOpenAIChat) {
		t.Fatalf("transport = %q", run.Transport)
	}
	if run.Local == nil || !*run.Local {
		t.Fatalf("local metadata = %#v", run.Local)
	}
}

func TestModelRunMetadataNoPresetOmitsPromptParity(t *testing.T) {
	t.Parallel()

	ref := llmprovider.ParseModelRef("ollama/dbrain:2026042701")
	parity := parityParamsForRun(llmprovider.ParityPresetNone, ref)
	run := newModelRunMetadata("ollama/dbrain:2026042701", ref, parity, llmprovider.ParityPresetNone)
	if run.PromptParityStatus != "" {
		t.Fatalf("prompt parity = %q", run.PromptParityStatus)
	}
}

func TestModelRunMetadataHostedPresetNotApplicable(t *testing.T) {
	t.Parallel()

	ref := llmprovider.ParseModelRef("openrouter/google/gemini-2.5-flash")
	parity := parityParamsForRun(llmprovider.ParityPresetDbrainModelfile, ref)
	run := newModelRunMetadata("openrouter/google/gemini-2.5-flash", ref, parity, llmprovider.ParityPresetDbrainModelfile)
	if run.PromptParityStatus != "not-applicable" {
		t.Fatalf("prompt parity = %q", run.PromptParityStatus)
	}
	if run.Local == nil || *run.Local {
		t.Fatalf("local metadata = %#v", run.Local)
	}
}
