package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) SaveSourceSummary(ctx context.Context, sourceID int64, result model.SummaryResult) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		current, err := s.GetSourceByID(ctx, sourceID)
		if err != nil {
			return false, err
		}

		if result.Status == model.SourceSummaryStatusError {
			now := time.Now().UTC()
			failureCount, firstFailedAt, lastFailedAt, nextAttemptAt := 0, "", "", ""
			if isSourceSummaryReady(current.ExtractStatus) {
				failureCount, firstFailedAt, lastFailedAt, nextAttemptAt = nextSourceSummaryFailureState(current, result, now)
			}
			changed := current.SummaryStatus != result.Status ||
				current.SummaryError != result.Error ||
				current.SummaryModel != result.Model ||
				current.SummaryContentHash != current.ContentHash ||
				current.SummaryPromptVersion != result.PromptVersion ||
				current.SummaryTool != result.Tool ||
				current.SummaryToolVersion != result.ToolVersion ||
				current.SummaryFailureCount != failureCount ||
				storedTimeString(current.SummaryFirstFailedAt) != firstFailedAt ||
				storedTimeString(current.SummaryLastFailedAt) != lastFailedAt ||
				storedTimeString(current.SummaryNextAttemptAt) != nextAttemptAt
			if !changed {
				return false, nil
			}
			if _, err := s.db.ExecContext(ctx, `
				UPDATE sources
				SET summary_status = ?,
					summary_error = ?,
					summary_model = ?,
					summary_content_hash = ?,
					summary_prompt_version = ?,
					summary_tool = ?,
					summary_tool_version = ?,
					summary_failure_count = ?,
					summary_first_failed_at = ?,
					summary_last_failed_at = ?,
					summary_next_attempt_at = ?,
					updated_at = ?
				WHERE id = ?`,
				result.Status,
				result.Error,
				result.Model,
				current.ContentHash,
				result.PromptVersion,
				result.Tool,
				result.ToolVersion,
				failureCount,
				firstFailedAt,
				lastFailedAt,
				nextAttemptAt,
				now.Format(time.RFC3339),
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
			current.SummaryFailureCount != 0 ||
			!current.SummaryFirstFailedAt.IsZero() ||
			!current.SummaryLastFailedAt.IsZero() ||
			!current.SummaryNextAttemptAt.IsZero() ||
			current.SummarizedAt.UTC().Format(time.RFC3339) != summarizedAt

		if !changed {
			return false, nil
		}

		changed, err = withAuthoritativeWriteTx(ctx, s, "save-source-summary", func(ctx context.Context, tx authoritativeWriteTx) (bool, error) {
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
				summary_failure_count = 0,
				summary_first_failed_at = '',
				summary_last_failed_at = '',
				summary_next_attempt_at = '',
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
			return true, nil
		})
		if err != nil {
			return false, err
		}

		if err := s.syncSourceFTS(ctx, sourceID); err != nil {
			return false, err
		}

		return changed, nil
	})
}
