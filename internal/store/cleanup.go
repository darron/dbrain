package store

import (
	"context"
	"fmt"
	"strings"
)

type DeleteItemsResult struct {
	Count     int      `json:"count"`
	NotePaths []string `json:"note_paths"`
	SourceIDs []int64  `json:"source_ids"`
}

type DeleteSourcesResult struct {
	Count     int      `json:"count"`
	NotePaths []string `json:"note_paths"`
}

func (s *Store) DeleteItemsBySourceType(ctx context.Context, sourceType string) (DeleteItemsResult, error) {
	sourceType = strings.TrimSpace(sourceType)
	if sourceType == "" {
		return DeleteItemsResult{}, fmt.Errorf("source type cannot be empty")
	}
	return withBusyRetry(ctx, func() (DeleteItemsResult, error) {
		return s.deleteItemsBySourceType(ctx, sourceType)
	})
}

func (s *Store) deleteItemsBySourceType(ctx context.Context, sourceType string) (DeleteItemsResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteItemsResult{}, fmt.Errorf("begin delete items tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result := DeleteItemsResult{}

	itemRows, err := tx.QueryContext(ctx, `SELECT id, note_path FROM items WHERE source_type = ? ORDER BY id`, sourceType)
	if err != nil {
		return DeleteItemsResult{}, fmt.Errorf("query items for deletion %s: %w", sourceType, err)
	}
	var itemIDs []int64
	for itemRows.Next() {
		var itemID int64
		var notePath string
		if scanErr := itemRows.Scan(&itemID, &notePath); scanErr != nil {
			_ = itemRows.Close()
			return DeleteItemsResult{}, fmt.Errorf("scan item for deletion %s: %w", sourceType, scanErr)
		}
		itemIDs = append(itemIDs, itemID)
		if strings.TrimSpace(notePath) != "" {
			result.NotePaths = append(result.NotePaths, notePath)
		}
	}
	if rowsErr := itemRows.Err(); rowsErr != nil {
		_ = itemRows.Close()
		return DeleteItemsResult{}, fmt.Errorf("iterate items for deletion %s: %w", sourceType, rowsErr)
	}
	_ = itemRows.Close()

	sourceRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT l.source_id
		FROM item_source_links l
		JOIN items i ON i.id = l.item_id
		WHERE i.source_type = ?
		ORDER BY l.source_id`, sourceType)
	if err != nil {
		return DeleteItemsResult{}, fmt.Errorf("query linked sources for deletion %s: %w", sourceType, err)
	}
	for sourceRows.Next() {
		var sourceID int64
		if scanErr := sourceRows.Scan(&sourceID); scanErr != nil {
			_ = sourceRows.Close()
			return DeleteItemsResult{}, fmt.Errorf("scan linked source for deletion %s: %w", sourceType, scanErr)
		}
		result.SourceIDs = append(result.SourceIDs, sourceID)
	}
	if rowsErr := sourceRows.Err(); rowsErr != nil {
		_ = sourceRows.Close()
		return DeleteItemsResult{}, fmt.Errorf("iterate linked sources for deletion %s: %w", sourceType, rowsErr)
	}
	_ = sourceRows.Close()

	for _, itemID := range itemIDs {
		if s.hasFTS {
			if _, execErr := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, itemID); execErr != nil {
				return DeleteItemsResult{}, fmt.Errorf("delete item fts row %d: %w", itemID, execErr)
			}
		}
	}

	deleteResult, err := tx.ExecContext(ctx, `DELETE FROM items WHERE source_type = ?`, sourceType)
	if err != nil {
		return DeleteItemsResult{}, fmt.Errorf("delete items %s: %w", sourceType, err)
	}
	rowsAffected, err := deleteResult.RowsAffected()
	if err != nil {
		return DeleteItemsResult{}, fmt.Errorf("rows affected deleting items %s: %w", sourceType, err)
	}
	result.Count = int(rowsAffected)

	if commitErr := tx.Commit(); commitErr != nil {
		return DeleteItemsResult{}, fmt.Errorf("commit delete items %s: %w", sourceType, commitErr)
	}
	return result, nil
}

func (s *Store) DeleteOrphanSources(ctx context.Context, sourceType string) (DeleteSourcesResult, error) {
	sourceType = strings.TrimSpace(sourceType)
	return withBusyRetry(ctx, func() (DeleteSourcesResult, error) {
		return s.deleteOrphanSources(ctx, sourceType)
	})
}

func (s *Store) deleteOrphanSources(ctx context.Context, sourceType string) (DeleteSourcesResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteSourcesResult{}, fmt.Errorf("begin delete orphan sources tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	query := `
		SELECT s.id, s.note_path
		FROM sources s
		LEFT JOIN item_source_links l ON l.source_id = s.id
		WHERE l.source_id IS NULL`
	args := []any{}
	if sourceType != "" {
		query += ` AND s.source_type = ?`
		args = append(args, sourceType)
	}
	query += ` ORDER BY s.id`

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return DeleteSourcesResult{}, fmt.Errorf("query orphan sources: %w", err)
	}

	var sourceIDs []int64
	result := DeleteSourcesResult{}
	for rows.Next() {
		var sourceID int64
		var notePath string
		if scanErr := rows.Scan(&sourceID, &notePath); scanErr != nil {
			return DeleteSourcesResult{}, fmt.Errorf("scan orphan source: %w", scanErr)
		}
		sourceIDs = append(sourceIDs, sourceID)
		if strings.TrimSpace(notePath) != "" {
			result.NotePaths = append(result.NotePaths, notePath)
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return DeleteSourcesResult{}, fmt.Errorf("iterate orphan sources: %w", rowsErr)
	}
	_ = rows.Close()

	for _, sourceID := range sourceIDs {
		_, _ = tx.ExecContext(ctx, `DELETE FROM sources_fts WHERE rowid = ?`, sourceID)
		if _, execErr := tx.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, sourceID); execErr != nil {
			return DeleteSourcesResult{}, fmt.Errorf("delete orphan source %d: %w", sourceID, execErr)
		}
		result.Count++
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return DeleteSourcesResult{}, fmt.Errorf("commit delete orphan sources: %w", commitErr)
	}
	return result, nil
}
