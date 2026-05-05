package store

import (
	"context"

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

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND summary_status = '`+model.ItemSummaryStatusOK+`' AND summary_text != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND (summary_status = '' OR summary_status = '`+model.ItemSummaryStatusError+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND summary_status IN ('`+model.ItemSummaryStatusBlocked+`', '`+model.ItemSummaryStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND summary_status != '' AND summary_status NOT IN ('`+model.ItemSummaryStatusOK+`', '`+model.ItemSummaryStatusError+`', '`+model.ItemSummaryStatusBlocked+`', '`+model.ItemSummaryStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindAppleNote,
		Total:   total,
		Current: current,
		Pending: pending,
		Blocked: blocked,
		Failed:  failed,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}
