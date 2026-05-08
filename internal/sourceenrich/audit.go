package sourceenrich

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

// SummarizeSourceReadOnly runs the normal source-summary prompt against an
// already extracted source without saving any derived state.
func SummarizeSourceReadOnly(ctx context.Context, cfg config.Config, source model.SourceDocument, opts Options) (model.SummaryResult, error) {
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)
	extract := model.ExtractResult{
		CanonicalURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        source.Title,
		Description:  source.Description,
		SiteName:     firstNonEmpty(source.SiteName, source.Domain),
		Content:      source.ExtractedText,
		Status:       source.ExtractStatus,
		FetchedAt:    source.ExtractedAt,
		Tool:         source.ExtractTool,
		ToolVersion:  source.ExtractToolVersion,
	}
	if strings.TrimSpace(extract.Content) == "" {
		return model.SummaryResult{}, fmt.Errorf("source %s has no extracted text to summarize", source.SourceKey)
	}
	if reason, ok := skipSummaryReason(source, extract); ok {
		return model.SummaryResult{
			Status:        model.SourceSummaryStatusSkipped,
			Error:         reason,
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.SummaryToolName(opts.Model),
			ToolVersion:   summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model),
		}, nil
	}
	result, err := summarizeExtract(ctx, cfg, source, extract, opts, nil)
	if err != nil {
		return model.SummaryResult{}, err
	}
	result.Summary.PromptVersion = SummaryPromptVersion
	return result.Summary, nil
}
