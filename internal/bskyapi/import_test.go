package bskyapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			_, _ = w.Write(testJPEGBytes())
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
			_, _ = w.Write(testMP4Bytes())
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

func TestRunBookmarksHydratesBlueskyQuoteChildWithMediaAndLinks(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/quote.jpg" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(testJPEGBytes())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookmarkPage{Bookmarks: []bookmarkView{{
			CreatedAt: "2026-08-08T18:00:00Z",
			Subject:   bookmarkSubject{URI: "at://did:plc:parent/app.bsky.feed.post/3lq7parent", CID: "bafy-parent"},
			Item: json.RawMessage(`{
  "uri": "at://did:plc:parent/app.bsky.feed.post/3lq7parent",
  "cid": "bafy-parent",
  "author": {"did": "did:plc:parent", "handle": "parent.example", "displayName": "Parent"},
  "record": {"text": "Parent text", "createdAt": "2026-08-07T17:00:00Z"},
  "embed": {
    "$type": "app.bsky.embed.record#viewRecord",
    "uri": "at://did:plc:quoted/app.bsky.feed.post/3lq7quoted",
    "cid": "bafy-quoted",
    "author": {"did": "did:plc:quoted", "handle": "quoted.example", "displayName": "Quoted"},
    "value": {"$type": "app.bsky.feed.post", "text": "Quoted text https://quoted.example/article", "createdAt": "2026-08-06T17:00:00Z", "langs": ["en"]},
    "embeds": [{
      "$type": "app.bsky.embed.images#view",
      "images": [{"fullsize": "` + server.URL + `/quote.jpg", "alt": "Quoted image", "aspectRatio": {"width": 1200, "height": 800}}]
    }]
  }
}`),
		}}})
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}

	stats, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{MediaHTTPPolicy: &safehttp.Policy{AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatalf("runBookmarks: %v", err)
	}
	if stats.Created != 1 || stats.QuoteLinked != 1 || stats.MediaDownloaded != 1 {
		t.Fatalf("stats = %+v", stats)
	}

	parent, err := st.GetItem(context.Background(), "bsky:at://did:plc:parent/app.bsky.feed.post/3lq7parent")
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}
	child, err := st.GetItem(context.Background(), "bsky:at://did:plc:quoted/app.bsky.feed.post/3lq7quoted")
	if err != nil {
		t.Fatalf("GetItem child: %v", err)
	}
	if child.SourceType != "bsky_quote" || child.SavedAt != "" || child.Text != "Quoted text https://quoted.example/article" {
		t.Fatalf("child identity/content = %+v", child)
	}
	if !strings.Contains(child.LinksJSON, "https://quoted.example/article") {
		t.Fatalf("child links = %q", child.LinksJSON)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs child: %v", err)
	}
	if len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusDownloaded || !strings.HasPrefix(refs[0].LocalPath, "media/bsky/photo/") {
		t.Fatalf("child media = %#v", refs)
	}
	childLinks, err := st.ListItemChildLinks(context.Background(), parent.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks: %v", err)
	}
	if len(childLinks) != 1 || childLinks[0] != child.ID {
		t.Fatalf("child links = %#v, want [%d]", childLinks, child.ID)
	}
	ocrItems, err := st.ListItemsForXPhotoOCR(context.Background(), 20, true)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR: %v", err)
	}
	if len(ocrItems) != 1 || ocrItems[0].ID != child.ID {
		t.Fatalf("OCR candidates = %#v", ocrItems)
	}

	parentNote, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(parent.NotePath)))
	if err != nil {
		t.Fatalf("read parent note: %v", err)
	}
	if !strings.Contains(string(parentNote), "## Quoted Bluesky Post") || !strings.Contains(string(parentNote), child.NotePath) || !strings.Contains(string(parentNote), "https://bsky.app/profile/quoted.example/post/3lq7quoted") || !strings.Contains(string(parentNote), "Quoted text https://quoted.example/article") {
		t.Fatalf("parent note missing quote context: %s", parentNote)
	}
	childNote, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(child.NotePath)))
	if err != nil {
		t.Fatalf("read child note: %v", err)
	}
	if !strings.Contains(string(childNote), "## Media") || !strings.Contains(string(childNote), "Quoted text https://quoted.example/article") {
		t.Fatalf("child note missing content/media: %s", childNote)
	}
}

