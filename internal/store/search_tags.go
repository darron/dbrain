package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

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
			snippet
		FROM (
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
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), NULLIF(ocr_text, ''), NULLIF(article_text, ''), text), char(10), ' ')), 1, 200) AS snippet,
			last_seen_at AS sort_at
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
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), extracted_text), char(10), ' ')), 1, 200) AS snippet,
			updated_at AS sort_at
		FROM sources
		WHERE lower(user_tags) LIKE ?
		)
		ORDER BY sort_at DESC, source_key ASC
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
			snippet
		FROM (
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
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), NULLIF(ocr_text, ''), NULLIF(article_text, ''), text), char(10), ' ')), 1, 200) AS snippet,
			last_seen_at AS sort_at
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
			substr(trim(replace(COALESCE(NULLIF(summary_text, ''), extracted_text), char(10), ' ')), 1, 200) AS snippet,
			updated_at AS sort_at
		FROM sources
		WHERE instr(',' || replace(replace(lower(user_tags), ', ', ','), ' ,', ',') || ',', ',' || ? || ',') > 0
		)
		ORDER BY sort_at DESC, source_key ASC
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

func normalizeUserTagQuery(tag string) string {
	tag = strings.TrimSpace(strings.ToLower(tag))
	tag = strings.Trim(tag, ", ")
	return tag
}
