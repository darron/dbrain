package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type fakeArchiveProxy struct {
	body      []byte
	signedURL string
}

func (f *fakeArchiveProxy) GetObject(_ context.Context, _, _ string, rangeHeader string) (archiveObject, error) {
	if rangeHeader != "" {
		return archiveObject{
			Body:          io.NopCloser(bytes.NewReader(f.body[:4])),
			ContentType:   "video/mp4",
			ContentLength: 4,
			ContentRange:  "bytes 0-3/12",
			ETag:          "etag-1",
			LastModified:  time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC),
		}, nil
	}
	return archiveObject{
		Body:          io.NopCloser(bytes.NewReader(f.body)),
		ContentType:   "video/mp4",
		ContentLength: int64(len(f.body)),
		ETag:          "etag-1",
		LastModified:  time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeArchiveProxy) HeadObject(_ context.Context, _, _ string) (archiveObject, error) {
	return archiveObject{
		ContentType:   "video/mp4",
		ContentLength: int64(len(f.body)),
		ETag:          "etag-1",
		LastModified:  time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeArchiveProxy) PresignGetObject(_ context.Context, _, _ string, ttl time.Duration) (string, time.Time, error) {
	return f.signedURL, time.Date(2026, time.April, 25, 22, 5, 0, 0, time.UTC).Add(ttl), nil
}

func TestWebHandlerServesBootstrapSearchGetAndResearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openTestStore(t)

	itemID, sourceKey := seedTestData(t, ctx, cfg, st)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	t.Run("bootstrap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response BootstrapResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode bootstrap: %v", err)
		}
		if response.App.Name != "dbrain" {
			t.Fatalf("expected app name dbrain, got %q", response.App.Name)
		}
		for _, forbidden := range []string{`"root_dir"`, `"vault_dir"`, `"db_path"`, cfg.RootDir, cfg.VaultDir, cfg.DBPath} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("bootstrap response exposed host-local metadata %q: %s", forbidden, rec.Body.String())
			}
		}
		if len(response.SourceActivity.RecentSuccesses) == 0 {
			t.Fatalf("expected bootstrap source activity successes")
		}
		if len(response.SourceActivity.RecentFailures) == 0 {
			t.Fatalf("expected bootstrap source activity failures")
		}
		if response.SourceActivity.Window == "" {
			t.Fatalf("expected bootstrap source activity window")
		}
	})

	t.Run("source activity", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/stats/source-activity?limit=3", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response store.SourceActivityFeed
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode source activity: %v", err)
		}
		if len(response.RecentSuccesses) == 0 {
			t.Fatalf("expected at least one recent success")
		}
		if len(response.RecentFailures) == 0 {
			t.Fatalf("expected at least one recent failure")
		}
		if len(response.FailureKinds) == 0 {
			t.Fatalf("expected source activity failure kind buckets")
		}
		if len(response.FailureDomains) == 0 {
			t.Fatalf("expected source activity failure domain buckets")
		}
		if len(response.FailureTable) != 2 || response.FailureTableTotal != 2 {
			t.Fatalf("expected source activity failure table, got total=%d rows=%+v", response.FailureTableTotal, response.FailureTable)
		}
		if response.FailureTableSort != "newest" {
			t.Fatalf("expected default failure table sort newest, got %q", response.FailureTableSort)
		}
		if len(response.Trend) == 0 || response.TrendBucket == "" {
			t.Fatalf("expected source activity trend data, got bucket=%q trend=%+v", response.TrendBucket, response.Trend)
		}
	})

	t.Run("source activity filters", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/stats/source-activity?limit=5&source_type=web&domain=broken.example.com&status=error&failure_kind=connectivity&message=connect&window=24h", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response store.SourceActivityFeed
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode filtered source activity: %v", err)
		}
		if len(response.RecentSuccesses) != 0 {
			t.Fatalf("expected no filtered successes, got %+v", response.RecentSuccesses)
		}
		if len(response.RecentFailures) != 2 {
			t.Fatalf("expected 2 filtered failures, got %+v", response.RecentFailures)
		}
		if response.RecentFailures[0].SourceKey != "src:test-agent-memory-failure" {
			t.Fatalf("unexpected filtered failure: %+v", response.RecentFailures[0])
		}
		if len(response.FailureHotspots) != 1 {
			t.Fatalf("expected 1 failure hotspot, got %+v", response.FailureHotspots)
		}
		if response.FailureHotspots[0].Domain != "broken.example.com" || response.FailureHotspots[0].Count != 2 {
			t.Fatalf("unexpected filtered hotspot: %+v", response.FailureHotspots[0])
		}
		if response.FailureHotspots[0].FailureKind != "connectivity" {
			t.Fatalf("unexpected filtered hotspot failure kind: %+v", response.FailureHotspots[0])
		}
		if len(response.FailureKinds) != 1 || response.FailureKinds[0].Key != "connectivity" || response.FailureKinds[0].Count != 2 {
			t.Fatalf("unexpected filtered failure kind buckets: %+v", response.FailureKinds)
		}
		if len(response.FailureStatuses) != 1 || response.FailureStatuses[0].Key != "error" || response.FailureStatuses[0].Count != 2 {
			t.Fatalf("unexpected filtered failure status buckets: %+v", response.FailureStatuses)
		}
		if len(response.FailureDomains) != 1 || response.FailureDomains[0].Key != "broken.example.com" || response.FailureDomains[0].Count != 2 {
			t.Fatalf("unexpected filtered failure domain buckets: %+v", response.FailureDomains)
		}
		if len(response.FailureTable) != 2 || response.FailureTableTotal != 2 {
			t.Fatalf("unexpected filtered failure table: total=%d rows=%+v", response.FailureTableTotal, response.FailureTable)
		}
	})

	t.Run("source activity failure table paging", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/stats/source-activity?limit=1&source_type=web&domain=broken.example.com&status=error&failure_sort=oldest&failure_offset=1&window=24h", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response store.SourceActivityFeed
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode paged source activity: %v", err)
		}
		if response.FailureTableTotal != 2 || response.FailureTableOffset != 1 || response.FailureTableSort != "oldest" {
			t.Fatalf("unexpected paged failure table metadata: %+v", response)
		}
		if len(response.FailureTable) != 1 || response.FailureTable[0].SourceKey != "src:test-agent-memory-failure-two" {
			t.Fatalf("unexpected paged failure rows: %+v", response.FailureTable)
		}
	})

	t.Run("search", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/search?q=agent+memory&limit=5", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response SearchResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode search: %v", err)
		}
		if len(response.Results) == 0 {
			t.Fatalf("expected at least one search result")
		}
	})

	t.Run("get item", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup=item:test-agent-memory", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get item: %v", err)
		}
		if response.Kind != "item" {
			t.Fatalf("expected kind item, got %q", response.Kind)
		}
		if response.Item == nil || response.Item.ID != itemID {
			t.Fatalf("expected item %d, got %+v", itemID, response.Item)
		}
		if response.NoteContent == "" {
			t.Fatalf("expected item note content")
		}
		if len(response.LinkedSources) == 0 {
			t.Fatalf("expected linked sources")
		}
	})

	t.Run("get item note error hides absolute path", func(t *testing.T) {
		notePath := filepath.Join(cfg.VaultDir, "items", "test-agent-memory.md")
		missingPath := notePath + ".missing"
		if err := os.Rename(notePath, missingPath); err != nil {
			t.Fatalf("hide note fixture: %v", err)
		}
		defer func() {
			if err := os.Rename(missingPath, notePath); err != nil {
				t.Fatalf("restore note fixture: %v", err)
			}
		}()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup=item:test-agent-memory", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get item: %v", err)
		}
		if response.NoteError == "" {
			t.Fatalf("expected note error")
		}
		if strings.Contains(response.NoteError, cfg.VaultDir) || strings.Contains(response.NoteError, notePath) {
			t.Fatalf("note error exposed absolute path: %q", response.NoteError)
		}
		if !strings.Contains(response.NoteError, "items/test-agent-memory.md") {
			t.Fatalf("note error should include relative path, got %q", response.NoteError)
		}
	})

	t.Run("get source", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup="+sourceKey, nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get source: %v", err)
		}
		if response.Kind != "source" {
			t.Fatalf("expected kind source, got %q", response.Kind)
		}
		if response.Source == nil || response.Source.SourceKey != sourceKey {
			t.Fatalf("expected source %q, got %+v", sourceKey, response.Source)
		}
		if len(response.Backlinks) == 0 {
			t.Fatalf("expected backlinks")
		}
		if response.Backlinks[0].UserTags != "agent-memory, retrieval" {
			t.Fatalf("expected backlink tags, got %+v", response.Backlinks[0])
		}
	})

	t.Run("tag source", func(t *testing.T) {
		body := bytes.NewBufferString(`{"lookup":"` + sourceKey + `","tags":"source-memory,example-source"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/tag", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response model.SourceDocument
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode tagged source: %v", err)
		}
		if response.UserTags != "source-memory,example-source" {
			t.Fatalf("expected source tags, got %q", response.UserTags)
		}
	})

	t.Run("research pack", func(t *testing.T) {
		body := bytes.NewBufferString(`{"question":"What do I have on agent memory?","limit":4,"include_related":true,"related_limit":2,"max_chars_per_doc":4000,"disable_planner":true}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode research response: %v", err)
		}
		if response["schema_version"] != "research_pack.v1" {
			t.Fatalf("expected schema version, got %#v", response["schema_version"])
		}
		if response["question"] != "What do I have on agent memory?" {
			t.Fatalf("expected echoed question, got %#v", response["question"])
		}
		plan := response["query_plan"].(map[string]any)
		if plan["max_chars_per_doc"] != float64(4000) {
			t.Fatalf("expected web-requested max chars in query plan, got %#v", plan)
		}
		evidence := response["evidence"].([]any)
		if len(evidence) == 0 {
			t.Fatalf("expected evidence rows, got %#v", response)
		}
		coverage := response["coverage"].(map[string]any)
		if coverage["recall_note"] == "" {
			t.Fatalf("expected recall note in research pack, got %#v", coverage)
		}
		nextSteps := response["next_steps"].([]any)
		if len(nextSteps) == 0 || nextSteps[0].(map[string]any)["action"] == "" {
			t.Fatalf("expected semantic next steps, got %#v", nextSteps)
		}
	})

	t.Run("research invalid body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", bytes.NewBufferString(`{`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("research requires question", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", bytes.NewBufferString(`{"question":" "}`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("ask route removed", func(t *testing.T) {
		body := bytes.NewBufferString(`{"question":"What do I have on agent memory?","limit":4}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/ask", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("save chat transcript", func(t *testing.T) {
		request := ChatTranscriptSaveRequest{
			PinnedEvidenceKeys: []string{sourceKey},
			SelectedLookup:     sourceKey,
			Turns: []ChatTranscriptTurn{
				{
					ID:                "chat:turn-1",
					Question:          "What do I know about Tanka?",
					RetrievalQuestion: "Current question: What do I know about Tanka?",
					Status:            "ready",
					Answer:            "Tanka uses Jsonnet for Kubernetes configuration [" + sourceKey + "].",
					CreatedAt:         "2026-05-02T16:00:00Z",
					Citations: []brainresearch.Citation{
						{SourceKey: sourceKey, Title: "Agent Memory Article", NotePath: "sources/test-agent-memory.md", Kind: "source"},
					},
					ResearchPack: brainresearch.Pack{
						SchemaVersion: brainresearch.SchemaVersion,
						Question:      "Current question: What do I know about Tanka?",
						QueryPlan: brainresearch.QueryPlan{
							TextQuery:  "tanka helm",
							QueryTerms: []string{"tanka", "helm"},
							TagQueries: []string{"tanka"},
						},
						Coverage: brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
						Evidence: []ask.Evidence{
							{
								SourceKey:  sourceKey,
								Kind:       "source",
								Title:      "Agent Memory Article",
								URL:        "https://example.com/agent-memory",
								NotePath:   "sources/test-agent-memory.md",
								Summary:    "Summary about durable retrieval.",
								Excerpt:    "Excerpt about citations and retrieval.",
								SourceType: "web",
							},
						},
					},
				},
			},
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal transcript request: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chat/transcripts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response ChatTranscriptSaveResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode transcript save response: %v", err)
		}
		if response.Turns != 1 || response.Bytes == 0 || response.Path == "" {
			t.Fatalf("unexpected transcript save response: %+v", response)
		}
		transcriptRoot := filepath.Join(cfg.DataDir, "chat-transcripts")
		if filepath.IsAbs(response.Path) {
			t.Fatalf("transcript response path should be relative, got %s", response.Path)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte(cfg.DataDir)) {
			t.Fatalf("transcript response exposed data dir: %s", rec.Body.String())
		}
		transcriptPath := filepath.Clean(filepath.Join(cfg.DataDir, filepath.FromSlash(response.Path)))
		if !strings.HasPrefix(transcriptPath, transcriptRoot+string(os.PathSeparator)) {
			t.Fatalf("transcript response path escapes expected root: %s", response.Path)
		}
		content, err := os.ReadFile(transcriptPath)
		if err != nil {
			t.Fatalf("read transcript: %v", err)
		}
		for _, want := range []string{
			"diagnostic export only",
			"What do I know about Tanka?",
			"Tanka uses Jsonnet",
			sourceKey,
			"Summary about durable retrieval.",
		} {
			if !bytes.Contains(content, []byte(want)) {
				t.Fatalf("expected transcript to contain %q:\n%s", want, string(content))
			}
		}
	})

	t.Run("add link", func(t *testing.T) {
		body := bytes.NewBufferString(`{"url":"https://example.com/manual?utm_source=test"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/links", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Queued  int `json:"queued"`
			Results []struct {
				CanonicalURL string `json:"canonical_url"`
				SourceType   string `json:"source_type"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode add link response: %v", err)
		}
		if response.Queued != 1 || len(response.Results) != 1 || response.Results[0].CanonicalURL != "https://example.com/manual" || response.Results[0].SourceType != "web" {
			t.Fatalf("unexpected add link response %+v", response)
		}
	})
}

func TestWebHandlerAddLinkRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := bytes.NewBufferString(`{"url":"not a url"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/links", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebHandlerServesIndexHTML(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	for _, path := range []string{"/", "/admin"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if body := rec.Body.String(); body == "" || !bytes.Contains(rec.Body.Bytes(), []byte("dbrain")) {
				t.Fatalf("expected index html to mention dbrain, got %q", body)
			}
		})
	}
}

func TestResearchSynthesizeStreamsFinalAnswer(t *testing.T) {
	cfg, st := openTestStore(t)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Agent memory requires durable retrieval [src:test]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan: brainresearch.QueryPlan{
			TextQuery:  "agent memory",
			QueryTerms: []string{"agent", "memory"},
			TagQueries: []string{"agent-memory"},
		},
		Coverage: brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{
				SourceKey: "src:test",
				Kind:      "source",
				Title:     "Agent Memory",
				URL:       "https://example.com/agent-memory",
				NotePath:  "sources/test.md",
				Summary:   "Agent memory benefits from durable retrieval and citations.",
			},
		},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:         pack.Question,
		ResearchPack:     pack,
		Model:            "ollama/qwen-test",
		MaxEvidenceChars: 4000,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", got)
	}
	events := parseSSEEvents(t, rec.Body.String())
	for _, event := range []string{"start", "answer", "citation", "done"} {
		if _, ok := events[event]; !ok {
			t.Fatalf("expected %s event in stream:\n%s", event, rec.Body.String())
		}
	}
	if _, ok := events["token"]; ok {
		t.Fatalf("request/response synthesis must not emit token events: %+v", events)
	}
	var answer struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(events["answer"][0]), &answer); err != nil {
		t.Fatalf("decode answer event: %v", err)
	}
	if answer.Text != "Agent memory requires durable retrieval [src:test]." {
		t.Fatalf("unexpected answer text %q", answer.Text)
	}
	var done brainresearch.SynthesisResult
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if done.AnswerStatus != "ok" || done.Model != "ollama/qwen-test" || len(done.Citations) != 1 || done.Citations[0].SourceKey != "src:test" {
		t.Fatalf("unexpected done event: %+v", done)
	}
}

