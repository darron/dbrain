package store

import (
	"time"

	"github.com/darron/dbrain/internal/model"
)

func nextSourceSummaryFailureState(current model.SourceDocument, result model.SummaryResult, now time.Time) (int, string, string, string) {
	now = now.UTC()
	failureCount := 1
	firstFailedAt := now.Format(time.RFC3339)

	// A retry belongs to the same durable candidate only while the extracted
	// input and the summary implementation identity are unchanged. A content,
	// prompt, or tool change starts a fresh failure series immediately.
	sameCandidate := current.SummaryStatus == model.SourceSummaryStatusError &&
		current.SummaryContentHash == current.ContentHash &&
		current.SummaryPromptVersion == result.PromptVersion &&
		current.SummaryTool == result.Tool &&
		current.SummaryToolVersion == result.ToolVersion
	if sameCandidate && current.SummaryFailureCount > 0 {
		failureCount = current.SummaryFailureCount + 1
		if !current.SummaryFirstFailedAt.IsZero() {
			firstFailedAt = current.SummaryFirstFailedAt.UTC().Format(time.RFC3339)
		}
	}

	lastFailedAt := now.Format(time.RFC3339)
	nextAttemptAt := now.Add(sourceSummaryErrorRetryCooldown).Format(time.RFC3339)
	return failureCount, firstFailedAt, lastFailedAt, nextAttemptAt
}
