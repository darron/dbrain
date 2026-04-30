package itemcategorize

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	_, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", Options{
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
