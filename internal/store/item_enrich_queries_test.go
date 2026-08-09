package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestListItemsForXMediaTranscriptionPreservesForceRetryAndPrunedSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC()

	type candidate struct {
		key       string
		status    string
		completed time.Time
		pruned    bool
	}
	for _, candidate := range []candidate{
		{key: "x:transcription-pending"},
		{key: "x:transcription-error-due", status: model.XMediaTranscriptStatusError, completed: now.Add(-25 * time.Hour)},
		{key: "x:transcription-error-young", status: model.XMediaTranscriptStatusError, completed: now},
		{key: "x:transcription-current", status: model.XMediaTranscriptStatusOK, completed: now},
		{key: "x:transcription-terminal", status: model.XMediaTranscriptStatusNoAudio, completed: now},
		{key: "x:transcription-pruned-pending", pruned: true},
	} {
		itemID, assetID := seedPrunedMediaRepairItem(t, ctx, st, candidate.key, "video", now)
		if candidate.status != "" {
			if err := st.SaveXMediaTranscriptionState(ctx, itemID, candidate.status, "", candidate.completed); err != nil {
				t.Fatalf("seed X media transcription status for %s: %v", candidate.key, err)
			}
		}
		if candidate.pruned {
			if _, err := st.db.ExecContext(ctx, `UPDATE media_assets SET local_pruned_at = ? WHERE id = ?`, now.Format(time.RFC3339), assetID); err != nil {
				t.Fatalf("prune X media transcription media for %s: %v", candidate.key, err)
			}
		}
	}

	pending, err := st.ListItemsForXMediaTranscription(ctx, 100, false)
	if err != nil {
		t.Fatalf("ListItemsForXMediaTranscription non-force: %v", err)
	}
	if got, want := sortedItemSourceKeys(pending), []string{"x:transcription-error-due", "x:transcription-pending"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected non-force X media transcription candidates: got=%v want=%v", got, want)
	}

	forced, err := st.ListItemsForXMediaTranscription(ctx, 100, true)
	if err != nil {
		t.Fatalf("ListItemsForXMediaTranscription force: %v", err)
	}
	if got, want := sortedItemSourceKeys(forced), []string{
		"x:transcription-current",
		"x:transcription-error-due",
		"x:transcription-error-young",
		"x:transcription-pending",
		"x:transcription-terminal",
	}; !slices.Equal(got, want) {
		t.Fatalf("unexpected forced X media transcription candidates: got=%v want=%v", got, want)
	}
}

func TestMediaEnrichmentQueriesIncludeBlueskyBookmarks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC()
	photo := testItem("bsky:ocr", "bsky_bookmark", "https://bsky.app/profile/alice/post/ocr", now)
	video := testItem("bsky:transcription", "bsky_bookmark", "https://bsky.app/profile/alice/post/transcription", now)
	photoResult, err := st.UpsertItem(ctx, photo)
	if err != nil {
		t.Fatalf("upsert photo item: %v", err)
	}
	videoResult, err := st.UpsertItem(ctx, video)
	if err != nil {
		t.Fatalf("upsert video item: %v", err)
	}
	insertDownloadedAssetLink(t, st, photoResult.ItemID, "https://cdn.example/ocr.jpg", "photo", "media/bsky/photo/aa/ocr.jpg", now)
	insertDownloadedAssetLink(t, st, videoResult.ItemID, "https://cdn.example/transcription.mp4", "video", "media/bsky/video/bb/transcription.mp4", now)

	ocr, err := st.ListItemsForXPhotoOCR(ctx, 100, true)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR: %v", err)
	}
	if got := sortedItemSourceKeys(ocr); !slices.Contains(got, photo.SourceKey) {
		t.Fatalf("Bluesky photo missing from OCR candidates: %v", got)
	}
	transcription, err := st.ListItemsForXMediaTranscription(ctx, 100, true)
	if err != nil {
		t.Fatalf("ListItemsForXMediaTranscription: %v", err)
	}
	if got := sortedItemSourceKeys(transcription); !slices.Contains(got, video.SourceKey) {
		t.Fatalf("Bluesky video missing from transcription candidates: %v", got)
	}
}

