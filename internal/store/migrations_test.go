package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenRecordsCurrentSchemaMigration(t *testing.T) {
	t.Parallel()

	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	assertCurrentSchemaMigration(t, st.db)
}

func TestOpenSchemaMigrationIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	assertCurrentSchemaMigration(t, st.db)

	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if count != len(schemaMigrations) {
		t.Fatalf("expected %d schema migration rows after reopen, got %d", len(schemaMigrations), count)
	}
}

func TestOpenAdoptsExistingCurrentSchemaWithoutMigrationMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.Exec(`
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title,
			content_hash, raw_json, imported_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"x:test-existing",
		"x_bookmark",
		"test-existing",
		"https://example.com/existing",
		"Existing item",
		"hash-existing",
		`{"id":"test-existing"}`,
		now,
		now,
		now,
	); err != nil {
		t.Fatalf("insert existing item: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	clearMigrationMetadata(t, path)

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	assertCurrentSchemaMigration(t, st.db)

	var title string
	if err := st.db.QueryRow(`SELECT title FROM items WHERE source_key = ?`, "x:test-existing").Scan(&title); err != nil {
		t.Fatalf("load preserved item: %v", err)
	}
	if title != "Existing item" {
		t.Fatalf("expected preserved item title, got %q", title)
	}
}

func TestMigrationBackfillsExistingMediaDownloadErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	now := time.Date(2026, 5, 5, 5, 14, 1, 0, time.UTC).Format(time.RFC3339)
	if _, err := st.db.Exec(`
		INSERT INTO media_assets (
			remote_url, media_type, download_status, download_error, discovered_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		"https://video.twimg.com/ext/error.mp4",
		"video",
		"error",
		"context deadline exceeded",
		now,
		now,
	); err != nil {
		t.Fatalf("insert media asset: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, currentSchemaVersion); err != nil {
		t.Fatalf("simulate pre-v2 migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if _, err := db.Exec(`UPDATE media_assets SET download_error_count = 0, last_download_attempt_at = ''`); err != nil {
		t.Fatalf("clear retry state: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	var count int
	var lastAttempt string
	if err := st.db.QueryRow(`
		SELECT download_error_count, last_download_attempt_at
		FROM media_assets
		WHERE remote_url = ?`,
		"https://video.twimg.com/ext/error.mp4",
	).Scan(&count, &lastAttempt); err != nil {
		t.Fatalf("load retry state: %v", err)
	}
	if count != 1 || lastAttempt != now {
		t.Fatalf("expected retry state to be backfilled from updated_at, got count=%d last=%q", count, lastAttempt)
	}
	assertCurrentSchemaMigration(t, st.db)
}

func TestOpenReadOnlySkipsSchemaMigration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close writable store: %v", err)
	}

	clearMigrationMetadata(t, path)

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open read-only store: %v", err)
	}
	defer func() {
		_ = ro.Close()
	}()

	var found int
	err = ro.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&found)
	if err == nil {
		t.Fatalf("schema_migrations table should not be created during read-only open")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("check schema_migrations table: %v", err)
	}

	var userVersion int
	if err := ro.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != 0 {
		t.Fatalf("read-only open should not set user_version, got %d", userVersion)
	}
}

func openStoreAtPath(t *testing.T, path string) *Store {
	t.Helper()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store %s: %v", path, err)
	}
	return st
}

func clearMigrationMetadata(t *testing.T, path string) {
	t.Helper()

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	if _, err := db.Exec(`DROP TABLE IF EXISTS schema_migrations`); err != nil {
		t.Fatalf("drop schema_migrations: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
}

func assertCurrentSchemaMigration(t *testing.T, db *sql.DB) {
	t.Helper()

	currentMigration := schemaMigrations[len(schemaMigrations)-1]
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ? AND name = ?`,
		currentSchemaVersion,
		currentMigration.Name,
	).Scan(&count); err != nil {
		t.Fatalf("count current schema migration: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected current schema migration row, got %d", count)
	}

	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != currentSchemaVersion {
		t.Fatalf("expected user_version %d, got %d", currentSchemaVersion, userVersion)
	}
}
