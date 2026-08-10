package mastodonapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

func TestRunBookmarksWithClientCheckpointsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	status := `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"<p>Hello <a href=\"https://example.com\">world</a></p>","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice","acct":"alice@hachyderm.io"}}]`
	client, err := NewClient("https://hachyderm.io", "test-token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization header leaked or missing: %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			return jsonResponse(http.StatusOK, status), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Created != 1 || first.Updated != 0 || first.StoppedReason != "backfill complete" {
		t.Fatalf("first stats = %#v", first)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", "https://hachyderm.io:443")
	if err != nil || state == nil || !state.BackfillComplete || state.VerifiedAccountID != "42" {
		t.Fatalf("state = %#v, err=%v", state, err)
	}
	second, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Created != 0 || second.Updated != 0 || second.Unchanged != 1 || second.StoppedReason != "overlap page reached" {
		t.Fatalf("second stats = %#v", second)
	}
	if _, err := os.Stat(cfg.VaultDir); err != nil {
		t.Fatalf("vault note directory was not created: %v", err)
	}
}

func TestMastodonSourceKeyRetainsCanonicalURIAndAccountScope(t *testing.T) {
	a := mastodonSourceKey("https://hachyderm.io", "42", "https://remote.example/users/a/statuses/1")
	b := mastodonSourceKey("https://other.example", "42", "https://remote.example/users/a/statuses/1")
	if a == b || !strings.Contains(a, "account:42:uri:") {
		t.Fatalf("source keys do not preserve account/origin scope: %q %q", a, b)
	}
	if _, err := url.Parse("https://remote.example/users/a/statuses/1"); err != nil {
		t.Fatal(err)
	}
}

func TestRunBookmarksPreservesLastSuccessWhenTheNextPageFails(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	failBookmarks := false
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			if failBookmarks {
				return jsonResponse(http.StatusServiceUnavailable, `{"error":"temporarily unavailable"}`), nil
			}
			return jsonResponse(http.StatusOK, `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"hello","created_at":"2026-08-08T12:00:00Z","account":{"id":"99","username":"author"}}]`), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := st.GetItem(context.Background(), mastodonSourceKey("https://hachyderm.io:443", "42", "https://hachyderm.io/users/alice/statuses/1")); err != nil {
		t.Fatalf("import did not scope item to verified account: %v", err)
	}
	failBookmarks = true
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err == nil {
		t.Fatal("second import unexpectedly succeeded")
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", "https://hachyderm.io:443")
	if err != nil {
		t.Fatalf("GetMastodonSyncState: %v", err)
	}
	if state == nil || !state.LastSuccessAt.Equal(now) || state.LastError == "" || state.LastErrorAt.IsZero() {
		t.Fatalf("state after failed retry = %#v, want prior success and recorded error", state)
	}
}

func TestRunBookmarksRejectsVerifiedIdentityChangeWithoutResettingState(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	verifiedID := "42"
	bookmarksCalls := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"`+verifiedID+`","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			bookmarksCalls++
			return jsonResponse(http.StatusOK, `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"hello","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}}]`), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	verifiedID = "99"
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Force: true, Now: func() time.Time { return now }}); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("identity replacement error = %v", err)
	}
	if bookmarksCalls != 1 {
		t.Fatalf("identity replacement contacted bookmarks endpoint %d times, want 1", bookmarksCalls)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", "https://hachyderm.io:443")
	if err != nil || state == nil || state.VerifiedAccountID != "42" || !state.BackfillComplete {
		t.Fatalf("state after rejected identity replacement = %#v, err=%v", state, err)
	}
}

