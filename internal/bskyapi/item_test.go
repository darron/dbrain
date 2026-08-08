package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestBookmarkViewToItemClassifiesUnavailablePosts(t *testing.T) {
	for _, test := range []struct {
		name     string
		typeName string
		want     error
	}{
		{name: "blocked", typeName: "app.bsky.feed.defs#blockedPost", want: errBlockedBookmark},
		{name: "not found", typeName: "app.bsky.feed.defs#notFoundPost", want: errNotFoundBookmark},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := bookmarkViewToItem(bookmarkView{
				Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7example"},
				Item:    json.RawMessage(`{"$type":"` + test.typeName + `"}`),
			}, time.Now())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBookmarkViewToItemMapsPostAndRedactsSessionFields(t *testing.T) {
	view := bookmarkView{
		CreatedAt: "2026-08-08T18:00:00Z",
		Subject: bookmarkSubject{
			URI: "at://did:plc:one/app.bsky.feed.post/3lq7example",
			CID: "bafyreiexample",
		},
		Item: json.RawMessage(`{
  "$type": "app.bsky.feed.defs#postView",
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7example",
  "cid": "bafyreiexample",
  "author": {"did": "did:plc:one", "handle": "alice.example", "displayName": "Alice"},
  "record": {
    "$type": "app.bsky.feed.post",
    "text": "Read this post",
    "createdAt": "2026-08-07T17:00:00Z",
    "langs": ["en"],
    "facets": [{"features": [{"$type": "app.bsky.richtext.facet#link", "uri": "https://example.com/article"}]}],
    "accessJwt": "nested-must-not-be-copied"
  },
  "embed": {"external": {"uri": "https://example.com/article", "refreshJwt": "nested-must-not-be-copied"}},
  "indexedAt": "2026-08-07T17:01:00Z",
  "likeCount": 4,
  "repostCount": 3,
  "replyCount": 2,
  "quoteCount": 1,
  "bookmarkCount": 9,
  "accessJwt": "must-not-be-copied",
  "refreshJwt": "must-not-be-copied"
}`),
	}

	item, err := bookmarkViewToItem(view, time.Date(2026, 8, 8, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("bookmarkViewToItem: %v", err)
	}
	if item.SourceType != "bsky_bookmark" {
		t.Fatalf("source type = %q", item.SourceType)
	}
	if item.SourceKey != "bsky:at://did:plc:one/app.bsky.feed.post/3lq7example" {
		t.Fatalf("source key = %q", item.SourceKey)
	}
	if item.ExternalID != view.Subject.URI {
		t.Fatalf("external ID = %q", item.ExternalID)
	}
	if item.CanonicalURL != "https://bsky.app/profile/alice.example/post/3lq7example" {
		t.Fatalf("canonical URL = %q", item.CanonicalURL)
	}
	if item.AuthorHandle != "alice.example" || item.AuthorName != "Alice" {
		t.Fatalf("author = %q / %q", item.AuthorHandle, item.AuthorName)
	}
	if item.Text != "Read this post" || item.Language != "en" {
		t.Fatalf("post fields = %q / %q", item.Text, item.Language)
	}
	if item.SavedAt != "2026-08-08T18:00:00Z" || item.PublishedAt != "2026-08-07T17:00:00Z" {
		t.Fatalf("timestamps = saved %q published %q", item.SavedAt, item.PublishedAt)
	}
	if item.SyncedAt != "2026-08-07T17:01:00Z" {
		t.Fatalf("synced at = %q", item.SyncedAt)
	}
	if !strings.Contains(item.LinksJSON, "https://example.com/article") {
		t.Fatalf("links JSON = %q", item.LinksJSON)
	}
	if item.LikeCount != 4 || item.RepostCount != 3 || item.ReplyCount != 2 || item.QuoteCount != 1 || item.BookmarkCount != 9 {
		t.Fatalf("engagement = %d/%d/%d/%d/%d", item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount)
	}
	if strings.Contains(item.RawJSON, "accessJwt") || strings.Contains(item.RawJSON, "refreshJwt") || strings.Contains(item.RawJSON, "must-not-be-copied") {
		t.Fatalf("raw JSON leaked session fields: %q", item.RawJSON)
	}
	if !strings.Contains(item.NotePath, "items/bsky/2026/") || strings.Contains(item.NotePath, "/at://") {
		t.Fatalf("unsafe note path = %q", item.NotePath)
	}
}

func TestBookmarkViewToItemLeavesSavedAtEmptyWhenBookmarkTimestampMissing(t *testing.T) {
	view := bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7example"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7example",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "No saved time", "createdAt": "2026-08-07T17:00:00Z"}
}`),
	}

	item, err := bookmarkViewToItem(view, time.Date(2026, 8, 8, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("bookmarkViewToItem: %v", err)
	}
	if item.SavedAt != "" {
		t.Fatalf("saved at = %q, want empty", item.SavedAt)
	}
}

func TestBookmarkViewToProjectionRejectsEmptyPostWithoutSupportedEmbed(t *testing.T) {
	view := bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7empty"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7empty",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "", "createdAt": "2026-08-07T17:00:00Z"}
}`),
	}
	_, err := bookmarkViewToProjection(context.Background(), view, time.Now(), nil)
	if !errors.Is(err, errUnsupportedBookmark) {
		t.Fatalf("error = %v, want unsupported bookmark", err)
	}
}

