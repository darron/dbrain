package store

import (
	"context"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) pipelineXMediaTranscriptionRow(ctx context.Context) (PipelineStageRow, bool, error) {
	transcriptStatus := itemXMediaTranscriptStatusExpr()
	transcriptText := itemXMediaTranscriptTextExpr()

	candidateWhere := xItemSourceTypeWhere + `
		AND external_id != ''
		AND ` + xMediaTranscriptionAnyMediaExistsWhere + `
		AND (
			article_text = ''
			OR article_title = '` + model.XMediaTranscriptArticleTitle + `'
			OR ` + transcriptStatus + ` != ''
			OR ` + transcriptText + ` != ''
		)`

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND `+transcriptText+` != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	withoutMaterializedTranscriptWhere := ` AND ` + transcriptText + ` = ''`
	pendingWhere := withoutMaterializedTranscriptWhere + ` AND ` + transcriptStatus + ` = '' AND ` + xMediaTranscriptionRunnableMediaExistsWhere
	blockedWhere := withoutMaterializedTranscriptWhere + ` AND (
		` + transcriptStatus + ` = '` + model.XMediaTranscriptStatusOK + `'
		OR (
			` + transcriptStatus + ` = ''
			AND NOT ` + xMediaTranscriptionRunnableMediaExistsWhere + `
		)
	)`

	pending, err := s.countWhere(ctx, "items", candidateWhere+pendingWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+blockedWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+withoutMaterializedTranscriptWhere+` AND `+transcriptStatus+` != '' AND `+transcriptStatus+` != '`+model.XMediaTranscriptStatusOK+`'`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindXMediaTranscript,
		Total:   total,
		Current: current,
		Pending: pending,
		Blocked: blocked,
		Failed:  failed,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func (s *Store) pipelineXMediaSummaryRow(ctx context.Context) (PipelineStageRow, bool, error) {
	transcriptStatus := itemXMediaTranscriptStatusExpr()
	transcriptText := itemXMediaTranscriptTextExpr()
	summaryStatus := itemSummaryStatusExpr()
	summaryText := itemSummaryTextExpr()

	candidateWhere := xItemSourceTypeWhere + `
		AND ` + transcriptText + ` != ''
		AND ` + transcriptStatus + ` = '` + model.XMediaTranscriptStatusOK + `'`

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
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND `+summaryStatus+` IN ('`+model.ItemSummaryStatusBlocked+`', '`+model.ItemSummaryStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND `+summaryStatus+` != '' AND `+summaryStatus+` NOT IN ('`+model.ItemSummaryStatusOK+`', '`+model.ItemSummaryStatusError+`', '`+model.ItemSummaryStatusBlocked+`', '`+model.ItemSummaryStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindXMediaSummary,
		Total:   total,
		Current: current,
		Pending: pending,
		Blocked: blocked,
		Failed:  failed,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}

func (s *Store) pipelineXPhotoOCRRow(ctx context.Context) (PipelineStageRow, bool, error) {
	ocrStatus := itemOCRStatusExpr()
	ocrText := itemOCRTextExpr()

	candidateWhere := xItemSourceTypeWhere + `
		AND external_id != ''
		AND EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND a.download_status = '` + model.MediaDownloadStatusDownloaded + `'
				AND a.media_type = 'photo'
		)`

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND `+ocrStatus+` = '`+model.ItemOCRStatusOK+`' AND `+ocrText+` != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND (`+ocrStatus+` = '' OR `+ocrStatus+` = '`+model.ItemOCRStatusError+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND `+ocrStatus+` IN ('`+model.ItemOCRStatusBlocked+`', '`+model.ItemOCRStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND `+ocrStatus+` != '' AND `+ocrStatus+` NOT IN ('`+model.ItemOCRStatusOK+`', '`+model.ItemOCRStatusError+`', '`+model.ItemOCRStatusBlocked+`', '`+model.ItemOCRStatusSkipped+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    pipelineKindXPhotoOCR,
		Total:   total,
		Current: current,
		Pending: pending,
		Blocked: blocked,
		Failed:  failed,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}
