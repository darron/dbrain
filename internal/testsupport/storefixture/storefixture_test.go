package storefixture

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenCurrentCreatesIndependentCurrentDatabases(t *testing.T) {
	t.Parallel()

	firstPath := filepath.Join(t.TempDir(), "first.db")
	secondPath := filepath.Join(t.TempDir(), "second.db")
	first := OpenCurrent(t, firstPath)
	second := OpenCurrent(t, secondPath)

	firstID, err := first.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read first database ID: %v", err)
	}
	secondID, err := second.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read second database ID: %v", err)
	}
	if firstID == secondID {
		t.Fatalf("fixture databases share database ID %q", firstID)
	}

	for _, path := range []string{firstPath, secondPath} {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open fixture database %s: %v", path, err)
		}
		var version int
		if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			_ = db.Close()
			t.Fatalf("read fixture schema version: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close fixture database: %v", err)
		}
		if version <= 0 {
			t.Fatalf("fixture schema version = %d, want positive current version", version)
		}
	}
}

func TestPrepareCurrentPreservesExistingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	first := OpenCurrent(t, path)
	firstID, err := first.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read first database ID: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	PrepareCurrent(t, path)
	reopened := OpenCurrent(t, path)
	reopenedID, err := reopened.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read reopened database ID: %v", err)
	}
	if reopenedID != firstID {
		t.Fatalf("existing database ID = %q after prepare, want %q", reopenedID, firstID)
	}
}
