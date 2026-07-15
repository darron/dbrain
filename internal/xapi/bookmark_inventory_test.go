package xapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

func newBookmarkAuditTestServer(t *testing.T, handler http.Handler) (*httptest.Server, bookmarkAuditHTTPInjections) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.InsecureSkipVerify = true //nolint:gosec // test server beneath fixed x.com safehttp policy
	return server, bookmarkAuditHTTPInjections{
		LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSClientConfig: tlsConfig,
	}
}

func TestBookmarkAuditInventoryRejectsBlankAndCyclicCursors(t *testing.T) {
	t.Parallel()

	t.Run("blank", func(t *testing.T) {
		blank := ""
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111"}, &blank))
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "secret-ct0"}, nil, injected)
		got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
		if !errors.Is(err, audit.ErrInventoryInvalid) || got.Complete {
			t.Fatalf("blank cursor result = %#v, err=%v", got, err)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		cursor := "private-cursor"
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111"}, &cursor))
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "secret-ct0"}, nil, injected)
		got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
		if !errors.Is(err, audit.ErrInventoryInvalid) || got.Complete || got.PageCount != 2 {
			t.Fatalf("cycle result = %#v, err=%v", got, err)
		}
		if strings.Contains(err.Error(), cursor) || strings.Contains(err.Error(), "secret-ct0") {
			t.Fatalf("cycle error leaked cursor/session: %v", err)
		}
	})
}

func TestBookmarkAuditInventoryDeduplicatesAndProvesCaps(t *testing.T) {
	t.Parallel()

	t.Run("duplicates do not consume identity cap", func(t *testing.T) {
		cursor := "terminal"
		requests := 0
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			if requests == 1 {
				_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111", "111"}, &cursor))
				return
			}
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload(nil, nil))
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 2})
		if err != nil || !got.Complete || got.PageCount != 2 || len(got.IdentityHashes) != 1 {
			t.Fatalf("dedupe result = %#v, err=%v", got, err)
		}
	})

	t.Run("cap plus one duplicate page can prove completion", func(t *testing.T) {
		cursor := "next"
		terminal := "terminal"
		requests := 0
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			if requests == 1 {
				_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111"}, &cursor))
				return
			}
			if requests == 2 {
				_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111"}, &terminal))
				return
			}
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload(nil, nil))
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 3})
		if err != nil || !got.Complete || got.PageCount != 3 || len(got.IdentityHashes) != 1 {
			t.Fatalf("cap+1 completion = %#v, err=%v", got, err)
		}
	})

	t.Run("new identity beyond cap fails incomplete", func(t *testing.T) {
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111", "222"}, nil))
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
		if !errors.Is(err, audit.ErrInventoryBudget) || got.Complete || len(got.IdentityHashes) != 1 {
			t.Fatalf("identity cap = %#v, err=%v", got, err)
		}
	})

	t.Run("cursor beyond page cap fails without extra request", func(t *testing.T) {
		cursor := "next"
		requests := 0
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111"}, &cursor))
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
		if !errors.Is(err, audit.ErrInventoryBudget) || got.Complete || got.PageCount != 1 || requests != 1 {
			t.Fatalf("page cap = %#v, requests=%d err=%v", got, requests, err)
		}
	})

	t.Run("invalid caller caps fail before session resolution", func(t *testing.T) {
		resolverCalls := 0
		inventory := newBookmarkAuditInventory(BookmarkOptions{}, func(context.Context, Options) (string, string, error) {
			resolverCalls++
			return "ct0", "", nil
		}, bookmarkAuditHTTPInjections{})
		budgets := []audit.InventoryBudget{{}, {MaxIdentities: audit.InventoryMaxIdentities + 1, MaxPages: 1}, {MaxIdentities: 1, MaxPages: audit.InventoryMaxPages + 1}}
		for _, budget := range budgets {
			if _, err := inventory.Inventory(t.Context(), budget); !errors.Is(err, audit.ErrInventoryInvalid) {
				t.Fatalf("budget %#v error=%v, want invalid", budget, err)
			}
		}
		if resolverCalls != 0 {
			t.Fatalf("resolver called %d times", resolverCalls)
		}
	})
}

