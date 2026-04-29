package linkextract

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func TestRunPrefersLocalArticleTextOverLiveFetch(t *testing.T) {
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

	if err := installFakeSummarize(t, root); err != nil {
		t.Fatalf("install fake summarize: %v", err)
	}

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:           "x:12345",
		SourceType:          "x_bookmark",
		ExternalID:          "12345",
		CanonicalURL:        "https://x.com/example/status/12345",
		Title:               "Bookmark title",
		ArticleTitle:        "Local cached article",
		ArticleText:         "LOCAL ARTICLE TEXT FROM ITEM CACHE",
		LinksJSON:           `["https://example.com/post"]`,
		ContentHash:         "item-hash-12345",
		NotePath:            vault.NoteRelativePath("x", "2026", "12345"),
		RawJSON:             `{}`,
		ImportedAt:          now,
		UpdatedAt:           now,
		LastSeenAt:          now,
		LinkExtractSyncedAt: time.Time{},
	}
	if _, err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	stats, err := Run(context.Background(), cfg, st, Options{
		DiscoverLimit: 10,
		Limit:         10,
		Summarize:     true,
		Model:         "cli/test/linkextract",
		Length:        "short",
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.SourcesCreated != 1 {
		t.Fatalf("expected 1 source created, got %d", stats.SourcesCreated)
	}
	if stats.SourcesExtracted != 1 {
		t.Fatalf("expected 1 source extracted, got %d", stats.SourcesExtracted)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 source summarized, got %d", stats.SourcesSummarized)
	}

	source, err := st.GetSource(context.Background(), "https://example.com/post")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}

	if source.ExtractedText != "LOCAL ARTICLE TEXT FROM ITEM CACHE" {
		t.Fatalf("expected local cached article text, got %q", source.ExtractedText)
	}
	if source.ExtractTool != "item-cache" || source.ExtractToolVersion != "local-item-cache" {
		t.Fatalf("unexpected extract tool metadata: %s %s", source.ExtractTool, source.ExtractToolVersion)
	}
	if source.SummaryText != "summary from fake summarize" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
	if source.SummaryToolVersion != "test-1.0.0" {
		t.Fatalf("unexpected summary tool version: %q", source.SummaryToolVersion)
	}

	notePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("source note not written: %v", err)
	}
}

func TestRunOnlyEnrichesDiscoveredBookmarkSources(t *testing.T) {
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

	if err := installFakeSummarize(t, root); err != nil {
		t.Fatalf("install fake summarize: %v", err)
	}

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:           "x:67890",
		SourceType:          "x_bookmark",
		ExternalID:          "67890",
		CanonicalURL:        "https://x.com/example/status/67890",
		Title:               "Bookmark title",
		ArticleTitle:        "Local cached article",
		ArticleText:         "LOCAL ARTICLE TEXT FROM ITEM CACHE",
		LinksJSON:           `["https://example.com/post"]`,
		ContentHash:         "item-hash-67890",
		NotePath:            vault.NoteRelativePath("x", "2026", "67890"),
		RawJSON:             `{}`,
		ImportedAt:          now,
		UpdatedAt:           now,
		LastSeenAt:          now,
		LinkExtractSyncedAt: time.Time{},
	}
	if _, err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	otherItem := model.Item{
		SourceKey:    "youtube:test-signal",
		SourceType:   "youtube_watch_later",
		ExternalID:   "youtube:test-signal",
		CanonicalURL: "https://www.youtube.com/watch?v=test-signal",
		Title:        "Other signal",
		ContentHash:  "other-signal-hash",
		NotePath:     vault.NoteRelativePath("youtube", "2026", "test-signal"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	otherUpserted, err := st.UpsertItem(context.Background(), otherItem)
	if err != nil {
		t.Fatalf("upsert other item: %v", err)
	}

	if _, err := st.UpsertSourceLink(context.Background(), otherUpserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:unrelated",
		OriginalURL:   "https://unrelated.example.com/post",
		NormalizedURL: "https://unrelated.example.com/post",
		CanonicalURL:  "https://unrelated.example.com/post",
		SourceType:    "web",
		Domain:        "unrelated.example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "unrelated"),
	}); err != nil {
		t.Fatalf("insert unrelated source: %v", err)
	}

	stats, err := Run(context.Background(), cfg, st, Options{
		DiscoverLimit: 10,
		Limit:         10,
		Summarize:     true,
		Model:         "cli/test/linkextract",
		Length:        "short",
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.SourcesQueued != 1 {
		t.Fatalf("expected 1 discovered source queued, got %d", stats.SourcesQueued)
	}

	discoveredSource, err := st.GetSource(context.Background(), "https://example.com/post")
	if err != nil {
		t.Fatalf("GetSource discovered: %v", err)
	}
	if discoveredSource.SummaryStatus != "ok" {
		t.Fatalf("expected discovered source to be summarized, got %q", discoveredSource.SummaryStatus)
	}

	unrelatedSource, err := st.GetSource(context.Background(), "https://unrelated.example.com/post")
	if err != nil {
		t.Fatalf("GetSource unrelated: %v", err)
	}
	if unrelatedSource.SummaryStatus != "" {
		t.Fatalf("expected unrelated pending source to remain untouched, got %q", unrelatedSource.SummaryStatus)
	}
}

func installFakeSummarize(t *testing.T, root string) error {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}

	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
for arg in "$@"; do
  last="$arg"
done
if [ ! -f "$last" ]; then
  echo "expected local summary file input" >&2
  exit 1
fi
input="$(cat "$last")"
case "$input" in
  *"LOCAL ARTICLE TEXT FROM ITEM CACHE"*) ;;
  *)
    echo "expected local cached article text in summary file" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"LOCAL ARTICLE TEXT FROM ITEM CACHE"},"summary":"summary from fake summarize"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return nil
}
