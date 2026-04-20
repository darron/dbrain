package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbrain/internal/model"
)

const sourceSelectColumns = `
	id, source_key, canonical_url, normalized_url, source_type, domain, title, description, site_name,
	extracted_text, extract_json, extract_status, extract_error, extracted_at,
	extract_tool, extract_tool_version,
	summary_text, summary_json, summary_status, summary_error, summary_model, summary_content_hash, summary_prompt_version,
	summary_tool, summary_tool_version, summarized_at,
	content_hash, note_path, created_at, updated_at`

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
	existing, err := s.tableColumns("sources")
	if err != nil {
		return fmt.Errorf("load source table info: %w", err)
	}

	required := map[string]string{
		"domain":                 "TEXT NOT NULL DEFAULT ''",
		"description":            "TEXT NOT NULL DEFAULT ''",
		"site_name":              "TEXT NOT NULL DEFAULT ''",
		"extracted_text":         "TEXT NOT NULL DEFAULT ''",
		"extract_json":           "TEXT NOT NULL DEFAULT ''",
		"extract_status":         "TEXT NOT NULL DEFAULT ''",
		"extract_error":          "TEXT NOT NULL DEFAULT ''",
		"extracted_at":           "TEXT NOT NULL DEFAULT ''",
		"extract_tool":           "TEXT NOT NULL DEFAULT ''",
		"extract_tool_version":   "TEXT NOT NULL DEFAULT ''",
		"summary_text":           "TEXT NOT NULL DEFAULT ''",
		"summary_json":           "TEXT NOT NULL DEFAULT ''",
		"summary_status":         "TEXT NOT NULL DEFAULT ''",
		"summary_error":          "TEXT NOT NULL DEFAULT ''",
		"summary_model":          "TEXT NOT NULL DEFAULT ''",
		"summary_content_hash":   "TEXT NOT NULL DEFAULT ''",
		"summary_prompt_version": "TEXT NOT NULL DEFAULT ''",
		"summary_tool":           "TEXT NOT NULL DEFAULT ''",
		"summary_tool_version":   "TEXT NOT NULL DEFAULT ''",
		"summarized_at":          "TEXT NOT NULL DEFAULT ''",
		"content_hash":           "TEXT NOT NULL DEFAULT ''",
		"note_path":              "TEXT NOT NULL DEFAULT ''",
	}

	for name, definition := range required {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE sources ADD COLUMN %s %s", name, definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add sources.%s: %w", name, err)
		}
	}

	return nil
}

func (s *Store) ensureSourceSummaryVersionColumns() error {
	existing, err := s.tableColumns("source_summary_versions")
	if err != nil {
		return fmt.Errorf("load source summary version table info: %w", err)
	}

	required := map[string]string{
		"summary_tool":         "TEXT NOT NULL DEFAULT ''",
		"summary_tool_version": "TEXT NOT NULL DEFAULT ''",
	}

	for name, definition := range required {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE source_summary_versions ADD COLUMN %s %s", name, definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add source_summary_versions.%s: %w", name, err)
		}
	}

	return nil
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

func (s *Store) ListItemsForLinkDiscovery(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 250
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_bookmark'
			AND links_json != '[]'`
	if !force {
		query += `
			AND (link_extract_synced_at = '' OR updated_at > link_extract_synced_at)`
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list items for link discovery: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan link discovery item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate link discovery items: %w", err)
	}

	return items, nil
}

func (s *Store) MarkItemLinkDiscovery(ctx context.Context, itemID int64, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE items
		SET link_extract_synced_at = ?
		WHERE id = ?`,
		at.UTC().Format(time.RFC3339),
		itemID,
	); err != nil {
		return fmt.Errorf("mark item link discovery %d: %w", itemID, err)
	}
	return nil
}

