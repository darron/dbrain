package store

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) Pipeline(ctx context.Context, promptVersion string, toolName string, toolVersion string) (PipelineStats, error) {
	now := time.Now().UTC()
	policy := newSourceEnrichmentPolicy(now, promptVersion, toolName, toolVersion)

	stats := PipelineStats{}
	if policy.promptVersion != "" || policy.toolName != "" || policy.toolVersion != "" {
		stats.SummaryPromptVersion = policy.promptVersion
		stats.SummaryTool = policy.toolName
		stats.SummaryToolVersion = policy.toolVersion
	}

	hydrationTotal, err := s.countGroupedWhere(ctx, "items", "source_type", xItemSourceTypeWhere+` AND external_id != ''`)
	if err != nil {
		return PipelineStats{}, err
	}
	hydrationCurrent, err := s.countGroupedWhere(ctx, "items", "source_type", xItemSourceTypeWhere+` AND external_id != '' AND x_post_status LIKE 'ok_%' AND NOT (`+xMediaHydrationRepairWhere+` OR `+xHydrationRepairWhere+`)`)
	if err != nil {
		return PipelineStats{}, err
	}
	hydrationPending, err := s.countGroupedWhere(ctx, "items", "source_type", xItemSourceTypeWhere+` AND external_id != '' AND `+xHydrationCandidateWhere)
	if err != nil {
		return PipelineStats{}, err
	}
	hydrationTerminal, err := s.countGroupedWhere(ctx, "items", "source_type", xItemSourceTypeWhere+` AND external_id != '' AND `+xHydrationTerminalWhere)
	if err != nil {
		return PipelineStats{}, err
	}
	hydrationBlocked, err := s.countGroupedWhere(ctx, "items", "source_type", xItemSourceTypeWhere+` AND external_id != '' AND `+xHydrationBlockedWhere)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Hydration = buildPipelineStageRows(hydrationTotal, hydrationCurrent, hydrationPending, hydrationBlocked, hydrationTerminal)
	hydrationPendingAt, hydrationPendingKnown, err := s.oldestTimestampWhere(ctx, "items", `CASE
		WHEN x_post_status = '' THEN COALESCE(NULLIF(imported_at, ''), NULLIF(updated_at, ''), last_seen_at)
		WHEN x_post_status IN ('api_error', 'error', 'rate_limited') THEN COALESCE(NULLIF(x_post_fetched_at, ''), NULLIF(updated_at, ''), imported_at)
		ELSE COALESCE(NULLIF(x_post_fetched_at, ''), NULLIF(updated_at, ''), imported_at)
	END`, xItemSourceTypeWhere+` AND external_id != '' AND `+xHydrationCandidateWhere)
	if err != nil {
		return PipelineStats{}, err
	}
	setPipelineOldestPending(stats.Hydration, hydrationPendingAt, hydrationPendingKnown)

	extractWhere, extractArgs := policy.extractBacklogWhere()
	extractionTotal, err := s.countGroupedWhere(ctx, "sources", "source_type", "")
	if err != nil {
		return PipelineStats{}, err
	}
	extractionCurrent, err := s.countGroupedWhere(ctx, "sources", "source_type", `extract_status IN ('`+model.SourceExtractStatusOK+`', '`+model.SourceExtractStatusEmpty+`') AND NOT `+sourceExtractCoverageRepairWhere())
	if err != nil {
		return PipelineStats{}, err
	}
	extractionPending, err := s.countGroupedWhere(ctx, "sources", "source_type", extractWhere, extractArgs...)
	if err != nil {
		return PipelineStats{}, err
	}
	extractionBlockedWhere, extractionBlockedArgs := policy.extractionBlockedWhere()
	extractionBlocked, err := s.countGroupedWhere(ctx, "sources", "source_type", extractionBlockedWhere, extractionBlockedArgs...)
	if err != nil {
		return PipelineStats{}, err
	}
	extractionTerminalWhere, extractionTerminalArgs := policy.extractionTerminalWhere()
	extractionTerminal, err := s.countGroupedWhere(ctx, "sources", "source_type", extractionTerminalWhere, extractionTerminalArgs...)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Extraction = buildPipelineStageRows(extractionTotal, extractionCurrent, extractionPending, extractionBlocked, extractionTerminal)
	extractionPendingAt, extractionPendingKnown, err := s.oldestTimestampWhere(ctx, "sources", `CASE
		WHEN extract_status = '' THEN COALESCE(NULLIF(created_at, ''), updated_at)
		WHEN extract_status = '`+model.SourceExtractStatusError+`' THEN COALESCE(NULLIF(extract_last_failed_at, ''), NULLIF(updated_at, ''), created_at)
		ELSE COALESCE(NULLIF(extracted_at, ''), NULLIF(updated_at, ''), created_at)
	END`, extractWhere, extractArgs...)
	if err != nil {
		return PipelineStats{}, err
	}
	appleNoteExtractionRow, ok, err := s.pipelineAppleNoteExtractionRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.Extraction = appendPipelineStageRow(stats.Extraction, appleNoteExtractionRow)
	}
	safariTabExtractionRow, ok, err := s.pipelineSafariTabExtractionRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.Extraction = appendPipelineStageRow(stats.Extraction, safariTabExtractionRow)
	}
	setPipelineOldestPending(stats.Extraction, extractionPendingAt, extractionPendingKnown)

	summaryTotal, err := s.countGroupedWhere(ctx, "sources", "source_type", "")
	if err != nil {
		return PipelineStats{}, err
	}
	readyForSummaryWhere := `extract_status IN ('` + model.SourceExtractStatusOK + `', '` + model.SourceExtractStatusEmpty + `') AND NOT ` + sourceExtractCoverageRepairWhere()
	extractPendingWhere, extractPendingArgs := policy.extractBacklogWhere()
	summaryStaleWhere, summaryArgs := sourceSummaryStaleWhere(policy.promptVersion, policy.toolName, policy.toolVersion)
	summaryCurrent, err := s.countGroupedWhere(
		ctx,
		"sources",
		"source_type",
		readyForSummaryWhere+` AND summary_status = '`+model.SourceSummaryStatusOK+`' AND summary_content_hash = content_hash AND NOT `+summaryStaleWhere,
		summaryArgs...,
	)
	if err != nil {
		return PipelineStats{}, err
	}
	summaryPendingWhere, summaryPendingArgs := policy.summaryBacklogWhere()
	summaryPendingCondition := `( (` + summaryPendingWhere + `) OR (` + extractPendingWhere + `) )`
	summaryPendingArgs = append(summaryPendingArgs, extractPendingArgs...)
	summaryPending, err := s.countGroupedWhere(ctx, "sources", "source_type", summaryPendingCondition, summaryPendingArgs...)
	if err != nil {
		return PipelineStats{}, err
	}
	summaryExtractionBlockedWhere, summaryExtractionBlockedArgs := policy.extractionBlockedWhere()
	summaryBlockedArgs := append([]any{}, summaryExtractionBlockedArgs...)
	summaryBlockedArgs = append(summaryBlockedArgs, extractPendingArgs...)
	summaryBlockedArgs = append(summaryBlockedArgs, summaryArgs...)
	summaryBlocked, err := s.countGroupedWhere(
		ctx,
		"sources",
		"source_type",
		`(`+summaryExtractionBlockedWhere+`) OR `+
			`(extract_status IN ('`+model.SourceExtractStatusDead+`', '`+model.SourceExtractStatusGone+`') AND NOT (`+extractPendingWhere+`)) OR `+
			`(`+readyForSummaryWhere+` AND summary_status IN ('`+model.SourceSummaryStatusBlocked+`', '`+model.SourceSummaryStatusSkipped+`') AND NOT (`+summaryStaleWhere+`))`,
		summaryBlockedArgs...,
	)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Summary = buildPipelineStageRows(summaryTotal, summaryCurrent, summaryPending, summaryBlocked, nil)
	summaryPendingAt, summaryPendingKnown, err := s.oldestTimestampWhere(ctx, "sources", `CASE
		WHEN extract_status = '' THEN COALESCE(NULLIF(created_at, ''), updated_at)
		WHEN extract_status = '`+model.SourceExtractStatusError+`' THEN COALESCE(NULLIF(extract_last_failed_at, ''), NULLIF(updated_at, ''), created_at)
		WHEN `+sourceExtractCoverageRepairWhere()+` OR `+sourceMakerWorldAPIRepairWhere()+` THEN COALESCE(NULLIF(extracted_at, ''), NULLIF(updated_at, ''), created_at)
		ELSE COALESCE(NULLIF(updated_at, ''), NULLIF(extracted_at, ''), created_at)
	END`, summaryPendingCondition, summaryPendingArgs...)
	if err != nil {
		return PipelineStats{}, err
	}

	transcriptionRow, ok, err := s.pipelineXMediaTranscriptionRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.Transcription = []PipelineStageRow{transcriptionRow}
	}
	xMediaSummaryRow, ok, err := s.pipelineXMediaSummaryRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.Summary = appendPipelineStageRow(stats.Summary, xMediaSummaryRow)
	}
	appleNoteSummaryRow, ok, err := s.pipelineAppleNoteSummaryRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.Summary = appendPipelineStageRow(stats.Summary, appleNoteSummaryRow)
	}
	xPhotoOCRRow, ok, err := s.pipelineXPhotoOCRRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.OCR = []PipelineStageRow{xPhotoOCRRow}
	}
	setPipelineOldestPending(stats.Summary, summaryPendingAt, summaryPendingKnown)
	mediaArchiveRow, ok, err := s.pipelineMediaArchiveRow(ctx)
	if err != nil {
		return PipelineStats{}, err
	}
	if ok {
		stats.MediaArchive = []PipelineStageRow{mediaArchiveRow}
	}

	return stats, nil
}

func (s *Store) pipelineMediaArchiveRow(ctx context.Context) (PipelineStageRow, bool, error) {
	base := mediaArchiveBaseWhere("a")
	total, err := s.countWhere(ctx, "media_assets a", base)
	if err != nil || total == 0 {
		return PipelineStageRow{}, false, err
	}
	current, err := s.countWhere(ctx, "media_assets a", base+` AND a.archive_status = '`+model.MediaArchiveStatusArchived+`'`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	pending, err := s.countWhere(ctx, "media_assets a", mediaArchiveCandidateWhere("a", false))
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	blocked, err := s.countWhere(ctx, "media_assets a", base+` AND (a.archive_status = '' OR a.archive_status = '`+model.MediaArchiveStatusError+`') AND NOT `+mediaArchiveEnrichmentCompleteWhere("a"))
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	unknown, err := s.countWhere(ctx, "media_assets a", base+` AND a.archive_status NOT IN ('', '`+model.MediaArchiveStatusError+`', '`+model.MediaArchiveStatusArchived+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	row := PipelineStageRow{Kind: "media_archive", Total: total, Current: current, Pending: pending, Blocked: blocked, Unknown: unknown}
	finalizePipelineStageRow(&row)
	return row, true, nil
}
