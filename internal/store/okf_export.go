package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

// OKFExportOptions controls a read-only snapshot used by the OKF projection.
type OKFExportOptions struct {
	IncludeItems   bool
	IncludeSources bool
	SourceTypes    []string
	Limit          int
}

// OKFExportSnapshot is a consistent SQLite view used by the OKF exporter.
type OKFExportSnapshot struct {
	Items           []model.Item                     `json:"items"`
	Sources         []model.SourceDocument           `json:"sources"`
	ItemSources     map[int64][]model.ItemSourceRef  `json:"item_sources"`
	SourceBacklinks map[int64][]model.SourceBacklink `json:"source_backlinks"`
	ItemMedia       map[int64][]model.ItemMediaRef   `json:"item_media"`
	ItemChildren    map[int64][]model.SourceBacklink `json:"item_children"`
}

func (s *Store) LoadOKFExportSnapshot(ctx context.Context, opts OKFExportOptions) (OKFExportSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return OKFExportSnapshot{}, fmt.Errorf("begin okf export snapshot: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	snapshot := OKFExportSnapshot{
		ItemSources:     map[int64][]model.ItemSourceRef{},
		SourceBacklinks: map[int64][]model.SourceBacklink{},
		ItemMedia:       map[int64][]model.ItemMediaRef{},
		ItemChildren:    map[int64][]model.SourceBacklink{},
	}

	sourceTypes := normalizeSourceTypes(opts.SourceTypes)
	if opts.IncludeItems {
		items, err := listOKFItemsTx(ctx, tx, sourceTypes, opts.Limit)
		if err != nil {
			return OKFExportSnapshot{}, err
		}
		for i := range items {
			if err := applyItemEnrichmentMirrorFrom(ctx, tx, &items[i]); err != nil {
				return OKFExportSnapshot{}, err
			}
		}
		snapshot.Items = items
		for _, item := range items {
			refs, err := listSourcesForItemTx(ctx, tx, item.ID)
			if err != nil {
				return OKFExportSnapshot{}, err
			}
			snapshot.ItemSources[item.ID] = refs
			media, err := listItemMediaRefsWithTx(ctx, tx, item.ID)
			if err != nil {
				return OKFExportSnapshot{}, err
			}
			snapshot.ItemMedia[item.ID] = media
			children, err := listItemChildrenAsBacklinksTx(ctx, tx, item.ID)
			if err != nil {
				return OKFExportSnapshot{}, err
			}
			snapshot.ItemChildren[item.ID] = children
		}
	}

	if opts.IncludeSources {
		sources, err := listOKFSourcesTx(ctx, tx, sourceTypes, opts.Limit)
		if err != nil {
			return OKFExportSnapshot{}, err
		}
		snapshot.Sources = sources
		for _, source := range sources {
			backlinks, err := listBacklinksForSourceTx(ctx, tx, source.ID)
			if err != nil {
				return OKFExportSnapshot{}, err
			}
			snapshot.SourceBacklinks[source.ID] = backlinks
		}
	}

	if err := tx.Commit(); err != nil {
		return OKFExportSnapshot{}, fmt.Errorf("commit okf export snapshot: %w", err)
	}
	return snapshot, nil
}

func normalizeSourceTypes(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func listOKFItemsTx(ctx context.Context, tx *sql.Tx, sourceTypes []string, limit int) ([]model.Item, error) {
	query := `SELECT ` + itemSelectColumns + ` FROM items WHERE source_key != ''`
	args := []any{}
	if len(sourceTypes) > 0 {
		query += ` AND source_type IN (` + placeholders(len(sourceTypes)) + `)`
		for _, sourceType := range sourceTypes {
			args = append(args, sourceType)
		}
	}
	query += ` ORDER BY source_type ASC, source_key ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list okf items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan okf item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate okf items: %w", err)
	}
	return items, nil
}

func listOKFSourcesTx(ctx context.Context, tx *sql.Tx, sourceTypes []string, limit int) ([]model.SourceDocument, error) {
	query := `SELECT ` + sourceSelectColumns + ` FROM sources WHERE source_key != ''`
	args := []any{}
	if len(sourceTypes) > 0 {
		query += ` AND source_type IN (` + placeholders(len(sourceTypes)) + `)`
		for _, sourceType := range sourceTypes {
			args = append(args, sourceType)
		}
	}
	query += ` ORDER BY source_type ASC, source_key ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list okf sources: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan okf source: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate okf sources: %w", err)
	}
	return sources, nil
}

