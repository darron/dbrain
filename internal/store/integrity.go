package store

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	databaseCompatibilityCurrent      = "current_compatible"
	databaseCompatibilityLegacy       = "legacy_compatible"
	databaseCompatibilityIncompatible = "incompatible"
)

type DatabaseIntegrity struct {
	QuickCheckChecked         bool   `json:"quick_check_checked"`
	QuickCheck                string `json:"quick_check,omitempty"`
	QuickViolationCount       int    `json:"quick_violation_count"`
	ForeignKeyViolationCount  int    `json:"foreign_key_violation_count"`
	SchemaCompatibility       string `json:"schema_compatibility"`
	MigrationCompatibility    string `json:"migration_compatibility"`
	UserVersion               int    `json:"user_version"`
	SupportedVersion          int    `json:"supported_version"`
	AppliedMigrationCount     int    `json:"applied_migration_count"`
	MissingTableCount         int    `json:"missing_table_count"`
	MissingColumnCount        int    `json:"missing_column_count"`
	schemaCompatibilityErr    error
	migrationCompatibilityErr error
}

func InspectDatabaseReadOnly(ctx context.Context, path string, includeIntegrity bool) (DatabaseIntegrity, error) {
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return DatabaseIntegrity{}, fmt.Errorf("open candidate database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { _ = db.Close() }()

	result := DatabaseIntegrity{SupportedVersion: currentSchemaVersion}
	if includeIntegrity {
		if err := inspectSQLiteIntegrity(ctx, db, &result); err != nil {
			return DatabaseIntegrity{}, err
		}
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&result.UserVersion); err != nil {
		return DatabaseIntegrity{}, fmt.Errorf("read schema user_version: %w", err)
	}

	st := &Store{db: db}
	result.MissingTableCount, result.MissingColumnCount, result.schemaCompatibilityErr = inspectDbrainCoreSchema(st)
	result.SchemaCompatibility = databaseCompatibilityIncompatible
	if result.schemaCompatibilityErr == nil {
		result.SchemaCompatibility = databaseCompatibilityCurrent
	}

	hasMigrations, err := st.tableExists("schema_migrations")
	if err != nil {
		return DatabaseIntegrity{}, fmt.Errorf("check dbrain migration metadata: %w", err)
	}
	switch {
	case result.UserVersion > currentSchemaVersion:
		result.MigrationCompatibility = databaseCompatibilityIncompatible
		result.migrationCompatibilityErr = fmt.Errorf("dbrain schema version %d is newer than supported version %d", result.UserVersion, currentSchemaVersion)
	case !hasMigrations && result.UserVersion == 0:
		result.MigrationCompatibility = databaseCompatibilityLegacy
		if result.schemaCompatibilityErr == nil {
			result.SchemaCompatibility = databaseCompatibilityLegacy
		}
	case !hasMigrations:
		result.MigrationCompatibility = databaseCompatibilityIncompatible
		result.migrationCompatibilityErr = fmt.Errorf("dbrain schema version %d has no migration metadata", result.UserVersion)
	case result.UserVersion <= 0:
		result.MigrationCompatibility = databaseCompatibilityIncompatible
		result.migrationCompatibilityErr = fmt.Errorf("dbrain migration metadata has invalid schema version %d", result.UserVersion)
	default:
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&result.AppliedMigrationCount); err != nil {
			return DatabaseIntegrity{}, fmt.Errorf("count dbrain migration metadata: %w", err)
		}
		result.migrationCompatibilityErr = validateAppliedSchemaMigrations(ctx, db, result.UserVersion)
		if result.migrationCompatibilityErr == nil {
			if result.UserVersion == currentSchemaVersion {
				result.MigrationCompatibility = databaseCompatibilityCurrent
			} else {
				result.MigrationCompatibility = databaseCompatibilityLegacy
			}
		} else {
			result.MigrationCompatibility = databaseCompatibilityIncompatible
		}
	}
	return result, nil
}

func inspectSQLiteIntegrity(ctx context.Context, db *sql.DB, result *DatabaseIntegrity) error {
	rows, err := db.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return fmt.Errorf("run sqlite quick_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result.QuickCheckChecked = true
	result.QuickCheck = "ok"
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			return fmt.Errorf("scan sqlite quick_check: %w", err)
		}
		if message != "ok" {
			result.QuickViolationCount++
			if result.QuickCheck == "ok" {
				result.QuickCheck = "violation"
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite quick_check: %w", err)
	}

	fkRows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("run sqlite foreign_key_check: %w", err)
	}
	defer func() { _ = fkRows.Close() }()
	for fkRows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var fkID int
		if err := fkRows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return fmt.Errorf("scan sqlite foreign_key_check: %w", err)
		}
		result.ForeignKeyViolationCount++
	}
	return fkRows.Err()
}