func TestRunBookmarksLimitedPageResumesWithoutWedge(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	page := `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"one","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}},` +
		`{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"two","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}},` +
		`{"id":"local-3","uri":"https://hachyderm.io/users/alice/statuses/3","url":"https://hachyderm.io/@alice/3","content":"three","created_at":"2026-08-08T10:00:00Z","account":{"id":"42","username":"alice"}}]`
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			return jsonResponse(http.StatusOK, page), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("first limited import: %v", err)
	}
	if first.Processed != 1 || first.StoppedReason != "limit reached" {
		t.Fatalf("first limited stats = %#v", first)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", "https://hachyderm.io:443")
	if err != nil || state == nil || state.BackfillComplete || state.BackfillPageOffset != 1 {
		t.Fatalf("partial state = %#v, err=%v", state, err)
	}
	second, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("resumed limited import: %v", err)
	}
	if second.Processed != 1 || second.StoppedReason != "limit reached" {
		t.Fatalf("resumed limited stats = %#v", second)
	}
	state, err = st.GetMastodonSyncState(context.Background(), "hachyderm", "https://hachyderm.io:443")
	if err != nil || state == nil || state.BackfillComplete || state.BackfillPageOffset != 2 {
		t.Fatalf("second partial state = %#v, err=%v", state, err)
	}
	third, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("completed resumed import: %v", err)
	}
	if third.Processed != 1 || third.StoppedReason != "backfill complete" {
		t.Fatalf("completed resumed stats = %#v", third)
	}
	for index := 1; index <= 3; index++ {
		if _, err := st.GetItem(context.Background(), mastodonSourceKey("https://hachyderm.io:443", "42", "https://hachyderm.io/users/alice/statuses/"+strconv.Itoa(index))); err != nil {
			t.Fatalf("status %d was not imported after resume: %v", index, err)
		}
	}
}

func TestRunBookmarksLimitedIncrementalPageDoesNotResumeHistoricalBackfill(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	status := `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"one","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}},` +
		`{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"two","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}},` +
		`{"id":"local-3","uri":"https://hachyderm.io/users/alice/statuses/3","url":"https://hachyderm.io/@alice/3","content":"three","created_at":"2026-08-08T10:00:00Z","account":{"id":"42","username":"alice"}}]`
	includeNext := false
	oldPageCalls := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "historical" {
				oldPageCalls++
				return jsonResponse(http.StatusOK, `[]`), nil
			}
			response := jsonResponse(http.StatusOK, status)
			if includeNext {
				response.Header.Set("Link", `<https://hachyderm.io/api/v1/bookmarks?max_id=historical>; rel="next"`)
			}
			return response, nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("seed complete import: %v", err)
	}
	includeNext = true
	partial, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("limited incremental import: %v", err)
	}
	if partial.StoppedReason != "limit reached" {
		t.Fatalf("partial incremental stats = %#v", partial)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || state.BackfillComplete || !state.BackfillIncremental {
		t.Fatalf("partial incremental state = %#v, err=%v", state, err)
	}
	resumed, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("first resumed incremental import: %v", err)
	}
	if resumed.Processed != 1 || resumed.StoppedReason != "limit reached" || oldPageCalls != 0 {
		t.Fatalf("first incremental resume fell into historical backfill: stats=%#v old_page_calls=%d", resumed, oldPageCalls)
	}
	state, err = st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || !state.BackfillIncremental || state.BackfillPageOffset != 2 {
		t.Fatalf("first resumed incremental state = %#v, err=%v", state, err)
	}
	complete, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("second resumed incremental import: %v", err)
	}
	if complete.StoppedReason != "empty page" || oldPageCalls != 1 {
		t.Fatalf("second incremental resume did not conservatively verify the next page: stats=%#v old_page_calls=%d", complete, oldPageCalls)
	}
}

