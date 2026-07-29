package store

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) listXArticleRehydrateItemIDs(ctx context.Context, sourceIDs []int64) ([]int64, error) {
	sourceIDs = uniquePositiveInt64s(sourceIDs)
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, 0, len(sourceIDs))
	args := make([]any, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		placeholders = append(placeholders, "?")
		args = append(args, sourceID)
	}
	sourceClause := strings.Join(placeholders, ",")

	rows, err := s.db.QueryContext(ctx, `
		WITH linked_items AS (
			SELECT i.id AS item_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			JOIN items i ON i.id = l.item_id
			WHERE s.source_type = 'x_article'
				AND s.id IN (`+sourceClause+`)
				AND (i.source_type = 'x_bookmark' OR i.source_type = 'x_quote')

			UNION

			SELECT p.id AS item_id
			FROM item_source_links l
			JOIN sources s ON s.id = l.source_id
			JOIN items i ON i.id = l.item_id
			JOIN item_item_links q ON q.child_item_id = i.id AND q.link_kind = 'quoted_post'
			JOIN items p ON p.id = q.parent_item_id
			WHERE s.source_type = 'x_article'
				AND s.id IN (`+sourceClause+`)
				AND (p.source_type = 'x_bookmark' OR p.source_type = 'x_quote')
		)
		SELECT DISTINCT item_id
		FROM linked_items
		ORDER BY item_id ASC`, append(args, args...)...)
	if err != nil {
		return nil, fmt.Errorf("list x article rehydrate items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var itemIDs []int64
	for rows.Next() {
		var itemID int64
		if err := rows.Scan(&itemID); err != nil {
			return nil, fmt.Errorf("scan x article rehydrate item: %w", err)
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x article rehydrate items: %w", err)
	}
	return itemIDs, nil
}

func (s *Store) resetXArticleHydrationItems(ctx context.Context, itemIDs []int64, nowText string) (int, error) {
	itemIDs = uniquePositiveInt64s(itemIDs)
	if len(itemIDs) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(itemIDs))
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, nowText)
	for _, itemID := range itemIDs {
		placeholders = append(placeholders, "?")
		args = append(args, itemID)
	}

	return withAuthoritativeWriteTx(ctx, s, "reset-x-article-hydration", func(ctx context.Context, tx authoritativeWriteTx) (int, error) {
		result, err := tx.ExecContext(ctx, `
			UPDATE items
			SET x_post_text = '',
				x_post_lang = '',
				x_post_json = '',
				x_post_fetched_at = '',
				x_post_status = '',
				x_post_error = '',
				link_extract_synced_at = '',
				updated_at = ?
			WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return 0, fmt.Errorf("reset x article hydration items: %w", err)
		}

		for _, itemID := range itemIDs {
			if _, err := s.invalidateItemSummaryTx(ctx, tx, itemID, nowText); err != nil {
				return 0, err
			}
			if err := s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
				return 0, err
			}
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return len(itemIDs), nil
		}
		return int(rowsAffected), nil
	})
}
