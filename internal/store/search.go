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

	exactResults, err := s.searchExactLookup(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	for _, result := range exactResults {
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

	if len(results) > 0 {
		return results, nil
	}

	if s.hasFTS {
		relaxedQuery := buildRelaxedFTSQuery(query)
		if relaxedQuery != buildFTSQuery(query) {
			relaxedTerms := relaxedMatchTerms(query)
			if sourceResults, err := s.searchSourcesFTSQuery(ctx, relaxedQuery, limit); err == nil {
				for _, result := range sourceResults {
					if len(results) >= limit {
						break
					}
					if !matchesRelaxedTerms(result, relaxedTerms) {
						continue
					}
					if _, exists := seen[result.SourceKey]; exists {
						continue
					}
					seen[result.SourceKey] = struct{}{}
					results = append(results, result)
				}
			}
			if len(results) < limit {
				if itemResults, err := s.searchFTSQuery(ctx, relaxedQuery, limit-len(results)); err == nil {
					for _, result := range itemResults {
						if len(results) >= limit {
							break
						}
						if !matchesRelaxedTerms(result, relaxedTerms) {
							continue
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
	}

	return results, nil
}

func (s *Store) searchExactLookup(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
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
		WHERE source_key = ?
			OR external_id = ?
			OR canonical_url = ?
			OR note_path = ?
		ORDER BY last_seen_at DESC
		LIMIT ?`, query, query, query, query, limit)
	if err != nil {
		return nil, fmt.Errorf("exact item search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	results, err := scanSearchResults(rows)
	if err != nil {
		return nil, err
	}
	if len(results) >= limit {
		return results, nil
	}

	sourceRows, err := s.db.QueryContext(ctx, `
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
		WHERE source_key = ?
			OR canonical_url = ?
			OR normalized_url = ?
			OR note_path = ?
		ORDER BY updated_at DESC
		LIMIT ?`, query, query, query, query, limit-len(results))
	if err != nil {
		return nil, fmt.Errorf("exact source search: %w", err)
	}
	defer func() {
		_ = sourceRows.Close()
	}()

	sourceResults, err := scanSearchResults(sourceRows)
	if err != nil {
		return nil, err
	}
	results = append(results, sourceResults...)
	return results, nil
}

func (s *Store) searchFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, error) {
	return s.searchFTSQuery(ctx, buildFTSQuery(query), limit)
}

func (s *Store) searchFTSQuery(ctx context.Context, ftsQuery string, limit int) ([]model.SearchResult, error) {
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
			substr(trim(replace(COALESCE(
				NULLIF(summary_enrichment.text, ''),
				NULLIF(i.summary_text, ''),
				NULLIF(ocr_enrichment.text, ''),
				NULLIF(i.ocr_text, ''),
				NULLIF(transcript_enrichment.text, ''),
				NULLIF(i.article_text, ''),
				i.text
			), char(10), ' ')), 1, 200) AS snippet
		FROM items_fts f
		JOIN items i ON i.id = f.rowid
		LEFT JOIN item_enrichments summary_enrichment
			ON summary_enrichment.item_id = i.id AND summary_enrichment.role = ?
		LEFT JOIN item_enrichments ocr_enrichment
			ON ocr_enrichment.item_id = i.id AND ocr_enrichment.role = ?
		LEFT JOIN item_enrichments transcript_enrichment
			ON transcript_enrichment.item_id = i.id AND transcript_enrichment.role = ?
		WHERE items_fts MATCH ?
		ORDER BY bm25(items_fts), i.last_seen_at DESC
		LIMIT ?`,
		model.ItemEnrichmentRoleSummary,
		model.ItemEnrichmentRoleOCR,
		model.ItemEnrichmentRoleXMediaTranscript,
		ftsQuery,
		limit,
	)
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
			OR source_key LIKE ?
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
		LIMIT ?`, like, like, like, like, like, like, like, like, like, like, like, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("like search: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return scanSearchResults(rows)
}
