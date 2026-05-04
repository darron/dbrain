package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
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

func (s *Store) ListItemsForLinkDiscovery(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 250
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + linkDiscoveryItemSourceTypeWhere + `
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
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE items
			SET link_extract_synced_at = ?
			WHERE id = ?`,
			at.UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return struct{}{}, fmt.Errorf("mark item link discovery %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) UpsertSourceLink(ctx context.Context, itemID int64, candidate model.SourceCandidate) (model.SourceLinkUpsertResult, error) {
	return withBusyRetry(ctx, func() (model.SourceLinkUpsertResult, error) {
		return s.upsertSourceLink(ctx, itemID, candidate)
	})
}

func (s *Store) UpsertSource(ctx context.Context, candidate model.SourceCandidate) (model.SourceUpsertResult, error) {
	return withBusyRetry(ctx, func() (model.SourceUpsertResult, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return model.SourceUpsertResult{}, fmt.Errorf("begin source tx: %w", err)
		}
		defer func() {
			if err != nil {
				_ = tx.Rollback()
			}
		}()

		sourceID, sourceCreated, err := upsertSourceCandidate(ctx, tx, candidate)
		if err != nil {
			return model.SourceUpsertResult{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return model.SourceUpsertResult{}, fmt.Errorf("commit source: %w", commitErr)
		}
		return model.SourceUpsertResult{SourceID: sourceID, SourceCreated: sourceCreated}, nil
	})
}

func (s *Store) upsertSourceLink(ctx context.Context, itemID int64, candidate model.SourceCandidate) (model.SourceLinkUpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.SourceLinkUpsertResult{}, fmt.Errorf("begin source link tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	sourceID, sourceCreated, err := upsertSourceCandidate(ctx, tx, candidate)
	if err != nil {
		return model.SourceLinkUpsertResult{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
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

func upsertSourceCandidate(ctx context.Context, tx *sql.Tx, candidate model.SourceCandidate) (int64, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var sourceID int64

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
			return 0, false, fmt.Errorf("insert source candidate %s: %w", candidate.CanonicalURL, execErr)
		}
		sourceID, execErr = result.LastInsertId()
		if execErr != nil {
			return 0, false, fmt.Errorf("last insert id source %s: %w", candidate.CanonicalURL, execErr)
		}
		return sourceID, true, nil
	case scanErr != nil:
		return 0, false, fmt.Errorf("lookup source candidate %s: %w", candidate.CanonicalURL, scanErr)
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
			return 0, false, fmt.Errorf("update source candidate %s: %w", candidate.CanonicalURL, execErr)
		}
		return sourceID, false, nil
	}
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
		errorEligible, errorArgs := sourceExtractBacklogWhere(time.Now().UTC())
		if summarize {
			args = append(args, errorArgs...)
			summaryStaleWhere, summaryArgs := sourceSummaryStaleWhere(promptVersion, toolName, toolVersion)
			args = append(args, summaryArgs...)

			query += `
				AND (
					` + errorEligible + `
					OR (
						extract_status IN ('ok', 'empty')
						AND ` + summaryStaleWhere + `
					)
				)`
		} else {
			args = append(args, errorArgs...)
			query += `
				AND ` + errorEligible
		}
	}

	query += `
		ORDER BY
			CASE WHEN extract_status = '' THEN 0 WHEN extract_status = 'error' THEN 1 ELSE 2 END,
			CASE WHEN extract_status = 'error' THEN extract_failure_count ELSE 0 END ASC,
			extract_last_failed_at ASC,
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
	return withBusyRetry(ctx, func() (bool, error) {
		current, err := s.GetSourceByID(ctx, sourceID)
		if err != nil {
			return false, err
		}

		if isExtractFailureStatus(result.Status) {
			now := time.Now().UTC()
			failureKind, failureCount, firstFailedAt, lastFailedAt := nextExtractFailureState(current, result.Status, result.Error, now)
			changed := current.ExtractStatus != result.Status ||
				current.ExtractError != result.Error ||
				current.ExtractFailureKind != failureKind ||
				current.ExtractFailureCount != failureCount ||
				storedTimeString(current.ExtractFirstFailedAt) != firstFailedAt ||
				storedTimeString(current.ExtractLastFailedAt) != lastFailedAt ||
				current.ExtractTool != result.Tool ||
				current.ExtractToolVersion != result.ToolVersion
			if !changed {
				return false, nil
			}
			if _, err := s.db.ExecContext(ctx, `
				UPDATE sources
				SET extract_status = ?,
					extract_error = ?,
					extract_failure_kind = ?,
					extract_failure_count = ?,
					extract_first_failed_at = ?,
					extract_last_failed_at = ?,
					extract_tool = ?,
					extract_tool_version = ?,
					updated_at = ?
				WHERE id = ?`,
				result.Status,
				result.Error,
				failureKind,
				failureCount,
				firstFailedAt,
				lastFailedAt,
				result.Tool,
				result.ToolVersion,
				now.Format(time.RFC3339),
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

		failureKind := ""
		failureCount := 0
		firstFailedAt := ""
		lastFailedAt := ""

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
				extract_failure_kind = ?,
				extract_failure_count = ?,
				extract_first_failed_at = ?,
				extract_last_failed_at = ?,
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
			failureKind,
			failureCount,
			firstFailedAt,
			lastFailedAt,
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
	})
}

func (s *Store) GetPreferredLocalSourceExtract(ctx context.Context, sourceID int64) (model.ExtractResult, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH local_candidates AS (
			SELECT
				s.canonical_url AS canonical_url,
				s.domain AS domain,
				s.source_type AS source_type,
				COALESCE(NULLIF(i.article_title, ''), s.title, '') AS title,
				CASE
					WHEN i.article_title = 'X Media Transcript' THEN ''
					WHEN i.source_type = 'apple_note' THEN ''
					ELSE i.article_text
				END AS article_text,
				i.author_handle AS author_handle,
				i.x_post_json AS x_post_json,
				i.updated_at AS updated_at,
				COALESCE(i.last_seen_at, i.updated_at, '') AS sort_time,
				0 AS provider_priority,
				i.id AS item_id
			FROM item_source_links l
			JOIN items i ON i.id = l.item_id
			JOIN sources s ON s.id = l.source_id
			WHERE l.source_id = ?

			UNION ALL

			SELECT
				s.canonical_url AS canonical_url,
				s.domain AS domain,
				s.source_type AS source_type,
				COALESCE(NULLIF(p.article_title, ''), NULLIF(i.article_title, ''), s.title, '') AS title,
				COALESCE(
					NULLIF(CASE WHEN p.article_title = 'X Media Transcript' THEN '' ELSE p.article_text END, ''),
					CASE
						WHEN i.article_title = 'X Media Transcript' THEN ''
						WHEN i.source_type = 'apple_note' THEN ''
						ELSE i.article_text
					END,
					''
				) AS article_text,
				p.author_handle AS author_handle,
				p.x_post_json AS x_post_json,
				p.updated_at AS updated_at,
				COALESCE(p.last_seen_at, p.updated_at, '') AS sort_time,
				1 AS provider_priority,
				p.id AS item_id
			FROM item_source_links l
			JOIN items i ON i.id = l.item_id
			JOIN item_item_links q ON q.child_item_id = i.id AND q.link_kind = 'quoted_post'
			JOIN items p ON p.id = q.parent_item_id
			JOIN sources s ON s.id = l.source_id
			WHERE l.source_id = ?
		)
		SELECT
			canonical_url,
			domain,
			source_type,
			title,
			article_text,
			author_handle,
			x_post_json,
			updated_at
		FROM local_candidates
		ORDER BY sort_time DESC, provider_priority ASC, item_id DESC`, sourceID, sourceID)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("load local source extract %d: %w", sourceID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var best model.ExtractResult
	bestRank := -1
	bestContentLen := -1

	for rows.Next() {
		var canonicalURL string
		var domain string
		var sourceType string
		var title string
		var articleText string
		var authorHandle string
		var xPostJSON string
		var updatedAt string
		if err := rows.Scan(&canonicalURL, &domain, &sourceType, &title, &articleText, &authorHandle, &xPostJSON, &updatedAt); err != nil {
			return model.ExtractResult{}, false, fmt.Errorf("scan local source extract %d: %w", sourceID, err)
		}

		var candidate model.ExtractResult
		candidateRank := -1
		if content := strings.TrimSpace(articleText); content != "" {
			candidate = model.ExtractResult{
				CanonicalURL: canonicalURL,
				FinalURL:     canonicalURL,
				Title:        title,
				SiteName:     domain,
				Content:      content,
				Status:       "ok",
				FetchedAt:    parseStoredTime(updatedAt),
				Tool:         "item-cache",
				ToolVersion:  "local-item-cache",
			}
			candidateRank = 2
		} else if sourceType == "x_article" {
			if preview, ok := parseXArticlePreview(xPostJSON, canonicalURL); ok {
				finalURL := canonicalURL
				if value := buildXArticlePublicURL(authorHandle, preview.RestID); value != "" {
					finalURL = value
				}
				toolVersion := "local-article-preview-cache"
				candidateRank = 1
				if preview.HasFullText {
					toolVersion = "local-article-body-cache"
					candidateRank = 2
				}
				candidate = model.ExtractResult{
					CanonicalURL: canonicalURL,
					FinalURL:     finalURL,
					Title:        firstNonEmpty(preview.Title, title),
					SiteName:     firstNonEmpty(domain, "x.com"),
					Content:      preview.Content,
					Status:       "ok",
					FetchedAt:    parseStoredTime(updatedAt),
					Tool:         "x-hydration",
					ToolVersion:  toolVersion,
				}
			}
		}

		if candidateRank < 0 || strings.TrimSpace(candidate.Content) == "" {
			continue
		}
		contentLen := len(candidate.Content)
		if candidateRank > bestRank || (candidateRank == bestRank && contentLen > bestContentLen) {
			best = candidate
			bestRank = candidateRank
			bestContentLen = contentLen
		}
	}
	if err := rows.Err(); err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("iterate local source extract %d: %w", sourceID, err)
	}
	if bestRank < 0 || strings.TrimSpace(best.Content) == "" {
		return model.ExtractResult{}, false, nil
	}
	if best.FetchedAt.IsZero() {
		best.FetchedAt = time.Now().UTC()
	}

	return best, true, nil
}

func (s *Store) SaveSourceSummary(ctx context.Context, sourceID int64, result model.SummaryResult) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
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
	})
}