func TestRunBookmarksPreservesDirectBlueskyBookmarkWhenItIsQuoted(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	now := time.Date(2026, 8, 8, 18, 0, 0, 0, time.UTC)
	directView := bookmarkView{
		CreatedAt: now.Format(time.RFC3339),
		Subject:   bookmarkSubject{URI: "at://did:plc:quoted/app.bsky.feed.post/3lq7quoted", CID: "bafy-quoted"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:quoted/app.bsky.feed.post/3lq7quoted",
  "cid": "bafy-quoted",
  "author": {"did": "did:plc:quoted", "handle": "quoted.example"},
  "record": {"text": "Directly saved quoted post", "createdAt": "2026-08-06T17:00:00Z"}
}`),
	}
	direct, err := bookmarkViewToItem(directView, now)
	if err != nil {
		t.Fatalf("bookmarkViewToItem: %v", err)
	}
	if _, err := st.UpsertItem(context.Background(), direct); err != nil {
		t.Fatalf("UpsertItem direct: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookmarkPage{Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: "at://did:plc:parent/app.bsky.feed.post/3lq7parent"},
			Item: json.RawMessage(`{
  "uri": "at://did:plc:parent/app.bsky.feed.post/3lq7parent",
  "author": {"did": "did:plc:parent", "handle": "parent.example"},
  "record": {"text": "Parent text", "createdAt": "2026-08-07T17:00:00Z"},
  "embed": {"$type": "app.bsky.embed.record#viewRecord", "uri": "at://did:plc:quoted/app.bsky.feed.post/3lq7quoted", "cid": "bafy-quoted", "author": {"did": "did:plc:quoted", "handle": "quoted.example"}, "value": {"text": "Quote preview", "createdAt": "2026-08-06T17:00:00Z"}}
}`),
		}}})
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	if _, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{}); err != nil {
		t.Fatalf("runBookmarks: %v", err)
	}

	refreshed, err := st.GetItem(context.Background(), direct.SourceKey)
	if err != nil {
		t.Fatalf("GetItem direct: %v", err)
	}
	if refreshed.SourceType != "bsky_bookmark" || refreshed.SavedAt != direct.SavedAt || refreshed.Text != direct.Text {
		t.Fatalf("direct bookmark was downgraded: %+v", refreshed)
	}
	parent, err := st.GetItem(context.Background(), "bsky:at://did:plc:parent/app.bsky.feed.post/3lq7parent")
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}
	childLinks, err := st.ListItemChildLinks(context.Background(), parent.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks: %v", err)
	}
	if len(childLinks) != 1 || childLinks[0] != refreshed.ID {
		t.Fatalf("quoted direct link = %#v, want [%d]", childLinks, refreshed.ID)
	}
}

func TestRunBookmarksBoundsNestedCyclicBlueskyQuotes(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	quoteView := func(uri, cid, handle, text string, embeds ...json.RawMessage) json.RawMessage {
		value := map[string]any{
			"$type":     "app.bsky.feed.post",
			"text":      text,
			"createdAt": "2026-08-06T17:00:00Z",
		}
		view := map[string]any{
			"$type": "app.bsky.embed.record#viewRecord",
			"uri":   uri,
			"cid":   cid,
			"author": map[string]string{
				"did":         strings.Split(uri, "/")[2],
				"handle":      handle,
				"displayName": handle,
			},
			"value": value,
		}
		if len(embeds) > 0 {
			view["embeds"] = embeds
		}
		raw, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("marshal quote view %s: %v", uri, err)
		}
		return raw
	}
	aURI := "at://did:plc:a/app.bsky.feed.post/3lq7a"
	bURI := "at://did:plc:b/app.bsky.feed.post/3lq7b"
	terminalA := quoteView(aURI, "bafy-a", "a.example", "A terminal cycle reference")
	bView := quoteView(bURI, "bafy-b", "b.example", "B quote", terminalA)
	aView := quoteView(aURI, "bafy-a", "a.example", "A quote", bView)
	parentURI := "at://did:plc:parent/app.bsky.feed.post/3lq7parent-cycle"
	parentItem, err := json.Marshal(map[string]any{
		"uri": parentURI,
		"cid": "bafy-parent",
		"author": map[string]string{
			"did":    "did:plc:parent",
			"handle": "parent.example",
		},
		"record": map[string]string{
			"text":      "Parent text",
			"createdAt": "2026-08-07T17:00:00Z",
		},
		"embed": json.RawMessage(aView),
	})
	if err != nil {
		t.Fatalf("marshal parent: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookmarkPage{Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: parentURI, CID: "bafy-parent"},
			Item:    parentItem,
		}}})
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	stats, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{})
	if err != nil {
		t.Fatalf("runBookmarks: %v", err)
	}
	if stats.QuoteLinked != 2 || stats.QuoteSkipped != 0 {
		t.Fatalf("stats = %+v, want two bounded quote links and no skips", stats)
	}

	parent, err := st.GetItem(context.Background(), "bsky:"+parentURI)
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}
	a, err := st.GetItem(context.Background(), "bsky:"+aURI)
	if err != nil {
		t.Fatalf("GetItem A: %v", err)
	}
	b, err := st.GetItem(context.Background(), "bsky:"+bURI)
	if err != nil {
		t.Fatalf("GetItem B: %v", err)
	}
	if a.SourceType != "bsky_quote" || b.SourceType != "bsky_quote" {
		t.Fatalf("quote source types = %q, %q", a.SourceType, b.SourceType)
	}
	parentLinks, err := st.ListItemChildLinks(context.Background(), parent.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks parent: %v", err)
	}
	aLinks, err := st.ListItemChildLinks(context.Background(), a.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks A: %v", err)
	}
	bLinks, err := st.ListItemChildLinks(context.Background(), b.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks B: %v", err)
	}
	if len(parentLinks) != 1 || parentLinks[0] != a.ID || len(aLinks) != 1 || aLinks[0] != b.ID || len(bLinks) != 0 {
		t.Fatalf("bounded quote links = parent=%#v A=%#v B=%#v", parentLinks, aLinks, bLinks)
	}
}

func TestRunBookmarksClearsStaleQuoteLinkForBlockedView(t *testing.T) {
	cfg, st := testBookmarkStore(t)
	request := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request++
		w.Header().Set("Content-Type", "application/json")
		embed := `{"$type":"app.bsky.embed.record#viewRecord","uri":"at://did:plc:quoted/app.bsky.feed.post/3lq7quoted","author":{"did":"did:plc:quoted","handle":"quoted.example"},"value":{"text":"Quote preview","createdAt":"2026-08-06T17:00:00Z"}}`
		if request > 1 {
			embed = `{"$type":"app.bsky.embed.record#viewBlocked"}`
		}
		item := `{"uri":"at://did:plc:parent/app.bsky.feed.post/3lq7parent","author":{"did":"did:plc:parent","handle":"parent.example"},"record":{"text":"Parent text","createdAt":"2026-08-07T17:00:00Z"},"embed":` + embed + `}`
		_ = json.NewEncoder(w).Encode(bookmarkPage{Bookmarks: []bookmarkView{{
			Subject: bookmarkSubject{URI: "at://did:plc:parent/app.bsky.feed.post/3lq7parent"},
			Item:    json.RawMessage(item),
		}}})
	}))
	defer server.Close()
	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	if _, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{}); err != nil {
		t.Fatalf("first runBookmarks: %v", err)
	}
	parent, err := st.GetItem(context.Background(), "bsky:at://did:plc:parent/app.bsky.feed.post/3lq7parent")
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}
	links, err := st.ListItemChildLinks(context.Background(), parent.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks first: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("first quote links = %#v", links)
	}
	childID := links[0]

	second, err := runBookmarks(context.Background(), cfg, st, client, BookmarkOptions{})
	if err != nil {
		t.Fatalf("second runBookmarks: %v", err)
	}
	if second.QuoteSkippedBlocked != 1 || second.QuoteSkipped != 1 {
		t.Fatalf("second stats = %+v", second)
	}
	links, err = st.ListItemChildLinks(context.Background(), parent.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks second: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("stale quote link remains = %#v", links)
	}
	if _, err := st.GetItemByID(context.Background(), childID); err != nil {
		t.Fatalf("historical quote child was deleted: %v", err)
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

func testJPEGBytes() []byte {
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.SetRGBA(0, 0, color.RGBA{R: 0x33, G: 0x66, B: 0x99, A: 0xff})
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, imageData, nil); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}

func testMP4Bytes() []byte {
	avcConfig := []byte{1, 0x64, 0, 0x1f, 0xff, 0xe1, 0, 4, 0x67, 0x64, 0, 0x1f, 1, 0, 2, 0x68, 0xeb}
	sampleEntry := make([]byte, 8+78+8+len(avcConfig))
	binary.BigEndian.PutUint32(sampleEntry[:4], uint32(len(sampleEntry)))
	copy(sampleEntry[4:8], []byte("avc1"))
	binary.BigEndian.PutUint32(sampleEntry[8+78:8+78+4], uint32(8+len(avcConfig)))
	copy(sampleEntry[8+78+4:8+78+8], []byte("avcC"))
	copy(sampleEntry[8+78+8:], avcConfig)
	stsdPayload := append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, sampleEntry...)
	stsd := testISOBox("stsd", stsdPayload)
	stbl := testISOBox("stbl", stsd)
	minf := testISOBox("minf", stbl)
	mdia := testISOBox("mdia", minf)
	trak := testISOBox("trak", mdia)
	moov := testISOBox("moov", trak)
	nal := append([]byte{0, 0, 0, 16, 0x65, 0x88, 0x84, 0x21}, bytes.Repeat([]byte{0x55}, 12)...)
	return append(testISOBox("ftyp", []byte{'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}), append(moov, testISOBox("mdat", nal)...)...)
}

func testISOBox(boxType string, payload []byte) []byte {
	result := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(8+len(payload)))
	copy(result[4:], boxType)
	return append(result, payload...)
}
