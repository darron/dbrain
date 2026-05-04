package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) SourceActivityFeed(ctx context.Context, limit int) (SourceActivityFeed, error) {
	return s.SourceActivityFeedFiltered(ctx, SourceActivityFilter{Limit: limit})
}

func (s *Store) SourceActivityFeedFiltered(ctx context.Context, filter SourceActivityFilter) (SourceActivityFeed, error) {
	if filter.Limit <= 0 {
		filter.Limit = sourceActivityDefaultLimit
	}
	if filter.FailureOffset < 0 {
		filter.FailureOffset = 0
	}
	if filter.Window <= 0 {
		filter.Window = sourceActivityDefaultWindow
	}
	filter.FailureSort = normalizeSourceFailureSort(filter.FailureSort)

	successQuery, successArgs := sourceActivitySuccessesQuery(filter)
	successes, err := s.listSourceActivityEvents(ctx, successQuery, successArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	failureQuery, failureArgs := sourceActivityFailuresQuery(filter)
	failures, err := s.listSourceActivityEvents(ctx, failureQuery, failureArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	hotspotQuery, hotspotArgs := sourceFailureHotspotsQuery(filter)
	hotspots, err := s.listSourceFailureHotspots(ctx, hotspotQuery, hotspotArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	failureKindQuery, failureKindArgs := sourceFailureFacetQuery(filter, "failure_kind")
	failureKinds, err := s.listCountBuckets(ctx, failureKindQuery, failureKindArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	failureStatusQuery, failureStatusArgs := sourceFailureFacetQuery(filter, "status")
	failureStatuses, err := s.listCountBuckets(ctx, failureStatusQuery, failureStatusArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	failureDomainQuery, failureDomainArgs := sourceFailureFacetQuery(filter, "domain")
	failureDomains, err := s.listCountBuckets(ctx, failureDomainQuery, failureDomainArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	failureTableQuery, failureTableArgs := sourceFailureTableQuery(filter)
	failureTable, err := s.listSourceActivityEvents(ctx, failureTableQuery, failureTableArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	failureCountQuery, failureCountArgs := sourceFailureCountQuery(filter)
	failureTableTotal, err := s.countByQuery(ctx, failureCountQuery, failureCountArgs...)
	if err != nil {
		return SourceActivityFeed{}, err
	}
	trend, trendBucket, err := s.listSourceActivityTrend(ctx, filter)
	if err != nil {
		return SourceActivityFeed{}, err
	}

	return SourceActivityFeed{
		Window:             filter.Window.String(),
		RecentSuccesses:    successes,
		RecentFailures:     failures,
		FailureHotspots:    hotspots,
		FailureKinds:       failureKinds,
		FailureStatuses:    failureStatuses,
		FailureDomains:     failureDomains,
		FailureTable:       failureTable,
		FailureTableTotal:  failureTableTotal,
		FailureTableOffset: filter.FailureOffset,
		FailureTableLimit:  filter.Limit,
		FailureTableSort:   filter.FailureSort,
		TrendBucket:        trendBucket.String(),
		Trend:              trend,
	}, nil
}

func (s *Store) listSourceActivityEvents(ctx context.Context, query string, args ...any) ([]SourceActivityEvent, error) {
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

func (s *Store) listSourceFailureHotspots(ctx context.Context, query string, args ...any) ([]SourceFailureHotspot, error) {
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

func (s *Store) listCountBuckets(ctx context.Context, query string, args ...any) ([]CountBucket, error) {
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

func (s *Store) countByQuery(ctx context.Context, query string, args ...any) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count by query: %w", err)
	}
	return count, nil
}

func (s *Store) listSourceActivityTrend(ctx context.Context, filter SourceActivityFilter) ([]SourceActivityTrendPoint, time.Duration, error) {
	bucket := sourceActivityTrendBucket(filter.Window)
	now := time.Now().UTC()
	query, args := sourceActivityTrendQuery(filter)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list source activity trend: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	points, err := buildSourceActivityTrend(rows, now, filter.Window, bucket)
	if err != nil {
		return nil, 0, err
	}
	return points, bucket, nil
}
