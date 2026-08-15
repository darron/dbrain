package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/semanticlock"
	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

// InteractivePoolSize is the shared pool size for live web and MCP consumers.
const InteractivePoolSize = 4

// LinkCaptureAdmissionBusyTimeout is the effective lock-wait budget for the
// deferred link admission connection. The modernc SQLite driver may report a
// context error only after its busy handler has waited, so callers should use
// this value when they need a bounded admission request.
const LinkCaptureAdmissionBusyTimeout = 2 * time.Second

type Store struct {
	db *sql.DB
	// Link-capture admission uses a dedicated one-connection pool with a short
	// SQLite busy timeout. Keeping it separate prevents the interactive intake
	// path from inheriting the 60-second timeout used by ordinary writes.
	linkCaptureAdmissionDB   *sql.DB
	linkCaptureAdmissionPath string
	linkCaptureAdmissionOnce sync.Once
	linkCaptureAdmissionErr  error
	// Semantic refresh heartbeats use a lazy one-connection pool so a blocked
	// heartbeat cannot consume the main pool's only connection.
	progressPath string
	progressOnce sync.Once
	progressDB   *sql.DB
	progressErr  error
	// A semantic stage holds this lease across its read/write work and durable
	// checkpoint so the independent heartbeat writer cannot invalidate a
	// SQLite read snapshot.
	semanticStageOnce sync.Once
	semanticStageGate chan struct{}
	read              sqlQueryer
	hasFTS            bool
	auditBegin        func(context.Context, *sql.Conn) error
	// Authoritative item, source, and projected-enrichment transactions use a
	// database-scoped shared maintenance lease when configured by a production
	// writable constructor.
	authoritativeWriteContextKey *authoritativeWriteContextKey
	authoritativeWriteAcquire    func(context.Context, string) (io.Closer, error)
	authoritativeWriteObserver   AuthoritativeWriteWaitObserver
	semanticLockScope            *semanticlock.Scope
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
	MigrationReporter          MigrationReporter
	SemanticCacheDir           string
	MaxOpenConns               int
	MaxIdleConns               int
	AuthoritativeWriteObserver AuthoritativeWriteWaitObserver
}

// AuthoritativeWriteWaitEvent reports the time spent waiting for the shared
// semantic maintenance lease before an authoritative write transaction.
type AuthoritativeWriteWaitEvent struct {
	Metadata string
	Wait     time.Duration
	Outcome  string
}

type AuthoritativeWriteWaitObserver func(AuthoritativeWriteWaitEvent)

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

func OpenWithSemanticCache(path string, cacheDir string) (*Store, error) {
	return OpenWithSemanticCacheOptions(path, cacheDir, OpenOptions{})
}

func OpenWithSemanticCacheOptions(path string, cacheDir string, opts OpenOptions) (*Store, error) {
	if strings.TrimSpace(cacheDir) == "" {
		return nil, errors.New("semantic cache directory is empty")
	}
	opts.SemanticCacheDir = cacheDir
	return OpenWithOptions(path, opts)
}

func OpenWithOptions(path string, opts OpenOptions) (*Store, error) {
	db, err := sql.Open(driverName, writableDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	// A bare :memory: database is connection-local. Preserve the historical
	// single-connection behavior even when a caller supplies interactive pool
	// sizing intended for a filesystem-backed database.
	if path == ":memory:" {
		opts.MaxOpenConns = 1
		opts.MaxIdleConns = 1
	}
	configurePool(db, opts)

	st := &Store{
		db:                           db,
		authoritativeWriteContextKey: &authoritativeWriteContextKey{},
		authoritativeWriteObserver:   opts.AuthoritativeWriteObserver,
	}
	if err := st.init(opts); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		st.linkCaptureAdmissionPath = path
	}
	if strings.TrimSpace(opts.SemanticCacheDir) != "" {
		databaseID, err := st.RetrievalDatabaseID(context.Background())
		if err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("read semantic database ID for writable store: %w", err)
		}
		scope, err := semanticlock.NewScope(opts.SemanticCacheDir, databaseID)
		if err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("configure authoritative semantic write scope: %w", err)
		}
		st.authoritativeWriteAcquire = func(ctx context.Context, metadata string) (io.Closer, error) {
			return scope.AcquireMaintenanceShared(ctx, metadata)
		}
		st.semanticLockScope = scope
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
	return OpenReadOnlyWithOptions(path, OpenOptions{})
}

// OpenReadOnlyWithOptions opens an existing query-only store with configurable
// connection-pool sizing. It intentionally skips schema migrations.
func OpenReadOnlyWithOptions(path string, opts OpenOptions) (*Store, error) {
	return OpenReadOnlyContextWithOptions(context.Background(), path, opts)
}

// OpenReadOnlyContext opens an existing query-only store and bounds all
// bootstrap probes with the caller's context.
func OpenReadOnlyContext(ctx context.Context, path string) (*Store, error) {
	return OpenReadOnlyContextWithOptions(ctx, path, OpenOptions{})
}

// OpenReadOnlyContextWithOptions opens an existing query-only store and bounds
// all bootstrap probes with the caller's context.
func OpenReadOnlyContextWithOptions(ctx context.Context, path string, opts OpenOptions) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	configurePool(db, opts)

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
	query.Add("_pragma", "busy_timeout(1000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "query_only(1)")
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

func writableDSN(path string) string {
	return writableDSNWithBusyTimeout(path, writableBusyTimeoutMillis)
}

const (
	writableBusyTimeoutMillis             = 60000
	linkCaptureAdmissionBusyTimeoutMillis = int(LinkCaptureAdmissionBusyTimeout / time.Millisecond)
)

func writableDSNWithBusyTimeout(path string, busyTimeoutMillis int) string {
	if path == ":memory:" {
		return path
	}
	values := url.Values{}
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMillis))
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "synchronous(1)")
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + values.Encode()
}

func configurePool(db *sql.DB, opts OpenOptions) {
	maxOpen := opts.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 1
	}
	maxIdle := opts.MaxIdleConns
	if maxIdle <= 0 || maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var admissionErr, progressErr, dbErr error
	if s.linkCaptureAdmissionDB != nil {
		admissionErr = s.linkCaptureAdmissionDB.Close()
	}
	if s.progressDB != nil {
		progressErr = s.progressDB.Close()
	}
	if s.db != nil {
		dbErr = s.db.Close()
	}
	return errors.Join(admissionErr, progressErr, dbErr)
}

func (s *Store) linkCaptureDB() (*sql.DB, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("link capture store is nil")
	}
	if s.linkCaptureAdmissionPath == "" {
		return s.db, nil
	}
	s.linkCaptureAdmissionOnce.Do(func() {
		s.linkCaptureAdmissionDB, s.linkCaptureAdmissionErr = sql.Open(
			driverName,
			writableDSNWithBusyTimeout(s.linkCaptureAdmissionPath, linkCaptureAdmissionBusyTimeoutMillis),
		)
		if s.linkCaptureAdmissionErr == nil {
			configurePool(s.linkCaptureAdmissionDB, OpenOptions{MaxOpenConns: 1, MaxIdleConns: 1})
		}
	})
	if s.linkCaptureAdmissionErr != nil {
		return nil, fmt.Errorf("open link capture admission db: %w", s.linkCaptureAdmissionErr)
	}
	return s.linkCaptureAdmissionDB, nil
}

func (s *Store) HasFTS() bool {
	if s == nil {
		return false
	}
	return s.hasFTS
}