func TestRunBookmarksEmptyIncrementalPageWithNextPreservesResumeMode(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	emptyPage := "https://hachyderm.io/api/v1/bookmarks?max_id=empty"
	nextPage := "https://hachyderm.io/api/v1/bookmarks?max_id=next"
	nextStatus := `[{"id":"next-1","uri":"https://hachyderm.io/users/alice/statuses/next-1","url":"https://hachyderm.io/@alice/next-1","content":"next page","created_at":"2026-08-08T12:00:00Z","account":{"id":"author-1","username":"author"}}]`
	emptyCalls, nextCalls := 0, 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			switch request.URL.Query().Get("max_id") {
			case "empty":
				emptyCalls++
				response := jsonResponse(http.StatusOK, `[]`)
				response.Header.Set("Link", `<`+nextPage+`>; rel="next"`)
				return response, nil
			case "next":
				nextCalls++
				return jsonResponse(http.StatusOK, nextStatus), nil
			default:
				t.Fatalf("unexpected bookmark cursor %q", request.URL.Query().Get("max_id"))
				return nil, nil
			}
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{
		AccountKey:        "hachyderm",
		CanonicalOrigin:   client.Origin,
		VerifiedAccountID: "42",
		BackfillComplete:  true,
		BackfillNextURL:   emptyPage,
		LastSuccessAt:     now,
	}); err != nil {
		t.Fatalf("seed completed incremental cursor: %v", err)
	}

	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{
		AccountKey: "hachyderm",
		MaxPages:   1,
		Now:        func() time.Time { return now },
	})
	if err == nil || first.PagesFetched != 1 || emptyCalls != 1 || nextCalls != 0 {
		t.Fatalf("interrupted empty incremental page = stats=%#v err=%v empty_calls=%d next_calls=%d", first, err, emptyCalls, nextCalls)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || !state.BackfillComplete || state.BackfillIncremental || state.BackfillNextURL != nextPage {
		t.Fatalf("empty incremental checkpoint = %#v, err=%v", state, err)
	}

	resumed, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{
		AccountKey: "hachyderm",
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("resume after empty incremental page: %v", err)
	}
	if resumed.Processed != 1 || resumed.StoppedReason != "backfill complete" || nextCalls != 1 {
		t.Fatalf("resume after empty incremental page = stats=%#v next_calls=%d", resumed, nextCalls)
	}
}

func TestRunBookmarksResumedIncrementalPageContinuesPastNewPrefix(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	existing := `{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"existing","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}}`
	newPrefix := `{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"new prefix","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}}`
	newHistorical := `{"id":"local-3","uri":"https://hachyderm.io/users/alice/statuses/3","url":"https://hachyderm.io/@alice/3","content":"new historical","created_at":"2026-08-08T10:00:00Z","account":{"id":"42","username":"alice"}}`
	phase := 0
	historicalCalls := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "historical" {
				historicalCalls++
				return jsonResponse(http.StatusOK, "["+newHistorical+"]"), nil
			}
			if phase == 0 {
				phase++
				return jsonResponse(http.StatusOK, "["+existing+"]"), nil
			}
			response := jsonResponse(http.StatusOK, "["+newPrefix+","+existing+"]")
			response.Header.Set("Link", `<https://hachyderm.io/api/v1/bookmarks?max_id=historical>; rel="next"`)
			return response, nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("seed import: %v", err)
	}
	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil || first.Processed != 1 || first.StoppedReason != "limit reached" {
		t.Fatalf("first limited incremental import = %#v, err=%v", first, err)
	}
	second, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil || second.Processed != 1 || second.StoppedReason != "limit reached" || historicalCalls != 1 {
		t.Fatalf("resumed incremental suffix did not continue safely: stats=%#v err=%v historical_calls=%d", second, err, historicalCalls)
	}
	third, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil || third.Created != 1 {
		t.Fatalf("historical page after resumed suffix = %#v, err=%v", third, err)
	}
	if _, err := st.GetItem(context.Background(), mastodonSourceKey(client.Origin, "42", "https://hachyderm.io/users/alice/statuses/3")); err != nil {
		t.Fatalf("new historical bookmark was skipped: %v", err)
	}
}

func TestRunBookmarksSkippedStatusesDoNotProveIncrementalOverlap(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	existing := `{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"existing","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}}`
	unsupported := `{"id":"unsupported","uri":"https://hachyderm.io/users/alice/statuses/u","created_at":"2026-08-08T11:00:00Z","content":"","account":{"id":"42","username":"alice"}}`
	malformed := `{"id":"malformed","uri":"https://hachyderm.io/users/alice/statuses/m","created_at":"not-a-timestamp","content":"malformed","account":{"id":"42","username":"alice"}}`
	historical := `{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"after skipped statuses","created_at":"2026-08-08T10:00:00Z","account":{"id":"42","username":"alice"}}`
	phase := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "historical" {
				return jsonResponse(http.StatusOK, "["+historical+"]"), nil
			}
			if phase == 0 {
				phase++
				return jsonResponse(http.StatusOK, "["+existing+"]"), nil
			}
			response := jsonResponse(http.StatusOK, "["+unsupported+","+malformed+","+existing+"]")
			response.Header.Set("Link", `<https://hachyderm.io/api/v1/bookmarks?max_id=historical>; rel="next"`)
			return response, nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("seed import: %v", err)
	}
	stats, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }})
	if err != nil || stats.SkippedUnsupported != 1 || stats.SkippedMalformed != 1 || stats.Created != 1 {
		t.Fatalf("skipped-status incremental import = %#v, err=%v", stats, err)
	}
	if _, err := st.GetItem(context.Background(), mastodonSourceKey(client.Origin, "42", "https://hachyderm.io/users/alice/statuses/2")); err != nil {
		t.Fatalf("bookmark after skipped statuses was not imported: %v", err)
	}
}