func (s *Store) UpsertSourceLink(ctx context.Context, itemID int64, candidate model.SourceCandidate) (model.SourceLinkUpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.SourceLinkUpsertResult{}, fmt.Errorf("begin source link tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC().Format(time.RFC3339)
	var sourceID int64
	var sourceCreated bool

	row := tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE normalized_url = ?`, candidate.NormalizedURL)
	switch scanErr := row.Scan(&sourceID); {
	case errors.Is(scanErr, sql.ErrNoRows):
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO sources (
				source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			candidate.SourceKey,
			candidate.CanonicalURL,
			candidate.NormalizedURL,
			candidate.SourceType,
			candidate.Domain,
			candidate.NotePath,
			now,
			now,
		)
		if execErr != nil {
			err = fmt.Errorf("insert source candidate %s: %w", candidate.CanonicalURL, execErr)
			return model.SourceLinkUpsertResult{}, err
		}
		sourceID, execErr = result.LastInsertId()
		if execErr != nil {
			err = fmt.Errorf("last insert id source %s: %w", candidate.CanonicalURL, execErr)
			return model.SourceLinkUpsertResult{}, err
		}
		sourceCreated = true
	case scanErr != nil:
		err = fmt.Errorf("lookup source candidate %s: %w", candidate.CanonicalURL, scanErr)
		return model.SourceLinkUpsertResult{}, err
	default:
		if _, execErr := tx.ExecContext(ctx, `
			UPDATE sources
			SET canonical_url = ?,
				source_type = ?,
				domain = ?,
				note_path = ?,
				updated_at = ?
			WHERE id = ?`,
			candidate.CanonicalURL,
			candidate.SourceType,
			candidate.Domain,
			candidate.NotePath,
			now,
			sourceID,
		); execErr != nil {
			err = fmt.Errorf("update source candidate %s: %w", candidate.CanonicalURL, execErr)
			return model.SourceLinkUpsertResult{}, err
		}
	}

	linkResult, execErr := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID,
		sourceID,
		candidate.OriginalURL,
		now,
	)
	if execErr != nil {
		err = fmt.Errorf("link item %d to source %d: %w", itemID, sourceID, execErr)
		return model.SourceLinkUpsertResult{}, err
	}
	rowsAffected, _ := linkResult.RowsAffected()

	if commitErr := tx.Commit(); commitErr != nil {
		return model.SourceLinkUpsertResult{}, fmt.Errorf("commit source link: %w", commitErr)
	}

	return model.SourceLinkUpsertResult{
		SourceID:      sourceID,
		SourceCreated: sourceCreated,
		LinkCreated:   rowsAffected > 0,
	}, nil
}

func (s *Store) ListSourcesForEnrichment(ctx context.Context, limit int, force bool, summarize bool, promptVersion string, toolName string, toolVersion string) ([]model.SourceDocument, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT ` + sourceSelectColumns + `
		FROM sources
		WHERE 1 = 1`
	args := make([]any, 0, 2)

	if !force {
		if summarize {
			summaryStale := []string{
				"summary_status = ''",
				"summary_status = 'error'",
				"summary_content_hash != content_hash",
				"summary_prompt_version != ?",
			}
			args = append(args, promptVersion)
			if toolName != "" {
				summaryStale = append(summaryStale, "summary_tool != ?")
				args = append(args, toolName)
			}
			if toolVersion != "" {
				summaryStale = append(summaryStale, "summary_tool_version != ?")
				args = append(args, toolVersion)
			}

			query += `
				AND (
					(extract_status = '' OR extract_status = 'error')
					OR (
						extract_status IN ('ok', 'empty')
						AND (` + strings.Join(summaryStale, ` OR `) + `)
					)
				)`
		} else {
			query += `
				AND (extract_status = '' OR extract_status = 'error')`
		}
	}

	query += `
		ORDER BY
			CASE WHEN extract_status = '' THEN 0 WHEN extract_status = 'error' THEN 1 ELSE 2 END,
			extracted_at ASC,
			id DESC
		LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sources for enrichment: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan source enrichment row: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source enrichment rows: %w", err)
	}

	return sources, nil
}

func (s *Store) SaveSourceExtraction(ctx context.Context, sourceID int64, result model.ExtractResult, contentHash string) (bool, error) {
	current, err := s.GetSourceByID(ctx, sourceID)
	if err != nil {
		return false, err
	}

	if result.Status == "error" {
		changed := current.ExtractStatus != result.Status ||
			current.ExtractError != result.Error ||
			current.ExtractTool != result.Tool ||
			current.ExtractToolVersion != result.ToolVersion
		if !changed {
			return false, nil
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE sources
			SET extract_status = ?,
				extract_error = ?,
				extract_tool = ?,
				extract_tool_version = ?,
				updated_at = ?
			WHERE id = ?`,
			result.Status,
			result.Error,
			result.Tool,
			result.ToolVersion,
			time.Now().UTC().Format(time.RFC3339),
			sourceID,
		); err != nil {
			return false, fmt.Errorf("save source extraction error %d: %w", sourceID, err)
		}
		return true, nil
	}

	fetchedAt := ""
	if !result.FetchedAt.IsZero() {
		fetchedAt = result.FetchedAt.UTC().Format(time.RFC3339)
	}
	canonicalURL := current.CanonicalURL
	if result.FinalURL != "" {
		canonicalURL = result.FinalURL
	}

	changed := current.CanonicalURL != canonicalURL ||
		current.Title != result.Title ||
		current.Description != result.Description ||
		current.SiteName != result.SiteName ||
		current.ExtractedText != result.Content ||
		current.ExtractJSON != result.RawJSON ||
		current.ExtractStatus != result.Status ||
		current.ExtractError != result.Error ||
		current.ExtractTool != result.Tool ||
		current.ExtractToolVersion != result.ToolVersion ||
		current.ContentHash != contentHash ||
		current.ExtractedAt.UTC().Format(time.RFC3339) != fetchedAt

	if !changed {
		return false, nil
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE sources
		SET canonical_url = ?,
			title = ?,
			description = ?,
			site_name = ?,
			extracted_text = ?,
			extract_json = ?,
			extract_status = ?,
			extract_error = ?,
			extracted_at = ?,
			extract_tool = ?,
			extract_tool_version = ?,
			content_hash = ?,
			updated_at = ?
		WHERE id = ?`,
		canonicalURL,
		result.Title,
		result.Description,
		result.SiteName,
		result.Content,
		result.RawJSON,
		result.Status,
		result.Error,
		fetchedAt,
		result.Tool,
		result.ToolVersion,
		contentHash,
		time.Now().UTC().Format(time.RFC3339),
		sourceID,
	); err != nil {
		return false, fmt.Errorf("save source extraction %d: %w", sourceID, err)
	}

	if err := s.syncSourceFTS(ctx, sourceID); err != nil {
		return false, err
	}

	return true, nil
}

func (s *Store) GetPreferredLocalSourceExtract(ctx context.Context, sourceID int64) (model.ExtractResult, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			s.canonical_url,
			s.domain,
			COALESCE(NULLIF(i.article_title, ''), s.title, ''),
			i.article_text,
			i.updated_at
		FROM item_source_links l
		JOIN items i ON i.id = l.item_id
		JOIN sources s ON s.id = l.source_id
		WHERE l.source_id = ?
			AND i.article_text != ''
		ORDER BY length(i.article_text) DESC, i.last_seen_at DESC, i.id DESC
		LIMIT 1`, sourceID)

	var canonicalURL string
	var domain string
	var title string
	var content string
	var updatedAt string
	if err := row.Scan(&canonicalURL, &domain, &title, &content, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ExtractResult{}, false, nil
		}
		return model.ExtractResult{}, false, fmt.Errorf("load local source extract %d: %w", sourceID, err)
	}

	result := model.ExtractResult{
		CanonicalURL: canonicalURL,
		FinalURL:     canonicalURL,
		Title:        title,
		SiteName:     domain,
		Content:      strings.TrimSpace(content),
		Status:       "ok",
		FetchedAt:    parseStoredTime(updatedAt),
		Tool:         "ft-bookmarks",
		ToolVersion:  "local-item-cache",
	}
	if strings.TrimSpace(result.Content) == "" {
		return model.ExtractResult{}, false, nil
	}
	if result.FetchedAt.IsZero() {
		result.FetchedAt = time.Now().UTC()
	}

	return result, true, nil
}

