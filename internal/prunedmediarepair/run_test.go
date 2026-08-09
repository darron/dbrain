package prunedmediarepair

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
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
		return mediadownload.Stats{Candidates: 3, Requested: 3, Gone: 1, Errors: 1, Blocked: 1, Changed: 3}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected operational error %v, got %v", wantErr, err)
	}
	if len(calls) != 1 || calls[0] != firstID {
		t.Fatalf("expected stop after first deterministic item %d (later=%d), got calls=%v", firstID, secondID, calls)
	}
	want := Stats{
		Apply: true, OCRCandidates: 2, ItemsVisited: 1, ItemsRestored: 0,
		MediaCandidates: 3, MediaRequested: 3, MediaGone: 1,
		MediaErrors: 1, MediaBlocked: 1, MediaChanged: 3,
	}
	if stats != want {
		t.Fatalf("partial stats lost on operational error: got=%+v want=%+v", stats, want)
	}
}

func TestRunApplyScopesAllowlistToSelectedCategory(t *testing.T) {
	t.Parallel()

	cfg, st := openPrunedMediaRepairTestStore(t)
	itemID := seedCoordinatorRepairItem(t, cfg, st, "x:ocr-with-unrelated-video", true, true)
	refs, err := st.ListItemMediaRefs(context.Background(), itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	var photoID, videoID int64
	for _, ref := range refs {
		switch ref.MediaType {
		case "photo":
			photoID = ref.MediaAssetID
		case "video":
			videoID = ref.MediaAssetID
		}
	}
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(`UPDATE media_assets SET local_pruned_at = '' WHERE id = ?`, videoID); err != nil {
		_ = raw.Close()
		t.Fatalf("mark unrelated video current: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}
	calls := 0
	stats, err := runWithDownloader(context.Background(), cfg, st, Options{Apply: true, OCR: true, Limit: 5000, Timeout: 45 * time.Second}, func(_ context.Context, _ config.Config, _ *store.Store, gotItemID int64, opts mediadownload.Options) (mediadownload.Stats, error) {
		calls++
		if gotItemID != itemID || len(opts.AllowedAssetIDs) != 1 || opts.AllowedAssetIDs[0] != photoID {
			t.Fatalf("OCR repair allowlist included unrelated ref: item=%d opts=%+v photo=%d video=%d", gotItemID, opts, photoID, videoID)
		}
		return mediadownload.Stats{Candidates: 1, Requested: 1, Downloaded: 1, Changed: 1}, nil
	})
	if err != nil {
		t.Fatalf("runWithDownloader: %v", err)
	}
	if calls != 1 || stats.ItemsRestored != 1 || stats.MediaDownloaded != 1 {
		t.Fatalf("unexpected scoped repair result calls=%d stats=%+v", calls, stats)
	}
}

func TestRunApplyRestoresSocialMediaThroughSourceNamespaceAndMastodonOriginPolicy(t *testing.T) {
	cfg, st := openPrunedMediaRepairTestStore(t)
	bskyItemID := seedSocialCoordinatorRepairItem(t, st, "bsky:repair-photo", "bsky_bookmark", "photo", "https://cdn.bsky.example/photo.jpg")
	mastodonItemID := seedSocialCoordinatorRepairItem(t, st, "mastodon:repair-video", "mastodon_bookmark", "video", "https://media.mastodon.example/video.mp4")

	var calls []int64
	stats, err := runWithDownloader(t.Context(), cfg, st, Options{Apply: true, OCR: true, Transcripts: true, Limit: 5000, Timeout: 45 * time.Second}, func(ctx context.Context, _ config.Config, gotStore *store.Store, itemID int64, opts mediadownload.Options) (mediadownload.Stats, error) {
		calls = append(calls, itemID)
		if !opts.Force || len(opts.AllowedAssetIDs) != 1 {
			t.Fatalf("repair options = %+v", opts)
		}
		item, err := gotStore.GetItemByID(ctx, itemID)
		if err != nil {
			t.Fatalf("GetItemByID(%d): %v", itemID, err)
		}
		if itemID == bskyItemID {
			if opts.MediaNamespace != "bsky" || opts.HTTPPolicy != nil {
				t.Fatalf("Bluesky repair options = %+v", opts)
			}
		} else {
			if itemID != mastodonItemID || opts.MediaNamespace != "mastodon" || opts.HTTPPolicy == nil {
				t.Fatalf("Mastodon repair options = %+v", opts)
			}
			want, err := mastodonapi.MediaHTTPPolicy("https://media.mastodon.example/video.mp4", nil)
			if err != nil {
				t.Fatalf("MediaHTTPPolicy: %v", err)
			}
			if !slices.Equal(opts.HTTPPolicy.AllowedOrigins, want.AllowedOrigins) || !opts.HTTPPolicy.RejectCredentialQueryOnRedirect || opts.HTTPPolicy.AllowPrivateNetwork || len(opts.HTTPPolicy.AllowedPrivateOrigins) != 0 {
				t.Fatalf("Mastodon repair policy = %+v, want exact public origin", opts.HTTPPolicy)
			}
		}
		refs, err := gotStore.ListItemMediaRefs(ctx, itemID)
		if err != nil {
			t.Fatalf("ListItemMediaRefs(%d): %v", itemID, err)
		}
		for _, ref := range refs {
			if ref.MediaAssetID != opts.AllowedAssetIDs[0] {
				continue
			}
			ext := ".jpg"
			if ref.MediaType == "video" {
				ext = ".mp4"
			}
			if _, err := gotStore.SaveMediaDownload(ctx, ref.MediaAssetID, model.MediaDownloadResult{
				MIMEType: map[string]string{"photo": "image/jpeg", "video": "video/mp4"}[ref.MediaType],
				ByteSize: 100, ContentHash: item.SourceKey + "-restored",
				LocalPath: "media/" + opts.MediaNamespace + "/" + ref.MediaType + "/restored" + ext,
				Status:    model.MediaDownloadStatusDownloaded, DownloadedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("SaveMediaDownload(%d): %v", ref.MediaAssetID, err)
			}
		}
		return mediadownload.Stats{Candidates: 1, Requested: 1, Downloaded: 1, Changed: 1}, nil
	})
	if err != nil {
		t.Fatalf("runWithDownloader: %v", err)
	}
	if !slices.Equal(calls, []int64{bskyItemID, mastodonItemID}) || stats.ItemsRestored != 2 || stats.MediaDownloaded != 2 {
		t.Fatalf("social repair calls=%v stats=%+v", calls, stats)
	}
	ocrItems, err := st.ListItemsForXPhotoOCR(t.Context(), 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR: %v", err)
	}
	if len(ocrItems) != 1 || ocrItems[0].ID != bskyItemID {
		t.Fatalf("restored Bluesky photo not selector-eligible: %+v", ocrItems)
	}
	transcriptItems, err := st.ListItemsForXMediaTranscription(t.Context(), 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXMediaTranscription: %v", err)
	}
	if len(transcriptItems) != 1 || transcriptItems[0].ID != mastodonItemID {
		t.Fatalf("restored Mastodon video not selector-eligible: %+v", transcriptItems)
	}
}

func TestRunApplyMastodonRepairRejectsUnsafeRedirectsWithoutCredentials(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location string
	}{
		{name: "cross origin", location: "https://evil.example/asset.png"},
		{name: "credential query", location: "https://media.example.com/asset.png?access_token=secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if got := r.Header.Get("Authorization"); got != "" {
					t.Errorf("repair leaked Authorization header %q", got)
				}
				if got := r.Header.Get("Cookie"); got != "" {
					t.Errorf("repair leaked Cookie header %q", got)
				}
				http.Redirect(w, r, tc.location, http.StatusFound)
			}))
			defer server.Close()

			cfg, st := openPrunedMediaRepairTestStore(t)
			itemID := seedSocialCoordinatorRepairItem(t, st, "mastodon:unsafe-redirect", "mastodon_bookmark", "photo", "https://media.example.com/start.png")
			base := syntheticMastodonPolicyBase(t, server, "media.example.com")
			stats, err := runWithDownloaderAndMastodonPolicy(t.Context(), cfg, st, Options{
				Apply: true, OCR: true, Limit: 5000, Timeout: 45 * time.Second,
			}, mediadownload.RunForItem, &base)
			if err != nil {
				t.Fatalf("runWithDownloaderAndMastodonPolicy: %v", err)
			}
			if requests != 1 || stats.MediaRequested != 1 || stats.MediaBlocked != 1 || stats.MediaDownloaded != 0 {
				t.Fatalf("unsafe redirect requests=%d stats=%+v", requests, stats)
			}
			refs, err := st.ListItemMediaRefs(t.Context(), itemID)
			if err != nil || len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusBlocked {
				t.Fatalf("unsafe redirect ref=%+v err=%v", refs, err)
			}
		})
	}
}