func TestRunBookmarksRetriesHistoricalMediaAfterBackfillCompletes(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	imageBytes, err := os.ReadFile("../mediadownload/testdata/mastodon-image-jfif.jpg")
	if err != nil {
		t.Fatalf("read test image: %v", err)
	}
	mediaAttempts := 0
	mediaServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaAttempts++
		if mediaAttempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("temporary media failure"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(imageBytes)
	}))
	defer mediaServer.Close()

	head := `{"id":"head","uri":"https://hachyderm.io/users/alice/statuses/head","url":"https://hachyderm.io/@alice/head","content":"head","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}}`
	mediaStatus := `{"id":"media","uri":"https://hachyderm.io/users/alice/statuses/media","url":"https://hachyderm.io/@alice/media","content":"historical media","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"},"media_attachments":[{"id":"m1","type":"image","url":"` + mediaServer.URL + `/image.jpg","remote_url":"` + mediaServer.URL + `/image.jpg"}]}`
	phase := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "page-2" {
				return jsonResponse(http.StatusOK, "["+mediaStatus+"]"), nil
			}
			if phase == 0 {
				phase++
				response := jsonResponse(http.StatusOK, "["+head+"]")
				response.Header.Set("Link", `<https://hachyderm.io/api/v1/bookmarks?max_id=page-2>; rel="next"`)
				return response, nil
			}
			return jsonResponse(http.StatusOK, "["+head+"]"), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	mediaTransport := mediaServer.Client().Transport.(*http.Transport)
	mediaPolicy := &safehttp.Policy{AllowPrivateNetwork: true, TLSClientConfig: mediaTransport.TLSClientConfig}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", MediaHTTPPolicy: mediaPolicy, Now: func() time.Time { return now }})
	if err != nil || first.MediaErrors != 1 || mediaAttempts != 1 {
		t.Fatalf("initial historical media failure = %#v, err=%v media_attempts=%d", first, err, mediaAttempts)
	}
	item, err := st.GetItem(context.Background(), mastodonSourceKey(client.Origin, "42", "https://hachyderm.io/users/alice/statuses/media"))
	if err != nil {
		t.Fatalf("load historical media item: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), item.ID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("historical media refs = %#v, err=%v", refs, err)
	}
	oldAttempt := time.Now().UTC().Add(-model.MediaDownloadRetryCooldown - time.Minute)
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{Status: model.MediaDownloadStatusError, Error: "temporary media failure", AttemptedAt: oldAttempt}); err != nil {
		t.Fatalf("age historical media failure: %v", err)
	}
	second, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", MediaHTTPPolicy: mediaPolicy, Now: func() time.Time { return now }})
	if err != nil || second.MediaDownloaded != 1 || mediaAttempts != 2 {
		t.Fatalf("normal incremental run did not retry historical media: %#v, err=%v media_attempts=%d", second, err, mediaAttempts)
	}
	refs, err = st.ListItemMediaRefs(context.Background(), item.ID)
	if err != nil || refs[0].DownloadStatus != model.MediaDownloadStatusDownloaded || refs[0].LocalPath == "" {
		t.Fatalf("historical media after retry = %#v, err=%v", refs, err)
	}
}

