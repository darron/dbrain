package feedimport

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

type fakeFetcher struct {
	results []FetchResult
	errs    []error
	feeds   []store.Feed
	calls   int
}

func (f *fakeFetcher) Fetch(_ context.Context, feed store.Feed, _ Options) (FetchResult, error) {
	i := f.calls
	f.calls++
	f.feeds = append(f.feeds, feed)
	if i >= len(f.results) {
		return FetchResult{HTTPStatus: http.StatusNotModified, NotModified: true}, nil
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return f.results[i], err
}

func TestRunMaterializesFeedEntryAndUnchangedBodySkipsEntries(t *testing.T) {
	ctx := context.Background()
	cfg, st := openFeedTestStore(t)
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Feed</title>
    <link>https://example.com/</link>
    <description>Feed description</description>
    <item>
      <guid>post-1</guid>
      <title>First post</title>
      <link>https://example.com/post-1</link>
      <pubDate>Wed, 06 May 2026 02:03:59 GMT</pubDate>
      <description><![CDATA[<p>Hello <strong>world</strong>.</p>]]></description>
    </item>
  </channel>
</rss>`)
	hash := sha256Hex(body)
	fetcher := &fakeFetcher{results: []FetchResult{
		{
			RequestURL:        "https://example.com/feed.xml",
			FinalURL:          "https://example.com/feed.xml",
			HTTPStatus:        200,
			DecodedBody:       body,
			DecodedBodyHash:   hash,
			WireResponseBytes: body,
			DecodedSizeBytes:  int64(len(body)),
			ETag:              `"v1"`,
		},
		{
			RequestURL:      "https://example.com/feed.xml",
			FinalURL:        "https://example.com/feed.xml",
			HTTPStatus:      200,
			DecodedBodyHash: hash,
			UnchangedBody:   true,
			ETag:            `"v1"`,
		},
	}}

	feed, created, stats, err := Add(ctx, cfg, st, "https://example.com/feed.xml", AddOptions{
		Fetch:   true,
		Import:  true,
		Fetcher: fetcher,
		Now:     fixedNow,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !created {
		t.Fatal("expected new feed")
	}
	if stats.ItemsCreated != 1 || stats.EntriesSeen != 1 || stats.SourcesCreated != 1 || stats.SourcesLinked != 1 {
		t.Fatalf("unexpected add stats: %+v", stats)
	}
	item, err := st.GetItem(ctx, "feed-entry:"+shortHash(feed.FeedKey+"|guid:post-1"))
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Title != "First post" || item.SourceType != "feed_entry" {
		t.Fatalf("unexpected item: %+v", item)
	}

	stats, err = CheckFeed(ctx, cfg, st, feed, Options{Fetcher: fetcher, Now: fixedNow})
	if err != nil {
		t.Fatalf("CheckFeed unchanged: %v", err)
	}
	if stats.FeedsUnchanged != 1 || stats.EntriesSeen != 0 {
		t.Fatalf("expected unchanged feed without entry processing, got %+v", stats)
	}
}

func TestAddFetchesMetadataWithoutImportUnlessRequested(t *testing.T) {
	ctx := context.Background()
	cfg, st := openFeedTestStore(t)
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Example Feed</title><link>https://example.com/</link>
<item><guid>post-1</guid><title>First post</title><link>https://example.com/post-1</link></item>
</channel></rss>`)
	fetcher := &fakeFetcher{results: []FetchResult{{
		RequestURL:        "https://example.com/feed.xml",
		FinalURL:          "https://example.com/feed.xml",
		HTTPStatus:        200,
		DecodedBody:       body,
		DecodedBodyHash:   sha256Hex(body),
		WireResponseBytes: body,
		DecodedSizeBytes:  int64(len(body)),
	}}}

	feed, _, stats, err := Add(ctx, cfg, st, "https://example.com/feed.xml", AddOptions{
		Fetch:   true,
		Import:  false,
		Fetcher: fetcher,
		Now:     fixedNow,
	})
	if err != nil {
		t.Fatalf("Add verify-only: %v", err)
	}
	if stats.FeedsChanged != 1 || stats.ItemsCreated != 0 || stats.EntriesSeen != 0 {
		t.Fatalf("expected metadata fetch without entry import, got %+v", stats)
	}
	if feed.Title != "Example Feed" {
		t.Fatalf("feed title = %q", feed.Title)
	}
	if _, err := st.GetItem(ctx, "feed-entry:"+shortHash(feed.FeedKey+"|guid:post-1")); err == nil {
		t.Fatal("expected verify-only add not to materialize feed entry item")
	}
}

