package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

const driverName = "sqlite"

const xItemSourceTypeWhere = "(source_type = 'x_bookmark' OR source_type = 'x_quote')"
const linkDiscoveryItemSourceTypeWhere = "(source_type = 'x_bookmark' OR source_type = 'x_quote' OR source_type = 'apple_note' OR source_type = 'safari_tab')"
const xTopLevelMediaObjectsWhere = `(json_valid(x_post_json) AND json_extract(x_post_json, '$.snapshot.media_objects[0].type') IS NOT NULL)`
const xQuotedPostRepairWhere = `((x_post_json LIKE '%"quoted_tweet"%' OR x_post_json LIKE '%"quoted_status_result"%' OR x_post_json LIKE '%"quoted_post"%')
	AND NOT EXISTS (
		SELECT 1
		FROM item_item_links q
		WHERE q.parent_item_id = items.id
			AND q.link_kind = 'quoted_post'
	))`
const xQuoteDirectHydrationRepairWhere = `(source_type = 'x_quote'
	AND x_post_status = 'ok_graphql'
	AND x_post_json NOT LIKE '%"tweetResult"%')`
const xNoteTweetLinkRepairWhere = `(json_valid(x_post_json)
	AND EXISTS (
		SELECT 1
		FROM json_tree(
			CASE WHEN json_valid(items.x_post_json) THEN items.x_post_json ELSE '{}' END,
			'$.raw.data.tweetResult.result.note_tweet.note_tweet_results.result.entity_set.urls'
		) note_url
		WHERE note_url.key = 'expanded_url'
			AND COALESCE(note_url.value, '') != ''
			AND NOT EXISTS (
				SELECT 1
				FROM json_each(CASE WHEN json_valid(items.links_json) THEN items.links_json ELSE '[]' END) existing_link
				WHERE existing_link.value = note_url.value
			)
	))`
const xMediaHydrationRepairWhere = `(` + xTopLevelMediaObjectsWhere + `
	AND (
		NOT EXISTS (
			SELECT 1
			FROM item_media_links l
			WHERE l.item_id = items.id
		)
		OR EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND (
					a.download_status = ''
					OR a.download_status = 'pending'
					OR a.download_status = 'error'
				)
		)
		OR EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND a.download_status = 'downloaded'
				AND a.media_type IN ('video', 'animated_gif')
				AND (
					a.local_path GLOB '*.jpg'
					OR a.local_path GLOB '*.jpeg'
					OR a.local_path GLOB '*.png'
					OR a.local_path GLOB '*.webp'
					OR a.remote_url LIKE 'https://pbs.twimg.com/%'
				)
		)
	))`
const xHydrationRepairWhere = `(` + xQuotedPostRepairWhere + `
	OR ` + xQuoteDirectHydrationRepairWhere + `
	OR ` + xNoteTweetLinkRepairWhere + `)`
const xHydrationCandidateWhere = `(
	x_post_status = ''
	OR x_post_status = 'api_error'
	OR x_post_status = 'error'
	OR x_post_status = 'rate_limited'
	OR (
		x_post_status LIKE 'ok_%'
		AND (
			` + xMediaHydrationRepairWhere + `
			OR ` + xHydrationRepairWhere + `
		)
	)
)`

const itemSelectColumns = `
	id, source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
	published_at, saved_at, synced_at, language, text, article_title, article_text,
	primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
	like_count, repost_count, reply_count, quote_count, bookmark_count,
	content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
	x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error,
	link_extract_synced_at,
	summary_text, summary_json, summary_status, summary_error, summary_model,
	summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
	ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at,
	x_media_transcript_status, x_media_transcript_error, x_media_transcript_at,
	user_tags`

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

