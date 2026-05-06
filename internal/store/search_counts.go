package store

import (
	"context"
	"fmt"
	"strings"
)

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