func TestRunImportsAtomAndJSONFeedFixtures(t *testing.T) {
	cases := []struct {
		name string
		url  string
		body string
		key  string
	}{
		{
			name: "atom",
			url:  "https://example.com/atom.xml",
			body: `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Feed</title><link href="https://example.com/"/><updated>2026-05-09T00:00:00Z</updated>
  <entry><id>atom-1</id><title>Atom post</title><link href="https://example.com/atom-1"/><updated>2026-05-09T00:00:00Z</updated><content>Atom body</content></entry>
</feed>`,
			key: "guid:atom-1",
		},
		{
			name: "json",
			url:  "https://example.com/feed.json",
			body: `{"version":"https://jsonfeed.org/version/1.1","title":"JSON Feed","home_page_url":"https://example.com/","items":[{"id":"json-1","url":"https://example.com/json-1","title":"JSON post","content_text":"JSON body"}]}`,
			key:  "guid:json-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cfg, st := openFeedTestStore(t)
			body := []byte(tc.body)
			fetcher := &fakeFetcher{results: []FetchResult{{
				RequestURL:        tc.url,
				FinalURL:          tc.url,
				HTTPStatus:        200,
				DecodedBody:       body,
				DecodedBodyHash:   sha256Hex(body),
				WireResponseBytes: body,
				DecodedSizeBytes:  int64(len(body)),
			}}}
			feed, _, stats, err := Add(ctx, cfg, st, tc.url, AddOptions{Fetch: true, Import: true, Fetcher: fetcher, Now: fixedNow})
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if stats.ItemsCreated != 1 || stats.EntriesSeen != 1 {
				t.Fatalf("unexpected stats: %+v", stats)
			}
			if _, err := st.GetItem(ctx, "feed-entry:"+shortHash(feed.FeedKey+"|"+tc.key)); err != nil {
				t.Fatalf("GetItem: %v", err)
			}
		})
	}
}

func TestFeedEntryContentHashUsesMarkdownExtensionWhitelist(t *testing.T) {
	bodyA := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:markdown="https://example.com/markdown">
  <channel><title>Example</title><link>https://example.com/</link>
    <item><guid>post-1</guid><title>Post</title><link>https://example.com/post</link><markdown:content type="text/markdown"># Markdown body</markdown:content></item>
  </channel>
</rss>`)
	bodyB := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:markdown="https://example.com/markdown">
  <channel><title>Example</title><link>https://example.com/</link>
    <item><guid>post-1</guid><title>Post</title><link>https://example.com/post</link><markdown:content type="text/markdown"># Changed body</markdown:content></item>
  </channel>
</rss>`)
	parser := newParser(t, bodyA)
	parser2 := newParser(t, bodyB)
	feed := store.Feed{ID: 1, FeedKey: "feed:test", NormalizedURL: "https://example.com/feed.xml"}
	entryA, ok := buildFeedEntry(feed, parser, parser.Items[0], fixedNow())
	if !ok {
		t.Fatal("entryA not built")
	}
	entryB, ok := buildFeedEntry(feed, parser2, parser2.Items[0], fixedNow())
	if !ok {
		t.Fatal("entryB not built")
	}
	if entryA.ContentMarkdown != "# Markdown body" {
		t.Fatalf("markdown = %q", entryA.ContentMarkdown)
	}
	if entryA.ContentHash == entryB.ContentHash {
		t.Fatal("expected markdown extension to affect content hash")
	}
}

