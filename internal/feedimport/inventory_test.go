package feedimport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

type auditFeedFetcher struct {
	results map[string]FetchResult
	errs    map[string]error
	feeds   []store.Feed
	opts    []Options
	before  func(context.Context, store.Feed) error
}

func (f *auditFeedFetcher) Fetch(ctx context.Context, feed store.Feed, opts Options) (FetchResult, error) {
	f.feeds = append(f.feeds, feed)
	f.opts = append(f.opts, opts)
	if f.before != nil {
		if err := f.before(ctx, feed); err != nil {
			return FetchResult{}, err
		}
	}
	return f.results[feed.FeedKey], f.errs[feed.FeedKey]
}

func TestFeedAuditInventoryUsesEnabledConfiguredFeedsForceFetchAndSharedAliases(t *testing.T) {
	feeds := []store.Feed{
		{FeedKey: "feed:rss", URL: "https://rss.example.test/feed", Enabled: true, FetchETag: `"cached"`, FetchBodyHash: "cached-hash"},
		{FeedKey: "feed:disabled", URL: "https://disabled.example.test/feed", Enabled: false},
		{FeedKey: "feed:atom", ResolvedURL: "https://atom.example.test/feed", Enabled: true},
	}
	original := append([]store.Feed(nil), feeds...)
	fetcher := &auditFeedFetcher{results: map[string]FetchResult{
		"feed:rss":  {HTTPStatus: http.StatusOK, DecodedBody: []byte(`<rss version="2.0"><channel><title>RSS</title><item><guid>same</guid><link>https://example.test/posts/one?b=2&amp;a=1</link><title>One</title></item></channel></rss>`)},
		"feed:atom": {HTTPStatus: http.StatusOK, DecodedBody: []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom</title><entry><id>atom-1</id><link href="https://example.test/atom/one"/><title>Atom One</title></entry></feed>`)},
	}}
	inventory := newAuditInventory(feeds, "audit-agent", fetcher)
	result, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.PageCount != 2 {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(feeds, original) {
		t.Fatalf("constructor mutated configured feed rows: got %#v want %#v", feeds, original)
	}
	if got := []string{fetcher.feeds[0].FeedKey, fetcher.feeds[1].FeedKey}; !reflect.DeepEqual(got, []string{"feed:rss", "feed:atom"}) {
		t.Fatalf("fetched feeds = %#v", got)
	}
	for _, opts := range fetcher.opts {
		if !opts.Force || opts.UserAgent != "audit-agent" || opts.Timeout != DefaultTimeout || opts.MaxBodyBytes != DefaultMaxBodyBytes {
			t.Fatalf("audit fetch options = %#v", opts)
		}
	}
	want := map[string]bool{}
	for _, value := range []struct{ feedKey, identity string }{
		{"feed:rss", "guid:same"},
		{"feed:rss", "link:https://example.test/posts/one?a=1&b=2"},
		{"feed:atom", "guid:atom-1"},
		{"feed:atom", "link:https://example.test/atom/one"},
	} {
		hash, hashErr := audit.HashUpstreamFeedIdentity(value.feedKey, value.identity)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		want[hash] = true
	}
	if len(result.IdentityHashes) != len(want) {
		t.Fatalf("identity hashes = %d, want %d: %#v", len(result.IdentityHashes), len(want), result.IdentityHashes)
	}
	for _, hash := range result.IdentityHashes {
		if !want[hash] {
			t.Fatalf("unexpected identity hash %q", hash)
		}
	}
}

func TestFeedItemIdentityAliasesShareImporterPrimaryAndStoreEvolutionAliases(t *testing.T) {
	parsed := newParser(t, []byte(`<rss version="2.0"><channel><title>Example</title><item><guid>guid-1</guid><link>https://example.test/post?z=2&amp;a=1</link><title>Post</title></item></channel></rss>`))
	item := parsed.Items[0]
	aliases := feedItemIdentityAliases(item, "", "", "")
	if got, want := aliases, []string{"guid:guid-1", "link:https://example.test/post?a=1&z=2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
	if primary := feedItemIdentity(item, "", "", ""); primary != aliases[0] {
		t.Fatalf("normal importer primary = %q, audit primary = %q", primary, aliases[0])
	}
}

func TestFeedAuditInventoryEnforcesPageAndUniqueIdentityCapsAfterDedupe(t *testing.T) {
	body := func(items string) []byte {
		return []byte(`<rss version="2.0"><channel><title>Feed</title>` + items + `</channel></rss>`)
	}
	feeds := []store.Feed{{FeedKey: "feed:one", URL: "https://one.test/feed", Enabled: true}, {FeedKey: "feed:two", URL: "https://two.test/feed", Enabled: true}}
	t.Run("page cap requires observing all configured feeds", func(t *testing.T) {
		fetcher := &auditFeedFetcher{results: map[string]FetchResult{
			"feed:one": {HTTPStatus: 200, DecodedBody: body(`<item><guid>one</guid></item>`)},
			"feed:two": {HTTPStatus: 200, DecodedBody: body(`<item><guid>two</guid></item>`)},
		}}
		result, err := newAuditInventory(feeds, "agent", fetcher).Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 1})
		if !errors.Is(err, audit.ErrInventoryBudget) || result.Complete || result.PageCount != 1 || len(fetcher.feeds) != 1 {
			t.Fatalf("result=%#v err=%v fetches=%d", result, err, len(fetcher.feeds))
		}
	})
	t.Run("duplicates do not consume unique cap and cap plus one is incomplete", func(t *testing.T) {
		fetcher := &auditFeedFetcher{results: map[string]FetchResult{
			"feed:one": {HTTPStatus: 200, DecodedBody: body(`<item><guid>one</guid></item><item><guid>one</guid></item><item><guid>two</guid></item><item><guid>three</guid></item>`)},
		}}
		result, err := newAuditInventory(feeds[:1], "agent", fetcher).Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 2, MaxPages: 1})
		if !errors.Is(err, audit.ErrInventoryBudget) || result.Complete || result.PageCount != 1 || len(result.IdentityHashes) != 2 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
	t.Run("exact unique cap at natural end is complete", func(t *testing.T) {
		fetcher := &auditFeedFetcher{results: map[string]FetchResult{
			"feed:one": {HTTPStatus: 200, DecodedBody: body(`<item><guid>one</guid></item><item><guid>one</guid></item><item><guid>two</guid></item>`)},
		}}
		result, err := newAuditInventory(feeds[:1], "agent", fetcher).Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 2, MaxPages: 1})
		if err != nil || !result.Complete || len(result.IdentityHashes) != 2 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
}

func TestFeedAuditInventoryFailureAndCancellationAreWholeInventoryAndPrivacySafe(t *testing.T) {
	secretURL := "https://user:secret@example.test/private-feed"
	feeds := []store.Feed{{FeedKey: "feed:private-name", URL: secretURL, Enabled: true}, {FeedKey: "feed:later", URL: "https://later.test/feed", Enabled: true}}
	t.Run("fetch error", func(t *testing.T) {
		fetcher := &auditFeedFetcher{results: map[string]FetchResult{}, errs: map[string]error{"feed:private-name": fmt.Errorf("fetch %s: SECRET_BODY", secretURL)}}
		result, err := newAuditInventory(feeds, "agent", fetcher).Inventory(t.Context(), audit.DefaultInventoryBudget())
		if err == nil || result.Complete || len(fetcher.feeds) != 1 {
			t.Fatalf("result=%#v err=%v fetches=%d", result, err, len(fetcher.feeds))
		}
		for _, secret := range []string{secretURL, "secret", "SECRET_BODY", "private-name"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked %q: %v", secret, err)
			}
		}
	})
	t.Run("parse error", func(t *testing.T) {
		fetcher := &auditFeedFetcher{results: map[string]FetchResult{"feed:private-name": {HTTPStatus: 200, DecodedBody: []byte(`not a feed SECRET_BODY`)}}}
		_, err := newAuditInventory(feeds[:1], "agent", fetcher).Inventory(t.Context(), audit.DefaultInventoryBudget())
		if !errors.Is(err, audit.ErrInventoryInvalid) || strings.Contains(err.Error(), "SECRET_BODY") || strings.Contains(err.Error(), "private-name") {
			t.Fatalf("parse error = %v", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		fetcher := &auditFeedFetcher{results: map[string]FetchResult{}, before: func(_ context.Context, _ store.Feed) error {
			cancel()
			return context.Canceled
		}}
		_, err := newAuditInventory(feeds, "agent", fetcher).Inventory(ctx, audit.DefaultInventoryBudget())
		if !errors.Is(err, context.Canceled) || len(fetcher.feeds) != 1 {
			t.Fatalf("error=%v fetches=%d", err, len(fetcher.feeds))
		}
	})
}

func TestFeedAuditHTTPPolicyRejectsUnsafeDestinationsAndProxyButAllowsExactConfiguredPrivateOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-private" {
			http.Redirect(w, r, "http://other-private.test/feed", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>Safe</title></channel></rss>`))
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
		switch host {
		case "public.test":
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		case "mixed.test":
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
		default:
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
	}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, serverURL.Host)
	}
	injected := feedAuditHTTPInjections{LookupNetIP: lookup, DialContext: dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:9")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:9")

	publicFeed := store.Feed{FeedKey: "feed:public", URL: "http://public.test:" + serverURL.Port() + "/feed", Enabled: true}
	publicFetcher := newFeedAuditHTTPFetcher([]store.Feed{publicFeed}, false, injected)
	if _, err := publicFetcher.Fetch(t.Context(), publicFeed, Options{Force: true, Timeout: DefaultTimeout, MaxBodyBytes: DefaultMaxBodyBytes}); err != nil {
		t.Fatalf("public no-proxy fetch: %v", err)
	}
	for _, raw := range []string{
		"http://127.0.0.1:" + serverURL.Port() + "/feed",
		"http://[::1]:" + serverURL.Port() + "/feed",
		"http://169.254.1.1:" + serverURL.Port() + "/feed",
		"http://mixed.test:" + serverURL.Port() + "/feed",
		"http://user:secret@public.test:" + serverURL.Port() + "/feed",
	} {
		feed := store.Feed{FeedKey: "feed:unsafe", URL: raw, Enabled: true}
		fetcher := newFeedAuditHTTPFetcher([]store.Feed{feed}, false, injected)
		if _, err := fetcher.Fetch(t.Context(), feed, Options{Force: true, Timeout: DefaultTimeout, MaxBodyBytes: DefaultMaxBodyBytes}); !safehttp.IsPolicyError(err) {
			t.Fatalf("unsafe URL %q error = %v", raw, err)
		}
	}
	privateFeed := store.Feed{FeedKey: "feed:private", URL: "http://private.test:" + serverURL.Port() + "/feed", Enabled: true}
	privateFetcher := newFeedAuditHTTPFetcher([]store.Feed{privateFeed}, true, injected)
	if _, err := privateFetcher.Fetch(t.Context(), privateFeed, Options{Force: true, Timeout: DefaultTimeout, MaxBodyBytes: DefaultMaxBodyBytes}); err != nil {
		t.Fatalf("exact configured private origin: %v", err)
	}
	redirectFeed := store.Feed{FeedKey: "feed:redirect", URL: "http://private.test:" + serverURL.Port() + "/redirect-private", Enabled: true}
	redirectFetcher := newFeedAuditHTTPFetcher([]store.Feed{redirectFeed}, true, injected)
	if _, err := redirectFetcher.Fetch(t.Context(), redirectFeed, Options{Force: true, Timeout: DefaultTimeout, MaxBodyBytes: DefaultMaxBodyBytes}); !safehttp.IsPolicyError(err) {
		t.Fatalf("redirect to unconfigured private origin error = %v", err)
	}
}

func TestFeedAuditInventoryRejectsInvalidBudgetsBeforeFetch(t *testing.T) {
	feed := store.Feed{FeedKey: "feed:one", URL: "https://example.test/feed", Enabled: true}
	fetcher := &auditFeedFetcher{results: map[string]FetchResult{}}
	for _, budget := range []audit.InventoryBudget{
		{},
		{MaxIdentities: audit.InventoryMaxIdentities + 1, MaxPages: 1},
		{MaxIdentities: 1, MaxPages: audit.InventoryMaxPages + 1},
	} {
		if _, err := newAuditInventory([]store.Feed{feed}, "agent", fetcher).Inventory(t.Context(), budget); !errors.Is(err, audit.ErrInventoryInvalid) {
			t.Fatalf("budget %#v error = %v", budget, err)
		}
	}
	if len(fetcher.feeds) != 0 {
		t.Fatalf("invalid budget fetched %d feeds", len(fetcher.feeds))
	}
}
