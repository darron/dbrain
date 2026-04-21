package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"dbrain/internal/model"
)

const driverName = "sqlite"

const itemSelectColumns = `
	id, source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
	published_at, saved_at, synced_at, language, text, article_title, article_text,
	primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
	like_count, repost_count, reply_count, quote_count, bookmark_count,
	content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
	x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error,
	link_extract_synced_at`

type Store struct {
	db     *sql.DB
	hasFTS bool
}

func Open(path string) (*Store, error) {
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	st := &Store{db: db}
	if err := st.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) HasFTS() bool {
	if s == nil {
		return false
	}
	return s.hasFTS
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
		"x_post_text":            "TEXT NOT NULL DEFAULT ''",
		"x_post_lang":            "TEXT NOT NULL DEFAULT ''",
		"x_post_json":            "TEXT NOT NULL DEFAULT ''",
		"x_post_fetched_at":      "TEXT NOT NULL DEFAULT ''",
		"x_post_status":          "TEXT NOT NULL DEFAULT ''",
		"x_post_error":           "TEXT NOT NULL DEFAULT ''",
		"link_extract_synced_at": "TEXT NOT NULL DEFAULT ''",
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

func (s *Store) UpsertItem(ctx context.Context, item model.Item) (model.UpsertResult, error) {
	return withBusyRetry(ctx, func() (model.UpsertResult, error) {
		return s.upsertItem(ctx, item)
	})
}

func (s *Store) upsertItem(ctx context.Context, item model.Item) (model.UpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.UpsertResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var existingID int64
	var existingHash string
	row := tx.QueryRowContext(ctx, `SELECT id, content_hash FROM items WHERE source_key = ?`, item.SourceKey)
	switch scanErr := row.Scan(&existingID, &existingHash); {
	case errors.Is(scanErr, sql.ErrNoRows):
		now := item.UpdatedAt.Format(time.RFC3339)
		result, execErr := tx.ExecContext(ctx, `INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.SourceKey, item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
			item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
			item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
			item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
			item.ContentHash, item.NotePath, item.RawJSON, now, now, item.LastSeenAt.Format(time.RFC3339))
		if execErr != nil {
			err = fmt.Errorf("insert item %s: %w", item.SourceKey, execErr)
			return model.UpsertResult{}, err
		}
		itemID, execErr := result.LastInsertId()
		if execErr != nil {
			err = fmt.Errorf("fetch inserted row id %s: %w", item.SourceKey, execErr)
			return model.UpsertResult{}, err
		}
		if syncErr := s.syncFTSTx(ctx, tx, itemID, item); syncErr != nil {
			err = syncErr
			return model.UpsertResult{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return model.UpsertResult{}, fmt.Errorf("commit insert: %w", commitErr)
		}
		return model.UpsertResult{Status: model.UpsertCreated, ItemID: itemID, NotePath: item.NotePath}, nil
	case scanErr != nil:
		err = fmt.Errorf("load existing item %s: %w", item.SourceKey, scanErr)
		return model.UpsertResult{}, err
	default:
	}

	if existingHash == item.ContentHash {
		if _, execErr := tx.ExecContext(ctx, `UPDATE items SET last_seen_at = ?, synced_at = ?, raw_json = ? WHERE id = ?`,
			item.LastSeenAt.Format(time.RFC3339), item.SyncedAt, item.RawJSON, existingID); execErr != nil {
			err = fmt.Errorf("touch unchanged item %s: %w", item.SourceKey, execErr)
			return model.UpsertResult{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return model.UpsertResult{}, fmt.Errorf("commit unchanged item: %w", commitErr)
		}
		return model.UpsertResult{Status: model.UpsertUnchanged, ItemID: existingID, NotePath: item.NotePath}, nil
	}

	if _, execErr := tx.ExecContext(ctx, `UPDATE items SET
		source_type = ?, external_id = ?, canonical_url = ?, title = ?, author_handle = ?, author_name = ?,
		published_at = ?, saved_at = ?, synced_at = ?, language = ?, text = ?, article_title = ?, article_text = ?,
		primary_category = ?, primary_domain = ?, links_json = ?, categories = ?, domains = ?, github_urls = ?, folder_names = ?,
		like_count = ?, repost_count = ?, reply_count = ?, quote_count = ?, bookmark_count = ?,
		content_hash = ?, note_path = ?, raw_json = ?, updated_at = ?, last_seen_at = ?
		WHERE id = ?`,
		item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
		item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
		item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
		item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
		item.ContentHash, item.NotePath, item.RawJSON, item.UpdatedAt.Format(time.RFC3339), item.LastSeenAt.Format(time.RFC3339),
		existingID); execErr != nil {
		err = fmt.Errorf("update item %s: %w", item.SourceKey, execErr)
		return model.UpsertResult{}, err
	}

	if syncErr := s.syncFTSTx(ctx, tx, existingID, item); syncErr != nil {
		err = syncErr
		return model.UpsertResult{}, err
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return model.UpsertResult{}, fmt.Errorf("commit update: %w", commitErr)
	}

	return model.UpsertResult{Status: model.UpsertUpdated, ItemID: existingID, NotePath: item.NotePath}, nil
}

func (s *Store) syncFTSTx(ctx context.Context, tx *sql.Tx, itemID int64, item model.Item) error {
	if !s.hasFTS {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, itemID); err != nil {
		return fmt.Errorf("delete fts row %s: %w", item.SourceKey, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (
		rowid, source_key, title, text, article_title, article_text, author_handle, author_name, primary_category, primary_domain
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, item.SourceKey, item.Title, item.Text, item.ArticleTitle, item.ArticleText, item.AuthorHandle, item.AuthorName, item.PrimaryCategory, item.PrimaryDomain); err != nil {
		return fmt.Errorf("insert fts row %s: %w", item.SourceKey, err)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	results := make([]model.SearchResult, 0, limit)
	seen := map[string]struct{}{}

	if s.hasFTS {
		if itemResults, err := s.searchFTS(ctx, query, limit); err == nil {
			for _, result := range itemResults {
				if len(results) >= limit {
					break
				}
				if _, exists := seen[result.SourceKey]; exists {
					continue
				}
				seen[result.SourceKey] = struct{}{}
				results = append(results, result)
			}
		}
		if len(results) < limit {
			if sourceResults, err := s.searchSourcesFTS(ctx, query, limit-len(results)); err == nil {
				for _, result := range sourceResults {
					if len(results) >= limit {
						break
					}
					if _, exists := seen[result.SourceKey]; exists {
						continue
					}
					seen[result.SourceKey] = struct{}{}
					results = append(results, result)
				}
			}
		}
	}

	if len(results) > 0 {
		return results, nil
	}

	itemResults, err := s.searchLike(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	for _, result := range itemResults {
		if len(results) >= limit {
			break
		}
		if _, exists := seen[result.SourceKey]; exists {
			continue
		}
		seen[result.SourceKey] = struct{}{}
		results = append(results, result)
	}
	if len(results) >= limit {
		return results, nil
	}

	sourceResults, err := s.searchSourcesLike(ctx, query, limit-len(results))
	if err != nil {
		return nil, err
	}
	for _, result := range sourceResults {
		if len(results) >= limit {
			break
		}
		if _, exists := seen[result.SourceKey]; exists {
			continue
		}
		seen[result.SourceKey] = struct{}{}
		results = append(results, result)
	}

	return results, nil
}

func (s *Store) searchFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	ftsQuery := buildFTSQuery(query)
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			i.source_key,
			i.external_id,
			i.title,
			i.author_handle,
			i.author_name,
			i.canonical_url,
			i.primary_domain,
			i.note_path,
			substr(trim(replace(COALESCE(NULLIF(i.article_text, ''), i.text), char(10), ' ')), 1, 200) AS snippet
		FROM items_fts f
		JOIN items i ON i.id = f.rowid
		WHERE items_fts MATCH ?
		ORDER BY bm25(items_fts), i.last_seen_at DESC
		LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}

func (s *Store) searchLike(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	like := "%" + strings.TrimSpace(query) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			source_key,
			external_id,
			title,
			author_handle,
			author_name,
			canonical_url,
			primary_domain,
			note_path,
			substr(trim(replace(COALESCE(NULLIF(article_text, ''), text), char(10), ' ')), 1, 200) AS snippet
		FROM items
		WHERE title LIKE ?
			OR text LIKE ?
			OR x_post_text LIKE ?
			OR article_title LIKE ?
			OR article_text LIKE ?
			OR author_handle LIKE ?
			OR author_name LIKE ?
			OR primary_category LIKE ?
			OR primary_domain LIKE ?
		ORDER BY last_seen_at DESC
		LIMIT ?`, like, like, like, like, like, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("like search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}

func scanSearchResults(rows *sql.Rows) ([]model.SearchResult, error) {
	var results []model.SearchResult
	for rows.Next() {
		var result model.SearchResult
		if err := rows.Scan(&result.SourceKey, &result.ExternalID, &result.Title, &result.AuthorHandle, &result.AuthorName, &result.CanonicalURL, &result.PrimaryDomain, &result.NotePath, &result.Snippet); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return results, nil
}

func buildFTSQuery(query string) string {
	parts := strings.Fields(strings.TrimSpace(query))
	if len(parts) == 0 {
		return `""`
	}

	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ReplaceAll(part, `"`, `""`)
		terms = append(terms, fmt.Sprintf(`"%s"*`, part))
	}
	return strings.Join(terms, " AND ")
}

func (s *Store) GetItem(ctx context.Context, lookup string) (model.Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			`+itemSelectColumns+`
		FROM items
		WHERE source_key = ?
			OR external_id = ?
			OR canonical_url = ?
			OR note_path = ?
		LIMIT 1`, lookup, lookup, lookup, lookup)

	var item model.Item
	if err := scanItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Item{}, fmt.Errorf("item not found: %s", lookup)
		}
		return model.Item{}, fmt.Errorf("load item %s: %w", lookup, err)
	}

	return item, nil
}

func (s *Store) ListItemsForXHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_bookmark'
			AND external_id != ''`
	if !force {
		query += `
			AND (x_post_status = ''
				OR x_post_status = 'api_error'
				OR x_post_status = 'error'
				OR x_post_status = 'rate_limited')`
	}
	query += `
		ORDER BY
			CASE WHEN x_post_status = '' THEN 0 ELSE 1 END,
			last_seen_at DESC,
			x_post_fetched_at ASC,
			id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x hydration items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x hydration item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x hydration items: %w", err)
	}

	return items, nil
}

func (s *Store) SaveXHydration(ctx context.Context, itemID int64, hydration model.XHydration) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		row := s.db.QueryRowContext(ctx, `
			SELECT x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error
			FROM items
			WHERE id = ?`, itemID)

		var currentText, currentLang, currentJSON, currentFetchedAt, currentStatus, currentError string
		if err := row.Scan(&currentText, &currentLang, &currentJSON, &currentFetchedAt, &currentStatus, &currentError); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("item not found for hydration: %d", itemID)
			}
			return false, fmt.Errorf("load current hydration %d: %w", itemID, err)
		}

		newFetchedAt := ""
		if !hydration.FetchedAt.IsZero() {
			newFetchedAt = hydration.FetchedAt.UTC().Format(time.RFC3339)
		}

		changed := currentText != hydration.FullText ||
			currentLang != hydration.Language ||
			currentJSON != hydration.APIJSON ||
			currentStatus != hydration.Status ||
			currentError != hydration.Error ||
			(currentFetchedAt == "" && newFetchedAt != "")

		if !changed {
			return false, nil
		}

		if _, err := s.db.ExecContext(ctx, `
			UPDATE items
			SET x_post_text = ?,
				x_post_lang = ?,
				x_post_json = ?,
				x_post_fetched_at = ?,
				x_post_status = ?,
				x_post_error = ?,
				updated_at = ?
			WHERE id = ?`,
			hydration.FullText,
			hydration.Language,
			hydration.APIJSON,
			newFetchedAt,
			hydration.Status,
			hydration.Error,
			time.Now().UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return false, fmt.Errorf("save hydration %d: %w", itemID, err)
		}

		return true, nil
	})
}

func scanItem(scanner interface{ Scan(dest ...any) error }, item *model.Item) error {
	var importedAt, updatedAt, lastSeenAt string
	var xPostFetchedAt string
	var linkExtractSyncedAt string
	if err := scanner.Scan(
		&item.ID, &item.SourceKey, &item.SourceType, &item.ExternalID, &item.CanonicalURL, &item.Title, &item.AuthorHandle, &item.AuthorName,
		&item.PublishedAt, &item.SavedAt, &item.SyncedAt, &item.Language, &item.Text, &item.ArticleTitle, &item.ArticleText,
		&item.PrimaryCategory, &item.PrimaryDomain, &item.LinksJSON, &item.Categories, &item.Domains, &item.GitHubURLs, &item.FolderNames,
		&item.LikeCount, &item.RepostCount, &item.ReplyCount, &item.QuoteCount, &item.BookmarkCount,
		&item.ContentHash, &item.NotePath, &item.RawJSON, &importedAt, &updatedAt, &lastSeenAt,
		&item.XPostText, &item.XPostLang, &item.XPostJSON, &xPostFetchedAt, &item.XPostStatus, &item.XPostError,
		&linkExtractSyncedAt,
	); err != nil {
		return err
	}

	item.ImportedAt = parseStoredTime(importedAt)
	item.UpdatedAt = parseStoredTime(updatedAt)
	item.LastSeenAt = parseStoredTime(lastSeenAt)
	item.XPostFetchedAt = parseStoredTime(xPostFetchedAt)
	item.LinkExtractSyncedAt = parseStoredTime(linkExtractSyncedAt)
	return nil
}

func parseStoredTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