func TestResearchSynthesizeRejectsInvalidPack(t *testing.T) {
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewBufferString(`{"question":"Agent memory","research_pack":{"schema_version":"old"}}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "research_pack.schema_version") {
		t.Fatalf("expected schema diagnostic, got %q", rec.Body.String())
	}
}

func TestResearchSynthesizeReturnsUnavailableWithoutConfiguredModel(t *testing.T) {
	t.Setenv("DBRAIN_SUMMARY_MODEL", "")
	t.Setenv("SUMMARIZE_MODEL", "")
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan:     brainresearch.QueryPlan{TextQuery: "agent memory"},
		Coverage:      brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{SourceKey: "src:test", Kind: "source", Title: "Agent Memory", NotePath: "sources/test.md", Summary: "Agent memory evidence."},
		},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:     pack.Question,
		ResearchPack: pack,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("expected JSON unavailable response, got %q", got)
	}
	var payload struct {
		AnswerStatus string   `json:"answer_status"`
		Warnings     []string `json:"answer_warnings"`
		Error        string   `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode unavailable response: %v", err)
	}
	if payload.AnswerStatus != "unavailable" || len(payload.Warnings) != 1 || payload.Warnings[0] != "model_unavailable" {
		t.Fatalf("unexpected unavailable payload: %+v", payload)
	}
}

