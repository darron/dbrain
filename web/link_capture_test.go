package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/linkextract"
)

func TestWebDeferredLinkCaptureReturnsBeforeFeedDiscovery(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	page := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		fetches.Add(1)
	}))
	t.Cleanup(page.Close)

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{"url":"`+page.URL+`/article","enrich":false,"defer":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("deferred status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Deferred bool `json:"deferred"`
		Queued   int  `json:"queued"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode deferred response: %v", err)
	}
	if !response.Deferred || response.Queued != 1 {
		t.Fatalf("deferred response = %+v", response)
	}
	if got := fetches.Load(); got != 0 {
		t.Fatalf("deferred request performed %d outbound feed-discovery fetches", got)
	}
	pending, err := st.ListPendingLinkCaptures(t.Context(), nowForLinkCaptureTest(), 10)
	if err != nil {
		t.Fatalf("list pending captures: %v", err)
	}
	if len(pending) != 1 || pending[0].Candidate.CanonicalURL != page.URL+"/article" {
		t.Fatalf("pending captures = %+v", pending)
	}
}

func TestWebDeferredLinkCaptureLogsAdmissionPoolStats(t *testing.T) {
	t.Parallel()

	var logOutput bytes.Buffer
	cfg, st := openTestStore(t)
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{LogOutput: &logOutput})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{"url":"https://example.com/article","enrich":false,"defer":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deferred status = %d, body=%s", rec.Code, rec.Body.String())
	}

	logOutputText := logOutput.String()
	for _, want := range []string{"link capture admission", "admission_db_max_open=1", "admission_db_wait_count=", "admission_db_wait_duration="} {
		if !strings.Contains(logOutputText, want) {
			t.Fatalf("admission log missing %q: %s", want, logOutputText)
		}
	}
}

func TestWebDeferredLinkCaptureStartsAndDrainsWorker(t *testing.T) {
	t.Parallel()

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Deferred article</title></head><body>content</body></html>`))
	}))
	t.Cleanup(page.Close)

	cfg, st := openTestStore(t)
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{Context: lifecycleCtx})
	if err != nil {
		cancel()
		t.Fatalf("NewHandlerWithOptions: %v", err)
	}
	owner, ok := handler.(CloseableHandler)
	if !ok {
		cancel()
		t.Fatal("handler does not expose lifecycle cleanup")
	}
	t.Cleanup(func() {
		cancel()
		_ = owner.Close()
	})

	url := page.URL + "/article"
	candidate, ok := linkextract.NormalizeCandidate(url)
	if !ok {
		t.Fatal("test page URL did not normalize")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{"url":"`+url+`","enrich":false,"defer":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deferred status = %d, body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := st.GetSource(t.Context(), candidate.SourceKey); err == nil {
			pending, listErr := st.ListPendingLinkCaptures(t.Context(), time.Now().UTC().Add(time.Second), 10)
			if listErr != nil {
				t.Fatalf("list pending captures after worker drain: %v", listErr)
			}
			if len(pending) != 0 {
				t.Fatalf("worker-created source still has pending capture: %+v", pending)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("deferred worker did not create source %q before timeout", candidate.SourceKey)
}

func TestWebDeferredLinkCaptureKeepsValidBatchEntriesWhenOneIsInvalid(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{"urls":["https://example.com/valid","not a url"],"enrich":false,"defer":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deferred mixed-batch status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Queued  int `json:"queued"`
		Results []struct {
			Queued bool   `json:"queued"`
			Error  string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode mixed-batch response: %v", err)
	}
	if response.Queued != 1 || len(response.Results) != 2 || !response.Results[0].Queued || response.Results[1].Error == "" {
		t.Fatalf("mixed-batch response = %+v", response)
	}
	pending, err := st.ListPendingLinkCaptures(t.Context(), nowForLinkCaptureTest(), 10)
	if err != nil {
		t.Fatalf("list mixed-batch captures: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("mixed-batch pending captures = %+v", pending)
	}
}

func nowForLinkCaptureTest() time.Time {
	return time.Now().UTC().Add(time.Second)
}