func TestBookmarkViewToItemUsesEmbedAndAuthorTitleFallbacks(t *testing.T) {
	base := `{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7title",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "", "createdAt": "2026-08-07T17:00:00Z"},
  "embed": {"$type": "app.bsky.embed.images#view", "images": [{"fullsize": "https://cdn.example/image.jpg", "alt": "A useful image"}]}
}`
	item, err := bookmarkViewToItem(bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7title"},
		Item:    json.RawMessage(base),
	}, time.Now())
	if err != nil {
		t.Fatalf("bookmarkViewToItem: %v", err)
	}
	if item.Title != "A useful image" {
		t.Fatalf("image title = %q", item.Title)
	}

	fallback, err := bookmarkViewToItem(bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7fallback"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7fallback",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "", "createdAt": "2026-08-07T17:00:00Z"},
  "embed": {"$type": "app.bsky.embed.video#view", "playlist": "https://video.example/playlist.m3u8"}
}`),
	}, time.Now())
	if err != nil {
		t.Fatalf("bookmarkViewToItem fallback: %v", err)
	}
	if fallback.Title != "Post by @alice.example" {
		t.Fatalf("author title = %q", fallback.Title)
	}
}

func TestBookmarkViewToProjectionKeepsTextOptionalAndDecodesImageViews(t *testing.T) {
	view := bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7image"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7image",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "", "createdAt": "2026-08-07T17:00:00Z"},
  "embed": {
    "$type": "app.bsky.embed.images#view",
    "images": [{"fullsize": "https://cdn.example/image.jpg", "thumb": "https://cdn.example/thumb.jpg", "alt": "alt text", "aspectRatio": {"width": 1200, "height": 800}}]
  }
}`),
	}

	projection, err := bookmarkViewToProjection(context.Background(), view, time.Date(2026, 8, 8, 19, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatalf("bookmarkViewToProjection: %v", err)
	}
	if projection.Item.Text != "" {
		t.Fatalf("text = %q, want empty text to remain importable", projection.Item.Text)
	}
	if !projection.MediaKnown || projection.MediaUnavailable {
		t.Fatalf("media state = known=%v unavailable=%v", projection.MediaKnown, projection.MediaUnavailable)
	}
	if len(projection.MediaCandidates) != 1 {
		t.Fatalf("media candidates = %#v", projection.MediaCandidates)
	}
	want := model.MediaCandidate{
		RemoteURL:   "https://cdn.example/image.jpg",
		ExpandedURL: "https://bsky.app/profile/alice.example/post/3lq7image",
		MediaType:   "photo",
		Width:       1200,
		Height:      800,
	}
	if got := projection.MediaCandidates[0]; got != want {
		t.Fatalf("media candidate = %#v, want %#v", got, want)
	}
}

