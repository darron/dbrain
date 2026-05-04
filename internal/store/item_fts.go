package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) syncFTSTx(ctx context.Context, tx *sql.Tx, itemID int64, item model.Item) error {
	if !s.hasFTS {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, itemID); err != nil {
		return fmt.Errorf("delete fts row %s: %w", item.SourceKey, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (
		rowid, source_key, title, text, article_title, article_text, author_handle, author_name, primary_category, primary_domain
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, item.SourceKey, item.Title, item.Text, item.ArticleTitle, indexedItemArticleText(item), item.AuthorHandle, item.AuthorName, item.PrimaryCategory, item.PrimaryDomain); err != nil {
		return fmt.Errorf("insert fts row %s: %w", item.SourceKey, err)
	}
	return nil
}

type RebuildFTSStats struct {
	Rebuilt int
	Skipped int
	Errors  int
}

func (s *Store) RebuildFTS(ctx context.Context) (RebuildFTSStats, error) {
	if !s.hasFTS {
		return RebuildFTSStats{}, fmt.Errorf("FTS is not enabled")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RebuildFTSStats{}, fmt.Errorf("begin fts rebuild tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts`); err != nil {
		return RebuildFTSStats{}, fmt.Errorf("clear fts table: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT `+itemSelectColumns+` FROM items ORDER BY id ASC`)
	if err != nil {
		return RebuildFTSStats{}, fmt.Errorf("list items for fts rebuild: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stats RebuildFTSStats
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			stats.Errors++
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (
			rowid, source_key, title, text, article_title, article_text, author_handle, author_name, primary_category, primary_domain
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.SourceKey, item.Title, item.Text, item.ArticleTitle, indexedItemArticleText(item),
			item.AuthorHandle, item.AuthorName, item.PrimaryCategory, item.PrimaryDomain); err != nil {
			stats.Errors++
			continue
		}
		stats.Rebuilt++
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	return stats, tx.Commit()
}

func indexedItemArticleText(item model.Item) string {
	parts := make([]string, 0, 4)
	for _, value := range []string{strings.TrimSpace(item.XPostText), strings.TrimSpace(item.ArticleText), strings.TrimSpace(item.SummaryText), strings.TrimSpace(item.OCRText), strings.TrimSpace(item.UserTags)} {
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n\n")
}

func (s *Store) syncItemFTSByIDTx(ctx context.Context, tx *sql.Tx, itemID int64) error {
	row := tx.QueryRowContext(ctx, `SELECT `+itemSelectColumns+` FROM items WHERE id = ?`, itemID)
	var item model.Item
	if err := scanItem(row, &item); err != nil {
		return fmt.Errorf("load item %d for fts sync: %w", itemID, err)
	}
	return s.syncFTSTx(ctx, tx, itemID, item)
}