func listSourcesForItemTx(ctx context.Context, tx *sql.Tx, itemID int64) ([]model.ItemSourceRef, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.source_key, s.canonical_url, s.source_type, s.title, s.note_path, s.extract_status, s.summary_status, s.user_tags
		FROM item_source_links l
		JOIN sources s ON s.id = l.source_id
		WHERE l.item_id = ?
		ORDER BY s.source_key ASC, s.id ASC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list okf item sources %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var refs []model.ItemSourceRef
	for rows.Next() {
		var ref model.ItemSourceRef
		if err := rows.Scan(&ref.SourceID, &ref.SourceKey, &ref.CanonicalURL, &ref.SourceType, &ref.Title, &ref.NotePath, &ref.ExtractStatus, &ref.SummaryStatus, &ref.UserTags); err != nil {
			return nil, fmt.Errorf("scan okf item source ref %d: %w", itemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate okf item source refs %d: %w", itemID, err)
	}
	return refs, nil
}

func listBacklinksForSourceTx(ctx context.Context, tx *sql.Tx, sourceID int64) ([]model.SourceBacklink, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.source_key, i.source_type, i.canonical_url, i.title, i.note_path, i.author_handle, i.author_name, i.published_at, i.user_tags
		FROM item_source_links l
		JOIN items i ON i.id = l.item_id
		WHERE l.source_id = ?
		ORDER BY i.source_key ASC, i.id ASC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list okf source backlinks %d: %w", sourceID, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var refs []model.SourceBacklink
	for rows.Next() {
		var ref model.SourceBacklink
		if err := rows.Scan(&ref.ItemID, &ref.SourceKey, &ref.SourceType, &ref.CanonicalURL, &ref.Title, &ref.NotePath, &ref.AuthorHandle, &ref.AuthorName, &ref.PublishedAt, &ref.UserTags); err != nil {
			return nil, fmt.Errorf("scan okf source backlink %d: %w", sourceID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate okf source backlinks %d: %w", sourceID, err)
	}
	return refs, nil
}

func listItemMediaRefsWithTx(ctx context.Context, tx *sql.Tx, itemID int64) ([]model.ItemMediaRef, error) {
	return (&Store{}).listItemMediaRefsTx(ctx, tx, itemID)
}

func listItemChildrenAsBacklinksTx(ctx context.Context, tx *sql.Tx, parentItemID int64) ([]model.SourceBacklink, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.source_key, c.source_type, c.canonical_url, c.title, c.note_path, c.author_handle, c.author_name, c.published_at, c.user_tags
		FROM item_item_links l
		JOIN items c ON c.id = l.child_item_id
		WHERE l.parent_item_id = ?
		ORDER BY l.link_kind ASC, l.ordinal ASC, c.source_key ASC`, parentItemID)
	if err != nil {
		return nil, fmt.Errorf("list okf item child links %d: %w", parentItemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var refs []model.SourceBacklink
	for rows.Next() {
		var ref model.SourceBacklink
		if err := rows.Scan(&ref.ItemID, &ref.SourceKey, &ref.SourceType, &ref.CanonicalURL, &ref.Title, &ref.NotePath, &ref.AuthorHandle, &ref.AuthorName, &ref.PublishedAt, &ref.UserTags); err != nil {
			return nil, fmt.Errorf("scan okf item child link %d: %w", parentItemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate okf item child links %d: %w", parentItemID, err)
	}
	return refs, nil
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
