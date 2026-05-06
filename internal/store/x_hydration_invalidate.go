package store

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *Store) invalidateLinkedXArticleSourcesTx(ctx context.Context, tx *sql.Tx, itemID int64, nowText string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources
		SET extracted_text = '',
			extract_json = '',
			extract_status = '',
			extract_error = '',
			extract_failure_kind = '',
			extract_failure_count = 0,
			extract_first_failed_at = '',
			extract_last_failed_at = '',
			extracted_at = '',
			extract_tool = '',
			extract_tool_version = '',
			summary_text = '',
			summary_json = '',
			summary_status = '',
			summary_error = '',
			summary_model = '',
			summary_content_hash = '',
			summary_prompt_version = '',
			summary_tool = '',
			summary_tool_version = '',
			summarized_at = '',
			content_hash = '',
			updated_at = ?
		WHERE id IN (
			SELECT l.source_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			WHERE l.item_id = ?
				AND s.source_type = 'x_article'
		)`,
		nowText,
		itemID,
	); err != nil {
		return fmt.Errorf("invalidate linked x article sources for item %d: %w", itemID, err)
	}
	return nil
}
