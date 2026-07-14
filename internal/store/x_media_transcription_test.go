package store

import "testing"

func TestXMediaTranscriptInputHashUsesSortedMediaAndResolvedSettings(t *testing.T) {
	t.Parallel()

	settings := XMediaTranscriptionInputSettings{
		Backend:    "whisper.cpp",
		Model:      "ggml-base.en",
		Language:   "en",
		VADEnabled: true,
	}
	first, err := xMediaTranscriptionInputHash([]string{"sha256:bbb", "sha256:aaa"}, []XMediaTranscriptionInputSettings{settings})
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash: %v", err)
	}
	second, err := xMediaTranscriptionInputHash([]string{"sha256:aaa", "sha256:bbb"}, []XMediaTranscriptionInputSettings{settings})
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash reversed: %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("input hash must be non-empty and order independent: first=%q second=%q", first, second)
	}

	settings.Language = "fr"
	changed, err := xMediaTranscriptionInputHash([]string{"sha256:aaa", "sha256:bbb"}, []XMediaTranscriptionInputSettings{settings})
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash changed settings: %v", err)
	}
	if changed == first {
		t.Fatal("resolved language change did not change input hash")
	}
}

func TestXMediaTranscriptInputHashCanonicalizesMixedSettingsAndDistinguishesAutomaticModel(t *testing.T) {
	t.Parallel()

	automatic := XMediaTranscriptionInputSettings{Backend: "macwhisper", Model: "automatic", Language: "auto"}
	configured := XMediaTranscriptionInputSettings{Backend: "macwhisper", Model: "whisperkit:openai_whisper-base", Language: "auto"}
	whisperCPP := XMediaTranscriptionInputSettings{Backend: "whisper.cpp", Model: "ggml-base.bin", Language: "en", VADEnabled: true}

	first, err := xMediaTranscriptionInputHash(
		[]string{"sha256:bbb", "sha256:aaa"},
		[]XMediaTranscriptionInputSettings{automatic, whisperCPP},
	)
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash mixed: %v", err)
	}
	reordered, err := xMediaTranscriptionInputHash(
		[]string{"sha256:aaa", "sha256:bbb"},
		[]XMediaTranscriptionInputSettings{whisperCPP, automatic},
	)
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash reordered mixed: %v", err)
	}
	if first != reordered {
		t.Fatalf("mixed input hash is order-dependent: first=%q reordered=%q", first, reordered)
	}

	configuredHash, err := xMediaTranscriptionInputHash(
		[]string{"sha256:aaa", "sha256:bbb"},
		[]XMediaTranscriptionInputSettings{whisperCPP, configured},
	)
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash configured: %v", err)
	}
	if configuredHash == first {
		t.Fatal("automatic and configured MacWhisper model selections produced the same input hash")
	}
}
