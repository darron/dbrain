package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

// AuditLocalIdentifierRow is app-only diagnostic evidence. It is intentionally
// excluded from the portable audit report schema.
type AuditLocalIdentifierRow struct {
	RowID     int64
	SourceKey string
}

// LocalIdentifierRows returns identifiers only for a closed set of fixed,
// read-only audit queries. Callers cannot supply SQL or cleanup paths.
func (s *AuditReadSnapshot) LocalIdentifierRows(ctx context.Context, checkID string, limit int) ([]AuditLocalIdentifierRow, error) {
	reader, err := s.query(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 101 {
		limit = 101
	}

	query, args, ok := auditLocalIdentifierQuery(checkID, time.Now().UTC())
	if !ok {
		return nil, fmt.Errorf("unsupported local audit identifier check %q", checkID)
	}
	args = append(args, limit)
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query local audit identifiers for %s: %w", checkID, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]AuditLocalIdentifierRow, 0)
	for rows.Next() {
		var row AuditLocalIdentifierRow
		if err := rows.Scan(&row.RowID, &row.SourceKey); err != nil {
			return nil, fmt.Errorf("scan local audit identifiers for %s: %w", checkID, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local audit identifiers for %s: %w", checkID, err)
	}
	return out, nil
}

func auditLocalIdentifierQuery(checkID string, now time.Time) (string, []any, bool) {
	const itemSelect = `SELECT items.id, items.source_key FROM items WHERE `
	const itemOrderLimit = ` ORDER BY items.id ASC LIMIT ?`
	const sourceSelect = `SELECT sources.id, sources.source_key FROM sources WHERE `
	const sourceOrderLimit = ` ORDER BY sources.id ASC LIMIT ?`

	switch checkID {
	case "pipeline.hydration.partition", "pipeline.hydration.pending_age":
		return itemSelect + xItemSourceTypeWhere + ` AND external_id != '' AND ` + xHydrationCandidateWhere + itemOrderLimit, nil, true
	case "pipeline.extraction.partition", "pipeline.extraction.pending_age":
		where, args := newSourceEnrichmentPolicy(now, "", "", "").extractBacklogWhere()
		return sourceSelect + where + sourceOrderLimit, args, true
	case "pipeline.summary.partition", "pipeline.summary.pending_age":
		where, args := newSourceEnrichmentPolicy(now, "", "", "").summaryBacklogWhere()
		return sourceSelect + where + sourceOrderLimit, args, true
	case "pipeline.transcription.partition", "pipeline.transcription.pending_age":
		where, args := xMediaTranscriptionPendingWhere(now)
		return itemSelect + xItemSourceTypeWhere + ` AND external_id != '' AND ` + where + itemOrderLimit, args, true
	case "pipeline.ocr.partition", "pipeline.ocr.pending_age":
		return itemSelect + xItemSourceTypeWhere + ` AND external_id != '' AND ` + xPhotoOCRPendingWhere() + itemOrderLimit, nil, true
	case "durability.media_local_coverage":
		return `SELECT a.id, COALESCE(MIN(i.source_key), '')
			FROM media_assets a
			LEFT JOIN item_media_links l ON l.media_asset_id = a.id
			LEFT JOIN items i ON i.id = l.item_id
			WHERE (a.local_pruned_at != ''
				AND EXISTS (SELECT 1 FROM item_media_links linked WHERE linked.media_asset_id = a.id)
				AND NOT ` + mediaArchiveEnrichmentCompleteWhere("a") + `)
			OR (a.download_status = '` + model.MediaDownloadStatusDownloaded + `'
				AND NOT EXISTS (SELECT 1 FROM item_media_links orphan WHERE orphan.media_asset_id = a.id))
			GROUP BY a.id ORDER BY a.id ASC LIMIT ?`, nil, true
	case "pipeline.item_summary.provenance":
		return auditItemProvenanceIdentifierQuery(model.ItemEnrichmentRoleSummary, model.ItemSummaryStatusOK,
			[]string{"raw_json", "model", "prompt_version", "tool", "tool_version", "input_hash", "completed_at"}), nil, true
	case "pipeline.item_ocr.provenance":
		return auditItemProvenanceIdentifierQuery(model.ItemEnrichmentRoleOCR, model.ItemOCRStatusOK,
			[]string{"raw_json", "model", "tool", "tool_version", "input_hash", "completed_at"}), nil, true
	case "pipeline.x_media_transcript.provenance":
		return auditItemProvenanceIdentifierQuery(model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusOK,
			[]string{"raw_json", "model", "tool", "tool_version", "input_hash", "completed_at"}), nil, true
	case "pipeline.source_summary.provenance":
		return `SELECT DISTINCT sources.id, sources.source_key
			FROM source_summary_versions versions
			JOIN sources ON sources.id = versions.source_id
			WHERE versions.summary_status = '` + model.SourceSummaryStatusOK + `'
			AND (` + auditBlankColumns("versions", []string{"summary_json", "summary_model", "summary_prompt_version", "summary_tool", "summary_tool_version", "content_hash", "summarized_at"}) + `)
			ORDER BY sources.id ASC LIMIT ?`, nil, true
	default:
		return "", nil, false
	}
}

func auditItemProvenanceIdentifierQuery(role, status string, fields []string) string {
	return `SELECT items.id, items.source_key
		FROM item_enrichments enrichments
		JOIN items ON items.id = enrichments.item_id
		WHERE enrichments.role = '` + role + `' AND enrichments.status = '` + status + `'
		AND (` + auditBlankColumns("enrichments", fields) + `)
		ORDER BY items.id ASC LIMIT ?`
}

func auditBlankColumns(alias string, fields []string) string {
	where := ""
	for _, field := range fields {
		if where != "" {
			where += " OR "
		}
		where += `TRIM(COALESCE(` + alias + `.` + field + `, '')) = ''`
	}
	return where
}
