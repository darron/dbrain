package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

func TestRunBookmarksImportsSupportedPostsSkipsBlockedAndRerunsSafely(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	firstPage := bookmarkPage{
		Bookmarks: []bookmarkView{
			{
				CreatedAt: "2026-08-08T18:00:00Z",
				Subject:   bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7example", CID: "bafyreiexample"},
				Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7example",
  "cid": "bafyreiexample",
  "author": {"did": "did:plc:one", "handle": "alice.example", "displayName": "Alice"},
  "record": {"text": "A saved post", "createdAt": "2026-08-07T17:00:00Z"},
  "indexedAt": "2026-08-07T17:01:00Z"
}`),
			},
			{
				Subject: bookmarkSubject{URI: "at://did:plc:two/app.bsky.feed.post/3lq7blocked", CID: "bafyreiblocked"},
				Item:    json.RawMessage(`{"$type":"app.bsky.feed.defs#blockedPost"}`),
			},
		},
		Cursor: "page-two",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "page-two" {
			_ = json.NewEncoder(w).Encode(bookmarkPage{})
			return
		}
		_ = json.NewEncoder(w).Encode(firstPage)
	}))
	defer server.Close()

	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	first, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{PageSize: 100})
	if err != nil {
		t.Fatalf("first runBookmarks: %v", err)
	}
	if first.Created != 1 || first.Skipped != 1 || first.SkippedBlocked != 1 || first.PagesFetched != 2 {
		t.Fatalf("first stats = %+v", first)
	}
	if first.StoppedReason != "end of bookmarks" {
		t.Fatalf("first stop reason = %q", first.StoppedReason)
	}

	item, err := st.GetItem(context.Background(), "bsky:at://did:plc:one/app.bsky.feed.post/3lq7example")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.SourceType != "bsky_bookmark" || !strings.Contains(item.RawJSON, "A saved post") {
		t.Fatalf("imported item = %+v", item)
	}
	if _, err := os.Stat(filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))); err != nil {
		t.Fatalf("rendered note %q: %v", item.NotePath, err)
	}

	second, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{PageSize: 100})
	if err != nil {
		t.Fatalf("second runBookmarks: %v", err)
	}
	if second.Created != 0 || second.Unchanged != 1 || second.Skipped != 1 || second.SkippedBlocked != 1 {
		t.Fatalf("second stats = %+v", second)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want four full cursor traversals", requests)
	}
}

func TestRunBookmarksStopsAtMaxPages(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	page := bookmarkPage{
		Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7blocked"},
			Item:    json.RawMessage(`{"$type":"app.bsky.feed.defs#blockedPost"}`),
		}},
		Cursor: "next",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}

	stats, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{MaxPages: 1})
	if err != nil {
		t.Fatalf("runBookmarks: %v", err)
	}
	if stats.StoppedReason != "max pages reached" || requests != 1 {
		t.Fatalf("stats = %+v, requests = %d", stats, requests)
	}
}

func TestRunBookmarksFollowsContinuationAfterEmptyPage(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_ = json.NewEncoder(w).Encode(bookmarkPage{Cursor: "after-empty"})
		case "after-empty":
			_ = json.NewEncoder(w).Encode(bookmarkPage{})
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
			_ = json.NewEncoder(w).Encode(bookmarkPage{})
		}
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}

	stats, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{PageSize: 100})
	if err != nil {
		t.Fatalf("runBookmarks: %v", err)
	}
	if stats.PagesFetched != 2 || stats.Seen != 0 || stats.StoppedReason != "end of bookmarks" || requests != 2 {
		t.Fatalf("stats = %+v, requests = %d", stats, requests)
	}
}

func TestRunBookmarksPersistsAndDownloadsTextlessImageBookmark(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	imageHits := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/image.jpg" {
			imageHits++
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("image-bytes"))
			return
		}
		_ = json.NewEncoder(w).Encode(bookmarkPage{Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7image"},
			Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7image",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "", "createdAt": "2026-08-07T17:00:00Z"},
  "embed": {"$type": "app.bsky.embed.images#view", "images": [{"fullsize": "` + server.URL + `/image.jpg", "aspectRatio": {"width": 1200, "height": 800}}]}
}`),
		}}})
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	policy := &safehttp.Policy{AllowPrivateNetwork: true}
	first, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{MediaHTTPPolicy: policy})
	if err != nil {
		t.Fatalf("first runBookmarks: %v", err)
	}
	if first.Processed != 1 || first.Created != 1 || first.MediaDiscovered != 1 || first.MediaLinked != 1 || first.MediaDownloaded != 1 || first.MediaUnavailable != 0 {
		t.Fatalf("first stats = %+v", first)
	}
	item, err := st.GetItem(context.Background(), "bsky:at://did:plc:one/app.bsky.feed.post/3lq7image")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusDownloaded || !strings.HasPrefix(refs[0].LocalPath, "media/bsky/photo/") {
		t.Fatalf("downloaded refs = %#v", refs)
	}
	second, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{MediaHTTPPolicy: policy})
	if err != nil {
		t.Fatalf("second runBookmarks: %v", err)
	}
	if second.Unchanged != 1 || second.MediaDiscovered != 1 || second.MediaLinked != 0 || second.MediaDownloaded != 0 || imageHits != 1 {
		t.Fatalf("second stats = %+v, image hits=%d", second, imageHits)
	}
}

