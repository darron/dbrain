package store

import (
	"fmt"
	"sort"
	"time"
)

const currentSchemaVersion = 5

type schemaMigration struct {
	Version int
	Name    string
	Run     func(*Store) error
}

var schemaMigrations = []schemaMigration{
	{
		Version: 1,
		Name:    "current_schema_baseline",
		Run: func(s *Store) error {
			return s.ensureCurrentSchema()
		},
	},
	{
		Version: 2,
		Name:    "media_download_retry_state",
		Run: func(s *Store) error {
			if err := s.ensureMediaAssetColumns(); err != nil {
				return err
			}
			if _, err := s.db.Exec(`
				CREATE INDEX IF NOT EXISTS idx_media_assets_download_retry
				ON media_assets(download_status, last_download_attempt_at)`); err != nil {
				return fmt.Errorf("ensure media download retry index: %w", err)
			}
			if _, err := s.db.Exec(`
				UPDATE media_assets
				SET download_error_count = CASE
						WHEN download_error_count <= 0 THEN 1
						ELSE download_error_count
					END,
					last_download_attempt_at = CASE
						WHEN last_download_attempt_at = '' THEN updated_at
						ELSE last_download_attempt_at
					END
				WHERE download_status = 'error'`); err != nil {
				return fmt.Errorf("backfill media download retry state: %w", err)
			}
			return nil
		},
	},
	{
		Version: 3,
		Name:    "item_enrichments_current_state",
		Run: func(s *Store) error {
			if err := s.ensureItemEnrichmentTables(); err != nil {
				return err
			}
			return s.backfillItemEnrichments()
		},
	},
	{
		Version: 4,
		Name:    "x_article_canonical_i_article_urls",
		Run: func(s *Store) error {
			return s.backfillXArticleCanonicalURLs()
		},
	},
	{
		Version: 5,
		Name:    "feed_ingestion_tables",
		Run: func(s *Store) error {
			return s.ensureFeedTables()
		},
	},
}

func (s *Store) migrate(reporter MigrationReporter) error {
	if err := validateSchemaMigrations(schemaMigrations); err != nil {
		return err
	}
	if err := s.ensureMigrationTable(); err != nil {
		return err
	}
	applied, err := s.appliedMigrationVersions()
	if err != nil {
		return err
	}

	for _, migration := range schemaMigrations {
		if applied[migration.Version] {
			continue
		}
		reportMigration(reporter, MigrationEvent{
			Phase:         MigrationStarted,
			Version:       migration.Version,
			LatestVersion: currentSchemaVersion,
			Name:          migration.Name,
		})
		if err := migration.Run(s); err != nil {
			reportMigration(reporter, MigrationEvent{
				Phase:         MigrationFailed,
				Version:       migration.Version,
				LatestVersion: currentSchemaVersion,
				Name:          migration.Name,
				Err:           err,
			})
			return fmt.Errorf("apply migration %d %s: %w", migration.Version, migration.Name, err)
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			migration.Version,
			migration.Name,
			time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			reportMigration(reporter, MigrationEvent{
				Phase:         MigrationFailed,
				Version:       migration.Version,
				LatestVersion: currentSchemaVersion,
				Name:          migration.Name,
				Err:           err,
			})
			return fmt.Errorf("record migration %d %s: %w", migration.Version, migration.Name, err)
		}
		reportMigration(reporter, MigrationEvent{
			Phase:         MigrationApplied,
			Version:       migration.Version,
			LatestVersion: currentSchemaVersion,
			Name:          migration.Name,
		})
	}
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, currentSchemaVersion)); err != nil {
		return fmt.Errorf("set schema user_version: %w", err)
	}
	if err := s.refreshFTSAvailability(); err != nil {
		return err
	}
	return nil
}

func reportMigration(reporter MigrationReporter, event MigrationEvent) {
	if reporter == nil {
		return
	}
	reporter(event)
}

func (s *Store) refreshFTSAvailability() error {
	var err error
	s.hasFTS, err = s.tableExists("items_fts")
	if err != nil {
		return fmt.Errorf("check fts availability: %w", err)
	}
	return nil
}

func validateSchemaMigrations(migrations []schemaMigration) error {
	if !sort.SliceIsSorted(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	}) {
		return fmt.Errorf("schema migrations are not sorted")
	}
	seen := map[int]struct{}{}
	for _, migration := range migrations {
		if migration.Version <= 0 {
			return fmt.Errorf("invalid schema migration version %d", migration.Version)
		}
		if migration.Name == "" {
			return fmt.Errorf("schema migration %d has empty name", migration.Version)
		}
		if migration.Run == nil {
			return fmt.Errorf("schema migration %d %s has nil runner", migration.Version, migration.Name)
		}
		if _, ok := seen[migration.Version]; ok {
			return fmt.Errorf("duplicate schema migration version %d", migration.Version)
		}
		seen[migration.Version] = struct{}{}
	}
	return nil
}

func (s *Store) ensureMigrationTable() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	return nil
}

func (s *Store) appliedMigrationVersions() (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied schema migrations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied schema migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied schema migrations: %w", err)
	}
	return applied, nil
}
