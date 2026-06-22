package store

import (
	"context"
	"fmt"
)

func (s *Store) ListReviewEvents(ctx context.Context, filter ReviewEventFilter) (ReviewEventPage, error) {
	if filter.Limit <= 0 {
		filter.Limit = reviewEventsDefaultLimit
	}
	if filter.Limit > reviewEventsMaxLimit {
		filter.Limit = reviewEventsMaxLimit
	}
	view, err := NormalizeReviewEventView(filter.View)
	if err != nil {
		return ReviewEventPage{}, err
	}
	kinds, err := NormalizeReviewEventTypes(filter.Types)
	if err != nil {
		return ReviewEventPage{}, err
	}
	queryFilter := filter
	queryFilter.Types = kinds
	queryFilter.Limit = filter.Limit + 1
	query, args := reviewEventsQuery(queryFilter)
	events, err := s.scanReviewEvents(ctx, query, args...)
	if err != nil {
		return ReviewEventPage{}, err
	}
	truncated := false
	if len(events) > filter.Limit {
		truncated = true
		events = events[:filter.Limit]
	}
	if events == nil {
		events = make([]ReviewEvent, 0)
	}
	counts := reviewEventCounts(events)
	if counts == nil {
		counts = make([]CountBucket, 0)
	}
	entities := make([]ReviewEntityGroup, 0)
	outputEvents := events
	if view == ReviewEventViewEntities {
		entities = groupReviewEvents(events)
		outputEvents = make([]ReviewEvent, 0)
	}
	nextCursor := filter.Cursor
	var highWatermark = filter.Cursor.EventAt
	if len(events) > 0 {
		last := events[len(events)-1]
		nextCursor = ReviewCursor{
			EventAt:    last.EventAt,
			EventKind:  last.EventKind,
			EntityKind: last.EntityKind,
			EntityID:   last.EntityID,
			EventStage: last.EventStage,
		}
		highWatermark = last.EventAt
	}
	nextToken, err := EncodeReviewCursor(nextCursor)
	if err != nil {
		return ReviewEventPage{}, fmt.Errorf("encode next review cursor: %w", err)
	}
	return ReviewEventPage{
		View:          view,
		Cursor:        filter.Cursor,
		NextCursor:    nextToken,
		HighWatermark: highWatermark,
		Events:        outputEvents,
		Entities:      entities,
		Truncated:     truncated,
		Counts:        counts,
	}, nil
}

func (s *Store) ensureReviewEventIndexes() error {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_items_imported_at ON items(imported_at);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_created_at ON sources(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_extracted_at ON sources(extracted_at);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_summarized_at ON sources(summarized_at);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_extract_last_failed_at ON sources(extract_last_failed_at);`,
		`CREATE INDEX IF NOT EXISTS idx_item_enrichments_completed_at ON item_enrichments(completed_at);`,
		`CREATE INDEX IF NOT EXISTS idx_item_enrichments_updated_at ON item_enrichments(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_feed_entries_last_changed_at ON feed_entries(last_changed_at);`,
	}
	for _, stmt := range statements {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("ensure review event index: %w", err)
		}
	}
	return nil
}
