package store

import (
	"context"
	"fmt"

	"dbrain/internal/model"
)

// ListCategorizedItems returns all items that have a non-empty user_tags field.
func (s *Store) ListCategorizedItems(ctx context.Context) ([]model.Item, error) {
	query := `SELECT ` + itemSelectColumns + ` FROM items WHERE user_tags != '' ORDER BY id ASC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list categorized items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan categorized item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListItemsForCategorize returns items ordered newest-first.
// When force is false only items with an empty user_tags field are returned.
func (s *Store) ListItemsForCategorize(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	query := `SELECT ` + itemSelectColumns + ` FROM items`
	if !force {
		query += ` WHERE user_tags = ''`
	}
	query += ` ORDER BY imported_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list items for categorize: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan categorize item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
