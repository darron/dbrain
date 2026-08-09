package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

const (
	currentSchemaVersion                       = 30
	auditProvenanceMigrationVersion            = 12
	auditProvenanceMigrationName               = "audit_provenance_v1"
	retrievalMigrationVersion                  = 13
	retrievalTriggerRepairVersion              = 14
	retrievalTriggerRepairName                 = "retrieval_profile_invariant_triggers_repair"
	retrievalChunkProvenanceVersion            = 15
	retrievalChunkProvenanceName               = "retrieval_chunk_projection_provenance"
	semanticFoundationMigrationVersion         = 16
	semanticFoundationMigrationName            = "retrieval_semantic_foundation_v2"
	semanticProjectionDirtyMigrationVersion    = 17
	semanticProjectionDirtyMigrationName       = "retrieval_projection_dirty_triggers"
	semanticProjectionDirtyRepairVersion       = 18
	semanticProjectionDirtyRepairName          = "retrieval_projection_dirty_trigger_provenance_repair"
	retrievalEmbeddingProfileVersion           = 19
	retrievalEmbeddingProfileName              = "retrieval_embedding_profile_definitions"
	retrievalEmbeddingRevisionRepairVersion    = 20
	retrievalEmbeddingRevisionRepairName       = "retrieval_embedding_revision_provenance_repair"
	retrievalReadinessCountersVersion          = 21
	retrievalReadinessCountersName             = "retrieval_runtime_readiness_counters"
	retrievalSegmentMembershipVersion          = 22
	retrievalSegmentMembershipName             = "retrieval_segment_membership_v1"
	retrievalMembershipL0ActivationVersion     = 23
	retrievalMembershipL0ActivationName        = "retrieval_membership_l0_activation_v1"
	retrievalOccurrenceChunkIndexVersion       = 24
	retrievalOccurrenceChunkIndexName          = "retrieval_occurrence_chunk_cleanup_index"
	semanticRefreshRunsMigrationVersion        = 25
	semanticRefreshRunsMigrationName           = "semantic_refresh_runs_v1"
	semanticRefreshRunsRepairMigrationVersion  = 26
	semanticRefreshRunsRepairMigrationName     = "semantic_refresh_runs_v2_byte_limits"
	semanticRefreshRunsArchiveMigrationVersion = 27
	semanticRefreshRunsArchiveMigrationName    = "semantic_refresh_runs_v25_compatibility_archive"
	retrievalProjectionStagingEpochVersion     = 28
	retrievalProjectionStagingEpochName        = "retrieval_projection_staging_expected_purge_epoch"
	semanticSegmentedDirtyTriggerVersion       = 29
	semanticSegmentedDirtyTriggerName          = "retrieval_segmented_dirty_trigger_repair"
	mastodonSyncStateVersion                   = 30
	mastodonSyncStateName                      = "mastodon_sync_state_v1"
)

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
	{
		Version: 6,
		Name:    "auth_user_approvals",
		Run: func(s *Store) error {
			return s.ensureAuthUserTables()
		},
	},
	{
		Version: 7,
		Name:    "mcp_bearer_tokens",
		Run: func(s *Store) error {
			return s.ensureMCPBearerTokenTables()
		},
	},
	{
		Version: 8,
		Name:    "auth_user_approvals_repair",
		Run: func(s *Store) error {
			return s.ensureAuthUserTables()
		},
	},
	{
		Version: 9,
		Name:    "public_chat_shares",
		Run: func(s *Store) error {
			return s.ensurePublicChatShareTables()
		},
	},
	{
		Version: 10,
		Name:    "feed_parse_error_retry_repair",
		Run: func(s *Store) error {
			return s.repairBlockedParseErrorFeeds()
		},
	},
	{
		Version: 11,
		Name:    "review_event_indexes",
		Run: func(s *Store) error {
			return s.ensureReviewEventIndexes()
		},
	},
	{
		Version: auditProvenanceMigrationVersion,
		Name:    auditProvenanceMigrationName,
		Run: func(s *Store) error {
			if err := s.ensureItemEnrichmentTables(); err != nil {
				return err
			}
			return s.backfillItemEnrichments()
		},
	},
	{
		Version: retrievalMigrationVersion,
		Name:    "retrieval_hybrid_storage_v1",
		Run: func(s *Store) error {
			return s.ensureRetrievalTables()
		},
	},
	{
		Version: retrievalTriggerRepairVersion,
		Name:    retrievalTriggerRepairName,
		Run: func(s *Store) error {
			return s.ensureRetrievalTables()
		},
	},
	{
		Version: retrievalChunkProvenanceVersion,
		Name:    retrievalChunkProvenanceName,
		Run: func(s *Store) error {
			return s.ensureRetrievalTables()
		},
	},
	{
		Version: semanticFoundationMigrationVersion,
		Name:    semanticFoundationMigrationName,
		Run: func(s *Store) error {
			return s.ensureSemanticFoundationRetrievalSchema()
		},
	},
	{
		Version: semanticProjectionDirtyMigrationVersion,
		Name:    semanticProjectionDirtyMigrationName,
		Run: func(s *Store) error {
			return s.ensureSemanticProjectionDirtyTriggersV17()
		},
	},
	{
		Version: semanticProjectionDirtyRepairVersion,
		Name:    semanticProjectionDirtyRepairName,
		Run: func(s *Store) error {
			return s.repairSemanticProjectionDirtyTriggerProvenance()
		},
	},
	{
		Version: retrievalEmbeddingProfileVersion,
		Name:    retrievalEmbeddingProfileName,
		Run: func(s *Store) error {
			return s.ensureRetrievalEmbeddingProfileDefinitions()
		},
	},
	{
		Version: retrievalEmbeddingRevisionRepairVersion,
		Name:    retrievalEmbeddingRevisionRepairName,
		Run: func(s *Store) error {
			return s.repairRetrievalEmbeddingRevisionProvenance()
		},
	},
	{
		Version: retrievalReadinessCountersVersion,
		Name:    retrievalReadinessCountersName,
		Run: func(s *Store) error {
			return s.RepairRetrievalRuntimeReadinessCounters(context.Background())
		},
	},
	{
		Version: retrievalSegmentMembershipVersion,
		Name:    retrievalSegmentMembershipName,
		Run: func(s *Store) error {
			return s.ensureRetrievalSegmentMembershipSchema()
		},
	},
	{
		Version: retrievalMembershipL0ActivationVersion,
		Name:    retrievalMembershipL0ActivationName,
		Run: func(s *Store) error {
			return s.repairRetrievalMembershipL0Activation()
		},
	},
	{
		Version: retrievalOccurrenceChunkIndexVersion,
		Name:    retrievalOccurrenceChunkIndexName,
		Run: func(s *Store) error {
			_, err := s.db.Exec(`
				CREATE INDEX IF NOT EXISTS idx_retrieval_chunk_occurrences_chunk
				ON retrieval_chunk_occurrences(chunk_id)`)
			if err != nil {
				return fmt.Errorf("ensure retrieval occurrence chunk cleanup index: %w", err)
			}
			return nil
		},
	},
	{
		Version: semanticRefreshRunsMigrationVersion,
		Name:    semanticRefreshRunsMigrationName,
		Run: func(s *Store) error {
			return ensureSemanticRefreshRunSchemaV25(s.db)
		},
	},
	{
		Version: semanticRefreshRunsRepairMigrationVersion,
		Name:    semanticRefreshRunsRepairMigrationName,
		Run:     func(s *Store) error { return s.repairSemanticRefreshRunSchemaV26() },
	},
	{
		Version: semanticRefreshRunsArchiveMigrationVersion,
		Name:    semanticRefreshRunsArchiveMigrationName,
		Run: func(s *Store) error {
			if _, err := s.db.Exec(semanticRefreshRunsV25CompatibilityArchiveCreateTableSQL); err != nil {
				return fmt.Errorf("ensure semantic refresh run v25 compatibility archive: %w", err)
			}
			return nil
		},
	},
	{
		Version: retrievalProjectionStagingEpochVersion,
		Name:    retrievalProjectionStagingEpochName,
		Run: func(s *Store) error {
			return s.ensureRetrievalProjectionStagingPurgeEpoch()
		},
	},
	{
		Version: semanticSegmentedDirtyTriggerVersion,
		Name:    semanticSegmentedDirtyTriggerName,
		Run: func(s *Store) error {
			return s.ensureSemanticProjectionDirtyTriggers()
		},
	},
	{
		Version: mastodonSyncStateVersion,
		Name:    mastodonSyncStateName,
		Run: func(s *Store) error {
			return ensureMastodonSyncStateTable(s.db)
		},
	},
}

func newRetrievalDatabaseID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate retrieval database id: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
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
	// Repair databases whose local migration history was stamped before a
	// schema mutation completed. Migration numbers are immutable, so reopening
	// must make these invariants true even when their metadata rows already exist.
	if err := s.ensureRetrievalProjectionStagingPurgeEpoch(); err != nil {
		return err
	}
	// Repair the v30 Mastodon state invariant even when the migration metadata
	// was stamped before its table or index was fully created.
	if err := ensureMastodonSyncStateTable(s.db); err != nil {
		return err
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