func (s *Store) SaveSourceSummary(ctx context.Context, sourceID int64, result model.SummaryResult) (bool, error) {
	current, err := s.GetSourceByID(ctx, sourceID)
	if err != nil {
		return false, err
	}

	if result.Status == "error" {
		changed := current.SummaryStatus != result.Status ||
			current.SummaryError != result.Error ||
			current.SummaryTool != result.Tool ||
			current.SummaryToolVersion != result.ToolVersion
		if !changed {
			return false, nil
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE sources
			SET summary_status = ?,
				summary_error = ?,
				summary_tool = ?,
				summary_tool_version = ?,
				updated_at = ?
			WHERE id = ?`,
			result.Status,
			result.Error,
			result.Tool,
			result.ToolVersion,
			time.Now().UTC().Format(time.RFC3339),
			sourceID,
		); err != nil {
			return false, fmt.Errorf("save source summary error %d: %w", sourceID, err)
		}
		return true, nil
	}

	summarizedAt := ""
	if !result.FetchedAt.IsZero() {
		summarizedAt = result.FetchedAt.UTC().Format(time.RFC3339)
	}

	changed := current.SummaryText != result.Text ||
		current.SummaryJSON != result.RawJSON ||
		current.SummaryStatus != result.Status ||
		current.SummaryError != result.Error ||
		current.SummaryModel != result.Model ||
		current.SummaryContentHash != current.ContentHash ||
		current.SummaryPromptVersion != result.PromptVersion ||
		current.SummaryTool != result.Tool ||
		current.SummaryToolVersion != result.ToolVersion ||
		current.SummarizedAt.UTC().Format(time.RFC3339) != summarizedAt

	if !changed {
		return false, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin source summary tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `
		UPDATE sources
		SET summary_text = ?,
			summary_json = ?,
			summary_status = ?,
			summary_error = ?,
			summary_model = ?,
			summary_content_hash = ?,
			summary_prompt_version = ?,
			summary_tool = ?,
			summary_tool_version = ?,
			summarized_at = ?,
			updated_at = ?
		WHERE id = ?`,
		result.Text,
		result.RawJSON,
		result.Status,
		result.Error,
		result.Model,
		current.ContentHash,
		result.PromptVersion,
		result.Tool,
		result.ToolVersion,
		summarizedAt,
		time.Now().UTC().Format(time.RFC3339),
		sourceID,
	); err != nil {
		return false, fmt.Errorf("update source summary %d: %w", sourceID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO source_summary_versions (
			source_id, content_hash, summary_text, summary_json, summary_status, summary_error,
			summary_model, summary_prompt_version, summary_tool, summary_tool_version, summarized_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sourceID,
		current.ContentHash,
		result.Text,
		result.RawJSON,
		result.Status,
		result.Error,
		result.Model,
		result.PromptVersion,
		result.Tool,
		result.ToolVersion,
		summarizedAt,
	); err != nil {
		return false, fmt.Errorf("insert source summary version %d: %w", sourceID, err)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return false, fmt.Errorf("commit source summary %d: %w", sourceID, commitErr)
	}

	if err := s.syncSourceFTS(ctx, sourceID); err != nil {
		return false, err
	}

	return true, nil
}

func (s *Store) GetSourceByID(ctx context.Context, sourceID int64) (model.SourceDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+sourceSelectColumns+`
		FROM sources
		WHERE id = ?`, sourceID)

	var source model.SourceDocument
	if err := scanSource(row, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SourceDocument{}, fmt.Errorf("source not found: %d", sourceID)
		}
		return model.SourceDocument{}, fmt.Errorf("load source %d: %w", sourceID, err)
	}
	return source, nil
}

func (s *Store) GetSource(ctx context.Context, lookup string) (model.SourceDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+sourceSelectColumns+`
		FROM sources
		WHERE source_key = ?
			OR canonical_url = ?
			OR normalized_url = ?
			OR note_path = ?
		LIMIT 1`, lookup, lookup, lookup, lookup)

	var source model.SourceDocument
	if err := scanSource(row, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SourceDocument{}, fmt.Errorf("source not found: %s", lookup)
		}
		return model.SourceDocument{}, fmt.Errorf("load source %s: %w", lookup, err)
	}
	return source, nil
}

func (s *Store) ListBacklinksForSource(ctx context.Context, sourceID int64) ([]model.SourceBacklink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.source_key, i.canonical_url, i.title, i.note_path, i.author_handle, i.author_name, i.published_at
		FROM item_source_links l
		JOIN items i ON i.id = l.item_id
		WHERE l.source_id = ?
		ORDER BY i.last_seen_at DESC, i.id DESC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list source backlinks %d: %w", sourceID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.SourceBacklink
	for rows.Next() {
		var ref model.SourceBacklink
		if err := rows.Scan(&ref.ItemID, &ref.SourceKey, &ref.CanonicalURL, &ref.Title, &ref.NotePath, &ref.AuthorHandle, &ref.AuthorName, &ref.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan source backlink %d: %w", sourceID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source backlinks %d: %w", sourceID, err)
	}
	return refs, nil
}

func scanSource(scanner interface{ Scan(dest ...any) error }, source *model.SourceDocument) error {
	var extractedAt, summarizedAt, createdAt, updatedAt string
	if err := scanner.Scan(
		&source.ID,
		&source.SourceKey,
		&source.CanonicalURL,
		&source.NormalizedURL,
		&source.SourceType,
		&source.Domain,
		&source.Title,
		&source.Description,
		&source.SiteName,
		&source.ExtractedText,
		&source.ExtractJSON,
		&source.ExtractStatus,
		&source.ExtractError,
		&extractedAt,
		&source.ExtractTool,
		&source.ExtractToolVersion,
		&source.SummaryText,
		&source.SummaryJSON,
		&source.SummaryStatus,
		&source.SummaryError,
		&source.SummaryModel,
		&source.SummaryContentHash,
		&source.SummaryPromptVersion,
		&source.SummaryTool,
		&source.SummaryToolVersion,
		&summarizedAt,
		&source.ContentHash,
		&source.NotePath,
		&createdAt,
		&updatedAt,
	); err != nil {
		return err
	}

	source.ExtractedAt = parseStoredTime(extractedAt)
	source.SummarizedAt = parseStoredTime(summarizedAt)
	source.CreatedAt = parseStoredTime(createdAt)
	source.UpdatedAt = parseStoredTime(updatedAt)
	return nil
}

func (s *Store) syncSourceFTS(ctx context.Context, sourceID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sources_fts WHERE rowid = ?`, sourceID); err != nil {
		return nil
	}

	source, err := s.GetSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sources_fts (
			rowid, source_key, title, description, site_name, extracted_text, summary_text, domain
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sourceID,
		source.SourceKey,
		source.Title,
		source.Description,
		source.SiteName,
		source.ExtractedText,
		source.SummaryText,
		source.Domain,
	); err != nil {
		return nil
	}
	return nil
}

func (s *Store) searchSourcesFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	ftsQuery := buildFTSQuery(query)
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			s.source_key,
			'' AS external_id,
			s.title,
			'' AS author_handle,
			'' AS author_name,
			s.canonical_url,
			s.domain,
			s.note_path,
			substr(trim(replace(COALESCE(NULLIF(s.summary_text, ''), s.extracted_text), char(10), ' ')), 1, 200) AS snippet
		FROM sources_fts f
		JOIN sources s ON s.id = f.rowid
		WHERE sources_fts MATCH ?
		ORDER BY bm25(sources_fts), s.updated_at DESC
		LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("source fts search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}

func (s *Store) searchSourcesLike(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	like := "%" + strings.TrimSpace(query) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			source_key,
			'' AS external_id,
			title,
			'' AS author_handle,
			'' AS author_name,
			canonical_url,
			domain,
			note_path,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), extracted_text), char(10), ' ')), 1, 200) AS snippet
		FROM sources
		WHERE title LIKE ?
			OR description LIKE ?
			OR site_name LIKE ?
			OR extracted_text LIKE ?
			OR summary_text LIKE ?
			OR domain LIKE ?
		ORDER BY updated_at DESC
		LIMIT ?`, like, like, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("source like search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}