func TestResearchSynthesizeNoEvidenceSkipsModel(t *testing.T) {
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about nonexistent material?",
		QueryPlan:     brainresearch.QueryPlan{TextQuery: "nonexistent material"},
		Coverage:      brainresearch.Coverage{EvidenceCount: 0, RecallNote: "no evidence"},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:     pack.Question,
		ResearchPack: pack,
		Model:        "ollama/unused",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	if _, ok := events["answer"]; ok {
		t.Fatalf("no-evidence synthesis should not emit answer: %+v", events)
	}
	var done brainresearch.SynthesisResult
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if done.AnswerStatus != "no_evidence" || len(done.Warnings) == 0 {
		t.Fatalf("unexpected no-evidence done event: %+v", done)
	}
}

func TestResearchSynthesizeStreamsPostStartError(t *testing.T) {
	cfg, st := openTestStore(t)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan:     brainresearch.QueryPlan{TextQuery: "agent memory"},
		Coverage:      brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{SourceKey: "src:test", Kind: "source", Title: "Agent Memory", NotePath: "sources/test.md", Summary: "Agent memory evidence."},
		},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:     pack.Question,
		ResearchPack: pack,
		Model:        "ollama/qwen-test",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected stream to start with 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	if _, ok := events["start"]; !ok {
		t.Fatalf("expected start event: %+v", events)
	}
	if _, ok := events["error"]; !ok {
		t.Fatalf("expected terminal error event: %+v", events)
	}
	if _, ok := events["done"]; ok {
		t.Fatalf("error stream must not also emit done: %+v", events)
	}
	var payload struct {
		AnswerStatus string   `json:"answer_status"`
		Warnings     []string `json:"answer_warnings"`
		Error        string   `json:"error"`
	}
	if err := json.Unmarshal([]byte(events["error"][0]), &payload); err != nil {
		t.Fatalf("decode error event: %v", err)
	}
	if payload.AnswerStatus != "error" || !strings.Contains(payload.Error, "503") {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
}

