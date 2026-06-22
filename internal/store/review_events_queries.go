package store

import (
	"strings"
	"time"
)

func reviewEventsQuery(filter ReviewEventFilter) (string, []any) {
	query := `
		SELECT ` + reviewEventSQLColumns + `
		FROM (` + reviewEventsUnionQuery + `) review_events`
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 16)
	if where, whereArgs := reviewCursorWhere(filter.Cursor); where != "" {
		conditions = append(conditions, where)
		args = append(args, whereArgs...)
	}
	if len(filter.Types) > 0 {
		conditions = append(conditions, "event_kind IN ("+strings.TrimRight(strings.Repeat("?,", len(filter.Types)), ",")+")")
		for _, kind := range filter.Types {
			args = append(args, kind)
		}
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += `
		ORDER BY event_at ASC, event_kind ASC, entity_kind ASC, entity_id ASC, event_stage ASC
		LIMIT ?`
	args = append(args, filter.Limit)
	return query, args
}

func reviewCursorWhere(cursor ReviewCursor) (string, []any) {
	if cursor.EventAt.IsZero() {
		return "", nil
	}
	eventAt := reviewCursorEventAtText(cursor)
	return `(event_at > ?
		OR (
			event_at = ?
			AND (
				event_kind > ?
				OR (
					event_kind = ?
					AND (
						entity_kind > ?
						OR (
							entity_kind = ?
							AND (
								entity_id > ?
								OR (
									entity_id = ?
									AND event_stage > ?
								)
							)
						)
					)
				)
			)
		))`, []any{
			eventAt,
			eventAt,
			cursor.EventKind,
			cursor.EventKind,
			cursor.EntityKind,
			cursor.EntityKind,
			cursor.EntityID,
			cursor.EntityID,
			cursor.EventStage,
		}
}

func reviewCursorEventAtText(cursor ReviewCursor) string {
	if cursor.EventAt.IsZero() {
		return ""
	}
	return cursor.EventAt.UTC().Format(time.RFC3339)
}

func reviewEventCounts(events []ReviewEvent) []CountBucket {
	counts := make([]CountBucket, 0)
	index := map[string]int{}
	for _, event := range events {
		if pos, ok := index[event.EventKind]; ok {
			counts[pos].Count++
			continue
		}
		index[event.EventKind] = len(counts)
		counts = append(counts, CountBucket{Key: event.EventKind, Count: 1})
	}
	return counts
}
