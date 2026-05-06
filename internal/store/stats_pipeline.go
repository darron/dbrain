package store

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) Pipeline(ctx context.Context, promptVersion string, toolName string, toolVersion string) (PipelineStats, error) {
	policy := newSourceEnrichmentPolicy(time.Now().UTC(), promptVersion, toolName, toolVersion)

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
	stats.Hydration = buildPipelineStageRows(hydrationTotal, hydrationCurrent, hydrationPending, nil)

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
	stats.Extraction = buildPipelineStageRows(extractionTotal, extractionCurrent, extractionPending, nil)
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
	summaryBlocked, err := s.countGroupedWhere(
		ctx,
		"sources",
		"source_type",
		`extract_status IN ('`+model.SourceExtractStatusDead+`', '`+model.SourceExtractStatusGone+`') OR (`+readyForSummaryWhere+` AND summary_status IN ('`+model.SourceSummaryStatusBlocked+`', '`+model.SourceSummaryStatusSkipped+`'))`,
	)
	if err != nil {
		return PipelineStats{}, err
	}
	stats.Summary = buildPipelineStageRows(summaryTotal, summaryCurrent, summaryPending, summaryBlocked)

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

	return stats, nil
}