func TestRunBookmarksRetriesUnavailableVideoResolutionOnUnchangedItem(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	videoHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/video" {
			videoHits++
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("mp4-bytes"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookmarkPage{Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7video-retry"},
			Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7video-retry",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "", "createdAt": "2026-08-07T17:00:00Z", "embed": {"$type": "app.bsky.embed.video", "video": {"ref": {"$link": "bafy-video"}}}},
  "embed": {"$type": "app.bsky.embed.video#view", "cid": "bafy-video", "playlist": "https://video.example/playlist.m3u8"}
}`),
		}}})
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	resolver := &retryVideoBlobResolver{url: server.URL + "/video", fail: true}
	first, err := runBookmarksWithResolver(context.Background(), cfg, st, client, BookmarkOptions{}, resolver)
	if err != nil {
		t.Fatalf("first runBookmarks: %v", err)
	}
	if first.Created != 1 || first.MediaUnavailable != 1 || first.MediaLinked != 0 {
		t.Fatalf("first stats = %+v", first)
	}
	item, err := st.GetItem(context.Background(), "bsky:at://did:plc:one/app.bsky.feed.post/3lq7video-retry")
	if err != nil {
		t.Fatalf("GetItem after first run: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs after first run: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("unavailable video created media refs: %#v", refs)
	}

	resolver.fail = false
	second, err := runBookmarksWithResolver(context.Background(), cfg, st, client, BookmarkOptions{MediaHTTPPolicy: &safehttp.Policy{AllowPrivateNetwork: true}}, resolver)
	if err != nil {
		t.Fatalf("second runBookmarks: %v", err)
	}
	if second.Unchanged != 1 || second.MediaUnavailable != 0 || second.MediaLinked != 1 || second.MediaDownloaded != 1 || videoHits != 1 {
		t.Fatalf("second stats = %+v, video hits=%d", second, videoHits)
	}
}

type retryVideoBlobResolver struct {
	url  string
	fail bool
}

func (r *retryVideoBlobResolver) ResolveVideoBlob(context.Context, string, string) (string, error) {
	if r.fail || r.url == "" {
		return "", errors.New("temporary PDS resolution failure")
	}
	return r.url, nil
}

func TestRunBookmarksRejectsRepeatedCursor(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	page := bookmarkPage{
		Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7blocked"},
			Item:    json.RawMessage(`{"$type":"app.bsky.feed.defs#blockedPost"}`),
		}},
		Cursor: "same-cursor",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}

	if _, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{MaxPages: 3}); err == nil {
		t.Fatal("expected repeated cursor error")
	}
}

func testBookmarkStore(t *testing.T) (config.Config, *store.Store) {
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
