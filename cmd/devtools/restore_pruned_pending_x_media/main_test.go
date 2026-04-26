package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestLoadIDsSelectsOnlyPendingPrunedMediaWork(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, rawDB, now := openRestoreTestStore(t)

	transcriptPendingID, transcriptPendingAssetID := seedRestoreTestItem(t, ctx, st, "x:transcript-pending-pruned", "video", now)
	markPrunedAndArchived(t, ctx, rawDB, transcriptPendingAssetID, now)

	transcriptCurrentID, transcriptCurrentAssetID := seedRestoreTestItem(t, ctx, st, "x:transcript-current-pruned", "video", now)
	markTranscriptCurrent(t, ctx, rawDB, transcriptCurrentID, now)
	markPrunedAndArchived(t, ctx, rawDB, transcriptCurrentAssetID, now)

	transcriptLocalID, _ := seedRestoreTestItem(t, ctx, st, "x:transcript-local-pending", "video", now)

	ocrPendingID, ocrPendingAssetID := seedRestoreTestItem(t, ctx, st, "x:ocr-pending-pruned", "photo", now)
	markPrunedAndArchived(t, ctx, rawDB, ocrPendingAssetID, now)

	ocrCurrentID, ocrCurrentAssetID := seedRestoreTestItem(t, ctx, st, "x:ocr-current-pruned", "photo", now)
	markOCRCurrent(t, ctx, rawDB, ocrCurrentID, now)
	markPrunedAndArchived(t, ctx, rawDB, ocrCurrentAssetID, now)

	ocrLocalID, _ := seedRestoreTestItem(t, ctx, st, "x:ocr-local-pending", "photo", now)

	transcriptionItems, err := st.ListItemsForXMediaTranscription(ctx, 20, false)
	if err != nil {
		t.Fatalf("ListItemsForXMediaTranscription: %v", err)
	}
	if len(transcriptionItems) != 1 || transcriptionItems[0].ID != transcriptLocalID {
		t.Fatalf("expected only local transcript item to be runnable, got %#v", transcriptionItems)
	}

	ocrItems, err := st.ListItemsForXPhotoOCR(ctx, 20, false)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR: %v", err)
	}
	if len(ocrItems) != 1 || ocrItems[0].ID != ocrLocalID {
		t.Fatalf("expected only local ocr item to be runnable, got %#v", ocrItems)
	}

	transcriptRestoreIDs, err := loadIDs(rawDB, pendingTranscriptQuery, 20)
	if err != nil {
		t.Fatalf("loadIDs transcript: %v", err)
	}
	if !slices.Equal(transcriptRestoreIDs, []int64{transcriptPendingID}) {
		t.Fatalf("unexpected pending pruned transcript ids: got=%v want=%v", transcriptRestoreIDs, []int64{transcriptPendingID})
	}

	ocrRestoreIDs, err := loadIDs(rawDB, pendingOCRQuery, 20)
	if err != nil {
		t.Fatalf("loadIDs ocr: %v", err)
	}
	if !slices.Equal(ocrRestoreIDs, []int64{ocrPendingID}) {
		t.Fatalf("unexpected pending pruned ocr ids: got=%v want=%v", ocrRestoreIDs, []int64{ocrPendingID})
	}
}

func openRestoreTestStore(t *testing.T) (*store.Store, *sql.DB, time.Time) {
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
	rawDB, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		_ = st.Close()
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
		_ = st.Close()
	})

	return st, rawDB, time.Date(2026, 4, 25, 20, 0, 0, 0, time.UTC)
}

func seedRestoreTestItem(t *testing.T, ctx context.Context, st *store.Store, sourceKey string, mediaType string, now time.Time) (int64, int64) {
	t.Helper()

	itemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   sourceKey,
		CanonicalURL: "https://x.com/example/status/" + sourceKey,
		Title:        sourceKey,
		ContentHash:  sourceKey + "-hash",
		LinksJSON:    "[]",
		NotePath:     filepath.ToSlash(filepath.Join("items", "x", "test.md")),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}

	expandedSuffix := "photo/1"
	remoteURL := "https://pbs.twimg.com/media/" + sourceKey + ".jpg"
	localPath := "media/x/photo/" + sourceKey + ".jpg"
	if mediaType == "video" {
		expandedSuffix = "video/1"
		remoteURL = "https://video.twimg.com/ext_tw_video/" + sourceKey + ".mp4"
		localPath = "media/x/video/" + sourceKey + ".mp4"
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"` + mediaType + `","url":"` + remoteURL + `","expanded_url":"https://x.com/example/status/` + sourceKey + `/` + expandedSuffix + `","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(ctx, itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration %s: %v", sourceKey, err)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs %s: %v", sourceKey, err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref for %s, got %d", sourceKey, len(refs))
	}
	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		LocalPath:    localPath,
		ContentHash:  sourceKey + "-download",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload %s: %v", sourceKey, err)
	}

	return itemResult.ItemID, refs[0].MediaAssetID
}

func markPrunedAndArchived(t *testing.T, ctx context.Context, rawDB *sql.DB, assetID int64, now time.Time) {
	t.Helper()

	if _, err := rawDB.ExecContext(ctx, `
		UPDATE media_assets
		SET archive_status = 'archived',
			local_pruned_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		assetID,
	); err != nil {
		t.Fatalf("mark asset pruned %d: %v", assetID, err)
	}
}

func markTranscriptCurrent(t *testing.T, ctx context.Context, rawDB *sql.DB, itemID int64, now time.Time) {
	t.Helper()

	if _, err := rawDB.ExecContext(ctx, `
		UPDATE items
		SET article_title = 'X Media Transcript',
			article_text = 'transcript text',
			x_media_transcript_status = 'ok',
			x_media_transcript_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		itemID,
	); err != nil {
		t.Fatalf("mark transcript current %d: %v", itemID, err)
	}
}

func markOCRCurrent(t *testing.T, ctx context.Context, rawDB *sql.DB, itemID int64, now time.Time) {
	t.Helper()

	if _, err := rawDB.ExecContext(ctx, `
		UPDATE items
		SET ocr_status = 'ok',
			ocr_text = 'ocr text',
			ocr_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		itemID,
	); err != nil {
		t.Fatalf("mark ocr current %d: %v", itemID, err)
	}
}
