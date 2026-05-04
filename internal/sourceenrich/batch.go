package sourceenrich

import (
	"context"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func runSources(ctx context.Context, cfg config.Config, st *store.Store, sources []model.SourceDocument, opts Options, extractToolVersion string, summaryToolVersion string) (Stats, []int64, error) {
	opts = defaultOptions(cfg, opts)

	stats := Stats{SourcesQueued: len(sources)}
	touchedSourceIDs := map[int64]struct{}{}

	debugLog(opts.Logger, "source enrichment candidates loaded", "sources", len(sources), "limit", opts.Limit, "summarize", opts.Summarize, "concurrency", opts.Concurrency, "timeout", opts.Timeout, "progress_interval", opts.ProgressInterval)

	results, err := processSourcesConcurrently(ctx, cfg, st, sources, opts, extractToolVersion, summaryToolVersion)
	for _, result := range results {
		stats.SourcesExtracted += result.Stats.SourcesExtracted
		stats.SourcesSummarized += result.Stats.SourcesSummarized
		stats.SourcesRendered += result.Stats.SourcesRendered
		stats.SourcesUnchanged += result.Stats.SourcesUnchanged
		stats.Errors += result.Stats.Errors
		if result.TouchedSourceID > 0 {
			touchedSourceIDs[result.TouchedSourceID] = struct{}{}
		}
	}
	orderedSourceIDs := uniqueSorted(mapKeys(touchedSourceIDs))
	if err != nil {
		return stats, orderedSourceIDs, err
	}

	return stats, orderedSourceIDs, nil
}
