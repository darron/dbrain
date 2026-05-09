package feedimport

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
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
