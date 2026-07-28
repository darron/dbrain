package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

var expectedSemanticFoundationConstraintTriggerNames = []string{
	"trg_retrieval_state_singleton_insert",
	"trg_retrieval_state_singleton_update",
	"trg_retrieval_chunk_occurrences_chunk_insert",
	"trg_retrieval_chunk_occurrences_chunk_update",
	"trg_retrieval_chunks_delete_occurrences",
	"trg_retrieval_chunks_update_occurrences",
}

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

func TestProjectionStagingPurgeEpochMigrationRepairsExistingStampedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		ALTER TABLE retrieval_projection_staging DROP COLUMN expected_purge_epoch;
		PRAGMA user_version=28`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	assertDatabaseTableColumn(t, st.db, "retrieval_projection_staging", "expected_purge_epoch")

	var migrationCount int
	if err := st.db.QueryRow(`
		SELECT COUNT(*) FROM schema_migrations
		WHERE version=28 AND name='retrieval_projection_staging_expected_purge_epoch'`,
	).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration 28 count=%d want 1", migrationCount)
	}
}

func TestSemanticRefreshRunsMigrationUpgradesV24DatabaseIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE version > 24; PRAGMA user_version=24; DROP INDEX IF EXISTS idx_semantic_refresh_runs_one_resumable; DROP INDEX IF EXISTS idx_semantic_refresh_runs_latest; DROP TABLE IF EXISTS semantic_refresh_runs`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st = openStoreAtPath(t, path)
	ctx := context.Background()
	run, resumed, err := st.StartOrResumeSemanticRefreshRun(ctx, StartSemanticRefreshRunInput{RunID: "migration-run", ProfileID: "profile-a", PurgeEpoch: 1, ProjectionWatermark: 2, Now: semanticRefreshTestNow()})
	if err != nil || resumed || run.RunID != "migration-run" {
		t.Fatalf("run=%+v resumed=%v err=%v", run, resumed, err)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`, semanticRefreshRunsMigrationVersion, semanticRefreshRunsMigrationName).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	for _, name := range []string{"idx_semantic_refresh_runs_one_resumable", "idx_semantic_refresh_runs_latest"} {
		var found string
		if err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&found); err != nil || found != name {
			t.Fatalf("index %s found=%q err=%v", name, found, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, semanticRefreshRunsMigrationVersion).Scan(&count); err != nil || count != 1 {
		t.Fatalf("second migration count=%d err=%v", count, err)
	}
	got, err := st.LatestSemanticRefreshRun(ctx, "profile-a")
	if err != nil || got == nil || got.RunID != run.RunID {
		t.Fatalf("persisted run=%+v err=%v", got, err)
	}
	columns, err := st.tableColumns("semantic_refresh_runs")
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range dbrainSemanticRefreshRunSchemaV25[0].columns {
		if !columns[column] {
			t.Errorf("missing column %s", column)
		}
	}
}

func TestSemanticRefreshRunsArchiveMigrationUpgradesGenuineV26DatabaseIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	run := startSemanticRefreshRunForTest(t, st, "prior-head-run", "profile-a", 3, 41)
	updated, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{
		RunID:             run.RunID,
		ExpectedVersion:   run.Version,
		Stage:             SemanticRefreshFlush,
		State:             SemanticRefreshRunRunning,
		Checkpoint:        "flush:41",
		Counters:          SemanticRefreshCounters{ProjectedParents: 7, EmbeddedChunks: 11, FlushedVectors: 5},
		EmbeddingRevision: 13,
		Now:               semanticRefreshTestNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DROP TABLE semantic_refresh_runs_v25_compatibility_archive;
		DELETE FROM schema_migrations WHERE version > 26;
		PRAGMA user_version = 26;
	`); err != nil {
		t.Fatal(err)
	}
	var stamped, minimum, maximum int
	if err := db.QueryRow(`SELECT COUNT(*), MIN(version), MAX(version) FROM schema_migrations`).Scan(&stamped, &minimum, &maximum); err != nil {
		t.Fatal(err)
	}
	if stamped != 26 || minimum != 1 || maximum != 26 {
		t.Fatalf("prior-head migration ledger count=%d range=%d..%d", stamped, minimum, maximum)
	}
	var archiveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='semantic_refresh_runs_v25_compatibility_archive'`).Scan(&archiveCount); err != nil || archiveCount != 0 {
		t.Fatalf("prior-head archive count=%d err=%v", archiveCount, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var events []MigrationEvent
	st, err = OpenWithOptions(path, OpenOptions{MigrationReporter: func(event MigrationEvent) {
		events = append(events, event)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 ||
		events[0].Phase != MigrationStarted ||
		events[1].Phase != MigrationApplied ||
		events[0].Version != semanticRefreshRunsArchiveMigrationVersion ||
		events[1].Version != semanticRefreshRunsArchiveMigrationVersion ||
		events[0].Name != semanticRefreshRunsArchiveMigrationName ||
		events[1].Name != semanticRefreshRunsArchiveMigrationName ||
		events[2].Phase != MigrationStarted ||
		events[3].Phase != MigrationApplied ||
		events[2].Version != retrievalProjectionStagingEpochVersion ||
		events[3].Version != retrievalProjectionStagingEpochVersion ||
		events[2].Name != retrievalProjectionStagingEpochName ||
		events[3].Name != retrievalProjectionStagingEpochName {
		t.Fatalf("v27-v28 migration events=%+v", events)
	}
	got, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a")
	if err != nil || got == nil {
		t.Fatalf("preserved run=%+v err=%v", got, err)
	}
	if got.RunID != updated.RunID || got.Version != updated.Version || got.Stage != updated.Stage ||
		got.Checkpoint != updated.Checkpoint || got.EmbeddingRevision != updated.EmbeddingRevision ||
		got.Counters != updated.Counters || got.State != updated.State {
		t.Fatalf("preserved run=%+v want=%+v", got, updated)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='semantic_refresh_runs_v25_compatibility_archive'`).Scan(&archiveCount); err != nil || archiveCount != 1 {
		t.Fatalf("created archive count=%d err=%v", archiveCount, err)
	}
	var migrationCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`, semanticRefreshRunsArchiveMigrationVersion, semanticRefreshRunsArchiveMigrationName).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("v27 migration count=%d err=%v", migrationCount, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	events = nil
	st, err = OpenWithOptions(path, OpenOptions{MigrationReporter: func(event MigrationEvent) {
		events = append(events, event)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if len(events) != 0 {
		t.Fatalf("idempotent reopen migration events=%+v", events)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, semanticRefreshRunsArchiveMigrationVersion).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("idempotent v27 migration count=%d err=%v", migrationCount, err)
	}
}

func TestSemanticRefreshRunsArchiveSchemaIdentityRequiresArchiveAtV27(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE semantic_refresh_runs_v25_compatibility_archive`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err = ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), "semantic_refresh_runs_v25_compatibility_archive") {
		t.Fatalf("schema identity after dropping v27 archive=%v", err)
	}
}

