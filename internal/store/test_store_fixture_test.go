package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenCurrentTestStoreAtPathClonesIsolatedCurrentDatabases(t *testing.T) {
	t.Parallel()

	first := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "first.db"))
	second := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "second.db"))

	firstID, err := first.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read first database ID: %v", err)
	}
	secondID, err := second.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read second database ID: %v", err)
	}
	if firstID == secondID {
		t.Fatalf("cloned test databases share database ID %q", firstID)
	}

	var firstVersion, secondVersion int
	if err := first.db.QueryRow(`PRAGMA user_version`).Scan(&firstVersion); err != nil {
		t.Fatalf("read first schema version: %v", err)
	}
	if err := second.db.QueryRow(`PRAGMA user_version`).Scan(&secondVersion); err != nil {
		t.Fatalf("read second schema version: %v", err)
	}
	if firstVersion != currentSchemaVersion || secondVersion != currentSchemaVersion {
		t.Fatalf("cloned schema versions = %d, %d; want %d", firstVersion, secondVersion, currentSchemaVersion)
	}

	if _, err := first.db.Exec(`INSERT INTO feeds (feed_key, url, normalized_url, created_at, updated_at) VALUES ('first', 'https://first.example/feed', 'https://first.example/feed', '', '')`); err != nil {
		t.Fatalf("write first database: %v", err)
	}
	var secondFeeds int
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM feeds`).Scan(&secondFeeds); err != nil {
		t.Fatalf("count second database feeds: %v", err)
	}
	if secondFeeds != 0 {
		t.Fatalf("second database feed count = %d, want isolated empty database", secondFeeds)
	}
}

func TestOpenCurrentTestStoreAtPathPreservesExistingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	first := openCurrentTestStoreAtPath(t, path)
	firstID, err := first.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read first database ID: %v", err)
	}
	if _, err := first.db.Exec(`INSERT INTO feeds (feed_key, url, normalized_url, created_at, updated_at) VALUES ('persist', 'https://persist.example/feed', 'https://persist.example/feed', '', '')`); err != nil {
		t.Fatalf("seed existing database: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	reopened := openCurrentTestStoreAtPath(t, path)
	reopenedID, err := reopened.RetrievalDatabaseID(context.Background())
	if err != nil {
		t.Fatalf("read reopened database ID: %v", err)
	}
	if reopenedID != firstID {
		t.Fatalf("reopened database ID = %q, want %q", reopenedID, firstID)
	}
	var feeds int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM feeds WHERE url='https://persist.example/feed'`).Scan(&feeds); err != nil {
		t.Fatalf("count persisted feed: %v", err)
	}
	if feeds != 1 {
		t.Fatalf("persisted feed count = %d, want 1", feeds)
	}
}
