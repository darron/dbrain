package sourceenrich

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

func TestSkipSummaryReasonSkipsTranscriptUnavailableYouTubeMetadataOnly(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "youtube"}
	extract := model.ExtractResult{
		Content: "Why I will NEVER surrender my guns.\nChannel business contact - ladner_chevy@hotmail.com",
		RawJSON: `{"extracted":{"transcriptSource":"unavailable","transcriptionProvider":null,"transcriptCharacters":null}}`,
	}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected summary to be skipped")
	}
	if reason == "" {
		t.Fatal("expected skip reason")
	}
}

func TestSkipSummaryReasonAllowsTranscriptBackedYouTubeExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "youtube"}
	extract := model.ExtractResult{
		Content: "Transcript:\nreal transcript content",
		RawJSON: `{"extracted":{"transcriptSource":"captionTracks","transcriptionProvider":null,"transcriptCharacters":2048}}`,
	}

	if reason, ok := skipSummaryReason(source, extract); ok {
		t.Fatalf("expected transcript-backed extract to summarize, got reason %q", reason)
	}
}

func TestSelectSourceDocumentsHonorsLimit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ordered := []int64{1, 2, 3}
	byID := map[int64]model.SourceDocument{
		1: {
			ID:                   1,
			SourceKey:            "src:one",
			ExtractStatus:        "ok",
			SummaryStatus:        "error",
			ContentHash:          "hash-1",
			SummaryContentHash:   "hash-0",
			SummaryPromptVersion: "old",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
		2: {
			ID:                   2,
			SourceKey:            "src:two",
			ExtractStatus:        "ok",
			SummaryStatus:        "error",
			ContentHash:          "hash-2",
			SummaryContentHash:   "hash-1",
			SummaryPromptVersion: "old",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
		3: {
			ID:                   3,
			SourceKey:            "src:three",
			ExtractStatus:        "ok",
			SummaryStatus:        "error",
			ContentHash:          "hash-3",
			SummaryContentHash:   "hash-2",
			SummaryPromptVersion: "old",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
	}

	selected := selectSourceDocuments(ordered, byID, Options{
		Limit:     2,
		Summarize: true,
	}, "0.13.0")

	if len(selected) != 2 {
		t.Fatalf("expected 2 selected sources, got %d", len(selected))
	}
	if selected[0].SourceKey != "src:one" || selected[1].SourceKey != "src:two" {
		t.Fatalf("unexpected selected sources: %s, %s", selected[0].SourceKey, selected[1].SourceKey)
	}
}

func TestRunSourceIDsUsesStoredExtractForStaleSummary(t *testing.T) {
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

	installSourceEnrichFakeSummarize(t, root)

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "github_star:test/repo",
		SourceType:   "github_star",
		ExternalID:   "test/repo",
		CanonicalURL: "https://github.com/test/repo",
		Title:        "test/repo",
		ContentHash:  "item-hash",
		NotePath:     vault.NoteRelativePath("github", "2026", "test__repo"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:github-test-repo",
		OriginalURL:   "https://github.com/test/repo",
		CanonicalURL:  "https://github.com/test/repo",
		NormalizedURL: "https://github.com/test/repo",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      vault.SourceNoteRelativePath("github", "test-repo"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/repo",
		FinalURL:     "https://github.com/test/repo",
		Title:        "test/repo",
		Description:  "A useful repo",
		SiteName:     "GitHub",
		Content:      "README CONTENT FROM GITHUB API",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "github-source-hash"); err != nil {
		t.Fatalf("save extraction: %v", err)
	}

	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "old summary",
		RawJSON:       `{"summary":"old summary"}`,
		Model:         "test/model",
		PromptVersion: "old-version",
		Status:        "ok",
		FetchedAt:     now.Add(-time.Hour),
		Tool:          "summarize",
		ToolVersion:   "0.10.0",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Length:    "short",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d", stats.SourcesSummarized)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "summary from stored extract" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func TestRunSourceIDsUsesPreferredCLIProviderForGenericSummary(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, ".summarize"), 0o755); err != nil {
		t.Fatalf("create summarize home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".summarize", "cli-state.json"), []byte(`{"lastSuccessfulProvider":"claude"}`), 0o644); err != nil {
		t.Fatalf("write cli-state: %v", err)
	}
	t.Setenv("HOME", home)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichGenericFakeSummarize(t, root)

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-generic",
		SourceType:   "x_bookmark",
		ExternalID:   "test-generic",
		CanonicalURL: "https://x.com/example/status/test-generic",
		Title:        "test generic",
		ContentHash:  "item-hash-generic",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-generic"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:generic-test",
		OriginalURL:   "https://example.com/post",
		CanonicalURL:  "https://example.com/post",
		NormalizedURL: "https://example.com/post",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "generic-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Length:    "short",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d", stats.SourcesSummarized)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.SummaryText != "summary from generic path" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func installSourceEnrichFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
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
  *"README CONTENT FROM GITHUB API"*) ;;
  *)
    echo "expected stored extract in summary file" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"README CONTENT FROM GITHUB API"},"summary":"summary from stored extract"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichGenericFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
prev=""
cli=""
for arg in "$@"; do
  if [ "$prev" = "--cli" ]; then
    cli="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ "$cli" != "claude" ]; then
  echo "expected preferred cli provider, got $cli" >&2
  exit 1
fi
if [ "$last" != "https://example.com/post" ]; then
  echo "unexpected summarize input: $last" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"https://example.com/post","title":"Example","description":"desc","siteName":"Example","content":"body"},"summary":"summary from generic path"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
