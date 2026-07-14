package store

import "github.com/darron/dbrain/internal/model"

func itemEnrichmentFieldExpr(role string, field string, fallback string) string {
	return `(COALESCE((SELECT ` + field + ` FROM item_enrichments e WHERE e.item_id = items.id AND e.role = '` + role + `'), ` + fallback + `))`
}

func itemSummaryStatusExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleSummary, "status", "summary_status")
}

func itemSummaryTextExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleSummary, "text", "summary_text")
}

func itemOCRStatusExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleOCR, "status", "ocr_status")
}

func itemOCRTextExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleOCR, "text", "ocr_text")
}

func itemXMediaTranscriptStatusExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleXMediaTranscript, "status", "x_media_transcript_status")
}

func itemXMediaTranscriptTextExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleXMediaTranscript, "text", `CASE WHEN article_title = '`+model.XMediaTranscriptArticleTitle+`' THEN article_text ELSE '' END`)
}

func itemXMediaTranscriptCompletedAtExpr() string {
	return itemEnrichmentFieldExpr(model.ItemEnrichmentRoleXMediaTranscript, "completed_at", "x_media_transcript_at")
}
