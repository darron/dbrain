package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrDatabaseIncompatible marks an observed dbrain schema or migration
// identity incompatibility. Ordinary open, descriptor, I/O, and availability
// failures deliberately do not wrap this sentinel.
var ErrDatabaseIncompatible = errors.New("database is incompatible")

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
	result, err := InspectDatabaseReadOnly(ctx, path, false)
	if err != nil {
		return err
	}
	if result.schemaCompatibilityErr != nil {
		return result.schemaCompatibilityErr
	}
	if result.migrationCompatibilityErr != nil {
		return result.migrationCompatibilityErr
	}
	return nil
}

func inspectDbrainCoreSchema(st *Store) (int, int, error) {
	missingTables, missingColumns := 0, 0
	var firstMissing error
	for _, required := range dbrainCoreSchema {
		exists, err := st.tableExists(required.name)
		if err != nil {
			return missingTables, missingColumns, fmt.Errorf("inspect dbrain core schema table %s: %w", required.name, err)
		}
		if !exists {
			missingTables++
			if firstMissing == nil {
				firstMissing = fmt.Errorf("%w: dbrain core schema table %s is missing", ErrDatabaseIncompatible, required.name)
			}
			continue
		}
		columns, err := st.tableColumns(required.name)
		if err != nil {
			return missingTables, missingColumns, fmt.Errorf("inspect dbrain core schema table %s columns: %w", required.name, err)
		}
		for _, column := range required.columns {
			if !columns[column] {
				missingColumns++
				if firstMissing == nil {
					firstMissing = fmt.Errorf("%w: dbrain core schema column %s.%s is missing", ErrDatabaseIncompatible, required.name, column)
				}
			}
		}
	}
	return missingTables, missingColumns, firstMissing
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
			return fmt.Errorf("%w: dbrain migration metadata contains unknown version %d", ErrDatabaseIncompatible, version)
		}
		if version > userVersion {
			return fmt.Errorf("%w: dbrain migration version %d exceeds schema user_version %d", ErrDatabaseIncompatible, version, userVersion)
		}
		if name != currentName && !compatibleSchemaMigrationName(version, name) {
			return fmt.Errorf("%w: dbrain migration version %d has unknown name %q", ErrDatabaseIncompatible, version, name)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate dbrain migration metadata: %w", err)
	}

	for version := 1; version <= userVersion; version++ {
		if _, ok := applied[version]; !ok {
			return fmt.Errorf("%w: dbrain migration metadata is missing version %d", ErrDatabaseIncompatible, version)
		}
	}
	return nil
}

func compatibleSchemaMigrationName(version int, name string) bool {
	names := compatibleSchemaMigrationNames[version]
	_, ok := names[name]
	return ok
}
