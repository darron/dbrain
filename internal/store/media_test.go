package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestSaveXHydrationPersistsMediaAssetsAndLinks(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-media",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Media post",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/123.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-22T03:04:05Z",
			"snapshot":{
				"id":"123",
				"text":"hello",
				"media_objects":[
					{"type":"photo","url":"https://pbs.twimg.com/media/A.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800},
					{"type":"photo","url":"https://pbs.twimg.com/media/A.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800},
					{"type":"video","url":"https://video.twimg.com/ext/B.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":1920,"height":1080}
				]
			},
			"raw":{}
		}`,
	}
	changed, err := st.SaveXHydration(ctx, item.ItemID, hydration)
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected hydration to change row")
	}

	assets, err := st.ListMediaAssetsForDownload(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListMediaAssetsForDownload: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("expected 2 media assets, got %d", len(assets))
	}
	for _, asset := range assets {
		if asset.DownloadStatus != "pending" {
			t.Fatalf("expected pending download status, got %+v", asset)
		}
		if asset.RemoteURL == "" {
			t.Fatalf("expected remote url to be populated, got %+v", asset)
		}
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT ordinal, expanded_url
		FROM item_media_links
		WHERE item_id = ?
		ORDER BY ordinal ASC`, item.ItemID)
	if err != nil {
		t.Fatalf("query item media links: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var ordinals []int
	var expanded []string
	for rows.Next() {
		var ordinal int
		var expandedURL string
		if err := rows.Scan(&ordinal, &expandedURL); err != nil {
			t.Fatalf("scan item media link: %v", err)
		}
		ordinals = append(ordinals, ordinal)
		expanded = append(expanded, expandedURL)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate item media links: %v", err)
	}

	if len(ordinals) != 2 {
		t.Fatalf("expected 2 deduped media links, got %d", len(ordinals))
	}
	if ordinals[0] != 0 || ordinals[1] != 1 {
		t.Fatalf("unexpected ordinals: %#v", ordinals)
	}
	if !strings.Contains(expanded[0], "/photo/1") || !strings.Contains(expanded[1], "/video/1") {
		t.Fatalf("unexpected expanded urls: %#v", expanded)
	}
}

func TestMediaAssetReusePreservesEstablishedFamilyAndDownloadState(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 9, 3, 4, 5, 0, time.UTC)
	result, err := st.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			remote_url, media_type, mime_type, byte_size, content_hash, download_status,
			local_path, discovered_at, downloaded_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"https://cdn.example/shared", "photo", "image/jpeg", 123, "sha256:photo", "downloaded",
		"media/mastodon/photo/ab/photo.jpg", now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert media asset: %v", err)
	}
	assetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("asset id: %v", err)
	}
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "mastodon:shared-media",
		SourceType:   "mastodon_bookmark",
		ExternalID:   "shared-media",
		CanonicalURL: "https://hachyderm.io/@alice/shared-media",
		Title:        "shared media",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/mastodon/2026/shared-media.md",
		RawJSON:      "{}",
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL:   "https://cdn.example/shared",
		MediaType:   "audio",
		ExpandedURL: "https://hachyderm.io/@alice/shared-media/media/1",
	}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates: %v", err)
	}

	var mediaType, mimeType, status, localPath, contentHash string
	var byteSize int64
	if err := st.db.QueryRowContext(ctx, `SELECT media_type, mime_type, download_status, local_path, content_hash, byte_size FROM media_assets WHERE id = ?`, assetID).Scan(&mediaType, &mimeType, &status, &localPath, &contentHash, &byteSize); err != nil {
		t.Fatalf("load reused media asset: %v", err)
	}
	if mediaType != "photo" || mimeType != "image/jpeg" || status != "downloaded" || localPath == "" || contentHash != "sha256:photo" || byteSize != 123 {
		t.Fatalf("reused media asset was relabeled or reset: type=%q mime=%q status=%q path=%q hash=%q size=%d", mediaType, mimeType, status, localPath, contentHash, byteSize)
	}
}

func TestMergeItemMediaCandidatesRetainsValidLinksFromIncompleteProjection(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 3, 4, 5, 0, time.UTC)
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "mastodon:incomplete-media",
		SourceType:   "mastodon_bookmark",
		ExternalID:   "incomplete-media",
		CanonicalURL: "https://hachyderm.io/@alice/1",
		Title:        "Mixed media",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/mastodon/2026/incomplete-media.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL: "https://cdn.example/known.jpg", MediaType: "photo", ExpandedURL: "https://hachyderm.io/@alice/1/media/1", Width: 640, Height: 480,
	}}); err != nil {
		t.Fatalf("seed media candidates: %v", err)
	}
	changed, err := st.MergeItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL: "https://cdn.example/new.mp4", MediaType: "video", ExpandedURL: "https://hachyderm.io/@alice/1/media/2", Width: 1280, Height: 720,
	}})
	if err != nil {
		t.Fatalf("merge media candidates: %v", err)
	}
	if !changed {
		t.Fatal("expected incomplete projection merge to add a link")
	}
	refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 2 || refs[0].RemoteURL != "https://cdn.example/known.jpg" || refs[1].RemoteURL != "https://cdn.example/new.mp4" {
		t.Fatalf("merged refs = %#v", refs)
	}
}