func parseSSEEvents(t *testing.T, body string) map[string][]string {
	t.Helper()

	events := map[string][]string{}
	currentEvent := ""
	var data strings.Builder
	flush := func() {
		if currentEvent == "" {
			data.Reset()
			return
		}
		events[currentEvent] = append(events[currentEvent], data.String())
		currentEvent = ""
		data.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data: ")))
		}
	}
	flush()
	return events
}

func TestWebHandlerServesArchivedMediaAndSignedURL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openTestStore(t)
	itemID, _ := seedTestData(t, ctx, cfg, st)
	now := time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC)

	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "Video post",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-25T22:00:00Z",
			"snapshot":{
				"id":"123",
				"text":"Video post",
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/test.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":1280,"height":720}
				]
			},
			"raw":{}
		}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 media ref, got %+v", refs)
	}

	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     12,
		ContentHash:  "video-hash",
		LocalPath:    "media/x/video/ab/test.mp4",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if _, err := st.SaveMediaArchive(ctx, refs[0].MediaAssetID, model.MediaArchiveResult{
		Provider:   "cloudflare_r2",
		Bucket:     "dbrain",
		Key:        "media/x/video/ab/test.mp4",
		Status:     "archived",
		ArchivedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaArchive: %v", err)
	}
	if _, err := st.MarkMediaLocalPrunedByPath(ctx, "media/x/video/ab/test.mp4", now); err != nil {
		t.Fatalf("MarkMediaLocalPrunedByPath: %v", err)
	}

	staticFS, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	indexHTML, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("ReadFile index: %v", err)
	}

	s := &server{
		cfg:         cfg,
		store:       st,
		archive:     &fakeArchiveProxy{body: []byte("hello-video!"), signedURL: "https://signed.example.com/video.mp4"},
		proxyBase:   "http://127.0.0.1:8742",
		staticFS:    staticFS,
		static:      http.FileServerFS(staticFS),
		indexHTML:   indexHTML,
		toolVersion: "test",
	}
	handler := s.newMux()

	t.Run("media get", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("expected video/mp4 content type, got %q", got)
		}
		if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
			t.Fatalf("expected bytes accept ranges, got %q", got)
		}
		if got := rec.Body.String(); got != "hello-video!" {
			t.Fatalf("unexpected body %q", got)
		}
	})

	t.Run("media head", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodHead, "/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty head body, got %q", rec.Body.String())
		}
	})

	t.Run("media range", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		req.Header.Set("Range", "bytes=0-3")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Range"); got != "bytes 0-3/12" {
			t.Fatalf("unexpected content-range %q", got)
		}
		if got := rec.Body.String(); got != "hell" {
			t.Fatalf("unexpected ranged body %q", got)
		}
	})

	t.Run("signed url", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/media/signed-url?id="+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response signedURLResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode signed url response: %v", err)
		}
		if response.URL != "https://signed.example.com/video.mp4" {
			t.Fatalf("unexpected signed url response %+v", response)
		}
		if response.ProxyURL != "http://127.0.0.1:8742/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10) {
			t.Fatalf("unexpected proxy url response %+v", response)
		}
		for _, forbidden := range []string{`"bucket"`, `"key"`, `"source"`, "dbrain", "media/x/video/ab/test.mp4"} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("signed url response exposed storage metadata %q: %s", forbidden, rec.Body.String())
			}
		}
	})

	t.Run("detail media payload hides storage metadata", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup=item:test-agent-memory", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, forbidden := range []string{`"local_path"`, `"archive_bucket"`, `"archive_key"`, "media/x/video/ab/test.mp4"} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("detail media response exposed storage metadata %q: %s", forbidden, rec.Body.String())
			}
		}
		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get item: %v", err)
		}
		if response.Item == nil || len(response.Item.Media) != 1 {
			t.Fatalf("expected sanitized media ref, got %+v", response.Item)
		}
		if response.Item.Media[0].MediaAssetID != refs[0].MediaAssetID || response.Item.Media[0].MediaType != "video" {
			t.Fatalf("unexpected sanitized media ref %+v", response.Item.Media[0])
		}
	})
}

func openTestStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(cfg.VaultDir, "sources"),
		filepath.Join(cfg.VaultDir, "items"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	return cfg, st
}

func seedTestData(t *testing.T, ctx context.Context, cfg config.Config, st *store.Store) (int64, string) {
	t.Helper()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:       "item:test-agent-memory",
		SourceType:      "x_bookmark",
		ExternalID:      "x-123",
		CanonicalURL:    "https://x.com/darron/status/123",
		Title:           "Agent Memory Notes",
		AuthorHandle:    "darron",
		AuthorName:      "Darron",
		Language:        "en",
		Text:            "Short note about agent memory systems.",
		ArticleTitle:    "Agent Memory Systems",
		ArticleText:     "Agent memory systems need durable notes, retrieval, and source tracking.",
		PrimaryCategory: "agents",
		PrimaryDomain:   "x.com",
		LinksJSON:       "[]",
		ContentHash:     "item-hash",
		NotePath:        "items/test-agent-memory.md",
		RawJSON:         "{}",
		UpdatedAt:       now,
		LastSeenAt:      now,
	}

	upsert, err := st.UpsertItem(ctx, item)
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := upsert.ItemID
	if err := st.SaveItemUserTags(ctx, itemID, "agent-memory, retrieval"); err != nil {
		t.Fatalf("SaveItemUserTags: %v", err)
	}

	itemNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	if err := os.WriteFile(itemNotePath, []byte("# Agent Memory Notes\n\nLocal note content.\n"), 0o644); err != nil {
		t.Fatalf("write item note: %v", err)
	}

	link, err := st.UpsertSourceLink(ctx, itemID, model.SourceCandidate{
		OriginalURL:   "https://example.com/agent-memory",
		CanonicalURL:  "https://example.com/agent-memory",
		NormalizedURL: "https://example.com/agent-memory",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:test-agent-memory",
		NotePath:      "sources/test-agent-memory.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/agent-memory",
		FinalURL:     "https://example.com/agent-memory",
		Title:        "Agent Memory Article",
		Description:  "Why durable memory matters",
		SiteName:     "Example",
		Content:      "This article explains retrieval, memory, and note linking.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "source-hash"); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Text:          "Summary about agent memory retrieval.",
		RawJSON:       `{"summary":"Summary about agent memory retrieval."}`,
		Status:        "ok",
		FetchedAt:     now,
		PromptVersion: "test-prompt",
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}

	sourceNotePath := filepath.Join(cfg.VaultDir, "sources", "test-agent-memory.md")
	if err := os.WriteFile(sourceNotePath, []byte("# Agent Memory Article\n\nRendered source note.\n"), 0o644); err != nil {
		t.Fatalf("write source note: %v", err)
	}

	failedLink, err := st.UpsertSourceLink(ctx, itemID, model.SourceCandidate{
		OriginalURL:   "https://broken.example.com/agent-memory",
		CanonicalURL:  "https://broken.example.com/agent-memory",
		NormalizedURL: "https://broken.example.com/agent-memory",
		SourceType:    "web",
		Domain:        "broken.example.com",
		SourceKey:     "src:test-agent-memory-failure",
		NotePath:      "sources/test-agent-memory-failure.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink failed source: %v", err)
	}
	failureAt := now.Add(2 * time.Minute)
	if _, err := st.SaveSourceExtraction(ctx, failedLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://broken.example.com/agent-memory",
		FinalURL:     "https://broken.example.com/agent-memory",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    failureAt,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, ""); err != nil {
		t.Fatalf("save failed source extraction: %v", err)
	}

	failedLinkTwo, err := st.UpsertSourceLink(ctx, itemID, model.SourceCandidate{
		OriginalURL:   "https://broken.example.com/agent-memory-two",
		CanonicalURL:  "https://broken.example.com/agent-memory-two",
		NormalizedURL: "https://broken.example.com/agent-memory-two",
		SourceType:    "web",
		Domain:        "broken.example.com",
		SourceKey:     "src:test-agent-memory-failure-two",
		NotePath:      "sources/test-agent-memory-failure-two.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink second failed source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, failedLinkTwo.SourceID, model.ExtractResult{
		CanonicalURL: "https://broken.example.com/agent-memory-two",
		FinalURL:     "https://broken.example.com/agent-memory-two",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    failureAt.Add(-45 * time.Minute),
		Tool:         "summarize",
		ToolVersion:  "test",
	}, ""); err != nil {
		t.Fatalf("save second failed source extraction: %v", err)
	}

	return itemID, "src:test-agent-memory"
}
