package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestReviewCursorTokenRoundTrip(t *testing.T) {
	t.Parallel()

	cursor := ReviewCursor{
		EventAt:    time.Date(2026, 6, 21, 15, 4, 5, 0, time.UTC),
		EventKind:  ReviewEventKindItemImported,
		EntityKind: "item",
		EntityID:   42,
		EventStage: "imported",
	}
	token, err := EncodeReviewCursor(cursor)
	if err != nil {
		t.Fatalf("EncodeReviewCursor: %v", err)
	}
	got, err := DecodeReviewCursor(token)
	if err != nil {
		t.Fatalf("DecodeReviewCursor: %v", err)
	}
	if !got.EventAt.Equal(cursor.EventAt) || got.EventKind != cursor.EventKind || got.EntityKind != cursor.EntityKind || got.EntityID != cursor.EntityID || got.EventStage != cursor.EventStage {
		t.Fatalf("cursor round trip mismatch: got %+v want %+v", got, cursor)
	}
}

func TestParseReviewSinceAcceptsRFC3339OffsetUTCAndDurations(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 21, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{name: "utc", in: "2026-06-21T15:00:00Z", want: time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)},
		{name: "offset", in: "2026-06-21T09:00:00-06:00", want: time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)},
		{name: "minutes", in: "30m", want: now.Add(-30 * time.Minute)},
		{name: "hours", in: "24h", want: now.Add(-24 * time.Hour)},
		{name: "days", in: "7d", want: now.Add(-7 * 24 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReviewSince(tt.in, now)
			if err != nil {
				t.Fatalf("ParseReviewSince(%q): %v", tt.in, err)
			}
			if !got.EventAt.Equal(tt.want) {
				t.Fatalf("ParseReviewSince(%q) event_at=%s want %s", tt.in, got.EventAt, tt.want)
			}
			if got.EventKind != "" || got.EntityKind != "" || got.EntityID != 0 || got.EventStage != "" {
				t.Fatalf("since cursor should only set event_at, got %+v", got)
			}
		})
	}
}

func TestParseReviewSinceRejectsAmbiguousDateOnly(t *testing.T) {
	t.Parallel()

	if _, err := ParseReviewSince("2026-06-21", time.Date(2026, 6, 21, 18, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected date-only since to be rejected")
	}
}

func TestNormalizeReviewEventTypesRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	got, err := NormalizeReviewEventTypes([]string{"imports", "enrichments", "imports"})
	if err != nil {
		t.Fatalf("NormalizeReviewEventTypes: %v", err)
	}
	want := []string{
		ReviewEventKindItemImported,
		ReviewEventKindItemUpdated,
		ReviewEventKindSourceCreated,
		ReviewEventKindSourceExtracted,
		ReviewEventKindSourceSummarized,
		ReviewEventKindItemSummarized,
		ReviewEventKindXMediaTranscribed,
		ReviewEventKindXPhotoOCRed,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("type expansion mismatch:\ngot  %#v\nwant %#v", got, want)
	}
	if _, err := NormalizeReviewEventTypes([]string{"imports", "bogus"}); err == nil {
		t.Fatal("expected unknown type filter to be rejected")
	}
}

