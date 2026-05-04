package store

import (
	"context"
	"strings"
	"time"
)

func (s *Store) Backlog(ctx context.Context, promptVersion string, toolName string, toolVersion string) (BacklogStats, error) {
	stats := BacklogStats{}
	summaryPromptVersion := strings.TrimSpace(promptVersion)
	summaryTool := strings.TrimSpace(toolName)
	summaryToolVersion := strings.TrimSpace(toolVersion)

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

	extractWhere, extractArgs := sourceExtractBacklogWhere(time.Now().UTC())
	extractBuckets, err := s.countGroupedWhere(ctx, "sources", "source_type", extractWhere, extractArgs...)
	if err != nil {
		return BacklogStats{}, err
	}
	stats.SourceExtractionPendingByType = extractBuckets
	for _, bucket := range extractBuckets {
		stats.SourceExtractionPending += bucket.Count
	}

	summaryWhere, args := sourceSummaryBacklogWhere(summaryPromptVersion, summaryTool, summaryToolVersion)
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

func sourceSummaryBacklogWhere(promptVersion string, toolName string, toolVersion string) (string, []any) {
	staleWhere, args := sourceSummaryStaleWhere(promptVersion, toolName, toolVersion)
	return `extract_status IN ('ok', 'empty') AND ` + staleWhere, args
}
