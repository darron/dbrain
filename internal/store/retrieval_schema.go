package store

import "fmt"

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
	return nil
}