func TestFallbackIdentityPrefersMarkdownAndTruncates(t *testing.T) {
	longA := strings.Repeat("a", 2048) + "x"
	longB := strings.Repeat("a", 2048) + "y"
	itemA := &gofeed.Item{Title: "Fallback", Custom: map[string]string{"markdown": longA}, Content: "<p>html A</p>"}
	itemB := &gofeed.Item{Title: "Fallback", Custom: map[string]string{"markdown": longB}, Content: "<p>html B</p>"}

	identityA := feedItemIdentity(itemA, extractMarkdown(nil, itemA.Custom), textFromHTML(itemA.Content), textFromHTML(itemA.Description))
	identityB := feedItemIdentity(itemB, extractMarkdown(nil, itemB.Custom), textFromHTML(itemB.Content), textFromHTML(itemB.Description))
	if identityA == "" {
		t.Fatal("expected fallback identity")
	}
	if identityA != identityB {
		t.Fatalf("expected identity to use truncated markdown fallback, got %q vs %q", identityA, identityB)
	}
}

func TestGUIDAndLinkChangesReuseExistingFeedEntry(t *testing.T) {
	ctx := context.Background()
	cfg, st := openFeedTestStore(t)
	body1 := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Example</title><link>https://example.com/</link>
<item><guid>old-guid</guid><title>Post</title><link>https://example.com/post</link><description>first</description></item>
</channel></rss>`)
	body2 := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Example</title><link>https://example.com/</link>
<item><guid>new-guid</guid><title>Post</title><link>https://example.com/post</link><description>changed</description></item>
</channel></rss>`)
	fetcher := &fakeFetcher{results: []FetchResult{
		{RequestURL: "https://example.com/feed.xml", FinalURL: "https://example.com/feed.xml", HTTPStatus: 200, DecodedBody: body1, DecodedBodyHash: sha256Hex(body1), WireResponseBytes: body1, DecodedSizeBytes: int64(len(body1))},
		{RequestURL: "https://example.com/feed.xml", FinalURL: "https://example.com/feed.xml", HTTPStatus: 200, DecodedBody: body2, DecodedBodyHash: sha256Hex(body2), WireResponseBytes: body2, DecodedSizeBytes: int64(len(body2))},
	}}
	feed, _, stats, err := Add(ctx, cfg, st, "https://example.com/feed.xml", AddOptions{Fetch: true, Import: true, Fetcher: fetcher, Now: fixedNow})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stats.ItemsCreated != 1 {
		t.Fatalf("expected initial item create, got %+v", stats)
	}

	stats, err = CheckFeed(ctx, cfg, st, feed, Options{Fetcher: fetcher, Now: func() time.Time { return fixedNow().Add(time.Hour) }})
	if err != nil {
		t.Fatalf("CheckFeed changed GUID: %v", err)
	}
	if stats.ItemsUpdated != 1 || stats.ItemsCreated != 0 {
		t.Fatalf("expected GUID change with stable link to update existing item, got %+v", stats)
	}
	oldKey := "feed-entry:" + shortHash(feed.FeedKey+"|guid:old-guid")
	newKey := "feed-entry:" + shortHash(feed.FeedKey+"|guid:new-guid")
	if _, err := st.GetItem(ctx, oldKey); err != nil {
		t.Fatalf("expected original item key to remain valid: %v", err)
	}
	if _, err := st.GetItem(ctx, newKey); err == nil {
		t.Fatal("expected changed GUID not to create a second item")
	}
}

