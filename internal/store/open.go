package store

import (
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

type Store struct {
	db     *sql.DB
	hasFTS bool
}

func Open(path string) (*Store, error) {
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	st := &Store{db: db}
	if err := st.init(); err != nil {
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
