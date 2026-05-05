package xmediatranscribe

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/summarizecli"
)

func summaryResultFromError(opts Options, err error) model.SummaryResult {
	status := "error"
	if isBlockedSummaryError(err) {
		status = "blocked"
	}
	return model.SummaryResult{
		Model:         strings.TrimSpace(opts.SummaryModel),
		PromptVersion: xMediaSummaryPromptVersion,
		Status:        status,
		Error:         err.Error(),
		Tool:          summarizecli.SummaryToolName(opts.SummaryModel),
		ToolVersion:   summarizecli.SummaryToolVersion(context.Background(), "summarize", opts.SummaryModel),
	}
}

func isBlockedSummaryError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "maximum context length") ||
		strings.Contains(message, "context length") ||
		strings.Contains(message, "too many tokens") ||
		strings.Contains(message, "input is too long")
}
