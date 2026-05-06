package sourceenrich

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

func saveSourceFailure(ctx context.Context, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, extractToolVersion string, summaryToolVersion string) error {
	if extract.Status == model.SourceExtractStatusError && isTerminalExtractStatus(source.ExtractStatus) {
		extract.Status = source.ExtractStatus
	}
	if strings.TrimSpace(extract.Tool) == "" {
		extract.Tool = summarizecli.ToolName
	}
	if strings.TrimSpace(extract.ToolVersion) == "" {
		extract.ToolVersion = extractToolVersion
	}
	if _, err := st.SaveSourceExtraction(ctx, source.ID, extract, source.ContentHash); err != nil {
		return err
	}
	if !opts.Summarize {
		return nil
	}

	summaryStatus := model.SourceSummaryStatusError
	if extract.Status != model.SourceExtractStatusError {
		summaryStatus = model.SourceSummaryStatusSkipped
	}
	_, err := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
		Status:        summaryStatus,
		Error:         extract.Error,
		Model:         opts.Model,
		PromptVersion: SummaryPromptVersion,
		Tool:          summarizecli.SummaryToolName(opts.Model),
		ToolVersion:   summaryToolVersion,
	})
	return err
}

func isTerminalExtractStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case model.SourceExtractStatusDead, model.SourceExtractStatusGone:
		return true
	default:
		return false
	}
}
