package store

import (
	"context"
	"database/sql"
	"fmt"
)

type schemaIdentityTable struct {
	name    string
	columns []string
}

var dbrainCoreSchema = []schemaIdentityTable{
	{
		name: "items",
		columns: []string{
			"source_key",
			"source_type",
			"external_id",
			"canonical_url",
			"content_hash",
			"note_path",
			"raw_json",
			"imported_at",
			"updated_at",
			"last_seen_at",
		},
	},
	{
		name: "sources",
		columns: []string{
			"source_key",
			"canonical_url",
			"normalized_url",
			"source_type",
			"extracted_text",
			"summary_text",
			"content_hash",
			"note_path",
			"created_at",
			"updated_at",
		},
	},
}

var compatibleSchemaMigrationNames = map[int]map[string]struct{}{
	6: {
		"source_summary_failure_timestamp": {},
	},
}

// ValidateRestorableDatabase verifies that path is a compatible dbrain store
// without creating, migrating, or otherwise modifying it.
func ValidateRestorableDatabase(ctx context.Context, path string) error {
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return fmt.Errorf("open candidate database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() {
		_ = db.Close()
	}()

	st := &Store{db: db}
	if err := validateDbrainCoreSchema(st); err != nil {
		return err
	}

	var userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return fmt.Errorf("read schema user_version: %w", err)
	}
	if userVersion > currentSchemaVersion {
		return fmt.Errorf("dbrain schema version %d is newer than supported version %d", userVersion, currentSchemaVersion)
	}

	hasMigrations, err := st.tableExists("schema_migrations")
	if err != nil {
		return fmt.Errorf("check dbrain migration metadata: %w", err)
	}
	if !hasMigrations {
		if userVersion != 0 {
			return fmt.Errorf("dbrain schema version %d has no migration metadata", userVersion)
		}
		return nil
	}
	if userVersion <= 0 {
		return fmt.Errorf("dbrain migration metadata has invalid schema version %d", userVersion)
	}

	return validateAppliedSchemaMigrations(ctx, db, userVersion)
}

func validateDbrainCoreSchema(st *Store) error {
	for _, required := range dbrainCoreSchema {
		exists, err := st.tableExists(required.name)
		if err != nil {
			return fmt.Errorf("inspect dbrain core schema table %s: %w", required.name, err)
		}
		if !exists {
			return fmt.Errorf("dbrain core schema table %s is missing", required.name)
		}
		columns, err := st.tableColumns(required.name)
		if err != nil {
			return fmt.Errorf("inspect dbrain core schema table %s columns: %w", required.name, err)
		}
		for _, column := range required.columns {
			if !columns[column] {
				return fmt.Errorf("dbrain core schema column %s.%s is missing", required.name, column)
			}
		}
	}
	return nil
}

func validateAppliedSchemaMigrations(ctx context.Context, db *sql.DB, userVersion int) error {
	expected := make(map[int]string, len(schemaMigrations))
	for _, migration := range schemaMigrations {
		expected[migration.Version] = migration.Name
	}

	rows, err := db.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read dbrain migration metadata: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	applied := make(map[int]struct{}, userVersion)
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return fmt.Errorf("scan dbrain migration metadata: %w", err)
		}
		currentName, knownVersion := expected[version]
		if !knownVersion {
			return fmt.Errorf("dbrain migration metadata contains unknown version %d", version)
		}
		if version > userVersion {
			return fmt.Errorf("dbrain migration version %d exceeds schema user_version %d", version, userVersion)
		}
		if name != currentName && !compatibleSchemaMigrationName(version, name) {
			return fmt.Errorf("dbrain migration version %d has unknown name %q", version, name)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate dbrain migration metadata: %w", err)
	}

	for version := 1; version <= userVersion; version++ {
		if _, ok := applied[version]; !ok {
			return fmt.Errorf("dbrain migration metadata is missing version %d", version)
		}
	}
	return nil
}

func compatibleSchemaMigrationName(version int, name string) bool {
	names := compatibleSchemaMigrationNames[version]
	_, ok := names[name]
	return ok
}
