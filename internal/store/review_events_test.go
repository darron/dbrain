package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestListReviewEventsPaginatesByFullCursorTuple(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	eventAt := time.Date(2026, 5, 11, 13, 0, 0, 0, time.UTC)
	first := insertReviewItem(t, ctx, st, "feed-entry:first", "feed_entry", eventAt)
	second := insertReviewItem(t, ctx, st, "feed-entry:second", "feed_entry", eventAt)
	if first == second {
		t.Fatal("expected distinct item ids")
	}

	start := NewReviewCursorSince(eventAt.Add(-time.Minute))
	feed, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: start,
		Limit:  1,
		Types:  []string{"imports"},
		Now:    eventAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("ListReviewEvents first page: %v", err)
	}
	if !feed.Truncated || len(feed.Events) != 1 {
		t.Fatalf("expected one truncated event, got truncated=%t events=%d", feed.Truncated, len(feed.Events))
	}
	if feed.Events[0].EntityKey != "feed-entry:first" {
		t.Fatalf("first page entity = %q", feed.Events[0].EntityKey)
	}
	cursor, err := ParseReviewCursorToken(feed.NextCursor)
	if err != nil {
		t.Fatalf("parse next cursor: %v", err)
	}
	next, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: cursor,
		Limit:  10,
		Types:  []string{"imports"},
		Now:    eventAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("ListReviewEvents second page: %v", err)
	}
	if next.Truncated || len(next.Events) != 1 {
		t.Fatalf("expected one final event, got truncated=%t events=%d", next.Truncated, len(next.Events))
	}
	if next.Events[0].EntityKey != "feed-entry:second" {
		t.Fatalf("second page entity = %q", next.Events[0].EntityKey)
	}
}

func TestListReviewEventsReturnsEnrichmentAndFailureEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	now := time.Date(2026, 5, 11, 13, 30, 0, 0, time.UTC)
	itemID := insertReviewItem(t, ctx, st, "x:media", "x_bookmark", now.Add(-10*time.Minute))
	if err := st.SaveXMediaTranscriptionState(ctx, itemID, model.XMediaTranscriptStatusOK, "", now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("SaveXMediaTranscriptionState: %v", err)
	}
	if _, err := st.SaveItemSummary(ctx, itemID, model.SummaryResult{
		Text:          "The clip discusses feed review automation.",
		Status:        model.ItemSummaryStatusOK,
		FetchedAt:     now.Add(-4 * time.Minute),
		Tool:          "test",
		ToolVersion:   "test",
		PromptVersion: "test",
	}, "summary-hash"); err != nil {
		t.Fatalf("SaveItemSummary: %v", err)
	}

	source, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:blocked",
		CanonicalURL:  "https://example.com/blocked",
		NormalizedURL: "https://example.com/blocked",
		OriginalURL:   "https://example.com/blocked",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/example.md",
	})
	if err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET summary_status = ?, summary_error = ?, summary_failed_at = ?, updated_at = ?
		WHERE id = ?`,
		model.SourceSummaryStatusBlocked,
		"context limit",
		now.Add(-3*time.Minute).Format(time.RFC3339),
		now.Add(-3*time.Minute).Format(time.RFC3339),
		source.SourceID,
	); err != nil {
		t.Fatalf("seed blocked source: %v", err)
	}

	feed, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: NewReviewCursorSince(now.Add(-15 * time.Minute)),
		Limit:  20,
		Types:  []string{"enrichments", "failures"},
		Now:    now,
	})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	kinds := map[string]bool{}
	for _, event := range feed.Events {
		kinds[event.EventKind+":"+event.EventStage] = true
	}
	for _, want := range []string{
		ReviewEventKindXMediaTranscribed + ":x_media_transcribed",
		ReviewEventKindXMediaSummarized + ":x_media_summarized",
		ReviewEventKindBlocked + ":source_summary",
	} {
		if !kinds[want] {
			t.Fatalf("missing %s in events: %+v", want, feed.Events)
		}
	}
	if len(feed.Counts.ByKind) == 0 {
		t.Fatal("expected counts by kind")
	}
}

func TestListReviewEventsUsesSummaryFailureTimestampNotUpdatedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	now := time.Date(2026, 5, 11, 13, 30, 0, 0, time.UTC)
	source, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:stale-summary-failure",
		CanonicalURL:  "https://example.com/stale-summary-failure",
		NormalizedURL: "https://example.com/stale-summary-failure",
		OriginalURL:   "https://example.com/stale-summary-failure",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/stale-summary-failure.md",
	})
	if err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET summary_status = ?, summary_error = ?, summary_failed_at = ?, updated_at = ?
		WHERE id = ?`,
		model.SourceSummaryStatusError,
		"old model error",
		now.Add(-48*time.Hour).Format(time.RFC3339),
		now.Add(-5*time.Minute).Format(time.RFC3339),
		source.SourceID,
	); err != nil {
		t.Fatalf("seed stale summary failure: %v", err)
	}

	feed, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: NewReviewCursorSince(now.Add(-time.Hour)),
		Limit:  20,
		Types:  []string{"failures"},
		Now:    now,
	})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	for _, event := range feed.Events {
		if event.EntityKey == "src:stale-summary-failure" {
			t.Fatalf("stale summary failure surfaced from updated_at: %+v", event)
		}
	}
}

