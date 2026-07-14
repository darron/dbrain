package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

type Store struct {
	db     *sql.DB
	read   sqlQueryer
	hasFTS bool
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) queryer() sqlQueryer {
	if s.read != nil {
		return s.read
	}
	return s.db
}

// OpenOptions configures writable store startup behavior.
type OpenOptions struct {
	MigrationReporter MigrationReporter
}

// MigrationReporter receives migration lifecycle events during writable startup.
type MigrationReporter func(MigrationEvent)

// MigrationPhase identifies the lifecycle state for a migration event.
type MigrationPhase string

const (
	// MigrationStarted is emitted before a missing migration starts running.
	MigrationStarted MigrationPhase = "started"
	// MigrationApplied is emitted after a missing migration has been applied and recorded.
	MigrationApplied MigrationPhase = "applied"
	// MigrationFailed is emitted when a missing migration or its metadata write fails.
	MigrationFailed MigrationPhase = "failed"
)

// MigrationEvent describes one schema migration lifecycle event.
type MigrationEvent struct {
	Phase         MigrationPhase
	Version       int
	LatestVersion int
	Name          string
	Err           error
}

func Open(path string) (*Store, error) {
	return OpenWithOptions(path, OpenOptions{})
}

func OpenWithOptions(path string, opts OpenOptions) (*Store, error) {
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	st := &Store{db: db}
	if err := st.init(opts); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

// OpenReadOnly opens an existing store for read-only consumers such as MCP.
// It intentionally skips schema creation/migrations so startup cannot block
// behind a long-running writer before the MCP initialize response is sent.
func OpenReadOnly(path string) (*Store, error) {
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	st := &Store{db: db}
	if err := st.initReadOnly(path); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func readOnlyDSN(path string) string {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) HasFTS() bool {
	if s == nil {
		return false
	}
	return s.hasFTS
}
