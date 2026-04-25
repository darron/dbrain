package store

import (
	"context"
	"slices"
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
	if refs[0].RemoteURL != "https://video.twimg.com/ext/real.mp4" {
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

func TestListMediaAssetsForArchiveRequiresTerminalCoverage(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 20, 0, 0, 0, time.UTC)

	photoReadyID := insertTestItem(t, st, "x:photo-ready", "", "", now)
	photoPendingID := insertTestItem(t, st, "x:photo-pending", "", "", now)
	videoReadyID := insertTestItem(t, st, "x:video-ready", "", "", now)
	videoPendingID := insertTestItem(t, st, "x:video-pending", "", "", now)

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
