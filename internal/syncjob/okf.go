package syncjob

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/store"
)

func executeOKFExportStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*OKFExportStage, error) {
	progressf(opts.Common.Progress, "==> export okf\n")
	start := time.Now()
	exportStats, err := runOKFExport(ctx, cfg, st, okf.ExportOptions{
		Profile:            okf.ProfilePrivate,
		IncludeItems:       true,
		IncludeSources:     true,
		IncludeEntities:    true,
		IncludeTopics:      true,
		IncludeRaw:         true,
		MediaPublicBaseURL: opts.OKFExport.PublicBaseURL,
		MediaProxyBaseURL:  opts.OKFExport.ProxyBaseURL,
	})
	stage := &OKFExportStage{Duration: time.Since(start), Stats: exportStats}
	if err != nil {
		return stage, fmt.Errorf("export okf: %w", err)
	}
	progressf(opts.Common.Progress, "OKF export complete: bundle=%s concepts=%d items=%d sources=%d entities=%d topics=%d indexes=%d broken_internal_links=%d omitted_by_filter=%d (%s)\n", exportStats.Bundle, exportStats.ConceptsWritten, exportStats.ItemsWritten, exportStats.SourcesWritten, exportStats.EntitiesWritten, exportStats.TopicsWritten, exportStats.IndexesWritten, exportStats.BrokenInternalLinks, exportStats.OmittedByFilterLinks, stage.Duration)
	return stage, nil
}
