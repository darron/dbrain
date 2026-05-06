package store

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListSourcesForItem(ctx context.Context, itemID int64) ([]model.ItemSourceRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.source_key, s.canonical_url, s.source_type, s.title, s.note_path, s.extract_status, s.summary_status, s.user_tags
		FROM item_source_links l
		JOIN sources s ON s.id = l.source_id
		WHERE l.item_id = ?
		ORDER BY s.updated_at DESC, s.id DESC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list item sources %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.ItemSourceRef
	for rows.Next() {
		var ref model.ItemSourceRef
		if err := rows.Scan(&ref.SourceID, &ref.SourceKey, &ref.CanonicalURL, &ref.SourceType, &ref.Title, &ref.NotePath, &ref.ExtractStatus, &ref.SummaryStatus, &ref.UserTags); err != nil {
			return nil, fmt.Errorf("scan item source ref %d: %w", itemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item source refs %d: %w", itemID, err)
	}
	return refs, nil
}

func (s *Store) ListBacklinksForSource(ctx context.Context, sourceID int64) ([]model.SourceBacklink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.source_key, i.source_type, i.canonical_url, i.title, i.note_path, i.author_handle, i.author_name, i.published_at, i.user_tags
		FROM item_source_links l
		JOIN items i ON i.id = l.item_id
		WHERE l.source_id = ?
		ORDER BY i.last_seen_at DESC, i.id DESC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list source backlinks %d: %w", sourceID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.SourceBacklink
	for rows.Next() {
		var ref model.SourceBacklink
		if err := rows.Scan(&ref.ItemID, &ref.SourceKey, &ref.SourceType, &ref.CanonicalURL, &ref.Title, &ref.NotePath, &ref.AuthorHandle, &ref.AuthorName, &ref.PublishedAt, &ref.UserTags); err != nil {
			return nil, fmt.Errorf("scan source backlink %d: %w", sourceID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source backlinks %d: %w", sourceID, err)
	}
	return refs, nil
}
