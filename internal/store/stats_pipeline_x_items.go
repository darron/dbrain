package store

import "context"

func (s *Store) pipelineXMediaTranscriptionRow(ctx context.Context) (PipelineStageRow, bool, error) {
	const transcriptTitle = "X Media Transcript"

	candidateWhere := xItemSourceTypeWhere + `
		AND external_id != ''
		AND EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND a.download_status = 'downloaded'
				AND a.media_type IN ('video', 'animated_gif')
		)
		AND (
			article_text = ''
			OR article_title = ?
			OR x_media_transcript_status != ''
		)`

	total, err := s.countWhere(ctx, "items", candidateWhere, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND article_title = ? AND article_text != ''`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND NOT (article_title = ? AND article_text != '') AND x_media_transcript_status = ''`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND NOT (article_title = ? AND article_text != '') AND x_media_transcript_status = 'ok'`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND NOT (article_title = ? AND article_text != '') AND x_media_transcript_status != '' AND x_media_transcript_status != 'ok'`, transcriptTitle, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    "x_media_transcript",
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
	const transcriptTitle = "X Media Transcript"

	candidateWhere := xItemSourceTypeWhere + `
		AND article_title = ?
		AND article_text != ''
		AND x_media_transcript_status = 'ok'`

	total, err := s.countWhere(ctx, "items", candidateWhere, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND summary_status = 'ok' AND summary_text != ''`, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND (summary_status = '' OR summary_status = 'error')`, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND summary_status IN ('blocked', 'skipped')`, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND summary_status != '' AND summary_status NOT IN ('ok', 'error', 'blocked', 'skipped')`, transcriptTitle)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    "x_media_summary",
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
	candidateWhere := xItemSourceTypeWhere + `
		AND external_id != ''
		AND EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND a.download_status = 'downloaded'
				AND a.media_type = 'photo'
		)`

	total, err := s.countWhere(ctx, "items", candidateWhere)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	if total == 0 {
		return PipelineStageRow{}, false, nil
	}

	current, err := s.countWhere(ctx, "items", candidateWhere+` AND ocr_status = 'ok' AND ocr_text != ''`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "items", candidateWhere+` AND (ocr_status = '' OR ocr_status = 'error')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "items", candidateWhere+` AND ocr_status IN ('blocked', 'skipped')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	failed, err := s.countWhere(ctx, "items", candidateWhere+` AND ocr_status != '' AND ocr_status NOT IN ('ok', 'error', 'blocked', 'skipped')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{
		Kind:    "x_photo_ocr",
		Total:   total,
		Current: current,
		Pending: pending,
		Blocked: blocked,
		Failed:  failed,
	}
	finalizePipelineStageRow(&row)
	return row, true, nil
}
