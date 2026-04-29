package itemhash

import (
	"testing"

	"github.com/darron/dbrain/internal/model"
)

func TestComputeIgnoresSyncedAt(t *testing.T) {
	base := model.Item{
		SourceKey:    "x:123",
		SourceType:   "x_bookmark",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Example",
		Text:         "hello world",
		SavedAt:      "2026-04-25T12:00:00Z",
		SyncedAt:     "2026-04-25T12:00:00Z",
	}
	other := base
	other.SyncedAt = "2026-04-26T09:00:00Z"

	if got, want := Compute(base), Compute(other); got != want {
		t.Fatalf("hash changed only from synced_at: %q != %q", got, want)
	}
}

func TestComputeIgnoresXBookmarkEngagementCounts(t *testing.T) {
	base := model.Item{
		SourceKey:     "x:123",
		SourceType:    "x_bookmark",
		CanonicalURL:  "https://x.com/example/status/123",
		Title:         "Example",
		Text:          "hello world",
		LikeCount:     1,
		RepostCount:   2,
		ReplyCount:    3,
		QuoteCount:    4,
		BookmarkCount: 5,
	}
	other := base
	other.LikeCount = 10
	other.RepostCount = 20
	other.ReplyCount = 30
	other.QuoteCount = 40
	other.BookmarkCount = 50

	if got, want := Compute(base), Compute(other); got != want {
		t.Fatalf("x_bookmark hash changed only from engagement counters: %q != %q", got, want)
	}
}

func TestComputePreservesEngagementCountsForOtherSourceTypes(t *testing.T) {
	base := model.Item{
		SourceKey:     "x:123",
		SourceType:    "x_post",
		CanonicalURL:  "https://x.com/example/status/123",
		Title:         "Example",
		Text:          "hello world",
		LikeCount:     1,
		RepostCount:   2,
		ReplyCount:    3,
		QuoteCount:    4,
		BookmarkCount: 5,
	}
	other := base
	other.LikeCount = 10

	if got, want := Compute(base), Compute(other); got == want {
		t.Fatalf("non-bookmark hash should change when engagement counters change: %q", got)
	}
}
