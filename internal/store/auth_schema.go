package store

import "fmt"

func (s *Store) ensureAuthUserTables() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS auth_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL,
			github_id TEXT NOT NULL DEFAULT '',
			github_username TEXT NOT NULL,
			github_username_normalized TEXT NOT NULL,
			email TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			avatar_url TEXT NOT NULL DEFAULT '',
			approved_at TEXT NOT NULL,
			last_login_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(provider, github_username_normalized)
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_users_provider_github_id
			ON auth_users(provider, github_id)
			WHERE github_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_auth_users_provider_updated
			ON auth_users(provider, updated_at);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply auth user schema: %w", err)
		}
	}
	return nil
}
