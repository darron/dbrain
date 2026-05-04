package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type ResetSourceEnrichmentOptions struct {
	Domains            []string
	SourceIDs          []int64
	SourceTypes        []string
	ExtractStatuses    []string
	SummaryStatuses    []string
	FailureKinds       []string
	MinFailures        int
	RehydrateXArticles bool
	DryRun             bool
}

type ResetSourceEnrichmentStats struct {
	Matched       int  `json:"matched"`
	Reset         int  `json:"reset"`
	XItemsMatched int  `json:"x_items_matched,omitempty"`
	XItemsReset   int  `json:"x_items_reset,omitempty"`
	DryRun        bool `json:"dry_run"`
}

func (s *Store) ResetSourceEnrichment(ctx context.Context, opts ResetSourceEnrichmentOptions) (ResetSourceEnrichmentStats, error) {
	stats := ResetSourceEnrichmentStats{DryRun: opts.DryRun}
	where, args := resetSourceEnrichmentWhere(opts)
	if where == "" {
		return stats, fmt.Errorf("at least one source reset filter is required")
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sources WHERE `+where+` ORDER BY id ASC`, args...)
	if err != nil {
		return stats, fmt.Errorf("select sources for enrichment reset: %w", err)
	}
	var sourceIDs []int64
	for rows.Next() {
		var sourceID int64
		if err := rows.Scan(&sourceID); err != nil {
			_ = rows.Close()
			return stats, fmt.Errorf("scan source reset id: %w", err)
		}
		sourceIDs = append(sourceIDs, sourceID)
	}
	if err := rows.Close(); err != nil {
		return stats, fmt.Errorf("close source reset rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("iterate source reset ids: %w", err)
	}

	stats.Matched = len(sourceIDs)
	var xItemIDs []int64
	if opts.RehydrateXArticles && len(sourceIDs) > 0 {
		xItemIDs, err = s.listXArticleRehydrateItemIDs(ctx, sourceIDs)
		if err != nil {
			return stats, err
		}
		stats.XItemsMatched = len(xItemIDs)
	}
	if opts.DryRun || len(sourceIDs) == 0 {
		return stats, nil
	}

	placeholders := make([]string, 0, len(sourceIDs))
	updateArgs := make([]any, 0, len(sourceIDs)+1)
	nowText := time.Now().UTC().Format(time.RFC3339)
	updateArgs = append(updateArgs, nowText)
	for _, sourceID := range sourceIDs {
		placeholders = append(placeholders, "?")
		updateArgs = append(updateArgs, sourceID)
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE sources
		SET title = '',
			description = '',
			site_name = '',
			extracted_text = '',
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
		WHERE id IN (`+strings.Join(placeholders, ",")+`)`, updateArgs...); err != nil {
		return stats, fmt.Errorf("reset source enrichment: %w", err)
	}

	for _, sourceID := range sourceIDs {
		if err := s.syncSourceFTS(ctx, sourceID); err != nil {
			return stats, err
		}
	}

	if opts.RehydrateXArticles && len(xItemIDs) > 0 {
		reset, err := s.resetXArticleHydrationItems(ctx, xItemIDs, nowText)
		if err != nil {
			return stats, err
		}
		stats.XItemsReset = reset
	}

	stats.Reset = len(sourceIDs)
	return stats, nil
}

