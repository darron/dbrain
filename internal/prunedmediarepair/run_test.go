package prunedmediarepair

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestRunDryRunReportsCandidatesWithoutDownloader(t *testing.T) {
	t.Parallel()

	cfg, st := openPrunedMediaRepairTestStore(t)
	seedCoordinatorRepairItem(t, cfg, st, "x:dry-run", true, false)
	calls := 0
	stats, err := runWithDownloader(context.Background(), cfg, st, Options{OCR: true, Transcripts: true, Limit: 5000, Timeout: 45 * time.Second}, func(context.Context, config.Config, *store.Store, int64, mediadownload.Options) (mediadownload.Stats, error) {
		calls++
		return mediadownload.Stats{}, nil
	})
	if err != nil {
		t.Fatalf("runWithDownloader: %v", err)
	}
	if calls != 0 {
		t.Fatalf("dry-run called downloader %d times", calls)
	}
	if stats.Apply || stats.OCRCandidates != 1 || stats.TranscriptCandidates != 0 || stats.ItemsVisited != 0 {
		t.Fatalf("unexpected dry-run stats: %+v", stats)
	}
}

func TestRunApplyDeduplicatesItemsAndAggregatesMediaStats(t *testing.T) {
	t.Parallel()

	cfg, st := openPrunedMediaRepairTestStore(t)
	itemID := seedCoordinatorRepairItem(t, cfg, st, "x:both", true, true)
	wantTimeout := 17 * time.Second
	var calls []int64
	stats, err := runWithDownloader(context.Background(), cfg, st, Options{Apply: true, OCR: true, Transcripts: true, Limit: 5000, Timeout: wantTimeout}, func(_ context.Context, gotCfg config.Config, gotStore *store.Store, gotItemID int64, opts mediadownload.Options) (mediadownload.Stats, error) {
		if gotCfg.DBPath != cfg.DBPath || gotStore != st {
			t.Fatalf("unexpected downloader dependencies")
		}
		if !opts.Force || opts.Timeout != wantTimeout {
			t.Fatalf("unexpected downloader options: %+v", opts)
		}
		calls = append(calls, gotItemID)
		return mediadownload.Stats{Candidates: 3, Requested: 2, Downloaded: 1, Gone: 1, Errors: 2, Blocked: 1, Changed: 4}, nil
	})
	if err != nil {
		t.Fatalf("runWithDownloader: %v", err)
	}
	if len(calls) != 1 || calls[0] != itemID {
		t.Fatalf("expected one deduplicated call for %d, got %v", itemID, calls)
	}
	want := Stats{
		Apply: true, OCRCandidates: 1, TranscriptCandidates: 1, ItemsVisited: 1, ItemsRestored: 1,
		MediaCandidates: 3, MediaRequested: 2, MediaDownloaded: 1, MediaGone: 1,
		MediaErrors: 2, MediaBlocked: 1, MediaChanged: 4,
	}
	if stats != want {
		t.Fatalf("unexpected apply stats: got=%+v want=%+v", stats, want)
	}
}

func TestRunApplyRetainsPartialStatsAndStopsOnOperationalError(t *testing.T) {
	t.Parallel()

	cfg, st := openPrunedMediaRepairTestStore(t)
	firstID := seedCoordinatorRepairItem(t, cfg, st, "x:error-first", true, false)
	secondID := seedCoordinatorRepairItem(t, cfg, st, "x:error-second", true, false)
	var calls []int64
	wantErr := errors.New("download transport failed")
	stats, err := runWithDownloader(context.Background(), cfg, st, Options{Apply: true, OCR: true, Limit: 5000, Timeout: 45 * time.Second}, func(_ context.Context, _ config.Config, _ *store.Store, itemID int64, _ mediadownload.Options) (mediadownload.Stats, error) {
		calls = append(calls, itemID)
		return mediadownload.Stats{Candidates: 4, Requested: 3, Downloaded: 1, Gone: 1, Errors: 1, Blocked: 2, Changed: 2}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected operational error %v, got %v", wantErr, err)
	}
	if len(calls) != 1 || calls[0] != firstID {
		t.Fatalf("expected stop after first deterministic item %d (later=%d), got calls=%v", firstID, secondID, calls)
	}
	want := Stats{
		Apply: true, OCRCandidates: 2, ItemsVisited: 1, ItemsRestored: 1,
		MediaCandidates: 4, MediaRequested: 3, MediaDownloaded: 1, MediaGone: 1,
		MediaErrors: 1, MediaBlocked: 2, MediaChanged: 2,
	}
	if stats != want {
		t.Fatalf("partial stats lost on operational error: got=%+v want=%+v", stats, want)
	}
}

func openPrunedMediaRepairTestStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return cfg, st
}

func seedCoordinatorRepairItem(t *testing.T, cfg config.Config, st *store.Store, key string, photo, video bool) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey: key, SourceType: "x_bookmark", ExternalID: key,
		CanonicalURL: "https://x.com/example/status/" + key, Title: key,
		ContentHash: key + "-hash", LinksJSON: "[]", NotePath: filepath.Join("items", key+".md"), RawJSON: `{}`,
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	mediaJSON := ""
	if photo {
		mediaJSON += `{"type":"photo","url":"https://pbs.twimg.com/media/` + key + `.jpg"}`
	}
	if video {
		if mediaJSON != "" {
			mediaJSON += ","
		}
		mediaJSON += `{"type":"video","url":"https://video.twimg.com/ext_tw_video/` + key + `.mp4"}`
	}
	if _, err := st.SaveXHydration(ctx, item.ItemID, model.XHydration{FullText: "hello", Status: "ok_graphql", FetchedAt: now, APIJSON: `{"snapshot":{"media_objects":[` + mediaJSON + `]}}`}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	for _, ref := range refs {
		localPath := filepath.Join("media", key, time.Now().Format("150405.000000000"))
		if _, err := st.SaveMediaDownload(ctx, ref.MediaAssetID, model.MediaDownloadResult{LocalPath: localPath, ContentHash: key, Status: model.MediaDownloadStatusDownloaded, DownloadedAt: now}); err != nil {
			t.Fatalf("SaveMediaDownload: %v", err)
		}
	}
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(ctx, `UPDATE media_assets SET archive_status = 'archived', local_pruned_at = ? WHERE id IN (SELECT media_asset_id FROM item_media_links WHERE item_id = ?)`, now.Format(time.RFC3339), item.ItemID); err != nil {
		t.Fatalf("mark pruned: %v", err)
	}
	return item.ItemID
}
