package store

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/model"
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

// ListCategorizedSources returns all sources that have a non-empty user_tags field.
func (s *Store) ListCategorizedSources(ctx context.Context) ([]model.SourceDocument, error) {
	query := `SELECT ` + sourceSelectColumns + ` FROM sources WHERE user_tags != '' ORDER BY id ASC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list categorized sources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan categorized source: %w", err)
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
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

// ListSourcesForCategorize returns sources ordered newest-first.
// When force is false only sources with an empty user_tags field are returned.
func (s *Store) ListSourcesForCategorize(ctx context.Context, limit int, force bool) ([]model.SourceDocument, error) {
	query := `SELECT ` + sourceSelectColumns + ` FROM sources`
	if !force {
		query += ` WHERE user_tags = ''`
	}
	query += ` ORDER BY updated_at DESC, id DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list sources for categorize: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan categorize source: %w", err)
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}
