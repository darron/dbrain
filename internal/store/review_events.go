package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const reviewCursorPrefix = "cursor_"

const (
	ReviewEventKindItemImported      = "item_imported"
	ReviewEventKindItemUpdated       = "item_updated"
	ReviewEventKindSourceCreated     = "source_created"
	ReviewEventKindSourceExtracted   = "source_extracted"
	ReviewEventKindSourceSummarized  = "source_summarized"
	ReviewEventKindItemSummarized    = "item_summarized"
	ReviewEventKindXMediaTranscribed = "x_media_transcribed"
	ReviewEventKindXMediaSummarized  = "x_media_summarized"
	ReviewEventKindXPhotoOCRed       = "x_photo_ocred"
	ReviewEventKindBlocked           = "blocked"
	ReviewEventKindFailed            = "failed"
)

type ReviewEventFilter struct {
	Cursor ReviewCursor
	Limit  int
	Types  []string
	Now    time.Time
}

type ReviewEventFeed struct {
	Cursor             string            `json:"cursor"`
	NextCursor         string            `json:"next_cursor"`
	HighWatermark      time.Time         `json:"high_watermark"`
	HighWatermarkLocal string            `json:"high_watermark_local"`
	HighWatermarkAge   string            `json:"high_watermark_age"`
	Events             []ReviewEvent     `json:"events"`
	Truncated          bool              `json:"truncated"`
	Counts             ReviewEventCounts `json:"counts"`
}

type ReviewEventCounts struct {
	ByKind       []CountBucket `json:"by_kind"`
	BySourceType []CountBucket `json:"by_source_type"`
	ByStatus     []CountBucket `json:"by_status"`
}

type ReviewEvent struct {
	EventID       string    `json:"event_id"`
	EventKind     string    `json:"event_kind"`
	EventAt       time.Time `json:"event_at"`
	EventAtLocal  string    `json:"event_at_local"`
	EventAge      string    `json:"event_age"`
	EntityKind    string    `json:"entity_kind"`
	EntityID      int64     `json:"entity_id"`
	EntityKey     string    `json:"entity_key"`
	EventStage    string    `json:"event_stage"`
	SourceType    string    `json:"source_type"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	NotePath      string    `json:"note_path"`
	Summary       string    `json:"summary"`
	Tags          []string  `json:"tags"`
	Status        string    `json:"status"`
	Actionability string    `json:"actionability"`
	Importance    int       `json:"importance"`
	Reason        string    `json:"reason"`
	Message       string    `json:"message,omitempty"`
}

type ReviewCursor struct {
	EventAt    time.Time `json:"event_at"`
	EventKind  string    `json:"event_kind"`
	EntityKind string    `json:"entity_kind"`
	EntityID   int64     `json:"entity_id"`
	EventStage string    `json:"event_stage"`
}

func NewReviewCursorSince(value time.Time) ReviewCursor {
	return ReviewCursor{EventAt: value.UTC().Truncate(time.Second)}
}

func ParseReviewCursorToken(raw string) (ReviewCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ReviewCursor{}, nil
	}
	if !strings.HasPrefix(raw, reviewCursorPrefix) {
		return ReviewCursor{}, fmt.Errorf("review cursor must start with %q", reviewCursorPrefix)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, reviewCursorPrefix))
	if err != nil {
		return ReviewCursor{}, fmt.Errorf("decode review cursor: %w", err)
	}
	var wire struct {
		EventAt    string `json:"event_at"`
		EventKind  string `json:"event_kind"`
		EntityKind string `json:"entity_kind"`
		EntityID   int64  `json:"entity_id"`
		EventStage string `json:"event_stage"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return ReviewCursor{}, fmt.Errorf("parse review cursor: %w", err)
	}
	eventAt, err := time.Parse(time.RFC3339, strings.TrimSpace(wire.EventAt))
	if err != nil {
		return ReviewCursor{}, fmt.Errorf("parse review cursor event_at: %w", err)
	}
	return ReviewCursor{
		EventAt:    eventAt.UTC(),
		EventKind:  strings.TrimSpace(wire.EventKind),
		EntityKind: strings.TrimSpace(wire.EntityKind),
		EntityID:   wire.EntityID,
		EventStage: strings.TrimSpace(wire.EventStage),
	}, nil
}

