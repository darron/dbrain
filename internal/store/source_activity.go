package store

import (
	"context"
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
	return s.scanSourceActivityEvents(ctx, query, args...)
}

func (s *Store) listSourceFailureHotspots(ctx context.Context, query string, args ...any) ([]SourceFailureHotspot, error) {
	return s.scanSourceFailureHotspots(ctx, query, args...)
}

func (s *Store) listCountBuckets(ctx context.Context, query string, args ...any) ([]CountBucket, error) {
	return s.scanCountBucketsByQuery(ctx, query, args...)
}

func (s *Store) countByQuery(ctx context.Context, query string, args ...any) (int, error) {
	return s.scanCountByQuery(ctx, query, args...)
}

func (s *Store) listSourceActivityTrend(ctx context.Context, filter SourceActivityFilter) ([]SourceActivityTrendPoint, time.Duration, error) {
	bucket := sourceActivityTrendBucket(filter.Window)
	now := time.Now().UTC()
	query, args := sourceActivityTrendQuery(filter)
	points, err := s.scanSourceActivityTrend(ctx, query, now, filter.Window, bucket, args...)
	if err != nil {
		return nil, 0, err
	}
	return points, bucket, nil
}