func TestReviewEventsUnionAppliesCursorInsideEachBranch(t *testing.T) {
	t.Parallel()

	if got := strings.Count(reviewEventsUnionQuery, ">= ?"); got != reviewEventBranchCursorPredicateCount {
		t.Fatalf("branch cursor predicate count = %d, want %d", got, reviewEventBranchCursorPredicateCount)
	}
}

func TestParseReviewCursorInputSupportsRelativeDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 11, 13, 0, 0, 0, time.UTC)
	cursor, err := ParseReviewCursorInput(now, "7d", "")
	if err != nil {
		t.Fatalf("ParseReviewCursorInput: %v", err)
	}
	if got, want := cursor.EventAt, now.Add(-7*24*time.Hour); !got.Equal(want) {
		t.Fatalf("cursor event_at = %s, want %s", got, want)
	}
	token, err := cursor.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	roundTrip, err := ParseReviewCursorToken(token)
	if err != nil {
		t.Fatalf("ParseReviewCursorToken: %v", err)
	}
	if !roundTrip.EventAt.Equal(cursor.EventAt) {
		t.Fatalf("round trip event_at = %s, want %s", roundTrip.EventAt, cursor.EventAt)
	}
}

func TestListReviewEventsKeepsCursorUnchangedWhenNoEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	cursor := NewReviewCursorSince(time.Date(2026, 5, 11, 13, 0, 0, 0, time.UTC))
	token, err := cursor.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	feed, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: cursor,
		Limit:  10,
		Now:    cursor.EventAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if len(feed.Events) != 0 || feed.Truncated {
		t.Fatalf("expected empty untruncated feed, got truncated=%t events=%+v", feed.Truncated, feed.Events)
	}
	if feed.NextCursor != token {
		t.Fatalf("next cursor = %q, want unchanged %q", feed.NextCursor, token)
	}
}

func TestListReviewEventsCategorizationFilterIsEmptyUntilSupported(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	eventAt := time.Date(2026, 5, 11, 13, 0, 0, 0, time.UTC)
	insertReviewItem(t, ctx, st, "feed-entry:categorized-later", "feed_entry", eventAt)
	cursor := NewReviewCursorSince(eventAt.Add(-time.Minute))
	token, err := cursor.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	feed, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: cursor,
		Limit:  10,
		Types:  []string{"categorization"},
		Now:    eventAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if len(feed.Events) != 0 {
		t.Fatalf("expected no categorization events, got %+v", feed.Events)
	}
	if feed.NextCursor != token {
		t.Fatalf("next cursor = %q, want unchanged %q", feed.NextCursor, token)
	}
}

func insertReviewItem(t *testing.T, ctx context.Context, st *Store, sourceKey string, sourceType string, at time.Time) int64 {
	t.Helper()
	result, err := st.UpsertItem(ctx, testItem(sourceKey, sourceType, "https://example.com/"+sourceKey, at))
	if err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET imported_at = ?, updated_at = ?, last_seen_at = ? WHERE id = ?`, at.Format(time.RFC3339), at.Format(time.RFC3339), at.Format(time.RFC3339), result.ItemID); err != nil {
		t.Fatalf("seed item times %s: %v", sourceKey, err)
	}
	return result.ItemID
}
