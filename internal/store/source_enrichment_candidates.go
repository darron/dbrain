package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListSourcesForEnrichment(ctx context.Context, limit int, force bool, summarize bool, promptVersion string, toolName string, toolVersion string) ([]model.SourceDocument, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT ` + sourceSelectColumns + `
		FROM sources
		WHERE 1 = 1`
	args := make([]any, 0, 2)

	if !force {
		policy := newSourceEnrichmentPolicy(time.Now().UTC(), promptVersion, toolName, toolVersion)
		candidateWhere, candidateArgs := policy.candidateWhere(summarize)
		args = append(args, candidateArgs...)
		query += `
			AND ` + candidateWhere
	}

	query += `
		ORDER BY
			CASE WHEN extract_status = '' THEN 0 WHEN extract_status = '` + model.SourceExtractStatusError + `' THEN 1 WHEN ` + sourceMakerWorldAPIRepairWhere() + ` THEN 1 ELSE 2 END,
			CASE WHEN extract_status = '` + model.SourceExtractStatusError + `' THEN extract_failure_count ELSE 0 END ASC,
			extract_last_failed_at ASC,
			extracted_at ASC,
			id DESC
		LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sources for enrichment: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var sources []model.SourceDocument
	for rows.Next() {
		var source model.SourceDocument
		if err := scanSource(rows, &source); err != nil {
			return nil, fmt.Errorf("scan source enrichment row: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source enrichment rows: %w", err)
	}

	return sources, nil
}