func TestRunBookmarksForceRecoversTerminalBlockedMastodonMedia(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	imageBytes, err := os.ReadFile("../mediadownload/testdata/mastodon-image-jfif.jpg")
	if err != nil {
		t.Fatalf("read test image: %v", err)
	}
	mediaAttempts := 0
	mediaServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaAttempts++
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("media request leaked authorization header %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("media request leaked cookie header %q", got)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(imageBytes)
	}))
	defer mediaServer.Close()

	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "mastodon:https://hachyderm.io:443:account:42:uri:blocked-media",
		SourceType:   "mastodon_bookmark",
		ExternalID:   "blocked-media",
		CanonicalURL: "https://hachyderm.io/@alice/blocked-media",
		Title:        "blocked media",
		ContentHash:  "blocked-media-hash",
		LinksJSON:    "[]",
		NotePath:     "items/mastodon/2026/blocked-media.md",
		RawJSON:      "{}",
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(context.Background(), item.ItemID, []model.MediaCandidate{{
		RemoteURL: mediaServer.URL + "/image.jpg",
		MediaType: "photo",
	}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), item.ItemID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListItemMediaRefs = %#v, err=%v", refs, err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		Status: model.MediaDownloadStatusBlocked,
		Error:  "media response content is not a complete recognized image format",
	}); err != nil {
		t.Fatalf("seed blocked media: %v", err)
	}

	client, err := NewClient("https://hachyderm.io", "api-bearer-token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			return jsonResponse(http.StatusOK, `[]`), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	mediaTransport := mediaServer.Client().Transport.(*http.Transport)
	mediaPolicy := &safehttp.Policy{AllowPrivateNetwork: true, TLSClientConfig: mediaTransport.TLSClientConfig}

	ordinary, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{
		AccountKey:      "hachyderm",
		MediaHTTPPolicy: mediaPolicy,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("ordinary import: %v", err)
	}
	if mediaAttempts != 0 || ordinary.MediaDownloaded != 0 {
		t.Fatalf("ordinary import retried blocked media: attempts=%d stats=%+v", mediaAttempts, ordinary)
	}
	ocrBeforeForce, err := st.ListItemsForXPhotoOCR(context.Background(), 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR before force recovery: %v", err)
	}
	if len(ocrBeforeForce) != 0 {
		t.Fatalf("terminal blocked Mastodon photo was unexpectedly OCR-eligible before recovery: %+v", ocrBeforeForce)
	}

	forced, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{
		AccountKey:      "hachyderm",
		Force:           true,
		MediaHTTPPolicy: mediaPolicy,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("forced import: %v", err)
	}
	if mediaAttempts != 1 || forced.MediaDownloaded != 1 {
		t.Fatalf("forced import did not recover blocked media: attempts=%d stats=%+v", mediaAttempts, forced)
	}
	refs, err = st.ListItemMediaRefs(context.Background(), item.ItemID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListItemMediaRefs after force = %#v, err=%v", refs, err)
	}
	if refs[0].DownloadStatus != model.MediaDownloadStatusDownloaded ||
		refs[0].DownloadErrors != 0 ||
		!strings.HasPrefix(refs[0].LocalPath, "media/mastodon/photo/") {
		t.Fatalf("recovered media ref = %#v", refs[0])
	}
	ocrCandidates, err := st.ListItemsForXPhotoOCR(context.Background(), 10, false)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR after force recovery: %v", err)
	}
	if len(ocrCandidates) != 1 || ocrCandidates[0].SourceKey != "mastodon:https://hachyderm.io:443:account:42:uri:blocked-media" {
		t.Fatalf("force-recovered Mastodon photo did not reach OCR selector: %+v", ocrCandidates)
	}
}

