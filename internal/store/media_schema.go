package store

import "fmt"

func (s *Store) ensureMediaTables() error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			remote_url TEXT NOT NULL UNIQUE,
			media_type TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			byte_size INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT '',
			download_status TEXT NOT NULL DEFAULT '',
			download_error TEXT NOT NULL DEFAULT '',
			download_error_count INTEGER NOT NULL DEFAULT 0,
			last_download_attempt_at TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			archive_provider TEXT NOT NULL DEFAULT '',
			archive_bucket TEXT NOT NULL DEFAULT '',
			archive_key TEXT NOT NULL DEFAULT '',
			archive_url TEXT NOT NULL DEFAULT '',
			archive_etag TEXT NOT NULL DEFAULT '',
			archive_status TEXT NOT NULL DEFAULT '',
			archive_error TEXT NOT NULL DEFAULT '',
			discovered_at TEXT NOT NULL DEFAULT '',
			downloaded_at TEXT NOT NULL DEFAULT '',
			archived_at TEXT NOT NULL DEFAULT '',
			local_pruned_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS item_media_links (
			item_id INTEGER NOT NULL,
			media_asset_id INTEGER NOT NULL,
			ordinal INTEGER NOT NULL DEFAULT 0,
			expanded_url TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (item_id, media_asset_id),
			UNIQUE (item_id, ordinal),
			FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE,
			FOREIGN KEY (media_asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
		);`,
	}

	for _, stmt := range tables {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply media schema: %w", err)
		}
	}

	if err := s.ensureMediaAssetColumns(); err != nil {
		return err
	}
	if err := s.ensureItemMediaLinkColumns(); err != nil {
		return err
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_media_assets_download_status ON media_assets(download_status);`,
		`CREATE INDEX IF NOT EXISTS idx_media_assets_download_retry ON media_assets(download_status, last_download_attempt_at);`,
		`CREATE INDEX IF NOT EXISTS idx_media_assets_content_hash ON media_assets(content_hash);`,
		`CREATE INDEX IF NOT EXISTS idx_item_media_links_media_asset_id ON item_media_links(media_asset_id);`,
	}
	for _, stmt := range indexes {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply media schema: %w", err)
		}
	}

	return nil
}

func (s *Store) ensureMediaAssetColumns() error {
	return s.ensureColumns("media_assets", []columnDefinition{
		{Name: "media_type", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "mime_type", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "width", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "height", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "byte_size", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "content_hash", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "download_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "download_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "download_error_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "last_download_attempt_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "local_path", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_provider", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_bucket", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_key", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_url", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_etag", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_status", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archive_error", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "discovered_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "downloaded_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "archived_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "local_pruned_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	})
}

func (s *Store) ensureItemMediaLinkColumns() error {
	return s.ensureColumns("item_media_links", []columnDefinition{
		{Name: "ordinal", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "expanded_url", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "created_at", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
	})
}
