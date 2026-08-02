package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// This fixture stays package-local because importing storefixture from the
// store package tests would create a store -> storefixture -> store cycle.
var currentTestDatabaseTemplate struct {
	once  sync.Once
	bytes []byte
	err   error
}

func openCurrentTestStoreAtPath(t *testing.T, path string) *Store {
	t.Helper()

	cloned, err := materializeCurrentTestDatabase(path)
	if err != nil {
		t.Fatalf("prepare current test store %s: %v", path, err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open current test store %s: %v", path, err)
	}
	if cloned {
		databaseID, err := newRetrievalDatabaseID()
		if err != nil {
			_ = st.Close()
			t.Fatalf("generate current test store database ID: %v", err)
		}
		if _, err := st.db.Exec(`UPDATE retrieval_state SET database_id=? WHERE singleton=1`, databaseID); err != nil {
			_ = st.Close()
			t.Fatalf("rekey current test store: %v", err)
		}
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func openStoreFromEmptyDatabase(t *testing.T, path string) *Store {
	t.Helper()

	if _, err := os.Stat(path); err == nil {
		t.Fatalf("empty-database test path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect empty-database test path %s: %v", path, err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store from empty database %s: %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func materializeCurrentTestDatabase(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect target: %w", err)
	}

	template, err := currentTestDatabaseTemplateBytes()
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

func currentTestDatabaseTemplateBytes() ([]byte, error) {
	currentTestDatabaseTemplate.once.Do(func() {
		dir, err := os.MkdirTemp("", "dbrain-current-test-db-")
		if err != nil {
			currentTestDatabaseTemplate.err = fmt.Errorf("create template directory: %w", err)
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()

		path := filepath.Join(dir, "brain.db")
		st, err := Open(path)
		if err != nil {
			currentTestDatabaseTemplate.err = fmt.Errorf("open template database: %w", err)
			return
		}
		var busy, logFrames, checkpointedFrames int
		if err := st.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(
			&busy,
			&logFrames,
			&checkpointedFrames,
		); err != nil {
			_ = st.Close()
			currentTestDatabaseTemplate.err = fmt.Errorf("checkpoint template database: %w", err)
			return
		}
		if busy != 0 || logFrames != checkpointedFrames {
			_ = st.Close()
			currentTestDatabaseTemplate.err = fmt.Errorf(
				"checkpoint template database incomplete: busy=%d log=%d checkpointed=%d",
				busy,
				logFrames,
				checkpointedFrames,
			)
			return
		}
		if err := st.Close(); err != nil {
			currentTestDatabaseTemplate.err = fmt.Errorf("close template database: %w", err)
			return
		}
		currentTestDatabaseTemplate.bytes, currentTestDatabaseTemplate.err = os.ReadFile(path)
		if currentTestDatabaseTemplate.err != nil {
			currentTestDatabaseTemplate.err = fmt.Errorf("read template database: %w", currentTestDatabaseTemplate.err)
		}
	})
	return currentTestDatabaseTemplate.bytes, currentTestDatabaseTemplate.err
}
