package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) pipelineAppleNoteExtractionRow(ctx context.Context) (PipelineStageRow, bool, error) {
	candidateWhere := `source_type = 'apple_note'`

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND (text != '' OR article_text != '')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND text = '' AND article_text = ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindAppleNote,
		Total:   total,
		Current: current,
		Blocked: blocked,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func (s *Store) pipelineSafariTabExtractionRow(ctx context.Context) (PipelineStageRow, bool, error) {
	candidateWhere := `source_type = 'safari_tab'`

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND text != '' AND canonical_url != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND (text = '' OR canonical_url = '')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindSafariTab,
		Total:   total,
		Current: current,
		Blocked: blocked,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func (s *Store) pipelineAppleNoteSummaryRow(ctx context.Context) (PipelineStageRow, bool, error) {
	candidateWhere := `source_type = 'apple_note' AND (text != '' OR article_text != '')`
	summaryStatus := itemSummaryStatusExpr()
	summaryText := itemSummaryTextExpr()

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND `+summaryStatus+` = '`+model.ItemSummaryStatusOK+`' AND `+summaryText+` != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND (`+summaryStatus+` = '' OR `+summaryStatus+` = '`+model.ItemSummaryStatusError+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pendingAt, pendingKnown, err := s.oldestItemSummaryPendingTimestamp(ctx, candidateWhere+` AND (`+summaryStatus+` = '' OR `+summaryStatus+` = '`+model.ItemSummaryStatusError+`')`, "items.imported_at")
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND `+summaryStatus+` IN ('`+model.ItemSummaryStatusBlocked+`', '`+model.ItemSummaryStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	unknown, err := s.countWhere(ctx, "items", candidateWhere+` AND `+summaryStatus+` != '' AND `+summaryStatus+` NOT IN ('`+model.ItemSummaryStatusOK+`', '`+model.ItemSummaryStatusError+`', '`+model.ItemSummaryStatusBlocked+`', '`+model.ItemSummaryStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindAppleNote,
		Total:   total,
		Current: current,
		Pending: pending,
		Blocked: blocked,
		Unknown: unknown,
	}
	finalizePipelineStageRow(&row)
	row.OldestPendingAt = pendingAt
	row.OldestPendingKnown = pendingKnown
	return row, true, nil
}

func (s *Store) oldestItemSummaryPendingTimestamp(ctx context.Context, where string, prerequisiteExpr string, args ...any) (time.Time, bool, error) {
	status := itemSummaryStatusExpr()
	stageUpdatedAt := itemEnrichmentFieldExpr(model.ItemEnrichmentRoleSummary, "updated_at", "''")
	rows, err := s.queryer().QueryContext(ctx, `SELECT `+status+`, items.updated_at, `+stageUpdatedAt+`, items.summarized_at, `+prerequisiteExpr+`
		FROM items WHERE `+where, args...)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("list item summary pending timestamps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var oldest time.Time
	for rows.Next() {
		var statusValue, itemUpdatedAt, summaryUpdatedAt, summarizedAt, prerequisiteAt string
		if err := rows.Scan(&statusValue, &itemUpdatedAt, &summaryUpdatedAt, &summarizedAt, &prerequisiteAt); err != nil {
			return time.Time{}, false, fmt.Errorf("scan item summary pending timestamp: %w", err)
		}
		var anchor time.Time
		var ok bool
		if strings.TrimSpace(statusValue) == model.ItemSummaryStatusError {
			value := summaryUpdatedAt
			if strings.TrimSpace(value) == "" {
				value = summarizedAt
			}
			anchor, ok = parsePendingTimestamp(value)
		} else if strings.TrimSpace(summaryUpdatedAt) != "" {
			anchor, ok = parsePendingTimestamp(summaryUpdatedAt)
		} else {
			itemTime, itemOK := parsePendingTimestamp(itemUpdatedAt)
			prerequisiteTime, prerequisiteOK := parsePendingTimestamp(prerequisiteAt)
			if !itemOK || !prerequisiteOK || itemTime.After(prerequisiteTime) {
				return time.Time{}, false, nil
			}
			anchor, ok = prerequisiteTime, true
		}
		if !ok {
			return time.Time{}, false, nil
		}
		if oldest.IsZero() || anchor.Before(oldest) {
			oldest = anchor
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, fmt.Errorf("iterate item summary pending timestamps: %w", err)
	}
	return oldest, !oldest.IsZero(), nil
}
