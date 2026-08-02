// Package storefixture provides isolated current-schema SQLite databases for
// tests that do not need to exercise dbrain's migration path.
package storefixture

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/darron/dbrain/internal/store"
)

var currentDatabaseTemplate struct {
	once  sync.Once
	bytes []byte
	err   error
}

// PrepareCurrent materializes an isolated current-schema database at path when
// no database exists there. Existing databases are left unchanged.
func PrepareCurrent(t testing.TB, path string) {
	t.Helper()

	cloned, err := materializeCurrent(path)
	if err != nil {
		t.Fatalf("prepare current store fixture %s: %v", path, err)
	}
	if !cloned {
		return
	}
	if err := rekeyDatabase(path); err != nil {
		t.Fatalf("rekey current store fixture %s: %v", path, err)
	}
}

// OpenCurrent prepares and opens an isolated current-schema database.
func OpenCurrent(t testing.TB, path string) *store.Store {
	t.Helper()

	PrepareCurrent(t, path)
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open current store fixture %s: %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func materializeCurrent(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect target: %w", err)
	}

	template, err := currentDatabaseTemplateBytes()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create target directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, fmt.Errorf("create target: %w", err)
	}
	if _, err := file.Write(template); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return false, fmt.Errorf("write target: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("close target: %w", err)
	}
	return true, nil
}

func currentDatabaseTemplateBytes() ([]byte, error) {
	currentDatabaseTemplate.once.Do(func() {
		dir, err := os.MkdirTemp("", "dbrain-store-fixture-")
		if err != nil {
			currentDatabaseTemplate.err = fmt.Errorf("create template directory: %w", err)
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()

		path := filepath.Join(dir, "brain.db")
		st, err := store.Open(path)
		if err != nil {
			currentDatabaseTemplate.err = fmt.Errorf("open template database: %w", err)
			return
		}
		if err := st.Close(); err != nil {
			currentDatabaseTemplate.err = fmt.Errorf("close template database: %w", err)
			return
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			currentDatabaseTemplate.err = fmt.Errorf("reopen template database: %w", err)
			return
		}
		var busy, logFrames, checkpointedFrames int
		if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(
			&busy,
			&logFrames,
			&checkpointedFrames,
		); err != nil {
			_ = db.Close()
			currentDatabaseTemplate.err = fmt.Errorf("checkpoint template database: %w", err)
			return
		}
		if busy != 0 || logFrames != checkpointedFrames {
			_ = db.Close()
			currentDatabaseTemplate.err = fmt.Errorf(
				"checkpoint template database incomplete: busy=%d log=%d checkpointed=%d",
				busy,
				logFrames,
				checkpointedFrames,
			)
			return
		}
		if err := db.Close(); err != nil {
			currentDatabaseTemplate.err = fmt.Errorf("close checkpointed template database: %w", err)
			return
		}
		currentDatabaseTemplate.bytes, currentDatabaseTemplate.err = os.ReadFile(path)
		if currentDatabaseTemplate.err != nil {
			currentDatabaseTemplate.err = fmt.Errorf("read template database: %w", currentDatabaseTemplate.err)
		}
	})
	return currentDatabaseTemplate.bytes, currentDatabaseTemplate.err
}

func rekeyDatabase(path string) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate database ID: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open cloned database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE retrieval_state SET database_id=? WHERE singleton=1`, hex.EncodeToString(random[:])); err != nil {
		return fmt.Errorf("update database ID: %w", err)
	}
	return nil
}
