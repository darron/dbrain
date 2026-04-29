package itemcategorize

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
