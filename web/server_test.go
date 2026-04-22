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

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body == "" || !bytes.Contains(rec.Body.Bytes(), []byte("dbrain")) {
		t.Fatalf("expected index html to mention dbrain, got %q", body)
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

	return itemID, "src:test-agent-memory"
}
