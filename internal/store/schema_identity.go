package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
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

var dbrainSemanticFoundationSchema = []schemaIdentityTable{
	{
		name: "retrieval_state",
		columns: []string{
			"singleton", "database_id", "projection_work_revision", "purge_epoch", "updated_at",
		},
	},
	{
		name: "retrieval_parent_projections",
		columns: []string{
			"parent_kind", "parent_source_key", "projection_hash", "projection_version", "chunker_version",
			"status", "chunk_count", "reason", "dirty_at", "dirty_revision", "projected_revision", "projected_at", "updated_at",
		},
	},
	{
		name:    "retrieval_chunks",
		columns: []string{"section_key", "heading_hash", "derived"},
	},
	{
		name:    "retrieval_embeddings",
		columns: []string{"revision", "vector_hash"},
	},
	{
		name:    "retrieval_chunk_occurrences",
		columns: []string{"parent_kind", "parent_source_key", "chunk_id", "section_key", "start_char", "end_char", "created_at", "updated_at"},
	},
	{
		name:    "retrieval_projection_staging",
		columns: []string{"work_id", "dirty_revision", "parent_kind", "parent_source_key", "projection_hash", "section_key", "next_boundary", "chunk_id", "chunk_json", "occurrence_json", "created_at", "updated_at"},
	},
	{
		name:    "retrieval_embedding_profiles",
		columns: []string{"profile_id", "latest_revision", "purge_epoch", "active_generation_id", "active_snapshot_revision", "active_indexed_count", "l0_ready_count", "active_tombstone_count", "updated_at"},
	},
}

var dbrainEmbeddingProfileDefinitionSchema = []schemaIdentityTable{{
	name: "retrieval_embedding_profiles",
	columns: []string{
		"provider", "model", "dimensions", "projection_version", "chunker_version", "representation", "normalization",
	},
}}

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
	requiredSchema := dbrainCoreSchema
	hasSemanticFoundation := false
	var semanticProjectionDirtyTriggersForVersion []retrievalConstraintTrigger
	hasMigrationTable, err := st.tableExists("schema_migrations")
	if err != nil {
		return 0, 0, fmt.Errorf("inspect dbrain migration table: %w", err)
	}
	if hasMigrationTable {
		var found int
		err := st.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1`, semanticFoundationMigrationVersion).Scan(&found)
		switch {
		case err == nil:
			requiredSchema = append(requiredSchema, dbrainSemanticFoundationSchema...)
			hasSemanticFoundation = true
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return 0, 0, fmt.Errorf("inspect dbrain semantic foundation migration: %w", err)
		}
		err = st.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1`, semanticProjectionDirtyMigrationVersion).Scan(&found)
		switch {
		case err == nil:
			semanticProjectionDirtyTriggersForVersion = semanticProjectionDirtyTriggersV17
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return 0, 0, fmt.Errorf("inspect dbrain semantic projection dirty migration: %w", err)
		}
		err = st.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1`, semanticProjectionDirtyRepairVersion).Scan(&found)
		switch {
		case err == nil:
			semanticProjectionDirtyTriggersForVersion = semanticProjectionDirtyTriggers
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return 0, 0, fmt.Errorf("inspect dbrain semantic projection dirty repair migration: %w", err)
		}
		err = st.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1`, retrievalEmbeddingProfileVersion).Scan(&found)
		switch {
		case err == nil:
			requiredSchema = append(requiredSchema, dbrainEmbeddingProfileDefinitionSchema...)
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return 0, 0, fmt.Errorf("inspect dbrain embedding profile migration: %w", err)
		}
	}

	missingTables, missingColumns := 0, 0
	var firstMissing error
	for _, required := range requiredSchema {
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
	if hasSemanticFoundation {
		var err error
		missingTables, firstMissing, err = inspectCanonicalTriggers(st, "semantic foundation", semanticFoundationConstraintTriggers, missingTables, firstMissing)
		if err != nil {
			return missingTables, missingColumns, err
		}
	}
	if len(semanticProjectionDirtyTriggersForVersion) != 0 {
		var err error
		missingTables, firstMissing, err = inspectCanonicalTriggers(st, "semantic projection dirty", semanticProjectionDirtyTriggersForVersion, missingTables, firstMissing)
		if err != nil {
			return missingTables, missingColumns, err
		}
	}
	if hasMigrationTable {
		var found int
		err := st.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1`, retrievalEmbeddingProfileVersion).Scan(&found)
		if err == nil {
			var inspectErr error
			missingTables, firstMissing, inspectErr = inspectCanonicalTriggers(st, "embedding profile definitions", retrievalEmbeddingProfileTriggersV19, missingTables, firstMissing)
			if inspectErr != nil {
				return missingTables, missingColumns, inspectErr
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return missingTables, missingColumns, fmt.Errorf("inspect embedding profile migration triggers: %w", err)
		}
	}
	return missingTables, missingColumns, firstMissing
}

func inspectCanonicalTriggers(st *Store, schemaName string, requiredTriggers []retrievalConstraintTrigger, missingTables int, firstMissing error) (int, error, error) {
	for _, required := range requiredTriggers {
		var table, definition string
		err := st.db.QueryRow(`SELECT tbl_name, sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, required.name).Scan(&table, &definition)
		switch {
		case err == nil && table == required.table && normalizeSQLiteTriggerSQL(definition) == normalizeSQLiteTriggerSQL(required.sql):
		case err == nil:
			missingTables++
			if firstMissing == nil {
				if table != required.table {
					firstMissing = fmt.Errorf("%w: dbrain %s trigger %s belongs to %s, want %s", ErrDatabaseIncompatible, schemaName, required.name, table, required.table)
				} else {
					firstMissing = fmt.Errorf("%w: dbrain %s trigger %s has a non-canonical definition", ErrDatabaseIncompatible, schemaName, required.name)
				}
			}
		case errors.Is(err, sql.ErrNoRows):
			missingTables++
			if firstMissing == nil {
				firstMissing = fmt.Errorf("%w: dbrain %s trigger %s is missing", ErrDatabaseIncompatible, schemaName, required.name)
			}
		case err != nil:
			return missingTables, firstMissing, fmt.Errorf("inspect dbrain %s trigger %s: %w", schemaName, required.name, err)
		}
	}
	return missingTables, firstMissing, nil
}

// normalizeSQLiteTriggerSQL compares the canonical trigger definitions with
// SQLite's sqlite_master representation without treating layout or case as
// schema identity. The definitions are static trusted SQL, so a compact token
// form is sufficient and keeps validation independent of SQLite formatting.
func normalizeSQLiteTriggerSQL(definition string) string {
	var normalized strings.Builder
	normalized.Grow(len(definition))
	for _, r := range strings.ToLower(definition) {
		if !unicode.IsSpace(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
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