func TestXMediaTranscriptionCandidateQueryLooksUpRunnableMediaByItemFirst(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	pendingWhere, pendingArgs := xMediaTranscriptionPendingWhere(time.Now().UTC())

	for _, tc := range []struct {
		name  string
		where string
		args  []any
	}{
		{name: "force", where: xMediaTranscriptionRunnableMediaExistsWhere},
		{name: "non-force", where: pendingWhere, args: pendingArgs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := st.db.QueryContext(ctx, `
				EXPLAIN QUERY PLAN
				SELECT id
				FROM items
				WHERE `+xItemSourceTypeWhere+`
					AND external_id != ''
					AND `+tc.where+`
				ORDER BY last_seen_at DESC, id DESC
				LIMIT 100`, tc.args...)
			if err != nil {
				t.Fatalf("explain %s X media transcription candidates: %v", tc.name, err)
			}
			defer func() { _ = rows.Close() }()

			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatalf("scan %s X media transcription query plan: %v", tc.name, err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate %s X media transcription query plan: %v", tc.name, err)
			}

			joined := strings.Join(plan, "\n")
			linkLookup := strings.Index(joined, "SEARCH l USING")
			assetLookup := strings.Index(joined, "SEARCH a USING INTEGER PRIMARY KEY")
			if linkLookup < 0 || assetLookup < linkLookup {
				t.Fatalf("%s X media transcription candidates must look up item_media_links before media_assets by ID:\n%s", tc.name, joined)
			}
			if strings.Contains(joined, "idx_media_assets_download_retry") {
				t.Fatalf("%s X media transcription candidates must not scan media_assets by download retry status for each item:\n%s", tc.name, joined)
			}
		})
	}
}

func TestListItemsForXPhotoOCRPreservesForcePendingAndPrunedSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC()

	type candidate struct {
		key    string
		status string
		pruned bool
	}
	for _, candidate := range []candidate{
		{key: "x:ocr-pending"},
		{key: "x:ocr-error", status: model.ItemOCRStatusError},
		{key: "x:ocr-current", status: model.ItemOCRStatusOK},
		{key: "x:ocr-blocked", status: model.ItemOCRStatusBlocked},
		{key: "x:ocr-pruned-pending", pruned: true},
	} {
		itemID, assetID := seedPrunedMediaRepairItem(t, ctx, st, candidate.key, "photo", now)
		if candidate.status != "" {
			if _, err := st.db.ExecContext(ctx, `UPDATE items SET ocr_status = ? WHERE id = ?`, candidate.status, itemID); err != nil {
				t.Fatalf("seed OCR status for %s: %v", candidate.key, err)
			}
		}
		if candidate.pruned {
			if _, err := st.db.ExecContext(ctx, `UPDATE media_assets SET local_pruned_at = ? WHERE id = ?`, now.Format(time.RFC3339), assetID); err != nil {
				t.Fatalf("prune OCR media for %s: %v", candidate.key, err)
			}
		}
	}

	pending, err := st.ListItemsForXPhotoOCR(ctx, 100, false)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR non-force: %v", err)
	}
	if got, want := sortedItemSourceKeys(pending), []string{"x:ocr-error", "x:ocr-pending"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected non-force OCR candidates: got=%v want=%v", got, want)
	}

	forced, err := st.ListItemsForXPhotoOCR(ctx, 100, true)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR force: %v", err)
	}
	if got, want := sortedItemSourceKeys(forced), []string{"x:ocr-blocked", "x:ocr-current", "x:ocr-error", "x:ocr-pending"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected forced OCR candidates: got=%v want=%v", got, want)
	}
}

func TestXPhotoOCRCandidateQueryLooksUpRunnableMediaByItemFirst(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		where string
	}{
		{name: "force", where: xPhotoOCRRunnableMediaExistsWhere},
		{name: "non-force", where: xPhotoOCRPendingWhere()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := st.db.QueryContext(ctx, `
				EXPLAIN QUERY PLAN
				SELECT id
				FROM items
				WHERE `+xItemSourceTypeWhere+`
					AND external_id != ''
					AND `+tc.where+`
				ORDER BY last_seen_at DESC, id DESC
				LIMIT 100`)
			if err != nil {
				t.Fatalf("explain %s X photo OCR candidates: %v", tc.name, err)
			}
			defer func() { _ = rows.Close() }()

			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatalf("scan %s query plan: %v", tc.name, err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate %s query plan: %v", tc.name, err)
			}

			joined := strings.Join(plan, "\n")
			linkLookup := strings.Index(joined, "SEARCH l USING")
			assetLookup := strings.Index(joined, "SEARCH a USING INTEGER PRIMARY KEY")
			if linkLookup < 0 || assetLookup < linkLookup {
				t.Fatalf("%s OCR candidates must look up item_media_links before media_assets by ID:\n%s", tc.name, joined)
			}
			if strings.Contains(joined, "idx_media_assets_download_retry") {
				t.Fatalf("%s OCR candidates must not scan media_assets by download retry status for each item:\n%s", tc.name, joined)
			}
		})
	}
}

func sortedItemSourceKeys(items []model.Item) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.SourceKey)
	}
	slices.Sort(keys)
	return keys
}
