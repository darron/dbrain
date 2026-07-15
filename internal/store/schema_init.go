package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) initReadOnly(ctx context.Context, path string) error {
	pragmas := []string{
		"PRAGMA busy_timeout = 1000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA query_only = ON;",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply read-only pragma %q: %w", stmt, err)
		}
	}

	hasItems, err := s.tableExistsContext(ctx, "items")
	if err != nil {
		return err
	}
	if !hasItems {
		return fmt.Errorf("items table not found in %s", path)
	}
	s.hasFTS, err = s.tableExistsContext(ctx, "items_fts")
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) tableExistsContext(ctx context.Context, name string) (bool, error) {
	var found int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ? LIMIT 1`, name).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check table %s: %w", name, err)
	}
	return true, nil
}

func (s *Store) tableExists(name string) (bool, error) {
	var found int
	if err := s.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ? LIMIT 1`, name).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check table %s: %w", name, err)
	}
	return true, nil
}

func (s *Store) init(opts OpenOptions) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 60000;",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}

	return s.migrate(opts.MigrationReporter)
}
