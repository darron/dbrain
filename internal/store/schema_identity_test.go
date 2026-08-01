package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRestorableDatabaseAcceptsCurrentSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "current.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate current dbrain database: %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsMissingRuntimeReadinessTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.db")
	st := openStoreAtPath(t, path)
	trigger := retrievalRuntimeProjectionCounterTriggers[0]
	if _, err := st.db.Exec(`DROP TRIGGER ` + trigger.name); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	err := ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), trigger.name) {
		t.Fatalf("validation after dropping v21 trigger=%v", err)
	}
}

func TestValidateRestorableDatabasePreservesV19EmbeddingProfileIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "v19.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	execSchemaIdentityTestDB(t, path, `
		DELETE FROM schema_migrations WHERE version>19;
		PRAGMA user_version=19`)
	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate genuine v19 embedding profile schema: %v", err)
	}
	trigger := retrievalEmbeddingProfileTriggersV19[0]
	execSchemaIdentityTestDB(t, path, `DROP TRIGGER `+trigger.name)
	err := ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), trigger.name) {
		t.Fatalf("validation after dropping historical v19 trigger=%v", err)
	}
}

func TestValidateRestorableDatabaseAcceptsGenuineV16WithoutProjectionDirtyTriggers(t *testing.T) {
	path := projectionDirtyTriggerV16Database(t)

	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate genuine v16 database without Task-4 triggers: %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsV18MissingProjectionDirtyTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `DROP TRIGGER `+semanticProjectionDirtyTriggers[0].name)

	err := ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), semanticProjectionDirtyTriggers[0].name) {
		t.Fatalf("validation after dropping v18 dirty trigger = %v, want incompatible trigger error", err)
	}
}

func TestValidateRestorableDatabaseRejectsV18NonCanonicalProjectionDirtyTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	trigger := semanticProjectionDirtyTriggers[0]
	execSchemaIdentityTestDB(t, path, `DROP TRIGGER `+trigger.name+`;
		CREATE TRIGGER `+trigger.name+` AFTER INSERT ON `+trigger.table+` BEGIN SELECT 1; END`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) || !strings.Contains(err.Error(), "non-canonical definition") {
		t.Fatalf("validation after replacing v18 dirty trigger = %v, want incompatible non-canonical trigger error", err)
	}
}

func TestValidateRestorableDatabaseAcceptsLegacySchemaWithoutMigrationMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	clearMigrationMetadata(t, path)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy database before validation: %v", err)
	}
	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate legacy dbrain database: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy database after validation: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("read-only validation modified legacy database")
	}
}

func TestValidateRestorableDatabaseAcceptsHistoricalMigrationNameAlias(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy-alias.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `
		UPDATE schema_migrations SET name = 'source_summary_failure_timestamp' WHERE version = 6;
		DELETE FROM schema_migrations WHERE version >= 8;
		PRAGMA user_version = 7;
	`)

	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate historical migration alias: %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsForeignSQLite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "foreign.db")
	execSchemaIdentityTestDB(t, path, `CREATE TABLE test_values (value TEXT NOT NULL)`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if !errors.Is(err, ErrDatabaseIncompatible) {
		t.Fatalf("foreign database classification = %v", err)
	}
	if err == nil {
		t.Fatal("expected foreign SQLite database to be rejected")
	}
	if !strings.Contains(err.Error(), "dbrain core schema") {
		t.Fatalf("expected dbrain schema diagnostic, got %v", err)
	}
}

func TestSchemaIdentityOperationalQueryFailuresAreNotIncompatibility(t *testing.T) {
	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, schemaErr := inspectDbrainCoreSchema(&Store{db: db})
	if schemaErr == nil || errors.Is(schemaErr, ErrDatabaseIncompatible) {
		t.Fatalf("schema operational classification = %v", schemaErr)
	}
	migrationErr := validateAppliedSchemaMigrations(t.Context(), db, 1)
	if migrationErr == nil || errors.Is(migrationErr, ErrDatabaseIncompatible) {
		t.Fatalf("migration operational classification = %v", migrationErr)
	}
}

func TestValidateRestorableDatabaseRejectsFutureSchemaVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "future.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `PRAGMA user_version = 999`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if err == nil {
		t.Fatal("expected future dbrain schema to be rejected")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("expected future schema diagnostic, got %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsUnknownMigrationName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mismatch.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `UPDATE schema_migrations SET name = 'unknown_migration' WHERE version = 6`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if err == nil {
		t.Fatal("expected unknown migration name to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown name") {
		t.Fatalf("expected migration-name diagnostic, got %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsUnknownMigrationVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unknown-version.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future_unknown', '2026-07-12T00:00:00Z')`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if err == nil {
		t.Fatal("expected unknown migration version to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown version 999") {
		t.Fatalf("expected unknown-version diagnostic, got %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsMissingMigrationWithinUserVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing-migration.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `DELETE FROM schema_migrations WHERE version = 5`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if err == nil {
		t.Fatal("expected missing migration row to be rejected")
	}
	if !strings.Contains(err.Error(), "missing version 5") {
		t.Fatalf("expected missing-version diagnostic, got %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsPositiveUserVersionWithoutMigrationTable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing-metadata.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `
		DROP TABLE schema_migrations;
		PRAGMA user_version = 1;
	`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if err == nil {
		t.Fatal("expected positive user_version without migration metadata to be rejected")
	}
	if !strings.Contains(err.Error(), "schema version 1 has no migration metadata") {
		t.Fatalf("expected missing-metadata diagnostic, got %v", err)
	}
}

func TestValidateRestorableDatabaseRejectsMigrationTableWithZeroUserVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "zero-version.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	execSchemaIdentityTestDB(t, path, `PRAGMA user_version = 0`)

	err := ValidateRestorableDatabase(t.Context(), path)
	if err == nil {
		t.Fatal("expected migration metadata with zero user_version to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid schema version 0") {
		t.Fatalf("expected invalid-version diagnostic, got %v", err)
	}
}

func execSchemaIdentityTestDB(t *testing.T, path string, statement string) {
	t.Helper()

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("execute test database statement: %v", err)
	}
}
