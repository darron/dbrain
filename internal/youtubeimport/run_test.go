package youtubeimport

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
		Model:           "cli/test/youtube",
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
		Model:           "cli/test/youtube",
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

func TestFallbackExtractForUsesWhisperCLIWhenTranscriptUnavailable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	modelPath := filepath.Join(root, "ggml-base.bin")
	if err := os.WriteFile(modelPath, []byte("model"), 0o644); err != nil {
		t.Fatalf("write fake model: %v", err)
	}

	ytDLPPath := installFallbackFakeYTDLP(t, root)
	whisperPath := installFallbackFakeWhisper(t, root)

	source := model.SourceDocument{
		SourceType:   "youtube",
		CanonicalURL: "https://www.youtube.com/watch?v=test123",
		Title:        "Fallback title",
		Description:  "Fallback description",
	}
	extract := model.ExtractResult{
		Title:       "- YouTube",
		Description: "",
		SiteName:    "youtube.com",
		Content:     "Enjoy the videos and music you love...",
		RawJSON:     `{"extracted":{"transcriptSource":"unavailable","transcriptionProvider":null,"transcriptCharacters":null}}`,
	}

	fallback, changed, err := fallbackExtractFor(cfg, Options{
		Browser:          "chrome",
		Profile:          "Default",
		YTDLPBinary:      ytDLPPath,
		WhisperBinary:    whisperPath,
		WhisperModelPath: modelPath,
		Timeout:          5 * time.Second,
	}, "chrome:Default")(context.Background(), source, extract)
	if err != nil {
		t.Fatalf("fallbackExtractFor: %v", err)
	}
	if !changed {
		t.Fatal("expected whisper fallback to run")
	}
	if !strings.Contains(fallback.Content, "transcript from fake whisper") {
		t.Fatalf("unexpected fallback transcript: %q", fallback.Content)
	}
	if fallback.Tool != "whisper.cpp" {
		t.Fatalf("unexpected fallback tool: %q", fallback.Tool)
	}
	if !strings.Contains(fallback.RawJSON, `"transcriptionProvider":"whisper.cpp"`) {
		t.Fatalf("unexpected fallback raw json: %q", fallback.RawJSON)
	}
}

func TestFallbackExtractForUsesMacWhisperWhenExplicitlyRequested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	ytDLPPath := installFallbackFakeYTDLP(t, root)
	macWhisperPath := installFallbackFakeMacWhisper(t, root, "mlx:large-v3-turbo", "transcript from fake macwhisper")

	source := model.SourceDocument{
		SourceType:   "youtube",
		CanonicalURL: "https://www.youtube.com/watch?v=test123",
		Title:        "Fallback title",
		Description:  "Fallback description",
	}
	extract := model.ExtractResult{
		Title:       "- YouTube",
		Description: "",
		SiteName:    "youtube.com",
		Content:     "Enjoy the videos and music you love...",
		RawJSON:     `{"extracted":{"transcriptSource":"unavailable","transcriptionProvider":null,"transcriptCharacters":null}}`,
	}

	fallback, changed, err := fallbackExtractFor(cfg, Options{
		Browser:          "chrome",
		Profile:          "Default",
		Transcriber:      "macwhisper:mlx:large-v3-turbo",
		YTDLPBinary:      ytDLPPath,
		MacWhisperBinary: macWhisperPath,
		Timeout:          5 * time.Second,
	}, "chrome:Default")(context.Background(), source, extract)
	if err != nil {
		t.Fatalf("fallbackExtractFor: %v", err)
	}
	if !changed {
		t.Fatal("expected macwhisper fallback to run")
	}
	if !strings.Contains(fallback.Content, "transcript from fake macwhisper") {
		t.Fatalf("unexpected fallback transcript: %q", fallback.Content)
	}
	if fallback.Tool != "macwhisper" {
		t.Fatalf("unexpected fallback tool: %q", fallback.Tool)
	}
	if !strings.Contains(fallback.RawJSON, `"transcriptionProvider":"macwhisper"`) {
		t.Fatalf("unexpected fallback raw json: %q", fallback.RawJSON)
	}
}