func TestBookmarkAuditInventoryFailsClosedWithoutLeaks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "null response", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "null") }},
		{name: "trailing JSON", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{} private-body`) }},
		{name: "missing timeline", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }},
		{name: "invalid tweet entry", handler: func(w http.ResponseWriter, _ *http.Request) {
			payload := bookmarkAuditPayload([]string{"private-tweet-id"}, nil)
			_ = json.NewEncoder(w).Encode(payload)
		}},
		{name: "multiple cursors", handler: func(w http.ResponseWriter, _ *http.Request) {
			one, two := "one", "two"
			payload := bookmarkAuditPayload(nil, &one)
			instructions := payload["data"].(map[string]any)["bookmark_timeline_v2"].(map[string]any)["timeline"].(map[string]any)["instructions"].([]any)
			entries := instructions[0].(map[string]any)["entries"].([]any)
			instructions[0].(map[string]any)["entries"] = append(entries, map[string]any{"entryId": "cursor-bottom-two", "content": map[string]any{"value": two}})
			_ = json.NewEncoder(w).Encode(payload)
		}},
		{name: "oversized body", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", bookmarkAuditMaxBodyBytes+1))
		}},
		{name: "non success", handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "private-body", http.StatusForbidden) }},
		{name: "redirect", handler: func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "https://evil.example/private-tweet-id", http.StatusFound)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, injected := newBookmarkAuditTestServer(t, test.handler)
			inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "secret-ct0", AuthToken: "secret-auth"}, nil, injected)
			got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
			if err == nil || got.Complete {
				t.Fatalf("result=%#v err=%v, want incomplete failure", got, err)
			}
			for _, private := range []string{"secret-ct0", "secret-auth", "private-body", "private-tweet-id", "evil.example"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("error leaked %q: %v", private, err)
				}
			}
		})
	}
}

func TestBookmarkAuditInventoryRejectsTopLevelErrorsBeforeUsingPartialData(t *testing.T) {
	t.Parallel()

	cursor := "next"
	requests := 0
	_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		payload := bookmarkAuditPayload([]string{"111"}, &cursor)
		payload["errors"] = []any{map[string]any{"message": "private-body"}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "secret-ct0"}, nil, injected)
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if !errors.Is(err, audit.ErrInventoryInvalid) || got.Complete || got.PageCount != 0 || len(got.IdentityHashes) != 0 || requests != 1 {
		t.Fatalf("partial GraphQL error result=%#v requests=%d err=%v", got, requests, err)
	}
	if strings.Contains(err.Error(), "private-body") || strings.Contains(err.Error(), "secret-ct0") {
		t.Fatalf("partial GraphQL error leaked private data: %v", err)
	}
}

func TestBookmarkAuditInventoryRejectsNonEmptyCursorlessPages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "partial page", ids: []string{"111"}},
		{name: "full page", ids: func() []string {
			ids := make([]string, bookmarkAuditPageSize)
			for index := range ids {
				ids[index] = fmt.Sprint(index + 1)
			}
			return ids
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(bookmarkAuditPayload(test.ids, nil))
			}))
			inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
			got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
			if !errors.Is(err, audit.ErrInventoryIncomplete) || got.Complete || got.PageCount != 1 {
				t.Fatalf("cursorless %s result=%#v err=%v", test.name, got, err)
			}
		})
	}
}

func TestBookmarkAuditInventoryPassesNormalizedTimeoutToLazyResolver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input time.Duration
		want  time.Duration
	}{
		{name: "default", want: bookmarkAuditMaxRequestTime},
		{name: "custom", input: 3 * time.Second, want: 3 * time.Second},
		{name: "clamped", input: time.Minute, want: bookmarkAuditMaxRequestTime},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var resolved Options
			inventory := newBookmarkAuditInventory(BookmarkOptions{Timeout: test.input}, func(_ context.Context, opts Options) (string, string, error) {
				resolved = opts
				return "", "", errors.New("stop before network")
			}, bookmarkAuditHTTPInjections{})
			implementation := inventory.(*bookmarkAuditInventory)
			if implementation.opts.Timeout != test.want {
				t.Fatalf("stored timeout=%v, want %v", implementation.opts.Timeout, test.want)
			}
			_, _ = inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
			if resolved.Timeout != test.want {
				t.Fatalf("resolver timeout=%v, want %v", resolved.Timeout, test.want)
			}
		})
	}
}

func TestBookmarkAuditInventoryCancellationAndResolverFailureAreSafe(t *testing.T) {
	t.Parallel()

	t.Run("resolver error", func(t *testing.T) {
		networkCalls := 0
		inventory := newBookmarkAuditInventory(BookmarkOptions{}, func(context.Context, Options) (string, string, error) {
			return "", "", errors.New("private-cookie-path")
		}, bookmarkAuditHTTPInjections{LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			networkCalls++
			return nil, errors.New("unexpected")
		}})
		got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
		if err == nil || got.Complete || strings.Contains(err.Error(), "private-cookie-path") || networkCalls != 0 {
			t.Fatalf("resolver result=%#v network=%d err=%v", got, networkCalls, err)
		}
	})

	t.Run("parent cancellation", func(t *testing.T) {
		started := make(chan struct{})
		_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			close(started)
			<-req.Context().Done()
		}))
		inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
		ctx, cancel := context.WithCancel(t.Context())
		type outcome struct {
			result audit.InventoryResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := inventory.Inventory(ctx, audit.DefaultInventoryBudget())
			done <- outcome{result: result, err: err}
		}()
		<-started
		cancel()
		got := <-done
		if !errors.Is(got.err, context.Canceled) || got.result.Complete {
			t.Fatalf("cancel result=%#v err=%v", got.result, got.err)
		}
	})
}

func TestBookmarkAuditInventoryIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")

	_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(bookmarkAuditPayload(nil, nil))
	}))
	inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil || !got.Complete {
		t.Fatalf("proxy-free result=%#v err=%v", got, err)
	}
}

func TestBookmarkAuditInventoryRejectsOversizedParsedPage(t *testing.T) {
	t.Parallel()

	ids := make([]string, bookmarkAuditPageSize+1)
	for index := range ids {
		ids[index] = fmt.Sprint(index + 1)
	}
	_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(bookmarkAuditPayload(ids, nil))
	}))
	inventory := newBookmarkAuditInventory(BookmarkOptions{CT0: "ct0"}, nil, injected)
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if !errors.Is(err, audit.ErrInventoryInvalid) || got.Complete {
		t.Fatalf("oversized page=%#v err=%v", got, err)
	}
}

func bookmarkAuditPayload(tweetIDs []string, cursor *string) map[string]any {
	entries := make([]any, 0, len(tweetIDs)+1)
	for _, tweetID := range tweetIDs {
		entries = append(entries, map[string]any{
			"entryId": "tweet-" + tweetID,
			"content": map[string]any{"itemContent": map[string]any{"tweet_results": map[string]any{"result": map[string]any{
				"rest_id": tweetID,
				"legacy":  map[string]any{"id_str": tweetID, "full_text": "private tweet body"},
			}}}},
		})
	}
	if cursor != nil {
		entries = append(entries, map[string]any{"entryId": "cursor-bottom-test", "content": map[string]any{"value": *cursor}})
	}
	return map[string]any{"data": map[string]any{"bookmark_timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
		map[string]any{"type": "TimelineAddEntries", "entries": entries},
	}}}}}
}

func TestBookmarkSourceKeyIsSharedWithNormalImporter(t *testing.T) {
	t.Parallel()

	got, err := bookmarkSourceKey(" 12345 ")
	if err != nil || got != "x:12345" {
		t.Fatalf("bookmarkSourceKey = %q, %v", got, err)
	}
	item, err := bookmarkRecordToItem(bookmarkRecord{TweetID: " 12345 "}, time.Unix(0, 0).UTC())
	if err != nil || item.SourceKey != got {
		t.Fatalf("normal importer source key = %q, %v; want %q", item.SourceKey, err, got)
	}
	for _, invalid := range []string{"", "not-a-tweet", "12/34"} {
		if _, err := bookmarkSourceKey(invalid); err == nil {
			t.Fatalf("bookmarkSourceKey(%q) succeeded", invalid)
		}
	}
}

func TestBookmarkAuditInventoryTraversesAllCursorsAtFixedAuthority(t *testing.T) {
	t.Parallel()

	firstCursor := "cursor-private-1"
	secondCursor := "cursor-private-2"
	requests := 0
	_, injected := newBookmarkAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Host != "x.com" || req.TLS == nil || req.URL.Path != "/i/api/graphql/"+bookmarksQueryID+"/"+bookmarksOperation {
			t.Fatalf("request escaped fixed x.com GraphQL authority: host=%q tls=%t path=%q", req.Host, req.TLS != nil, req.URL.Path)
		}
		if req.Header.Get("X-Csrf-Token") != "secret-ct0" || req.Header.Get("Cookie") != "ct0=secret-ct0; auth_token=secret-auth" {
			t.Fatalf("native session headers missing")
		}
		var variables map[string]any
		if err := json.Unmarshal([]byte(req.URL.Query().Get("variables")), &variables); err != nil {
			t.Fatalf("variables: %v", err)
		}
		if variables["count"] != float64(100) {
			t.Fatalf("page size = %#v, want 100", variables["count"])
		}
		if requests == 1 {
			if _, exists := variables["cursor"]; exists {
				t.Fatal("first request unexpectedly had cursor")
			}
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"111"}, &firstCursor))
			return
		}
		if requests == 2 {
			if variables["cursor"] != firstCursor {
				t.Fatalf("second cursor = %#v", variables["cursor"])
			}
			_ = json.NewEncoder(w).Encode(bookmarkAuditPayload([]string{"222"}, &secondCursor))
			return
		}
		if variables["cursor"] != secondCursor {
			t.Fatalf("terminal cursor = %#v", variables["cursor"])
		}
		_ = json.NewEncoder(w).Encode(bookmarkAuditPayload(nil, nil))
	}))
	resolveCalls := 0
	inventory := newBookmarkAuditInventory(BookmarkOptions{}, func(context.Context, Options) (string, string, error) {
		resolveCalls++
		return "secret-ct0", "secret-auth", nil
	}, injected)
	if resolveCalls != 0 {
		t.Fatal("cookies resolved before Inventory")
	}
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if resolveCalls != 1 || requests != 3 || !got.Complete || got.PageCount != 3 {
		t.Fatalf("result = %#v, resolver=%d requests=%d", got, resolveCalls, requests)
	}
	want := make([]string, 0, 2)
	for _, id := range []string{"111", "222"} {
		sourceKey, _ := bookmarkSourceKey(id)
		hash, _ := audit.HashUpstreamIdentity(audit.SourceXBookmarks, sourceKey)
		want = append(want, hash)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got.IdentityHashes, want) {
		t.Fatalf("identity hashes = %#v, want %#v", got.IdentityHashes, want)
	}
}
