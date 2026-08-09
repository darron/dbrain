package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type PrunedMediaRepairCandidates struct {
	OCRItemIDs        []int64
	TranscriptItemIDs []int64
}

func (s *Store) ListPrunedMediaRepairCandidates(ctx context.Context, includeOCR, includeTranscripts bool, limit int) (PrunedMediaRepairCandidates, error) {
	result := PrunedMediaRepairCandidates{
		OCRItemIDs:        make([]int64, 0),
		TranscriptItemIDs: make([]int64, 0),
	}
	if limit <= 0 {
		return result, fmt.Errorf("pruned media repair limit must be positive")
	}

	if includeOCR {
		status := itemOCRStatusExpr()
		where := mediaEnrichmentItemSourceTypeWhere + `
			AND external_id != ''
			AND ` + prunedArchivedMediaExistsWhere("photo") + `
			AND NOT ` + xPhotoOCRRunnableMediaExistsWhere + `
			AND (` + status + ` = '' OR ` + status + ` = '` + model.ItemOCRStatusError + `')`
		ids, err := s.listPrunedMediaRepairIDs(ctx, where, limit)
		if err != nil {
			return result, fmt.Errorf("list pruned OCR repair candidates: %w", err)
		}
		result.OCRItemIDs = ids
	}

	if includeTranscripts {
		status := itemXMediaTranscriptStatusExpr()
		text := itemXMediaTranscriptTextExpr()
		completedAt := itemXMediaTranscriptCompletedAtExpr()
		where := mediaEnrichmentItemSourceTypeWhere + `
			AND external_id != ''
			AND ` + prunedArchivedMediaExistsWhere("video", "animated_gif") + `
			AND NOT ` + xMediaTranscriptionRunnableMediaExistsWhere + `
			AND ` + text + ` = ''
			AND (` + status + ` = '' OR (
				` + status + ` = '` + model.XMediaTranscriptStatusError + `'
				AND ` + completedAt + ` != ''
				AND ` + completedAt + ` <= ?
			))`
		cutoff := time.Now().UTC().Add(-xMediaTranscriptionErrorRetryCooldown).Format(time.RFC3339)
		ids, err := s.listPrunedMediaRepairIDs(ctx, where, limit, cutoff)
		if err != nil {
			return result, fmt.Errorf("list pruned transcript repair candidates: %w", err)
		}
		result.TranscriptItemIDs = ids
	}

	return result, nil
}

func prunedArchivedMediaExistsWhere(mediaTypes ...string) string {
	types := ""
	for i, mediaType := range mediaTypes {
		if i > 0 {
			types += ", "
		}
		types += "'" + mediaType + "'"
	}
	return `EXISTS (
		SELECT 1 FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = items.id
			AND a.download_status = '` + model.MediaDownloadStatusDownloaded + `'
			AND a.local_path != ''
			AND a.local_pruned_at != ''
			AND a.archive_status = '` + model.MediaArchiveStatusArchived + `'
			AND a.media_type IN (` + types + `)
	)`
}

func (s *Store) listPrunedMediaRepairIDs(ctx context.Context, where string, limit int, args ...any) ([]int64, error) {
	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, limit)
	rows, err := s.queryer().QueryContext(ctx, `SELECT DISTINCT items.id FROM items WHERE `+where+` ORDER BY items.id LIMIT ?`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