func TestMembershipL0ActivationMigrationRepairsCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	ctx := context.Background()
	seedReadyRetrievalEmbeddings(t, st, "flush-profile", 2)
	first, err := st.NextRetrievalFlushWindow(ctx, "flush-profile", 2)
	if err != nil {
		t.Fatal(err)
	}
	initial := testRetrievalSegment("migration-initial", 2)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("migration-generation-initial", 2), Segments: []RetrievalIndexSegmentRow{initial},
		Members: retrievalSegmentMembers(first.Rows, initial.SegmentHash), SnapshotRevision: first.SnapshotRevision,
		ExpectedActiveGenerationID: first.Profile.ActiveGenerationID, ExpectedPurgeEpoch: first.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: first.Profile.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	active, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	replacement := testRetrievalSegment("migration-replacement", 1)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: testCompletedGeneration("migration-generation-replacement", 1), Segments: []RetrievalIndexSegmentRow{replacement},
		Members: retrievalSegmentMembers(first.Rows[:1], replacement.SegmentHash), SnapshotRevision: active.ActiveSnapshotRevision,
		ExpectedActiveGenerationID: active.ActiveGenerationID, ExpectedPurgeEpoch: active.PurgeEpoch,
		ExpectedActiveSnapshotRevision: active.ActiveSnapshotRevision, ActivationMode: RetrievalGenerationRewriteSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embedding_profiles SET l0_ready_count=0,active_tombstone_count=99 WHERE profile_id='flush-profile'; DELETE FROM schema_migrations WHERE version=?`, retrievalMembershipL0ActivationVersion); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	profile, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	if profile.L0ReadyCount != 1 || profile.ActiveTombstoneCount != 0 {
		t.Fatalf("profile = %+v", profile)
	}
	var migrationCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`, retrievalMembershipL0ActivationVersion, retrievalMembershipL0ActivationName).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count = %d", migrationCount)
	}
}

func TestProjectionDirtyTriggerMigrationUpgradesGenuineV16DatabaseOnce(t *testing.T) {
	path := projectionDirtyTriggerV16Database(t)
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open genuine v16 database directly: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, title,
			extracted_text, content_hash, note_path, created_at, updated_at
		) VALUES ('source:v16-upgrade', 'https://example.com/v16-upgrade',
			'https://example.com/v16-upgrade', 'article', 'before', 'body',
			'hash', 'source-v16-upgrade.md', ?, ?)`, now, now); err != nil {
		_ = db.Close()
		t.Fatalf("seed genuine v16 source without dirty triggers: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close genuine v16 database: %v", err)
	}

	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close upgraded store: %v", err)
	}
	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()

	var migrationCount int
	if err := st.db.QueryRow(`
		SELECT COUNT(*)
		FROM schema_migrations
		WHERE version = 17 AND name = 'retrieval_projection_dirty_triggers'`).Scan(&migrationCount); err != nil {
		t.Fatalf("read migration 17 metadata: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration 17 row count = %d, want 1", migrationCount)
	}
	var userVersion int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read upgraded user_version: %v", err)
	}
	if userVersion != currentSchemaVersion {
		t.Fatalf("upgraded user_version = %d, want %d", userVersion, currentSchemaVersion)
	}
	for _, trigger := range semanticProjectionDirtyTriggers {
		var table, definition string
		if err := st.db.QueryRow(`
			SELECT tbl_name, sql
			FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, trigger.name).Scan(&table, &definition); err != nil {
			t.Fatalf("read upgraded dirty trigger %s: %v", trigger.name, err)
		}
		if table != trigger.table || normalizeSQLiteTriggerSQL(definition) != normalizeSQLiteTriggerSQL(trigger.sql) {
			t.Fatalf("dirty trigger %s was not installed canonically", trigger.name)
		}
	}
	before := projectionRevisionForTest(t, st)
	if _, err := st.db.Exec(`UPDATE sources SET title = 'after' WHERE source_key = 'source:v16-upgrade'`); err != nil {
		t.Fatalf("mutate projected source after v16 upgrade: %v", err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("post-upgrade projected mutation revision = %d, want %d", got, before+1)
	}
	assertProjectionPendingAtRevision(t, st, "source", "source:v16-upgrade", before+1)
}

