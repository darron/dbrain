package store

import (
	"database/sql"
	"fmt"
)

func (s *Store) ensureSourceTables() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_key TEXT NOT NULL UNIQUE,
			canonical_url TEXT NOT NULL,
			normalized_url TEXT NOT NULL UNIQUE,
			source_type TEXT NOT NULL,
			domain TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			site_name TEXT NOT NULL DEFAULT '',
			extracted_text TEXT NOT NULL DEFAULT '',
			extract_json TEXT NOT NULL DEFAULT '',
			extract_status TEXT NOT NULL DEFAULT '',
			extract_error TEXT NOT NULL DEFAULT '',
			extract_failure_kind TEXT NOT NULL DEFAULT '',
			extract_failure_count INTEGER NOT NULL DEFAULT 0,
			extract_first_failed_at TEXT NOT NULL DEFAULT '',
			extract_last_failed_at TEXT NOT NULL DEFAULT '',
			extracted_at TEXT NOT NULL DEFAULT '',
			extract_tool TEXT NOT NULL DEFAULT '',
			extract_tool_version TEXT NOT NULL DEFAULT '',
			summary_text TEXT NOT NULL DEFAULT '',
			summary_json TEXT NOT NULL DEFAULT '',
			summary_status TEXT NOT NULL DEFAULT '',
			summary_error TEXT NOT NULL DEFAULT '',
			summary_model TEXT NOT NULL DEFAULT '',
			summary_content_hash TEXT NOT NULL DEFAULT '',
			summary_prompt_version TEXT NOT NULL DEFAULT '',
			summary_tool TEXT NOT NULL DEFAULT '',
			summary_tool_version TEXT NOT NULL DEFAULT '',
			summarized_at TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			note_path TEXT NOT NULL DEFAULT '',
			user_tags TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_source_type ON sources(source_type);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_domain ON sources(domain);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_extract_status ON sources(extract_status);`,
		`CREATE INDEX IF NOT EXISTS idx_sources_summary_status ON sources(summary_status);`,
		`CREATE TABLE IF NOT EXISTS item_source_links (
			item_id INTEGER NOT NULL,
			source_id INTEGER NOT NULL,
			original_url TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (item_id, source_id),
			FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE,
			FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_item_source_links_source_id ON item_source_links(source_id);`,
		`CREATE TABLE IF NOT EXISTS source_summary_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			summary_text TEXT NOT NULL,
			summary_json TEXT NOT NULL,
			summary_status TEXT NOT NULL,
			summary_error TEXT NOT NULL DEFAULT '',
			summary_model TEXT NOT NULL DEFAULT '',
			summary_prompt_version TEXT NOT NULL DEFAULT '',
			summary_tool TEXT NOT NULL DEFAULT '',
			summary_tool_version TEXT NOT NULL DEFAULT '',
			summarized_at TEXT NOT NULL,
			FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_source_summary_versions_source_id ON source_summary_versions(source_id, summarized_at DESC);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply source schema: %w", err)
		}
	}

	if err := s.ensureSourceColumns(); err != nil {
		return err
	}
	if err := s.ensureSourceSummaryVersionColumns(); err != nil {
		return err
	}

	_, _ = s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS sources_fts USING fts5(
		source_key UNINDEXED,
		title,
		description,
		site_name,
		extracted_text,
		summary_text,
		domain,
		tokenize = 'porter unicode61'
	);`)

	return nil
}

func (s *Store) ensureSourceColumns() error {
	return s.ensureColumns("sources", []columnDefinition{
		{Name: "domain", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "description", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "site_name", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extracted_text", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_failure_kind", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_failure_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "extract_first_failed_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_last_failed_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extracted_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_tool", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "extract_tool_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_text", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_model", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_content_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_prompt_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_tool", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_tool_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summarized_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "content_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "note_path", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "user_tags", Definition: "TEXT NOT NULL DEFAULT ''"},
	})
}

func (s *Store) ensureSourceSummaryVersionColumns() error {
	return s.ensureColumns("source_summary_versions", []columnDefinition{
		{Name: "summary_tool", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_tool_version", Definition: "TEXT NOT NULL DEFAULT ''"},
	})
}

func (s *Store) tableColumns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return existing, nil
}