func TestSaveItemMediaCandidatesReplacesGenericMediaAndInvalidatesDerivedState(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 3, 4, 5, 0, time.UTC)
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "bsky:at://did:plc:one/app.bsky.feed.post/3lq7media",
		SourceType:   "bsky_bookmark",
		ExternalID:   "at://did:plc:one/app.bsky.feed.post/3lq7media",
		CanonicalURL: "https://bsky.app/profile/alice.example/post/3lq7media",
		Title:        "Media post",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/bsky/2026/3lq7media.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL: "https://cdn.example/old.jpg", MediaType: "photo", ExpandedURL: "https://bsky.app/old", Width: 640, Height: 480,
	}}); err != nil {
		t.Fatalf("seed media candidates: %v", err)
	}
	if _, err := st.SaveItemSummary(ctx, item.ItemID, model.SummaryResult{Text: "old summary", Status: "ok", FetchedAt: now}, "summary-hash"); err != nil {
		t.Fatalf("SaveItemSummary: %v", err)
	}
	if _, err := st.SaveItemOCR(ctx, item.ItemID, model.OCRResult{Text: "old OCR", Status: "ok", FetchedAt: now}, "ocr-hash"); err != nil {
		t.Fatalf("SaveItemOCR: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET article_title = ?, article_text = ? WHERE id = ?`, model.XMediaTranscriptArticleTitle, "old transcript", item.ItemID); err != nil {
		t.Fatalf("seed transcript article: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, item.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("SaveXMediaTranscriptionState: %v", err)
	}

	changed, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL: "https://cdn.example/new.jpg", MediaType: "photo", ExpandedURL: "https://bsky.app/new", Width: 1200, Height: 800,
	}})
	if err != nil {
		t.Fatalf("replace media candidates: %v", err)
	}
	if !changed {
		t.Fatal("expected media replacement to report changed")
	}
	refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].RemoteURL != "https://cdn.example/new.jpg" {
		t.Fatalf("media refs = %#v", refs)
	}
	got, err := st.GetItem(ctx, "bsky:at://did:plc:one/app.bsky.feed.post/3lq7media")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.SummaryText != "" || got.OCRText != "" || got.XMediaTranscriptStatus != "" || got.ArticleText != "" {
		t.Fatalf("derived state was not invalidated: %+v", got)
	}
	for _, role := range []string{model.ItemEnrichmentRoleSummary, model.ItemEnrichmentRoleOCR, model.ItemEnrichmentRoleXMediaTranscript} {
		if _, err := st.GetItemEnrichment(ctx, item.ItemID, role); err == nil {
			t.Fatalf("enrichment role %q still exists", role)
		}
	}

	changed, err = st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL: "https://cdn.example/new.jpg", MediaType: "photo", ExpandedURL: "https://bsky.app/new", Width: 1200, Height: 800,
	}})
	if err != nil {
		t.Fatalf("repeat media candidates: %v", err)
	}
	if changed {
		t.Fatal("identical media candidates should be idempotent")
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET article_title = 'Original article', article_text = 'Original body' WHERE id = ?`, item.ItemID); err != nil {
		t.Fatalf("seed unrelated article: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{{
		RemoteURL: "https://cdn.example/final.jpg", MediaType: "photo", ExpandedURL: "https://bsky.app/final", Width: 1200, Height: 800,
	}}); err != nil {
		t.Fatalf("replace media with unrelated article: %v", err)
	}
	got, err = st.GetItem(ctx, "bsky:at://did:plc:one/app.bsky.feed.post/3lq7media")
	if err != nil {
		t.Fatalf("GetItem after unrelated article replacement: %v", err)
	}
	if got.ArticleTitle != "Original article" || got.ArticleText != "Original body" {
		t.Fatalf("unrelated article was cleared: title=%q text=%q", got.ArticleTitle, got.ArticleText)
	}
}

func TestSaveXHydrationPersistsSnapshotLinks(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 27, 3, 4, 5, 0, time.UTC)

	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-links",
		SourceType:   "x_bookmark",
		ExternalID:   "2048567034506838416",
		CanonicalURL: "https://x.com/example/status/2048567034506838416",
		Title:        "Long note post",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2048567034506838416.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "Please read https://t.co/example",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-27T03:04:05Z",
			"snapshot":{
				"id":"2048567034506838416",
				"text":"Please read https://t.co/example",
				"links":[
					"https://example.com/note",
					"https://github.com/example/repo",
					"https://example.com/note"
				]
			},
			"raw":{}
		}`,
	}
	changed, err := st.SaveXHydration(ctx, item.ItemID, hydration)
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected snapshot links to change row")
	}

	refreshed, err := st.GetItem(ctx, "x:test-links")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got, want := refreshed.LinksJSON, `["https://example.com/note","https://github.com/example/repo"]`; got != want {
		t.Fatalf("links_json = %q, want %q", got, want)
	}
	if got, want := refreshed.PrimaryDomain, "example.com"; got != want {
		t.Fatalf("primary_domain = %q, want %q", got, want)
	}
	if got, want := refreshed.Domains, "example.com,github.com"; got != want {
		t.Fatalf("domains = %q, want %q", got, want)
	}
	if got, want := refreshed.GitHubURLs, "https://github.com/example/repo"; got != want {
		t.Fatalf("github_urls = %q, want %q", got, want)
	}
	if !refreshed.LinkExtractSyncedAt.IsZero() {
		t.Fatalf("expected link extraction marker to be cleared, got %s", refreshed.LinkExtractSyncedAt)
	}
}

