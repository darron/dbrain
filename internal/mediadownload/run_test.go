package mediadownload

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestRunForItemDownloadsMediaIntoVault(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:download-media", now)
	changed, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"` + server.URL + `/image.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	})
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected hydration insert to change state")
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Candidates != 1 || stats.Requested != 1 || stats.Downloaded != 1 {
		t.Fatalf("unexpected download stats: %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].DownloadStatus != "downloaded" {
		t.Fatalf("expected downloaded media ref, got %+v", refs[0])
	}
	if !strings.HasPrefix(refs[0].LocalPath, "media/x/photo/") {
		t.Fatalf("unexpected local path: %+v", refs[0])
	}
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(refs[0].LocalPath))
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("expected downloaded file at %s: %v", fullPath, err)
	}
}

func TestRunForItemLogsLargeDownloadProgress(t *testing.T) {
	t.Parallel()

	payload := []byte("progress-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "video/mp4")
		w.Header().Set("content-length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 6, 0, 0, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:download-progress", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "video",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"` + server.URL + `/video.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":3840,"height":2160}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	stats, err := RunForItem(ctx, cfg, st, itemID, Options{
		Logger:           logger,
		ProgressBytes:    1,
		ProgressInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Downloaded != 1 {
		t.Fatalf("expected downloaded media, got %+v", stats)
	}

	logOutput := logs.String()
	for _, value := range []string{"x media download started", "x media download progress", "media_asset_id", "percent"} {
		if !strings.Contains(logOutput, value) {
			t.Fatalf("expected progress log to contain %q, got %q", value, logOutput)
		}
	}
}

func TestRunForItemMarksGoneMedia(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:gone-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"` + server.URL + `/missing.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Gone != 1 {
		t.Fatalf("expected gone media stat, got %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].DownloadStatus != "gone" {
		t.Fatalf("expected gone media ref, got %+v", refs[0])
	}
	if refs[0].LocalPath != "" {
		t.Fatalf("expected gone media to have no local path, got %+v", refs[0])
	}
}

func TestRunForItemBlocksMediaAfterRepeatedErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 5, 14, 1, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:block-error-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"` + server.URL + `/video.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":1920,"height":1080}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	var stats Stats
	for range model.MediaDownloadMaxConsecutiveErrors {
		stats, err = RunForItem(ctx, cfg, st, itemID, Options{Force: true})
		if err != nil {
			t.Fatalf("RunForItem: %v", err)
		}
	}
	if stats.Blocked != 1 {
		t.Fatalf("expected terminal blocked stat on final attempt, got %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].DownloadStatus != "blocked" {
		t.Fatalf("expected blocked media ref, got %+v", refs[0])
	}
	if refs[0].DownloadErrors != model.MediaDownloadMaxConsecutiveErrors {
		t.Fatalf("expected %d errors, got %+v", model.MediaDownloadMaxConsecutiveErrors, refs[0])
	}
}

func TestShouldDownloadSkipsArchivedPrunedMedia(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	ref := model.ItemMediaRef{
		DownloadStatus: "downloaded",
		LocalPath:      "media/x/photo/ab/test.jpg",
		ArchiveStatus:  "archived",
		LocalPrunedAt:  time.Now().UTC(),
	}
	if shouldDownload(ref, cfg, false) {
		t.Fatal("expected archived pruned media to skip re-download")
	}
}

func TestShouldDownloadBacksOffRecentErrors(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	now := time.Now().UTC()
	recent := model.ItemMediaRef{
		DownloadStatus: "error",
		DownloadErrors: 1,
		LastDownloadAt: now,
	}
	if shouldDownload(recent, cfg, false) {
		t.Fatal("expected recent media error to wait for retry cooldown")
	}

	old := recent
	old.LastDownloadAt = now.Add(-model.MediaDownloadRetryCooldown - time.Minute)
	if !shouldDownload(old, cfg, false) {
		t.Fatal("expected old media error to retry after cooldown")
	}

	blocked := old
	blocked.DownloadErrors = model.MediaDownloadMaxConsecutiveErrors
	if shouldDownload(blocked, cfg, false) {
		t.Fatal("expected terminal media errors to stay out of retry queue")
	}
}

func openTestStore(t *testing.T, path string) *store.Store {
	t.Helper()

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func insertTestItem(t *testing.T, st *store.Store, sourceKey string, now time.Time) int64 {
	t.Helper()

	result, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   strings.TrimPrefix(sourceKey, "x:"),
		CanonicalURL: "https://x.com/example/status/" + strings.TrimPrefix(sourceKey, "x:"),
		Title:        sourceKey,
		ContentHash:  sourceKey + "-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/" + strings.TrimPrefix(sourceKey, "x:") + ".md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	return result.ItemID
}
