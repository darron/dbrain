package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) GetSourceByID(ctx context.Context, sourceID int64) (model.SourceDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+sourceSelectColumns+`
		FROM sources
		WHERE id = ?`, sourceID)

	var source model.SourceDocument
	if err := scanSource(row, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SourceDocument{}, fmt.Errorf("source not found: %d", sourceID)
		}
		return model.SourceDocument{}, fmt.Errorf("load source %d: %w", sourceID, err)
	}
	return source, nil
}

func (s *Store) ListAllSources(ctx context.Context, limit int) ([]model.SourceDocument, error) {
	query := `
		SELECT ` + sourceSelectColumns + `
		FROM sources
		WHERE note_path != ''
		ORDER BY id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list all sources: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan source row: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return sources, nil
}

func (s *Store) ListAllEntitySources(ctx context.Context, limit int) ([]model.SourceDocument, error) {
	query := `
		SELECT id, source_key, canonical_url, source_type, domain, title, site_name, note_path
		FROM sources
		WHERE note_path != ''
		ORDER BY id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entity sources: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := rows.Scan(
			&source.ID,
			&source.SourceKey,
			&source.CanonicalURL,
			&source.SourceType,
			&source.Domain,
			&source.Title,
			&source.SiteName,
			&source.NotePath,
		); err != nil {
			return nil, fmt.Errorf("scan entity source row: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity sources: %w", err)
	}
	return sources, nil
}

func (s *Store) GetSourcesByIDs(ctx context.Context, sourceIDs []int64) ([]model.SourceDocument, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, 0, len(sourceIDs))
	args := make([]any, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		placeholders = append(placeholders, "?")
		args = append(args, sourceID)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sourceSelectColumns+`
		FROM sources
		WHERE id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("load sources by ids: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan source by id row: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources by ids: %w", err)
	}

	return sources, nil
}

func (s *Store) GetSource(ctx context.Context, lookup string) (model.SourceDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+sourceSelectColumns+`
		FROM sources
		WHERE source_key = ?
			OR canonical_url = ?
			OR normalized_url = ?
			OR note_path = ?
		LIMIT 1`, lookup, lookup, lookup, lookup)

	var source model.SourceDocument
	if err := scanSource(row, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SourceDocument{}, fmt.Errorf("source not found: %s", lookup)
		}
		return model.SourceDocument{}, fmt.Errorf("load source %s: %w", lookup, err)
	}
	return source, nil
}

func (s *Store) GetSourceEvidence(ctx context.Context, lookup string) (model.SourceDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, source_key, canonical_url, normalized_url, source_type, domain, title, description, site_name,
			extract_status, extracted_at,
			summary_text, summary_status, summarized_at,
			note_path, user_tags, created_at, updated_at
		FROM sources
		WHERE source_key = ?
			OR canonical_url = ?
			OR normalized_url = ?
			OR note_path = ?
		LIMIT 1`, lookup, lookup, lookup, lookup)

	var source model.SourceDocument
	var extractedAt, summarizedAt, createdAt, updatedAt string
	if err := row.Scan(
		&source.ID,
		&source.SourceKey,
		&source.CanonicalURL,
		&source.NormalizedURL,
		&source.SourceType,
		&source.Domain,
		&source.Title,
		&source.Description,
		&source.SiteName,
		&source.ExtractStatus,
		&extractedAt,
		&source.SummaryText,
		&source.SummaryStatus,
		&summarizedAt,
		&source.NotePath,
		&source.UserTags,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SourceDocument{}, fmt.Errorf("source not found: %s", lookup)
		}
		return model.SourceDocument{}, fmt.Errorf("load source evidence %s: %w", lookup, err)
	}
	source.ExtractedAt = parseStoredTime(extractedAt)
	source.SummarizedAt = parseStoredTime(summarizedAt)
	source.CreatedAt = parseStoredTime(createdAt)
	source.UpdatedAt = parseStoredTime(updatedAt)
	return source, nil
}

func (s *Store) GetSourceExtractedText(ctx context.Context, lookup string) (string, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT extracted_text
		FROM sources
		WHERE source_key = ?
			OR canonical_url = ?
			OR normalized_url = ?
			OR note_path = ?
		LIMIT 1`, lookup, lookup, lookup, lookup)
	var extractedText string
	if err := row.Scan(&extractedText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("source not found: %s", lookup)
		}
		return "", fmt.Errorf("load source extracted text %s: %w", lookup, err)
	}
	return extractedText, nil
}

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

func (s *Store) SaveSourceUserTags(ctx context.Context, sourceID int64, tags string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source tag tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `UPDATE sources SET user_tags = ?, updated_at = ? WHERE id = ?`, tags, time.Now().UTC().Format(time.RFC3339), sourceID); err != nil {
		return fmt.Errorf("update user_tags for source %d: %w", sourceID, err)
	}
	if err = s.syncSourceFTSByIDTx(ctx, tx, sourceID); err != nil {
		return fmt.Errorf("sync fts for source %d: %w", sourceID, err)
	}
	return tx.Commit()
}
