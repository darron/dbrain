package noterepair

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

func TestRunTracksAlreadyCurrentNotesSeparately(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	currentSource := createRepairSource(t, st, "repair-current", "https://example.com/current")
	staleSource := createRepairSource(t, st, "repair-stale", "https://example.com/stale")

	currentBacklinks, err := st.ListBacklinksForSource(context.Background(), currentSource.ID)
	if err != nil {
		t.Fatalf("list current backlinks: %v", err)
	}
	if _, err := writeSourceNote(cfg, currentSource, currentBacklinks); err != nil {
		t.Fatalf("write current source note: %v", err)
	}

	stalePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(staleSource.NotePath))
	if err := os.MkdirAll(filepath.Dir(stalePath), 0o755); err != nil {
		t.Fatalf("mkdir stale note dir: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("stale note body"), 0o644); err != nil {
		t.Fatalf("write stale note: %v", err)
	}

	stats, err := Run(context.Background(), cfg, st, Options{
		Sources:     true,
		MissingOnly: false,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.SourcesConsidered != 2 {
		t.Fatalf("expected 2 sources considered, got %+v", stats)
	}
	if stats.SourcesWritten != 1 {
		t.Fatalf("expected 1 source rewritten, got %+v", stats)
	}
	if stats.SourcesAlreadyCurrent != 1 {
		t.Fatalf("expected 1 source already current, got %+v", stats)
	}
	if stats.SourcesSkippedMissingOnly != 0 {
		t.Fatalf("expected 0 source missing-only skips, got %+v", stats)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected 0 errors, got %+v", stats)
	}
}

func createRepairSource(t *testing.T, st *store.Store, slug string, sourceURL string) model.SourceDocument {
	t.Helper()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:" + slug,
		SourceType:   "x_bookmark",
		ExternalID:   slug,
		CanonicalURL: "https://x.com/example/status/" + slug,
		Title:        slug,
		ContentHash:  "item-hash-" + slug,
		NotePath:     vault.NoteRelativePath("x", "2026", slug),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item %s: %v", slug, err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:" + slug,
		OriginalURL:   sourceURL,
		CanonicalURL:  sourceURL,
		NormalizedURL: sourceURL,
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", slug),
	})
	if err != nil {
		t.Fatalf("upsert source link %s: %v", slug, err)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source %s: %v", slug, err)
	}
	return source
}
