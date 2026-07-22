package sqlitearchive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	brainstore "github.com/darron/dbrain/internal/store"
)

func snapshotSQLite(ctx context.Context, dbPath string, snapshotPath string) error {
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite database %s: %w", dbPath, err)
	}
	defer func() {
		_ = db.Close()
	}()
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 60000;"); err != nil {
		return fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	stmt := "VACUUM INTO " + sqliteStringLiteral(snapshotPath)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("snapshot sqlite database %s: %w", dbPath, err)
	}
	return nil
}

func validateSQLite(ctx context.Context, dbPath string) error {
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite database %s: %w", dbPath, err)
	}
	defer func() {
		_ = db.Close()
	}()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check;").Scan(&result); err != nil {
		return fmt.Errorf("run quick_check: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(result)) != "ok" {
		return fmt.Errorf("quick_check returned %q", result)
	}
	if err := brainstore.ValidateRestorableDatabase(ctx, dbPath); err != nil {
		return fmt.Errorf("validate dbrain schema identity: %w", err)
	}
	return nil
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
