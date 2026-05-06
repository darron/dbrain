package store

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

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
