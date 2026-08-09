package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) GetPreferredLocalSourceExtract(ctx context.Context, sourceID int64) (model.ExtractResult, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH local_candidates AS (
			SELECT
				s.canonical_url AS canonical_url,
				s.normalized_url AS normalized_url,
				s.domain AS domain,
				s.source_type AS source_type,
				COALESCE(NULLIF(i.article_title, ''), NULLIF(i.title, ''), s.title, '') AS title,
				CASE
					WHEN i.article_title = '`+model.XMediaTranscriptArticleTitle+`' THEN ''
					WHEN i.source_type = 'apple_note' THEN ''
					ELSE i.article_text
				END AS article_text,
				CASE WHEN i.source_type IN ('bsky_bookmark', 'bsky_quote', 'mastodon_bookmark', 'mastodon_quote', 'mastodon_reblog') THEN COALESCE(NULLIF(i.text, ''), '') ELSE '' END AS item_text,
				i.author_handle AS author_handle,
				i.x_post_json AS x_post_json,
				i.updated_at AS updated_at,
				COALESCE(i.last_seen_at, i.updated_at, '') AS sort_time,
				0 AS provider_priority,
				i.id AS item_id
			FROM item_source_links l
			JOIN items i ON i.id = l.item_id
			JOIN sources s ON s.id = l.source_id
			WHERE l.source_id = ?

			UNION ALL

			SELECT
				s.canonical_url AS canonical_url,
				s.normalized_url AS normalized_url,
				s.domain AS domain,
				s.source_type AS source_type,
				COALESCE(NULLIF(p.article_title, ''), NULLIF(p.title, ''), NULLIF(i.article_title, ''), NULLIF(i.title, ''), s.title, '') AS title,
				COALESCE(
					NULLIF(CASE WHEN p.article_title = '`+model.XMediaTranscriptArticleTitle+`' THEN '' ELSE p.article_text END, ''),
					CASE
						WHEN i.article_title = '`+model.XMediaTranscriptArticleTitle+`' THEN ''
						WHEN i.source_type = 'apple_note' THEN ''
						ELSE i.article_text
					END,
					''
				) AS article_text,
				CASE WHEN p.source_type IN ('bsky_bookmark', 'bsky_quote', 'mastodon_bookmark', 'mastodon_quote', 'mastodon_reblog') THEN COALESCE(NULLIF(p.text, ''), '') ELSE '' END AS item_text,
				p.author_handle AS author_handle,
				p.x_post_json AS x_post_json,
				p.updated_at AS updated_at,
				COALESCE(p.last_seen_at, p.updated_at, '') AS sort_time,
				1 AS provider_priority,
				p.id AS item_id
			FROM item_source_links l
			JOIN items i ON i.id = l.item_id
			JOIN item_item_links q ON q.child_item_id = i.id AND q.link_kind IN ('quoted_post', 'reposted_post')
			JOIN items p ON p.id = q.parent_item_id
			JOIN sources s ON s.id = l.source_id
			WHERE l.source_id = ?
		)
		SELECT
			canonical_url,
			normalized_url,
			domain,
			source_type,
			title,
			article_text,
			item_text,
			author_handle,
			x_post_json,
			updated_at
		FROM local_candidates
		ORDER BY sort_time DESC, provider_priority ASC, item_id DESC`, sourceID, sourceID)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("load local source extract %d: %w", sourceID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var best model.ExtractResult
	bestRank := -1
	bestContentLen := -1

	for rows.Next() {
		var canonicalURL string
		var normalizedURL string
		var domain string
		var sourceType string
		var title string
		var articleText string
		var itemText string
		var authorHandle string
		var xPostJSON string
		var updatedAt string
		if err := rows.Scan(&canonicalURL, &normalizedURL, &domain, &sourceType, &title, &articleText, &itemText, &authorHandle, &xPostJSON, &updatedAt); err != nil {
			return model.ExtractResult{}, false, fmt.Errorf("scan local source extract %d: %w", sourceID, err)
		}

		sourceURL := preferredLocalExtractURL(sourceType, canonicalURL, normalizedURL)
		var candidate model.ExtractResult
		candidateRank := -1
		if content := strings.TrimSpace(articleText); content != "" {
			candidate = model.ExtractResult{
				CanonicalURL: sourceURL,
				FinalURL:     sourceURL,
				Title:        title,
				SiteName:     domain,
				Content:      content,
				Status:       model.SourceExtractStatusOK,
				FetchedAt:    parseStoredTime(updatedAt),
				Tool:         "item-cache",
				ToolVersion:  "local-item-cache",
			}
			candidateRank = 2
		} else if content := strings.TrimSpace(itemText); content != "" {
			candidate = model.ExtractResult{
				CanonicalURL: sourceURL,
				FinalURL:     sourceURL,
				Title:        title,
				SiteName:     domain,
				Content:      content,
				Status:       model.SourceExtractStatusOK,
				FetchedAt:    parseStoredTime(updatedAt),
				Tool:         "item-cache",
				ToolVersion:  "local-item-cache",
			}
			candidateRank = 1
		} else if sourceType == "x_article" {
			if preview, ok := parseXArticlePreview(xPostJSON, sourceURL); ok {
				toolVersion := "local-article-preview-cache"
				candidateRank = 1
				if preview.HasFullText {
					toolVersion = "local-article-body-cache"
					candidateRank = 2
				}
				candidate = model.ExtractResult{
					CanonicalURL: sourceURL,
					FinalURL:     sourceURL,
					Title:        firstNonEmpty(preview.Title, title),
					SiteName:     firstNonEmpty(domain, "x.com"),
					Content:      preview.Content,
					Status:       model.SourceExtractStatusOK,
					FetchedAt:    parseStoredTime(updatedAt),
					Tool:         "x-hydration",
					ToolVersion:  toolVersion,
				}
			}
		}

		if candidateRank < 0 || strings.TrimSpace(candidate.Content) == "" {
			continue
		}
		contentLen := len(candidate.Content)
		if candidateRank > bestRank || (candidateRank == bestRank && contentLen > bestContentLen) {
			best = candidate
			bestRank = candidateRank
			bestContentLen = contentLen
		}
	}
	if err := rows.Err(); err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("iterate local source extract %d: %w", sourceID, err)
	}
	if bestRank < 0 || strings.TrimSpace(best.Content) == "" {
		return model.ExtractResult{}, false, nil
	}
	if best.FetchedAt.IsZero() {
		best.FetchedAt = time.Now().UTC()
	}

	return best, true, nil
}

func preferredLocalExtractURL(sourceType string, canonicalURL string, normalizedURL string) string {
	if sourceType == "x_article" && strings.Contains(normalizedURL, "/i/article/") {
		return normalizedURL
	}
	return canonicalURL
}
