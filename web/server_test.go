package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestWebHandlerServesBootstrapSearchGetAndAsk(t *testing.T) {
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
	})

	t.Run("ask retrieve only", func(t *testing.T) {
		body := bytes.NewBufferString(`{"question":"What do I have on agent memory?","limit":4}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/ask", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode ask response: %v", err)
		}
		if response["question"] != "What do I have on agent memory?" {
			t.Fatalf("expected echoed question, got %#v", response["question"])
		}
		if response["answer"] != "" {
			t.Fatalf("expected retrieve-only empty answer, got %#v", response["answer"])
		}
		if _, ok := response["evidence"].([]any); !ok {
			t.Fatalf("expected evidence array, got %#v", response["evidence"])
		}
	})
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
