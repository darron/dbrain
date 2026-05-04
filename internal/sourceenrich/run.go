package sourceenrich

import (
	"context"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func RunPending(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []int64, error) {
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)
	summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion := summaryFreshnessTarget(ctx, opts)
	extractToolVersion := summarizecli.Version(ctx, opts.Binary)
	summaryToolVersion := summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
	sources, err := st.ListSourcesForEnrichment(ctx, opts.Limit, opts.Force, opts.Summarize, summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion)
	if err != nil {
		return Stats{}, nil, err
	}
	return runSources(ctx, cfg, st, sources, opts, extractToolVersion, summaryToolVersion)
}

func RunSourceIDs(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (Stats, []int64, error) {
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)
	ordered := uniqueSorted(sourceIDs)
	sources, err := st.GetSourcesByIDs(ctx, ordered)
	if err != nil {
		return Stats{}, nil, err
	}

	byID := make(map[int64]model.SourceDocument, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}

	summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion := summaryFreshnessTarget(ctx, opts)
	extractToolVersion := summarizecli.Version(ctx, opts.Binary)
	summaryToolVersion := summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
	filtered := selectSourceDocuments(ordered, byID, opts, summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion)

	return runSources(ctx, cfg, st, filtered, opts, extractToolVersion, summaryToolVersion)
}