func TestSaveMediaDownloadRemovesCompletedAssetFromPendingList(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	result, err := st.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			remote_url, media_type, download_status, discovered_at, updated_at
		) VALUES (?, ?, ?, ?, ?)`,
		"https://pbs.twimg.com/media/asset.jpg",
		"photo",
		"pending",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert media asset: %v", err)
	}
	assetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("asset id: %v", err)
	}

	changed, err := st.SaveMediaDownload(ctx, assetID, model.MediaDownloadResult{
		MIMEType:     "image/jpeg",
		ByteSize:     12345,
		ContentHash:  "sha256:abc",
		LocalPath:    "media/x/photo/ab/asset.jpg",
		Status:       "downloaded",
		DownloadedAt: now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if !changed {
		t.Fatal("expected download update to change row")
	}

	assets, err := st.ListMediaAssetsForDownload(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListMediaAssetsForDownload: %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("expected downloaded asset to be absent from pending list, got %+v", assets)
	}

	forceAssets, err := st.ListMediaAssetsForDownload(ctx, 10, true)
	if err != nil {
		t.Fatalf("ListMediaAssetsForDownload(force): %v", err)
	}
	if len(forceAssets) != 1 {
		t.Fatalf("expected force list to return downloaded asset, got %d", len(forceAssets))
	}
	if forceAssets[0].LocalPath != "media/x/photo/ab/asset.jpg" {
		t.Fatalf("unexpected local path: %+v", forceAssets[0])
	}
}

func TestListMediaAssetsForDownloadBacksOffRecentErrors(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	oldAttempt := now.Add(-model.MediaDownloadRetryCooldown - time.Minute).Format(time.RFC3339)
	recentAttempt := now.Add(-time.Hour).Format(time.RFC3339)

	rows := []struct {
		url       string
		status    string
		errCount  int
		attemptAt string
	}{
		{"https://video.twimg.com/ext/pending.mp4", "pending", 0, ""},
		{"https://video.twimg.com/ext/old-error.mp4", "error", 1, oldAttempt},
		{"https://video.twimg.com/ext/recent-error.mp4", "error", 1, recentAttempt},
		{"https://video.twimg.com/ext/blocked.mp4", "blocked", model.MediaDownloadMaxConsecutiveErrors, recentAttempt},
	}
	for _, row := range rows {
		if _, err := st.db.ExecContext(ctx, `
			INSERT INTO media_assets (
				remote_url, media_type, download_status, download_error_count,
				last_download_attempt_at, discovered_at, updated_at
			) VALUES (?, 'video', ?, ?, ?, ?, ?)`,
			row.url,
			row.status,
			row.errCount,
			row.attemptAt,
			now.Format(time.RFC3339),
			now.Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert %s: %v", row.url, err)
		}
	}

	assets, err := st.ListMediaAssetsForDownload(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListMediaAssetsForDownload: %v", err)
	}
	var urls []string
	for _, asset := range assets {
		urls = append(urls, asset.RemoteURL)
	}
	want := []string{
		"https://video.twimg.com/ext/pending.mp4",
		"https://video.twimg.com/ext/old-error.mp4",
	}
	if !slices.Equal(urls, want) {
		t.Fatalf("expected retryable assets %v, got %v", want, urls)
	}

	forced, err := st.ListMediaAssetsForDownload(ctx, 10, true)
	if err != nil {
		t.Fatalf("ListMediaAssetsForDownload(force): %v", err)
	}
	if len(forced) != len(rows) {
		t.Fatalf("expected force to include all assets, got %d", len(forced))
	}
}

func TestSaveMediaDownloadClearsLocalPrunedAtOnRestore(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 20, 0, 0, 0, time.UTC)

	result, err := st.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			remote_url, media_type, download_status, local_path, downloaded_at, local_pruned_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"https://video.twimg.com/ext_tw_video/restored.mp4",
		"video",
		"downloaded",
		"media/x/video/ab/restored.mp4",
		now.Add(-time.Hour).Format(time.RFC3339),
		now.Add(-30*time.Minute).Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert media asset: %v", err)
	}
	assetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("asset id: %v", err)
	}

	changed, err := st.SaveMediaDownload(ctx, assetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     45678,
		ContentHash:  "sha256:restored",
		LocalPath:    "media/x/video/ab/restored.mp4",
		Status:       "downloaded",
		DownloadedAt: now,
	})
	if err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if !changed {
		t.Fatal("expected restore update to change row")
	}

	asset, err := st.GetMediaAsset(ctx, assetID)
	if err != nil {
		t.Fatalf("GetMediaAsset: %v", err)
	}
	if !asset.LocalPrunedAt.IsZero() {
		t.Fatalf("expected restore to clear local_pruned_at, got %v", asset.LocalPrunedAt)
	}
	if asset.LocalPath != "media/x/video/ab/restored.mp4" {
		t.Fatalf("unexpected local path after restore: %+v", asset)
	}
}

func TestSaveMediaDownloadBlocksAfterRepeatedErrors(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 5, 14, 1, 0, time.UTC)

	result, err := st.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			remote_url, media_type, download_status, download_error_count,
			last_download_attempt_at, discovered_at, updated_at
		) VALUES (?, 'video', 'error', ?, ?, ?, ?)`,
		"https://video.twimg.com/ext/flaky.mp4",
		model.MediaDownloadMaxConsecutiveErrors-1,
		now.Add(-model.MediaDownloadRetryCooldown-time.Minute).Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert media asset: %v", err)
	}
	assetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("asset id: %v", err)
	}

	changed, err := st.SaveMediaDownload(ctx, assetID, model.MediaDownloadResult{
		Status:      "error",
		Error:       "context deadline exceeded",
		AttemptedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if !changed {
		t.Fatal("expected terminal error update to change row")
	}

	asset, err := st.GetMediaAsset(ctx, assetID)
	if err != nil {
		t.Fatalf("GetMediaAsset: %v", err)
	}
	if asset.DownloadStatus != "blocked" {
		t.Fatalf("expected blocked media asset, got %+v", asset)
	}
	if asset.DownloadErrors != model.MediaDownloadMaxConsecutiveErrors {
		t.Fatalf("expected %d download errors, got %+v", model.MediaDownloadMaxConsecutiveErrors, asset)
	}
	if !strings.Contains(asset.DownloadError, "blocked after 3 failed media download attempts") {
		t.Fatalf("expected terminal error message, got %q", asset.DownloadError)
	}
}

func TestSaveXHydrationBackfillsMediaWithoutChangingHydrationFields(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:cached-media", "", "", now)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET x_post_text = ?,
			x_post_lang = ?,
			x_post_json = ?,
			x_post_fetched_at = ?,
			x_post_status = ?,
			x_post_error = ''
		WHERE id = ?`,
		"hello",
		"en",
		`{"snapshot":{"media_objects":[{"type":"photo","url":"https://pbs.twimg.com/media/backfill.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
		now.Format(time.RFC3339),
		"ok_graphql",
		itemID,
	); err != nil {
		t.Fatalf("seed hydration: %v", err)
	}

	changed, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"https://pbs.twimg.com/media/backfill.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
		FetchedAt: now,
		Status:    "ok_graphql",
	})
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected media backfill to count as change")
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].RemoteURL != "https://pbs.twimg.com/media/backfill.jpg" {
		t.Fatalf("unexpected media ref: %+v", refs[0])
	}
}

func TestSaveXHydrationInvalidatesLinkedXArticleSources(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 24, 17, 0, 0, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:article-link", "", "", now)
	sourceID := insertTestSource(t, st, "src:x-article-link", "https://x.com/example/article/2028710814601908224")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = 'x_article',
			extracted_text = 'old extract',
			extract_status = 'ok',
			extract_error = 'old error',
			extract_failure_kind = 'connectivity',
			extract_failure_count = 3,
			extract_first_failed_at = ?,
			extract_last_failed_at = ?,
			extracted_at = ?,
			extract_tool = 'summarize',
			extract_tool_version = '0.13.0',
			summary_text = 'old summary',
			summary_json = '{"ok":true}',
			summary_status = 'ok',
			summary_error = '',
			summary_model = 'cli/codex/gpt-5.2',
			summary_content_hash = 'old-hash',
			summary_prompt_version = 'dbrain-v1',
			summary_tool = 'summarize',
			summary_tool_version = '0.13.0',
			summarized_at = ?,
			content_hash = 'old-content-hash',
			updated_at = ?
		WHERE id = ?`,
		now.Add(-2*time.Hour).Format(time.RFC3339),
		now.Add(-time.Hour).Format(time.RFC3339),
		now.Add(-2*time.Hour).Format(time.RFC3339),
		now.Add(-90*time.Minute).Format(time.RFC3339),
		now.Add(-time.Hour).Format(time.RFC3339),
		sourceID,
	); err != nil {
		t.Fatalf("seed x article source: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID,
		sourceID,
		"https://x.com/i/article/2028710814601908224",
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source link: %v", err)
	}

	changed, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "tweet text",
		Language:  "en",
		APIJSON:   `{"raw":{"data":{"tweetResult":{"result":{"article":{"article_results":{"result":{"title":"Evals Skills for Coding Agents","rest_id":"2028710814601908224","plain_text":"Full article body"}}}}}}}}`,
		FetchedAt: now,
		Status:    "ok_graphql",
	})
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected hydration change")
	}

	source, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != "" {
		t.Fatalf("expected linked x article source to be re-queued, got %q", source.ExtractStatus)
	}
	if source.ExtractError != "" || source.ExtractFailureKind != "" || source.ExtractFailureCount != 0 {
		t.Fatalf("expected linked x article failure state reset, got error=%q kind=%q count=%d", source.ExtractError, source.ExtractFailureKind, source.ExtractFailureCount)
	}
	if source.ExtractedText != "" || source.ExtractTool != "" || source.ExtractToolVersion != "" {
		t.Fatalf("expected stale extract cleared, got text=%q tool=%q tool_version=%q", source.ExtractedText, source.ExtractTool, source.ExtractToolVersion)
	}
	if source.SummaryText != "" || source.SummaryStatus != "" || source.SummaryTool != "" {
		t.Fatalf("expected stale summary cleared, got summary_text=%q status=%q tool=%q", source.SummaryText, source.SummaryStatus, source.SummaryTool)
	}
}

func TestSaveXHydrationPreservesDirectQuotedHydrationOverSnapshotOnlyUpdate(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 1, 30, 0, 0, time.UTC)

	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:quoted-direct",
		SourceType:   "x_quote",
		ExternalID:   "2040464914855100670",
		CanonicalURL: "https://x.com/example/status/2040464914855100670",
		Title:        "Quoted child",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/quoted-direct.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	directHydration := model.XHydration{
		FullText:  "Direct child full text",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-26T01:30:00Z",
			"snapshot":{"id":"2040464914855100670","text":"Direct child full text"},
			"raw":{"data":{"tweetResult":{"result":{"rest_id":"2040464914855100670","legacy":{"full_text":"Direct child full text"}}}}}
		}`,
	}
	changed, err := st.SaveXHydration(ctx, item.ItemID, directHydration)
	if err != nil {
		t.Fatalf("SaveXHydration direct: %v", err)
	}
	if !changed {
		t.Fatal("expected direct hydration to change row")
	}

	snapshotOnlyHydration := model.XHydration{
		FullText:  "Snapshot preview text",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now.Add(time.Minute),
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-26T01:31:00Z",
			"snapshot":{"id":"2040464914855100670","text":"Snapshot preview text"},
			"raw":{"__typename":"Tweet","rest_id":"2040464914855100670","legacy":{"full_text":"Snapshot preview text"}}
		}`,
	}
	changed, err = st.SaveXHydration(ctx, item.ItemID, snapshotOnlyHydration)
	if err != nil {
		t.Fatalf("SaveXHydration snapshot-only: %v", err)
	}
	if changed {
		t.Fatal("expected snapshot-only hydration to preserve direct quoted hydration")
	}

	refreshed, err := st.GetItem(ctx, "x:quoted-direct")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if refreshed.XPostText != "Direct child full text" {
		t.Fatalf("expected direct hydration text to be preserved, got %q", refreshed.XPostText)
	}
	if !strings.Contains(refreshed.XPostJSON, `"tweetResult"`) {
		t.Fatalf("expected direct graphql payload to be preserved, got %q", refreshed.XPostJSON)
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected preserved direct quote hydration to stay out of repair backlog, got %#v", items)
	}
}

func TestSaveXHydrationBackfillsVideoMediaFromRawPayload(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:cached-video", "", "", now)
	hydration := model.XHydration{
		FullText:  "video post",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://pbs.twimg.com/amplify_video_thumb/123/img/thumb.jpg","expanded_url":"https://x.com/example/status/123/video/1","width":1920,"height":1080}
				]
			},
			"raw":{
				"data":{
					"tweetResult":{
						"result":{
							"legacy":{
								"extended_entities":{
									"media":[
										{
											"type":"video",
											"expanded_url":"https://x.com/example/status/123/video/1",
											"media_url_https":"https://pbs.twimg.com/amplify_video_thumb/123/img/thumb.jpg",
											"original_info":{"width":1920,"height":1080},
											"video_info":{
												"variants":[
													{"content_type":"application/x-mpegURL","url":"https://video.twimg.com/ext/playlist.m3u8"},
													{"bitrate":832000,"content_type":"video/mp4","url":"https://video.twimg.com/ext/low.mp4"},
													{"bitrate":4320000,"content_type":"video/mp4","url":"https://video.twimg.com/ext/3840x2160/high.mp4"}
												]
											}
										}
									]
								}
							}
						}
					}
				}
			}
		}`,
	}
	changed, err := st.SaveXHydration(ctx, itemID, hydration)
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected hydration to change row")
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].MediaType != "video" {
		t.Fatalf("expected video media ref, got %+v", refs[0])
	}
	if refs[0].RemoteURL != "https://video.twimg.com/ext/3840x2160/high.mp4" {
		t.Fatalf("expected playable video url, got %+v", refs[0])
	}
}

