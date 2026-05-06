package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) scanSourceActivityEvents(ctx context.Context, query string, args ...any) ([]SourceActivityEvent, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list source activity events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var events []SourceActivityEvent
	for rows.Next() {
		var event SourceActivityEvent
		var eventAt string
		if err := rows.Scan(
			&event.SourceID,
			&event.SourceKey,
			&event.SourceType,
			&event.Domain,
			&event.FailureKind,
			&event.CanonicalURL,
			&event.Title,
			&event.NotePath,
			&event.EventKind,
			&event.Status,
			&event.Message,
			&eventAt,
		); err != nil {
			return nil, fmt.Errorf("scan source activity event: %w", err)
		}
		event.EventAt = parseStoredTime(eventAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source activity events: %w", err)
	}
	return events, nil
}

func (s *Store) scanSourceFailureHotspots(ctx context.Context, query string, args ...any) ([]SourceFailureHotspot, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list source failure hotspots: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var hotspots []SourceFailureHotspot
	for rows.Next() {
		var hotspot SourceFailureHotspot
		var eventAt string
		if err := rows.Scan(
			&hotspot.Domain,
			&hotspot.SourceType,
			&hotspot.Status,
			&hotspot.FailureKind,
			&hotspot.Count,
			&eventAt,
		); err != nil {
			return nil, fmt.Errorf("scan source failure hotspot: %w", err)
		}
		hotspot.LatestEventAt = parseStoredTime(eventAt)
		hotspots = append(hotspots, hotspot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source failure hotspots: %w", err)
	}
	return hotspots, nil
}

func (s *Store) scanCountBucketsByQuery(ctx context.Context, query string, args ...any) ([]CountBucket, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list count buckets: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	buckets, err := scanCountBuckets(rows, true)
	if err != nil {
		return nil, fmt.Errorf("scan count buckets: %w", err)
	}
	return buckets, nil
}

func (s *Store) scanCountByQuery(ctx context.Context, query string, args ...any) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count by query: %w", err)
	}
	return count, nil
}

func (s *Store) scanSourceActivityTrend(ctx context.Context, query string, now time.Time, window time.Duration, bucket time.Duration, args ...any) ([]SourceActivityTrendPoint, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list source activity trend: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return buildSourceActivityTrend(rows, now, window, bucket)
}
