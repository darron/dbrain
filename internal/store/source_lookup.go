package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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