func TestProjectionDirtyTriggerMigrationRepairsNonCanonicalV16Trigger(t *testing.T) {
	path := projectionDirtyTriggerV16Database(t)
	trigger := semanticProjectionDirtyTriggers[0]
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open v16 database directly: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER ` + trigger.name + ` AFTER INSERT ON ` + trigger.table + ` BEGIN SELECT 1; END`); err != nil {
		_ = db.Close()
		t.Fatalf("install non-canonical v16 trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v16 database: %v", err)
	}

	st := openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	var table, definition string
	if err := st.db.QueryRow(`
		SELECT tbl_name, sql
		FROM sqlite_master
		WHERE type = 'trigger' AND name = ?`, trigger.name).Scan(&table, &definition); err != nil {
		t.Fatalf("read repaired dirty trigger %s: %v", trigger.name, err)
	}
	if table != trigger.table || normalizeSQLiteTriggerSQL(definition) != normalizeSQLiteTriggerSQL(trigger.sql) {
		t.Fatalf("dirty trigger %s was not repaired canonically", trigger.name)
	}
}

func projectionDirtyTriggerV16Database(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open database directly: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, trigger := range semanticProjectionDirtyTriggers {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatalf("drop Task-4 projection dirty trigger %s: %v", trigger.name, err)
		}
	}
	if _, err := db.Exec(`
		DELETE FROM schema_migrations WHERE version > 16;
		PRAGMA user_version = 16`); err != nil {
		t.Fatalf("stamp genuine v16 database: %v", err)
	}
	return path
}

func TestSemanticFoundationMigrationCreatesV2TablesAndColumns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()

	for _, table := range []string{
		"retrieval_state",
		"retrieval_parent_projections",
		"retrieval_chunk_occurrences",
		"retrieval_projection_staging",
		"retrieval_embedding_profiles",
	} {
		assertSQLiteObject(t, st.db, "table", table)
	}
	for _, column := range []string{"section_key", "heading_hash", "derived"} {
		assertTableColumn(t, st, "retrieval_chunks", column)
	}
	for _, column := range []string{"revision", "vector_hash"} {
		assertTableColumn(t, st, "retrieval_embeddings", column)
	}
	assertSQLiteIndex(t, st.db, "idx_retrieval_chunks_v3_identity_unique", "retrieval_chunks", []string{
		"parent_kind", "parent_source_key", "section_key", "evidence_role", "derived", "heading_hash", "chunk_text_hash",
	}, "chunker_version = 'retrieval-chunker-v3'")
	assertSQLiteIndex(t, st.db, "idx_retrieval_chunk_occurrences_unique", "retrieval_chunk_occurrences", []string{
		"parent_kind", "parent_source_key", "chunk_id", "section_key", "start_char", "end_char",
	}, "")
	assertSQLiteIndex(t, st.db, "idx_retrieval_projection_staging_work_unique", "retrieval_projection_staging", []string{
		"work_id", "dirty_revision", "section_key", "next_boundary", "chunk_id",
	}, "")

	var databaseID string
	var projectionWorkRevision, purgeEpoch int64
	if err := st.db.QueryRow(`
		SELECT database_id, projection_work_revision, purge_epoch
		FROM retrieval_state
		WHERE singleton = 1`).Scan(&databaseID, &projectionWorkRevision, &purgeEpoch); err != nil {
		t.Fatalf("read retrieval state: %v", err)
	}
	if databaseID == "" {
		t.Fatal("retrieval_state.database_id is empty")
	}
	if projectionWorkRevision != 0 || purgeEpoch != 0 {
		t.Fatalf("fresh retrieval state = work revision %d, purge epoch %d, want zeroes", projectionWorkRevision, purgeEpoch)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}
	st = openStoreAtPath(t, path)
	var reopenedID string
	if err := st.db.QueryRow(`SELECT database_id FROM retrieval_state WHERE singleton = 1`).Scan(&reopenedID); err != nil {
		t.Fatalf("read reopened retrieval state: %v", err)
	}
	if reopenedID != databaseID {
		t.Fatalf("database id changed on reopen: got %q, want %q", reopenedID, databaseID)
	}
}

func TestSemanticFoundationMigrationSeedsEveryEligibleParentPending(t *testing.T) {
	t.Parallel()

	path := semanticFoundationV15Database(t)
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open v15 database: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, content_hash,
			raw_json, imported_at, updated_at, last_seen_at, note_path
		) VALUES
			('item:eligible', 'apple_note', 'item:eligible', '', 'item-hash', '{}', ?, ?, ?, 'items/eligible.md'),
			('item:ineligible', 'apple_note', 'item:ineligible', '', 'item-hash', '{}', ?, ?, ?, '')`, now, now, now, now, now, now); err != nil {
		_ = db.Close()
		t.Fatalf("seed v15 items: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, content_hash,
			note_path, created_at, updated_at
		) VALUES
			('source:eligible', 'https://example.com/eligible', 'https://example.com/eligible', 'article', 'source-hash', 'sources/eligible.md', ?, ?),
			('source:ineligible', 'https://example.com/ineligible', 'https://example.com/ineligible', 'article', 'source-hash', '', ?, ?)`, now, now, now, now); err != nil {
		_ = db.Close()
		t.Fatalf("seed v15 sources: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v15 database: %v", err)
	}

	st := openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	rows, err := st.db.Query(`
		SELECT parent_kind, parent_source_key, status, dirty_revision, projected_revision
		FROM retrieval_parent_projections
		ORDER BY parent_kind, parent_source_key`)
	if err != nil {
		t.Fatalf("list seeded retrieval parents: %v", err)
	}
	defer func() { _ = rows.Close() }()
	type parentState struct {
		kind, key, status string
		dirty, projected  int64
	}
	var got []parentState
	for rows.Next() {
		var row parentState
		if err := rows.Scan(&row.kind, &row.key, &row.status, &row.dirty, &row.projected); err != nil {
			t.Fatalf("scan seeded retrieval parent: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate seeded retrieval parents: %v", err)
	}
	want := []parentState{
		{kind: "item", key: "item:eligible", status: "pending", dirty: 2, projected: 0},
		{kind: "item", key: "item:legacy", status: "pending", dirty: 2, projected: 0},
		{kind: "source", key: "source:eligible", status: "pending", dirty: 2, projected: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("seeded retrieval parents = %#v, want %#v", got, want)
	}
	var workRevision int64
	if err := st.db.QueryRow(`SELECT projection_work_revision FROM retrieval_state WHERE singleton = 1`).Scan(&workRevision); err != nil {
		t.Fatalf("read seeded work revision: %v", err)
	}
	if workRevision != 2 {
		t.Fatalf("seeded work revision = %d, want 2 (foundation seed plus provenance repair)", workRevision)
	}
}

func TestSemanticFoundationMigrationRepairsExistingMetadataIdempotently(t *testing.T) {
	t.Parallel()

	path := semanticFoundationV15Database(t)
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open v15 database: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE retrieval_state (
			singleton INTEGER PRIMARY KEY,
			database_id TEXT NOT NULL
		);
		INSERT INTO retrieval_state (singleton, database_id) VALUES (1, 'stable-database-id');
		CREATE TABLE retrieval_parent_projections (
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			PRIMARY KEY(parent_kind, parent_source_key)
		)`); err != nil {
		_ = db.Close()
		t.Fatalf("seed partial v16 metadata: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close partial v16 metadata: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		st := openStoreAtPath(t, path)
		if err := st.Close(); err != nil {
			t.Fatalf("close repaired store attempt %d: %v", attempt+1, err)
		}
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen repaired database: %v", err)
	}
	defer func() { _ = db.Close() }()
	var databaseID string
	if err := db.QueryRow(`SELECT database_id FROM retrieval_state WHERE singleton = 1`).Scan(&databaseID); err != nil {
		t.Fatalf("read repaired database id: %v", err)
	}
	if databaseID != "stable-database-id" {
		t.Fatalf("repaired database id = %q, want preserved value", databaseID)
	}
	for _, table := range []string{
		"retrieval_chunk_occurrences",
		"retrieval_projection_staging",
		"retrieval_embedding_profiles",
	} {
		assertSQLiteObject(t, db, "table", table)
	}
	for _, column := range []string{"projection_hash", "dirty_revision", "projected_revision", "status"} {
		assertDatabaseTableColumn(t, db, "retrieval_parent_projections", column)
	}
	for _, column := range []string{"projection_work_revision", "purge_epoch", "updated_at"} {
		assertDatabaseTableColumn(t, db, "retrieval_state", column)
	}
	for _, column := range []string{"section_key", "heading_hash", "derived"} {
		assertDatabaseTableColumn(t, db, "retrieval_chunks", column)
	}
	for _, column := range []string{"revision", "vector_hash"} {
		assertDatabaseTableColumn(t, db, "retrieval_embeddings", column)
	}
	var chunkText, vectorBytes []byte
	if err := db.QueryRow(`SELECT text FROM retrieval_chunks WHERE chunk_id = 'legacy-chunk'`).Scan(&chunkText); err != nil {
		t.Fatalf("read preserved legacy chunk: %v", err)
	}
	if string(chunkText) != "legacy text" {
		t.Fatalf("legacy chunk text = %q, want preserved text", chunkText)
	}
	if err := db.QueryRow(`SELECT vector_bytes FROM retrieval_embeddings WHERE chunk_id = 'legacy-chunk' AND profile_id = 'legacy-profile'`).Scan(&vectorBytes); err != nil {
		t.Fatalf("read preserved partial embedding: %v", err)
	}
	if len(vectorBytes) != 0 {
		t.Fatalf("legacy partial embedding bytes = %x, want empty", vectorBytes)
	}
}