func TestRunApplyMastodonRepairBlocksPrivateNetworkAndRequiresExplicitForceRecovery(t *testing.T) {
	payload := validRepairPNG(t)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("repair leaked Authorization header %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("repair leaked Cookie header %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	cfg, st := openPrunedMediaRepairTestStore(t)
	itemID := seedSocialCoordinatorRepairItem(t, st, "mastodon:private-repair", "mastodon_bookmark", "photo", server.URL+"/asset.png")
	stats, err := runWithDownloader(t.Context(), cfg, st, Options{
		Apply: true, OCR: true, Limit: 5000, Timeout: 45 * time.Second,
	}, mediadownload.RunForItem)
	if err != nil {
		t.Fatalf("runWithDownloader private boundary: %v", err)
	}
	if requests != 0 || stats.MediaBlocked != 1 || stats.MediaDownloaded != 0 {
		t.Fatalf("private-network repair requests=%d stats=%+v", requests, stats)
	}
	refs, err := st.ListItemMediaRefs(t.Context(), itemID)
	if err != nil || len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusBlocked {
		t.Fatalf("private-network blocked ref=%+v err=%v", refs, err)
	}

	testPolicy, err := mastodonapi.MediaHTTPPolicy(server.URL+"/asset.png", &safehttp.Policy{
		AllowPrivateNetwork: true,
		TLSClientConfig:     server.Client().Transport.(*http.Transport).TLSClientConfig,
	})
	if err != nil {
		t.Fatalf("MediaHTTPPolicy test recovery: %v", err)
	}
	normal, err := mediadownload.RunForItem(t.Context(), cfg, st, itemID, mediadownload.Options{
		MediaNamespace: "mastodon", AllowedAssetIDs: []int64{refs[0].MediaAssetID}, HTTPPolicy: &testPolicy,
	})
	if err != nil {
		t.Fatalf("normal retry: %v", err)
	}
	if normal.Requested != 0 || requests != 0 {
		t.Fatalf("terminal blocked asset retried without force: stats=%+v requests=%d", normal, requests)
	}
	forced, err := mediadownload.RunForItem(t.Context(), cfg, st, itemID, mediadownload.Options{
		Force: true, MediaNamespace: "mastodon", AllowedAssetIDs: []int64{refs[0].MediaAssetID}, HTTPPolicy: &testPolicy,
	})
	if err != nil {
		t.Fatalf("explicit force recovery: %v", err)
	}
	if forced.Downloaded != 1 || requests != 1 {
		t.Fatalf("explicit force did not recover terminal asset: stats=%+v requests=%d", forced, requests)
	}
	refs, err = st.ListItemMediaRefs(t.Context(), itemID)
	if err != nil || len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusDownloaded || !refs[0].LocalPrunedAt.IsZero() || !strings.HasPrefix(refs[0].LocalPath, "media/mastodon/photo/") {
		t.Fatalf("force-recovered Mastodon ref=%+v err=%v", refs, err)
	}
	items, err := st.ListItemsForXPhotoOCR(t.Context(), 10, false)
	if err != nil || len(items) != 1 || items[0].ID != itemID {
		t.Fatalf("force-recovered photo not OCR eligible: items=%+v err=%v", items, err)
	}
}

func TestRunApplyDownloadsSharedAssetOnlyOnce(t *testing.T) {
	t.Parallel()

	cfg, st := openPrunedMediaRepairTestStore(t)
	firstID := seedCoordinatorRepairItem(t, cfg, st, "x:shared-first", true, false)
	secondID := seedCoordinatorRepairItem(t, cfg, st, "x:shared-second", true, false)
	firstRefs, err := st.ListItemMediaRefs(context.Background(), firstID)
	if err != nil || len(firstRefs) != 1 {
		t.Fatalf("first refs=%+v err=%v", firstRefs, err)
	}
	secondRefs, err := st.ListItemMediaRefs(context.Background(), secondID)
	if err != nil || len(secondRefs) != 1 {
		t.Fatalf("second refs=%+v err=%v", secondRefs, err)
	}
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec(`DELETE FROM item_media_links WHERE item_id = ?`, secondID); err != nil {
		t.Fatalf("delete second link: %v", err)
	}
	if _, err := raw.Exec(`DELETE FROM media_assets WHERE id = ?`, secondRefs[0].MediaAssetID); err != nil {
		t.Fatalf("delete second asset: %v", err)
	}
	nowText := time.Now().UTC().Format(time.RFC3339)
	if _, err := raw.Exec(`INSERT INTO item_media_links (item_id, media_asset_id, ordinal, expanded_url, created_at, updated_at) VALUES (?, ?, 0, '', ?, ?)`, secondID, firstRefs[0].MediaAssetID, nowText, nowText); err != nil {
		t.Fatalf("link shared asset: %v", err)
	}

	var calls []int64
	stats, err := runWithDownloader(context.Background(), cfg, st, Options{Apply: true, OCR: true, Limit: 5000, Timeout: 45 * time.Second}, func(_ context.Context, _ config.Config, _ *store.Store, itemID int64, opts mediadownload.Options) (mediadownload.Stats, error) {
		calls = append(calls, itemID)
		if len(opts.AllowedAssetIDs) != 1 || opts.AllowedAssetIDs[0] != firstRefs[0].MediaAssetID {
			t.Fatalf("unexpected shared asset allowlist: %+v", opts.AllowedAssetIDs)
		}
		return mediadownload.Stats{Candidates: 1, Requested: 1, Downloaded: 1, Changed: 1}, nil
	})
	if err != nil {
		t.Fatalf("runWithDownloader: %v", err)
	}
	if len(calls) != 1 || calls[0] != firstID {
		t.Fatalf("shared asset downloaded more than once or out of order: calls=%v first=%d second=%d", calls, firstID, secondID)
	}
	if stats.OCRCandidates != 2 || stats.ItemsVisited != 1 || stats.MediaDownloaded != 1 {
		t.Fatalf("unexpected shared asset stats: %+v", stats)
	}
}

func TestRunApplyPrefersMastodonOwnerForSharedAssetPolicy(t *testing.T) {
	t.Parallel()

	cfg, st := openPrunedMediaRepairTestStore(t)
	remoteURL := "https://media.mastodon.example/shared-photo.jpg"
	bskyItemID := seedSocialCoordinatorRepairItem(t, st, "bsky:shared-mastodon-policy", "bsky_bookmark", "photo", remoteURL)
	mastodonItemID := seedSocialCoordinatorRepairItem(t, st, "mastodon:shared-mastodon-policy", "mastodon_bookmark", "photo", remoteURL)
	if bskyItemID >= mastodonItemID {
		t.Fatalf("test fixture item ordering = bsky %d, Mastodon %d; want lower-ID Bluesky owner", bskyItemID, mastodonItemID)
	}
	bskyRefs, err := st.ListItemMediaRefs(t.Context(), bskyItemID)
	if err != nil || len(bskyRefs) != 1 {
		t.Fatalf("Bluesky refs=%+v err=%v", bskyRefs, err)
	}
	mastodonRefs, err := st.ListItemMediaRefs(t.Context(), mastodonItemID)
	if err != nil || len(mastodonRefs) != 1 {
		t.Fatalf("Mastodon refs=%+v err=%v", mastodonRefs, err)
	}
	if bskyRefs[0].MediaAssetID != mastodonRefs[0].MediaAssetID {
		t.Fatalf("shared asset IDs = Bluesky %d, Mastodon %d", bskyRefs[0].MediaAssetID, mastodonRefs[0].MediaAssetID)
	}

	calls := 0
	stats, err := runWithDownloader(t.Context(), cfg, st, Options{
		Apply: true, OCR: true, Limit: 5000, Timeout: 45 * time.Second,
	}, func(_ context.Context, _ config.Config, _ *store.Store, itemID int64, opts mediadownload.Options) (mediadownload.Stats, error) {
		calls++
		if itemID != mastodonItemID {
			t.Fatalf("shared asset owner item=%d, want Mastodon item %d", itemID, mastodonItemID)
		}
		if opts.MediaNamespace != "mastodon" || len(opts.AllowedAssetIDs) != 1 || opts.AllowedAssetIDs[0] != mastodonRefs[0].MediaAssetID {
			t.Fatalf("Mastodon shared-asset options = %+v", opts)
		}
		if opts.HTTPPolicy == nil {
			t.Fatal("Mastodon shared-asset repair omitted HTTPPolicy")
		}
		wantPolicy, err := mastodonapi.MediaHTTPPolicy(mastodonRefs[0].RemoteURL, nil)
		if err != nil {
			t.Fatalf("MediaHTTPPolicy: %v", err)
		}
		if !slices.Equal(opts.HTTPPolicy.AllowedOrigins, wantPolicy.AllowedOrigins) ||
			!opts.HTTPPolicy.RejectCredentialQueryOnRedirect || opts.HTTPPolicy.AllowPrivateNetwork ||
			len(opts.HTTPPolicy.AllowedPrivateOrigins) != 0 {
			t.Fatalf("shared-asset Mastodon policy = %+v, want exact public origin", opts.HTTPPolicy)
		}
		return mediadownload.Stats{Candidates: 1, Requested: 1, Downloaded: 1, Changed: 1}, nil
	})
	if err != nil {
		t.Fatalf("runWithDownloader: %v", err)
	}
	if calls != 1 || stats.OCRCandidates != 2 || stats.ItemsVisited != 1 || stats.MediaDownloaded != 1 {
		t.Fatalf("shared Mastodon-preferred repair calls=%d stats=%+v", calls, stats)
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

func seedSocialCoordinatorRepairItem(t *testing.T, st *store.Store, sourceKey, sourceType, mediaType, remoteURL string) int64 {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey: sourceKey, SourceType: sourceType, ExternalID: sourceKey,
		CanonicalURL: "https://social.example/" + sourceKey, Title: sourceKey,
		ContentHash: sourceKey + "-hash", LinksJSON: "[]", NotePath: filepath.Join("items", sourceKey+".md"), RawJSON: "{}",
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{RemoteURL: remoteURL, MediaType: mediaType}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates: %v", err)
	}
	refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListItemMediaRefs: refs=%+v err=%v", refs, err)
	}
	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		LocalPath: "media/original/" + mediaType + "/asset", ContentHash: sourceKey + "-original",
		Status: model.MediaDownloadStatusDownloaded, DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if _, err := st.SaveMediaArchive(ctx, refs[0].MediaAssetID, model.MediaArchiveResult{
		Provider: "s3", Bucket: "dbrain", Key: "media/original/" + mediaType + "/asset",
		Status: model.MediaArchiveStatusArchived, ArchivedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaArchive: %v", err)
	}
	if _, err := st.MarkMediaLocalPrunedByPath(ctx, "media/original/"+mediaType+"/asset", now); err != nil {
		t.Fatalf("MarkMediaLocalPrunedByPath: %v", err)
	}
	return item.ItemID
}

func syntheticMastodonPolicyBase(t *testing.T, server *httptest.Server, host string) safehttp.Policy {
	t.Helper()
	return safehttp.Policy{
		LookupNetIP: func(_ context.Context, _, gotHost string) ([]netip.Addr, error) {
			if gotHost != host {
				return nil, fmt.Errorf("unexpected host %q", gotHost)
			}
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	}
}

func validRepairPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, A: 255})
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatalf("encode repair PNG: %v", err)
	}
	return body.Bytes()
}