// OpenReadOnly opens an existing store for read-only consumers such as MCP.
// It intentionally skips schema creation/migrations so startup cannot block
// behind a long-running writer before the MCP initialize response is sent.
func OpenReadOnly(path string) (*Store, error) {
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	st := &Store{db: db}
	if err := st.initReadOnly(path); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func readOnlyDSN(path string) string {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	return uri.String()
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
	var existingArticleTitle string
	var existingArticleText string
	var existingSummary itemSummaryFields
	var existingOCR itemOCRFields
	row := tx.QueryRowContext(ctx, `SELECT
		id, content_hash, article_title, article_text,
		summary_text, summary_json, summary_status, summary_error, summary_model, summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
		ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at
		FROM items
		WHERE source_key = ?`, item.SourceKey)
	switch scanErr := row.Scan(
		&existingID, &existingHash, &existingArticleTitle, &existingArticleText,
		&existingSummary.Text, &existingSummary.JSON, &existingSummary.Status, &existingSummary.Error, &existingSummary.Model, &existingSummary.PromptVersion, &existingSummary.Tool, &existingSummary.ToolVersion, &existingSummary.InputHash, &existingSummary.At,
		&existingOCR.Text, &existingOCR.JSON, &existingOCR.Status, &existingOCR.Error, &existingOCR.Model, &existingOCR.Tool, &existingOCR.ToolVersion, &existingOCR.InputHash, &existingOCR.At,
	); {
	case errors.Is(scanErr, sql.ErrNoRows):
		now := item.UpdatedAt.Format(time.RFC3339)
		result, execErr := tx.ExecContext(ctx, `INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			summary_text, summary_json, summary_status, summary_error, summary_model, summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
			ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.SourceKey, item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
			item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
			item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
			item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
			item.ContentHash, item.NotePath, item.RawJSON, now, now, item.LastSeenAt.Format(time.RFC3339),
			item.SummaryText, item.SummaryJSON, item.SummaryStatus, item.SummaryError, item.SummaryModel, item.SummaryPromptVersion, item.SummaryTool, item.SummaryToolVersion, item.SummaryInputHash, formatTimeForDB(item.SummarizedAt),
			item.OCRText, item.OCRJSON, item.OCRStatus, item.OCRError, item.OCRModel, item.OCRTool, item.OCRToolVersion, item.OCRInputHash, formatTimeForDB(item.OCRAt))
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

	if preserveExistingItemEnrichmentFields(&item, existingArticleTitle, existingArticleText, existingSummary, existingOCR) {
		item.ContentHash = itemhash.Compute(item)
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
		content_hash = ?, note_path = ?, raw_json = ?, updated_at = ?, last_seen_at = ?,
		summary_text = ?, summary_json = ?, summary_status = ?, summary_error = ?, summary_model = ?, summary_prompt_version = ?, summary_tool = ?, summary_tool_version = ?, summary_input_hash = ?, summarized_at = ?,
		ocr_text = ?, ocr_json = ?, ocr_status = ?, ocr_error = ?, ocr_model = ?, ocr_tool = ?, ocr_tool_version = ?, ocr_input_hash = ?, ocr_at = ?
		WHERE id = ?`,
		item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
		item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
		item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
		item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
		item.ContentHash, item.NotePath, item.RawJSON, item.UpdatedAt.Format(time.RFC3339), item.LastSeenAt.Format(time.RFC3339),
		item.SummaryText, item.SummaryJSON, item.SummaryStatus, item.SummaryError, item.SummaryModel, item.SummaryPromptVersion, item.SummaryTool, item.SummaryToolVersion, item.SummaryInputHash, formatTimeForDB(item.SummarizedAt),
		item.OCRText, item.OCRJSON, item.OCRStatus, item.OCRError, item.OCRModel, item.OCRTool, item.OCRToolVersion, item.OCRInputHash, formatTimeForDB(item.OCRAt),
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

type itemSummaryFields struct {
	Text          string
	JSON          string
	Status        string
	Error         string
	Model         string
	PromptVersion string
	Tool          string
	ToolVersion   string
	InputHash     string
	At            string
}

type itemOCRFields struct {
	Text        string
	JSON        string
	Status      string
	Error       string
	Model       string
	Tool        string
	ToolVersion string
	InputHash   string
	At          string
}

func preserveExistingItemEnrichmentFields(item *model.Item, existingArticleTitle string, existingArticleText string, existingSummary itemSummaryFields, existingOCR itemOCRFields) bool {
	if item == nil {
		return false
	}

	changed := false
	if strings.TrimSpace(item.ArticleText) == "" && strings.TrimSpace(existingArticleText) != "" {
		item.ArticleText = existingArticleText
		changed = true
	}
	if strings.TrimSpace(item.ArticleTitle) == "" && strings.TrimSpace(existingArticleTitle) != "" {
		item.ArticleTitle = existingArticleTitle
		changed = true
	}
	if strings.TrimSpace(item.SummaryText) == "" && strings.TrimSpace(existingSummary.Text) != "" {
		item.SummaryText = existingSummary.Text
		changed = true
	}
	if strings.TrimSpace(item.SummaryJSON) == "" && strings.TrimSpace(existingSummary.JSON) != "" {
		item.SummaryJSON = existingSummary.JSON
		changed = true
	}
	if strings.TrimSpace(item.SummaryStatus) == "" && strings.TrimSpace(existingSummary.Status) != "" {
		item.SummaryStatus = existingSummary.Status
		changed = true
	}
	if strings.TrimSpace(item.SummaryError) == "" && strings.TrimSpace(existingSummary.Error) != "" {
		item.SummaryError = existingSummary.Error
		changed = true
	}
	if strings.TrimSpace(item.SummaryModel) == "" && strings.TrimSpace(existingSummary.Model) != "" {
		item.SummaryModel = existingSummary.Model
		changed = true
	}
	if strings.TrimSpace(item.SummaryPromptVersion) == "" && strings.TrimSpace(existingSummary.PromptVersion) != "" {
		item.SummaryPromptVersion = existingSummary.PromptVersion
		changed = true
	}
	if strings.TrimSpace(item.SummaryTool) == "" && strings.TrimSpace(existingSummary.Tool) != "" {
		item.SummaryTool = existingSummary.Tool
		changed = true
	}
	if strings.TrimSpace(item.SummaryToolVersion) == "" && strings.TrimSpace(existingSummary.ToolVersion) != "" {
		item.SummaryToolVersion = existingSummary.ToolVersion
		changed = true
	}
	if strings.TrimSpace(item.SummaryInputHash) == "" && strings.TrimSpace(existingSummary.InputHash) != "" {
		item.SummaryInputHash = existingSummary.InputHash
		changed = true
	}
	if item.SummarizedAt.IsZero() && strings.TrimSpace(existingSummary.At) != "" {
		item.SummarizedAt = parseStoredTime(existingSummary.At)
		changed = true
	}
	if strings.TrimSpace(item.OCRText) == "" && strings.TrimSpace(existingOCR.Text) != "" {
		item.OCRText = existingOCR.Text
		changed = true
	}
	if strings.TrimSpace(item.OCRJSON) == "" && strings.TrimSpace(existingOCR.JSON) != "" {
		item.OCRJSON = existingOCR.JSON
		changed = true
	}
	if strings.TrimSpace(item.OCRStatus) == "" && strings.TrimSpace(existingOCR.Status) != "" {
		item.OCRStatus = existingOCR.Status
		changed = true
	}
	if strings.TrimSpace(item.OCRError) == "" && strings.TrimSpace(existingOCR.Error) != "" {
		item.OCRError = existingOCR.Error
		changed = true
	}
	if strings.TrimSpace(item.OCRModel) == "" && strings.TrimSpace(existingOCR.Model) != "" {
		item.OCRModel = existingOCR.Model
		changed = true
	}
	if strings.TrimSpace(item.OCRTool) == "" && strings.TrimSpace(existingOCR.Tool) != "" {
		item.OCRTool = existingOCR.Tool
		changed = true
	}
	if strings.TrimSpace(item.OCRToolVersion) == "" && strings.TrimSpace(existingOCR.ToolVersion) != "" {
		item.OCRToolVersion = existingOCR.ToolVersion
		changed = true
	}
	if strings.TrimSpace(item.OCRInputHash) == "" && strings.TrimSpace(existingOCR.InputHash) != "" {
		item.OCRInputHash = existingOCR.InputHash
		changed = true
	}
	if item.OCRAt.IsZero() && strings.TrimSpace(existingOCR.At) != "" {
		item.OCRAt = parseStoredTime(existingOCR.At)
		changed = true
	}
	return changed
}

func formatTimeForDB(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
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
		itemID, item.SourceKey, item.Title, item.Text, item.ArticleTitle, indexedItemArticleText(item), item.AuthorHandle, item.AuthorName, item.PrimaryCategory, item.PrimaryDomain); err != nil {
		return fmt.Errorf("insert fts row %s: %w", item.SourceKey, err)
	}
	return nil
}

type RebuildFTSStats struct {
	Rebuilt int
	Skipped int
	Errors  int
}

func (s *Store) RebuildFTS(ctx context.Context) (RebuildFTSStats, error) {
	if !s.hasFTS {
		return RebuildFTSStats{}, fmt.Errorf("FTS is not enabled")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RebuildFTSStats{}, fmt.Errorf("begin fts rebuild tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts`); err != nil {
		return RebuildFTSStats{}, fmt.Errorf("clear fts table: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT `+itemSelectColumns+` FROM items ORDER BY id ASC`)
	if err != nil {
		return RebuildFTSStats{}, fmt.Errorf("list items for fts rebuild: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stats RebuildFTSStats
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			stats.Errors++
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (
			rowid, source_key, title, text, article_title, article_text, author_handle, author_name, primary_category, primary_domain
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.SourceKey, item.Title, item.Text, item.ArticleTitle, indexedItemArticleText(item),
			item.AuthorHandle, item.AuthorName, item.PrimaryCategory, item.PrimaryDomain); err != nil {
			stats.Errors++
			continue
		}
		stats.Rebuilt++
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	return stats, tx.Commit()
}

func indexedItemArticleText(item model.Item) string {
	parts := make([]string, 0, 4)
	for _, value := range []string{strings.TrimSpace(item.XPostText), strings.TrimSpace(item.ArticleText), strings.TrimSpace(item.SummaryText), strings.TrimSpace(item.OCRText), strings.TrimSpace(item.UserTags)} {
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n\n")
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
			i.source_type,
			i.external_id,
			i.title,
			i.author_handle,
			i.author_name,
			i.canonical_url,
			i.primary_domain,
			i.note_path,
			i.user_tags,
			substr(trim(replace(COALESCE(NULLIF(i.summary_text, ''), NULLIF(i.ocr_text, ''), NULLIF(i.article_text, ''), i.text), char(10), ' ')), 1, 200) AS snippet
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
			source_type,
			external_id,
			title,
			author_handle,
			author_name,
			canonical_url,
			primary_domain,
			note_path,
			user_tags,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), NULLIF(ocr_text, ''), NULLIF(article_text, ''), text), char(10), ' ')), 1, 200) AS snippet
		FROM items
		WHERE title LIKE ?
			OR text LIKE ?
			OR x_post_text LIKE ?
			OR article_title LIKE ?
			OR article_text LIKE ?
			OR summary_text LIKE ?
			OR ocr_text LIKE ?
			OR author_handle LIKE ?
			OR author_name LIKE ?
			OR primary_category LIKE ?
			OR primary_domain LIKE ?
			OR canonical_url LIKE ?
			OR external_id LIKE ?
			OR user_tags LIKE ?
		ORDER BY last_seen_at DESC
		LIMIT ?`, like, like, like, like, like, like, like, like, like, like, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("like search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}

func (s *Store) SearchUserTags(ctx context.Context, tagQuery string, limit int) ([]model.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	tagQuery = strings.TrimSpace(strings.ToLower(tagQuery))
	if tagQuery == "" {
		return nil, nil
	}
	like := "%" + tagQuery + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			source_key,
			source_type,
			external_id,
			title,
			author_handle,
			author_name,
			canonical_url,
			primary_domain,
			note_path,
			user_tags,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), NULLIF(ocr_text, ''), NULLIF(article_text, ''), text), char(10), ' ')), 1, 200) AS snippet
		FROM items
		WHERE lower(user_tags) LIKE ?
		UNION ALL
		SELECT
			source_key,
			source_type,
			'' AS external_id,
			title,
			'' AS author_handle,
			'' AS author_name,
			canonical_url,
			domain AS primary_domain,
			note_path,
			user_tags,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), extracted_text), char(10), ' ')), 1, 200) AS snippet
		FROM sources
		WHERE lower(user_tags) LIKE ?
		ORDER BY source_key DESC
		LIMIT ?`, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("tag search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}

func (s *Store) SearchExactUserTag(ctx context.Context, tag string, limit int) ([]model.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	tag = normalizeUserTagQuery(tag)
	if tag == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			source_key,
			source_type,
			external_id,
			title,
			author_handle,
			author_name,
			canonical_url,
			primary_domain,
			note_path,
			user_tags,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), NULLIF(ocr_text, ''), NULLIF(article_text, ''), text), char(10), ' ')), 1, 200) AS snippet
		FROM items
		WHERE instr(',' || replace(replace(lower(user_tags), ', ', ','), ' ,', ',') || ',', ',' || ? || ',') > 0
		UNION ALL
		SELECT
			source_key,
			source_type,
			'' AS external_id,
			title,
			'' AS author_handle,
			'' AS author_name,
			canonical_url,
			domain AS primary_domain,
			note_path,
			user_tags,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), extracted_text), char(10), ' ')), 1, 200) AS snippet
		FROM sources
		WHERE instr(',' || replace(replace(lower(user_tags), ', ', ','), ' ,', ',') || ',', ',' || ? || ',') > 0
		ORDER BY source_key DESC
		LIMIT ?`, tag, tag, limit)
	if err != nil {
		return nil, fmt.Errorf("exact tag search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}

func (s *Store) CountExactUserTag(ctx context.Context, tag string, sourceTypes []string) (int, error) {
	tag = normalizeUserTagQuery(tag)
	if tag == "" {
		return 0, nil
	}
	itemQuery := `SELECT COUNT(*) FROM items WHERE instr(',' || replace(replace(lower(user_tags), ', ', ','), ' ,', ',') || ',', ',' || ? || ',') > 0`
	sourceQuery := `SELECT COUNT(*) FROM sources WHERE instr(',' || replace(replace(lower(user_tags), ', ', ','), ' ,', ',') || ',', ',' || ? || ',') > 0`
	itemArgs := []any{tag}
	sourceArgs := []any{tag}
	if len(sourceTypes) > 0 {
		placeholders := make([]string, 0, len(sourceTypes))
		for _, sourceType := range sourceTypes {
			sourceType = strings.TrimSpace(sourceType)
			if sourceType == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			itemArgs = append(itemArgs, sourceType)
			sourceArgs = append(sourceArgs, sourceType)
		}
		if len(placeholders) > 0 {
			filter := ` AND source_type IN (` + strings.Join(placeholders, ",") + `)`
			itemQuery += filter
			sourceQuery += filter
		}
	}
	var itemCount int
	if err := s.db.QueryRowContext(ctx, itemQuery, itemArgs...).Scan(&itemCount); err != nil {
		return 0, fmt.Errorf("count exact tag %q: %w", tag, err)
	}
	var sourceCount int
	if err := s.db.QueryRowContext(ctx, sourceQuery, sourceArgs...).Scan(&sourceCount); err != nil {
		return 0, fmt.Errorf("count exact source tag %q: %w", tag, err)
	}
	return itemCount + sourceCount, nil
}

func (s *Store) CountItemTextMatches(ctx context.Context, query string, sourceTypes []string) (int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, nil
	}
	if s.hasFTS {
		count, err := s.countItemFTSMatches(ctx, query, sourceTypes)
		if err == nil {
			return count, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
	}
	query = strings.ToLower(query)
	like := "%" + query + "%"
	sqlQuery := `
		SELECT COUNT(*)
		FROM items
		WHERE (
			lower(title) LIKE ?
			OR lower(text) LIKE ?
			OR lower(x_post_text) LIKE ?
			OR lower(article_title) LIKE ?
			OR lower(article_text) LIKE ?
			OR lower(summary_text) LIKE ?
			OR lower(ocr_text) LIKE ?
			OR lower(author_handle) LIKE ?
			OR lower(author_name) LIKE ?
			OR lower(canonical_url) LIKE ?
			OR lower(user_tags) LIKE ?
		)`
	args := []any{like, like, like, like, like, like, like, like, like, like, like}
	if len(sourceTypes) > 0 {
		placeholders := make([]string, 0, len(sourceTypes))
		for _, sourceType := range sourceTypes {
			sourceType = strings.TrimSpace(sourceType)
			if sourceType == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, sourceType)
		}
		if len(placeholders) > 0 {
			sqlQuery += ` AND source_type IN (` + strings.Join(placeholders, ",") + `)`
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, sqlQuery, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count item text matches %q: %w", query, err)
	}
	return count, nil
}

func (s *Store) countItemFTSMatches(ctx context.Context, query string, sourceTypes []string) (int, error) {
	ftsQuery := buildFTSQuery(query)
	sqlQuery := `
		SELECT COUNT(*)
		FROM items_fts f
		JOIN items i ON i.id = f.rowid
		WHERE items_fts MATCH ?`
	args := []any{ftsQuery}
	if filter, filterArgs := sourceTypeFilter("i.source_type", sourceTypes); filter != "" {
		sqlQuery += filter
		args = append(args, filterArgs...)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, sqlQuery, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count item fts matches %q: %w", query, err)
	}
	return count, nil
}

func (s *Store) CountSourceTextMatches(ctx context.Context, query string, sourceTypes []string) (int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, nil
	}
	if s.hasFTS {
		count, err := s.countSourceFTSMatches(ctx, query, sourceTypes)
		if err == nil {
			return count, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
	}
	query = strings.ToLower(query)
	like := "%" + query + "%"
	sqlQuery := `
		SELECT COUNT(*)
		FROM sources
		WHERE (
			lower(title) LIKE ?
			OR lower(description) LIKE ?
			OR lower(extracted_text) LIKE ?
			OR lower(summary_text) LIKE ?
			OR lower(canonical_url) LIKE ?
			OR lower(domain) LIKE ?
			OR lower(user_tags) LIKE ?
		)`
	args := []any{like, like, like, like, like, like, like}
	if len(sourceTypes) > 0 {
		placeholders := make([]string, 0, len(sourceTypes))
		for _, sourceType := range sourceTypes {
			sourceType = strings.TrimSpace(sourceType)
			if sourceType == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, sourceType)
		}
		if len(placeholders) > 0 {
			sqlQuery += ` AND source_type IN (` + strings.Join(placeholders, ",") + `)`
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, sqlQuery, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count source text matches %q: %w", query, err)
	}
	return count, nil
}

func (s *Store) countSourceFTSMatches(ctx context.Context, query string, sourceTypes []string) (int, error) {
	ftsQuery := buildFTSQuery(query)
	sqlQuery := `
		SELECT COUNT(*)
		FROM sources_fts f
		JOIN sources s ON s.id = f.rowid
		WHERE sources_fts MATCH ?`
	args := []any{ftsQuery}
	if filter, filterArgs := sourceTypeFilter("s.source_type", sourceTypes); filter != "" {
		sqlQuery += filter
		args = append(args, filterArgs...)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, sqlQuery, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count source fts matches %q: %w", query, err)
	}
	return count, nil
}

func sourceTypeFilter(column string, sourceTypes []string) (string, []any) {
	placeholders := make([]string, 0, len(sourceTypes))
	args := make([]any, 0, len(sourceTypes))
	for _, sourceType := range sourceTypes {
		sourceType = strings.TrimSpace(sourceType)
		if sourceType == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, sourceType)
	}
	if len(placeholders) == 0 {
		return "", nil
	}
	return ` AND ` + column + ` IN (` + strings.Join(placeholders, ",") + `)`, args
}

func normalizeUserTagQuery(tag string) string {
	tag = strings.TrimSpace(strings.ToLower(tag))
	tag = strings.Trim(tag, ", ")
	return tag
}

func scanSearchResults(rows *sql.Rows) ([]model.SearchResult, error) {
	var results []model.SearchResult
	for rows.Next() {
		var result model.SearchResult
		if err := rows.Scan(&result.SourceKey, &result.SourceType, &result.ExternalID, &result.Title, &result.AuthorHandle, &result.AuthorName, &result.CanonicalURL, &result.PrimaryDomain, &result.NotePath, &result.UserTags, &result.Snippet); err != nil {
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
		part = strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(part, `"`, `""`)
		terms = append(terms, fmt.Sprintf(`"%s"*`, part))
	}
	if len(terms) == 0 {
		return `""`
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

	media, err := s.ListItemMediaRefs(ctx, item.ID)
	if err != nil {
		return model.Item{}, err
	}
	item.Media = media

	return item, nil
}