func TestListItemsForXHydrationIncludesCachedMediaPendingDownloads(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:pending-media", "", "", now)
	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"https://pbs.twimg.com/media/pending.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	}
	changed, err := st.SaveXHydration(ctx, itemID, hydration)
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected initial hydration change")
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected cached pending-media item to be selected, got %#v", items)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one pending media ref, got %#v", refs)
	}

	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "image/jpeg",
		ByteSize:     123,
		ContentHash:  "sha256:done",
		LocalPath:    "media/x/photo/do/done.jpg",
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	items, err = st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration after download: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected completed media item to drop out of hydration queue, got %#v", items)
	}
}

func TestListItemsForXHydrationSkipsMediaInRetryBackoffOrBlocked(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	recentAttempt := now.Add(-time.Hour).Format(time.RFC3339)

	recentErrorID := insertTestItem(t, st, "x:recent-media-error", "", "", now)
	if _, err := st.SaveXHydration(ctx, recentErrorID, model.XHydration{
		FullText:  "recent error",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"https://video.twimg.com/ext/recent-error.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":1920,"height":1080}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration recent: %v", err)
	}
	blockedID := insertTestItem(t, st, "x:blocked-media-error", "", "", now)
	if _, err := st.SaveXHydration(ctx, blockedID, model.XHydration{
		FullText:  "blocked",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"https://video.twimg.com/ext/blocked.mp4","expanded_url":"https://x.com/example/status/456/video/1","width":1920,"height":1080}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration blocked: %v", err)
	}

	for _, row := range []struct {
		itemID   int64
		status   string
		errCount int
	}{
		{recentErrorID, "error", 1},
		{blockedID, "blocked", model.MediaDownloadMaxConsecutiveErrors},
	} {
		if _, err := st.db.ExecContext(ctx, `
			UPDATE media_assets
			SET download_status = ?,
				download_error_count = ?,
				last_download_attempt_at = ?
			WHERE id IN (
				SELECT media_asset_id
				FROM item_media_links
				WHERE item_id = ?
			)`,
			row.status,
			row.errCount,
			recentAttempt,
			row.itemID,
		); err != nil {
			t.Fatalf("update media status for %d: %v", row.itemID, err)
		}
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected recent/blocked media to stay out of hydration queue, got %#v", items)
	}
}

