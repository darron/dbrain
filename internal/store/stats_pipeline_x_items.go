package store

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) pipelineXMediaTranscriptionRow(ctx context.Context) (PipelineStageRow, bool, error) {
	status := itemXMediaTranscriptStatusExpr()
	text := itemXMediaTranscriptTextExpr()
	completedAt := itemXMediaTranscriptCompletedAtExpr()
	candidateWhere := xItemSourceTypeWhere + ` AND external_id != '' AND ` + xMediaTranscriptionAnyMediaExistsWhere

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil || total == 0 {
		return PipelineStageRow{}, false, err
	}
	current, err := s.countWhere(ctx, "items", candidateWhere+` AND `+status+` = '`+model.XMediaTranscriptStatusOK+`' AND `+text+` != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pendingWhere, pendingArgs := xMediaTranscriptionPendingWhere(time.Now().UTC())
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND `+pendingWhere, pendingArgs...)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blockedWhere := text + ` = '' AND (
		` + status + ` = '` + model.XMediaTranscriptStatusOK + `'
		OR ((` + status + ` = '' OR ` + status + ` = '` + model.XMediaTranscriptStatusError + `') AND NOT ` + xMediaTranscriptionRunnableMediaExistsWhere + `)
		OR (` + status + ` = '` + model.XMediaTranscriptStatusError + `' AND (` + completedAt + ` = '' OR ` + completedAt + ` > ?))
	)`
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND `+blockedWhere, pendingArgs[0])
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	terminal, err := s.countWhere(ctx, "items", candidateWhere+` AND `+text+` = '' AND `+status+` IN ('`+model.XMediaTranscriptStatusNoAudio+`', '`+model.XMediaTranscriptStatusNoise+`', '`+model.XMediaTranscriptStatusTooShort+`', '`+model.XMediaTranscriptStatusEmpty+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	unknown, err := s.countWhere(ctx, "items", candidateWhere+` AND `+status+` NOT IN ('', '`+model.XMediaTranscriptStatusOK+`', '`+model.XMediaTranscriptStatusError+`', '`+model.XMediaTranscriptStatusNoAudio+`', '`+model.XMediaTranscriptStatusNoise+`', '`+model.XMediaTranscriptStatusTooShort+`', '`+model.XMediaTranscriptStatusEmpty+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{Kind: pipelineKindXMediaTranscript, Total: total, Current: current, Pending: pending, Blocked: blocked, Terminal: terminal, Unknown: unknown}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func (s *Store) pipelineXMediaSummaryRow(ctx context.Context) (PipelineStageRow, bool, error) {
	transcriptStatus := itemXMediaTranscriptStatusExpr()
	transcriptText := itemXMediaTranscriptTextExpr()
	summaryStatus := itemSummaryStatusExpr()
	summaryText := itemSummaryTextExpr()
	candidateWhere := xItemSourceTypeWhere + ` AND ` + transcriptText + ` != '' AND ` + transcriptStatus + ` = '` + model.XMediaTranscriptStatusOK + `'`

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil || total == 0 {
		return PipelineStageRow{}, false, err
	}
	current, err := s.countWhere(ctx, "items", candidateWhere+` AND `+summaryStatus+` = '`+model.ItemSummaryStatusOK+`' AND `+summaryText+` != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND (`+summaryStatus+` = '' OR `+summaryStatus+` = '`+model.ItemSummaryStatusError+`')`)
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

	row := PipelineStageRow{Kind: pipelineKindXMediaSummary, Total: total, Current: current, Pending: pending, Blocked: blocked, Unknown: unknown}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func (s *Store) pipelineXPhotoOCRRow(ctx context.Context) (PipelineStageRow, bool, error) {
	status := itemOCRStatusExpr()
	text := itemOCRTextExpr()
	candidateWhere := xItemSourceTypeWhere + ` AND external_id != '' AND ` + xPhotoOCRAnyMediaExistsWhere

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil || total == 0 {
		return PipelineStageRow{}, false, err
	}
	current, err := s.countWhere(ctx, "items", candidateWhere+` AND `+status+` = '`+model.ItemOCRStatusOK+`' AND `+text+` != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND `+xPhotoOCRPendingWhere())
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND (`+status+` IN ('`+model.ItemOCRStatusBlocked+`', '`+model.ItemOCRStatusSkipped+`') OR ((`+status+` = '' OR `+status+` = '`+model.ItemOCRStatusError+`') AND NOT `+xPhotoOCRRunnableMediaExistsWhere+`))`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	unknown, err := s.countWhere(ctx, "items", candidateWhere+` AND `+status+` != '' AND `+status+` NOT IN ('`+model.ItemOCRStatusOK+`', '`+model.ItemOCRStatusError+`', '`+model.ItemOCRStatusBlocked+`', '`+model.ItemOCRStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{Kind: pipelineKindXPhotoOCR, Total: total, Current: current, Pending: pending, Blocked: blocked, Unknown: unknown}
	finalizePipelineStageRow(&row)
	return row, true, nil
}
