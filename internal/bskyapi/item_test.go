package bskyapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
