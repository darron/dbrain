package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) syncSourceFTS(ctx context.Context, sourceID int64) error {
	return s.syncSourceFTSByIDTx(ctx, nil, sourceID)
}

func (s *Store) syncSourceFTSByIDTx(ctx context.Context, tx *sql.Tx, sourceID int64) error {
	exec := func(query string, args ...any) (sql.Result, error) {
		if tx != nil {
			return tx.ExecContext(ctx, query, args...)
		}
		return s.db.ExecContext(ctx, query, args...)
	}
	queryRow := func(query string, args ...any) *sql.Row {
		if tx != nil {
			return tx.QueryRowContext(ctx, query, args...)
		}
		return s.db.QueryRowContext(ctx, query, args...)
	}

	if _, err := exec(`DELETE FROM sources_fts WHERE rowid = ?`, sourceID); err != nil {
		return fmt.Errorf("delete source fts %d: %w", sourceID, err)
	}

	var source model.SourceDocument
	if err := scanSource(queryRow(`SELECT `+sourceSelectColumns+` FROM sources WHERE id = ?`, sourceID), &source); err != nil {
		return err
	}

	if _, err := exec(`
		INSERT INTO sources_fts (
			rowid, source_key, title, description, site_name, extracted_text, summary_text, domain
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sourceID,
		source.SourceKey,
		source.Title,
		source.Description,
		source.SiteName,
		source.ExtractedText,
		indexedSourceSummaryText(source),
		source.Domain,
	); err != nil {
		return fmt.Errorf("insert source fts %d: %w", sourceID, err)
	}
	return nil
}

func indexedSourceSummaryText(source model.SourceDocument) string {
	parts := make([]string, 0, 2)
	for _, value := range []string{strings.TrimSpace(source.SummaryText), strings.TrimSpace(source.UserTags)} {
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n\n")
}

func (s *Store) SearchSources(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if s.hasFTS {
		results, err := s.searchSourcesFTS(ctx, query, limit)
		if err == nil {
			return results, nil
		}
	}
	return s.searchSourcesLike(ctx, query, limit)
}

func (s *Store) searchSourcesFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	return s.searchSourcesFTSQuery(ctx, buildFTSQuery(query), limit)
}

func (s *Store) searchSourcesFTSQuery(ctx context.Context, ftsQuery string, limit int) ([]model.SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			s.source_key,
			s.source_type,
			'' AS external_id,
			s.title,
			'' AS author_handle,
			'' AS author_name,
			s.canonical_url,
			s.domain,
			s.note_path,
			s.user_tags,
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
			source_type,
			'' AS external_id,
			title,
			'' AS author_handle,
			'' AS author_name,
			canonical_url,
			domain,
			note_path,
			user_tags,
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), extracted_text), char(10), ' ')), 1, 200) AS snippet
		FROM sources
		WHERE title LIKE ?
			OR source_key LIKE ?
			OR description LIKE ?
			OR site_name LIKE ?
			OR extracted_text LIKE ?
			OR summary_text LIKE ?
			OR canonical_url LIKE ?
			OR normalized_url LIKE ?
			OR domain LIKE ?
			OR note_path LIKE ?
			OR user_tags LIKE ?
		ORDER BY updated_at DESC
		LIMIT ?`, like, like, like, like, like, like, like, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("source like search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}
