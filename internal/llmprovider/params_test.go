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

	omlx := DbrainParityForProvider(ProviderOMLX)
	if omlx.Sent["temperature"] != 0.6 || omlx.Sent["top_p"] != 0.95 || omlx.Sent["top_k"] != 20 || omlx.Sent["repeat_penalty"] != 1.0 {
		t.Fatalf("omlx sent params = %#v", omlx.Sent)
	}
	if omlx.Omitted["min_p"] == "" {
		t.Fatalf("expected omlx min_p omission reason, got %#v", omlx.Omitted)
	}
	if omlx.Strictness != StrictnessNonStrict {
		t.Fatalf("omlx strictness = %q", omlx.Strictness)
	}

	none := DbrainParityForProvider(ProviderOpenRouter)
	if len(none.Requested) != 0 || len(none.Sent) != 0 || len(none.Omitted) != 0 {
		t.Fatalf("openrouter should not receive local parity params, got %#v", none)
	}
}

func TestAccountParamsForSpec(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	ollamaSpec, _ := reg.Spec(ProviderOllama)
	openAISpec, _ := reg.Spec(ProviderOMLX)
	requested := map[string]any{
		"temperature": 0.4,
		"top_p":       0.9,
		"min_p":       0.1,
		"unknown":     "value",
	}

	ollama := AccountParamsForSpec(ollamaSpec, requested)
	if ollama.Sent["min_p"] != 0.1 {
		t.Fatalf("ollama min_p not sent: %#v", ollama.Sent)
	}
	if ollama.Omitted["unknown"] == "" {
		t.Fatalf("expected unknown omission, got %#v", ollama.Omitted)
	}
	if ollama.Strictness != StrictnessNonStrict {
		t.Fatalf("ollama strictness = %q", ollama.Strictness)
	}

	openai := AccountParamsForSpec(openAISpec, requested)
	if _, ok := openai.Sent["min_p"]; ok {
		t.Fatalf("openai min_p should be omitted, sent=%#v", openai.Sent)
	}
	if openai.Omitted["min_p"] == "" || openai.Omitted["unknown"] == "" {
		t.Fatalf("expected omitted min_p and unknown, got %#v", openai.Omitted)
	}
	if openai.Strictness != StrictnessNonStrict {
		t.Fatalf("openai strictness = %q", openai.Strictness)
	}
}