func TestListItemsForXHydrationIncludesDownloadedVideoThumbsForRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:video-thumb-repair", "", "", now)
	hydration := model.XHydration{
		FullText:  "video post",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://pbs.twimg.com/amplify_video_thumb/123/img/thumb.jpg","expanded_url":"https://x.com/example/status/123/video/1","width":1920,"height":1080}
				]
			},
			"raw":{
				"data":{
					"tweetResult":{
						"result":{
							"legacy":{
								"extended_entities":{
									"media":[
										{
											"type":"video",
											"expanded_url":"https://x.com/example/status/123/video/1",
											"media_url_https":"https://pbs.twimg.com/amplify_video_thumb/123/img/thumb.jpg",
											"original_info":{"width":1920,"height":1080},
											"video_info":{
												"variants":[
													{"bitrate":832000,"content_type":"video/mp4","url":"https://video.twimg.com/ext/real.mp4"}
												]
											}
										}
									]
								}
							}
						}
					}
				}
			}
		}`,
	}
	if _, err := st.SaveXHydration(ctx, itemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "image/jpeg",
		ByteSize:     123,
		ContentHash:  "sha256:thumb",
		LocalPath:    "media/x/video/ab/thumb.jpg",
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 1 || items[0].ID != itemID {
		t.Fatalf("expected downloaded video thumb item to be selected for repair, got %#v", items)
	}
}

func TestXHydrationCandidateQueryLooksUpDownloadedMediaByItemFirst(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()

	rows, err := st.db.QueryContext(ctx, `
		EXPLAIN QUERY PLAN
		SELECT id
		FROM items
		WHERE `+xItemSourceTypeWhere+`
			AND external_id != ''
			AND `+xHydrationCandidateWhere+`
		ORDER BY
			CASE WHEN x_post_status = '' THEN 0 ELSE 1 END,
			last_seen_at DESC,
			x_post_fetched_at ASC,
			id DESC
		LIMIT 100`)
	if err != nil {
		t.Fatalf("explain x hydration candidates: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}

	joined := strings.Join(plan, "\n")
	if strings.Contains(joined, "idx_media_assets_download_retry") {
		t.Fatalf("downloaded media repair should not scan media_assets by status for each item:\n%s", joined)
	}
}

func TestListItemsForXHydrationIncludesQuotedPostBackfillRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:quoted-backfill", "", "", now)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET x_post_text = ?,
			x_post_status = ?,
			x_post_fetched_at = ?,
			x_post_json = ?
		WHERE id = ?`,
		"Oh this is delicious...",
		"ok_syndication",
		now.Format(time.RFC3339),
		`{
			"source":"syndication",
			"snapshot":{"id":"2030852374739198197","text":"Oh this is delicious..."},
			"raw":{
				"id_str":"2030852374739198197",
				"text":"Oh this is delicious...",
				"quoted_tweet":{
					"id_str":"2030838203549184127",
					"text":"Quoted context that should become a linked x quote item."
				}
			}
		}`,
		itemID,
	); err != nil {
		t.Fatalf("seed quoted hydration: %v", err)
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 1 || items[0].ID != itemID {
		t.Fatalf("expected quoted-backfill item to be selected for repair, got %#v", items)
	}
}

