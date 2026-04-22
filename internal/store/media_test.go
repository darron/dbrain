package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"dbrain/internal/model"
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
