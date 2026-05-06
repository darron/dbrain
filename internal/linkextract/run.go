package linkextract

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.DiscoverLimit <= 0 {
		opts.DiscoverLimit = 500
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}

	stats := Stats{}
	touchedSourceIDs := map[int64]struct{}{}

	items, err := st.ListItemsForLinkDiscovery(ctx, opts.DiscoverLimit, opts.Force)
	if err != nil {
		return stats, err
	}
	debugLog(opts.Logger, "link discovery candidates loaded", "items", len(items), "limit", opts.DiscoverLimit, "force", opts.Force)

	for _, item := range items {
		candidates, err := collectCandidates(item)
		if err != nil {
			return stats, fmt.Errorf("collect link candidates for %s: %w", item.SourceKey, err)
		}
		stats.ItemsScanned++
		stats.LinksFound += len(candidates)
		debugLog(opts.Logger, "link discovery item", "source_key", item.SourceKey, "candidate_count", len(candidates))
		for _, candidate := range candidates {
			result, err := st.UpsertSourceLink(ctx, item.ID, candidate)
			if err != nil {
				return stats, fmt.Errorf("upsert source link %s for %s: %w", candidate.CanonicalURL, item.SourceKey, err)
			}
			touchedSourceIDs[result.SourceID] = struct{}{}
			if result.SourceCreated {
				stats.SourcesCreated++
			}
			if result.LinkCreated {
				stats.LinksCreated++
			}
		}
		if err := st.MarkItemLinkDiscovery(ctx, item.ID, time.Now().UTC()); err != nil {
			return stats, err
		}
		stats.ItemsMarked++
	}

	if len(touchedSourceIDs) == 0 {
		return stats, nil
	}

	enrichStats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, mapKeys(touchedSourceIDs), sourceenrich.Options{
		Limit:                opts.Limit,
		Concurrency:          opts.Concurrency,
		Force:                opts.Force,
		AcceptCurrentSummary: true,
		Summarize:            opts.Summarize,
		Model:                opts.Model,
		CLI:                  opts.CLI,
		Length:               opts.Length,
		Timeout:              opts.Timeout,
		Logger:               opts.Logger,
	})
	if err != nil {
		return stats, err
	}

	stats.SourcesQueued = enrichStats.SourcesQueued
	stats.SourcesExtracted = enrichStats.SourcesExtracted
	stats.SourcesSummarized = enrichStats.SourcesSummarized
	stats.SourcesRendered = enrichStats.SourcesRendered
	stats.SourcesUnchanged = enrichStats.SourcesUnchanged
	stats.Errors = enrichStats.Errors

	return stats, nil
}
