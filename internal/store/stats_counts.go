package store

import (
	"context"
	"fmt"
	"strings"
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