func (s *Store) listXArticleRehydrateItemIDs(ctx context.Context, sourceIDs []int64) ([]int64, error) {
	sourceIDs = uniquePositiveInt64s(sourceIDs)
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, 0, len(sourceIDs))
	args := make([]any, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		placeholders = append(placeholders, "?")
		args = append(args, sourceID)
	}
	sourceClause := strings.Join(placeholders, ",")

	rows, err := s.db.QueryContext(ctx, `
		WITH linked_items AS (
			SELECT i.id AS item_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			JOIN items i ON i.id = l.item_id
			WHERE s.source_type = 'x_article'
				AND s.id IN (`+sourceClause+`)
				AND (i.source_type = 'x_bookmark' OR i.source_type = 'x_quote')

			UNION

			SELECT p.id AS item_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			JOIN items i ON i.id = l.item_id
			JOIN item_item_links q ON q.child_item_id = i.id AND q.link_kind = 'quoted_post'
			JOIN items p ON p.id = q.parent_item_id
			WHERE s.source_type = 'x_article'
				AND s.id IN (`+sourceClause+`)
				AND (p.source_type = 'x_bookmark' OR p.source_type = 'x_quote')
		)
		SELECT DISTINCT item_id
		FROM linked_items
		ORDER BY item_id ASC`, append(args, args...)...)
	if err != nil {
		return nil, fmt.Errorf("list x article rehydrate items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var itemIDs []int64
	for rows.Next() {
		var itemID int64
		if err := rows.Scan(&itemID); err != nil {
			return nil, fmt.Errorf("scan x article rehydrate item: %w", err)
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x article rehydrate items: %w", err)
	}
	return itemIDs, nil
}

func (s *Store) resetXArticleHydrationItems(ctx context.Context, itemIDs []int64, nowText string) (int, error) {
	itemIDs = uniquePositiveInt64s(itemIDs)
	if len(itemIDs) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(itemIDs))
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, nowText)
	for _, itemID := range itemIDs {
		placeholders = append(placeholders, "?")
		args = append(args, itemID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin x article hydration reset tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	result, err := tx.ExecContext(ctx, `
		UPDATE items
		SET x_post_text = '',
			x_post_lang = '',
			x_post_json = '',
			x_post_fetched_at = '',
			x_post_status = '',
			x_post_error = '',
			link_extract_synced_at = '',
			updated_at = ?
		WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("reset x article hydration items: %w", err)
	}

	for _, itemID := range itemIDs {
		if _, err := s.invalidateItemSummaryTx(ctx, tx, itemID, nowText); err != nil {
			return 0, err
		}
		if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit x article hydration reset: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return len(itemIDs), nil
	}
	return int(rowsAffected), nil
}

func resetSourceEnrichmentWhere(opts ResetSourceEnrichmentOptions) (string, []any) {
	parts := make([]string, 0, 7)
	args := make([]any, 0, len(opts.Domains)*2+len(opts.SourceIDs)+len(opts.SourceTypes)+len(opts.ExtractStatuses)+len(opts.SummaryStatuses)+len(opts.FailureKinds)+1)

	domains := uniqueSourceResetDomains(opts.Domains)
	if len(domains) > 0 {
		domainParts := make([]string, 0, len(domains))
		for _, domain := range domains {
			domainParts = append(domainParts, `(lower(domain) = ? OR lower(domain) LIKE ?)`)
			args = append(args, domain, "%."+domain)
		}
		parts = append(parts, "("+strings.Join(domainParts, " OR ")+")")
	}

	sourceIDs := uniquePositiveInt64s(opts.SourceIDs)
	if len(sourceIDs) > 0 {
		placeholders := make([]string, 0, len(sourceIDs))
		for _, sourceID := range sourceIDs {
			placeholders = append(placeholders, "?")
			args = append(args, sourceID)
		}
		parts = append(parts, "id IN ("+strings.Join(placeholders, ",")+")")
	}

	if sourceTypes := uniqueLowerNonEmptyStrings(opts.SourceTypes); len(sourceTypes) > 0 {
		clause, clauseArgs := stringInClause("source_type", sourceTypes)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if extractStatuses := uniqueLowerNonEmptyStrings(opts.ExtractStatuses); len(extractStatuses) > 0 {
		clause, clauseArgs := stringInClause("extract_status", extractStatuses)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if summaryStatuses := uniqueLowerNonEmptyStrings(opts.SummaryStatuses); len(summaryStatuses) > 0 {
		clause, clauseArgs := stringInClause("summary_status", summaryStatuses)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if failureKinds := uniqueLowerNonEmptyStrings(opts.FailureKinds); len(failureKinds) > 0 {
		clause, clauseArgs := stringInClause("extract_failure_kind", failureKinds)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if opts.MinFailures > 0 {
		parts = append(parts, "extract_failure_count >= ?")
		args = append(args, opts.MinFailures)
	}

	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

func uniqueSourceResetDomains(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "www.")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniquePositiveInt64s(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueLowerNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func stringInClause(column string, values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return "lower(" + column + ") IN (" + strings.Join(placeholders, ",") + ")", args
}
