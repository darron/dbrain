package store

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListItemsForXHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	return s.listItemsForXHydration(ctx, limit, force, xItemSourceTypeWhere)
}

func (s *Store) ListItemsForXQuoteHydration(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE source_type = 'x_quote'
			AND external_id != ''`
	if !force {
		query += `
			AND (
				x_post_status = ''
				OR x_post_status = 'api_error'
				OR x_post_status = 'error'
				OR x_post_status = 'rate_limited'
				OR ` + xHydrationRepairWhere + `
			)`
	}
	query += `
		ORDER BY
			CASE WHEN x_post_status = '' THEN 0 ELSE 1 END,
			last_seen_at DESC,
			x_post_fetched_at ASC,
			id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x quote hydration items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x quote hydration item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x quote hydration items: %w", err)
	}

	return items, nil
}

func (s *Store) listItemsForXHydration(ctx context.Context, limit int, force bool, sourceWhere string) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + sourceWhere + `
			AND external_id != ''`
	if !force {
		query += `
				AND ` + xHydrationCandidateWhere
	}
	query += `
		ORDER BY
			CASE WHEN x_post_status = '' THEN 0 ELSE 1 END,
			last_seen_at DESC,
			x_post_fetched_at ASC,
			id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list x hydration items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x hydration item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x hydration items: %w", err)
	}

	return items, nil
}