func (s *Store) GetItemByID(ctx context.Context, id int64) (model.Item, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+itemSelectColumns+` FROM items WHERE id = ?`, id)
	var item model.Item
	if err := scanItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Item{}, fmt.Errorf("item not found: %d", id)
		}
		return model.Item{}, fmt.Errorf("load item %d: %w", id, err)
	}
	media, err := s.ListItemMediaRefs(ctx, item.ID)
	if err != nil {
		return model.Item{}, err
	}
	item.Media = media
	return item, nil
}

func (s *Store) SaveItemUserTags(ctx context.Context, itemID int64, tags string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `UPDATE items SET user_tags = ? WHERE id = ?`, tags, itemID); err != nil {
		return fmt.Errorf("update user_tags for item %d: %w", itemID, err)
	}
	if err = s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
		return fmt.Errorf("sync fts for item %d: %w", itemID, err)
	}
	return tx.Commit()
}

func (s *Store) ListAllItems(ctx context.Context, limit int) ([]model.Item, error) {
	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE note_path != ''
		ORDER BY id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list all items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan item row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate items: %w", err)
	}
	return items, nil
}

func (s *Store) ListAllEntityItems(ctx context.Context, limit int) ([]model.Item, error) {
	query := `
		SELECT id, source_key, source_type, canonical_url, title, author_handle, author_name, note_path
		FROM items
		WHERE note_path != ''
		ORDER BY id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entity items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := rows.Scan(
			&item.ID,
			&item.SourceKey,
			&item.SourceType,
			&item.CanonicalURL,
			&item.Title,
			&item.AuthorHandle,
			&item.AuthorName,
			&item.NotePath,
		); err != nil {
			return nil, fmt.Errorf("scan entity item row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity items: %w", err)
	}
	return items, nil
}

func (s *Store) ListItemsForXHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	return s.listItemsForXHydration(ctx, limit, force, xItemSourceTypeWhere)
}

func (s *Store) ListItemsForXQuoteHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_quote'
			AND external_id != ''`
	if !force {
		query += `
			AND (
				x_post_status = ''
				OR x_post_status = 'api_error'
				OR x_post_status = 'error'
				OR x_post_status = 'rate_limited'
				OR ` + xHydrationRepairWhere + `
			)`
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
		return nil, fmt.Errorf("list x quote hydration items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x quote hydration item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x quote hydration items: %w", err)
	}

	return items, nil
}

