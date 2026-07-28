package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

type Store struct {
	db *sql.DB
	// Semantic refresh heartbeats use a lazy one-connection pool so a blocked
	// heartbeat cannot consume the main pool's only connection.
	progressPath string
	progressOnce sync.Once
	progressDB   *sql.DB
	progressErr  error
	read         sqlQueryer
	hasFTS       bool
	auditBegin   func(context.Context, *sql.Conn) error
	// Test-only observation seam for expensive authoritative projection checks.
	retrievalProjectionFullValidation   func()
	retrievalProjectionPlanHashObserved func(int)
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
	if path != ":memory:" {
		st.progressPath = path
	}

	return st, nil
}

func openProgressDB(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite progress db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, stmt := range []string{
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 60000;",
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply progress pragma %q: %w", stmt, err)
		}
	}
	return db, nil
}

func (s *Store) semanticProgressDB() (*sql.DB, error) {
	if s.progressPath == "" {
		return s.db, nil
	}
	s.progressOnce.Do(func() {
		s.progressDB, s.progressErr = openProgressDB(s.progressPath)
	})
	return s.progressDB, s.progressErr
}

// OpenReadOnly opens an existing store for read-only consumers such as MCP.
// It intentionally skips schema creation/migrations so startup cannot block
// behind a long-running writer before the MCP initialize response is sent.
func OpenReadOnly(path string) (*Store, error) {
	return OpenReadOnlyContext(context.Background(), path)
}

// OpenReadOnlyContext opens an existing query-only store and bounds all
// bootstrap probes with the caller's context.
func OpenReadOnlyContext(ctx context.Context, path string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	st := &Store{db: db}
	if err := st.initReadOnly(ctx, path); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func readOnlyDSN(path string) string {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	// Descriptor-backed immutable candidates are complete private snapshots.
	// immutable avoids SQLite trying to discover journal siblings beside the
	// descriptor pseudo-path. Ordinary active database paths must not use it,
	// because their WAL may contain authoritative state.
	if strings.HasPrefix(path, "/dev/fd/") || strings.HasPrefix(path, "/proc/self/fd/") {
		query.Set("immutable", "1")
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var progressErr, dbErr error
	if s.progressDB != nil {
		progressErr = s.progressDB.Close()
	}
	if s.db != nil {
		dbErr = s.db.Close()
	}
	return errors.Join(progressErr, dbErr)
}

func (s *Store) HasFTS() bool {
	if s == nil {
		return false
	}
	return s.hasFTS
}