func TestGUIDLinkConflictKeepsRowsAndReportsConflict(t *testing.T) {
	ctx := context.Background()
	cfg, st := openFeedTestStore(t)
	now := fixedNow()
	feedID := createImporterTestFeed(t, ctx, st)
	feed := store.Feed{ID: feedID, FeedKey: "feed:test", NormalizedURL: "https://example.com/feed.xml", SiteURL: "https://example.com/"}
	parsed := newParser(t, []byte(`<rss version="2.0"><channel><title>Example</title><link>https://example.com/</link></channel></rss>`))

	guidItem := &gofeed.Item{GUID: "stable-guid", Title: "GUID row", Link: "https://example.com/original", Description: "first"}
	guidEntry, ok := buildFeedEntry(feed, parsed, guidItem, now)
	if !ok {
		t.Fatal("guid entry not built")
	}
	if _, err := st.ApplyFeedEntry(ctx, guidEntry); err != nil {
		t.Fatalf("apply guid entry: %v", err)
	}
	linkItem := &gofeed.Item{Title: "Link row", Link: "https://example.com/new", Description: "second"}
	linkEntry, ok := buildFeedEntry(feed, parsed, linkItem, now)
	if !ok {
		t.Fatal("link entry not built")
	}
	if _, err := st.ApplyFeedEntry(ctx, linkEntry); err != nil {
		t.Fatalf("apply link entry: %v", err)
	}

	conflictItem := &gofeed.Item{GUID: "stable-guid", Title: "GUID row", Link: "https://example.com/new", Description: "changed"}
	conflictEntry, ok := buildFeedEntry(feed, parsed, conflictItem, now.Add(time.Hour))
	if !ok {
		t.Fatal("conflict entry not built")
	}
	result, err := st.ApplyFeedEntry(ctx, conflictEntry)
	if err != nil {
		t.Fatalf("apply conflict entry: %v", err)
	}
	if !result.IdentityConflict || !result.Updated {
		t.Fatalf("expected conflict update on GUID row, got %+v", result)
	}
	if _, err := st.GetItem(ctx, guidEntry.Item.SourceKey); err != nil {
		t.Fatalf("expected GUID item retained: %v", err)
	}
	if _, err := st.GetItem(ctx, linkEntry.Item.SourceKey); err != nil {
		t.Fatalf("expected link-conflict item retained: %v", err)
	}
	_ = cfg
}

func TestFailureBackoffAndDeadThreshold(t *testing.T) {
	ctx := context.Background()
	cfg, st := openFeedTestStore(t)
	createImporterTestFeed(t, ctx, st)
	feed, err := st.GetFeed(ctx, "feed:test")
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	fetcher := &fakeFetcher{
		results: []FetchResult{{RequestURL: feed.NormalizedURL, FinalURL: feed.NormalizedURL, HTTPStatus: http.StatusInternalServerError}},
		errs:    []error{errors.New("server exploded")},
	}
	now := fixedNow()
	if _, err := CheckFeed(ctx, cfg, st, feed, Options{Fetcher: fetcher, Now: func() time.Time { return now }}); err == nil {
		t.Fatal("expected fetch error")
	}
	failed, err := st.GetFeed(ctx, "feed:test")
	if err != nil {
		t.Fatalf("GetFeed failed: %v", err)
	}
	if failed.HealthStatus != store.FeedHealthError || failed.ErrorCount != 1 {
		t.Fatalf("unexpected first failure state: %+v", failed)
	}
	if !failed.NextFetchAfter.Equal(now.Add(initialBackoff)) {
		t.Fatalf("next_fetch_after = %s, want %s", failed.NextFetchAfter, now.Add(initialBackoff))
	}

	if _, err := st.UpsertFeed(ctx, store.FeedUpsert{
		FeedKey:             "feed:dead",
		URL:                 "https://example.com/dead.xml",
		NormalizedURL:       "https://example.com/dead.xml",
		PollIntervalSeconds: 3600,
		Enabled:             true,
	}); err != nil {
		t.Fatalf("create dead feed: %v", err)
	}
	for i := 0; i < deadFailureCount; i++ {
		feed, err := st.GetFeed(ctx, "feed:dead")
		if err != nil {
			t.Fatalf("GetFeed 404 #%d: %v", i, err)
		}
		at := now.Add(-25 * time.Hour).Add(time.Duration(i) * time.Hour)
		if i == deadFailureCount-1 {
			at = now
		}
		fetcher = &fakeFetcher{
			results: []FetchResult{{RequestURL: feed.NormalizedURL, FinalURL: feed.NormalizedURL, HTTPStatus: http.StatusNotFound}},
			errs:    []error{errors.New("not found")},
		}
		if _, err := CheckFeed(ctx, cfg, st, feed, Options{Fetcher: fetcher, Now: func() time.Time { return at }}); err == nil {
			t.Fatalf("expected 404 fetch error #%d", i)
		}
	}
	dead, err := st.GetFeed(ctx, "feed:dead")
	if err != nil {
		t.Fatalf("GetFeed dead: %v", err)
	}
	if dead.HealthStatus != store.FeedHealthDead || dead.ErrorCount != 5 {
		t.Fatalf("expected dead after threshold, got status=%q count=%d", dead.HealthStatus, dead.ErrorCount)
	}
}

