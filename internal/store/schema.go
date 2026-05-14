package store

import (
	"fmt"
)

func (s *Store) ensureCurrentSchema() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_key TEXT NOT NULL UNIQUE,
			source_type TEXT NOT NULL,
			external_id TEXT NOT NULL,
			canonical_url TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			author_handle TEXT NOT NULL DEFAULT '',
			author_name TEXT NOT NULL DEFAULT '',
			published_at TEXT NOT NULL DEFAULT '',
			saved_at TEXT NOT NULL DEFAULT '',
			synced_at TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL DEFAULT '',
			article_title TEXT NOT NULL DEFAULT '',
			article_text TEXT NOT NULL DEFAULT '',
			primary_category TEXT NOT NULL DEFAULT '',
			primary_domain TEXT NOT NULL DEFAULT '',
			links_json TEXT NOT NULL DEFAULT '[]',
			categories TEXT NOT NULL DEFAULT '',
			domains TEXT NOT NULL DEFAULT '',
			github_urls TEXT NOT NULL DEFAULT '',
			folder_names TEXT NOT NULL DEFAULT '',
			like_count INTEGER NOT NULL DEFAULT 0,
			repost_count INTEGER NOT NULL DEFAULT 0,
			reply_count INTEGER NOT NULL DEFAULT 0,
			quote_count INTEGER NOT NULL DEFAULT 0,
			bookmark_count INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL,
			note_path TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL,
			imported_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_items_source_type ON items(source_type);`,
		`CREATE INDEX IF NOT EXISTS idx_items_external_id ON items(external_id);`,
		`CREATE INDEX IF NOT EXISTS idx_items_last_seen_at ON items(last_seen_at);`,
		`CREATE INDEX IF NOT EXISTS idx_items_primary_domain ON items(primary_domain);`,
		`CREATE INDEX IF NOT EXISTS idx_items_primary_category ON items(primary_category);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}

	if err := s.ensureItemColumns(); err != nil {
		return err
	}
	if err := s.ensureSourceTables(); err != nil {
		return err
	}
	if err := s.ensureMediaTables(); err != nil {
		return err
	}
	if err := s.ensureItemLinkTables(); err != nil {
		return err
	}
	if err := s.ensureItemEnrichmentTables(); err != nil {
		return err
	}
	if err := s.ensureFeedTables(); err != nil {
		return err
	}
	if err := s.ensureAuthUserTables(); err != nil {
		return err
	}
	if err := s.ensureMCPBearerTokenTables(); err != nil {
		return err
	}

	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS items_fts USING fts5(
		source_key UNINDEXED,
		title,
		text,
		article_title,
		article_text,
		author_handle,
		author_name,
		primary_category,
		primary_domain,
		tokenize = 'porter unicode61'
	);`); err == nil {
		s.hasFTS = true
	} else {
		s.hasFTS = false
	}

	return nil
}

func (s *Store) ensureItemColumns() error {
	return s.ensureColumns("items", []columnDefinition{
		{Name: "x_post_text", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_post_lang", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_post_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_post_fetched_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_post_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_post_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "link_extract_synced_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_text", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_model", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_prompt_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_tool", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_tool_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summary_input_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "summarized_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_text", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_json", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_model", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_tool", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_tool_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_input_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "ocr_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_media_transcript_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_media_transcript_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "x_media_transcript_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "user_tags", Definition: "TEXT NOT NULL DEFAULT ''"},
	})
}

type columnDefinition struct {
	Name       string
	Definition string
}

func (s *Store) ensureColumns(table string, required []columnDefinition) error {
	existing, err := s.tableColumns(table)
	if err != nil {
		return fmt.Errorf("load %s table info: %w", table, err)
	}

	for _, column := range required {
		if existing[column.Name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column.Name, column.Definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, column.Name, err)
		}
	}

	return nil
}
