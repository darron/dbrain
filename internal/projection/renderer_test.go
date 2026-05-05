package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func TestRendererRefreshItemWritesRenderedNote(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openProjectionTestStore(t)
	now := time.Date(2026, time.May, 5, 12, 0, 0, 0, time.UTC)
	item := model.Item{
		SourceKey:    "test:item-refresh",
		SourceType:   "test",
		CanonicalURL: "https://example.com/item",
		Title:        "Projected Item",
		Text:         "raw item text",
		LinksJSON:    "[]",
		ContentHash:  "hash-item",
		NotePath:     "items/test/item-refresh.md",
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	if _, err := st.UpsertItem(ctx, item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	rendered, err := NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey)
	if err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}
	if rendered.SourceKey != item.SourceKey {
		t.Fatalf("unexpected rendered item: %+v", rendered)
	}

	body := readProjectionFile(t, cfg, item.NotePath)
	if !strings.Contains(body, "# Projected Item") || !strings.Contains(body, "raw item text") {
		t.Fatalf("rendered item note missing expected content:\n%s", body)
	}
}

func TestRendererRefreshSourceLoadsBacklinks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openProjectionTestStore(t)
	now := time.Date(2026, time.May, 5, 12, 0, 0, 0, time.UTC)
	item := model.Item{
		SourceKey:    "test:item-backlink",
		SourceType:   "test",
		CanonicalURL: "https://example.com/backlink-item",
		Title:        "Backlink Item",
		LinksJSON:    `["https://example.com/source"]`,
		ContentHash:  "hash-backlink",
		NotePath:     "items/test/item-backlink.md",
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	itemResult, err := st.UpsertItem(ctx, item)
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	candidate := model.SourceCandidate{
		OriginalURL:   "https://example.com/source",
		CanonicalURL:  "https://example.com/source",
		NormalizedURL: "https://example.com/source",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:projection-test",
		NotePath:      vault.SourceNoteRelativePath("web", "projection-test"),
	}
	link, err := st.UpsertSourceLink(ctx, itemResult.ItemID, candidate)
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	source, err := NewRenderer(cfg, st).RefreshSourceByID(ctx, link.SourceID)
	if err != nil {
		t.Fatalf("RefreshSourceByID: %v", err)
	}
	if source.SourceKey != candidate.SourceKey {
		t.Fatalf("unexpected rendered source: %+v", source)
	}

	body := readProjectionFile(t, cfg, candidate.NotePath)
	if !strings.Contains(body, "Backlink Item") || !strings.Contains(body, "[[items/test/item-backlink|Backlink Item]]") {
		t.Fatalf("rendered source note missing backlink content:\n%s", body)
	}
}

func openProjectionTestStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		DBPath:   filepath.Join(root, "brain.db"),
		VaultDir: filepath.Join(root, "vault"),
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return cfg, st
}

func readProjectionFile(t *testing.T, cfg config.Config, notePath string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(notePath)))
	if err != nil {
		t.Fatalf("read rendered note %s: %v", notePath, err)
	}
	return string(body)
}
