package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ensureItemLinkTables() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS item_item_links (
			parent_item_id INTEGER NOT NULL,
			child_item_id INTEGER NOT NULL,
			link_kind TEXT NOT NULL DEFAULT '',
			ordinal INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (parent_item_id, child_item_id, link_kind),
			UNIQUE (parent_item_id, link_kind, ordinal),
			FOREIGN KEY (parent_item_id) REFERENCES items(id) ON DELETE CASCADE,
			FOREIGN KEY (child_item_id) REFERENCES items(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_item_item_links_child_item_id ON item_item_links(child_item_id);`,
		`CREATE INDEX IF NOT EXISTS idx_item_item_links_link_kind ON item_item_links(link_kind);`,
	}
	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply item link schema: %w", err)
		}
	}

	existing, err := s.tableColumns("item_item_links")
	if err != nil {
		return fmt.Errorf("load item_item_links table info: %w", err)
	}
	required := map[string]string{
		"link_kind":  "TEXT NOT NULL DEFAULT ''",
		"ordinal":    "INTEGER NOT NULL DEFAULT 0",
		"created_at": "TEXT NOT NULL DEFAULT ''",
		"updated_at": "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range required {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE item_item_links ADD COLUMN %s %s", name, definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add item_item_links.%s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) ReplaceItemChildLinks(ctx context.Context, parentItemID int64, linkKind string, childItemIDs []int64) (bool, error) {
	linkKind = strings.TrimSpace(linkKind)
	if parentItemID <= 0 {
		return false, fmt.Errorf("invalid parent item id: %d", parentItemID)
	}
	if linkKind == "" {
		return false, fmt.Errorf("link kind is required")
	}

	return withBusyRetry(ctx, func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin replace item links tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		current, err := listChildLinksTx(ctx, tx, parentItemID, linkKind)
		if err != nil {
			return false, err
		}
		if sameInt64s(current, childItemIDs) {
			if err := tx.Commit(); err != nil {
				return false, fmt.Errorf("commit unchanged item links: %w", err)
			}
			return false, nil
		}

		if _, err := tx.ExecContext(ctx, `
			DELETE FROM item_item_links
			WHERE parent_item_id = ?
				AND link_kind = ?`,
			parentItemID,
			linkKind,
		); err != nil {
			return false, fmt.Errorf("clear item links parent=%d kind=%s: %w", parentItemID, linkKind, err)
		}

		nowText := time.Now().UTC().Format(time.RFC3339)
		for ordinal, childItemID := range childItemIDs {
			if childItemID <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO item_item_links (
					parent_item_id, child_item_id, link_kind, ordinal, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?)`,
				parentItemID,
				childItemID,
				linkKind,
				ordinal,
				nowText,
				nowText,
			); err != nil {
				return false, fmt.Errorf("insert item link parent=%d child=%d kind=%s: %w", parentItemID, childItemID, linkKind, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit item links parent=%d kind=%s: %w", parentItemID, linkKind, err)
		}
		return true, nil
	})
}

func (s *Store) ListItemChildLinks(ctx context.Context, parentItemID int64, linkKind string) ([]int64, error) {
	return withBusyRetry(ctx, func() ([]int64, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("begin list item links tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		childIDs, err := listChildLinksTx(ctx, tx, parentItemID, strings.TrimSpace(linkKind))
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit list item links: %w", err)
		}
		return childIDs, nil
	})
}

func listChildLinksTx(ctx context.Context, tx *sql.Tx, parentItemID int64, linkKind string) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT child_item_id
		FROM item_item_links
		WHERE parent_item_id = ?
			AND link_kind = ?
		ORDER BY ordinal ASC, child_item_id ASC`,
		parentItemID,
		linkKind,
	)
	if err != nil {
		return nil, fmt.Errorf("list item links parent=%d kind=%s: %w", parentItemID, linkKind, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []int64
	for rows.Next() {
		var childItemID int64
		if err := rows.Scan(&childItemID); err != nil {
			return nil, fmt.Errorf("scan item link parent=%d kind=%s: %w", parentItemID, linkKind, err)
		}
		out = append(out, childItemID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item links parent=%d kind=%s: %w", parentItemID, linkKind, err)
	}
	return out, nil
}

func sameInt64s(left []int64, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