func TestBookmarkViewToProjectionCollectsNestedExternalLinksWithoutMediaURLs(t *testing.T) {
	view := bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7nested"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7nested",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "https://example.com/from-text", "facets": [{"features": [{"$type": "app.bsky.richtext.facet#link", "uri": "https://example.com/from-facet"}]}]},
  "embed": {
    "$type": "app.bsky.embed.recordWithMedia#view",
    "record": {"$type": "app.bsky.embed.record#viewRecord", "uri": "at://did:plc:two/app.bsky.feed.post/3lq7quote", "value": {"text": "https://example.com/from-quote"}},
    "media": {"$type": "app.bsky.embed.external#view", "external": {"uri": "https://example.com/from-nested-external", "thumb": "https://cdn.example/not-a-link.jpg"}}
  }
}`),
	}

	projection, err := bookmarkViewToProjection(context.Background(), view, time.Now(), nil)
	if err != nil {
		t.Fatalf("bookmarkViewToProjection: %v", err)
	}
	for _, want := range []string{
		"https://example.com/from-text",
		"https://example.com/from-facet",
		"https://example.com/from-quote",
		"https://example.com/from-nested-external",
	} {
		if !strings.Contains(projection.Item.LinksJSON, want) {
			t.Fatalf("links JSON %q does not contain %q", projection.Item.LinksJSON, want)
		}
	}
	for _, unwanted := range []string{"not-a-link.jpg", "at://did:plc:two/app.bsky.feed.post/3lq7quote"} {
		if strings.Contains(projection.Item.LinksJSON, unwanted) {
			t.Fatalf("links JSON %q unexpectedly contains %q", projection.Item.LinksJSON, unwanted)
		}
	}
}

func TestBookmarkViewToProjectionResolvesVideoToBlobURL(t *testing.T) {
	view := bookmarkView{
		Subject: bookmarkSubject{URI: "at://did:plc:one/app.bsky.feed.post/3lq7video"},
		Item: json.RawMessage(`{
  "uri": "at://did:plc:one/app.bsky.feed.post/3lq7video",
  "author": {"did": "did:plc:one", "handle": "alice.example"},
  "record": {"text": "video", "embed": {"$type": "app.bsky.embed.video", "video": {"ref": {"$link": "bafy-video"}, "mimeType": "video/mp4"}}},
  "embed": {"$type": "app.bsky.embed.video#view", "cid": "bafy-video", "playlist": "https://video.example/playlist.m3u8"}
}`),
	}
	resolver := staticVideoBlobResolver{url: "https://pds.example/xrpc/com.atproto.sync.getBlob?did=did%3Aplc%3Aone&cid=bafy-video"}
	projection, err := bookmarkViewToProjection(context.Background(), view, time.Now(), resolver)
	if err != nil {
		t.Fatalf("bookmarkViewToProjection: %v", err)
	}
	if !projection.MediaKnown || projection.MediaUnavailable || len(projection.MediaCandidates) != 1 {
		t.Fatalf("media state = %#v", projection)
	}
	got := projection.MediaCandidates[0]
	if got.RemoteURL != resolver.url || got.MediaType != "video" || got.ExpandedURL != "https://video.example/playlist.m3u8" {
		t.Fatalf("video candidate = %#v", got)
	}
}

type staticVideoBlobResolver struct {
	url string
}

func (r staticVideoBlobResolver) ResolveVideoBlob(context.Context, string, string) (string, error) {
	return r.url, nil
}