func TestRetrievalOccurrenceChunkIndexRepairsExistingCurrentSchemaDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current-schema store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open current-schema database: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_retrieval_chunk_occurrences_chunk`); err != nil {
		_ = db.Close()
		t.Fatalf("remove occurrence chunk index: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, retrievalOccurrenceChunkIndexVersion); err != nil {
		_ = db.Close()
		t.Fatalf("restore pre-index migration metadata: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, retrievalMembershipL0ActivationVersion)); err != nil {
		_ = db.Close()
		t.Fatalf("restore pre-index user version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database without occurrence chunk index: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	var migrationName string
	if err := st.db.QueryRow(`SELECT name FROM schema_migrations WHERE version = ?`, retrievalOccurrenceChunkIndexVersion).Scan(&migrationName); err != nil {
		t.Fatalf("read occurrence chunk index migration: %v", err)
	}
	if migrationName != retrievalOccurrenceChunkIndexName {
		t.Fatalf("occurrence chunk index migration name = %q, want %q", migrationName, retrievalOccurrenceChunkIndexName)
	}
	var userVersion int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read repaired user version: %v", err)
	}
	if userVersion != currentSchemaVersion {
		t.Fatalf("repaired user version = %d, want current schema %d", userVersion, currentSchemaVersion)
	}
	assertSQLiteObject(t, st.db, "index", "idx_retrieval_chunk_occurrences_chunk")
	var queryPlan string
	if err := st.db.QueryRow(`
		EXPLAIN QUERY PLAN
		DELETE FROM retrieval_chunk_occurrences WHERE chunk_id = 'missing'`).Scan(new(int), new(int), new(int), &queryPlan); err != nil {
		t.Fatalf("explain occurrence cleanup: %v", err)
	}
	if !strings.Contains(queryPlan, "idx_retrieval_chunk_occurrences_chunk") {
		t.Fatalf("occurrence cleanup query plan = %q, want chunk index", queryPlan)
	}
}

func TestSemanticFoundationMigrationRepairsEveryPartialFoundationTableAndDatabaseID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		retrievalStateDDL string
		retrievalStateRow string
	}{
		{
			name:              "missing database id",
			retrievalStateDDL: `CREATE TABLE retrieval_state (singleton INTEGER PRIMARY KEY)`,
			retrievalStateRow: `INSERT INTO retrieval_state (singleton) VALUES (1)`,
		},
		{
			name: "empty database id",
			retrievalStateDDL: `CREATE TABLE retrieval_state (
				singleton INTEGER PRIMARY KEY,
				database_id TEXT NOT NULL
			)`,
			retrievalStateRow: `INSERT INTO retrieval_state (singleton, database_id) VALUES (1, '')`,
		},
		{
			name: "whitespace database id",
			retrievalStateDDL: `CREATE TABLE retrieval_state (
				singleton INTEGER PRIMARY KEY,
				database_id TEXT NOT NULL
			)`,
			retrievalStateRow: `INSERT INTO retrieval_state (singleton, database_id) VALUES (1, char(9) || char(10) || ' ')`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := semanticFoundationV15Database(t)
			db, err := sql.Open(driverName, path)
			if err != nil {
				t.Fatalf("open v15 database: %v", err)
			}
			if _, err := db.Exec(tc.retrievalStateDDL + `;
				` + tc.retrievalStateRow + `;
				CREATE TABLE retrieval_parent_projections (
					parent_kind TEXT NOT NULL,
					parent_source_key TEXT NOT NULL,
					PRIMARY KEY(parent_kind, parent_source_key)
				);
				CREATE TABLE retrieval_chunk_occurrences (parent_kind TEXT NOT NULL);
				CREATE TABLE retrieval_projection_staging (work_id TEXT NOT NULL);
				CREATE TABLE retrieval_embedding_profiles (profile_id TEXT PRIMARY KEY)`); err != nil {
				_ = db.Close()
				t.Fatalf("seed partial foundation metadata: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close partial foundation metadata: %v", err)
			}

			st := openStoreAtPath(t, path)
			var databaseID string
			if err := st.db.QueryRow(`SELECT database_id FROM retrieval_state WHERE singleton = 1`).Scan(&databaseID); err != nil {
				_ = st.Close()
				t.Fatalf("read repaired database id: %v", err)
			}
			if strings.TrimSpace(databaseID) == "" {
				_ = st.Close()
				t.Fatal("repaired database id is empty")
			}
			if err := st.Close(); err != nil {
				t.Fatalf("close repaired store: %v", err)
			}

			st = openStoreAtPath(t, path)
			defer func() { _ = st.Close() }()
			var reopenedID string
			if err := st.db.QueryRow(`SELECT database_id FROM retrieval_state WHERE singleton = 1`).Scan(&reopenedID); err != nil {
				t.Fatalf("read reopened database id: %v", err)
			}
			if reopenedID != databaseID {
				t.Fatalf("database id changed on reopen: got %q, want %q", reopenedID, databaseID)
			}
			for table, columns := range map[string][]string{
				"retrieval_state": {
					"singleton", "database_id", "projection_work_revision", "purge_epoch", "updated_at",
				},
				"retrieval_chunk_occurrences": {
					"parent_kind", "parent_source_key", "chunk_id", "section_key", "start_char", "end_char", "created_at", "updated_at",
				},
				"retrieval_projection_staging": {
					"work_id", "dirty_revision", "parent_kind", "parent_source_key", "projection_hash", "section_key", "next_boundary", "chunk_id", "chunk_json", "occurrence_json", "created_at", "updated_at",
				},
				"retrieval_embedding_profiles": {
					"profile_id", "latest_revision", "purge_epoch", "active_generation_id", "active_snapshot_revision", "active_indexed_count", "l0_ready_count", "active_tombstone_count", "updated_at",
					"provider", "model", "dimensions", "projection_version", "chunker_version", "representation", "normalization",
					"provider", "model", "dimensions", "projection_version", "chunker_version", "representation", "normalization",
				},
			} {
				for _, column := range columns {
					assertTableColumn(t, st, table, column)
				}
			}
		})
	}
}

func TestSemanticFoundationMigrationRepairsConstraintEquivalentTriggers(t *testing.T) {
	t.Parallel()

	path := semanticFoundationV15Database(t)
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open v15 database: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE retrieval_state (
			singleton INTEGER,
			database_id TEXT NOT NULL
		);
		INSERT INTO retrieval_state (singleton, database_id) VALUES (1, 'preserved-id');
		CREATE TABLE retrieval_parent_projections (
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			PRIMARY KEY(parent_kind, parent_source_key)
		);
		CREATE TABLE retrieval_chunk_occurrences (
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			chunk_id TEXT NOT NULL,
			section_key TEXT NOT NULL,
			start_char INTEGER NOT NULL,
			end_char INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO retrieval_chunk_occurrences (
			parent_kind, parent_source_key, chunk_id, section_key, start_char, end_char, created_at, updated_at
		) VALUES ('item', 'item:legacy', 'legacy-chunk', 'body', 0, 11, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')`); err != nil {
		_ = db.Close()
		t.Fatalf("seed partial constraint schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close partial constraint schema: %v", err)
	}

	st := openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	for _, trigger := range expectedSemanticFoundationConstraintTriggerNames {
		assertSQLiteObject(t, st.db, "trigger", trigger)
	}
	var databaseID string
	if err := st.db.QueryRow(`SELECT database_id FROM retrieval_state WHERE singleton = 1`).Scan(&databaseID); err != nil {
		t.Fatalf("read preserved state identity: %v", err)
	}
	if databaseID != "preserved-id" {
		t.Fatalf("preserved state identity = %q, want preserved-id", databaseID)
	}
	var preservedOccurrences int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunk_occurrences WHERE chunk_id = 'legacy-chunk'`).Scan(&preservedOccurrences); err != nil {
		t.Fatalf("count preserved occurrence: %v", err)
	}
	if preservedOccurrences != 1 {
		t.Fatalf("preserved occurrence count = %d, want 1", preservedOccurrences)
	}
	if _, err := st.db.Exec(`INSERT INTO retrieval_state (singleton, database_id, updated_at) VALUES (2, 'other', '')`); err == nil {
		t.Fatal("repaired retrieval state accepted singleton 2")
	}
	if _, err := st.db.Exec(`UPDATE retrieval_state SET singleton = 2 WHERE singleton = 1`); err == nil {
		t.Fatal("repaired retrieval state accepted singleton update to 2")
	}
	if _, err := st.db.Exec(`INSERT INTO retrieval_state (singleton, database_id, updated_at) VALUES (1, 'other', '')`); err == nil {
		t.Fatal("repaired retrieval state accepted duplicate singleton 1")
	}
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_chunk_occurrences (
			parent_kind, parent_source_key, chunk_id, section_key, start_char, end_char, created_at, updated_at
		) VALUES ('item', 'item:legacy', 'orphan-chunk', 'body', 12, 16, '', '')`); err == nil {
		t.Fatal("repaired occurrences accepted an orphan chunk")
	}
	if _, err := st.db.Exec(`UPDATE retrieval_chunk_occurrences SET chunk_id = 'orphan-chunk' WHERE chunk_id = 'legacy-chunk'`); err == nil {
		t.Fatal("repaired occurrences accepted an orphan chunk update")
	}
	if _, err := st.db.Exec(`UPDATE retrieval_chunks SET chunk_id = 'renamed-legacy-chunk' WHERE chunk_id = 'legacy-chunk'`); err == nil {
		t.Fatal("repaired chunks accepted a referenced chunk ID update")
	}
	if _, err := st.db.Exec(`INSERT INTO retrieval_chunks
		(chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal, ordinal, start_char, end_char, heading, projection_version, chunker_version, input_content_hash, chunk_text_hash, text, created_at, updated_at)
		VALUES ('unreferenced-chunk', 'item', 'item:unreferenced', 'raw', 0, 0, 0, 1, '', 'v1', 'v1', 'input-hash', 'chunk-hash', 'text', '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')`); err != nil {
		t.Fatalf("insert unreferenced chunk after repair: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_chunks SET chunk_id = 'renamed-unreferenced-chunk' WHERE chunk_id = 'unreferenced-chunk'`); err != nil {
		t.Fatalf("repaired chunks rejected an unreferenced chunk ID update: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM retrieval_chunks WHERE chunk_id = 'legacy-chunk'`); err != nil {
		t.Fatalf("delete legacy chunk: %v", err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunk_occurrences WHERE chunk_id = 'legacy-chunk'`).Scan(&preservedOccurrences); err != nil {
		t.Fatalf("count cascaded occurrences: %v", err)
	}
	if preservedOccurrences != 0 {
		t.Fatalf("deleted chunk left %d occurrences, want zero", preservedOccurrences)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close constraint-repaired store: %v", err)
	}
	st = openStoreAtPath(t, path)
	for _, trigger := range expectedSemanticFoundationConstraintTriggerNames {
		assertSQLiteObject(t, st.db, "trigger", trigger)
	}
}

func TestSemanticFoundationMigrationRejectsInvalidConstraintRows(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, setup, want string
	}{
		{
			name: "invalid singleton",
			setup: `
				CREATE TABLE retrieval_state (singleton INTEGER, database_id TEXT NOT NULL);
				INSERT INTO retrieval_state (singleton, database_id) VALUES (2, 'invalid');
				CREATE TABLE retrieval_parent_projections (parent_kind TEXT, parent_source_key TEXT);
				CREATE TABLE retrieval_chunk_occurrences (parent_kind TEXT);
				CREATE TABLE retrieval_projection_staging (work_id TEXT);
				CREATE TABLE retrieval_embedding_profiles (profile_id TEXT)`,
			want: "invalid retrieval_state singleton",
		},
		{
			name: "duplicate singleton",
			setup: `
				CREATE TABLE retrieval_state (singleton INTEGER, database_id TEXT NOT NULL);
				INSERT INTO retrieval_state (singleton, database_id) VALUES (1, 'first'), (1, 'second');
				CREATE TABLE retrieval_parent_projections (parent_kind TEXT, parent_source_key TEXT);
				CREATE TABLE retrieval_chunk_occurrences (parent_kind TEXT);
				CREATE TABLE retrieval_projection_staging (work_id TEXT);
				CREATE TABLE retrieval_embedding_profiles (profile_id TEXT)`,
			want: "duplicate retrieval_state singleton",
		},
		{
			name: "orphan occurrence",
			setup: `
				CREATE TABLE retrieval_state (singleton INTEGER PRIMARY KEY, database_id TEXT NOT NULL);
				INSERT INTO retrieval_state (singleton, database_id) VALUES (1, 'valid');
				CREATE TABLE retrieval_parent_projections (parent_kind TEXT, parent_source_key TEXT);
				CREATE TABLE retrieval_chunk_occurrences (
					parent_kind TEXT, parent_source_key TEXT, chunk_id TEXT, section_key TEXT,
					start_char INTEGER, end_char INTEGER, created_at TEXT, updated_at TEXT
				);
				INSERT INTO retrieval_chunk_occurrences (parent_kind, parent_source_key, chunk_id, section_key, start_char, end_char, created_at, updated_at)
				VALUES ('item', 'item:legacy', 'missing-chunk', 'body', 0, 1, '', '');
				CREATE TABLE retrieval_projection_staging (work_id TEXT);
				CREATE TABLE retrieval_embedding_profiles (profile_id TEXT)`,
			want: "orphan retrieval_chunk_occurrences",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := semanticFoundationV15Database(t)
			db, err := sql.Open(driverName, path)
			if err != nil {
				t.Fatalf("open v15 database: %v", err)
			}
			if _, err := db.Exec(tc.setup); err != nil {
				_ = db.Close()
				t.Fatalf("seed invalid partial schema: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close invalid partial schema: %v", err)
			}
			_, err = Open(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("open invalid partial schema = %v, want %q", err, tc.want)
			}
			db, err = sql.Open(driverName, path)
			if err != nil {
				t.Fatalf("reopen invalid partial database: %v", err)
			}
			defer func() { _ = db.Close() }()
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, semanticFoundationMigrationVersion).Scan(&count); err != nil {
				t.Fatalf("read migration 16 metadata: %v", err)
			}
			if count != 0 {
				t.Fatalf("invalid schema recorded migration 16 %d times", count)
			}
		})
	}
}

func TestSemanticFoundationSchemaIdentityRejectsMissingConstraintTriggers(t *testing.T) {
	t.Parallel()

	for _, trigger := range expectedSemanticFoundationConstraintTriggerNames {
		t.Run(trigger, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "brain.db")
			st := openStoreAtPath(t, path)
			if err := st.Close(); err != nil {
				t.Fatalf("close current database: %v", err)
			}
			db, err := sql.Open(driverName, path)
			if err != nil {
				t.Fatalf("open current database directly: %v", err)
			}
			if _, err := db.Exec(`DROP TRIGGER ` + trigger); err != nil {
				_ = db.Close()
				t.Fatalf("drop %s: %v", trigger, err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close trigger-reduced database: %v", err)
			}
			err = ValidateRestorableDatabase(t.Context(), path)
			if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), trigger) {
				t.Fatalf("validation after dropping %s = %v, want incompatible trigger error", trigger, err)
			}
		})
	}
}

func TestSemanticFoundationSchemaIdentityRejectsWrongConstraintTriggerBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open current database directly: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER trg_retrieval_chunks_delete_occurrences`); err != nil {
		_ = db.Close()
		t.Fatalf("drop canonical trigger: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER trg_retrieval_chunks_delete_occurrences
		AFTER DELETE ON retrieval_chunks
		BEGIN SELECT 1; END`); err != nil {
		_ = db.Close()
		t.Fatalf("create same-name no-op trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close wrong-trigger database: %v", err)
	}
	err = ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), "non-canonical definition") {
		t.Fatalf("validation after replacing a trigger body = %v, want incompatible non-canonical trigger error", err)
	}
}

func TestSemanticFoundationSchemaIdentityRejectsMissingFoundationColumns(t *testing.T) {
	t.Parallel()

	requiredColumns := map[string][]string{
		"retrieval_chunk_occurrences": {
			"parent_kind", "parent_source_key", "chunk_id", "section_key", "start_char", "end_char", "created_at", "updated_at",
		},
		"retrieval_projection_staging": {
			"work_id", "dirty_revision", "parent_kind", "parent_source_key", "projection_hash", "section_key", "next_boundary", "chunk_id", "chunk_json", "occurrence_json", "created_at", "updated_at",
		},
		"retrieval_embedding_profiles": {
			"profile_id", "latest_revision", "purge_epoch", "active_generation_id", "active_snapshot_revision", "active_indexed_count", "l0_ready_count", "active_tombstone_count", "updated_at",
			"provider", "model", "dimensions", "projection_version", "chunker_version", "representation", "normalization",
			"ready_embedding_count", "pending_embedding_count", "blocked_embedding_count", "error_embedding_count", "corrupt_embedding_count",
		},
	}
	for table, columns := range requiredColumns {
		for _, missingColumn := range columns {
			t.Run(table+"/"+missingColumn, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "brain.db")
				st := openStoreAtPath(t, path)
				if err := st.Close(); err != nil {
					t.Fatalf("close current database: %v", err)
				}
				db, err := sql.Open(driverName, path)
				if err != nil {
					t.Fatalf("open current database directly: %v", err)
				}
				if table == "retrieval_chunk_occurrences" {
					if _, err := db.Exec(`
						DROP TRIGGER trg_retrieval_chunks_delete_occurrences;
						DROP TRIGGER trg_retrieval_chunks_update_occurrences`); err != nil {
						_ = db.Close()
						t.Fatalf("drop parent-side occurrence triggers: %v", err)
					}
				}
				if table == "retrieval_embedding_profiles" {
					for _, trigger := range retrievalEmbeddingProfileTriggersV19 {
						if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
							_ = db.Close()
							t.Fatalf("drop embedding profile trigger %s: %v", trigger.name, err)
						}
					}
					for _, trigger := range retrievalRuntimeReadinessCounterTriggers {
						if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
							_ = db.Close()
							t.Fatalf("drop runtime readiness trigger %s: %v", trigger.name, err)
						}
					}
				}
				columnsAfterDrop := withoutColumn(columns, missingColumn)
				if _, err := db.Exec(`CREATE TABLE rebuilt AS SELECT ` + strings.Join(columnsAfterDrop, ", ") + ` FROM ` + table + `;
					DROP TABLE ` + table + `;
					ALTER TABLE rebuilt RENAME TO ` + table); err != nil {
					_ = db.Close()
					t.Fatalf("remove %s.%s: %v", table, missingColumn, err)
				}
				if err := db.Close(); err != nil {
					t.Fatalf("close reduced database: %v", err)
				}

				err = ValidateRestorableDatabase(t.Context(), path)
				if !errors.Is(err, ErrDatabaseIncompatible) {
					t.Fatalf("validation after removing %s.%s = %v, want incompatibility", table, missingColumn, err)
				}
				if !strings.Contains(err.Error(), table+"."+missingColumn) {
					t.Fatalf("validation error = %q, want missing %s.%s", err, table, missingColumn)
				}
			})
		}
	}
}

func semanticFoundationV15Database(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close initial database: %v", err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open database directly: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, trigger := range semanticProjectionDirtyTriggers {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatalf("drop Task-4 projection dirty trigger %s: %v", trigger.name, err)
		}
	}
	for _, table := range []string{
		"retrieval_chunk_occurrences",
		"retrieval_projection_staging",
		"retrieval_embedding_profiles",
		"retrieval_parent_projections",
		"retrieval_state",
		"retrieval_embeddings",
		"retrieval_chunks",
	} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	for _, index := range []string{
		"idx_retrieval_chunks_v3_identity_unique",
		"idx_retrieval_chunk_occurrences_unique",
		"idx_retrieval_projection_staging_work_unique",
	} {
		if _, err := db.Exec(`DROP INDEX IF EXISTS ` + index); err != nil {
			t.Fatalf("drop %s: %v", index, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= 16; PRAGMA user_version = 15`); err != nil {
		t.Fatalf("stamp database as v15: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE retrieval_chunks (
			chunk_id TEXT PRIMARY KEY,
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			evidence_role TEXT NOT NULL,
			section_ordinal INTEGER NOT NULL DEFAULT 0,
			ordinal INTEGER NOT NULL,
			start_char INTEGER NOT NULL,
			end_char INTEGER NOT NULL,
			heading TEXT NOT NULL DEFAULT '',
			projection_version TEXT NOT NULL DEFAULT '',
			chunker_version TEXT NOT NULL,
			input_content_hash TEXT NOT NULL,
			chunk_text_hash TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(parent_kind, parent_source_key, ordinal)
		);
		CREATE TABLE retrieval_embeddings (
			chunk_id TEXT NOT NULL,
			profile_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			dimensions INTEGER NOT NULL,
			representation TEXT NOT NULL,
			normalization TEXT NOT NULL,
			vector_bytes BLOB NOT NULL,
			chunk_text_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			next_attempt_at TEXT NOT NULL DEFAULT '',
			embedded_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(chunk_id, profile_id)
		);
		INSERT INTO retrieval_chunks (
			chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, projection_version, chunker_version,
			input_content_hash, chunk_text_hash, text, created_at, updated_at
		) VALUES ('legacy-chunk', 'item', 'item:legacy', 'raw', 0, 0, 0, 11, '',
			'retrieval-projection-v1', 'retrieval-chunker-v2', 'legacy-input', 'legacy-hash',
			'legacy text', '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z');
		INSERT INTO retrieval_embeddings (
			chunk_id, profile_id, provider, model, dimensions, representation,
			normalization, vector_bytes, chunk_text_hash, status, updated_at
		) VALUES ('legacy-chunk', 'legacy-profile', 'fake', 'fake-v1', 2, 'dense_f32',
			'l2', X'', 'legacy-hash', 'pending', '2026-07-21T00:00:00Z')`); err != nil {
		t.Fatalf("create v15 retrieval schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded v15 database: %v", err)
	}
	return path
}

func assertSQLiteObject(t *testing.T, db *sql.DB, objectType, name string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, objectType, name).Scan(&count); err != nil {
		t.Fatalf("look up %s %s: %v", objectType, name, err)
	}
	if count != 1 {
		t.Fatalf("%s %s count = %d, want 1", objectType, name, count)
	}
}

func assertSQLiteIndex(t *testing.T, db *sql.DB, index, table string, wantColumns []string, wantPredicate string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		t.Fatalf("list indexes for %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var found bool
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index for %s: %v", table, err)
		}
		if name != index {
			continue
		}
		found = true
		if unique != 1 {
			t.Fatalf("index %s unique = %d, want 1", index, unique)
		}
		if (wantPredicate != "") != (partial == 1) {
			t.Fatalf("index %s partial = %d, predicate %q", index, partial, wantPredicate)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes for %s: %v", table, err)
	}
	if !found {
		t.Fatalf("index %s is missing", index)
	}

	rows, err = db.Query(`PRAGMA index_xinfo(` + index + `)`)
	if err != nil {
		t.Fatalf("list index columns for %s: %v", index, err)
	}
	defer func() { _ = rows.Close() }()
	var gotColumns []string
	for rows.Next() {
		var sequence, columnID, descending, key int
		var name sql.NullString
		var collation string
		if err := rows.Scan(&sequence, &columnID, &name, &descending, &collation, &key); err != nil {
			t.Fatalf("scan index column for %s: %v", index, err)
		}
		if key != 0 {
			gotColumns = append(gotColumns, name.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index columns for %s: %v", index, err)
	}
	if !reflect.DeepEqual(gotColumns, wantColumns) {
		t.Fatalf("index %s columns = %#v, want %#v", index, gotColumns, wantColumns)
	}

	var definition string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&definition); err != nil {
		t.Fatalf("read index %s definition: %v", index, err)
	}
	definition = strings.Join(strings.Fields(definition), " ")
	if wantPredicate != "" && !strings.HasSuffix(definition, "WHERE "+wantPredicate) {
		t.Fatalf("index %s predicate = %q, want WHERE %s", index, definition, wantPredicate)
	}
	if wantPredicate == "" && strings.Contains(definition, " WHERE ") {
		t.Fatalf("index %s unexpectedly has predicate: %q", index, definition)
	}
}

func assertTableColumn(t *testing.T, st *Store, table, column string) {
	t.Helper()
	columns, err := st.tableColumns(table)
	if err != nil {
		t.Fatalf("table columns for %s: %v", table, err)
	}
	if !columns[column] {
		t.Fatalf("table %s is missing column %s", table, column)
	}
}

func assertDatabaseTableColumn(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("table columns for %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column for %s: %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %s: %v", table, err)
	}
	t.Fatalf("table %s is missing column %s", table, column)
}

func withoutColumn(columns []string, without string) []string {
	result := make([]string, 0, len(columns)-1)
	for _, column := range columns {
		if column != without {
			result = append(result, column)
		}
	}
	return result
}

func TestRetrievalChunkProvenanceMigrationRepairsV14SchemaIdempotently(t *testing.T) {
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
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, retrievalChunkProvenanceVersion); err != nil {
		t.Fatalf("remove retrieval chunk provenance migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 14`); err != nil {
		t.Fatalf("set v14 user_version: %v", err)
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
	var projectionVersion string
	if err := db.QueryRow(`SELECT section_ordinal, projection_version FROM retrieval_chunks WHERE chunk_id = 'legacy-chunk'`).Scan(&sectionOrdinal, &projectionVersion); err != nil {
		t.Fatalf("read repaired legacy chunk: %v", err)
	}
	if sectionOrdinal != 0 {
		t.Fatalf("repaired section_ordinal = %d, want 0", sectionOrdinal)
	}
	if projectionVersion != "" {
		t.Fatalf("repaired projection_version = %q, want explicitly stale empty value", projectionVersion)
	}
	var migrationName string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 15`).Scan(&migrationName); err != nil {
		t.Fatalf("read retrieval chunk provenance migration: %v", err)
	}
	if migrationName != "retrieval_chunk_projection_provenance" {
		t.Fatalf("migration 15 name = %q", migrationName)
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

func TestEmbeddingProfileDefinitionMigrationRejectsMixedChunkProvenance(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	ctx := t.Context()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("migration-profile-a", "item", "item:migration-profile", 0, "hash-a", "alpha"),
		testRetrievalChunk("migration-profile-b", "item", "item:migration-profile", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:migration-profile", chunks); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "migration-profile", chunk.TextHash)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, trigger := range retrievalEmbeddingProfileTriggersV19 {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, trigger := range retrievalRuntimeReadinessCounterTriggers {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE retrieval_chunks SET projection_version='mixed-projection' WHERE chunk_id='migration-profile-b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=?`, retrievalEmbeddingProfileVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, semanticProjectionDirtyRepairVersion)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path); err == nil {
		_ = reopened.Close()
		t.Fatal("migration accepted mixed projection provenance under one embedding profile")
	} else if !strings.Contains(err.Error(), "mixed chunk provenance") {
		t.Fatalf("migration error=%v, want mixed chunk provenance", err)
	}
}

func TestEmbeddingRevisionRepairV20UpgradesGenuineV18ReadyRow(t *testing.T) {
	t.Parallel()
	path, profile, profileID := genuineV18DatabaseWithUnprovenReadyEmbedding(t)
	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate genuine v18 database: %v", err)
	}
	st := openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	var migrationCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=20 AND name='retrieval_embedding_revision_provenance_repair'`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration 20 count=%d want 1", migrationCount)
	}
	rows, err := st.db.Query(`SELECT chunk_id,status,vector_bytes,vector_hash,revision FROM retrieval_embeddings WHERE profile_id=? ORDER BY chunk_id`, profileID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	repairedRows := 0
	for rows.Next() {
		var chunkID, status, vectorHash string
		var vectorBytes []byte
		var revision int64
		if err := rows.Scan(&chunkID, &status, &vectorBytes, &vectorHash, &revision); err != nil {
			t.Fatal(err)
		}
		if status != string(RetrievalEmbeddingPending) || len(vectorBytes) != 0 || vectorHash != retrievalVectorHash([]byte{}) {
			t.Fatalf("repaired row %s status=%q vector_len=%d hash=%q revision=%d", chunkID, status, len(vectorBytes), vectorHash, revision)
		}
		repairedRows++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if repairedRows != 2 {
		t.Fatalf("repaired rows=%d want 2", repairedRows)
	}
	assertProfileAggregatesForTest(t, st, profileID, "", 0, 0, 0)
	candidates, err := st.ListChunksNeedingEmbeddingForProfileAt(t.Context(), profile, "", 10, time.Now().UTC())
	if err != nil || len(candidates) != 2 {
		t.Fatalf("re-embedding candidates=%+v err=%v", candidates, err)
	}
	repaired := []RetrievalEmbeddingRow{
		testEmbedding("migration-v20-hash", profileID, "migration-v20-hash-text"),
		testEmbedding("migration-v20-revision", profileID, "migration-v20-revision-text"),
	}
	batchRevision, err := st.PutRetrievalEmbeddingBatch(t.Context(), PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: repaired, ExpectedPurgeEpoch: 0})
	if err != nil || batchRevision <= 0 {
		t.Fatalf("repair batch revision=%d err=%v", batchRevision, err)
	}
	candidates, err = st.ListChunksNeedingEmbeddingForProfileAt(t.Context(), profile, "", 10, time.Now().UTC())
	if err != nil || len(candidates) != 0 {
		t.Fatalf("post-repair candidates=%+v err=%v", candidates, err)
	}
}

func genuineV18DatabaseWithUnprovenReadyEmbedding(t *testing.T) (string, embedding.Profile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	profile := embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("migration-v20-hash", "item", "item:migration-v20", 0, "migration-v20-hash-text", "alpha"),
		testRetrievalChunk("migration-v20-revision", "item", "item:migration-v20", 1, "migration-v20-revision-text", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(t.Context(), "item", "item:migration-v20", chunks); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:migration-v20")
	for _, chunk := range chunks {
		if err := st.PutRetrievalEmbedding(t.Context(), testEmbedding(chunk.ID, profileID, chunk.TextHash)); err != nil {
			t.Fatal(err)
		}
	}
	for _, trigger := range retrievalEmbeddingProfileTriggersV19 {
		if _, err := st.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, trigger := range retrievalRuntimeReadinessCounterTriggers {
		if _, err := st.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ensureRetrievalTables(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET vector_hash='' WHERE chunk_id='migration-v20-hash'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET revision=0 WHERE chunk_id='migration-v20-revision'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`
		CREATE TABLE retrieval_embedding_profiles_v18 (
			profile_id TEXT PRIMARY KEY,
			latest_revision INTEGER NOT NULL DEFAULT 0,
			purge_epoch INTEGER NOT NULL DEFAULT 0,
			active_generation_id TEXT NOT NULL DEFAULT '',
			active_snapshot_revision INTEGER NOT NULL DEFAULT 0,
			active_indexed_count INTEGER NOT NULL DEFAULT 0,
			l0_ready_count INTEGER NOT NULL DEFAULT 0,
			active_tombstone_count INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);
		INSERT INTO retrieval_embedding_profiles_v18
			SELECT profile_id,0,purge_epoch,'',0,0,2,0,updated_at FROM retrieval_embedding_profiles;
		DROP TABLE retrieval_embedding_profiles;
		ALTER TABLE retrieval_embedding_profiles_v18 RENAME TO retrieval_embedding_profiles;
		DELETE FROM schema_migrations WHERE version>18;
		PRAGMA user_version=18`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return path, profile, profileID
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
