package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
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

func TestMigrationRepairsPartialRetrievalSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close fresh store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, retrievalMigrationVersion); err != nil {
		t.Fatalf("remove retrieval migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatalf("set pre-retrieval user_version: %v", err)
	}
	for _, table := range []string{"retrieval_embeddings", "retrieval_index_generations", "retrieval_chunks"} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if _, err := db.Exec(`
		CREATE TABLE retrieval_chunks (
			chunk_id TEXT PRIMARY KEY,
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			evidence_role TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			start_char INTEGER NOT NULL,
			end_char INTEGER NOT NULL,
			heading TEXT NOT NULL,
			chunker_version TEXT NOT NULL,
			input_content_hash TEXT NOT NULL,
			chunk_text_hash TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create partial retrieval_chunks: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO retrieval_chunks (
			chunk_id, parent_kind, parent_source_key, evidence_role, ordinal,
			start_char, end_char, heading, chunker_version, input_content_hash,
			chunk_text_hash, text, created_at, updated_at
		) VALUES ('legacy-chunk', 'item', 'legacy-item', 'raw', 0, 0, 6, '',
			'retrieval-chunker-v1', 'input', 'text-hash', 'legacy', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert partial retrieval chunk: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		st = openStoreAtPath(t, path)
		if err := st.Close(); err != nil {
			t.Fatalf("close repaired store attempt %d: %v", attempt+1, err)
		}
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen repaired sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	var sectionOrdinal int
	if err := db.QueryRow(`SELECT section_ordinal FROM retrieval_chunks WHERE chunk_id = 'legacy-chunk'`).Scan(&sectionOrdinal); err != nil {
		t.Fatalf("read repaired legacy chunk: %v", err)
	}
	if sectionOrdinal != 0 {
		t.Fatalf("repaired section_ordinal = %d, want 0", sectionOrdinal)
	}
	for _, table := range []string{"retrieval_embeddings", "retrieval_index_generations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("check repaired table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("repaired table %s count = %d, want 1", table, count)
		}
	}
	for _, index := range []string{
		"idx_retrieval_chunks_parent_ordinal_unique",
		"idx_retrieval_embeddings_chunk_profile_unique",
		"idx_retrieval_generations_one_active_profile",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatalf("check repaired index %s: %v", index, err)
		}
		if count != 1 {
			t.Fatalf("repaired index %s count = %d, want 1", index, count)
		}
	}
	for _, trigger := range []string{
		"trg_retrieval_embeddings_profile_invariants_insert",
		"trg_retrieval_embeddings_profile_invariants_update",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&count); err != nil {
			t.Fatalf("check repaired trigger %s: %v", trigger, err)
		}
		if count != 1 {
			t.Fatalf("repaired trigger %s count = %d, want 1", trigger, count)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO retrieval_chunks (
			chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, chunker_version,
			input_content_hash, chunk_text_hash, text, created_at, updated_at
		) VALUES ('duplicate-ordinal', 'item', 'legacy-item', 'raw', 0, 0, 0, 3, '',
			'retrieval-chunker-v1', 'input-2', 'hash-2', 'two', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err == nil {
		t.Fatal("repaired parent ordinal uniqueness accepted a duplicate")
	}
	if _, err := db.Exec(`
		INSERT INTO retrieval_embeddings (
			chunk_id, profile_id, provider, model, dimensions, representation,
			normalization, vector_bytes, chunk_text_hash, status, updated_at
		) VALUES ('legacy-chunk', 'profile-a', 'fake', 'fake-v1', 2, 'dense_f32',
			'l2', X'0000000000000000', 'text-hash', 'ready', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert embedding into repaired schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO retrieval_chunks (
			chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, chunker_version,
			input_content_hash, chunk_text_hash, text, created_at, updated_at
		) VALUES ('profile-conflict', 'item', 'other-item', 'raw', 0, 0, 0, 5, '',
			'retrieval-chunker-v1', 'input-3', 'hash-3', 'three', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert profile conflict chunk: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO retrieval_embeddings (
			chunk_id, profile_id, provider, model, dimensions, representation,
			normalization, vector_bytes, chunk_text_hash, status, updated_at
		) VALUES ('profile-conflict', 'profile-a', 'fake', 'different-model', 2, 'dense_f32',
			'l2', X'0000000000000000', 'hash-3', 'ready', '2026-07-18T00:00:00Z')`); err == nil {
		t.Fatal("repaired profile trigger allowed mixed model provenance")
	}
	if _, err := db.Exec(`DELETE FROM retrieval_chunks WHERE chunk_id = 'legacy-chunk'`); err != nil {
		t.Fatalf("delete repaired legacy chunk: %v", err)
	}
	var embeddings int
	if err := db.QueryRow(`SELECT COUNT(*) FROM retrieval_embeddings WHERE chunk_id = 'legacy-chunk'`).Scan(&embeddings); err != nil {
		t.Fatalf("count cascaded repaired embeddings: %v", err)
	}
	if embeddings != 0 {
		t.Fatalf("repaired cascade left %d embeddings", embeddings)
	}
	if _, err := db.Exec(`
		INSERT INTO retrieval_index_generations (
			generation_id, profile_id, backend, backend_version, dimensions,
			distance_metric, build_status, active, created_at, updated_at
		) VALUES ('invalid-active', 'profile-a', 'hnsw', '1', 2, 'cosine',
			'building', 1, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err == nil {
		t.Fatal("repaired generation constraints allowed an active incomplete generation")
	}
}

func TestMigrationRepairsProfileInvariantTriggersAfterRetrievalMigration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close fresh store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	for _, trigger := range []string{
		"trg_retrieval_embeddings_profile_invariants_insert",
		"trg_retrieval_embeddings_profile_invariants_update",
	} {
		if _, err := db.Exec(`DROP TRIGGER ` + trigger); err != nil {
			t.Fatalf("drop profile invariant trigger %s: %v", trigger, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version > ?`, retrievalMigrationVersion); err != nil {
		t.Fatalf("remove post-retrieval migration metadata: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, retrievalMigrationVersion)); err != nil {
		t.Fatalf("stamp retrieval schema version: %v", err)
	}
	var migrationName string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = ?`, retrievalMigrationVersion).Scan(&migrationName); err != nil {
		t.Fatalf("confirm retrieval migration metadata: %v", err)
	}
	if migrationName != "retrieval_hybrid_storage_v1" {
		t.Fatalf("retrieval migration name = %q, want retrieval_hybrid_storage_v1", migrationName)
	}
	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read schema user_version: %v", err)
	}
	if userVersion != retrievalMigrationVersion {
		t.Fatalf("schema user_version = %d, want stamped retrieval version %d", userVersion, retrievalMigrationVersion)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		st = openStoreAtPath(t, path)
		if err := st.Close(); err != nil {
			t.Fatalf("close repaired store attempt %d: %v", attempt+1, err)
		}
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen repaired sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, trigger := range []string{
		"trg_retrieval_embeddings_profile_invariants_insert",
		"trg_retrieval_embeddings_profile_invariants_update",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&count); err != nil {
			t.Fatalf("check repaired trigger %s: %v", trigger, err)
		}
		if count != 1 {
			t.Fatalf("repaired trigger %s count = %d, want 1", trigger, count)
		}
	}
	var repairMigrationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ? AND name = ?`, retrievalTriggerRepairVersion, retrievalTriggerRepairName).Scan(&repairMigrationCount); err != nil {
		t.Fatalf("check retrieval trigger repair migration metadata: %v", err)
	}
	if repairMigrationCount != 1 {
		t.Fatalf("retrieval trigger repair migration count = %d, want 1", repairMigrationCount)
	}
}

func TestMigrationRepairsAuditProvenanceStateIdempotently(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	result, err := st.UpsertItem(t.Context(), model.Item{
		SourceKey:              "x:audit-provenance-migration",
		SourceType:             "x_bookmark",
		ExternalID:             "audit-provenance-migration",
		CanonicalURL:           "https://x.com/example/status/audit-provenance-migration",
		Title:                  "Audit provenance migration",
		ArticleTitle:           model.XMediaTranscriptArticleTitle,
		ArticleText:            "durable transcript",
		ContentHash:            "audit-provenance-migration-hash",
		NotePath:               "items/x/audit-provenance-migration.md",
		RawJSON:                `{}`,
		UpdatedAt:              now,
		LastSeenAt:             now,
		XMediaTranscriptStatus: model.XMediaTranscriptStatusOK,
		XMediaTranscriptAt:     now,
	})
	if err != nil {
		t.Fatalf("insert migration fixture: %v", err)
	}
	if _, err := st.db.Exec(`
		UPDATE items
		SET x_media_transcript_status = ?, x_media_transcript_at = ?
		WHERE id = ?`, model.XMediaTranscriptStatusOK, now.Format(time.RFC3339), result.ItemID); err != nil {
		t.Fatalf("seed legacy transcript compatibility columns: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE name = 'audit_provenance_v1'`); err != nil {
		t.Fatalf("remove audit provenance migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = ` + strings.TrimSpace(fmt.Sprint(currentSchemaVersion-1))); err != nil {
		t.Fatalf("set pre-audit-provenance user_version: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_item_enrichments_role_status`); err != nil {
		t.Fatalf("drop enrichment role/status index: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM item_enrichments WHERE item_id = ?`, result.ItemID); err != nil {
		t.Fatalf("remove pre-fix enrichment row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		st = openStoreAtPath(t, path)
		if err := st.Close(); err != nil {
			t.Fatalf("close repaired store attempt %d: %v", attempt+1, err)
		}
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen sqlite directly: %v", err)
	}
	defer func() { _ = db.Close() }()

	var migrationCount int
	var appliedAt string
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(MAX(applied_at), '')
		FROM schema_migrations
		WHERE name = 'audit_provenance_v1'`).Scan(&migrationCount, &appliedAt); err != nil {
		t.Fatalf("load audit provenance migration: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("audit_provenance_v1 migration count = %d, want 1", migrationCount)
	}
	applied, err := time.Parse(time.RFC3339, appliedAt)
	if err != nil {
		t.Fatalf("audit_provenance_v1 applied_at %q is not RFC3339: %v", appliedAt, err)
	}
	if applied.Location() != time.UTC || !strings.HasSuffix(appliedAt, "Z") {
		t.Fatalf("audit_provenance_v1 applied_at = %q, want UTC RFC3339", appliedAt)
	}

	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_item_enrichments_role_status'`).Scan(&indexCount); err != nil {
		t.Fatalf("check enrichment index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("enrichment role/status index count = %d, want 1", indexCount)
	}
	var status, text string
	if err := db.QueryRow(`SELECT status, text FROM item_enrichments WHERE item_id = ? AND role = ?`, result.ItemID, model.ItemEnrichmentRoleXMediaTranscript).Scan(&status, &text); err != nil {
		t.Fatalf("load repaired transcript enrichment: %v", err)
	}
	if status != model.XMediaTranscriptStatusOK || text != "durable transcript" {
		t.Fatalf("repaired transcript enrichment = status %q text %q", status, text)
	}
}

func TestOpenWithOptionsReportsAppliedSchemaMigrations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	var events []MigrationEvent
	st, err := OpenWithOptions(path, OpenOptions{
		MigrationReporter: func(event MigrationEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("open store with migration reporter: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if len(events) != len(schemaMigrations)*2 {
		t.Fatalf("expected start/applied events for %d migrations, got %d: %#v", len(schemaMigrations), len(events), events)
	}
	for i, migration := range schemaMigrations {
		started := events[i*2]
		if started.Phase != MigrationStarted || started.Version != migration.Version || started.LatestVersion != currentSchemaVersion || started.Name != migration.Name || started.Err != nil {
			t.Fatalf("unexpected started event for migration %d: %#v", migration.Version, started)
		}
		applied := events[i*2+1]
		if applied.Phase != MigrationApplied || applied.Version != migration.Version || applied.LatestVersion != currentSchemaVersion || applied.Name != migration.Name || applied.Err != nil {
			t.Fatalf("unexpected applied event for migration %d: %#v", migration.Version, applied)
		}
	}

	events = nil
	st, err = OpenWithOptions(path, OpenOptions{
		MigrationReporter: func(event MigrationEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("reopen current store with migration reporter: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()
	if len(events) != 0 {
		t.Fatalf("expected no migration events on current-schema reopen, got %#v", events)
	}
}

func TestOpenSchemaMigrationRestoresFTSAvailabilityOnReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if !st.HasFTS() {
		t.Fatal("expected fresh store to have FTS enabled")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := st.db.Exec(`
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, text,
			content_hash, raw_json, imported_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"gh-star:test:litestream",
		"github_star",
		"litestream",
		"https://github.com/benbjohnson/litestream",
		"benbjohnson/litestream",
		"Litestream provides streaming replication for SQLite databases.",
		"hash-litestream",
		`{}`,
		now,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert litestream item: %v", err)
	}
	itemID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO items_fts (
			rowid, source_key, title, text, article_title, article_text,
			author_handle, author_name, primary_category, primary_domain
		) VALUES (?, ?, ?, ?, '', '', '', '', '', '')`,
		itemID,
		"gh-star:test:litestream",
		"benbjohnson/litestream",
		"Litestream provides streaming replication for SQLite databases.",
	); err != nil {
		t.Fatalf("insert litestream fts row: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()
	if !st.HasFTS() {
		t.Fatal("expected reopened current-schema store to refresh FTS availability")
	}
	results, err := st.Search(t.Context(), "litestream sqlite replication", 5)
	if err != nil {
		t.Fatalf("search reopened store: %v", err)
	}
	if len(results) == 0 || results[0].SourceKey != "gh-star:test:litestream" {
		t.Fatalf("expected multi-term FTS result after reopen, got %#v", results)
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
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 2); err != nil {
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

func TestMigrationBackfillsXArticleCanonicalURLs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.Exec(`
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:x-article-pretty-url",
		"https://x.com/cyrilXBT/article/2052202263263744010",
		"https://x.com/i/article/2052202263263744010",
		"x_article",
		"x.com",
		"sources/x_article/x-com-test.md",
		now,
		now,
	); err != nil {
		t.Fatalf("insert x article source: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 4); err != nil {
		t.Fatalf("simulate pre-v4 migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	var canonicalURL string
	if err := st.db.QueryRow(`SELECT canonical_url FROM sources WHERE source_key = ?`, "src:x-article-pretty-url").Scan(&canonicalURL); err != nil {
		t.Fatalf("load x article canonical url: %v", err)
	}
	if canonicalURL != "https://x.com/i/article/2052202263263744010" {
		t.Fatalf("expected canonical url to use i/article URL, got %q", canonicalURL)
	}
	assertCurrentSchemaMigration(t, st.db)
}

func TestOpenRepairsLegacyMediaSchemaBeforeCreatingRetryIndex(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE media_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			remote_url TEXT NOT NULL UNIQUE,
			download_status TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);`); err != nil {
		t.Fatalf("seed legacy media schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st := openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	for _, column := range []string{"download_error_count", "last_download_attempt_at"} {
		var found int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('media_assets') WHERE name = ?`, column).Scan(&found); err != nil {
			t.Fatalf("check media column %s: %v", column, err)
		}
		if found != 1 {
			t.Fatalf("expected media_assets.%s to be repaired, found=%d", column, found)
		}
	}
	var indexName string
	if err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_media_assets_download_retry'`).Scan(&indexName); err != nil {
		t.Fatalf("expected retry index after schema repair: %v", err)
	}
	assertCurrentSchemaMigration(t, st.db)
}

func TestOpenRepairsAuthUserSchemaWhenVersionSixWasUsedByOlderMigration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS auth_users`); err != nil {
		t.Fatalf("drop auth_users: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET name = ? WHERE version = 6`, "source_summary_failure_timestamp"); err != nil {
		t.Fatalf("simulate older version 6 migration name: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 8); err != nil {
		t.Fatalf("remove auth repair migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	var tableName string
	if err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'auth_users'`).Scan(&tableName); err != nil {
		t.Fatalf("expected auth_users repair migration to create table: %v", err)
	}
	if _, _, err := st.ApproveGitHubAuthUser(t.Context(), "darron"); err != nil {
		t.Fatalf("ApproveGitHubAuthUser after repair migration: %v", err)
	}
	assertCurrentSchemaMigration(t, st.db)
}

func TestMigrationRepairsBlockedParseErrorFeeds(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	ctx := t.Context()
	result, err := st.UpsertFeed(ctx, FeedUpsert{
		FeedKey:             "feed:parse-error",
		URL:                 "https://example.com/feed.xml",
		NormalizedURL:       "https://example.com/feed.xml",
		PollIntervalSeconds: 3600,
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if _, err := st.db.Exec(`
		UPDATE feeds
		SET health_status = ?,
			failure_kind = 'parse_error',
			first_failed_at = ?,
			last_failed_at = ?,
			last_http_status = 200,
			last_error = 'XML syntax error on line 2161: unexpected EOF in CDATA section',
			error_count = 1,
			next_fetch_after = '',
			updated_at = ?
		WHERE id = ?`,
		FeedHealthBlocked, now, now, now, result.FeedID,
	); err != nil {
		t.Fatalf("seed blocked parse-error feed: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 10); err != nil {
		t.Fatalf("remove feed repair migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 9`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	feed, err := st.GetFeed(ctx, "feed:parse-error")
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if feed.HealthStatus != FeedHealthError {
		t.Fatalf("expected parse-error feed to be retryable, got health_status=%q", feed.HealthStatus)
	}
	if feed.FailureKind != "parse_error" || feed.ErrorCount != 1 || feed.LastError == "" {
		t.Fatalf("expected parse failure diagnostics to be preserved, got %+v", feed)
	}
	if !feed.NextFetchAfter.IsZero() {
		t.Fatalf("expected repaired parse-error feed to be due immediately, next_fetch_after=%s", feed.NextFetchAfter)
	}
	due, err := st.ListFeedsDue(ctx, time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC), 10, false)
	if err != nil {
		t.Fatalf("ListFeedsDue: %v", err)
	}
	if len(due) != 1 || due[0].FeedKey != "feed:parse-error" {
		t.Fatalf("expected repaired parse-error feed in normal due queue, got %+v", due)
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
