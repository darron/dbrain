package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
)

func TestFeedTablesExistInCurrentSchema(t *testing.T) {
	t.Parallel()

	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	for _, table := range []string{"feeds", "feed_fetches", "feed_entries", "feed_entry_versions"} {
		exists, err := st.tableExists(table)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("expected %s table to exist", table)
		}
	}
}

func TestFeedEnableDisableKeepsRowsAndResetsHealth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	result, err := st.UpsertFeed(ctx, FeedUpsert{
		FeedKey:             "feed:test",
		URL:                 "https://example.com/feed.xml",
		NormalizedURL:       "https://example.com/feed.xml",
		PollIntervalSeconds: 3600,
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}
	if !result.Created {
		t.Fatal("expected feed to be created")
	}

	if _, err := st.db.ExecContext(ctx, `
		UPDATE feeds
		SET health_status = 'blocked',
			failure_kind = 'unsafe_url',
			first_failed_at = '2026-01-01T00:00:00Z',
			last_failed_at = '2026-01-01T00:00:00Z',
			last_http_status = 400,
			last_error = 'blocked',
			error_count = 3,
			next_fetch_after = '2026-01-02T00:00:00Z'
		WHERE id = ?`, result.FeedID); err != nil {
		t.Fatalf("seed failed health: %v", err)
	}

	if err := st.EnableFeed(ctx, "feed:test", false); err != nil {
		t.Fatalf("disable feed: %v", err)
	}
	disabled, err := st.GetFeed(ctx, "feed:test")
	if err != nil {
		t.Fatalf("GetFeed disabled: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("expected feed disabled")
	}
	if disabled.HealthStatus != FeedHealthBlocked {
		t.Fatalf("disable should preserve health, got %q", disabled.HealthStatus)
	}

	if err := st.EnableFeed(ctx, "feed:test", true); err != nil {
		t.Fatalf("enable feed: %v", err)
	}
	enabled, err := st.GetFeed(ctx, "feed:test")
	if err != nil {
		t.Fatalf("GetFeed enabled: %v", err)
	}
	if !enabled.Enabled {
		t.Fatal("expected feed enabled")
	}
	if enabled.HealthStatus != FeedHealthOK || enabled.LastError != "" || enabled.ErrorCount != 0 || !enabled.NextFetchAfter.IsZero() {
		t.Fatalf("expected enable to reset health diagnostics, got status=%q error=%q count=%d next=%s", enabled.HealthStatus, enabled.LastError, enabled.ErrorCount, enabled.NextFetchAfter)
	}
}

func TestListFeedsAndDueFeedsCloseKeyCursorBeforeLoadingRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	for _, input := range []FeedUpsert{
		{
			FeedKey:             "feed:one",
			URL:                 "https://example.com/one.xml",
			NormalizedURL:       "https://example.com/one.xml",
			PollIntervalSeconds: 3600,
			Enabled:             true,
		},
		{
			FeedKey:             "feed:two",
			URL:                 "https://example.com/two.xml",
			NormalizedURL:       "https://example.com/two.xml",
			PollIntervalSeconds: 3600,
			Enabled:             true,
		},
	} {
		if _, err := st.UpsertFeed(ctx, input); err != nil {
			t.Fatalf("UpsertFeed %s: %v", input.FeedKey, err)
		}
	}

	listCtx, cancelList := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelList()
	feeds, err := st.ListFeeds(listCtx, false)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 2 {
		t.Fatalf("ListFeeds count = %d, want 2", len(feeds))
	}

	dueCtx, cancelDue := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDue()
	due, err := st.ListFeedsDue(dueCtx, time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC), 10, false)
	if err != nil {
		t.Fatalf("ListFeedsDue: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("ListFeedsDue count = %d, want 2", len(due))
	}
}

func TestRecordFeedFetchAllowsAuditRowWithoutBodyForUnchanged200(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()
	feedID := createTestFeed(t, ctx, st)

	if err := st.RecordFeedFetch(ctx, FeedFetchRecord{
		FeedID:           feedID,
		ObservedAt:       time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		RequestURL:       "https://example.com/feed.xml",
		FinalURL:         "https://example.com/feed.xml",
		HTTPStatus:       200,
		HeadersJSON:      `{"etag":["abc"]}`,
		DecodedBodyHash:  "same-hash",
		DecodedSizeBytes: 128,
		ParseStatus:      "unchanged",
	}); err != nil {
		t.Fatalf("RecordFeedFetch: %v", err)
	}

	var body []byte
	var hash string
	if err := st.db.QueryRowContext(ctx, `SELECT wire_response_bytes, decoded_body_hash FROM feed_fetches WHERE feed_id = ?`, feedID).Scan(&body, &hash); err != nil {
		t.Fatalf("load feed fetch: %v", err)
	}
	if body != nil {
		t.Fatalf("expected unchanged audit row to omit duplicate body bytes, got %d bytes", len(body))
	}
	if hash != "same-hash" {
		t.Fatalf("decoded hash = %q, want same-hash", hash)
	}
}

