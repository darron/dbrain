package store

import (
	"context"
	"fmt"
	"sort"
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

type PipelineStageRow struct {
	Kind           string  `json:"kind"`
	Total          int     `json:"total"`
	Current        int     `json:"current"`
	Pending        int     `json:"pending"`
	Blocked        int     `json:"blocked"`
	Failed         int     `json:"failed"`
	PercentCurrent float64 `json:"percent_current"`
}

type PipelineStats struct {
	SummaryPromptVersion string             `json:"summary_prompt_version"`
	SummaryTool          string             `json:"summary_tool"`
	SummaryToolVersion   string             `json:"summary_tool_version"`
	Hydration            []PipelineStageRow `json:"hydration"`
	Extraction           []PipelineStageRow `json:"extraction"`
	Summary              []PipelineStageRow `json:"summary"`
	Transcription        []PipelineStageRow `json:"transcription"`
}

type SourceActivityEvent struct {
	SourceID     int64     `json:"source_id"`
	SourceKey    string    `json:"source_key"`
	SourceType   string    `json:"source_type"`
	Domain       string    `json:"domain"`
	FailureKind  string    `json:"failure_kind,omitempty"`
	CanonicalURL string    `json:"canonical_url"`
	Title        string    `json:"title"`
	NotePath     string    `json:"note_path"`
	EventKind    string    `json:"event_kind"`
	Status       string    `json:"status"`
	Message      string    `json:"message,omitempty"`
	EventAt      time.Time `json:"event_at"`
}

type SourceFailureHotspot struct {
	Domain        string    `json:"domain"`
	SourceType    string    `json:"source_type"`
	Status        string    `json:"status"`
	FailureKind   string    `json:"failure_kind,omitempty"`
	Count         int       `json:"count"`
	LatestEventAt time.Time `json:"latest_event_at"`
}

type SourceActivityTrendPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	Label        string    `json:"label"`
	SuccessCount int       `json:"success_count"`
	FailureCount int       `json:"failure_count"`
}

type SourceActivityFeed struct {
	Window             string                     `json:"window"`
	RecentSuccesses    []SourceActivityEvent      `json:"recent_successes"`
	RecentFailures     []SourceActivityEvent      `json:"recent_failures"`
	FailureHotspots    []SourceFailureHotspot     `json:"failure_hotspots"`
	FailureKinds       []CountBucket              `json:"failure_kinds"`
	FailureStatuses    []CountBucket              `json:"failure_statuses"`
	FailureDomains     []CountBucket              `json:"failure_domains"`
	FailureTable       []SourceActivityEvent      `json:"failure_table"`
	FailureTableTotal  int                        `json:"failure_table_total"`
	FailureTableOffset int                        `json:"failure_table_offset"`
	FailureTableLimit  int                        `json:"failure_table_limit"`
	FailureTableSort   string                     `json:"failure_table_sort"`
	TrendBucket        string                     `json:"trend_bucket"`
	Trend              []SourceActivityTrendPoint `json:"trend"`
}

type SourceActivityFilter struct {
	Limit         int
	FailureOffset int
	FailureSort   string
	SourceType    string
	Domain        string
	Status        string
	FailureKind   string
	Message       string
	Window        time.Duration
}

const (
	sourceActivityDefaultLimit        = 8
	sourceActivityDefaultWindow       = 24 * time.Hour
	sourceActivityDefaultFacetLimit   = 8
	sourceActivityDefaultHotspotLimit = 8
	sourceActivityDefaultFailureSort  = "newest"
)

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
	summaryPromptVersion := strings.TrimSpace(promptVersion)
	summaryTool := strings.TrimSpace(toolName)
	summaryToolVersion := strings.TrimSpace(toolVersion)
	strictSummaryFreshness := summaryPromptVersion != "" || summaryTool != "" || summaryToolVersion != ""

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

	extractWhere, extractArgs := sourceExtractBacklogWhere(time.Now().UTC())
	extractBuckets, err := s.countGroupedWhere(ctx, "sources", "source_type", extractWhere, extractArgs...)
	if err != nil {
		return BacklogStats{}, err
	}
	stats.SourceExtractionPendingByType = extractBuckets
	for _, bucket := range extractBuckets {
		stats.SourceExtractionPending += bucket.Count
	}

	summaryWhere := `extract_status IN ('ok', 'empty') AND (summary_status = '' OR summary_status = 'error' OR summary_content_hash != content_hash)`
	args := []any{}
	if strictSummaryFreshness {
		summaryWhere, args = sourceSummaryBacklogWhere(summaryPromptVersion, summaryTool, summaryToolVersion)
	}
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