func TestListReviewEventsReturnsImportsAndEnrichmentsInCursorOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	itemID := insertReviewTestItem(t, st, "x:review-order", "x_bookmark", "Review order item", base.Add(time.Minute), base.Add(5*time.Minute))
	sourceID := insertReviewTestSource(t, st, "src:review-order", "web", "Review order source", base.Add(2*time.Minute), "")
	updateReviewTestSourceExtracted(t, st, sourceID, base.Add(3*time.Minute), model.SourceExtractStatusOK)
	updateReviewTestSourceSummary(t, st, sourceID, base.Add(4*time.Minute), model.SourceSummaryStatusOK, "source summary")
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleSummary, model.ItemSummaryStatusOK, "item summary", "", base.Add(6*time.Minute), base.Add(6*time.Minute))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	gotKinds := reviewEventKinds(page.Events)
	wantKinds := []string{
		ReviewEventKindItemImported,
		ReviewEventKindSourceCreated,
		ReviewEventKindSourceExtracted,
		ReviewEventKindSourceSummarized,
		ReviewEventKindItemSummarized,
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("event order mismatch:\ngot  %v\nwant %v", gotKinds, wantKinds)
	}
	if page.Truncated {
		t.Fatalf("unexpected truncated page: %+v", page)
	}
	if page.HighWatermark.IsZero() || !page.HighWatermark.Equal(base.Add(6*time.Minute)) {
		t.Fatalf("unexpected high watermark %s", page.HighWatermark)
	}
	if len(page.Counts) != len(wantKinds) {
		t.Fatalf("expected page-local counts for %d kinds, got %+v", len(wantKinds), page.Counts)
	}
}

func TestListReviewEventsPaginatesWithoutSkippingSameTimestampEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	ts := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	insertReviewTestItem(t, st, "x:same-a", "x_bookmark", "Same timestamp A", ts, ts)
	insertReviewTestItem(t, st, "x:same-b", "x_bookmark", "Same timestamp B", ts, ts)
	insertReviewTestItem(t, st, "x:same-c", "x_bookmark", "Same timestamp C", ts, ts)

	first, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: ts.Add(-time.Second)}, Limit: 2})
	if err != nil {
		t.Fatalf("first ListReviewEvents: %v", err)
	}
	if len(first.Events) != 2 || !first.Truncated {
		t.Fatalf("expected first truncated page with 2 events, got %+v", first)
	}
	cursor, err := DecodeReviewCursor(first.NextCursor)
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	second, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: cursor, Limit: 2})
	if err != nil {
		t.Fatalf("second ListReviewEvents: %v", err)
	}
	all := append(reviewEventIDs(first.Events), reviewEventIDs(second.Events)...)
	if len(all) != 3 {
		t.Fatalf("expected 3 same-timestamp events across pages, got %v", all)
	}
	if hasDuplicateStrings(all) {
		t.Fatalf("expected no duplicate event ids across pages, got %v", all)
	}
}

func TestListReviewEventsResumesAfterTruncatedPageWithoutOverlapOrGap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	for i, key := range []string{"x:page-a", "x:page-b", "x:page-c", "x:page-d", "x:page-e"} {
		insertReviewTestItem(t, st, key, "x_bookmark", key, base.Add(time.Duration(i+1)*time.Minute), base.Add(time.Duration(i+1)*time.Minute))
	}

	var all []string
	cursor := ReviewCursor{EventAt: base}
	for {
		page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("ListReviewEvents: %v", err)
		}
		all = append(all, reviewEventIDs(page.Events)...)
		if !page.Truncated {
			break
		}
		cursor, err = DecodeReviewCursor(page.NextCursor)
		if err != nil {
			t.Fatalf("decode cursor: %v", err)
		}
	}
	if len(all) != 5 {
		t.Fatalf("expected all 5 events, got %d: %v", len(all), all)
	}
	if hasDuplicateStrings(all) {
		t.Fatalf("expected no duplicates, got %v", all)
	}
}

