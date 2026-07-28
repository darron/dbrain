package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type RetrievalProjectionStatus string

const (
	RetrievalProjectionPending RetrievalProjectionStatus = "pending"
	RetrievalProjectionCurrent RetrievalProjectionStatus = "current"
	RetrievalProjectionEmpty   RetrievalProjectionStatus = "empty"
	RetrievalProjectionBlocked RetrievalProjectionStatus = "blocked"
	RetrievalProjectionError   RetrievalProjectionStatus = "error"
)

type RetrievalEmbeddingProfileRow struct {
	ProfileID, ActiveGenerationID                          string
	LatestRevision, PurgeEpoch, ActiveSnapshotRevision     int64
	ActiveIndexedCount, L0ReadyCount, ActiveTombstoneCount int
}

type retrievalConstraintTrigger struct {
	name  string
	table string
	sql   string
}

var semanticFoundationConstraintTriggers = []retrievalConstraintTrigger{
	{
		name:  "trg_retrieval_state_singleton_insert",
		table: "retrieval_state",
		sql: `CREATE TRIGGER trg_retrieval_state_singleton_insert
			BEFORE INSERT ON retrieval_state
			WHEN NEW.singleton IS NULL OR NEW.singleton != 1
				OR EXISTS (SELECT 1 FROM retrieval_state WHERE singleton = 1)
			BEGIN
				SELECT RAISE(ABORT, 'retrieval state requires one singleton = 1 row');
			END`,
	},
	{
		name:  "trg_retrieval_state_singleton_update",
		table: "retrieval_state",
		sql: `CREATE TRIGGER trg_retrieval_state_singleton_update
			BEFORE UPDATE OF singleton ON retrieval_state
			WHEN NEW.singleton IS NULL OR NEW.singleton != 1
				OR (NEW.singleton != OLD.singleton AND EXISTS (SELECT 1 FROM retrieval_state WHERE singleton = 1))
			BEGIN
				SELECT RAISE(ABORT, 'retrieval state requires one singleton = 1 row');
			END`,
	},
	{
		name:  "trg_retrieval_chunk_occurrences_chunk_insert",
		table: "retrieval_chunk_occurrences",
		sql: `CREATE TRIGGER trg_retrieval_chunk_occurrences_chunk_insert
			BEFORE INSERT ON retrieval_chunk_occurrences
			WHEN NEW.chunk_id IS NULL
				OR NOT EXISTS (SELECT 1 FROM retrieval_chunks WHERE chunk_id = NEW.chunk_id)
			BEGIN
				SELECT RAISE(ABORT, 'retrieval chunk occurrence requires an existing chunk');
			END`,
	},
	{
		name:  "trg_retrieval_chunk_occurrences_chunk_update",
		table: "retrieval_chunk_occurrences",
		sql: `CREATE TRIGGER trg_retrieval_chunk_occurrences_chunk_update
			BEFORE UPDATE OF chunk_id ON retrieval_chunk_occurrences
			WHEN NEW.chunk_id IS NULL
				OR NOT EXISTS (SELECT 1 FROM retrieval_chunks WHERE chunk_id = NEW.chunk_id)
			BEGIN
				SELECT RAISE(ABORT, 'retrieval chunk occurrence requires an existing chunk');
			END`,
	},
	{
		name:  "trg_retrieval_chunks_delete_occurrences",
		table: "retrieval_chunks",
		sql: `CREATE TRIGGER trg_retrieval_chunks_delete_occurrences
			AFTER DELETE ON retrieval_chunks
			BEGIN
				DELETE FROM retrieval_chunk_occurrences WHERE chunk_id = OLD.chunk_id;
			END`,
	},
	{
		name:  "trg_retrieval_chunks_update_occurrences",
		table: "retrieval_chunks",
		sql: `CREATE TRIGGER trg_retrieval_chunks_update_occurrences
			BEFORE UPDATE OF chunk_id ON retrieval_chunks
			WHEN NEW.chunk_id IS NOT OLD.chunk_id
				AND EXISTS (SELECT 1 FROM retrieval_chunk_occurrences WHERE chunk_id = OLD.chunk_id)
			BEGIN
				SELECT RAISE(ABORT, 'retrieval chunk ID is referenced by an occurrence');
			END`,
	},
}

