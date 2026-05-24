package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) GetSourceEvidence(ctx context.Context, lookup string) (model.SourceDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, source_key, canonical_url, normalized_url, source_type, domain, title, description, site_name,
			extract_status, extracted_at, extract_tool,
			summary_text, summary_status, summarized_at, summary_model, summary_tool,
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
		&source.ExtractTool,
		&source.SummaryText,
		&source.SummaryStatus,
		&summarizedAt,
		&source.SummaryModel,
		&source.SummaryTool,
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