func TestListReviewEventsNormalizesOffsetTimestamps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, '{}', ?, ?, ?)`,
		"x:offset-timestamp",
		"x_bookmark",
		"offset-timestamp",
		"https://x.com/example/status/offset-timestamp",
		"Offset timestamp",
		"hash-offset",
		"items/x/offset.md",
		"2026-06-21T09:00:00-06:00",
		"2026-06-21T09:00:00-06:00",
		"2026-06-21T09:00:00-06:00",
	); err != nil {
		t.Fatalf("insert offset timestamp item: %v", err)
	}
	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: time.Date(2026, 6, 21, 14, 59, 59, 0, time.UTC)}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("expected offset timestamp event, got %+v", page.Events)
	}
	want := time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)
	if !page.Events[0].EventAt.Equal(want) {
		t.Fatalf("expected normalized UTC event_at %s, got %s", want, page.Events[0].EventAt)
	}
}

func TestListReviewEventsFiltersByTypeGroup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	itemID := insertReviewTestItem(t, st, "x:filter", "x_bookmark", "Filter item", base.Add(time.Minute), base.Add(time.Minute))
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleOCR, model.ItemOCRStatusOK, "ocr text", "", base.Add(2*time.Minute), base.Add(2*time.Minute))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base}, Limit: 10, Types: []string{"enrichments"}})
	if err != nil {
		t.Fatalf("ListReviewEvents enrichments: %v", err)
	}
	if got, want := reviewEventKinds(page.Events), []string{ReviewEventKindXPhotoOCRed}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected only enrichment events, got %v", got)
	}
}

func TestListReviewEventsEntityViewGroupsEventsAndPrefersSummaries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	itemID := insertReviewTestItem(t, st, "x:entity-view", "x_bookmark", "Entity view item", base.Add(time.Minute), base.Add(time.Minute))
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleSummary, model.ItemSummaryStatusOK, "preferred item summary", "", base.Add(2*time.Minute), base.Add(2*time.Minute))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: ReviewCursor{EventAt: base},
		Limit:  10,
		View:   ReviewEventViewEntities,
	})
	if err != nil {
		t.Fatalf("ListReviewEvents entity view: %v", err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("entity view should suppress raw event rows, got %+v", page.Events)
	}
	if page.Events == nil || page.Entities == nil || page.Counts == nil {
		t.Fatalf("expected non-nil slices, got events=%#v entities=%#v counts=%#v", page.Events, page.Entities, page.Counts)
	}
	if len(page.Entities) != 1 {
		t.Fatalf("expected one grouped entity, got %+v", page.Entities)
	}
	entity := page.Entities[0]
	if entity.EntityKey != "x:entity-view" || entity.EventCount != 2 {
		t.Fatalf("unexpected grouped entity identity/count: %+v", entity)
	}
	if entity.Summary != "preferred item summary" {
		t.Fatalf("expected summary enrichment to be preferred, got %q", entity.Summary)
	}
	if got, want := entity.EventKinds, []string{ReviewEventKindItemImported, ReviewEventKindItemSummarized}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event kinds mismatch: got %v want %v", got, want)
	}
	if len(entity.Events) != 2 {
		t.Fatalf("expected compact event refs, got %+v", entity.Events)
	}
	if !entity.FirstEventAt.Equal(base.Add(time.Minute)) || !entity.LatestEventAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("unexpected entity time bounds: first=%s latest=%s", entity.FirstEventAt, entity.LatestEventAt)
	}
	if entity.Actionability != ReviewActionabilityReview || entity.Importance < 80 {
		t.Fatalf("expected actionable high-importance entity, got %+v", entity)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal entity page: %v", err)
	}
	for _, field := range []string{"events", "entities", "counts", "event_kinds", "tags", "reasons"} {
		if containsNullArrayField(string(encoded), field) {
			t.Fatalf("expected %s to marshal as an array, got %s", field, encoded)
		}
	}
}

func TestListReviewEventsEntityViewCompactsLongRawSummaries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	itemID := insertReviewTestItem(t, st, "x:long-transcript", "x_bookmark", "Long transcript item", base.Add(time.Minute), base.Add(time.Minute))
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusOK, strings.Repeat("word\n", 600), "", base.Add(2*time.Minute), base.Add(2*time.Minute))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{
		Cursor: ReviewCursor{EventAt: base},
		Limit:  10,
		View:   ReviewEventViewEntities,
	})
	if err != nil {
		t.Fatalf("ListReviewEvents entity view: %v", err)
	}
	if len(page.Entities) != 1 {
		t.Fatalf("expected one grouped entity, got %+v", page.Entities)
	}
	summary := page.Entities[0].Summary
	if len([]rune(summary)) > reviewEntitySummaryMaxRunes {
		t.Fatalf("expected compact summary <= %d runes, got %d", reviewEntitySummaryMaxRunes, len([]rune(summary)))
	}
	if strings.Contains(summary, "\n") {
		t.Fatalf("expected compact summary to normalize whitespace, got %q", summary)
	}
	if !strings.HasSuffix(summary, "...") {
		t.Fatalf("expected compact summary to show truncation, got %q", summary)
	}
}

func TestGroupReviewEventsPrefersNewestSameRankSummary(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	groups := groupReviewEvents([]ReviewEvent{
		{
			EventID:       "summary:old",
			EventKind:     ReviewEventKindItemSummarized,
			EventAt:       base.Add(time.Minute),
			EntityKind:    "item",
			EntityID:      7,
			EntityKey:     "x:same-rank-summary",
			EventStage:    "summary",
			SourceType:    "x_bookmark",
			Title:         "Same rank summary",
			Summary:       "older summary",
			Status:        "ok",
			Importance:    80,
			Actionability: ReviewActionabilityReview,
		},
		{
			EventID:       "summary:new",
			EventKind:     ReviewEventKindItemSummarized,
			EventAt:       base.Add(2 * time.Minute),
			EntityKind:    "item",
			EntityID:      7,
			EntityKey:     "x:same-rank-summary",
			EventStage:    "summary",
			SourceType:    "x_bookmark",
			Title:         "Same rank summary",
			Summary:       "newer summary",
			Status:        "ok",
			Importance:    80,
			Actionability: ReviewActionabilityReview,
		},
	})
	if len(groups) != 1 {
		t.Fatalf("expected one grouped entity, got %+v", groups)
	}
	if groups[0].Summary != "newer summary" || groups[0].SummaryEventID != "summary:new" {
		t.Fatalf("expected newest same-rank summary to win, got %+v", groups[0])
	}
}

func TestListReviewEventsRejectsUnknownView(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	_, err := st.ListReviewEvents(context.Background(), ReviewEventFilter{
		Cursor: ReviewCursor{EventAt: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)},
		Limit:  10,
		View:   "cards",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown review event view") {
		t.Fatalf("expected unknown view error, got %v", err)
	}
}

func TestListReviewEventsDoesNotUseGenericItemUpdatedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	insertReviewTestItem(t, st, "x:generic-updated-at", "x_bookmark", "Generic updated", base.Add(time.Minute), base.Add(6*time.Hour))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base.Add(2 * time.Hour)}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("generic items.updated_at should not produce review events, got %+v", page.Events)
	}
}

func TestListReviewEventsUsesItemEnrichmentCompletedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	itemID := insertReviewTestItem(t, st, "x:item-enrichment-completed", "x_bookmark", "Enriched item", base.Add(time.Minute), base.Add(time.Minute))
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleSummary, model.ItemSummaryStatusOK, "completed summary", "", base.Add(3*time.Hour), base.Add(4*time.Hour))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base.Add(2 * time.Hour)}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if got, want := reviewEventKinds(page.Events), []string{ReviewEventKindItemSummarized}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected item summary from completed_at, got %v", got)
	}
	if !page.Events[0].EventAt.Equal(base.Add(3 * time.Hour)) {
		t.Fatalf("expected completed_at timestamp, got %s", page.Events[0].EventAt)
	}
}

func TestListReviewEventsSurfacesActionableFailuresAndBlockedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	sourceID := insertReviewTestSource(t, st, "src:blocked", "web", "Blocked source", base.Add(time.Minute), "")
	updateReviewTestSourceSummary(t, st, sourceID, base.Add(2*time.Minute), model.SourceSummaryStatusBlocked, "summary blocked")
	itemID := insertReviewTestItem(t, st, "x:failed-enrichment", "x_bookmark", "Failed enrichment", base.Add(time.Minute), base.Add(time.Minute))
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleOCR, model.ItemOCRStatusError, "", "ocr failed", time.Time{}, base.Add(3*time.Minute))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base}, Limit: 10, Types: []string{"failures"}})
	if err != nil {
		t.Fatalf("ListReviewEvents failures: %v", err)
	}
	got := reviewEventKinds(page.Events)
	want := []string{ReviewEventKindBlocked, ReviewEventKindItemEnrichmentFailed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failure/blocked events mismatch: got %v want %v", got, want)
	}
	if page.Events[0].Actionability != ReviewActionabilityBlocked || page.Events[1].Actionability != ReviewActionabilityFailure {
		t.Fatalf("unexpected actionability values: %+v", page.Events)
	}
}

func TestListReviewEventsIgnoresNonActionableXTranscriptTerminalStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	itemID := insertReviewTestItem(t, st, "x:no-audio-transcript", "x_bookmark", "No audio", base.Add(time.Minute), base.Add(time.Minute))
	insertReviewTestItemEnrichment(t, st, itemID, model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusNoAudio, "", "", base.Add(2*time.Minute), base.Add(2*time.Minute))

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	for _, event := range page.Events {
		if event.EventKind == ReviewEventKindXMediaTranscribed || event.EventKind == ReviewEventKindItemEnrichmentFailed || event.EventKind == ReviewEventKindBlocked {
			t.Fatalf("non-content terminal transcript state should not be actionable, got %+v", event)
		}
	}
}

func TestListReviewEventsReturnsEmptySlices(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if page.Events == nil || page.Entities == nil || page.Counts == nil {
		t.Fatalf("expected non-nil empty slices, got events=%#v entities=%#v counts=%#v", page.Events, page.Entities, page.Counts)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	if string(encoded) == "" || containsNullArrayField(string(encoded), "events") || containsNullArrayField(string(encoded), "entities") || containsNullArrayField(string(encoded), "counts") {
		t.Fatalf("expected empty arrays, got JSON %s", encoded)
	}
}

func TestReviewEventImportanceDoesNotFilterReturnedEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	insertReviewTestSource(t, st, "src:low-importance", "web", "Low importance", base.Add(time.Minute), "")

	page, err := st.ListReviewEvents(ctx, ReviewEventFilter{Cursor: ReviewCursor{EventAt: base}, Limit: 10})
	if err != nil {
		t.Fatalf("ListReviewEvents: %v", err)
	}
	if got, want := reviewEventKinds(page.Events), []string{ReviewEventKindSourceCreated}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected low-importance source_created event to be returned, got %v", got)
	}
	if page.Events[0].Importance >= 40 {
		t.Fatalf("test setup expected low importance event, got %+v", page.Events[0])
	}
}

func insertReviewTestItem(t *testing.T, st *Store, sourceKey string, sourceType string, title string, importedAt time.Time, updatedAt time.Time) int64 {
	t.Helper()

	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, '{}', ?, ?, ?)`,
		sourceKey,
		sourceType,
		sourceKey,
		"https://example.com/"+sourceKey,
		title,
		sourceKey+"-hash",
		"items/review/"+sourceKey+".md",
		importedAt.UTC().Format(time.RFC3339),
		updatedAt.UTC().Format(time.RFC3339),
		updatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert review item %s: %v", sourceKey, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("item id %s: %v", sourceKey, err)
	}
	return id
}

