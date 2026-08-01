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

	if _, err := withAuthoritativeWriteTx(ctx, s, "reset-source-enrichment", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
		if _, err := tx.ExecContext(ctx, `
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
			return struct{}{}, fmt.Errorf("reset source enrichment: %w", err)
		}
		return struct{}{}, nil
	}); err != nil {
		return stats, err
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