func ParseReviewCursorInput(now time.Time, since string, cursorToken string) (ReviewCursor, error) {
	if strings.TrimSpace(cursorToken) != "" {
		return ParseReviewCursorToken(cursorToken)
	}
	since = strings.TrimSpace(since)
	if since == "" {
		return ReviewCursor{}, fmt.Errorf("since or cursor is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if duration, ok, err := parseReviewDuration(since); ok || err != nil {
		if err != nil {
			return ReviewCursor{}, err
		}
		return NewReviewCursorSince(now.Add(-duration)), nil
	}
	parsed, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return ReviewCursor{}, fmt.Errorf("parse since as RFC3339 timestamp or duration: %w", err)
	}
	return NewReviewCursorSince(parsed), nil
}

func (c ReviewCursor) Token() (string, error) {
	if c.EventAt.IsZero() {
		return "", nil
	}
	wire := struct {
		EventAt    string `json:"event_at"`
		EventKind  string `json:"event_kind"`
		EntityKind string `json:"entity_kind"`
		EntityID   int64  `json:"entity_id"`
		EventStage string `json:"event_stage"`
	}{
		EventAt:    c.EventAt.UTC().Format(time.RFC3339),
		EventKind:  strings.TrimSpace(c.EventKind),
		EntityKind: strings.TrimSpace(c.EntityKind),
		EntityID:   c.EntityID,
		EventStage: strings.TrimSpace(c.EventStage),
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	return reviewCursorPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (e ReviewEvent) Cursor() ReviewCursor {
	return ReviewCursor{
		EventAt:    e.EventAt.UTC(),
		EventKind:  e.EventKind,
		EntityKind: e.EntityKind,
		EntityID:   e.EntityID,
		EventStage: e.EventStage,
	}
}

func (s *Store) ListReviewEvents(ctx context.Context, filter ReviewEventFilter) (ReviewEventFeed, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	now := filter.Now
	if now.IsZero() {
		now = time.Now()
	}

	cursorToken, err := filter.Cursor.Token()
	if err != nil {
		return ReviewEventFeed{}, fmt.Errorf("encode requested cursor: %w", err)
	}
	eventKinds, err := reviewEventKindsForTypes(filter.Types)
	if err != nil {
		return ReviewEventFeed{}, err
	}
	if len(eventKinds) == 0 {
		return emptyReviewEventFeed(filter.Cursor, cursorToken, now), nil
	}

	query := `SELECT event_kind, event_at, entity_kind, entity_id, entity_key, event_stage, source_type, title, url, note_path, summary, user_tags, status, actionability, importance, reason, message
FROM (` + reviewEventsUnionQuery + `) e
WHERE e.event_at != ''
	AND (
		e.event_at > ?
		OR (
			e.event_at = ?
			AND (
				e.event_kind > ?
				OR (
					e.event_kind = ?
					AND (
						e.entity_kind > ?
						OR (
							e.entity_kind = ?
							AND (
								e.entity_id > ?
								OR (
									e.entity_id = ?
									AND e.event_stage > ?
								)
							)
						)
					)
				)
		)
		)
	)`
	args := []any{
		formatTimeForDB(filter.Cursor.EventAt),
		formatTimeForDB(filter.Cursor.EventAt),
		filter.Cursor.EventKind,
		filter.Cursor.EventKind,
		filter.Cursor.EntityKind,
		filter.Cursor.EntityKind,
		filter.Cursor.EntityID,
		filter.Cursor.EntityID,
		filter.Cursor.EventStage,
	}
	if len(eventKinds) > 0 {
		query += ` AND e.event_kind IN (` + placeholders(len(eventKinds)) + `)`
		for _, kind := range eventKinds {
			args = append(args, kind)
		}
	}
	query += `
ORDER BY e.event_at ASC, e.event_kind ASC, e.entity_kind ASC, e.entity_id ASC, e.event_stage ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ReviewEventFeed{}, fmt.Errorf("list review events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	events := make([]ReviewEvent, 0)
	for rows.Next() {
		event, err := scanReviewEvent(rows, now)
		if err != nil {
			return ReviewEventFeed{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return ReviewEventFeed{}, fmt.Errorf("iterate review events: %w", err)
	}

	truncated := len(events) > limit
	if truncated {
		events = events[:limit]
	}
	nextCursor := filter.Cursor
	highWatermark := filter.Cursor.EventAt
	if len(events) > 0 {
		nextCursor = events[len(events)-1].Cursor()
		highWatermark = events[len(events)-1].EventAt
	}
	nextToken, err := nextCursor.Token()
	if err != nil {
		return ReviewEventFeed{}, fmt.Errorf("encode next cursor: %w", err)
	}

	return ReviewEventFeed{
		Cursor:             cursorToken,
		NextCursor:         nextToken,
		HighWatermark:      highWatermark,
		HighWatermarkLocal: reviewLocalTimeString(highWatermark),
		HighWatermarkAge:   reviewRelativeTimeString(highWatermark, now),
		Events:             events,
		Truncated:          truncated,
		Counts:             countReviewEvents(events),
	}, nil
}

func emptyReviewEventFeed(cursor ReviewCursor, cursorToken string, now time.Time) ReviewEventFeed {
	return ReviewEventFeed{
		Cursor:             cursorToken,
		NextCursor:         cursorToken,
		HighWatermark:      cursor.EventAt,
		HighWatermarkLocal: reviewLocalTimeString(cursor.EventAt),
		HighWatermarkAge:   reviewRelativeTimeString(cursor.EventAt, now),
		Events:             []ReviewEvent{},
		Truncated:          false,
		Counts:             ReviewEventCounts{},
	}
}

func scanReviewEvent(rows *sql.Rows, now time.Time) (ReviewEvent, error) {
	var event ReviewEvent
	var eventAt string
	var tags string
	if err := rows.Scan(
		&event.EventKind,
		&eventAt,
		&event.EntityKind,
		&event.EntityID,
		&event.EntityKey,
		&event.EventStage,
		&event.SourceType,
		&event.Title,
		&event.URL,
		&event.NotePath,
		&event.Summary,
		&tags,
		&event.Status,
		&event.Actionability,
		&event.Importance,
		&event.Reason,
		&event.Message,
	); err != nil {
		return ReviewEvent{}, fmt.Errorf("scan review event: %w", err)
	}
	event.EventAt = parseStoredTime(eventAt)
	event.EventAtLocal = reviewLocalTimeString(event.EventAt)
	event.EventAge = reviewRelativeTimeString(event.EventAt, now)
	event.Tags = splitReviewTags(tags)
	event.EventID = event.EntityKind + ":" + event.EntityKey + ":" + event.EventStage
	return event, nil
}

func countReviewEvents(events []ReviewEvent) ReviewEventCounts {
	byKind := map[string]int{}
	bySourceType := map[string]int{}
	byStatus := map[string]int{}
	for _, event := range events {
		byKind[event.EventKind]++
		if event.SourceType != "" {
			bySourceType[event.SourceType]++
		}
		if event.Status != "" {
			byStatus[event.Status]++
		}
	}
	return ReviewEventCounts{
		ByKind:       reviewBuckets(byKind),
		BySourceType: reviewBuckets(bySourceType),
		ByStatus:     reviewBuckets(byStatus),
	}
}

func reviewBuckets(values map[string]int) []CountBucket {
	if len(values) == 0 {
		return nil
	}
	buckets := make([]CountBucket, 0, len(values))
	for key, count := range values {
		buckets = append(buckets, CountBucket{Key: key, Count: count})
	}
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0 && buckets[j-1].Key > buckets[j].Key; j-- {
			buckets[j-1], buckets[j] = buckets[j], buckets[j-1]
		}
	}
	return buckets
}

func reviewEventKindsForTypes(types []string) ([]string, error) {
	all := []string{
		ReviewEventKindBlocked,
		ReviewEventKindFailed,
		ReviewEventKindItemImported,
		ReviewEventKindItemSummarized,
		ReviewEventKindItemUpdated,
		ReviewEventKindSourceCreated,
		ReviewEventKindSourceExtracted,
		ReviewEventKindSourceSummarized,
		ReviewEventKindXMediaSummarized,
		ReviewEventKindXMediaTranscribed,
		ReviewEventKindXPhotoOCRed,
	}
	groups := map[string][]string{
		"all":            nil,
		"imports":        {ReviewEventKindItemImported, ReviewEventKindItemUpdated, ReviewEventKindSourceCreated},
		"enrichments":    {ReviewEventKindSourceExtracted, ReviewEventKindSourceSummarized, ReviewEventKindItemSummarized, ReviewEventKindXMediaTranscribed, ReviewEventKindXMediaSummarized, ReviewEventKindXPhotoOCRed},
		"failures":       {ReviewEventKindBlocked, ReviewEventKindFailed},
		"categorization": {},
	}
	if len(types) == 0 {
		return all, nil
	}
	set := map[string]struct{}{}
	sawType := false
	for _, raw := range types {
		for _, part := range strings.Split(raw, ",") {
			typ := strings.TrimSpace(strings.ToLower(part))
			if typ == "" {
				continue
			}
			sawType = true
			kinds, ok := groups[typ]
			if !ok {
				return nil, fmt.Errorf("unknown review event type %q", typ)
			}
			if typ == "all" {
				return all, nil
			}
			for _, kind := range kinds {
				set[kind] = struct{}{}
			}
		}
	}
	if !sawType {
		return all, nil
	}
	kinds := make([]string, 0, len(set))
	for _, kind := range all {
		if _, ok := set[kind]; ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds, nil
}

func parseReviewDuration(raw string) (time.Duration, bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, false, nil
	}
	if strings.HasSuffix(raw, "d") {
		value, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64)
		if err != nil {
			return 0, true, fmt.Errorf("parse day duration %q: %w", raw, err)
		}
		if value < 0 {
			return 0, true, fmt.Errorf("duration must be positive")
		}
		return time.Duration(value * float64(24*time.Hour)), true, nil
	}
	if !strings.ContainsAny(raw, "hms") {
		return 0, false, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, true, fmt.Errorf("parse duration %q: %w", raw, err)
	}
	if duration < 0 {
		return 0, true, fmt.Errorf("duration must be positive")
	}
	return duration, true, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

func splitReviewTags(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t'
	})
	tags := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		tag := strings.TrimSpace(field)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

func reviewLocalTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func reviewRelativeTimeString(value time.Time, now time.Time) string {
	if value.IsZero() || now.IsZero() {
		return ""
	}
	delta := value.Sub(now)
	suffix := "from now"
	if delta < 0 {
		suffix = "ago"
		delta = -delta
	}
	if delta < time.Minute {
		return "less than a minute " + suffix
	}

	unitValue := int((delta + 30*time.Second) / time.Minute)
	unit := "minute"
	switch {
	case unitValue < 60:
	case delta < 36*time.Hour:
		unitValue = int((delta + 30*time.Minute) / time.Hour)
		unit = "hour"
	default:
		unitValue = int((delta + 12*time.Hour) / (24 * time.Hour))
		unit = "day"
	}
	if unitValue != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s %s", unitValue, unit, suffix)
}

const reviewEventsUnionQuery = `
	SELECT
		'` + ReviewEventKindItemImported + `' AS event_kind,
		i.imported_at AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'imported' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		i.summary_text AS summary,
		i.user_tags,
		'ok' AS status,
		'review' AS actionability,
		CASE WHEN i.summary_text != '' OR i.user_tags != '' THEN 70 ELSE 50 END AS importance,
		'new item imported' AS reason,
		'' AS message
	FROM items i
	WHERE i.imported_at != ''

	UNION ALL

	SELECT
		'` + ReviewEventKindItemUpdated + `' AS event_kind,
		e.last_changed_at AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'updated' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		i.summary_text AS summary,
		i.user_tags,
		'ok' AS status,
		'review' AS actionability,
		75 AS importance,
		'feed entry content changed' AS reason,
		'' AS message
	FROM feed_entries e
	JOIN items i ON i.id = e.item_id
	WHERE e.last_changed_at != '' AND e.version > 1

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceCreated + `' AS event_kind,
		s.created_at AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'created' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		s.summary_text AS summary,
		s.user_tags,
		'ok' AS status,
		'review' AS actionability,
		55 AS importance,
		'new linked source created' AS reason,
		'' AS message
	FROM sources s
	WHERE s.created_at != ''

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceExtracted + `' AS event_kind,
		s.extracted_at AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'extracted' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		'' AS summary,
		s.user_tags,
		s.extract_status AS status,
		'background' AS actionability,
		45 AS importance,
		'raw source extract became available' AS reason,
		'' AS message
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusOK + `', '` + model.SourceExtractStatusEmpty + `') AND s.extracted_at != ''

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceSummarized + `' AS event_kind,
		s.summarized_at AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'summarized' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		s.summary_text AS summary,
		s.user_tags,
		s.summary_status AS status,
		'review' AS actionability,
		CASE WHEN s.user_tags != '' THEN 85 ELSE 75 END AS importance,
		'source summary became available' AS reason,
		'' AS message
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusOK + `' AND s.summarized_at != ''

	UNION ALL

	SELECT
		CASE
			WHEN i.source_type IN ('x_bookmark', 'x_quote') AND tx.status = '` + model.XMediaTranscriptStatusOK + `' THEN '` + ReviewEventKindXMediaSummarized + `'
			ELSE '` + ReviewEventKindItemSummarized + `'
		END AS event_kind,
		COALESCE(NULLIF(e.completed_at, ''), e.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		CASE
			WHEN i.source_type IN ('x_bookmark', 'x_quote') AND tx.status = '` + model.XMediaTranscriptStatusOK + `' THEN 'x_media_summarized'
			ELSE 'summarized'
		END AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		e.text AS summary,
		i.user_tags,
		e.status AS status,
		'review' AS actionability,
		CASE
			WHEN i.source_type IN ('x_bookmark', 'x_quote') AND tx.status = '` + model.XMediaTranscriptStatusOK + `' THEN 80
			ELSE 70
		END AS importance,
		CASE
			WHEN i.source_type IN ('x_bookmark', 'x_quote') AND tx.status = '` + model.XMediaTranscriptStatusOK + `' THEN 'X media transcript summary became available'
			ELSE 'item summary became available'
		END AS reason,
		'' AS message
	FROM item_enrichments e
	JOIN items i ON i.id = e.item_id
	LEFT JOIN item_enrichments tx ON tx.item_id = i.id AND tx.role = '` + model.ItemEnrichmentRoleXMediaTranscript + `'
	WHERE e.role = '` + model.ItemEnrichmentRoleSummary + `' AND e.status = '` + model.ItemSummaryStatusOK + `' AND COALESCE(NULLIF(e.completed_at, ''), e.updated_at) != ''

	UNION ALL

	SELECT
		'` + ReviewEventKindXMediaTranscribed + `' AS event_kind,
		COALESCE(NULLIF(e.completed_at, ''), e.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'x_media_transcribed' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		'' AS summary,
		i.user_tags,
		e.status AS status,
		'review' AS actionability,
		80 AS importance,
		'raw X media transcript became available' AS reason,
		'' AS message
	FROM item_enrichments e
	JOIN items i ON i.id = e.item_id
	WHERE e.role = '` + model.ItemEnrichmentRoleXMediaTranscript + `' AND e.status = '` + model.XMediaTranscriptStatusOK + `' AND COALESCE(NULLIF(e.completed_at, ''), e.updated_at) != ''

	UNION ALL

	SELECT
		'` + ReviewEventKindXPhotoOCRed + `' AS event_kind,
		COALESCE(NULLIF(e.completed_at, ''), e.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'x_photo_ocred' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		'' AS summary,
		i.user_tags,
		e.status AS status,
		'review' AS actionability,
		75 AS importance,
		'raw photo OCR text became available' AS reason,
		'' AS message
	FROM item_enrichments e
	JOIN items i ON i.id = e.item_id
	WHERE e.role = '` + model.ItemEnrichmentRoleOCR + `' AND e.status = '` + model.ItemOCRStatusOK + `' AND COALESCE(NULLIF(e.completed_at, ''), e.updated_at) != ''

	UNION ALL

	SELECT
		CASE WHEN s.summary_status = '` + model.SourceSummaryStatusError + `' THEN '` + ReviewEventKindFailed + `' ELSE '` + ReviewEventKindBlocked + `' END AS event_kind,
		s.updated_at AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'source_summary' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		s.summary_text AS summary,
		s.user_tags,
		s.summary_status AS status,
		CASE WHEN s.summary_status = '` + model.SourceSummaryStatusError + `' THEN 'failure' ELSE 'blocked' END AS actionability,
		95 AS importance,
		'source summary requires attention' AS reason,
		s.summary_error AS message
	FROM sources s
	WHERE s.summary_status IN ('` + model.SourceSummaryStatusError + `', '` + model.SourceSummaryStatusBlocked + `', '` + model.SourceSummaryStatusSkipped + `') AND s.updated_at != ''

	UNION ALL

	SELECT
		CASE WHEN s.extract_status = '` + model.SourceExtractStatusError + `' THEN '` + ReviewEventKindFailed + `' ELSE '` + ReviewEventKindBlocked + `' END AS event_kind,
		COALESCE(NULLIF(s.extract_last_failed_at, ''), s.updated_at) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'source_extract' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		'' AS summary,
		s.user_tags,
		s.extract_status AS status,
		CASE WHEN s.extract_status = '` + model.SourceExtractStatusError + `' THEN 'failure' ELSE 'blocked' END AS actionability,
		95 AS importance,
		'source extraction requires attention' AS reason,
		COALESCE(NULLIF(s.extract_error, ''), s.extract_failure_kind) AS message
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusError + `', '` + model.SourceExtractStatusDead + `', '` + model.SourceExtractStatusGone + `') AND COALESCE(NULLIF(s.extract_last_failed_at, ''), s.updated_at) != ''

	UNION ALL

	SELECT
		CASE WHEN e.status = '` + model.ItemSummaryStatusError + `' THEN '` + ReviewEventKindFailed + `' ELSE '` + ReviewEventKindBlocked + `' END AS event_kind,
		COALESCE(NULLIF(e.completed_at, ''), e.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'item_summary' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		'' AS summary,
		i.user_tags,
		e.status AS status,
		CASE WHEN e.status = '` + model.ItemSummaryStatusError + `' THEN 'failure' ELSE 'blocked' END AS actionability,
		90 AS importance,
		'item summary requires attention' AS reason,
		e.error AS message
	FROM item_enrichments e
	JOIN items i ON i.id = e.item_id
	WHERE e.role = '` + model.ItemEnrichmentRoleSummary + `' AND e.status IN ('` + model.ItemSummaryStatusError + `', '` + model.ItemSummaryStatusBlocked + `', '` + model.ItemSummaryStatusSkipped + `') AND COALESCE(NULLIF(e.completed_at, ''), e.updated_at) != ''

	UNION ALL

	SELECT
		CASE WHEN e.status = '` + model.ItemOCRStatusError + `' THEN '` + ReviewEventKindFailed + `' ELSE '` + ReviewEventKindBlocked + `' END AS event_kind,
		COALESCE(NULLIF(e.completed_at, ''), e.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'item_ocr' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		'' AS summary,
		i.user_tags,
		e.status AS status,
		CASE WHEN e.status = '` + model.ItemOCRStatusError + `' THEN 'failure' ELSE 'blocked' END AS actionability,
		90 AS importance,
		'item OCR requires attention' AS reason,
		e.error AS message
	FROM item_enrichments e
	JOIN items i ON i.id = e.item_id
	WHERE e.role = '` + model.ItemEnrichmentRoleOCR + `' AND e.status IN ('` + model.ItemOCRStatusError + `', '` + model.ItemOCRStatusBlocked + `', '` + model.ItemOCRStatusSkipped + `') AND COALESCE(NULLIF(e.completed_at, ''), e.updated_at) != ''

	UNION ALL

	SELECT
		CASE WHEN e.status = '` + model.XMediaTranscriptStatusError + `' THEN '` + ReviewEventKindFailed + `' ELSE '` + ReviewEventKindBlocked + `' END AS event_kind,
		COALESCE(NULLIF(e.completed_at, ''), e.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'x_media_transcript' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		'' AS summary,
		i.user_tags,
		e.status AS status,
		CASE WHEN e.status = '` + model.XMediaTranscriptStatusError + `' THEN 'failure' ELSE 'blocked' END AS actionability,
		90 AS importance,
		'X media transcription requires attention' AS reason,
		e.error AS message
	FROM item_enrichments e
	JOIN items i ON i.id = e.item_id
	WHERE e.role = '` + model.ItemEnrichmentRoleXMediaTranscript + `' AND e.status IN ('` + model.XMediaTranscriptStatusError + `', '` + model.XMediaTranscriptStatusEmpty + `', '` + model.XMediaTranscriptStatusNoise + `', '` + model.XMediaTranscriptStatusTooShort + `', '` + model.XMediaTranscriptStatusNoAudio + `') AND COALESCE(NULLIF(e.completed_at, ''), e.updated_at) != ''
`
