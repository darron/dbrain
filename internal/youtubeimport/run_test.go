package youtubeimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/store"
)

func TestRunImportsYouTubeSignalsAndSummarizesCanonicalSource(t *testing.T) {
	t.Parallel()

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

	ytDLPPath := installFakeYTDLP(t, root)
	summarizePath := installFakeSummarize(t, root, "chrome:Profile 1")

	stats, err := Run(context.Background(), cfg, st, Options{
		Browser:         "chrome",
		Profile:         "Profile 1",
		Limit:           5,
		WatchLater:      true,
		Summarize:       true,
		Length:          "short",
		Timeout:         5 * time.Second,
		YTDLPBinary:     ytDLPPath,
		SummarizeBinary: summarizePath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.FeedsProcessed != 1 {
		t.Fatalf("expected 1 feed processed, got %d", stats.FeedsProcessed)
	}
	if stats.ItemsProcessed != 1 || stats.ItemsCreated != 1 {
		t.Fatalf("unexpected item stats: %+v", stats)
	}
	if stats.SourcesCreated != 1 || stats.LinksCreated != 1 {
		t.Fatalf("unexpected source link stats: %+v", stats)
	}
	if stats.SourcesExtracted != 1 || stats.SourcesSummarized != 1 || stats.SourcesRendered != 1 {
		t.Fatalf("unexpected enrichment stats: %+v", stats)
	}

	item, err := st.GetItem(context.Background(), "yt:watch_later:abc123XYZ99")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.SourceType != "youtube_watch_later" {
		t.Fatalf("unexpected item source type: %s", item.SourceType)
	}
	if item.CanonicalURL != "https://www.youtube.com/watch?v=abc123XYZ99" {
		t.Fatalf("unexpected canonical url: %s", item.CanonicalURL)
	}
	if item.SavedAt != "" || item.SyncedAt != "" {
		t.Fatalf("expected stable empty saved/synced timestamps, got saved=%q synced=%q", item.SavedAt, item.SyncedAt)
	}

	itemNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	itemNoteBytes, err := os.ReadFile(itemNotePath)
	if err != nil {
		t.Fatalf("read item note: %v", err)
	}
	itemNote := string(itemNoteBytes)
	for _, want := range []string{"source/youtube", "Signal: `watch_later`", "## Imported Description"} {
		if !strings.Contains(itemNote, want) {
			t.Fatalf("expected item note to contain %q, got %q", want, itemNote)
		}
	}

	source, err := st.GetSource(context.Background(), "https://www.youtube.com/watch?v=abc123XYZ99")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if source.SourceType != "youtube" {
		t.Fatalf("unexpected source type: %s", source.SourceType)
	}
	if source.ExtractedText != "full transcript from fake summarize" {
		t.Fatalf("unexpected extracted text: %q", source.ExtractedText)
	}
	if source.SummaryText != "youtube summary from fake summarize" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
	if source.SummaryToolVersion != "test-youtube-1.0.0" {
		t.Fatalf("unexpected summary tool version: %q", source.SummaryToolVersion)
	}

	sourceNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
	if _, err := os.Stat(sourceNotePath); err != nil {
		t.Fatalf("expected source note to exist: %v", err)
	}

	secondStats, err := Run(context.Background(), cfg, st, Options{
		Browser:         "chrome",
		Profile:         "Profile 1",
		Limit:           5,
		WatchLater:      true,
		Summarize:       true,
		Length:          "short",
		Timeout:         5 * time.Second,
		YTDLPBinary:     ytDLPPath,
		SummarizeBinary: summarizePath,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if secondStats.ItemsProcessed != 1 || secondStats.ItemsUnchanged != 1 {
		t.Fatalf("expected unchanged item on second run, got %+v", secondStats)
	}
	if secondStats.ItemsUpdated != 0 || secondStats.SourcesQueued != 0 || secondStats.SourcesExtracted != 0 || secondStats.SourcesSummarized != 0 {
		t.Fatalf("expected no second-pass source work, got %+v", secondStats)
	}
}

func installFakeYTDLP(t *testing.T, root string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-yt-dlp")
	script := `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
if [ "$last" != "https://www.youtube.com/playlist?list=WL" ]; then
  echo "unexpected yt-dlp url: $last" >&2
  exit 1
fi
printf '%s\n' '{"id":"WL","title":"Watch Later","entries":[{"id":"abc123XYZ99","title":"A useful video","url":"abc123XYZ99","webpage_url":"https://youtu.be/abc123XYZ99?t=10","description":"A durable description from yt-dlp","uploader":"Channel Name","uploader_id":"channel-handle","channel":"Channel Name","channel_id":"UC123","upload_date":"20260418"}]}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return scriptPath
}

func installFakeSummarize(t *testing.T, root string, wantCookies string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-summarize-youtube")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-youtube-1.0.0"
  exit 0
fi
last=""
youtube_mode=""
video_mode=""
transcriber=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--youtube" ]; then
    youtube_mode="$arg"
  fi
  if [ "$prev" = "--video-mode" ]; then
    video_mode="$arg"
  fi
  if [ "$prev" = "--transcriber" ]; then
    transcriber="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ "$last" = "-" ]; then
  input="$(cat)"
  case "$input" in
    *"full transcript from fake summarize"*) ;;
    *)
      echo "expected transcript on stdin during summary step" >&2
      exit 1
      ;;
  esac
  printf '%s\n' '{"input":{"model":"cli/test/youtube"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"full transcript from fake summarize"},"summary":"youtube summary from fake summarize"}'
  exit 0
fi
if [ "$SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER" != "` + wantCookies + `" ]; then
  echo "unexpected summarize cookies env: $SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER" >&2
  exit 1
fi
if [ "$last" != "https://www.youtube.com/watch?v=abc123XYZ99" ]; then
  echo "unexpected summarize input: $last" >&2
  exit 1
fi
if [ "$youtube_mode" != "auto" ]; then
  echo "unexpected summarize youtube mode: $youtube_mode" >&2
  exit 1
fi
if [ "$video_mode" != "transcript" ]; then
  echo "unexpected summarize video mode: $video_mode" >&2
  exit 1
fi
if [ "$transcriber" != "auto" ]; then
  echo "unexpected summarize transcriber: $transcriber" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"cli/test/youtube"},"extracted":{"url":"https://www.youtube.com/watch?v=abc123XYZ99","title":"A useful video","description":"Video description","siteName":"YouTube","content":"full transcript from fake summarize"},"summary":null}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}
	return scriptPath
}