func TestRunBookmarksForceRetriesBlockedMediaDuringStatusProcessing(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	imageBytes, err := os.ReadFile("../mediadownload/testdata/mastodon-image-rgba.png")
	if err != nil {
		t.Fatalf("read test image: %v", err)
	}
	mediaAttempts := 0
	mediaServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaAttempts++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatalf("media request carried API credentials: authorization=%q cookie=%q", r.Header.Get("Authorization"), r.Header.Get("Cookie"))
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes)
	}))
	defer mediaServer.Close()

	const statusURI = "https://hachyderm.io/users/alice/statuses/blocked-status"
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    mastodonSourceKey("https://hachyderm.io:443", "42", statusURI),
		SourceType:   "mastodon_bookmark",
		ExternalID:   "blocked-status",
		CanonicalURL: "https://hachyderm.io/@alice/blocked-status",
		Title:        "blocked status",
		ContentHash:  "blocked-status-hash",
		LinksJSON:    "[]",
		NotePath:     "items/mastodon/2026/blocked-status.md",
		RawJSON:      "{}",
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.SaveItemMediaCandidates(context.Background(), item.ItemID, []model.MediaCandidate{{
		RemoteURL: mediaServer.URL + "/image.png",
		MediaType: "photo",
	}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), item.ItemID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListItemMediaRefs = %#v, err=%v", refs, err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		Status: model.MediaDownloadStatusBlocked,
		Error:  "media response content is not a complete recognized image format",
	}); err != nil {
		t.Fatalf("seed blocked media: %v", err)
	}

	page := `[{"id":"blocked-status","uri":"` + statusURI + `","url":"https://hachyderm.io/@alice/blocked-status","content":"blocked status","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"},"media_attachments":[{"id":"m1","type":"image","url":"` + mediaServer.URL + `/image.png","remote_url":"` + mediaServer.URL + `/image.png"}]},{"id":"limit-stop","uri":"https://hachyderm.io/users/alice/statuses/limit-stop","url":"https://hachyderm.io/@alice/limit-stop","content":"limit stop","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}}]`
	client, err := NewClient("https://hachyderm.io", "api-bearer-token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			return jsonResponse(http.StatusOK, page), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	mediaTransport := mediaServer.Client().Transport.(*http.Transport)
	stats, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{
		AccountKey: "hachyderm",
		Limit:      1,
		Force:      true,
		MediaHTTPPolicy: &safehttp.Policy{
			AllowPrivateNetwork: true,
			TLSClientConfig:     mediaTransport.TLSClientConfig,
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("forced import: %v", err)
	}
	if stats.StoppedReason != "limit reached" || stats.MediaDownloaded != 1 || mediaAttempts != 1 {
		t.Fatalf("blocked status media was not force-retried before sweep: attempts=%d stats=%+v", mediaAttempts, stats)
	}
}

func TestRunBookmarksResumesCompletedIncrementalCursorAfterPageCheckpoint(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	page2 := "https://hachyderm.io/api/v1/bookmarks?max_id=page-2"
	pageOne := `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"one","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}}]`
	pageTwo := `[{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"two","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}}]`
	headCalls := 0
	page2Calls := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "page-2" {
				page2Calls++
				return jsonResponse(http.StatusOK, pageTwo), nil
			}
			headCalls++
			return jsonResponse(http.StatusOK, pageOne), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("seed complete import: %v", err)
	}
	headCalls = 0
	page2Calls = 0
	if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{
		AccountKey:        "hachyderm",
		CanonicalOrigin:   client.Origin,
		VerifiedAccountID: "42",
		BackfillComplete:  true,
		BackfillNextURL:   page2,
		LastSuccessAt:     now,
	}); err != nil {
		t.Fatalf("seed completed incremental cursor: %v", err)
	}
	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", MaxPages: 1, Now: func() time.Time { return now }})
	if err != nil || first.PagesFetched != 1 || first.Processed != 1 || first.StoppedReason != "backfill complete" || page2Calls != 1 || headCalls != 0 {
		t.Fatalf("completed incremental cursor was not resumed: stats=%#v err=%v page2_calls=%d head_calls=%d", first, err, page2Calls, headCalls)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || !state.BackfillComplete || state.BackfillNextURL != "" {
		t.Fatalf("checkpointed incremental state = %#v, err=%v", state, err)
	}
	if _, err := st.GetItem(context.Background(), mastodonSourceKey(client.Origin, "42", "https://hachyderm.io/users/alice/statuses/2")); err != nil {
		t.Fatalf("page 2 status was not imported from the resumed cursor: %v", err)
	}
}

