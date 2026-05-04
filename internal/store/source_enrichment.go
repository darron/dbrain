package store

import (
	"context"
	"fmt"
	"strings"
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
		errorEligible, errorArgs := sourceExtractBacklogWhere(time.Now().UTC())
		if summarize {
			args = append(args, errorArgs...)
			summaryStaleWhere, summaryArgs := sourceSummaryStaleWhere(promptVersion, toolName, toolVersion)
			args = append(args, summaryArgs...)

			query += `
				AND (
					` + errorEligible + `
					OR (
						extract_status IN ('ok', 'empty')
						AND ` + summaryStaleWhere + `
					)
				)`
		} else {
			args = append(args, errorArgs...)
			query += `
				AND ` + errorEligible
		}
	}

	query += `
		ORDER BY
			CASE WHEN extract_status = '' THEN 0 WHEN extract_status = 'error' THEN 1 ELSE 2 END,
			CASE WHEN extract_status = 'error' THEN extract_failure_count ELSE 0 END ASC,
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

func (s *Store) SaveSourceExtraction(ctx context.Context, sourceID int64, result model.ExtractResult, contentHash string) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		current, err := s.GetSourceByID(ctx, sourceID)
		if err != nil {
			return false, err
		}

		if isExtractFailureStatus(result.Status) {
			now := time.Now().UTC()
			failureKind, failureCount, firstFailedAt, lastFailedAt := nextExtractFailureState(current, result.Status, result.Error, now)
			changed := current.ExtractStatus != result.Status ||
				current.ExtractError != result.Error ||
				current.ExtractFailureKind != failureKind ||
				current.ExtractFailureCount != failureCount ||
				storedTimeString(current.ExtractFirstFailedAt) != firstFailedAt ||
				storedTimeString(current.ExtractLastFailedAt) != lastFailedAt ||
				current.ExtractTool != result.Tool ||
				current.ExtractToolVersion != result.ToolVersion
			if !changed {
				return false, nil
			}
			if _, err := s.db.ExecContext(ctx, `
				UPDATE sources
				SET extract_status = ?,
					extract_error = ?,
					extract_failure_kind = ?,
					extract_failure_count = ?,
					extract_first_failed_at = ?,
					extract_last_failed_at = ?,
					extract_tool = ?,
					extract_tool_version = ?,
					updated_at = ?
				WHERE id = ?`,
				result.Status,
				result.Error,
				failureKind,
				failureCount,
				firstFailedAt,
				lastFailedAt,
				result.Tool,
				result.ToolVersion,
				now.Format(time.RFC3339),
				sourceID,
			); err != nil {
				return false, fmt.Errorf("save source extraction error %d: %w", sourceID, err)
			}
			return true, nil
		}

		fetchedAt := ""
		if !result.FetchedAt.IsZero() {
			fetchedAt = result.FetchedAt.UTC().Format(time.RFC3339)
		}
		canonicalURL := current.CanonicalURL
		if result.FinalURL != "" {
			canonicalURL = result.FinalURL
		}

		changed := current.CanonicalURL != canonicalURL ||
			current.Title != result.Title ||
			current.Description != result.Description ||
			current.SiteName != result.SiteName ||
			current.ExtractedText != result.Content ||
			current.ExtractJSON != result.RawJSON ||
			current.ExtractStatus != result.Status ||
			current.ExtractError != result.Error ||
			current.ExtractTool != result.Tool ||
			current.ExtractToolVersion != result.ToolVersion ||
			current.ContentHash != contentHash ||
			current.ExtractedAt.UTC().Format(time.RFC3339) != fetchedAt

		if !changed {
			return false, nil
		}

		failureKind := ""
		failureCount := 0
		firstFailedAt := ""
		lastFailedAt := ""

		if _, err := s.db.ExecContext(ctx, `
			UPDATE sources
			SET canonical_url = ?,
				title = ?,
				description = ?,
				site_name = ?,
				extracted_text = ?,
				extract_json = ?,
				extract_status = ?,
				extract_error = ?,
				extract_failure_kind = ?,
				extract_failure_count = ?,
				extract_first_failed_at = ?,
				extract_last_failed_at = ?,
				extracted_at = ?,
				extract_tool = ?,
				extract_tool_version = ?,
				content_hash = ?,
				updated_at = ?
			WHERE id = ?`,
			canonicalURL,
			result.Title,
			result.Description,
			result.SiteName,
			result.Content,
			result.RawJSON,
			result.Status,
			result.Error,
			failureKind,
			failureCount,
			firstFailedAt,
			lastFailedAt,
			fetchedAt,
			result.Tool,
			result.ToolVersion,
			contentHash,
			time.Now().UTC().Format(time.RFC3339),
			sourceID,
		); err != nil {
			return false, fmt.Errorf("save source extraction %d: %w", sourceID, err)
		}

		if err := s.syncSourceFTS(ctx, sourceID); err != nil {
			return false, err
		}

		return true, nil
	})
}

func (s *Store) GetPreferredLocalSourceExtract(ctx context.Context, sourceID int64) (model.ExtractResult, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH local_candidates AS (
			SELECT
				s.canonical_url AS canonical_url,
				s.domain AS domain,
				s.source_type AS source_type,
				COALESCE(NULLIF(i.article_title, ''), s.title, '') AS title,
				CASE
					WHEN i.article_title = 'X Media Transcript' THEN ''
					WHEN i.source_type = 'apple_note' THEN ''
					ELSE i.article_text
				END AS article_text,
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
				s.domain AS domain,
				s.source_type AS source_type,
				COALESCE(NULLIF(p.article_title, ''), NULLIF(i.article_title, ''), s.title, '') AS title,
				COALESCE(
					NULLIF(CASE WHEN p.article_title = 'X Media Transcript' THEN '' ELSE p.article_text END, ''),
					CASE
						WHEN i.article_title = 'X Media Transcript' THEN ''
						WHEN i.source_type = 'apple_note' THEN ''
						ELSE i.article_text
					END,
					''
				) AS article_text,
				p.author_handle AS author_handle,
				p.x_post_json AS x_post_json,
				p.updated_at AS updated_at,
				COALESCE(p.last_seen_at, p.updated_at, '') AS sort_time,
				1 AS provider_priority,
				p.id AS item_id
			FROM item_source_links l
			JOIN items i ON i.id = l.item_id
			JOIN item_item_links q ON q.child_item_id = i.id AND q.link_kind = 'quoted_post'
			JOIN items p ON p.id = q.parent_item_id
			JOIN sources s ON s.id = l.source_id
			WHERE l.source_id = ?
		)
		SELECT
			canonical_url,
			domain,
			source_type,
			title,
			article_text,
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
		var domain string
		var sourceType string
		var title string
		var articleText string
		var authorHandle string
		var xPostJSON string
		var updatedAt string
		if err := rows.Scan(&canonicalURL, &domain, &sourceType, &title, &articleText, &authorHandle, &xPostJSON, &updatedAt); err != nil {
			return model.ExtractResult{}, false, fmt.Errorf("scan local source extract %d: %w", sourceID, err)
		}

		var candidate model.ExtractResult
		candidateRank := -1
		if content := strings.TrimSpace(articleText); content != "" {
			candidate = model.ExtractResult{
				CanonicalURL: canonicalURL,
				FinalURL:     canonicalURL,
				Title:        title,
				SiteName:     domain,
				Content:      content,
				Status:       "ok",
				FetchedAt:    parseStoredTime(updatedAt),
				Tool:         "item-cache",
				ToolVersion:  "local-item-cache",
			}
			candidateRank = 2
		} else if sourceType == "x_article" {
			if preview, ok := parseXArticlePreview(xPostJSON, canonicalURL); ok {
				finalURL := canonicalURL
				if value := buildXArticlePublicURL(authorHandle, preview.RestID); value != "" {
					finalURL = value
				}
				toolVersion := "local-article-preview-cache"
				candidateRank = 1
				if preview.HasFullText {
					toolVersion = "local-article-body-cache"
					candidateRank = 2
				}
				candidate = model.ExtractResult{
					CanonicalURL: canonicalURL,
					FinalURL:     finalURL,
					Title:        firstNonEmpty(preview.Title, title),
					SiteName:     firstNonEmpty(domain, "x.com"),
					Content:      preview.Content,
					Status:       "ok",
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

func (s *Store) SaveSourceSummary(ctx context.Context, sourceID int64, result model.SummaryResult) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		current, err := s.GetSourceByID(ctx, sourceID)
		if err != nil {
			return false, err
		}

		if result.Status == "error" {
			changed := current.SummaryStatus != result.Status ||
				current.SummaryError != result.Error ||
				current.SummaryTool != result.Tool ||
				current.SummaryToolVersion != result.ToolVersion
			if !changed {
				return false, nil
			}
			if _, err := s.db.ExecContext(ctx, `
				UPDATE sources
				SET summary_status = ?,
					summary_error = ?,
					summary_tool = ?,
					summary_tool_version = ?,
					updated_at = ?
				WHERE id = ?`,
				result.Status,
				result.Error,
				result.Tool,
				result.ToolVersion,
				time.Now().UTC().Format(time.RFC3339),
				sourceID,
			); err != nil {
				return false, fmt.Errorf("save source summary error %d: %w", sourceID, err)
			}
			return true, nil
		}

		summarizedAt := ""
		if !result.FetchedAt.IsZero() {
			summarizedAt = result.FetchedAt.UTC().Format(time.RFC3339)
		}

		changed := current.SummaryText != result.Text ||
			current.SummaryJSON != result.RawJSON ||
			current.SummaryStatus != result.Status ||
			current.SummaryError != result.Error ||
			current.SummaryModel != result.Model ||
			current.SummaryContentHash != current.ContentHash ||
			current.SummaryPromptVersion != result.PromptVersion ||
			current.SummaryTool != result.Tool ||
			current.SummaryToolVersion != result.ToolVersion ||
			current.SummarizedAt.UTC().Format(time.RFC3339) != summarizedAt

		if !changed {
			return false, nil
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, fmt.Errorf("begin source summary tx: %w", err)
		}
		defer func() {
			if err != nil {
				_ = tx.Rollback()
			}
		}()

		if _, err := tx.ExecContext(ctx, `
			UPDATE sources
			SET summary_text = ?,
				summary_json = ?,
				summary_status = ?,
				summary_error = ?,
				summary_model = ?,
				summary_content_hash = ?,
				summary_prompt_version = ?,
				summary_tool = ?,
				summary_tool_version = ?,
				summarized_at = ?,
				updated_at = ?
			WHERE id = ?`,
			result.Text,
			result.RawJSON,
			result.Status,
			result.Error,
			result.Model,
			current.ContentHash,
			result.PromptVersion,
			result.Tool,
			result.ToolVersion,
			summarizedAt,
			time.Now().UTC().Format(time.RFC3339),
			sourceID,
		); err != nil {
			return false, fmt.Errorf("update source summary %d: %w", sourceID, err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO source_summary_versions (
				source_id, content_hash, summary_text, summary_json, summary_status, summary_error,
				summary_model, summary_prompt_version, summary_tool, summary_tool_version, summarized_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sourceID,
			current.ContentHash,
			result.Text,
			result.RawJSON,
			result.Status,
			result.Error,
			result.Model,
			result.PromptVersion,
			result.Tool,
			result.ToolVersion,
			summarizedAt,
		); err != nil {
			return false, fmt.Errorf("insert source summary version %d: %w", sourceID, err)
		}

		if commitErr := tx.Commit(); commitErr != nil {
			return false, fmt.Errorf("commit source summary %d: %w", sourceID, commitErr)
		}

		if err := s.syncSourceFTS(ctx, sourceID); err != nil {
			return false, err
		}

		return true, nil
	})
}
