package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDatabaseReadOnlySeparatesIntegrityAndCompatibility(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	got, err := InspectDatabaseReadOnly(t.Context(), path, true)
	if err != nil {
		t.Fatalf("InspectDatabaseReadOnly: %v", err)
	}
	if got.QuickCheck != "ok" || got.QuickViolationCount != 0 || got.ForeignKeyViolationCount != 0 {
		t.Fatalf("unexpected SQLite integrity: %+v", got)
	}
	if got.SchemaCompatibility != "current_compatible" || got.MigrationCompatibility != "current_compatible" {
		t.Fatalf("unexpected compatibility: %+v", got)
	}
	if got.UserVersion != currentSchemaVersion || got.SupportedVersion != currentSchemaVersion || got.AppliedMigrationCount != len(schemaMigrations) {
		t.Fatalf("unexpected migration metadata: %+v", got)
	}
}

func TestInspectDatabaseReadOnlyDoesNotCreateMigrationMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "foreign.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open foreign sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE foreign_data (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create foreign sqlite: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close foreign sqlite: %v", err)
	}

	got, err := InspectDatabaseReadOnly(t.Context(), path, true)
	if err != nil {
		t.Fatalf("InspectDatabaseReadOnly foreign: %v", err)
	}
	if got.QuickCheck != "ok" || got.SchemaCompatibility != "incompatible" || got.MigrationCompatibility != "legacy_compatible" {
		t.Fatalf("unexpected foreign inspection: %+v", got)
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen foreign sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&count); err != nil {
		t.Fatalf("check migration metadata: %v", err)
	}
	if count != 0 {
		t.Fatal("read-only inspection created schema_migrations")
	}
}

func TestInspectDatabaseReadOnlyRejectsCorruptInput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}
	if _, err := InspectDatabaseReadOnly(t.Context(), path, true); err == nil {
		t.Fatal("expected corrupt database inspection to fail")
	}
}

func TestInspectDatabaseReadOnlyClassifiesMigrationCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit string
		want string
	}{
		{name: "legacy", edit: `DROP TABLE schema_migrations; PRAGMA user_version=0`, want: "legacy_compatible"},
		{name: "migration_backed_legacy", edit: `DELETE FROM schema_migrations WHERE version=11; PRAGMA user_version=10`, want: "legacy_compatible"},
		{name: "future", edit: `PRAGMA user_version=999`, want: "incompatible"},
		{name: "missing", edit: `DELETE FROM schema_migrations WHERE version=11`, want: "incompatible"},
		{name: "invalid", edit: `PRAGMA user_version=0`, want: "incompatible"},
		{name: "mismatched_name", edit: `UPDATE schema_migrations SET name='wrong' WHERE version=11`, want: "incompatible"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "brain.db")
			st := openStoreAtPath(t, path)
			if err := st.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}
			execSchemaIdentityTestDB(t, path, tc.edit)
			got, err := InspectDatabaseReadOnly(t.Context(), path, false)
			if err != nil {
				t.Fatalf("InspectDatabaseReadOnly: %v", err)
			}
			wantSchema := "current_compatible"
			if tc.name == "legacy" {
				wantSchema = "legacy_compatible"
			}
			if got.SchemaCompatibility != wantSchema || got.MigrationCompatibility != tc.want {
				t.Fatalf("inspection = %+v, want migration %q", got, tc.want)
			}
		})
	}
}

func TestInspectDatabaseReadOnlyCountsForeignKeyViolations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "foreign-keys.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE parent (id INTEGER PRIMARY KEY);
		CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id));
		INSERT INTO child (id, parent_id) VALUES (1, 999)`); err != nil {
		t.Fatalf("seed foreign key violation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	got, err := InspectDatabaseReadOnly(t.Context(), path, true)
	if err != nil {
		t.Fatalf("InspectDatabaseReadOnly: %v", err)
	}
	if got.ForeignKeyViolationCount != 1 || got.QuickCheck != "ok" {
		t.Fatalf("unexpected integrity result: %+v", got)
	}
}
