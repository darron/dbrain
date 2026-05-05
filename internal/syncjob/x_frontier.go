package syncjob

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xapi"
)

const maxXQuoteDrainPasses = 8
const maxXFrontierSettlePasses = 3

func executeXFrontierStages(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	if shouldSettleXFrontier(opts) {
		for pass := 1; pass <= maxXFrontierSettlePasses; pass++ {
			if pass > 1 {
				progressf(opts.Common.Progress, "==> x settle pass %d\n", pass)
			}

			frontierActive := false

			bookmarkStats, bookmarkDuration, err := runXBookmarksPass(ctx, cfg, st, opts)
			mergeXBookmarkStage(&stats.XBookmarks, bookmarkDuration, bookmarkStats)
			if err != nil {
				return fmt.Errorf("import x-bookmarks: %w", err)
			}
			if xBookmarkFrontierActivity(bookmarkStats) {
				frontierActive = true
			}

			xStats, xDuration, err := runXHydratePass(ctx, cfg, st, opts)
			mergeXStage(&stats.X, xDuration, xStats)
			if err != nil {
				return fmt.Errorf("hydrate x: %w", err)
			}
			if xHydrationFrontierActivity(xStats) {
				frontierActive = true
			}

			linkStats, linkDuration, err := runLinksPass(ctx, cfg, st, opts)
			mergeLinksStage(&stats.Links, linkDuration, linkStats)
			if err != nil {
				return fmt.Errorf("extract links: %w", err)
			}
			if linkFrontierActivity(linkStats) {
				frontierActive = true
			}

			if !frontierActive {
				break
			}
			if pass == maxXFrontierSettlePasses {
				progressf(opts.Common.Progress, "X frontier settle stopped after %d passes with activity still present\n", maxXFrontierSettlePasses)
			}
		}
		return nil
	}

	if opts.XBookmarks.Enabled {
		bookmarkStats, bookmarkDuration, err := runXBookmarksPass(ctx, cfg, st, opts)
		mergeXBookmarkStage(&stats.XBookmarks, bookmarkDuration, bookmarkStats)
		if err != nil {
			return fmt.Errorf("import x-bookmarks: %w", err)
		}
	}

	if opts.X.Enabled {
		xStats, xDuration, err := runXHydratePass(ctx, cfg, st, opts)
		mergeXStage(&stats.X, xDuration, xStats)
		if err != nil {
			return fmt.Errorf("hydrate x: %w", err)
		}
	}

	if opts.Links.Enabled {
		linkStats, linkDuration, err := runLinksPass(ctx, cfg, st, opts)
		mergeLinksStage(&stats.Links, linkDuration, linkStats)
		if err != nil {
			return fmt.Errorf("extract links: %w", err)
		}
	}

	return nil
}

