package store

import "fmt"

func (s *Store) ensureItemEnrichmentTables() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS item_enrichments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			prompt_version TEXT NOT NULL DEFAULT '',
			tool TEXT NOT NULL DEFAULT '',
			tool_version TEXT NOT NULL DEFAULT '',
			input_hash TEXT NOT NULL DEFAULT '',
			completed_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(item_id, role),
			FOREIGN KEY(item_id) REFERENCES items(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_item_enrichments_item ON item_enrichments(item_id);`,
		`CREATE INDEX IF NOT EXISTS idx_item_enrichments_role_status ON item_enrichments(role, status);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply item enrichment schema: %w", err)
		}
	}
	return nil
}