func TestListItemsForXHydrationIncludesQuotedSnapshotDirectFetchRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 3, 4, 5, 0, time.UTC)

	item, err := st.UpsertItem(ctx, testItem("x:quoted-snapshot-repair", "x_quote", "https://x.com/example/status/2040448463540830705", now))
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET external_id = ?,
			x_post_text = ?,
			x_post_status = ?,
			x_post_fetched_at = ?,
			x_post_json = ?
		WHERE id = ?`,
		"2040448463540830705",
		"https://t.co/example",
		"ok_graphql",
		now.Format(time.RFC3339),
		`{
			"source":"graphql",
			"fetched_at":"2026-04-25T03:04:05Z",
			"snapshot":{"id":"2040448463540830705","text":"https://t.co/example"},
			"raw":{"__typename":"Tweet","rest_id":"2040448463540830705","legacy":{"full_text":"https://t.co/example"}}
		}`,
		item.ItemID,
	); err != nil {
		t.Fatalf("seed quoted snapshot hydration: %v", err)
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ItemID {
		t.Fatalf("expected quoted snapshot item to be selected for direct hydration repair, got %#v", items)
	}
}

func TestListItemsForXHydrationIncludesNoteTweetLinkRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 27, 3, 4, 5, 0, time.UTC)

	missingLinkID := insertTestItem(t, st, "x:note-link-repair", "", "", now)
	currentLinkID := insertTestItem(t, st, "x:note-link-current", "", "", now)
	hydrationJSON := `{
		"source":"graphql",
		"fetched_at":"2026-04-27T03:04:05Z",
		"snapshot":{"id":"2048567034506838416","text":"Please read https://t.co/example"},
		"raw":{
			"data":{
				"tweetResult":{
					"result":{
						"rest_id":"2048567034506838416",
						"note_tweet":{
							"note_tweet_results":{
								"result":{
									"text":"Please read https://t.co/example",
									"entity_set":{
										"urls":[{
											"url":"https://t.co/example",
											"expanded_url":"https://example.com/note",
											"display_url":"example.com/note"
										}]
									}
								}
							}
						},
						"legacy":{
							"id_str":"2048567034506838416",
							"full_text":"Please read",
							"created_at":"Mon Apr 27 00:00:00 +0000 2026",
							"entities":{"urls":[]}
						}
					}
				}
			}
		}
	}`
	for _, itemID := range []int64{missingLinkID, currentLinkID} {
		if _, err := st.db.ExecContext(ctx, `
			UPDATE items
			SET external_id = ?,
				x_post_text = ?,
				x_post_status = ?,
				x_post_fetched_at = ?,
				x_post_json = ?
			WHERE id = ?`,
			"2048567034506838416",
			"Please read https://t.co/example",
			"ok_graphql",
			now.Format(time.RFC3339),
			hydrationJSON,
			itemID,
		); err != nil {
			t.Fatalf("seed note tweet hydration: %v", err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET links_json = ? WHERE id = ?`, `["https://example.com/note"]`, currentLinkID); err != nil {
		t.Fatalf("seed current link: %v", err)
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 1 || items[0].ID != missingLinkID {
		t.Fatalf("expected only missing note link item to be selected for repair, got %#v", items)
	}
}

