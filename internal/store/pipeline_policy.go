package store

import (
	"time"

	"github.com/darron/dbrain/internal/model"
)

const xMediaTranscriptionErrorRetryCooldown = 24 * time.Hour

const xHydrationTerminalWhere = `x_post_status IN ('not_found', 'empty')`

// A forbidden response records provider/access denial, not confirmed content
// absence. It is non-runnable without force, so report it as blocked rather
// than terminal or unknown.
const xHydrationBlockedWhere = `x_post_status = 'forbidden'`

const xPhotoOCRAnyMediaExistsWhere = `EXISTS (
	SELECT 1 FROM item_media_links l
	JOIN media_assets a ON a.id = l.media_asset_id
	WHERE l.item_id = items.id
		AND a.download_status = '` + model.MediaDownloadStatusDownloaded + `'
		AND a.media_type = 'photo'
)`

const xPhotoOCRRunnableMediaExistsWhere = `EXISTS (
	SELECT 1 FROM item_media_links l
	CROSS JOIN media_assets a
	WHERE l.item_id = items.id
		AND a.id = l.media_asset_id
		AND a.download_status = '` + model.MediaDownloadStatusDownloaded + `'
		AND a.local_path != ''
		AND a.local_pruned_at = ''
		AND a.media_type = 'photo'
)`

func xMediaTranscriptionPendingWhere(now time.Time) (string, []any) {
	status := itemXMediaTranscriptStatusExpr()
	text := itemXMediaTranscriptTextExpr()
	completedAt := itemXMediaTranscriptCompletedAtExpr()
	return `(` + text + ` = '' AND ` + xMediaTranscriptionRunnableMediaExistsWhere + ` AND (
		` + status + ` = '' OR (
			` + status + ` = '` + model.XMediaTranscriptStatusError + `'
			AND ` + completedAt + ` != '' AND ` + completedAt + ` <= ?
		)
	))`, []any{now.UTC().Add(-xMediaTranscriptionErrorRetryCooldown).Format(time.RFC3339)}
}

func xPhotoOCRPendingWhere() string {
	status := itemOCRStatusExpr()
	return `(` + xPhotoOCRRunnableMediaExistsWhere + ` AND (` + status + ` = '' OR ` + status + ` = '` + model.ItemOCRStatusError + `'))`
}

func mediaArchiveBaseWhere(alias string) string {
	return alias + `.download_status = '` + model.MediaDownloadStatusDownloaded + `'
		AND ` + alias + `.local_path != ''
		AND ` + alias + `.local_pruned_at = ''
		AND ` + mediaArchiveSupportedOwnerExistsWhere(alias)
}

func mediaArchiveEnrichmentCompleteWhere(alias string) string {
	return `((` + alias + `.media_type = 'photo' AND NOT EXISTS (
		SELECT 1 FROM item_media_links l JOIN items i ON i.id = l.item_id
		WHERE l.media_asset_id = ` + alias + `.id
			AND ` + mediaEnrichmentItemSourceTypeWhereFor("i") + `
			AND i.ocr_status != '` + model.ItemOCRStatusOK + `'
	)) OR (` + alias + `.media_type IN ('video', 'animated_gif') AND NOT EXISTS (
		SELECT 1 FROM item_media_links l JOIN items i ON i.id = l.item_id
		WHERE l.media_asset_id = ` + alias + `.id
			AND ` + mediaEnrichmentItemSourceTypeWhereFor("i") + `
			AND i.x_media_transcript_status NOT IN ('` + model.XMediaTranscriptStatusOK + `', '` + model.XMediaTranscriptStatusNoAudio + `', '` + model.XMediaTranscriptStatusNoise + `', '` + model.XMediaTranscriptStatusTooShort + `', '` + model.XMediaTranscriptStatusEmpty + `')
	)) OR ` + alias + `.media_type = 'audio')`
}

func mediaArchiveSupportedOwnerExistsWhere(alias string) string {
	return `EXISTS (
		SELECT 1 FROM item_media_links l
		JOIN items i ON i.id = l.item_id
		WHERE l.media_asset_id = ` + alias + `.id
			AND ` + mediaEnrichmentItemSourceTypeWhereFor("i") + `
	)`
}

func mediaArchiveCandidateWhere(alias string, force bool) string {
	where := mediaArchiveBaseWhere(alias) + ` AND ` + mediaArchiveEnrichmentCompleteWhere(alias)
	if !force {
		where += ` AND (` + alias + `.archive_status = '' OR ` + alias + `.archive_status = '` + model.MediaArchiveStatusError + `')`
	}
	return where
}

func (p sourceEnrichmentPolicy) extractionTerminalWhere() (string, []any) {
	pendingWhere, args := p.extractBacklogWhere()
	return `extract_status IN ('` + model.SourceExtractStatusDead + `', '` + model.SourceExtractStatusGone + `') AND NOT (` + pendingWhere + `)`, args
}

func (p sourceEnrichmentPolicy) extractionBlockedWhere() (string, []any) {
	pendingWhere, args := p.extractBacklogWhere()
	return `extract_status = '` + model.SourceExtractStatusError + `' AND NOT (` + pendingWhere + `)`, args
}
