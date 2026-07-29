package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) SaveSourceExtraction(ctx context.Context, sourceID int64, result model.ExtractResult, contentHash string) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		current, err := s.GetSourceByID(ctx, sourceID)
		if err != nil {
			return false, err
		}

		if isExtractFailureStatus(result.Status) {
			now := time.Now().UTC()
			failedAt := now
			if !result.FetchedAt.IsZero() {
				failedAt = result.FetchedAt.UTC()
			}
			failureKind, failureCount, firstFailedAt, lastFailedAt := nextExtractFailureState(current, result.Status, result.Error, failedAt)
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

		if _, err := withAuthoritativeWriteTx(ctx, s, "save-source-extraction", func(ctx context.Context, tx authoritativeWriteTx) (struct{}, error) {
			if _, err := tx.ExecContext(ctx, `
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
				return struct{}{}, fmt.Errorf("save source extraction %d: %w", sourceID, err)
			}
			return struct{}{}, nil
		}); err != nil {
			return false, err
		}

		if err := s.syncSourceFTS(ctx, sourceID); err != nil {
			return false, err
		}

		return true, nil
	})
}