func TestApplyFeedEntryVersioningAndUniqueItem(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()
	feedID := createTestFeed(t, ctx, st)
	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)

	entry := testFeedEntry(feedID, now, "first body")
	result, err := st.ApplyFeedEntry(ctx, entry)
	if err != nil {
		t.Fatalf("ApplyFeedEntry create: %v", err)
	}
	if !result.Created || result.Version != 1 {
		t.Fatalf("expected created version 1, got %+v", result)
	}

	unchanged, err := st.ApplyFeedEntry(ctx, entry)
	if err != nil {
		t.Fatalf("ApplyFeedEntry unchanged: %v", err)
	}
	if !unchanged.Unchanged || unchanged.ItemID != result.ItemID || unchanged.Version != 1 {
		t.Fatalf("expected unchanged same item version 1, got %+v", unchanged)
	}

	updatedEntry := testFeedEntry(feedID, now.Add(time.Minute), "changed body")
	updated, err := st.ApplyFeedEntry(ctx, updatedEntry)
	if err != nil {
		t.Fatalf("ApplyFeedEntry update: %v", err)
	}
	if !updated.Updated || updated.ItemID != result.ItemID || updated.Version != 2 {
		t.Fatalf("expected updated same item version 2, got %+v", updated)
	}

	var versions int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM feed_entry_versions WHERE feed_entry_id = ?`, result.FeedEntryID).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != 1 {
		t.Fatalf("expected one prior version row, got %d", versions)
	}

	duplicate := testFeedEntry(feedID, now.Add(2*time.Minute), "another body")
	duplicate.EntryKey = "feed-entry:duplicate"
	duplicate.IdentityKey = "duplicate"
	duplicate.GUID = ""
	duplicate.Link = "https://example.com/duplicate"
	duplicate.NormalizedLink = "https://example.com/duplicate"
	duplicate.Item.SourceKey = entry.Item.SourceKey
	if _, err := st.ApplyFeedEntry(ctx, duplicate); err == nil {
		t.Fatal("expected duplicate materialized item to fail")
	}
}

func createTestFeed(t *testing.T, ctx context.Context, st *Store) int64 {
	t.Helper()
	result, err := st.UpsertFeed(ctx, FeedUpsert{
		FeedKey:             "feed:test",
		URL:                 "https://example.com/feed.xml",
		NormalizedURL:       "https://example.com/feed.xml",
		PollIntervalSeconds: 3600,
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	return result.FeedID
}

func testFeedEntry(feedID int64, observed time.Time, text string) FeedEntry {
	item := model.Item{
		SourceKey:    "feed-entry:test",
		SourceType:   "feed_entry",
		ExternalID:   "entry-1",
		CanonicalURL: "https://example.com/post",
		Title:        "Example post",
		AuthorName:   "Example Author",
		PublishedAt:  "2026-05-09T00:00:00Z",
		SyncedAt:     observed.Format(time.RFC3339),
		Text:         text,
		LinksJSON:    `["https://example.com/post"]`,
		RawJSON:      `{"id":"entry-1"}`,
		UpdatedAt:    observed,
		LastSeenAt:   observed,
		UserTags:     "feedtag",
	}
	item.ContentHash = itemhash.Compute(item)
	return FeedEntry{
		FeedID:          feedID,
		EntryKey:        "feed-entry:test",
		IdentityKey:     "entry-1",
		GUID:            "entry-1",
		Link:            "https://example.com/post",
		NormalizedLink:  "https://example.com/post",
		Title:           "Example post",
		Author:          "Example Author",
		PublishedAt:     "2026-05-09T00:00:00Z",
		ContentText:     text,
		EnclosuresJSON:  `[]`,
		ExtensionsJSON:  `{}`,
		RawJSON:         `{"id":"entry-1"}`,
		ContentHash:     item.ContentHash,
		Item:            item,
		ObservedAt:      observed,
		SourceCandidate: nil,
	}
}
