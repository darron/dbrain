package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

var ErrItemNotFound = errors.New("item not found")

func (s *Store) GetItem(ctx context.Context, lookup string) (model.Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			`+itemSelectColumns+`
		FROM items
		WHERE source_key = ?
			OR external_id = ?
			OR canonical_url = ?
			OR note_path = ?
		LIMIT 1`, lookup, lookup, lookup, lookup)

	var item model.Item
	if err := scanItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Item{}, fmt.Errorf("%w: %s", ErrItemNotFound, lookup)
		}
		return model.Item{}, fmt.Errorf("load item %s: %w", lookup, err)
	}
	if err := s.applyItemEnrichmentMirror(ctx, &item); err != nil {
		return model.Item{}, err
	}

	media, err := s.ListItemMediaRefs(ctx, item.ID)
	if err != nil {
		return model.Item{}, err
	}
	item.Media = media

	return item, nil
}

func (s *Store) GetItemByID(ctx context.Context, id int64) (model.Item, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+itemSelectColumns+` FROM items WHERE id = ?`, id)
	var item model.Item
	if err := scanItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Item{}, fmt.Errorf("%w: %d", ErrItemNotFound, id)
		}
		return model.Item{}, fmt.Errorf("load item %d: %w", id, err)
	}
	if err := s.applyItemEnrichmentMirror(ctx, &item); err != nil {
		return model.Item{}, err
	}
	media, err := s.ListItemMediaRefs(ctx, item.ID)
	if err != nil {
		return model.Item{}, err
	}
	item.Media = media
	return item, nil
}

func (s *Store) SaveItemUserTags(ctx context.Context, itemID int64, tags string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `UPDATE items SET user_tags = ? WHERE id = ?`, tags, itemID); err != nil {
		return fmt.Errorf("update user_tags for item %d: %w", itemID, err)
	}
	if err = s.syncItemFTSByIDTx(ctx, tx, itemID); err != nil {
		return fmt.Errorf("sync fts for item %d: %w", itemID, err)
	}
	return tx.Commit()
}

func (s *Store) ListAllItems(ctx context.Context, limit int) ([]model.Item, error) {
	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE note_path != ''
		ORDER BY id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list all items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan item row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate items: %w", err)
	}
	return items, nil
}

func (s *Store) ListAllEntityItems(ctx context.Context, limit int) ([]model.Item, error) {
	query := `
		SELECT id, source_key, source_type, canonical_url, title, author_handle, author_name, note_path
		FROM items
		WHERE note_path != ''
		ORDER BY id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entity items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := rows.Scan(
			&item.ID,
			&item.SourceKey,
			&item.SourceType,
			&item.CanonicalURL,
			&item.Title,
			&item.AuthorHandle,
			&item.AuthorName,
			&item.NotePath,
		); err != nil {
			return nil, fmt.Errorf("scan entity item row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity items: %w", err)
	}
	return items, nil
}
