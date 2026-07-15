package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestListPrunedMediaRepairCandidatesSelectsWorkerPendingItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC()

	transcriptPendingID, transcriptPendingAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:transcript-pending-pruned", "video", now)
	markRepairAssetPrunedAndArchived(t, ctx, st, transcriptPendingAssetID, now)

	transcriptDueErrorID, transcriptDueErrorAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:transcript-due-error-pruned", "video", now)
	if err := st.SaveXMediaTranscriptionState(ctx, transcriptDueErrorID, model.XMediaTranscriptStatusError, "retry", now.Add(-xMediaTranscriptionErrorRetryCooldown-time.Minute)); err != nil {
		t.Fatalf("SaveXMediaTranscriptionState due error: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, transcriptDueErrorAssetID, now)

	transcriptYoungErrorID, transcriptYoungErrorAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:transcript-young-error-pruned", "video", now)
	if err := st.SaveXMediaTranscriptionState(ctx, transcriptYoungErrorID, model.XMediaTranscriptStatusError, "cooldown", now); err != nil {
		t.Fatalf("SaveXMediaTranscriptionState young error: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, transcriptYoungErrorAssetID, now)

	transcriptCurrentID, transcriptCurrentAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:transcript-current-pruned", "video", now)
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET article_title = ?, article_text = ? WHERE id = ?`, model.XMediaTranscriptArticleTitle, "raw transcript", transcriptCurrentID); err != nil {
		t.Fatalf("seed current transcript text: %v", err)
	}
	if err := st.SaveXMediaTranscription(ctx, transcriptCurrentID, XMediaTranscriptionState{Status: model.XMediaTranscriptStatusOK, CompletedAt: now}); err != nil {
		t.Fatalf("SaveXMediaTranscription current: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, transcriptCurrentAssetID, now)

	transcriptTerminalID, transcriptTerminalAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:transcript-terminal-pruned", "video", now)
	if err := st.SaveXMediaTranscriptionState(ctx, transcriptTerminalID, model.XMediaTranscriptStatusTooShort, "terminal", now); err != nil {
		t.Fatalf("SaveXMediaTranscriptionState terminal: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, transcriptTerminalAssetID, now)

	seedAdditionalRepairAsset(t, ctx, st, transcriptPendingID, "video", "x:transcript-pending-runnable", now, false)

	ocrPendingID, ocrPendingAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:ocr-pending-pruned", "photo", now)
	markRepairAssetPrunedAndArchived(t, ctx, st, ocrPendingAssetID, now)

	ocrErrorID, ocrErrorAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:ocr-error-pruned", "photo", now)
	if _, err := st.SaveItemOCR(ctx, ocrErrorID, model.OCRResult{Status: model.ItemOCRStatusError, Error: "retry", FetchedAt: now}, "hash"); err != nil {
		t.Fatalf("SaveItemOCR error: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, ocrErrorAssetID, now)

	ocrCurrentID, ocrCurrentAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:ocr-current-pruned", "photo", now)
	if _, err := st.SaveItemOCR(ctx, ocrCurrentID, model.OCRResult{Status: model.ItemOCRStatusOK, Text: "raw OCR", FetchedAt: now}, "hash"); err != nil {
		t.Fatalf("SaveItemOCR current: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, ocrCurrentAssetID, now)

	ocrBlockedID, ocrBlockedAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:ocr-blocked-pruned", "photo", now)
	if _, err := st.SaveItemOCR(ctx, ocrBlockedID, model.OCRResult{Status: model.ItemOCRStatusBlocked, Error: "blocked", FetchedAt: now}, "hash"); err != nil {
		t.Fatalf("SaveItemOCR blocked: %v", err)
	}
	markRepairAssetPrunedAndArchived(t, ctx, st, ocrBlockedAssetID, now)

	_, notArchivedAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:ocr-pruned-not-archived", "photo", now)
	if _, err := st.db.ExecContext(ctx, `UPDATE media_assets SET local_pruned_at = ? WHERE id = ?`, now.Format(time.RFC3339), notArchivedAssetID); err != nil {
		t.Fatalf("mark pruned without archive: %v", err)
	}

	got, err := st.ListPrunedMediaRepairCandidates(ctx, true, true, 20)
	if err != nil {
		t.Fatalf("ListPrunedMediaRepairCandidates: %v", err)
	}
	if !slices.Equal(got.TranscriptItemIDs, []int64{transcriptDueErrorID}) {
		t.Fatalf("unexpected transcript candidates: got=%v want=%v (pending=%d young=%d current=%d terminal=%d)", got.TranscriptItemIDs, []int64{transcriptDueErrorID}, transcriptPendingID, transcriptYoungErrorID, transcriptCurrentID, transcriptTerminalID)
	}
	if !slices.Equal(got.OCRItemIDs, []int64{ocrPendingID, ocrErrorID}) {
		t.Fatalf("unexpected OCR candidates: got=%v want=%v", got.OCRItemIDs, []int64{ocrPendingID, ocrErrorID})
	}
}

func TestListPrunedMediaRepairCandidatesAppliesIndependentLimitsAndNonNilSlices(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC()
	for _, tc := range []struct{ key, mediaType string }{
		{"x:ocr-limit-one", "photo"}, {"x:ocr-limit-two", "photo"},
		{"x:transcript-limit-one", "video"}, {"x:transcript-limit-two", "video"},
	} {
		_, assetID := seedPrunedMediaRepairItem(t, ctx, st, tc.key, tc.mediaType, now)
		markRepairAssetPrunedAndArchived(t, ctx, st, assetID, now)
	}

	got, err := st.ListPrunedMediaRepairCandidates(ctx, true, true, 1)
	if err != nil {
		t.Fatalf("ListPrunedMediaRepairCandidates: %v", err)
	}
	if len(got.OCRItemIDs) != 1 || len(got.TranscriptItemIDs) != 1 {
		t.Fatalf("expected independent per-category limits, got %+v", got)
	}
	if got.OCRItemIDs == nil || got.TranscriptItemIDs == nil {
		t.Fatalf("expected non-nil slices, got %+v", got)
	}

	disabled, err := st.ListPrunedMediaRepairCandidates(ctx, false, false, 1)
	if err != nil {
		t.Fatalf("ListPrunedMediaRepairCandidates disabled: %v", err)
	}
	if disabled.OCRItemIDs == nil || disabled.TranscriptItemIDs == nil || len(disabled.OCRItemIDs) != 0 || len(disabled.TranscriptItemIDs) != 0 {
		t.Fatalf("expected disabled categories as non-nil empty slices, got %+v", disabled)
	}
}

func TestListPrunedMediaRepairCandidatesSelectsMultipleItemsSharingOnePrunedAsset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC()
	firstID, sharedAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:shared-photo-first", "photo", now)
	markRepairAssetPrunedAndArchived(t, ctx, st, sharedAssetID, now)
	secondID, secondAssetID := seedPrunedMediaRepairItem(t, ctx, st, "x:shared-photo-second", "photo", now)
	if _, err := st.db.ExecContext(ctx, `DELETE FROM item_media_links WHERE item_id = ?`, secondID); err != nil {
		t.Fatalf("remove second item private link: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM media_assets WHERE id = ?`, secondAssetID); err != nil {
		t.Fatalf("remove second item private asset: %v", err)
	}
	nowText := now.Format(time.RFC3339)
	if _, err := st.db.ExecContext(ctx, `INSERT INTO item_media_links (item_id, media_asset_id, ordinal, expanded_url, created_at, updated_at) VALUES (?, ?, 0, '', ?, ?)`, secondID, sharedAssetID, nowText, nowText); err != nil {
		t.Fatalf("link shared pruned asset: %v", err)
	}

	got, err := st.ListPrunedMediaRepairCandidates(ctx, true, false, 20)
	if err != nil {
		t.Fatalf("ListPrunedMediaRepairCandidates: %v", err)
	}
	if !slices.Equal(got.OCRItemIDs, []int64{firstID, secondID}) {
		t.Fatalf("shared asset candidates are not deterministic: got=%v want=%v", got.OCRItemIDs, []int64{firstID, secondID})
	}
	if got.TranscriptItemIDs == nil || len(got.TranscriptItemIDs) != 0 {
		t.Fatalf("expected non-nil disabled transcript candidates, got=%v", got.TranscriptItemIDs)
	}
}

func seedPrunedMediaRepairItem(t *testing.T, ctx context.Context, st *Store, sourceKey, mediaType string, now time.Time) (int64, int64) {
	t.Helper()
	result, err := st.UpsertItem(ctx, model.Item{
		SourceKey: sourceKey, SourceType: "x_bookmark", ExternalID: sourceKey,
		CanonicalURL: "https://x.com/example/status/" + sourceKey, Title: sourceKey,
		ContentHash: sourceKey + "-hash", LinksJSON: "[]",
		NotePath: filepath.ToSlash(filepath.Join("items", "x", sourceKey+".md")), RawJSON: `{}`,
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}
	assetID := seedAdditionalRepairAsset(t, ctx, st, result.ItemID, mediaType, sourceKey, now, false)
	return result.ItemID, assetID
}

func seedAdditionalRepairAsset(t *testing.T, ctx context.Context, st *Store, itemID int64, mediaType, key string, now time.Time, pruned bool) int64 {
	t.Helper()
	remoteURL := "https://pbs.twimg.com/media/" + key + ".jpg"
	localPath := "media/x/photo/" + key + ".jpg"
	if mediaType == "video" {
		remoteURL = "https://video.twimg.com/ext_tw_video/" + key + ".mp4"
		localPath = "media/x/video/" + key + ".mp4"
	}
	nowText := now.Format(time.RFC3339)
	result, err := st.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			remote_url, media_type, mime_type, width, height, byte_size, content_hash,
			download_status, download_error, local_path, discovered_at, downloaded_at, updated_at
		) VALUES (?, ?, '', 0, 0, 0, '', ?, '', '', ?, '', ?)`,
		remoteURL, mediaType, model.MediaDownloadStatusPending, nowText, nowText)
	if err != nil {
		t.Fatalf("insert media asset %s: %v", key, err)
	}
	assetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("media asset id %s: %v", key, err)
	}
	var ordinal int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_media_links WHERE item_id = ?`, itemID).Scan(&ordinal); err != nil {
		t.Fatalf("load media ordinal %s: %v", key, err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO item_media_links (item_id, media_asset_id, ordinal, expanded_url, created_at, updated_at) VALUES (?, ?, ?, '', ?, ?)`, itemID, assetID, ordinal, nowText, nowText); err != nil {
		t.Fatalf("LinkItemMedia %s: %v", key, err)
	}
	if _, err := st.SaveMediaDownload(ctx, assetID, model.MediaDownloadResult{LocalPath: localPath, ContentHash: key + "-download", Status: model.MediaDownloadStatusDownloaded, DownloadedAt: now}); err != nil {
		t.Fatalf("SaveMediaDownload %s: %v", key, err)
	}
	if pruned {
		markRepairAssetPrunedAndArchived(t, ctx, st, assetID, now)
	}
	return assetID
}

func markRepairAssetPrunedAndArchived(t *testing.T, ctx context.Context, st *Store, assetID int64, now time.Time) {
	t.Helper()
	if _, err := st.db.ExecContext(ctx, `UPDATE media_assets SET archive_status = 'archived', local_pruned_at = ? WHERE id = ?`, now.Format(time.RFC3339), assetID); err != nil {
		t.Fatalf("mark asset pruned and archived %d: %v", assetID, err)
	}
}
