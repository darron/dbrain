package linkextract

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
		ArticleText:         "LOCAL ARTICLE TEXT FROM FT",
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

	if source.ExtractedText != "LOCAL ARTICLE TEXT FROM FT" {
		t.Fatalf("expected local cached article text, got %q", source.ExtractedText)
	}
	if source.ExtractTool != "ft-bookmarks" || source.ExtractToolVersion != "local-item-cache" {
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
if [ "$last" != "-" ]; then
  echo "expected stdin input mode" >&2
  exit 1
fi
input="$(cat)"
case "$input" in
  *"LOCAL ARTICLE TEXT FROM FT"*) ;;
  *)
    echo "expected local cached article text on stdin" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"LOCAL ARTICLE TEXT FROM FT"},"summary":"summary from fake summarize"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return nil
}