func TestRunPrunesYouTubeHistorySignalsAndOrphanSources(t *testing.T) {
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

	now := time.Now().UTC()
	historyItem := model.Item{
		SourceKey:    "yt:history:deadbeef123",
		SourceType:   "youtube_history",
		ExternalID:   "deadbeef123",
		CanonicalURL: "https://www.youtube.com/watch?v=deadbeef123",
		Title:        "History signal",
		ContentHash:  "history-signal-hash",
		NotePath:     filepath.ToSlash(filepath.Join("items", "youtube", "history", "2026", "deadbeef123.md")),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), historyItem)
	if err != nil {
		t.Fatalf("upsert history item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:history-video",
		OriginalURL:   historyItem.CanonicalURL,
		CanonicalURL:  historyItem.CanonicalURL,
		NormalizedURL: historyItem.CanonicalURL,
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      filepath.ToSlash(filepath.Join("sources", "youtube", "history-video.md")),
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: historyItem.CanonicalURL,
		FinalURL:     historyItem.CanonicalURL,
		Title:        "History video",
		Content:      "history only content",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "youtube-test",
		ToolVersion:  "test",
	}, "history-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	itemNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(historyItem.NotePath))
	if err := os.MkdirAll(filepath.Dir(itemNotePath), 0o755); err != nil {
		t.Fatalf("mkdir history item note dir: %v", err)
	}
	if err := os.WriteFile(itemNotePath, []byte("history note"), 0o644); err != nil {
		t.Fatalf("write history item note: %v", err)
	}

	source, err := st.GetSource(context.Background(), historyItem.CanonicalURL)
	if err != nil {
		t.Fatalf("get history source: %v", err)
	}
	sourceNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
	if err := os.MkdirAll(filepath.Dir(sourceNotePath), 0o755); err != nil {
		t.Fatalf("mkdir history source note dir: %v", err)
	}
	if err := os.WriteFile(sourceNotePath, []byte("history source note"), 0o644); err != nil {
		t.Fatalf("write history source note: %v", err)
	}

	ytDLPPath := installDualFeedFakeYTDLP(t, root)

	stats, err := Run(context.Background(), cfg, st, Options{
		Browser:     "chrome",
		Profile:     "Default",
		WatchLater:  true,
		Liked:       true,
		Summarize:   false,
		Limit:       5,
		Timeout:     5 * time.Second,
		YTDLPBinary: ytDLPPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.ItemsDeleted != 1 {
		t.Fatalf("expected one history item deleted, got %+v", stats)
	}
	if stats.SourcesDeleted != 1 {
		t.Fatalf("expected one orphan history source deleted, got %+v", stats)
	}
	if _, err := st.GetItem(context.Background(), historyItem.SourceKey); err == nil {
		t.Fatal("expected history item to be deleted")
	}
	if _, err := st.GetSource(context.Background(), historyItem.CanonicalURL); err == nil {
		t.Fatal("expected orphan history source to be deleted")
	}
	if _, err := os.Stat(itemNotePath); !os.IsNotExist(err) {
		t.Fatalf("expected history item note to be removed, got err=%v", err)
	}
	if _, err := os.Stat(sourceNotePath); !os.IsNotExist(err) {
		t.Fatalf("expected history source note to be removed, got err=%v", err)
	}
}

func TestRunDefaultsToWatchLaterAndLikedOnly(t *testing.T) {
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

	ytDLPPath := installDualFeedFakeYTDLP(t, root)

	stats, err := Run(context.Background(), cfg, st, Options{
		Browser:     "chrome",
		Profile:     "Default",
		Summarize:   false,
		Limit:       5,
		Timeout:     5 * time.Second,
		YTDLPBinary: ytDLPPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.FeedsProcessed != 2 {
		t.Fatalf("expected default run to process watch later and liked only, got %+v", stats)
	}
	if stats.ItemsProcessed != 2 {
		t.Fatalf("expected two imported youtube signals, got %+v", stats)
	}
	if _, err := st.GetItem(context.Background(), "yt:watch_later:dualWatch12345"); err != nil {
		t.Fatalf("expected watch later item: %v", err)
	}
	if _, err := st.GetItem(context.Background(), "yt:liked:dualLiked12345"); err != nil {
		t.Fatalf("expected liked item: %v", err)
	}
}

func TestRunContinuesWhenOneFeedFails(t *testing.T) {
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

	ytDLPPath := installPartialFailureFakeYTDLP(t, root)
	summarizePath := installSourceExtractFakeSummarize(t, root)

	stats, err := Run(context.Background(), cfg, st, Options{
		Browser:         "chrome",
		Profile:         "Default",
		Summarize:       false,
		Limit:           5,
		Timeout:         5 * time.Second,
		WatchLater:      true,
		Liked:           true,
		YTDLPBinary:     ytDLPPath,
		SummarizeBinary: summarizePath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Errors != 1 {
		t.Fatalf("expected one feed error, got %+v", stats)
	}
	if stats.FeedsProcessed != 1 {
		t.Fatalf("expected one successful feed, got %+v", stats)
	}
	if stats.ItemsProcessed != 1 {
		t.Fatalf("expected liked feed item to import, got %+v", stats)
	}
	if _, err := st.GetItem(context.Background(), "yt:liked:partialLiked123"); err != nil {
		t.Fatalf("expected liked item to import after watch later failure: %v", err)
	}
	if _, err := st.GetItem(context.Background(), "yt:watch_later:partialWatch123"); err == nil {
		t.Fatal("did not expect failed watch later feed item to exist")
	}
}

func TestRunRetriesDiscoveredProfilesWhenProfileNotSpecified(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)

	chromeDir := filepath.Join(browserProfileBaseDir("chrome"), "Profile 2")
	if err := os.MkdirAll(chromeDir, 0o755); err != nil {
		t.Fatalf("mkdir chrome profile: %v", err)
	}

	cfg, err := config.Load(t.TempDir())
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

	ytDLPPath := installDiscoveredProfileFakeYTDLP(t, root, "chrome:Profile 2")
	summarizePath := installSourceExtractFakeSummarize(t, root)

	stats, err := Run(context.Background(), cfg, st, Options{
		Browser:         "chrome",
		Summarize:       false,
		Limit:           5,
		Timeout:         5 * time.Second,
		WatchLater:      true,
		YTDLPBinary:     ytDLPPath,
		SummarizeBinary: summarizePath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Errors != 0 {
		t.Fatalf("expected profile discovery to avoid feed errors, got %+v", stats)
	}
	if stats.FeedsProcessed != 1 || stats.ItemsProcessed != 1 {
		t.Fatalf("expected watch later feed to import through discovered profile, got %+v", stats)
	}
	if _, err := st.GetItem(context.Background(), "yt:watch_later:profileRetry123"); err != nil {
		t.Fatalf("expected imported item through discovered profile: %v", err)
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

func installDualFeedFakeYTDLP(t *testing.T, root string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-yt-dlp-dual")
	script := `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  "https://www.youtube.com/playlist?list=WL")
    printf '%s\n' '{"id":"WL","title":"Watch Later","entries":[{"id":"dualWatch12345","title":"Watch Later video","url":"dualWatch12345","webpage_url":"https://youtu.be/dualWatch12345","description":"Watch Later description","uploader":"Channel One","uploader_id":"channel-one","channel":"Channel One","channel_id":"UC1","upload_date":"20260418"}]}'
    ;;
  "https://www.youtube.com/playlist?list=LL")
    printf '%s\n' '{"id":"LL","title":"Liked videos","entries":[{"id":"dualLiked12345","title":"Liked video","url":"dualLiked12345","webpage_url":"https://youtu.be/dualLiked12345","description":"Liked description","uploader":"Channel Two","uploader_id":"channel-two","channel":"Channel Two","channel_id":"UC2","upload_date":"20260417"}]}'
    ;;
  *)
    echo "unexpected yt-dlp url: $last" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake dual yt-dlp: %v", err)
	}
	return scriptPath
}

func installPartialFailureFakeYTDLP(t *testing.T, root string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-yt-dlp-partial-failure")
	script := `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  "https://www.youtube.com/playlist?list=WL")
    echo "WARNING: [youtube:tab] YouTube said: The playlist does not exist." >&2
    echo "ERROR: [youtube:tab] WL: YouTube said: The playlist does not exist." >&2
    exit 1
    ;;
  "https://www.youtube.com/playlist?list=LL")
    printf '%s\n' '{"id":"LL","title":"Liked videos","entries":[{"id":"partialLiked123","title":"Liked video after failure","url":"partialLiked123","webpage_url":"https://youtu.be/partialLiked123","description":"Liked description","uploader":"Channel Two","uploader_id":"channel-two","channel":"Channel Two","channel_id":"UC2","upload_date":"20260417"}]}'
    ;;
  *)
    echo "unexpected yt-dlp url: $last" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake partial failure yt-dlp: %v", err)
	}
	return scriptPath
}

func installDiscoveredProfileFakeYTDLP(t *testing.T, root string, wantCookies string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-yt-dlp-discovered-profile")
	script := `#!/bin/sh
last=""
cookies=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--cookies-from-browser" ]; then
    cookies="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ "$cookies" != "` + wantCookies + `" ]; then
  echo "ERROR: wrong cookies source: $cookies" >&2
  exit 1
fi
if [ "$last" != "https://www.youtube.com/playlist?list=WL" ]; then
  echo "unexpected yt-dlp url: $last" >&2
  exit 1
fi
printf '%s\n' '{"id":"WL","title":"Watch Later","entries":[{"id":"profileRetry123","title":"Retried via discovered profile","url":"profileRetry123","webpage_url":"https://youtu.be/profileRetry123","description":"Recovered with profile discovery","uploader":"Channel Name","uploader_id":"channel-name","channel":"Channel Name","channel_id":"UC123","upload_date":"20260418"}]}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake discovered profile yt-dlp: %v", err)
	}
	return scriptPath
}

func installSourceExtractFakeSummarize(t *testing.T, root string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-summarize-source-extract")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-youtube-extract-1.0.0"
  exit 0
fi
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  https://www.youtube.com/watch\?v=*|https://youtu.be/*)
    printf '{"input":{"model":"cli/test/youtube"},"extracted":{"url":"%s","title":"YouTube source","description":"","siteName":"YouTube","content":"source extract from fake summarize"},"summary":null}\n' "$last"
    ;;
  *)
    echo "unexpected summarize input: $last" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake source extract summarize: %v", err)
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
if [ -f "$last" ]; then
  input="$(cat "$last")"
  case "$input" in
    *"full transcript from fake summarize"*) ;;
    *)
      echo "expected transcript in summary file during summary step" >&2
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

func installFallbackFakeYTDLP(t *testing.T, root string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-yt-dlp-fallback")
	script := `#!/bin/sh
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    out="$arg"
  fi
  prev="$arg"
done
audio="${out%\.*}.mp3"
printf '%s\n' "fake audio" > "$audio"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp fallback: %v", err)
	}
	return scriptPath
}

func installFallbackFakeWhisper(t *testing.T, root string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-whisper-cli")
	script := `#!/bin/sh
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-of" ]; then
    out="$arg"
  fi
  prev="$arg"
done
printf '%s\n' "transcript from fake whisper" > "${out}.txt"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake whisper cli: %v", err)
	}
	return scriptPath
}

func installFallbackFakeMacWhisper(t *testing.T, root string, wantModel string, transcript string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-mw-cli")
	script := `#!/bin/sh
model=""
file=""
prev=""
first="$1"
for arg in "$@"; do
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  file="$arg"
  prev="$arg"
done
if [ "$first" != "transcribe" ]; then
  echo "expected transcribe subcommand" >&2
  exit 1
fi
if [ "` + wantModel + `" = "" ]; then
  if [ "$model" != "" ]; then
    echo "unexpected model: $model" >&2
    exit 1
  fi
else
  if [ "$model" != "` + wantModel + `" ]; then
    echo "unexpected model: $model" >&2
    exit 1
  fi
fi
if [ ! -f "$file" ]; then
  echo "expected audio file argument" >&2
  exit 1
fi
printf '%s\n' "` + transcript + `"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mw cli: %v", err)
	}
	return scriptPath
}