func TestListItemsForXHydrationIgnoresNestedQuotedMediaNoise(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 3, 4, 5, 0, time.UTC)

	parent, err := st.UpsertItem(ctx, testItem("x:quoted-parent-media-noise", "x_quote", "https://x.com/example/status/2048185135015870852", now))
	if err != nil {
		t.Fatalf("UpsertItem parent: %v", err)
	}
	child, err := st.UpsertItem(ctx, testItem("x:quoted-child-media", "x_quote", "https://x.com/example/status/2047941621358928157", now))
	if err != nil {
		t.Fatalf("UpsertItem child: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET external_id = ?,
			x_post_text = ?,
			x_post_status = ?,
			x_post_fetched_at = ?,
			x_post_json = ?
		WHERE id = ?`,
		"2047941621358928157",
		"quoted child text",
		"ok_graphql",
		now.Format(time.RFC3339),
		`{
			"source":"graphql",
			"fetched_at":"2026-04-25T03:04:05Z",
			"snapshot":{"id":"2047941621358928157","text":"quoted child text"},
			"raw":{"data":{"tweetResult":{"result":{"rest_id":"2047941621358928157","legacy":{"full_text":"quoted child text"}}}}}
		}`,
		child.ItemID,
	); err != nil {
		t.Fatalf("seed child hydration: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET external_id = ?,
			x_post_text = ?,
			x_post_status = ?,
			x_post_fetched_at = ?,
			x_post_json = ?
		WHERE id = ?`,
		"2048185135015870852",
		"parent quote text",
		"ok_graphql",
		now.Format(time.RFC3339),
		`{
			"source":"graphql",
			"fetched_at":"2026-04-25T03:04:05Z",
			"snapshot":{
				"id":"2048185135015870852",
				"text":"parent quote text",
				"quoted_post":{
					"id":"2047941621358928157",
					"text":"quoted child text",
					"media_objects":[
						{"type":"photo","url":"https://pbs.twimg.com/media/noise.jpg","expanded_url":"https://x.com/example/status/2047941621358928157/photo/1","width":1200,"height":800}
					]
				}
			},
			"raw":{
				"data":{
					"tweetResult":{
						"result":{
							"rest_id":"2048185135015870852",
							"legacy":{"full_text":"parent quote text"},
							"quoted_status_result":{"result":{"rest_id":"2047941621358928157"}}
						}
					}
				}
			}
		}`,
		parent.ItemID,
	); err != nil {
		t.Fatalf("seed parent hydration: %v", err)
	}
	if changed, err := st.ReplaceItemChildLinks(ctx, parent.ItemID, "quoted_post", []int64{child.ItemID}); err != nil {
		t.Fatalf("ReplaceItemChildLinks: %v", err)
	} else if !changed {
		t.Fatal("expected quoted child link to be created")
	}

	items, err := st.ListItemsForXHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXHydration: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected generic hydration query to ignore nested media noise, got %#v", items)
	}

	quoteItems, err := st.ListItemsForXQuoteHydration(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXQuoteHydration: %v", err)
	}
	if len(quoteItems) != 0 {
		t.Fatalf("expected quote-only hydration query to ignore nested media noise, got %#v", quoteItems)
	}
}

