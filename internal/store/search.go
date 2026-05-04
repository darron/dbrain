package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"github.com/darron/dbrain/internal/model"
)

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
