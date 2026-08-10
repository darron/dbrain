package store

import (
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestListMastodonMediaRefsForDownloadDeduplicatesBeforeForceLimit(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := t.Context()
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	seed := func(key, remoteURL, status string, discoveredAt time.Time) (int64, int64) {
		t.Helper()
		item, err := st.UpsertItem(ctx, testItem(key, "mastodon_bookmark", "https://mastodon.example/"+key, base))
		if err != nil {
			t.Fatalf("UpsertItem(%s): %v", key, err)
		}
		if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{RemoteURL: remoteURL, MediaType: "photo"}}); err != nil {
			t.Fatalf("SaveItemMediaCandidates(%s): %v", key, err)
		}
		refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
		if err != nil || len(refs) != 1 {
			t.Fatalf("ListItemMediaRefs(%s): refs=%+v err=%v", key, refs, err)
		}
		if _, err := st.db.ExecContext(ctx, `
			UPDATE media_assets
			SET download_status = ?, discovered_at = ?, last_download_attempt_at = '', download_error_count = 0
			WHERE id = ?`, status, discoveredAt.Format(time.RFC3339), refs[0].MediaAssetID); err != nil {
			t.Fatalf("seed media asset %s: %v", key, err)
		}
		return item.ItemID, refs[0].MediaAssetID
	}

	firstItemID, firstAssetID := seed("force-first", "https://media.mastodon.example/first.jpg", model.MediaDownloadStatusPending, base)
	duplicateItemID, duplicateAssetID := seed("force-duplicate", "https://media.mastodon.example/first.jpg", model.MediaDownloadStatusPending, base)
	if firstItemID >= duplicateItemID {
		t.Fatalf("fixture item order = first %d duplicate %d; want first item lower", firstItemID, duplicateItemID)
	}
	if firstAssetID != duplicateAssetID {
		t.Fatalf("duplicate remote URL created assets %d and %d", firstAssetID, duplicateAssetID)
	}
	_, secondAssetID := seed("force-second", "https://media.mastodon.example/second.jpg", model.MediaDownloadStatusPending, base.Add(time.Minute))
	_, downloadedAssetID := seed("force-downloaded", "https://media.mastodon.example/downloaded.jpg", model.MediaDownloadStatusDownloaded, base.Add(-time.Hour))
	_, goneAssetID := seed("force-gone", "https://media.mastodon.example/gone.jpg", model.MediaDownloadStatusGone, base.Add(-2*time.Hour))

	refs, err := st.ListMastodonMediaRefsForDownload(ctx, 2, true)
	if err != nil {
		t.Fatalf("ListMastodonMediaRefsForDownload(force): %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("force selector returned %d refs, want 2: %+v", len(refs), refs)
	}
	if refs[0].MediaAssetID != firstAssetID || refs[1].MediaAssetID != secondAssetID {
		t.Fatalf("force selector order/assets = %+v, want [%d %d]", refs, firstAssetID, secondAssetID)
	}
	if refs[0].MediaAssetID == refs[1].MediaAssetID {
		t.Fatalf("force selector returned duplicate asset rows: %+v", refs)
	}
	for _, ref := range refs {
		if ref.MediaAssetID == downloadedAssetID || ref.MediaAssetID == goneAssetID {
			t.Fatalf("force selector returned successful/gone asset %d: %+v", ref.MediaAssetID, refs)
		}
	}
}

func TestListMastodonMediaRefsForDownloadForceRotatesAttemptedFailures(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := t.Context()
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	seed := func(key, status string, lastAttempt time.Time) int64 {
		t.Helper()
		item, err := st.UpsertItem(ctx, testItem(key, "mastodon_bookmark", "https://mastodon.example/"+key, base))
		if err != nil {
			t.Fatalf("UpsertItem(%s): %v", key, err)
		}
		if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{RemoteURL: "https://media.mastodon.example/" + key + ".jpg", MediaType: "photo"}}); err != nil {
			t.Fatalf("SaveItemMediaCandidates(%s): %v", key, err)
		}
		refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
		if err != nil || len(refs) != 1 {
			t.Fatalf("ListItemMediaRefs(%s): refs=%+v err=%v", key, refs, err)
		}
		downloadErrorCount := 0
		if status == model.MediaDownloadStatusBlocked {
			downloadErrorCount = model.MediaDownloadMaxConsecutiveErrors
		}
		if _, err := st.db.ExecContext(ctx, `
			UPDATE media_assets
			SET download_status = ?, download_error_count = ?, discovered_at = ?, last_download_attempt_at = ?
			WHERE id = ?`, status, downloadErrorCount, base, lastAttempt.Format(time.RFC3339), refs[0].MediaAssetID); err != nil {
			t.Fatalf("seed blocked media asset %s: %v", key, err)
		}
		return refs[0].MediaAssetID
	}

	oldestAssetID := seed("rotate-oldest", model.MediaDownloadStatusBlocked, base)
	nextAssetID := seed("rotate-next", model.MediaDownloadStatusBlocked, base.Add(time.Minute))
	_ = seed("rotate-downloaded", model.MediaDownloadStatusDownloaded, base.Add(-time.Hour))
	_ = seed("rotate-gone", model.MediaDownloadStatusGone, base.Add(-2*time.Hour))

	first, err := st.ListMastodonMediaRefsForDownload(ctx, 1, true)
	if err != nil {
		t.Fatalf("first forced selection: %v", err)
	}
	if len(first) != 1 || first[0].MediaAssetID != oldestAssetID {
		t.Fatalf("first forced selection = %+v, want oldest blocked asset %d", first, oldestAssetID)
	}
	if _, err := st.SaveMediaDownload(ctx, oldestAssetID, model.MediaDownloadResult{
		Status: model.MediaDownloadStatusBlocked, Error: "still blocked", AttemptedAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("record repeated blocked attempt: %v", err)
	}
	second, err := st.ListMastodonMediaRefsForDownload(ctx, 1, true)
	if err != nil {
		t.Fatalf("second forced selection: %v", err)
	}
	if len(second) != 1 || second[0].MediaAssetID != nextAssetID {
		t.Fatalf("second forced selection = %+v, want next blocked asset %d", second, nextAssetID)
	}
}