func (s *Store) Pipeline(ctx context.Context, promptVersion string, toolName string, toolVersion string) (PipelineStats, error) {
	summaryPromptVersion := strings.TrimSpace(promptVersion)
	summaryTool := strings.TrimSpace(toolName)
	summaryToolVersion := strings.TrimSpace(toolVersion)
	strictSummaryFreshness := summaryPromptVersion != "" || summaryTool != "" || summaryToolVersion != ""

	stats := PipelineStats{}
	if strictSummaryFreshness {
		stats.SummaryPromptVersion = summaryPromptVersion
		stats.SummaryTool = summaryTool
		stats.SummaryToolVersion = summaryToolVersion
	}

	hydrationTotal, err := s.countGroupedWhere(ctx, "items", "source_type", `source_type = 'x_bookmark' AND external_id != ''`)
	if err != nil {
		return PipelineStats{}, err
	}
	hydrationCurrent, err := s.countGroupedWhere(ctx, "items", "source_type", `source_type = 'x_bookmark' AND external_id != '' AND x_post_status LIKE 'ok_%'`)
	if err != nil {
		return PipelineStats{}, err
	}
	hydrationPending, err := s.countGroupedWhere(ctx, "items", "source_type", `source_type = 'x_bookmark' AND external_id != '' AND (x_post_status = '' OR x_post_status = 'api_error' OR x_post_status = 'error' OR x_post_status = 'rate_limited')`)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Hydration = buildPipelineStageRows(hydrationTotal, hydrationCurrent, hydrationPending, nil)

	extractWhere, extractArgs := sourceExtractBacklogWhere(time.Now().UTC())
	extractionTotal, err := s.countGroupedWhere(ctx, "sources", "source_type", "")
	if err != nil {
		return PipelineStats{}, err
	}
	extractionCurrent, err := s.countGroupedWhere(ctx, "sources", "source_type", `extract_status IN ('ok', 'empty')`)
	if err != nil {
		return PipelineStats{}, err
	}
	extractionPending, err := s.countGroupedWhere(ctx, "sources", "source_type", extractWhere, extractArgs...)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Extraction = buildPipelineStageRows(extractionTotal, extractionCurrent, extractionPending, nil)

	summaryTotal, err := s.countGroupedWhere(ctx, "sources", "source_type", "")
	if err != nil {
		return PipelineStats{}, err
	}
	readyForSummaryWhere := `extract_status IN ('ok', 'empty')`
	var summaryCurrent []CountBucket
	var summaryPending []CountBucket
	if strictSummaryFreshness {
		summaryStaleWhere, summaryArgs := sourceSummaryStaleWhere(summaryPromptVersion, summaryTool, summaryToolVersion)
		summaryCurrent, err = s.countGroupedWhere(
			ctx,
			"sources",
			"source_type",
			readyForSummaryWhere+` AND NOT `+summaryStaleWhere,
			summaryArgs...,
		)
		if err != nil {
			return PipelineStats{}, err
		}
		summaryPendingWhere, summaryPendingArgs := sourceSummaryBacklogWhere(summaryPromptVersion, summaryTool, summaryToolVersion)
		summaryPending, err = s.countGroupedWhere(ctx, "sources", "source_type", summaryPendingWhere, summaryPendingArgs...)
		if err != nil {
			return PipelineStats{}, err
		}
	} else {
		summaryCurrent, err = s.countGroupedWhere(
			ctx,
			"sources",
			"source_type",
			readyForSummaryWhere+` AND summary_status = 'ok' AND summary_content_hash = content_hash`,
		)
		if err != nil {
			return PipelineStats{}, err
		}
		summaryPending, err = s.countGroupedWhere(
			ctx,
			"sources",
			"source_type",
			readyForSummaryWhere+` AND (summary_status = '' OR summary_status = 'error' OR summary_content_hash != content_hash)`,
		)
		if err != nil {
			return PipelineStats{}, err
		}
	}
	summaryBlocked, err := s.countGroupedWhere(ctx, "sources", "source_type", `NOT (`+readyForSummaryWhere+`)`)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Summary = buildPipelineStageRows(summaryTotal, summaryCurrent, summaryPending, summaryBlocked)

	transcriptionRow, ok, err := s.pipelineXMediaTranscriptionRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.Transcription = []PipelineStageRow{transcriptionRow}
	}

	return stats, nil
}

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

