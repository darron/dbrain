package store

import (
	"context"
	"time"
)

func (s *Store) Backlog(ctx context.Context, promptVersion string, toolName string, toolVersion string) (BacklogStats, error) {
	stats := BacklogStats{}
	policy := newSourceEnrichmentPolicy(time.Now().UTC(), promptVersion, toolName, toolVersion)

	xWhere := xItemSourceTypeWhere + `
		AND external_id != ''
		AND ` + xHydrationCandidateWhere
	if value, err := s.countWhere(ctx, "items", xWhere); err != nil {
		return BacklogStats{}, err
	} else {
		stats.XHydrationPending = value
	}

	linkWhere := linkDiscoveryItemSourceTypeWhere + `
		AND links_json != '[]'
		AND (link_extract_synced_at = '' OR updated_at > link_extract_synced_at)`
	if value, err := s.countWhere(ctx, "items", linkWhere); err != nil {
		return BacklogStats{}, err
	} else {
		stats.LinkDiscoveryPending = value
	}

	extractWhere, extractArgs := policy.extractBacklogWhere()
	extractBuckets, err := s.countGroupedWhere(ctx, "sources", "source_type", extractWhere, extractArgs...)
	if err != nil {
		return BacklogStats{}, err
	}
	stats.SourceExtractionPendingByType = extractBuckets
	for _, bucket := range extractBuckets {
		stats.SourceExtractionPending += bucket.Count
	}

	summaryWhere, args := policy.summaryBacklogWhere()
	summaryBuckets, err := s.countGroupedWhere(ctx, "sources", "source_type", summaryWhere, args...)
	if err != nil {
		return BacklogStats{}, err
	}
	stats.SourceSummaryPendingByType = summaryBuckets
	for _, bucket := range summaryBuckets {
		stats.SourceSummaryPending += bucket.Count
	}

	stats.Drained = stats.XHydrationPending == 0 &&
		stats.LinkDiscoveryPending == 0 &&
		stats.SourceExtractionPending == 0 &&
		stats.SourceSummaryPending == 0

	return stats, nil
}