func TestRunBookmarksDoesNotRegressConcurrentSameIdentityCursor(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	page2 := "https://hachyderm.io/api/v1/bookmarks?max_id=page-2"
	page3 := "https://hachyderm.io/api/v1/bookmarks?max_id=page-3"
	page4 := "https://hachyderm.io/api/v1/bookmarks?max_id=page-4"
	pageOne := `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"one","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}}]`
	pageTwo := `[{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"two","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}}]`
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			switch request.URL.Query().Get("max_id") {
			case "":
				response := jsonResponse(http.StatusOK, pageOne)
				response.Header.Set("Link", "<"+page2+">; rel=\"next\"")
				return response, nil
			case "page-2":
				response := jsonResponse(http.StatusOK, pageTwo)
				response.Header.Set("Link", "<"+page3+">; rel=\"next\"")
				return response, nil
			case "page-3":
				return jsonResponse(http.StatusOK, `[]`), nil
			default:
				t.Fatalf("unexpected bookmark cursor %q", request.URL.Query().Get("max_id"))
				return nil, nil
			}
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	checkpointCount := 0
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	_, err = RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{
		AccountKey: "hachyderm",
		Now:        func() time.Time { return now },
		afterCheckpointHook: func(written store.MastodonSyncState) {
			checkpointCount++
			if checkpointCount != 1 {
				return
			}
			if written.BackfillNextURL != page2 {
				t.Fatalf("first checkpoint = %#v, want cursor %q", written, page2)
			}
			if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{
				AccountKey:        written.AccountKey,
				CanonicalOrigin:   written.CanonicalOrigin,
				VerifiedAccountID: written.VerifiedAccountID,
				Handle:            written.Handle,
				BackfillNextURL:   page4,
				LastSuccessAt:     written.LastSuccessAt.Add(time.Second),
			}); err != nil {
				t.Fatalf("concurrent same-identity advancement: %v", err)
			}
		},
	})
	if !errors.Is(err, store.ErrMastodonSyncStateChanged) {
		t.Fatalf("concurrent cursor regression error = %v, want %v", err, store.ErrMastodonSyncStateChanged)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil {
		t.Fatalf("GetMastodonSyncState: %v", err)
	}
	if state == nil || state.BackfillNextURL != page4 || state.BackfillComplete {
		t.Fatalf("newer same-identity cursor was regressed: %#v", state)
	}
}

func TestRunBookmarksStopsAfterOneStaleCursorRecovery(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	bookmarksCalls := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice"}`), nil
		case "/api/v1/bookmarks":
			bookmarksCalls++
			if request.URL.Query().Get("max_id") == "stale" {
				return jsonResponse(http.StatusGone, `{"error":"cursor expired"}`), nil
			}
			response := jsonResponse(http.StatusOK, `[]`)
			response.Header.Set("Link", `<https://hachyderm.io/api/v1/bookmarks?max_id=stale>; rel="next"`)
			return response, nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{
		AccountKey:        "hachyderm",
		CanonicalOrigin:   client.Origin,
		VerifiedAccountID: "42",
		BackfillNextURL:   strings.TrimRight(client.Origin, "/") + "/api/v1/bookmarks?max_id=stale",
		LastSuccessAt:     now,
	}); err != nil {
		t.Fatalf("seed sync state: %v", err)
	}

	_, err = RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return now }})
	if err == nil || !strings.Contains(err.Error(), "remained stale after recovery") {
		t.Fatalf("stale recovery error = %v", err)
	}
	if bookmarksCalls != 3 {
		t.Fatalf("bookmarks calls = %d, want initial stale, head, and guarded stale", bookmarksCalls)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || state.VerifiedAccountID != "42" || state.LastError == "" || state.BackfillNextURL != "" || state.BackfillPageURL != "" || state.BackfillPageOffset != 0 {
		t.Fatalf("state after stale recovery = %#v, err=%v", state, err)
	}
}

