package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

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
