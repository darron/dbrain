package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestAuditReadSnapshotCountsClosedStaticIdentityMatches(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	for _, item := range []model.Item{
		{SourceKey: "x:one", SourceType: "x_bookmark", ExternalID: "one"},
		{SourceKey: "gh-star:viewer:owner/repo", SourceType: "github_star", ExternalID: "owner/repo"},
		{SourceKey: "x:wrong-type", SourceType: "github_star", ExternalID: "wrong-type"},
	} {
		item.CanonicalURL = "https://example.invalid/" + item.ExternalID
		item.ContentHash = item.SourceKey
		item.RawJSON = "{}"
		item.UpdatedAt = now
		item.LastSeenAt = now
		if _, err := st.UpsertItem(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	one := hashAuditIdentity(AuditSourceXBookmarks, "x:one")
	missing := hashAuditIdentity(AuditSourceXBookmarks, "x:missing")
	wrongType := hashAuditIdentity(AuditSourceXBookmarks, "x:wrong-type")
	matched, err := snapshot.CountLocalIdentityMatches(t.Context(), AuditSourceXBookmarks, []string{one, one, missing, wrongType})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
}

func TestAuditIdentityHashMatchesLockedV1Vector(t *testing.T) {
	const want = "61870beb388d1e3a983d1d78909980b7f4f5ab93d11f761c7aed5831bcbb345a"
	if got := hashAuditIdentity(AuditSourceXBookmarks, "x:123"); got != want {
		t.Fatalf("store identity hash = %q, want locked v1 vector %q", got, want)
	}
}

func TestAuditSourceEnumIsExactClosedSevenValueMapping(t *testing.T) {
	want := map[AuditSource]string{
		AuditSourceAppleNotes: "apple-notes", AuditSourceSafariTabs: "safari-tabs",
		AuditSourceXBookmarks: "x-bookmarks", AuditSourceGitHubStars: "github-stars",
		AuditSourceYouTubeLiked: "youtube-liked", AuditSourceYouTubeWatchLater: "youtube-watch-later",
		AuditSourceFeeds: "feeds",
	}
	if len(want) != 7 {
		t.Fatalf("test mapping count = %d", len(want))
	}
	for source, domain := range want {
		if !source.valid() || source.hashDomain() != domain {
			t.Fatalf("source %q valid=%t domain=%q, want %q", source, source.valid(), source.hashDomain(), domain)
		}
	}
}

func TestAuditReadSnapshotCountsFeedIdentityEvolutionAliases(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	item := model.Item{
		SourceKey: "feed-entry:stable-old-key", SourceType: "feed_entry", ExternalID: "fallback:old",
		CanonicalURL: "https://example.com/current", ContentHash: "content", RawJSON: "{}", UpdatedAt: now, LastSeenAt: now,
	}
	itemResult, err := st.UpsertItem(t.Context(), item)
	if err != nil {
		t.Fatal(err)
	}
	nowText := now.Format(time.RFC3339)
	feedResult, err := st.db.ExecContext(t.Context(), `INSERT INTO feeds
		(feed_key, url, normalized_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"feed:key", "https://example.com/feed", "https://example.com/feed", nowText, nowText)
	if err != nil {
		t.Fatal(err)
	}
	feedID, err := feedResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.db.ExecContext(t.Context(), `INSERT INTO feed_entries
		(feed_id, entry_key, identity_key, guid, normalized_link, content_hash, item_id,
		 first_seen_at, last_seen_at, last_changed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		feedID, item.SourceKey, "fallback:old", "current-guid", "https://example.com/current", "content", itemResult.ItemID,
		nowText, nowText, nowText, nowText, nowText)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	wanted := []string{
		hashAuditFeedIdentity("feed:key", "fallback:old"),
		hashAuditFeedIdentity("feed:key", "guid:current-guid"),
		hashAuditFeedIdentity("feed:key", "link:https://example.com/current"),
		hashAuditFeedIdentity("feed:key", "guid:missing"),
	}
	matched, err := snapshot.CountLocalIdentityMatches(t.Context(), AuditSourceFeeds, wanted)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 3 {
		t.Fatalf("feed alias matches = %d, want 3", matched)
	}
}

func TestAuditReadSnapshotIdentityMatchValidationFailsBeforeQuery(t *testing.T) {
	st := openTestStore(t)
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	valid := hashAuditIdentity(AuditSourceXBookmarks, "x:one")
	for _, test := range []struct {
		name   string
		source AuditSource
		hashes []string
	}{
		{name: "unknown source", source: AuditSource("future"), hashes: []string{valid}},
		{name: "uppercase hash", source: AuditSourceXBookmarks, hashes: []string{strings.ToUpper(valid)}},
		{name: "short hash", source: AuditSourceXBookmarks, hashes: []string{"abc"}},
		{name: "over cap", source: AuditSourceXBookmarks, hashes: make([]string, AuditIdentityMaxCount+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := snapshot.CountLocalIdentityMatches(t.Context(), test.source, test.hashes); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestAuditReadSnapshotIdentityMatchesHonorCancellationAndClosedState(t *testing.T) {
	st := openTestStore(t)
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshot.CountLocalIdentityMatches(canceled, AuditSourceXBookmarks, nil); err == nil {
		t.Fatal("expected canceled identity match to fail")
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.CountLocalIdentityMatches(t.Context(), AuditSourceXBookmarks, nil); err == nil {
		t.Fatal("expected closed snapshot identity match to fail even for empty input")
	}
}
