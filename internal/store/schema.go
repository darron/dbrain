package store

import (
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) initReadOnly(path string) error {
	pragmas := []string{
		"PRAGMA busy_timeout = 1000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA query_only = ON;",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply read-only pragma %q: %w", stmt, err)
		}
	}

	hasItems, err := s.tableExists("items")
	if err != nil {
		return err
	}
	if !hasItems {
		return fmt.Errorf("items table not found in %s", path)
	}
	s.hasFTS, err = s.tableExists("items_fts")
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) tableExists(name string) (bool, error) {
	var found int
	if err := s.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ? LIMIT 1`, name).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check table %s: %w", name, err)
	}
	return true, nil
}

func (s *Store) init() error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 60000;",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}

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
	existing := map[string]bool{}
	rows, err := s.db.Query(`PRAGMA table_info(items)`)
	if err != nil {
		return fmt.Errorf("load item table info: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan item table info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate item table info: %w", err)
	}

	required := map[string]string{
		"x_post_text":               "TEXT NOT NULL DEFAULT ''",
		"x_post_lang":               "TEXT NOT NULL DEFAULT ''",
		"x_post_json":               "TEXT NOT NULL DEFAULT ''",
		"x_post_fetched_at":         "TEXT NOT NULL DEFAULT ''",
		"x_post_status":             "TEXT NOT NULL DEFAULT ''",
		"x_post_error":              "TEXT NOT NULL DEFAULT ''",
		"link_extract_synced_at":    "TEXT NOT NULL DEFAULT ''",
		"summary_text":              "TEXT NOT NULL DEFAULT ''",
		"summary_json":              "TEXT NOT NULL DEFAULT ''",
		"summary_status":            "TEXT NOT NULL DEFAULT ''",
		"summary_error":             "TEXT NOT NULL DEFAULT ''",
		"summary_model":             "TEXT NOT NULL DEFAULT ''",
		"summary_prompt_version":    "TEXT NOT NULL DEFAULT ''",
		"summary_tool":              "TEXT NOT NULL DEFAULT ''",
		"summary_tool_version":      "TEXT NOT NULL DEFAULT ''",
		"summary_input_hash":        "TEXT NOT NULL DEFAULT ''",
		"summarized_at":             "TEXT NOT NULL DEFAULT ''",
		"ocr_text":                  "TEXT NOT NULL DEFAULT ''",
		"ocr_json":                  "TEXT NOT NULL DEFAULT ''",
		"ocr_status":                "TEXT NOT NULL DEFAULT ''",
		"ocr_error":                 "TEXT NOT NULL DEFAULT ''",
		"ocr_model":                 "TEXT NOT NULL DEFAULT ''",
		"ocr_tool":                  "TEXT NOT NULL DEFAULT ''",
		"ocr_tool_version":          "TEXT NOT NULL DEFAULT ''",
		"ocr_input_hash":            "TEXT NOT NULL DEFAULT ''",
		"ocr_at":                    "TEXT NOT NULL DEFAULT ''",
		"x_media_transcript_status": "TEXT NOT NULL DEFAULT ''",
		"x_media_transcript_error":  "TEXT NOT NULL DEFAULT ''",
		"x_media_transcript_at":     "TEXT NOT NULL DEFAULT ''",
		"user_tags":                 "TEXT NOT NULL DEFAULT ''",
	}

	for name, definition := range required {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE items ADD COLUMN %s %s", name, definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add items.%s: %w", name, err)
		}
	}

	return nil
}
