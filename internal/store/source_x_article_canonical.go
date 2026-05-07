package store

import (
	"fmt"
	"time"
)

func (s *Store) backfillXArticleCanonicalURLs() error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`
		UPDATE sources
		SET canonical_url = normalized_url,
			updated_at = ?
		WHERE source_type = 'x_article'
			AND normalized_url LIKE 'https://x.com/i/article/%'
			AND canonical_url != normalized_url`, now); err != nil {
		return fmt.Errorf("backfill x article canonical urls: %w", err)
	}
	return nil
}