func runXBookmarksPass(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (xapi.BookmarkStats, time.Duration, error) {
	common := opts.Common
	stageOpts := opts.XBookmarks
	progressf(common.Progress, "==> import x-bookmarks\n")
	start := time.Now()
	bookmarkStats, err := runXBookmarkImport(ctx, cfg, st, xapi.BookmarkOptions{
		Limit:   stageOpts.Limit,
		Browser: common.Browser,
		Profile: common.Profile,
		Force:   common.Force,
		Timeout: stageOpts.Timeout,
		Logger:  common.Logger,
	})
	duration := time.Since(start)
	if err == nil {
		progressf(common.Progress, "X bookmarks import complete: created=%d updated=%d unchanged=%d rendered=%d pages=%d stopped=%s (%s)\n", bookmarkStats.Created, bookmarkStats.Updated, bookmarkStats.Unchanged, bookmarkStats.Rendered, bookmarkStats.PagesFetched, bookmarkStats.StoppedReason, duration)
	}
	return bookmarkStats, duration, err
}

func runXHydratePass(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (xapi.Stats, time.Duration, error) {
	common := opts.Common
	stageOpts := opts.X
	progressf(common.Progress, "==> hydrate x\n")
	start := time.Now()
	xStats, err := runXHydrate(ctx, cfg, st, xapi.Options{
		Limit:        stageOpts.Limit,
		Force:        common.Force,
		Concurrency:  stageOpts.Concurrency,
		Browser:      common.Browser,
		Profile:      common.Profile,
		Timeout:      stageOpts.Timeout,
		MediaTimeout: stageOpts.MediaTimeout,
		Logger:       common.Logger,
	})
	if err != nil {
		return xStats, time.Since(start), err
	}
	if !common.Force && xStats.Candidates > 0 {
		for pass := 1; pass <= maxXQuoteDrainPasses; pass++ {
			quoteStats, quoteErr := runXHydrate(ctx, cfg, st, xapi.Options{
				Limit:        stageOpts.Limit,
				Force:        false,
				QuoteOnly:    true,
				Concurrency:  stageOpts.Concurrency,
				Browser:      common.Browser,
				Profile:      common.Profile,
				Timeout:      stageOpts.Timeout,
				MediaTimeout: stageOpts.MediaTimeout,
				Logger:       common.Logger,
			})
			mergeXStats(&xStats, quoteStats)
			if quoteErr != nil {
				return xStats, time.Since(start), fmt.Errorf("hydrate x quote pass %d: %w", pass, quoteErr)
			}
			if quoteStats.Candidates == 0 {
				break
			}
			progressf(common.Progress, "X quote hydration pass %d complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d media_blocked=%d rendered=%d\n", pass, quoteStats.Hydrated, quoteStats.Missing, quoteStats.APIErrors, quoteStats.MediaDownloaded, quoteStats.MediaErrors, quoteStats.MediaBlocked, quoteStats.Rendered)
			if !xHydrationFrontierActivity(quoteStats) {
				break
			}
			if pass == maxXQuoteDrainPasses {
				progressf(common.Progress, "X quote hydration drain stopped after %d extra passes with candidates still present\n", maxXQuoteDrainPasses)
			}
		}
	}
	duration := time.Since(start)
	progressf(common.Progress, "X hydration complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d media_blocked=%d rendered=%d (%s)\n", xStats.Hydrated, xStats.Missing, xStats.APIErrors, xStats.MediaDownloaded, xStats.MediaErrors, xStats.MediaBlocked, xStats.Rendered, duration)
	return xStats, duration, nil
}

func runLinksPass(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (linkextract.Stats, time.Duration, error) {
	common := opts.Common
	stageOpts := opts.Links
	progressf(common.Progress, "==> extract links\n")
	start := time.Now()
	linkStats, err := runLinkExtract(ctx, cfg, st, linkextract.Options{
		DiscoverLimit: stageOpts.DiscoverLimit,
		Limit:         stageOpts.Limit,
		Concurrency:   stageOpts.Concurrency,
		Force:         common.Force,
		Summarize:     common.Summarize,
		Model:         common.Model,
		CLI:           common.CLI,
		Length:        common.Length,
		Timeout:       common.Timeout,
		Logger:        common.Logger,
	})
	duration := time.Since(start)
	if err == nil {
		progressf(common.Progress, "Link extraction complete: items_scanned=%d sources_queued=%d sources_summarized=%d errors=%d (%s)\n", linkStats.ItemsScanned, linkStats.SourcesQueued, linkStats.SourcesSummarized, linkStats.Errors, duration)
	}
	return linkStats, duration, err
}

func xBookmarkFrontierActivity(stats xapi.BookmarkStats) bool {
	return stats.Created > 0 || stats.Updated > 0 || stats.Rendered > 0
}

func xHydrationFrontierActivity(stats xapi.Stats) bool {
	return stats.Rendered > 0 || stats.MediaDownloaded > 0 || stats.MediaGone > 0
}

func linkFrontierActivity(stats linkextract.Stats) bool {
	return stats.SourcesCreated > 0 ||
		stats.LinksCreated > 0 ||
		stats.SourcesQueued > 0 ||
		stats.SourcesExtracted > 0 ||
		stats.SourcesSummarized > 0 ||
		stats.SourcesRendered > 0
}
