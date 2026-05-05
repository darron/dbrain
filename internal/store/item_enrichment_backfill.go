package store

import (
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) backfillItemEnrichments() error {
	nowText := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO item_enrichments (
			item_id, role, status, text, raw_json, error, model, prompt_version,
			tool, tool_version, input_hash, completed_at, created_at, updated_at
		)
		SELECT id, ?, summary_status, summary_text, summary_json, summary_error,
			summary_model, summary_prompt_version, summary_tool, summary_tool_version,
			summary_input_hash, summarized_at,
			COALESCE(NULLIF(imported_at, ''), NULLIF(updated_at, ''), ?),
			COALESCE(NULLIF(updated_at, ''), NULLIF(imported_at, ''), ?)
		FROM items
		WHERE trim(summary_text) != ''
			OR trim(summary_json) != ''
			OR trim(summary_status) != ''
			OR trim(summary_error) != ''
			OR trim(summary_model) != ''
			OR trim(summary_tool) != ''`,
		model.ItemEnrichmentRoleSummary,
		nowText,
		nowText,
	); err != nil {
		return fmt.Errorf("backfill item summary enrichments: %w", err)
	}

	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO item_enrichments (
			item_id, role, status, text, raw_json, error, model, prompt_version,
			tool, tool_version, input_hash, completed_at, created_at, updated_at
		)
		SELECT id, ?, ocr_status, ocr_text, ocr_json, ocr_error,
			ocr_model, '', ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at,
			COALESCE(NULLIF(imported_at, ''), NULLIF(updated_at, ''), ?),
			COALESCE(NULLIF(updated_at, ''), NULLIF(imported_at, ''), ?)
		FROM items
		WHERE trim(ocr_text) != ''
			OR trim(ocr_json) != ''
			OR trim(ocr_status) != ''
			OR trim(ocr_error) != ''
			OR trim(ocr_model) != ''
			OR trim(ocr_tool) != ''`,
		model.ItemEnrichmentRoleOCR,
		nowText,
		nowText,
	); err != nil {
		return fmt.Errorf("backfill item ocr enrichments: %w", err)
	}

	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO item_enrichments (
			item_id, role, status, text, raw_json, error, model, prompt_version,
			tool, tool_version, input_hash, completed_at, created_at, updated_at
		)
		SELECT id, ?, x_media_transcript_status,
			CASE WHEN article_title = ? THEN article_text ELSE '' END,
			'', x_media_transcript_error, '', '', '', '', '', x_media_transcript_at,
			COALESCE(NULLIF(imported_at, ''), NULLIF(updated_at, ''), ?),
			COALESCE(NULLIF(updated_at, ''), NULLIF(imported_at, ''), ?)
		FROM items
		WHERE article_title = ?
			OR trim(x_media_transcript_status) != ''
			OR trim(x_media_transcript_error) != ''`,
		model.ItemEnrichmentRoleXMediaTranscript,
		model.XMediaTranscriptArticleTitle,
		nowText,
		nowText,
		model.XMediaTranscriptArticleTitle,
	); err != nil {
		return fmt.Errorf("backfill x media transcript enrichments: %w", err)
	}

	return nil
}
