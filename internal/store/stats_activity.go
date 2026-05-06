package store

import (
	"context"
	"time"
)

func (s *Store) Activity(ctx context.Context, now time.Time, window time.Duration) (ActivityStats, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if window <= 0 {
		window = 15 * time.Minute
	}

	stats := ActivityStats{
		Now:    now,
		Window: window.String(),
	}

	if value, err := s.maxTimestamp(ctx, "items", "updated_at", ""); err != nil {
		return ActivityStats{}, err
	} else {
		stats.LatestItemUpdatedAt = value
	}
	if value, err := s.maxTimestamp(ctx, "sources", "updated_at", ""); err != nil {
		return ActivityStats{}, err
	} else {
		stats.LatestSourceUpdatedAt = value
	}
	if value, err := s.maxTimestamp(ctx, "sources", "summarized_at", "summarized_at != ''"); err != nil {
		return ActivityStats{}, err
	} else {
		stats.LatestSourceSummaryAt = value
	}

	cutoff := now.Add(-window).UTC().Format(time.RFC3339)
	if value, err := s.countWhere(ctx, "items", "updated_at >= ?", cutoff); err != nil {
		return ActivityStats{}, err
	} else {
		stats.ItemsUpdatedInWindow = value
	}
	if value, err := s.countWhere(ctx, "sources", "updated_at >= ?", cutoff); err != nil {
		return ActivityStats{}, err
	} else {
		stats.SourcesUpdatedInWindow = value
	}
	if value, err := s.countWhere(ctx, "sources", "summarized_at != '' AND summarized_at >= ?", cutoff); err != nil {
		return ActivityStats{}, err
	} else {
		stats.SourcesSummarizedInWindow = value
	}

	return stats, nil
}
