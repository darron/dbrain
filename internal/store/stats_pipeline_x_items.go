package store

import (
	"context"
	"fmt"
	"strings"
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
	pendingAt, pendingKnown, err := s.oldestXMediaPendingTimestamp(ctx, candidateWhere+` AND `+pendingWhere, pendingArgs...)
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
	terminal, err := s.countWhere(ctx, "items", candidateWhere+` AND `+status+` IN ('`+model.XMediaTranscriptStatusNoAudio+`', '`+model.XMediaTranscriptStatusNoise+`', '`+model.XMediaTranscriptStatusTooShort+`', '`+model.XMediaTranscriptStatusEmpty+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}
	unknown, err := s.countWhere(ctx, "items", candidateWhere+` AND `+status+` NOT IN ('', '`+model.XMediaTranscriptStatusOK+`', '`+model.XMediaTranscriptStatusError+`', '`+model.XMediaTranscriptStatusNoAudio+`', '`+model.XMediaTranscriptStatusNoise+`', '`+model.XMediaTranscriptStatusTooShort+`', '`+model.XMediaTranscriptStatusEmpty+`')`)
	if err != nil {
		return PipelineStageRow{}, false, err
	}

	row := PipelineStageRow{Kind: pipelineKindXMediaTranscript, Total: total, Current: current, Pending: pending, Blocked: blocked, Terminal: terminal, Unknown: unknown}
	finalizePipelineStageRow(&row)
	row.OldestPendingAt = pendingAt
	row.OldestPendingKnown = pendingKnown
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
	transcriptReadyAt := itemEnrichmentFieldExpr(model.ItemEnrichmentRoleXMediaTranscript, "updated_at", itemXMediaTranscriptCompletedAtExpr())
	pendingAt, pendingKnown, err := s.oldestItemSummaryPendingTimestamp(ctx, candidateWhere+` AND (`+summaryStatus+` = '' OR `+summaryStatus+` = '`+model.ItemSummaryStatusError+`')`, transcriptReadyAt)
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
	row.OldestPendingAt = pendingAt
	row.OldestPendingKnown = pendingKnown
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
	pendingAt, pendingKnown, err := s.oldestXPhotoOCRPendingTimestamp(ctx, candidateWhere+` AND `+xPhotoOCRPendingWhere())
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
	row.OldestPendingAt = pendingAt
	row.OldestPendingKnown = pendingKnown
	return row, true, nil
}

type mediaPendingCandidate struct {
	status, itemUpdatedAt, stageUpdatedAt, attemptAt string
	mediaDownloadedAt                                []string
}

func (s *Store) oldestXMediaPendingTimestamp(ctx context.Context, where string, args ...any) (time.Time, bool, error) {
	status := itemXMediaTranscriptStatusExpr()
	completedAt := itemXMediaTranscriptCompletedAtExpr()
	stageUpdatedAt := itemEnrichmentFieldExpr(model.ItemEnrichmentRoleXMediaTranscript, "updated_at", "''")
	rows, err := s.queryer().QueryContext(ctx, `SELECT items.id, `+status+`, items.updated_at, `+stageUpdatedAt+`, `+completedAt+`, a.downloaded_at
		FROM items
		JOIN item_media_links l ON l.item_id = items.id
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE `+where+`
			AND a.download_status = '`+model.MediaDownloadStatusDownloaded+`'
			AND a.local_path != '' AND a.local_pruned_at = ''
			AND a.media_type IN ('video', 'animated_gif')
		ORDER BY items.id`, args...)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("list transcription pending timestamps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanMediaPendingTimestamps(rows, model.XMediaTranscriptStatusError, true)
}

func (s *Store) oldestXPhotoOCRPendingTimestamp(ctx context.Context, where string, args ...any) (time.Time, bool, error) {
	status := itemOCRStatusExpr()
	stageUpdatedAt := itemEnrichmentFieldExpr(model.ItemEnrichmentRoleOCR, "updated_at", "''")
	rows, err := s.queryer().QueryContext(ctx, `SELECT items.id, `+status+`, items.updated_at, `+stageUpdatedAt+`, items.ocr_at, a.downloaded_at
		FROM items
		JOIN item_media_links l ON l.item_id = items.id
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE `+where+`
			AND a.download_status = '`+model.MediaDownloadStatusDownloaded+`'
			AND a.local_path != '' AND a.local_pruned_at = ''
			AND a.media_type = 'photo'
		ORDER BY items.id`, args...)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("list OCR pending timestamps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanMediaPendingTimestamps(rows, model.ItemOCRStatusError, false)
}

func scanMediaPendingTimestamps(rows rowScanner, errorStatus string, errorUsesAttempt bool) (time.Time, bool, error) {
	candidates := map[int64]*mediaPendingCandidate{}
	order := make([]int64, 0)
	for rows.Next() {
		var id int64
		var status, itemUpdatedAt, stageUpdatedAt, attemptAt, mediaDownloadedAt string
		if err := rows.Scan(&id, &status, &itemUpdatedAt, &stageUpdatedAt, &attemptAt, &mediaDownloadedAt); err != nil {
			return time.Time{}, false, fmt.Errorf("scan media pending timestamp: %w", err)
		}
		candidate, ok := candidates[id]
		if !ok {
			candidate = &mediaPendingCandidate{status: status, itemUpdatedAt: itemUpdatedAt, stageUpdatedAt: stageUpdatedAt, attemptAt: attemptAt}
			candidates[id] = candidate
			order = append(order, id)
		}
		candidate.mediaDownloadedAt = append(candidate.mediaDownloadedAt, mediaDownloadedAt)
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, fmt.Errorf("iterate media pending timestamps: %w", err)
	}

	var oldest time.Time
	for _, id := range order {
		candidate := candidates[id]
		var anchor time.Time
		var ok bool
		if strings.TrimSpace(candidate.status) == errorStatus {
			value := candidate.stageUpdatedAt
			if errorUsesAttempt {
				value = candidate.attemptAt
			}
			if strings.TrimSpace(value) == "" {
				value = candidate.attemptAt
			}
			anchor, ok = parsePendingTimestamp(value)
		} else if strings.TrimSpace(candidate.stageUpdatedAt) != "" {
			anchor, ok = parsePendingTimestamp(candidate.stageUpdatedAt)
		} else {
			itemUpdatedAt, itemOK := parsePendingTimestamp(candidate.itemUpdatedAt)
			firstMediaAt, mediaOK := earliestPendingTimestamp(candidate.mediaDownloadedAt)
			if !itemOK || !mediaOK || itemUpdatedAt.After(firstMediaAt) {
				return time.Time{}, false, nil
			}
			anchor, ok = firstMediaAt, true
		}
		if !ok {
			return time.Time{}, false, nil
		}
		if oldest.IsZero() || anchor.Before(oldest) {
			oldest = anchor
		}
	}
	return oldest, !oldest.IsZero(), nil
}

func earliestPendingTimestamp(values []string) (time.Time, bool) {
	var earliest time.Time
	for _, value := range values {
		parsed, ok := parsePendingTimestamp(value)
		if !ok {
			return time.Time{}, false
		}
		if earliest.IsZero() || parsed.Before(earliest) {
			earliest = parsed
		}
	}
	return earliest, !earliest.IsZero()
}

func parsePendingTimestamp(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	return parsed, err == nil
}
