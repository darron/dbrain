package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type CountBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type SourceCountFilter struct {
	SourceType    string
	ExtractTool   string
	SummaryStatus string
	ExtractStatus string
}

type ActivityStats struct {
	Now                       time.Time `json:"now"`
	Window                    string    `json:"window"`
	LatestItemUpdatedAt       time.Time `json:"latest_item_updated_at"`
	LatestSourceUpdatedAt     time.Time `json:"latest_source_updated_at"`
	LatestSourceSummaryAt     time.Time `json:"latest_source_summary_at"`
	ItemsUpdatedInWindow      int       `json:"items_updated_in_window"`
	SourcesUpdatedInWindow    int       `json:"sources_updated_in_window"`
	SourcesSummarizedInWindow int       `json:"sources_summarized_in_window"`
}

type BacklogStats struct {
	XHydrationPending             int           `json:"x_hydration_pending"`
	LinkDiscoveryPending          int           `json:"link_discovery_pending"`
	SourceExtractionPending       int           `json:"source_extraction_pending"`
	SourceSummaryPending          int           `json:"source_summary_pending"`
	SourceExtractionPendingByType []CountBucket `json:"source_extraction_pending_by_type"`
	SourceSummaryPendingByType    []CountBucket `json:"source_summary_pending_by_type"`
	Drained                       bool          `json:"drained"`
}

func (s *Store) CountItems(ctx context.Context, sourceType string, groupBy string) ([]CountBucket, error) {
	column, grouped, err := itemGroupColumn(groupBy)
	if err != nil {
		return nil, err
	}

	query := `SELECT `
	if grouped {
		query += column + `, COUNT(*) `
	} else {
		query += `COUNT(*) `
	}
	query += `FROM items`

	args := make([]any, 0, 1)
	where := make([]string, 0, 1)
	if value := strings.TrimSpace(sourceType); value != "" {
		where = append(where, `source_type = ?`)
		args = append(args, value)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	if grouped {
		query += ` GROUP BY ` + column + ` ORDER BY COUNT(*) DESC, ` + column + ` ASC`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("count items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanCountBuckets(rows, grouped)
}

func (s *Store) CountSources(ctx context.Context, filter SourceCountFilter, groupBy string) ([]CountBucket, error) {
	column, grouped, err := sourceGroupColumn(groupBy)
	if err != nil {
		return nil, err
	}

	query := `SELECT `
	if grouped {
		query += column + `, COUNT(*) `
	} else {
		query += `COUNT(*) `
	}
	query += `FROM sources`

	args := make([]any, 0, 4)
	where := make([]string, 0, 4)
	if value := strings.TrimSpace(filter.SourceType); value != "" {
		where = append(where, `source_type = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ExtractTool); value != "" {
		where = append(where, `extract_tool = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.SummaryStatus); value != "" {
		where = append(where, `summary_status = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ExtractStatus); value != "" {
		where = append(where, `extract_status = ?`)
		args = append(args, value)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	if grouped {
		query += ` GROUP BY ` + column + ` ORDER BY COUNT(*) DESC, ` + column + ` ASC`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("count sources: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanCountBuckets(rows, grouped)
}

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

func (s *Store) Backlog(ctx context.Context, promptVersion string, toolName string, toolVersion string) (BacklogStats, error) {
	stats := BacklogStats{}

	xWhere := `source_type = 'x_bookmark'
		AND external_id != ''
		AND (x_post_status = ''
			OR x_post_status = 'api_error'
			OR x_post_status = 'error'
			OR x_post_status = 'rate_limited')`
	if value, err := s.countWhere(ctx, "items", xWhere); err != nil {
		return BacklogStats{}, err
	} else {
		stats.XHydrationPending = value
	}

	linkWhere := `source_type = 'x_bookmark'
		AND links_json != '[]'
		AND (link_extract_synced_at = '' OR updated_at > link_extract_synced_at)`
	if value, err := s.countWhere(ctx, "items", linkWhere); err != nil {
		return BacklogStats{}, err
	} else {
		stats.LinkDiscoveryPending = value
	}

	extractWhere := `extract_status = '' OR extract_status = 'error'`
	extractBuckets, err := s.countGroupedWhere(ctx, "sources", "source_type", extractWhere)
	if err != nil {
		return BacklogStats{}, err
	}
	stats.SourceExtractionPendingByType = extractBuckets
	for _, bucket := range extractBuckets {
		stats.SourceExtractionPending += bucket.Count
	}

	summaryWhere, args := sourceSummaryBacklogWhere(promptVersion, toolName, toolVersion)
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

func (s *Store) maxTimestamp(ctx context.Context, table string, column string, where string) (time.Time, error) {
	query := `SELECT COALESCE(MAX(` + column + `), '') FROM ` + table
	if strings.TrimSpace(where) != "" {
		query += ` WHERE ` + where
	}

	var value string
	if err := s.db.QueryRowContext(ctx, query).Scan(&value); err != nil {
		return time.Time{}, fmt.Errorf("max timestamp %s.%s: %w", table, column, err)
	}
	return parseStoredTime(value), nil
}

func (s *Store) countWhere(ctx context.Context, table string, where string, args ...any) (int, error) {
	query := `SELECT COUNT(*) FROM ` + table
	if strings.TrimSpace(where) != "" {
		query += ` WHERE ` + where
	}

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return count, nil
}

func (s *Store) countGroupedWhere(ctx context.Context, table string, groupBy string, where string, args ...any) ([]CountBucket, error) {
	query := `SELECT ` + groupBy + `, COUNT(*) FROM ` + table
	if strings.TrimSpace(where) != "" {
		query += ` WHERE ` + where
	}
	query += ` GROUP BY ` + groupBy + ` ORDER BY COUNT(*) DESC, ` + groupBy + ` ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("count grouped %s: %w", table, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanCountBuckets(rows, true)
}

func sourceSummaryBacklogWhere(promptVersion string, toolName string, toolVersion string) (string, []any) {
	parts := []string{
		"extract_status IN ('ok', 'empty')",
		"(summary_status = '' OR summary_status = 'error' OR summary_content_hash != content_hash OR summary_prompt_version != ?",
	}
	args := []any{promptVersion}
	if strings.TrimSpace(toolName) != "" {
		parts[1] += " OR summary_tool != ?"
		args = append(args, toolName)
	}
	if strings.TrimSpace(toolVersion) != "" {
		parts[1] += " OR summary_tool_version != ?"
		args = append(args, toolVersion)
	}
	parts[1] += ")"
	return strings.Join(parts, " AND "), args
}

func scanCountBuckets(rows rowScanner, grouped bool) ([]CountBucket, error) {
	var buckets []CountBucket
	for rows.Next() {
		var bucket CountBucket
		if grouped {
			if err := rows.Scan(&bucket.Key, &bucket.Count); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&bucket.Count); err != nil {
				return nil, err
			}
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buckets, nil
}

type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func itemGroupColumn(groupBy string) (string, bool, error) {
	switch strings.TrimSpace(groupBy) {
	case "", "source-type":
		return "source_type", true, nil
	case "none":
		return "", false, nil
	default:
		return "", false, fmt.Errorf("unsupported item group-by %q", groupBy)
	}
}

func sourceGroupColumn(groupBy string) (string, bool, error) {
	switch strings.TrimSpace(groupBy) {
	case "", "source-type":
		return "source_type", true, nil
	case "summary-status":
		return "summary_status", true, nil
	case "extract-status":
		return "extract_status", true, nil
	case "none":
		return "", false, nil
	default:
		return "", false, fmt.Errorf("unsupported source group-by %q", groupBy)
	}
}
