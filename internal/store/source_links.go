package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListItemsForLinkDiscovery(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 250
	}

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + linkDiscoveryItemSourceTypeWhere + `
			AND links_json != '[]'`
	if !force {
		query += `
			AND (link_extract_synced_at = '' OR updated_at > link_extract_synced_at)`
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list items for link discovery: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan link discovery item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate link discovery items: %w", err)
	}

	return items, nil
}

func (s *Store) MarkItemLinkDiscovery(ctx context.Context, itemID int64, at time.Time) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE items
			SET link_extract_synced_at = ?
			WHERE id = ?`,
			at.UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return struct{}{}, fmt.Errorf("mark item link discovery %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) UpsertSourceLink(ctx context.Context, itemID int64, candidate model.SourceCandidate) (model.SourceLinkUpsertResult, error) {
	return withBusyRetry(ctx, func() (model.SourceLinkUpsertResult, error) {
		return s.upsertSourceLink(ctx, itemID, candidate)
	})
}

func (s *Store) UpsertSource(ctx context.Context, candidate model.SourceCandidate) (model.SourceUpsertResult, error) {
	return withBusyRetry(ctx, func() (model.SourceUpsertResult, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return model.SourceUpsertResult{}, fmt.Errorf("begin source tx: %w", err)
		}
		defer func() {
			if err != nil {
				_ = tx.Rollback()
			}
		}()

		sourceID, sourceCreated, err := upsertSourceCandidate(ctx, tx, candidate)
		if err != nil {
			return model.SourceUpsertResult{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return model.SourceUpsertResult{}, fmt.Errorf("commit source: %w", commitErr)
		}
		return model.SourceUpsertResult{SourceID: sourceID, SourceCreated: sourceCreated}, nil
	})
}

func (s *Store) SourceHasLinkedItemType(ctx context.Context, sourceID int64, sourceType string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM item_source_links l
		JOIN items i ON i.id = l.item_id
		WHERE l.source_id = ?
			AND i.source_type = ?
		LIMIT 1`, sourceID, sourceType).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check source linked item type %d %s: %w", sourceID, sourceType, err)
	}
	return true, nil
}

func (s *Store) upsertSourceLink(ctx context.Context, itemID int64, candidate model.SourceCandidate) (model.SourceLinkUpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.SourceLinkUpsertResult{}, fmt.Errorf("begin source link tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	sourceID, sourceCreated, err := upsertSourceCandidate(ctx, tx, candidate)
	if err != nil {
		return model.SourceLinkUpsertResult{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	linkResult, execErr := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID,
		sourceID,
		candidate.OriginalURL,
		now,
	)
	if execErr != nil {
		err = fmt.Errorf("link item %d to source %d: %w", itemID, sourceID, execErr)
		return model.SourceLinkUpsertResult{}, err
	}
	rowsAffected, _ := linkResult.RowsAffected()

	if commitErr := tx.Commit(); commitErr != nil {
		return model.SourceLinkUpsertResult{}, fmt.Errorf("commit source link: %w", commitErr)
	}

	return model.SourceLinkUpsertResult{
		SourceID:      sourceID,
		SourceCreated: sourceCreated,
		LinkCreated:   rowsAffected > 0,
	}, nil
}

func upsertSourceCandidate(ctx context.Context, tx *sql.Tx, candidate model.SourceCandidate) (int64, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var sourceID int64

	row := tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE normalized_url = ?`, candidate.NormalizedURL)
	switch scanErr := row.Scan(&sourceID); {
	case errors.Is(scanErr, sql.ErrNoRows):
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO sources (
				source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			candidate.SourceKey,
			candidate.CanonicalURL,
			candidate.NormalizedURL,
			candidate.SourceType,
			candidate.Domain,
			candidate.NotePath,
			now,
			now,
		)
		if execErr != nil {
			return 0, false, fmt.Errorf("insert source candidate %s: %w", candidate.CanonicalURL, execErr)
		}
		sourceID, execErr = result.LastInsertId()
		if execErr != nil {
			return 0, false, fmt.Errorf("last insert id source %s: %w", candidate.CanonicalURL, execErr)
		}
		return sourceID, true, nil
	case scanErr != nil:
		return 0, false, fmt.Errorf("lookup source candidate %s: %w", candidate.CanonicalURL, scanErr)
	default:
		if _, execErr := tx.ExecContext(ctx, `
			UPDATE sources
			SET canonical_url = ?,
				source_type = ?,
				domain = ?,
				note_path = ?,
				updated_at = ?
			WHERE id = ?`,
			candidate.CanonicalURL,
			candidate.SourceType,
			candidate.Domain,
			candidate.NotePath,
			now,
			sourceID,
		); execErr != nil {
			return 0, false, fmt.Errorf("update source candidate %s: %w", candidate.CanonicalURL, execErr)
		}
		return sourceID, false, nil
	}
}
