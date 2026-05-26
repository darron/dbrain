package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

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

func scanCountBuckets(rows rowScanner, grouped bool) ([]CountBucket, error) {
	buckets := make([]CountBucket, 0)
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