func insertReviewTestSource(t *testing.T, st *Store, sourceKey string, sourceType string, title string, createdAt time.Time, userTags string) int64 {
	t.Helper()

	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title, note_path, user_tags,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sourceKey,
		"https://example.com/"+sourceKey,
		"https://example.com/"+sourceKey,
		sourceType,
		"example.com",
		title,
		"sources/review/"+sourceKey+".md",
		userTags,
		createdAt.UTC().Format(time.RFC3339),
		createdAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert review source %s: %v", sourceKey, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("source id %s: %v", sourceKey, err)
	}
	return id
}

func updateReviewTestSourceExtracted(t *testing.T, st *Store, sourceID int64, at time.Time, status string) {
	t.Helper()
	if _, err := st.db.ExecContext(context.Background(), `
		UPDATE sources
		SET extracted_text = 'extracted text',
			extract_status = ?,
			extracted_at = ?,
			updated_at = ?
		WHERE id = ?`,
		status,
		at.UTC().Format(time.RFC3339),
		at.UTC().Format(time.RFC3339),
		sourceID,
	); err != nil {
		t.Fatalf("update review source extraction: %v", err)
	}
}

func updateReviewTestSourceSummary(t *testing.T, st *Store, sourceID int64, at time.Time, status string, message string) {
	t.Helper()
	summaryText := ""
	summaryError := ""
	if status == model.SourceSummaryStatusOK {
		summaryText = message
	} else {
		summaryError = message
	}
	if _, err := st.db.ExecContext(context.Background(), `
		UPDATE sources
		SET summary_text = ?,
			summary_status = ?,
			summary_error = ?,
			summarized_at = ?,
			updated_at = ?
		WHERE id = ?`,
		summaryText,
		status,
		summaryError,
		at.UTC().Format(time.RFC3339),
		at.UTC().Format(time.RFC3339),
		sourceID,
	); err != nil {
		t.Fatalf("update review source summary: %v", err)
	}
}