func (s *Store) pipelineXMediaTranscriptionRow(ctx context.Context) (PipelineStageRow, bool, error) {
	const transcriptTitle = "X Media Transcript"

	candidateWhere := `source_type = 'x_bookmark'
		AND external_id != ''
		AND EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND a.download_status = 'downloaded'
				AND a.local_path != ''
				AND a.media_type IN ('video', 'animated_gif')
		)
		AND (
			article_text = ''
			OR article_title = ?
			OR x_media_transcript_status != ''
		)`

	total, err := s.countWhere(ctx, "items", candidateWhere, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND article_title = ? AND article_text != ''`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND NOT (article_title = ? AND article_text != '') AND x_media_transcript_status = ''`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND NOT (article_title = ? AND article_text != '') AND x_media_transcript_status != '' AND x_media_transcript_status != 'ok'`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    "x_media_transcript",
		Total:   total,
		Current: current,
		Pending: pending,
		Failed:  failed,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func buildPipelineStageRows(total []CountBucket, current []CountBucket, pending []CountBucket, blocked []CountBucket) []PipelineStageRow {
	if len(total) == 0 {
		return nil
	}

	currentByKind := countBucketMap(current)
	pendingByKind := countBucketMap(pending)
	blockedByKind := countBucketMap(blocked)

	rows := make([]PipelineStageRow, 0, len(total)+1)
	for _, bucket := range total {
		row := PipelineStageRow{
			Kind:    bucket.Key,
			Total:   bucket.Count,
			Current: currentByKind[bucket.Key],
			Pending: pendingByKind[bucket.Key],
			Blocked: blockedByKind[bucket.Key],
		}
		finalizePipelineStageRow(&row)
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Total == rows[j].Total {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Total > rows[j].Total
	})

	return append([]PipelineStageRow{aggregatePipelineStageRows(rows)}, rows...)
}

func aggregatePipelineStageRows(rows []PipelineStageRow) PipelineStageRow {
	total := PipelineStageRow{Kind: "ALL"}
	for _, row := range rows {
		total.Total += row.Total
		total.Current += row.Current
		total.Pending += row.Pending
		total.Blocked += row.Blocked
		total.Failed += row.Failed
	}
	finalizePipelineStageRow(&total)
	return total
}

func finalizePipelineStageRow(row *PipelineStageRow) {
	if row == nil {
		return
	}
	known := row.Current + row.Pending + row.Blocked + row.Failed
	if row.Total > known {
		row.Failed += row.Total - known
	}
	if row.Total > 0 {
		row.PercentCurrent = (float64(row.Current) / float64(row.Total)) * 100
	}
}

func countBucketMap(buckets []CountBucket) map[string]int {
	out := make(map[string]int, len(buckets))
	for _, bucket := range buckets {
		out[bucket.Key] = bucket.Count
	}
	return out
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

func sourceActivitySuccessesQuery(filter SourceActivityFilter) (string, []any) {
	query := `
		SELECT source_id, source_key, source_type, domain, failure_kind, canonical_url, title, note_path, event_kind, status, message, event_at
		FROM (` + sourceActivitySuccessUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	query += where + `
		ORDER BY event_at DESC
		LIMIT ?`
	args = append(args, filter.Limit)
	return query, args
}

func sourceActivityFailuresQuery(filter SourceActivityFilter) (string, []any) {
	query := `
		SELECT source_id, source_key, source_type, domain, failure_kind, canonical_url, title, note_path, event_kind, status, message, event_at
		FROM (` + sourceActivityFailureUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	query += where + `
		ORDER BY event_at DESC
		LIMIT ?`
	args = append(args, filter.Limit)
	return query, args
}

func sourceFailureHotspotsQuery(filter SourceActivityFilter) (string, []any) {
	query := `
		SELECT domain, source_type, status, failure_kind, COUNT(*) AS failure_count, MAX(event_at) AS latest_event_at
		FROM (` + sourceActivityFailureUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	query += where + `
		GROUP BY domain, source_type, status, failure_kind
		HAVING COUNT(*) >= 2
		ORDER BY failure_count DESC, latest_event_at DESC, domain ASC, source_type ASC, status ASC, failure_kind ASC
		LIMIT ?`
	args = append(args, sourceActivityDefaultHotspotLimit)
	return query, args
}

func sourceFailureFacetQuery(filter SourceActivityFilter, column string) (string, []any) {
	query := `
		SELECT ` + column + ` AS facet_key, COUNT(*) AS facet_count
		FROM (` + sourceActivityFailureUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	query += where
	if column == "domain" {
		if strings.TrimSpace(where) == "" {
			query += ` WHERE domain != ''`
		} else {
			query += ` AND domain != ''`
		}
	}
	query += `
		GROUP BY ` + column + `
		ORDER BY facet_count DESC, facet_key ASC
		LIMIT ?`
	args = append(args, sourceActivityDefaultFacetLimit)
	return query, args
}

func sourceFailureTableQuery(filter SourceActivityFilter) (string, []any) {
	query := `
		SELECT source_id, source_key, source_type, domain, failure_kind, canonical_url, title, note_path, event_kind, status, message, event_at
		FROM (` + sourceActivityFailureUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	query += where + `
		ORDER BY ` + sourceFailureSortClause(filter.FailureSort) + `
		LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.FailureOffset)
	return query, args
}

func sourceFailureCountQuery(filter SourceActivityFilter) (string, []any) {
	query := `SELECT COUNT(*) FROM (` + sourceActivityFailureUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	return query + where, args
}

func sourceActivityTrendQuery(filter SourceActivityFilter) (string, []any) {
	query := `
		SELECT event_class, event_at
		FROM (` + sourceActivityTrendUnionQuery + `) activity`
	where, args := sourceActivityOuterWhere(filter)
	query += where + `
		ORDER BY event_at ASC`
	return query, args
}

func sourceActivityOuterWhere(filter SourceActivityFilter) (string, []any) {
	conditions := make([]string, 0, 5)
	args := make([]any, 0, 5)
	if value := strings.TrimSpace(filter.SourceType); value != "" {
		conditions = append(conditions, "source_type = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Domain); value != "" {
		conditions = append(conditions, "LOWER(domain) LIKE ?")
		args = append(args, "%"+strings.ToLower(value)+"%")
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.FailureKind); value != "" {
		conditions = append(conditions, "failure_kind = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Message); value != "" {
		conditions = append(conditions, "LOWER(message) LIKE ?")
		args = append(args, "%"+strings.ToLower(value)+"%")
	}
	if filter.Window > 0 {
		conditions = append(conditions, "event_at >= ?")
		args = append(args, time.Now().UTC().Add(-filter.Window).Format(time.RFC3339))
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func sourceFailureSortClause(value string) string {
	switch normalizeSourceFailureSort(value) {
	case "oldest":
		return "event_at ASC, source_key ASC"
	case "domain":
		return "domain ASC, event_at DESC, source_key ASC"
	case "kind":
		return "failure_kind ASC, event_at DESC, source_key ASC"
	case "status":
		return "status ASC, event_at DESC, source_key ASC"
	default:
		return "event_at DESC, source_key ASC"
	}
}

func normalizeSourceFailureSort(value string) string {
	switch strings.TrimSpace(value) {
	case "oldest", "domain", "kind", "status":
		return strings.TrimSpace(value)
	default:
		return sourceActivityDefaultFailureSort
	}
}

func sourceActivityTrendBucket(window time.Duration) time.Duration {
	switch {
	case window <= 12*time.Hour:
		return time.Hour
	case window <= 24*time.Hour:
		return 2 * time.Hour
	case window <= 72*time.Hour:
		return 6 * time.Hour
	case window <= 168*time.Hour:
		return 12 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func buildSourceActivityTrend(rows rowScanner, now time.Time, window time.Duration, bucket time.Duration) ([]SourceActivityTrendPoint, error) {
	if bucket <= 0 {
		bucket = time.Hour
	}
	end := now.UTC().Truncate(bucket).Add(bucket)
	bucketCount := int(window / bucket)
	if window%bucket != 0 {
		bucketCount++
	}
	if bucketCount < 1 {
		bucketCount = 1
	}
	start := end.Add(-time.Duration(bucketCount) * bucket)

	points := make([]SourceActivityTrendPoint, bucketCount)
	indexByBucket := make(map[time.Time]int, bucketCount)
	for i := 0; i < bucketCount; i++ {
		bucketStart := start.Add(time.Duration(i) * bucket)
		points[i] = SourceActivityTrendPoint{
			BucketStart: bucketStart,
			Label:       sourceActivityTrendLabel(bucketStart, bucket),
		}
		indexByBucket[bucketStart] = i
	}

	for rows.Next() {
		var eventClass string
		var eventAtRaw string
		if err := rows.Scan(&eventClass, &eventAtRaw); err != nil {
			return nil, fmt.Errorf("scan source activity trend row: %w", err)
		}
		eventAt := parseStoredTime(eventAtRaw)
		if eventAt.IsZero() || eventAt.Before(start) || !eventAt.Before(end) {
			continue
		}
		bucketStart := eventAt.UTC().Truncate(bucket)
		index, ok := indexByBucket[bucketStart]
		if !ok {
			continue
		}
		if eventClass == "success" {
			points[index].SuccessCount++
		} else {
			points[index].FailureCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source activity trend rows: %w", err)
	}
	return points, nil
}

func sourceActivityTrendLabel(bucketStart time.Time, bucket time.Duration) string {
	if bucket >= 24*time.Hour {
		return bucketStart.Format("Jan 2")
	}
	if bucket >= 6*time.Hour {
		return bucketStart.Format("Jan 2 15:04")
	}
	return bucketStart.Format("15:04")
}

const sourceActivitySuccessUnionQuery = `
	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		'summary_ok' AS event_kind,
		s.summary_status AS status,
		'' AS message,
		s.summarized_at AS event_at
	FROM sources s
	WHERE s.summary_status = 'ok' AND s.summarized_at != ''

	UNION ALL

	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		CASE WHEN s.extract_status = 'empty' THEN 'extract_empty' ELSE 'extract_ok' END AS event_kind,
		s.extract_status AS status,
		'' AS message,
		s.extracted_at AS event_at
	FROM sources s
	WHERE s.extract_status IN ('ok', 'empty') AND s.extracted_at != ''`

const sourceActivityFailureUnionQuery = `
	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		'summary_error' AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		'summary_error' AS event_kind,
		s.summary_status AS status,
		s.summary_error AS message,
		s.updated_at AS event_at
	FROM sources s
	WHERE s.summary_status = 'error' AND s.updated_at != ''

	UNION ALL

	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		COALESCE(NULLIF(s.extract_failure_kind, ''), 'extract_error') AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		CASE
			WHEN s.extract_status = 'dead' THEN 'extract_dead'
			WHEN s.extract_status = 'gone' THEN 'extract_gone'
			ELSE 'extract_error'
		END AS event_kind,
		s.extract_status AS status,
		s.extract_error AS message,
		COALESCE(NULLIF(s.extract_last_failed_at, ''), s.updated_at) AS event_at
	FROM sources s
	WHERE s.extract_status IN ('error', 'dead', 'gone')`

const sourceActivityTrendUnionQuery = `
	SELECT
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.summary_status AS status,
		'' AS message,
		s.summarized_at AS event_at,
		'success' AS event_class
	FROM sources s
	WHERE s.summary_status = 'ok' AND s.summarized_at != ''

	UNION ALL

	SELECT
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.extract_status AS status,
		'' AS message,
		s.extracted_at AS event_at,
		'success' AS event_class
	FROM sources s
	WHERE s.extract_status IN ('ok', 'empty') AND s.extracted_at != ''

	UNION ALL

	SELECT
		s.source_type,
		s.domain,
		'summary_error' AS failure_kind,
		s.summary_status AS status,
		s.summary_error AS message,
		s.updated_at AS event_at,
		'failure' AS event_class
	FROM sources s
	WHERE s.summary_status = 'error' AND s.updated_at != ''

	UNION ALL

	SELECT
		s.source_type,
		s.domain,
		COALESCE(NULLIF(s.extract_failure_kind, ''), 'extract_error') AS failure_kind,
		s.extract_status AS status,
		s.extract_error AS message,
		COALESCE(NULLIF(s.extract_last_failed_at, ''), s.updated_at) AS event_at,
		'failure' AS event_class
	FROM sources s
	WHERE s.extract_status IN ('error', 'dead', 'gone')`

func sourceSummaryBacklogWhere(promptVersion string, toolName string, toolVersion string) (string, []any) {
	staleWhere, args := sourceSummaryStaleWhere(promptVersion, toolName, toolVersion)
	return `extract_status IN ('ok', 'empty') AND ` + staleWhere, args
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