func (s *Store) listItemsForXHydration(ctx context.Context, limit int, force bool, sourceWhere string) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + sourceWhere + `
			AND external_id != ''`
	if !force {
		query += `
				AND ` + xHydrationCandidateWhere
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

func (s *Store) ListItemsForXMediaTranscription(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
			AND external_id != ''
			AND EXISTS (
				SELECT 1
				FROM item_media_links l
				JOIN media_assets a ON a.id = l.media_asset_id
				WHERE l.item_id = items.id
					AND a.download_status = 'downloaded'
					AND a.local_path != ''
					AND a.local_pruned_at = ''
					AND a.media_type IN ('video', 'animated_gif')
			)`
	if !force {
		query += `
			AND NOT (
				article_title = 'X Media Transcript'
				AND article_text != ''
			)`
		query += `
			AND x_media_transcript_status = ''`
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x media transcription items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x media transcription item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x media transcription items: %w", err)
	}

	return items, nil
}

func (s *Store) SaveXMediaTranscriptionState(ctx context.Context, itemID int64, status string, errorText string, at time.Time) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		atText := ""
		if !at.IsZero() {
			atText = at.UTC().Format(time.RFC3339)
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE items
			SET x_media_transcript_status = ?,
				x_media_transcript_error = ?,
				x_media_transcript_at = ?,
				updated_at = ?
			WHERE id = ?`,
			strings.TrimSpace(status),
			strings.TrimSpace(errorText),
			atText,
			time.Now().UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return struct{}{}, fmt.Errorf("save x media transcription state %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) SaveXHydration(ctx context.Context, itemID int64, hydration model.XHydration) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin hydration tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		row := tx.QueryRowContext(ctx, `
			SELECT source_type, x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, links_json
			FROM items
			WHERE id = ?`, itemID)

		var currentSourceType, currentText, currentLang, currentJSON, currentFetchedAt, currentStatus, currentError, currentLinksJSON string
		if err := row.Scan(&currentSourceType, &currentText, &currentLang, &currentJSON, &currentFetchedAt, &currentStatus, &currentError, &currentLinksJSON); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("item not found for hydration: %d", itemID)
			}
			return false, fmt.Errorf("load current hydration %d: %w", itemID, err)
		}

		if shouldPreserveDirectQuotedHydration(currentSourceType, currentStatus, currentJSON, hydration.Status, hydration.APIJSON) {
			hydration.FullText = currentText
			hydration.Language = currentLang
			hydration.APIJSON = currentJSON
			hydration.Status = currentStatus
			hydration.Error = currentError
		}

		newFetchedAt := ""
		if !hydration.FetchedAt.IsZero() {
			newFetchedAt = hydration.FetchedAt.UTC().Format(time.RFC3339)
		}

		hydrationChanged := currentText != hydration.FullText ||
			currentLang != hydration.Language ||
			currentJSON != hydration.APIJSON ||
			currentStatus != hydration.Status ||
			currentError != hydration.Error ||
			(currentFetchedAt == "" && newFetchedAt != "")

		nowText := time.Now().UTC().Format(time.RFC3339)
		if hydrationChanged {
			if _, err := tx.ExecContext(ctx, `
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
				nowText,
				itemID,
			); err != nil {
				return false, fmt.Errorf("save hydration %d: %w", itemID, err)
			}
			if err := s.invalidateLinkedXArticleSourcesTx(ctx, tx, itemID, nowText); err != nil {
				return false, err
			}
			if _, err := s.invalidateItemSummaryTx(ctx, tx, itemID, nowText); err != nil {
				return false, err
			}
		}

		mediaChanged, err := s.syncXHydrationMediaTx(ctx, tx, itemID, hydration, time.Now().UTC())
		if err != nil {
			return false, err
		}
		if mediaChanged {
			if _, err := s.invalidateItemOCRTx(ctx, tx, itemID, nowText); err != nil {
				return false, err
			}
		}
		if mediaChanged && !hydrationChanged {
			if _, err := tx.ExecContext(ctx, `
				UPDATE items
				SET updated_at = ?
				WHERE id = ?`,
				nowText,
				itemID,
			); err != nil {
				return false, fmt.Errorf("touch item after media sync %d: %w", itemID, err)
			}
		}
		linksChanged, err := syncXHydrationLinksTx(ctx, tx, itemID, currentLinksJSON, hydration.APIJSON, nowText)
		if err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit hydration %d: %w", itemID, err)
		}

		return hydrationChanged || mediaChanged || linksChanged, nil
	})
}

