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
			failedAt := sourceSummaryFailureTime(result)
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
					summary_failed_at = ?,
					updated_at = ?
				WHERE id = ?`,
				result.Status,
				result.Error,
				result.Tool,
				result.ToolVersion,
				failedAt,
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
		summaryFailedAt := ""
		if result.Status != model.SourceSummaryStatusOK {
			summaryFailedAt = sourceSummaryFailureTime(result)
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
				summary_failed_at = ?,
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
			summaryFailedAt,
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

func sourceSummaryFailureTime(result model.SummaryResult) string {
	if !result.FetchedAt.IsZero() {
		return result.FetchedAt.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}