func TestRunBookmarksResumedPageLimitCountsSkippedStatuses(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	page := `[{"id":"local-1","uri":"https://hachyderm.io/users/alice/statuses/1","url":"https://hachyderm.io/@alice/1","content":"one","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"alice"}},` +
		`{"id":"local-2","uri":"https://hachyderm.io/users/alice/statuses/2","url":"https://hachyderm.io/@alice/2","content":"","created_at":"2026-08-08T11:00:00Z","account":{"id":"42","username":"alice"}},` +
		`{"id":"local-3","uri":"https://hachyderm.io/users/alice/statuses/3","url":"https://hachyderm.io/@alice/3","content":"three","created_at":"2026-08-08T10:00:00Z","account":{"id":"42","username":"alice"}}]`
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice","acct":"alice@hachyderm.io"}`), nil
		case "/api/v1/bookmarks":
			return jsonResponse(http.StatusOK, page), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	first, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("first limited import: %v", err)
	}
	if first.Seen != 1 || first.Processed != 1 {
		t.Fatalf("first stats = %#v", first)
	}
	second, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("resumed limited import: %v", err)
	}
	if second.Seen != 1 || second.Processed != 0 || second.Skipped != 1 || second.StoppedReason != "limit reached" {
		t.Fatalf("resumed stats = %#v", second)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || state.BackfillPageOffset != 2 {
		t.Fatalf("resumed state = %#v, err=%v", state, err)
	}
	third, err := RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Limit: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("final resumed import: %v", err)
	}
	if third.Seen != 1 || third.Processed != 1 || third.StoppedReason != "backfill complete" {
		t.Fatalf("final stats = %#v", third)
	}
}

func TestRunBookmarksStaleResetRejectsConcurrentVerifiedIdentityChange(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "stale" {
				if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{
					AccountKey:        "hachyderm",
					CanonicalOrigin:   "https://hachyderm.io:443",
					VerifiedAccountID: "99",
					BackfillNextURL:   strings.TrimRight(request.URL.Scheme+"://"+request.URL.Host, "/") + "/api/v1/bookmarks?max_id=other",
				}); err != nil {
					t.Fatalf("concurrent state replacement: %v", err)
				}
				return jsonResponse(http.StatusGone, `{"error":"cursor expired"}`), nil
			}
			return jsonResponse(http.StatusOK, `[]`), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{
		AccountKey:        "hachyderm",
		CanonicalOrigin:   client.Origin,
		VerifiedAccountID: "42",
		BackfillNextURL:   strings.TrimRight(client.Origin, "/") + "/api/v1/bookmarks?max_id=stale",
	}); err != nil {
		t.Fatalf("seed sync state: %v", err)
	}

	_, err = RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC) }})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("stale reset identity error = %v", err)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || state.VerifiedAccountID != "99" {
		t.Fatalf("state after rejected stale reset = %#v, err=%v", state, err)
	}
}

func TestRunBookmarksCheckpointRejectsIdentityChangedAfterStaleReset(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	head := false
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			return jsonResponse(http.StatusOK, `{"id":"42","username":"alice"}`), nil
		case "/api/v1/bookmarks":
			if request.URL.Query().Get("max_id") == "stale" {
				return jsonResponse(http.StatusGone, `{"error":"cursor expired"}`), nil
			}
			if !head {
				head = true
				if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{AccountKey: "hachyderm", CanonicalOrigin: "https://hachyderm.io:443", VerifiedAccountID: "99", BackfillNextURL: "replacement"}); err != nil {
					t.Fatalf("concurrent state replacement: %v", err)
				}
			}
			return jsonResponse(http.StatusOK, `[]`), nil
		default:
			t.Fatalf("unexpected API path %s", request.URL.Path)
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := st.UpsertMastodonSyncState(context.Background(), store.MastodonSyncState{AccountKey: "hachyderm", CanonicalOrigin: client.Origin, VerifiedAccountID: "42", BackfillNextURL: strings.TrimRight(client.Origin, "/") + "/api/v1/bookmarks?max_id=stale"}); err != nil {
		t.Fatalf("seed sync state: %v", err)
	}

	_, err = RunBookmarksWithClient(context.Background(), cfg, st, client, BookmarkOptions{AccountKey: "hachyderm", Now: func() time.Time { return time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC) }})
	if !errors.Is(err, store.ErrMastodonSyncStateChanged) {
		t.Fatalf("checkpoint after identity replacement error = %v, want %v", err, store.ErrMastodonSyncStateChanged)
	}
	state, err := st.GetMastodonSyncState(context.Background(), "hachyderm", client.Origin)
	if err != nil || state == nil || state.VerifiedAccountID != "99" || state.BackfillNextURL != "replacement" {
		t.Fatalf("checkpoint overwrote concurrent replacement: state=%#v err=%v", state, err)
	}
}
