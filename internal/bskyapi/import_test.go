package bskyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
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