func shouldPreserveDirectQuotedHydration(sourceType, currentStatus, currentJSON, newStatus, newJSON string) bool {
	return sourceType == "x_quote" &&
		currentStatus == "ok_graphql" &&
		newStatus == "ok_graphql" &&
		strings.Contains(currentJSON, `"tweetResult"`) &&
		!strings.Contains(newJSON, `"tweetResult"`)
}

func syncXHydrationLinksTx(ctx context.Context, tx *sql.Tx, itemID int64, currentLinksJSON string, apiJSON string, nowText string) (bool, error) {
	snapshot, ok, err := xpost.DecodeSnapshot(apiJSON)
	if err != nil {
		return false, fmt.Errorf("decode hydration snapshot links %d: %w", itemID, err)
	}
	if !ok || len(snapshot.Links) == 0 {
		return false, nil
	}

	links := uniqueNonEmptyStrings(snapshot.Links)
	if len(links) == 0 {
		return false, nil
	}
	linksJSONBytes, err := json.Marshal(links)
	if err != nil {
		return false, fmt.Errorf("marshal hydration links %d: %w", itemID, err)
	}
	linksJSON := string(linksJSONBytes)
	if currentLinksJSON == linksJSON {
		return false, nil
	}

	primaryDomain, domains, githubURLs := deriveItemLinkMetadata(links)
	if _, err := tx.ExecContext(ctx, `
		UPDATE items
		SET links_json = ?,
			primary_domain = ?,
			domains = ?,
			github_urls = ?,
			link_extract_synced_at = '',
			updated_at = ?
		WHERE id = ?`,
		linksJSON,
		primaryDomain,
		strings.Join(domains, ","),
		strings.Join(githubURLs, ","),
		nowText,
		itemID,
	); err != nil {
		return false, fmt.Errorf("sync hydration links %d: %w", itemID, err)
	}

	return true, nil
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func deriveItemLinkMetadata(links []string) (string, []string, []string) {
	domains := make([]string, 0, len(links))
	githubURLs := make([]string, 0)
	seenDomains := map[string]struct{}{}
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if _, ok := seenDomains[host]; !ok {
			seenDomains[host] = struct{}{}
			domains = append(domains, host)
		}
		if host == "github.com" {
			githubURLs = append(githubURLs, link)
		}
	}
	primary := ""
	if len(domains) > 0 {
		primary = domains[0]
	}
	return primary, domains, githubURLs
}

