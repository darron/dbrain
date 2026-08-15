package store

import "fmt"

func (s *Store) ensureLinkCaptureQueueSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS link_capture_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			normalized_url TEXT NOT NULL UNIQUE,
			original_url TEXT NOT NULL,
			canonical_url TEXT NOT NULL,
			source_type TEXT NOT NULL,
			domain TEXT NOT NULL,
			source_key TEXT NOT NULL,
			note_path TEXT NOT NULL,
			enqueued_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_attempt_at TEXT NOT NULL DEFAULT '',
			next_attempt_at TEXT NOT NULL DEFAULT '',
			processed_at TEXT NOT NULL DEFAULT '',
			dead_lettered_at TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_link_capture_queue_pending
			ON link_capture_queue(processed_at, next_attempt_at, enqueued_at);`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("apply link capture queue schema: %w", err)
		}
	}
	if err := s.ensureColumns("link_capture_queue", []columnDefinition{
		{Name: "dead_lettered_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("ensure link capture queue columns: %w", err)
	}
	return nil
}
