package safaritabs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
)

func openSnapshotDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("_pragma", "query_only(1)")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open Safari CloudTabs snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func validateSnapshotDB(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"cloud_tab_devices", "cloud_tabs"} {
		var found int
		err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`, table).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("safari CloudTabs snapshot missing required table %s", table)
		}
		if err != nil {
			return fmt.Errorf("check Safari CloudTabs table %s: %w", table, err)
		}
	}
	return nil
}