func insertReviewTestItemEnrichment(t *testing.T, st *Store, itemID int64, role string, status string, text string, errorText string, completedAt time.Time, updatedAt time.Time) {
	t.Helper()
	completedAtText := ""
	if !completedAt.IsZero() {
		completedAtText = completedAt.UTC().Format(time.RFC3339)
	}
	if _, err := st.db.ExecContext(context.Background(), `
		INSERT INTO item_enrichments (
			item_id, role, status, text, raw_json, error, model, prompt_version,
			tool, tool_version, input_hash, completed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, '{}', ?, '', '', '', '', '', ?, ?, ?)`,
		itemID,
		role,
		status,
		text,
		errorText,
		completedAtText,
		updatedAt.UTC().Format(time.RFC3339),
		updatedAt.UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert review item enrichment: %v", err)
	}
}

func reviewEventKinds(events []ReviewEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.EventKind)
	}
	return kinds
}

func reviewEventIDs(events []ReviewEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID)
	}
	return ids
}

func hasDuplicateStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func containsNullArrayField(raw string, field string) bool {
	return strings.Contains(raw, `"`+field+`":null`)
}

func TestMigrationAddsReviewEventIndexesToExistingDB(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openCurrentTestStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	db := openSQLForReviewTest(t, path)
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 11); err != nil {
		t.Fatalf("simulate pre-v11 migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 10`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openCurrentTestStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	for table, indexes := range map[string][]string{
		"items":            {"idx_items_imported_at"},
		"sources":          {"idx_sources_created_at", "idx_sources_extracted_at", "idx_sources_summarized_at", "idx_sources_extract_last_failed_at"},
		"item_enrichments": {"idx_item_enrichments_completed_at", "idx_item_enrichments_updated_at"},
		"feed_entries":     {"idx_feed_entries_last_changed_at"},
	} {
		for _, index := range indexes {
			if !reviewTestIndexExists(t, st, table, index) {
				t.Fatalf("expected %s index on %s", index, table)
			}
		}
	}
}

func openSQLForReviewTest(t *testing.T, path string) *sql.DB {
	t.Helper()

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	return db
}

func reviewTestIndexExists(t *testing.T, st *Store, table string, indexName string) bool {
	t.Helper()

	rows, err := st.db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		t.Fatalf("list indexes for %s: %v", table, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index for %s: %v", table, err)
		}
		if name == indexName {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes for %s: %v", table, err)
	}
	return false
}