func (s *Store) ensureRetrievalTables() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS retrieval_chunks (
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
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("ensure retrieval schema: %w", err)
		}
	}
	if err := s.ensureColumns("retrieval_chunks", []columnDefinition{
		{Name: "section_ordinal", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "projection_version", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval chunks: %w", err)
	}

	statements = []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_retrieval_chunks_parent_ordinal_unique
			ON retrieval_chunks(parent_kind, parent_source_key, ordinal)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_chunks_parent
			ON retrieval_chunks(parent_kind, parent_source_key, ordinal)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_chunks_content
			ON retrieval_chunks(parent_kind, parent_source_key, input_content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_chunks_role
			ON retrieval_chunks(evidence_role, chunk_id)`,
		`CREATE TABLE IF NOT EXISTS retrieval_embeddings (
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
			PRIMARY KEY(chunk_id, profile_id),
			FOREIGN KEY(chunk_id) REFERENCES retrieval_chunks(chunk_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_embeddings_profile_status
			ON retrieval_embeddings(profile_id, status, chunk_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_retrieval_embeddings_chunk_profile_unique
			ON retrieval_embeddings(chunk_id, profile_id)`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_chunks_delete_embeddings
			AFTER DELETE ON retrieval_chunks
			BEGIN
				DELETE FROM retrieval_embeddings WHERE chunk_id = OLD.chunk_id;
			END`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_embeddings_profile_invariants_insert
			BEFORE INSERT ON retrieval_embeddings
			WHEN EXISTS (
				SELECT 1 FROM retrieval_embeddings e
				WHERE e.profile_id = NEW.profile_id
					AND (e.provider != NEW.provider
						OR e.model != NEW.model
						OR e.dimensions != NEW.dimensions
						OR e.representation != NEW.representation
						OR e.normalization != NEW.normalization)
			)
			BEGIN
				SELECT RAISE(ABORT, 'retrieval embedding profile invariants do not match');
			END`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_embeddings_profile_invariants_update
			BEFORE UPDATE ON retrieval_embeddings
			WHEN (
				(OLD.profile_id = NEW.profile_id
					AND (OLD.provider != NEW.provider
						OR OLD.model != NEW.model
						OR OLD.dimensions != NEW.dimensions
						OR OLD.representation != NEW.representation
						OR OLD.normalization != NEW.normalization))
				OR EXISTS (
					SELECT 1 FROM retrieval_embeddings e
					WHERE e.profile_id = NEW.profile_id
						AND NOT (e.chunk_id = OLD.chunk_id AND e.profile_id = OLD.profile_id)
						AND (e.provider != NEW.provider
							OR e.model != NEW.model
							OR e.dimensions != NEW.dimensions
							OR e.representation != NEW.representation
							OR e.normalization != NEW.normalization)
				)
			)
			BEGIN
				SELECT RAISE(ABORT, 'retrieval embedding profile invariants do not match');
			END`,
		`CREATE TABLE IF NOT EXISTS retrieval_index_generations (
			generation_id TEXT PRIMARY KEY,
			profile_id TEXT NOT NULL,
			backend TEXT NOT NULL,
			backend_version TEXT NOT NULL,
			dimensions INTEGER NOT NULL,
			distance_metric TEXT NOT NULL,
			indexed_chunk_count INTEGER NOT NULL DEFAULT 0,
			source_manifest_hash TEXT NOT NULL DEFAULT '',
			build_status TEXT NOT NULL,
			build_error TEXT NOT NULL DEFAULT '',
			relative_cache_path TEXT NOT NULL DEFAULT '',
			build_started_at TEXT NOT NULL DEFAULT '',
			build_completed_at TEXT NOT NULL DEFAULT '',
			activated_at TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 0 CHECK(active IN (0, 1)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_generations_profile_status
			ON retrieval_index_generations(profile_id, build_status, generation_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_retrieval_generations_one_active_profile
			ON retrieval_index_generations(profile_id) WHERE active = 1`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_generations_completed_active_insert
			BEFORE INSERT ON retrieval_index_generations
			WHEN NEW.active = 1 AND NEW.build_status != 'completed'
			BEGIN
				SELECT RAISE(ABORT, 'only completed retrieval generations can be active');
			END`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_generations_completed_active_update
			BEFORE UPDATE ON retrieval_index_generations
			WHEN NEW.active = 1 AND NEW.build_status != 'completed'
			BEGIN
				SELECT RAISE(ABORT, 'only completed retrieval generations can be active');
			END`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("ensure retrieval schema: %w", err)
		}
	}
	return ensureSemanticRefreshRunSchema(s.db)
}

func (s *Store) ensureSemanticFoundationRetrievalSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS retrieval_state (
			singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
			database_id TEXT NOT NULL,
			projection_work_revision INTEGER NOT NULL DEFAULT 0,
			purge_epoch INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS retrieval_parent_projections (
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			projection_hash TEXT NOT NULL DEFAULT '',
			projection_version TEXT NOT NULL DEFAULT '',
			chunker_version TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			chunk_count INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			dirty_at TEXT NOT NULL DEFAULT '',
			dirty_revision INTEGER NOT NULL DEFAULT 0,
			projected_revision INTEGER NOT NULL DEFAULT 0,
			projected_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(parent_kind, parent_source_key)
		)`,
		`CREATE TABLE IF NOT EXISTS retrieval_chunk_occurrences (
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			chunk_id TEXT NOT NULL,
			section_key TEXT NOT NULL,
			start_char INTEGER NOT NULL,
			end_char INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(chunk_id) REFERENCES retrieval_chunks(chunk_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS retrieval_projection_staging (
			work_id TEXT NOT NULL,
			dirty_revision INTEGER NOT NULL,
			parent_kind TEXT NOT NULL,
			parent_source_key TEXT NOT NULL,
			projection_hash TEXT NOT NULL DEFAULT '',
			section_key TEXT NOT NULL DEFAULT '',
			next_boundary INTEGER NOT NULL DEFAULT 0,
			chunk_id TEXT NOT NULL DEFAULT '',
			chunk_json TEXT NOT NULL DEFAULT '',
			occurrence_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS retrieval_embedding_profiles (
			profile_id TEXT PRIMARY KEY,
			latest_revision INTEGER NOT NULL DEFAULT 0,
			purge_epoch INTEGER NOT NULL DEFAULT 0,
			active_generation_id TEXT NOT NULL DEFAULT '',
			active_snapshot_revision INTEGER NOT NULL DEFAULT 0,
			active_indexed_count INTEGER NOT NULL DEFAULT 0,
			l0_ready_count INTEGER NOT NULL DEFAULT 0,
			active_tombstone_count INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("ensure semantic retrieval foundation schema: %w", err)
		}
	}
	if err := s.ensureColumns("retrieval_state", []columnDefinition{
		{Name: "singleton", Definition: "INTEGER NOT NULL DEFAULT 1"},
		{Name: "database_id", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "projection_work_revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "purge_epoch", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval state: %w", err)
	}
	if err := s.ensureColumns("retrieval_parent_projections", []columnDefinition{
		{Name: "projection_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "projection_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "chunker_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "status", Definition: "TEXT NOT NULL DEFAULT 'pending'"},
		{Name: "chunk_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "reason", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "dirty_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "dirty_revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "projected_revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "projected_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval parent projections: %w", err)
	}
	if err := s.ensureColumns("retrieval_chunks", []columnDefinition{
		{Name: "section_key", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "heading_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "derived", Definition: "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return fmt.Errorf("repair retrieval chunks semantic identity: %w", err)
	}
	if err := s.ensureColumns("retrieval_embeddings", []columnDefinition{
		{Name: "revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "vector_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval embedding revisions: %w", err)
	}
	if err := s.ensureColumns("retrieval_chunk_occurrences", []columnDefinition{
		{Name: "parent_kind", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "parent_source_key", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "chunk_id", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "section_key", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "start_char", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "end_char", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "created_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval chunk occurrences: %w", err)
	}
	if err := s.ensureColumns("retrieval_projection_staging", []columnDefinition{
		{Name: "work_id", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "dirty_revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "parent_kind", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "parent_source_key", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "projection_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "section_key", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "next_boundary", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "chunk_id", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "chunk_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "occurrence_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "created_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval projection staging: %w", err)
	}
	if err := s.ensureColumns("retrieval_embedding_profiles", []columnDefinition{
		{Name: "profile_id", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "latest_revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "purge_epoch", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "active_generation_id", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "active_snapshot_revision", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "active_indexed_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "l0_ready_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "active_tombstone_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("repair retrieval embedding profiles: %w", err)
	}
	if err := s.ensureSemanticFoundationConstraints(); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_retrieval_chunks_v3_identity_unique
			ON retrieval_chunks(parent_kind, parent_source_key, section_key, evidence_role, derived, heading_hash, chunk_text_hash)
			WHERE chunker_version = 'retrieval-chunker-v3'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_retrieval_chunk_occurrences_unique
			ON retrieval_chunk_occurrences(parent_kind, parent_source_key, chunk_id, section_key, start_char, end_char)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_retrieval_projection_staging_work_unique
			ON retrieval_projection_staging(work_id, dirty_revision, section_key, next_boundary, chunk_id)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_parent_projections_pending
			ON retrieval_parent_projections(status, dirty_revision, parent_kind, parent_source_key)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_chunk_occurrences_parent
			ON retrieval_chunk_occurrences(parent_kind, parent_source_key, section_key, start_char)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_projection_staging_parent
			ON retrieval_projection_staging(parent_kind, parent_source_key, dirty_revision, work_id)`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("ensure semantic retrieval foundation index: %w", err)
		}
	}
	return s.seedSemanticFoundationRetrievalParents()
}

func (s *Store) ensureRetrievalSegmentMembershipSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS retrieval_index_segments (
			segment_hash TEXT PRIMARY KEY,
			profile_id TEXT NOT NULL,
			backend TEXT NOT NULL,
			backend_version TEXT NOT NULL,
			dimensions INTEGER NOT NULL,
			distance_metric TEXT NOT NULL,
			indexed_chunk_count INTEGER NOT NULL,
			relative_cache_path TEXT NOT NULL,
			membership_hash TEXT NOT NULL,
			payload_hash TEXT NOT NULL,
			manifest_hash TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_index_segments_profile
			ON retrieval_index_segments(profile_id, segment_hash)`,
		`CREATE TABLE IF NOT EXISTS retrieval_index_segment_members (
			segment_hash TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			chunk_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			vector_hash TEXT NOT NULL,
			PRIMARY KEY(segment_hash, ordinal),
			UNIQUE(segment_hash, chunk_id),
			FOREIGN KEY(segment_hash) REFERENCES retrieval_index_segments(segment_hash) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_index_segment_members_chunk
			ON retrieval_index_segment_members(chunk_id, revision)`,
		`CREATE TABLE IF NOT EXISTS retrieval_generation_segments (
			generation_id TEXT NOT NULL,
			segment_hash TEXT NOT NULL,
			PRIMARY KEY(generation_id, segment_hash),
			FOREIGN KEY(generation_id) REFERENCES retrieval_index_generations(generation_id) ON DELETE RESTRICT,
			FOREIGN KEY(segment_hash) REFERENCES retrieval_index_segments(segment_hash) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_generation_segments_segment
			ON retrieval_generation_segments(segment_hash, generation_id)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("ensure retrieval segment membership schema: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureSemanticFoundationConstraints() error {
	var invalidSingletons int
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM retrieval_state
		WHERE singleton IS NULL OR singleton != 1`).Scan(&invalidSingletons); err != nil {
		return fmt.Errorf("validate retrieval_state singleton: %w", err)
	}
	if invalidSingletons != 0 {
		return fmt.Errorf("invalid retrieval_state singleton: %d rows are not singleton = 1", invalidSingletons)
	}
	var singletonRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM retrieval_state`).Scan(&singletonRows); err != nil {
		return fmt.Errorf("count retrieval_state singleton rows: %w", err)
	}
	if singletonRows > 1 {
		return fmt.Errorf("duplicate retrieval_state singleton: found %d rows", singletonRows)
	}
	var orphanOccurrences int
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM retrieval_chunk_occurrences occurrence
		LEFT JOIN retrieval_chunks chunk ON chunk.chunk_id = occurrence.chunk_id
		WHERE chunk.chunk_id IS NULL`).Scan(&orphanOccurrences); err != nil {
		return fmt.Errorf("validate retrieval_chunk_occurrences references: %w", err)
	}
	if orphanOccurrences != 0 {
		return fmt.Errorf("orphan retrieval_chunk_occurrences: %d rows reference missing retrieval_chunks", orphanOccurrences)
	}
	for _, trigger := range semanticFoundationConstraintTriggers {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			return fmt.Errorf("drop semantic retrieval constraint trigger %s: %w", trigger.name, err)
		}
		if _, err := s.db.Exec(trigger.sql); err != nil {
			return fmt.Errorf("create semantic retrieval constraint trigger %s: %w", trigger.name, err)
		}
	}
	return nil
}

func (s *Store) seedSemanticFoundationRetrievalParents() error {
	const eligibleParents = `
		SELECT 'item' AS parent_kind, source_key AS parent_source_key
		FROM items
		WHERE trim(note_path) != ''
		UNION ALL
		SELECT 'source' AS parent_kind, source_key AS parent_source_key
		FROM sources
		WHERE trim(note_path) != ''`

	var databaseID string
	err := s.db.QueryRow(`SELECT database_id FROM retrieval_state WHERE singleton = 1`).Scan(&databaseID)
	switch {
	case err == sql.ErrNoRows:
		generatedID, generateErr := newRetrievalDatabaseID()
		if generateErr != nil {
			return generateErr
		}
		databaseID = generatedID
	case err != nil:
		return fmt.Errorf("read retrieval database identity: %w", err)
	}
	if strings.TrimSpace(databaseID) == "" {
		generatedID, generateErr := newRetrievalDatabaseID()
		if generateErr != nil {
			return generateErr
		}
		databaseID = generatedID
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err == sql.ErrNoRows {
		if _, err := s.db.Exec(`
			INSERT INTO retrieval_state (singleton, database_id, projection_work_revision, purge_epoch, updated_at)
			VALUES (1, ?, 0, 0, ?)`, databaseID, now); err != nil {
			return fmt.Errorf("initialize retrieval state: %w", err)
		}
	} else if _, err := s.db.Exec(`
		UPDATE retrieval_state
		SET database_id = ?,
			updated_at = CASE WHEN trim(updated_at) = '' THEN ? ELSE updated_at END
		WHERE singleton = 1`, databaseID, now); err != nil {
		return fmt.Errorf("repair retrieval state identity: %w", err)
	}

	var needsSeed bool
	if err := s.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM (` + eligibleParents + `) eligible
			LEFT JOIN retrieval_parent_projections parent
				ON parent.parent_kind = eligible.parent_kind
				AND parent.parent_source_key = eligible.parent_source_key
			WHERE parent.parent_source_key IS NULL
		)`).Scan(&needsSeed); err != nil {
		return fmt.Errorf("check retrieval parent migration seed: %w", err)
	}
	if !needsSeed {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE retrieval_state
		SET projection_work_revision = projection_work_revision + 1, updated_at = ?
		WHERE singleton = 1`, now); err != nil {
		return fmt.Errorf("allocate retrieval parent migration revision: %w", err)
	}
	var revision int64
	if err := s.db.QueryRow(`SELECT projection_work_revision FROM retrieval_state WHERE singleton = 1`).Scan(&revision); err != nil {
		return fmt.Errorf("read retrieval parent migration revision: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO retrieval_parent_projections (
			parent_kind, parent_source_key, projection_version, chunker_version,
			status, dirty_at, dirty_revision, updated_at
		)
		SELECT parent_kind, parent_source_key, 'retrieval-projection-v2', 'retrieval-chunker-v3', 'pending', ?, ?, ?
		FROM (`+eligibleParents+`)`, now, revision, now); err != nil {
		return fmt.Errorf("seed retrieval parent projections: %w", err)
	}
	return nil
}
