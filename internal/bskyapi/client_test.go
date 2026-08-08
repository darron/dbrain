package bskyapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestFetchBookmarksPageUsesPDSAndCursor(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/xrpc/app.bsky.bookmark.getBookmarks" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got, want := r.URL.Query().Get("limit"), "100"; got != want {
			t.Errorf("limit = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("cursor"), "next page"; got != want {
			t.Errorf("cursor = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer access-token"; got != want {
			t.Errorf("authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bookmarks":[{"subject":{"uri":"at://did:plc:one/app.bsky.feed.post/3lq7example","cid":"bafyreiexample"},"item":{"uri":"at://did:plc:one/app.bsky.feed.post/3lq7example"}}],"cursor":"next2"}`))
	}))
	defer server.Close()

	client, err := newBookmarkClient(sessionCredentials{
		Service:   "https://wrong.example",
		PDSURL:    server.URL,
		AccessJWT: "access-token",
	}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}

	page, err := client.fetchBookmarksPage(context.Background(), "next page", 100)
	if err != nil {
		t.Fatalf("fetchBookmarksPage: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got, want := page.Cursor, "next2"; got != want {
		t.Fatalf("cursor = %q, want %q", got, want)
	}
	if got, want := len(page.Bookmarks), 1; got != want {
		t.Fatalf("bookmarks = %d, want %d", got, want)
	}
}

func TestFetchBookmarksPageRejectsMissingSessionToken(t *testing.T) {
	if _, err := newBookmarkClient(sessionCredentials{Service: "https://bsky.social"}, http.DefaultClient); err == nil {
		t.Fatal("expected missing session token error")
	}
}

func TestBookmarkURLAcceptsOnlyHTTPSBase(t *testing.T) {
	if _, err := newBookmarkClient(sessionCredentials{
		Service:   "http://insecure.example",
		AccessJWT: "access-token",
	}, http.DefaultClient); err == nil {
		t.Fatal("expected insecure service URL error")
	}
	for _, service := range []string{
		"https://user:password@bsky.social",
		"https://bsky.social/?redirect=https://evil.example",
		"https://bsky.social/#fragment",
	} {
		if _, err := newBookmarkClient(sessionCredentials{Service: service, AccessJWT: "access-token"}, http.DefaultClient); err == nil {
			t.Fatalf("expected unsafe service URL error for %q", service)
		}
	}
	if _, err := url.Parse("https://bsky.social"); err != nil {
		t.Fatalf("test URL parse: %v", err)
	}
}

func TestFetchBookmarksPageRetriesTransientResponses(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bookmarks":[],"cursor":""}`))
	}))
	defer server.Close()

	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, AccessJWT: "access-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	if _, err := client.fetchBookmarksPage(context.Background(), "", 100); err != nil {
		t.Fatalf("fetchBookmarksPage: %v", err)
	}
	if got, want := attempts.Load(), int32(3); got != want {
		t.Fatalf("attempts = %d, want %d", got, want)
	}
}

func TestFetchBookmarksPageRefreshesOnceAfterUnauthorized(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.refreshSession":
			if got, want := r.Header.Get("Authorization"), "Bearer refresh-token"; got != want {
				t.Errorf("refresh authorization = %q, want %q", got, want)
			}
			refreshes.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessJwt":"new-access-token","refreshJwt":"new-refresh-token"}`))
		case "/xrpc/app.bsky.bookmark.getBookmarks":
			if got := r.Header.Get("Authorization"); got == "Bearer old-access-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got, want := r.Header.Get("Authorization"), "Bearer new-access-token"; got != want {
				t.Errorf("bookmark authorization = %q, want %q", got, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bookmarks":[],"cursor":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newBookmarkClient(sessionCredentials{
		PDSURL:     server.URL,
		AccessJWT:  "old-access-token",
		RefreshJWT: "refresh-token",
	}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	if _, err := client.fetchBookmarksPage(context.Background(), "", 100); err != nil {
		t.Fatalf("fetchBookmarksPage: %v", err)
	}
	if got, want := refreshes.Load(), int32(1); got != want {
		t.Fatalf("refreshes = %d, want %d", got, want)
	}
}

func TestFetchBookmarksPageRefreshesExpiredTokenError(t *testing.T) {
	var refreshes atomic.Int32
	var bookmarkRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.refreshSession":
			refreshes.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessJwt":"new-access-token"}`))
		case "/xrpc/app.bsky.bookmark.getBookmarks":
			if bookmarkRequests.Add(1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"ExpiredToken","message":"Token has expired"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bookmarks":[],"cursor":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newBookmarkClient(sessionCredentials{
		PDSURL:     server.URL,
		AccessJWT:  "old-access-token",
		RefreshJWT: "refresh-token",
	}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	if _, err := client.fetchBookmarksPage(context.Background(), "", 100); err != nil {
		t.Fatalf("fetchBookmarksPage: %v", err)
	}
	if got, want := refreshes.Load(), int32(1); got != want {
		t.Fatalf("refreshes = %d, want %d", got, want)
	}
}

func TestFetchBookmarksPageRefreshesWhenAccessTokenIsAbsent(t *testing.T) {
	var bookmarkAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.refreshSession":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessJwt":"new-access-token"}`))
		case "/xrpc/app.bsky.bookmark.getBookmarks":
			bookmarkAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bookmarks":[],"cursor":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newBookmarkClient(sessionCredentials{PDSURL: server.URL, RefreshJWT: "refresh-token"}, server.Client())
	if err != nil {
		t.Fatalf("newBookmarkClient: %v", err)
	}
	if _, err := client.fetchBookmarksPage(context.Background(), "", 100); err != nil {
		t.Fatalf("fetchBookmarksPage: %v", err)
	}
	if bookmarkAuth != "Bearer new-access-token" {
		t.Fatalf("bookmark authorization = %q", bookmarkAuth)
	}
}
