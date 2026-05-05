package applenotes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func openSnapshotDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("_pragma", "query_only(1)")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open Notes snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func validateSnapshotDB(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("validate Notes snapshot: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(result)) != "ok" {
		return fmt.Errorf("validate Notes snapshot: quick_check returned %q", result)
	}
	return nil
}

func probeTable(ctx context.Context, db *sql.DB, name string) (TableProbe, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ? LIMIT 1`, name).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return TableProbe{}, nil
	}
	if err != nil {
		return TableProbe{}, fmt.Errorf("check Notes table %s: %w", name, err)
	}

	columns, err := tableColumns(ctx, db, name)
	if err != nil {
		return TableProbe{}, err
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+name+`"`).Scan(&rows); err != nil {
		return TableProbe{}, fmt.Errorf("count Notes table %s: %w", name, err)
	}
	return TableProbe{Exists: true, Columns: columns, Rows: rows}, nil
}

func tableColumns(ctx context.Context, db *sql.DB, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+strings.ReplaceAll(name, `"`, `""`)+`")`)
	if err != nil {
		return nil, fmt.Errorf("load Notes table info %s: %w", name, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var columns []string
	for rows.Next() {
		var cid int
		var colName string
		var colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan Notes table info %s: %w", name, err)
		}
		columns = append(columns, colName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Notes table info %s: %w", name, err)
	}
	return columns, nil
}

func estimateEntityCount(ctx context.Context, db *sql.DB, objectTable TableProbe, kind string) int {
	if !objectTable.Exists {
		return 0
	}
	column := firstColumn(objectTable.Columns, kindEntityTitleColumns(kind)...)
	if column == "" {
		if kind == "note" {
			if firstColumn(objectTable.Columns, "ZTITLE1", "ZSNIPPET", "ZNOTEDATA") != "" {
				var count int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ZICCLOUDSYNCINGOBJECT WHERE COALESCE(ZMARKEDFORDELETION, 0) = 0`).Scan(&count); err == nil {
					return count
				}
			}
		}
		return 0
	}
	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM ZICCLOUDSYNCINGOBJECT WHERE %s IS NOT NULL`, quoteIdent(column))
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0
	}
	return count
}

func kindEntityTitleColumns(kind string) []string {
	switch kind {
	case "note":
		return []string{"ZTITLE1", "ZSNIPPET"}
	case "folder":
		return []string{"ZTITLE2"}
	case "account":
		return []string{"ZNAME"}
	default:
		return nil
	}
}

func firstColumn(columns []string, names ...string) string {
	for _, want := range names {
		for _, have := range columns {
			if strings.EqualFold(have, want) {
				return have
			}
		}
	}
	return ""
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