func TestDiscoverFromHTMLReturnsNormalizedFeedCandidates(t *testing.T) {
	candidates, err := DiscoverFromHTML("https://example.com/blog/", `
		<html><head>
			<link rel="alternate" type="application/rss+xml" title="RSS" href="/feed.xml?section=main&amp;format=rss">
			<link rel="stylesheet" href="/app.css">
		</head></html>`)
	if err != nil {
		t.Fatalf("DiscoverFromHTML: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %+v", len(candidates), candidates)
	}
	if candidates[0].URL != "https://example.com/feed.xml?section=main&format=rss" {
		t.Fatalf("candidate URL = %q", candidates[0].URL)
	}
}

func TestAddDiscoversFeedFromHTMLPage(t *testing.T) {
	ctx := context.Background()
	cfg, st := openFeedTestStore(t)
	pageBody := []byte(`<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`)
	feedBody := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel><title>Discovered</title><link>https://example.com/</link>
    <item><guid>post-1</guid><title>Discovered post</title><link>https://example.com/post-1</link></item>
  </channel>
</rss>`)
	fetcher := &fakeFetcher{results: []FetchResult{
		{
			RequestURL:        "https://example.com/",
			FinalURL:          "https://example.com/",
			HTTPStatus:        200,
			DecodedBody:       pageBody,
			DecodedBodyHash:   sha256Hex(pageBody),
			WireResponseBytes: pageBody,
			DecodedSizeBytes:  int64(len(pageBody)),
		},
		{
			RequestURL:        "https://example.com/feed.xml",
			FinalURL:          "https://example.com/feed.xml",
			HTTPStatus:        200,
			DecodedBody:       feedBody,
			DecodedBodyHash:   sha256Hex(feedBody),
			WireResponseBytes: feedBody,
			DecodedSizeBytes:  int64(len(feedBody)),
		},
	}}

	feed, _, stats, err := Add(ctx, cfg, st, "https://example.com/", AddOptions{
		Fetch:   true,
		Import:  true,
		Fetcher: fetcher,
		Now:     fixedNow,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if feed.NormalizedURL != "https://example.com/feed.xml" {
		t.Fatalf("normalized URL = %q", feed.NormalizedURL)
	}
	if stats.ItemsCreated != 1 || stats.EntriesSeen != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(fetcher.feeds) != 2 {
		t.Fatalf("fetch calls = %d, want 2", len(fetcher.feeds))
	}
	if fetcher.feeds[1].NormalizedURL != "https://example.com/feed.xml" {
		t.Fatalf("second fetch URL = %q", fetcher.feeds[1].NormalizedURL)
	}
}

func openFeedTestStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		RootDir:   root,
		ConfigDir: root,
		DataDir:   filepath.Join(root, "data"),
		TempDir:   filepath.Join(root, "tmp"),
		CacheDir:  filepath.Join(root, "cache"),
		LogDir:    filepath.Join(root, "logs"),
		MediaDir:  filepath.Join(root, "vault", "media"),
		VaultDir:  filepath.Join(root, "vault"),
		DBPath:    filepath.Join(root, "data", "brain.db"),
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return cfg, st
}

func createImporterTestFeed(t *testing.T, ctx context.Context, st *store.Store) int64 {
	t.Helper()
	result, err := st.UpsertFeed(ctx, store.FeedUpsert{
		FeedKey:             "feed:test",
		URL:                 "https://example.com/feed.xml",
		NormalizedURL:       "https://example.com/feed.xml",
		PollIntervalSeconds: 3600,
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}
	return result.FeedID
}

func newParser(t *testing.T, body []byte) *gofeed.Feed {
	t.Helper()
	feed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return feed
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
}
