package store

import (
	"strings"
	"time"
)

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