func (s *Store) invalidateLinkedXArticleSourcesTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources
		SET extracted_text = '',
			extract_json = '',
			extract_status = '',
			extract_error = '',
			extract_failure_kind = '',
			extract_failure_count = 0,
			extract_first_failed_at = '',
			extract_last_failed_at = '',
			extracted_at = '',
			extract_tool = '',
			extract_tool_version = '',
			summary_text = '',
			summary_json = '',
			summary_status = '',
			summary_error = '',
			summary_model = '',
			summary_content_hash = '',
			summary_prompt_version = '',
			summary_tool = '',
			summary_tool_version = '',
			summarized_at = '',
			content_hash = '',
			updated_at = ?
		WHERE id IN (
			SELECT l.source_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			WHERE l.item_id = ?
				AND s.source_type = 'x_article'
		)`,
		nowText,
		itemID,
	); err != nil {
		return fmt.Errorf("invalidate linked x article sources for item %d: %w", itemID, err)
	}
	return nil
}

func scanItem(scanner interface{ Scan(dest ...any) error }, item *model.Item) error {
	var importedAt, updatedAt, lastSeenAt string
	var xPostFetchedAt string
	var linkExtractSyncedAt string
	var summarizedAt string
	var ocrAt string
	var xMediaTranscriptAt string
	if err := scanner.Scan(
		&item.ID, &item.SourceKey, &item.SourceType, &item.ExternalID, &item.CanonicalURL, &item.Title, &item.AuthorHandle, &item.AuthorName,
		&item.PublishedAt, &item.SavedAt, &item.SyncedAt, &item.Language, &item.Text, &item.ArticleTitle, &item.ArticleText,
		&item.PrimaryCategory, &item.PrimaryDomain, &item.LinksJSON, &item.Categories, &item.Domains, &item.GitHubURLs, &item.FolderNames,
		&item.LikeCount, &item.RepostCount, &item.ReplyCount, &item.QuoteCount, &item.BookmarkCount,
		&item.ContentHash, &item.NotePath, &item.RawJSON, &importedAt, &updatedAt, &lastSeenAt,
		&item.XPostText, &item.XPostLang, &item.XPostJSON, &xPostFetchedAt, &item.XPostStatus, &item.XPostError,
		&linkExtractSyncedAt,
		&item.SummaryText, &item.SummaryJSON, &item.SummaryStatus, &item.SummaryError, &item.SummaryModel,
		&item.SummaryPromptVersion, &item.SummaryTool, &item.SummaryToolVersion, &item.SummaryInputHash, &summarizedAt,
		&item.OCRText, &item.OCRJSON, &item.OCRStatus, &item.OCRError, &item.OCRModel, &item.OCRTool, &item.OCRToolVersion, &item.OCRInputHash, &ocrAt,
		&item.XMediaTranscriptStatus, &item.XMediaTranscriptError, &xMediaTranscriptAt,
		&item.UserTags,
	); err != nil {
		return err
	}

	item.ImportedAt = parseStoredTime(importedAt)
	item.UpdatedAt = parseStoredTime(updatedAt)
	item.LastSeenAt = parseStoredTime(lastSeenAt)
	item.XPostFetchedAt = parseStoredTime(xPostFetchedAt)
	item.LinkExtractSyncedAt = parseStoredTime(linkExtractSyncedAt)
	item.SummarizedAt = parseStoredTime(summarizedAt)
	item.OCRAt = parseStoredTime(ocrAt)
	item.XMediaTranscriptAt = parseStoredTime(xMediaTranscriptAt)
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
