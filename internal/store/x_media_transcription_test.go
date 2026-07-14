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
	first, err := xMediaTranscriptionInputHash([]string{"sha256:bbb", "sha256:aaa"}, settings)
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash: %v", err)
	}
	second, err := xMediaTranscriptionInputHash([]string{"sha256:aaa", "sha256:bbb"}, settings)
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash reversed: %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("input hash must be non-empty and order independent: first=%q second=%q", first, second)
	}

	settings.Language = "fr"
	changed, err := xMediaTranscriptionInputHash([]string{"sha256:aaa", "sha256:bbb"}, settings)
	if err != nil {
		t.Fatalf("xMediaTranscriptionInputHash changed settings: %v", err)
	}
	if changed == first {
		t.Fatal("resolved language change did not change input hash")
	}
}