func TestListMediaAssetsForArchiveRequiresTerminalCoverage(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 20, 0, 0, 0, time.UTC)

	photoReadyID := insertTestItem(t, st, "x:photo-ready", "", "", now)
	photoPendingID := insertTestItem(t, st, "x:photo-pending", "", "", now)
	videoReadyID := insertTestItem(t, st, "x:video-ready", "", "", now)
	videoPendingID := insertTestItem(t, st, "x:video-pending", "", "", now)
	audioReadyID := insertTestItem(t, st, "mastodon:audio-ready", "", "", now)

	if _, err := st.db.ExecContext(ctx, `UPDATE items SET ocr_status = 'ok', ocr_text = 'photo text', ocr_at = ? WHERE id = ?`, now.Format(time.RFC3339), photoReadyID); err != nil {
		t.Fatalf("seed ready photo ocr: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET x_media_transcript_status = 'no_audio', x_media_transcript_at = ? WHERE id = ?`, now.Format(time.RFC3339), videoReadyID); err != nil {
		t.Fatalf("seed ready video transcript: %v", err)
	}

	insertDownloadedAssetLink(t, st, photoReadyID, "https://example.com/photo-ready.jpg", "photo", "media/x/photo/ab/photo-ready.jpg", now)
	insertDownloadedAssetLink(t, st, photoPendingID, "https://example.com/photo-pending.jpg", "photo", "media/x/photo/ab/photo-pending.jpg", now)
	insertDownloadedAssetLink(t, st, videoReadyID, "https://example.com/video-ready.mp4", "video", "media/x/video/ab/video-ready.mp4", now)
	insertDownloadedAssetLink(t, st, videoPendingID, "https://example.com/video-pending.mp4", "video", "media/x/video/ab/video-pending.mp4", now)
	insertDownloadedAssetLink(t, st, audioReadyID, "https://example.com/audio-ready.mp3", "audio", "media/mastodon/audio/ab/audio-ready.mp3", now)

	assets, err := st.ListMediaAssetsForArchive(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListMediaAssetsForArchive: %v", err)
	}
	got := make([]string, 0, len(assets))
	for _, asset := range assets {
		got = append(got, asset.RemoteURL)
	}
	slices.Sort(got)
	want := []string{
		"https://example.com/audio-ready.mp3",
		"https://example.com/photo-ready.jpg",
		"https://example.com/video-ready.mp4",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected archive candidates: got=%#v want=%#v", got, want)
	}
}

func insertDownloadedAssetLink(t *testing.T, st *Store, itemID int64, remoteURL string, mediaType string, localPath string, now time.Time) {
	t.Helper()

	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO media_assets (
			remote_url, media_type, mime_type, byte_size, content_hash, download_status, local_path, discovered_at, downloaded_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'downloaded', ?, ?, ?, ?)`,
		remoteURL,
		mediaType,
		"application/octet-stream",
		123,
		"sha256:"+strings.TrimPrefix(remoteURL, "https://"),
		localPath,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert media asset %s: %v", remoteURL, err)
	}
	assetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("asset id %s: %v", remoteURL, err)
	}
	if _, err := st.db.ExecContext(context.Background(), `
		INSERT INTO item_media_links (item_id, media_asset_id, ordinal, expanded_url, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?, ?)`,
		itemID,
		assetID,
		remoteURL,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert item media link %s: %v", remoteURL, err)
	}
}
