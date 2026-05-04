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

func runXBookmarksPass(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (xapi.BookmarkStats, time.Duration, error) {
	progressf(opts.Progress, "==> import x-bookmarks\n")
	start := time.Now()
	bookmarkStats, err := runXBookmarkImport(ctx, cfg, st, xapi.BookmarkOptions{
		Limit:   opts.XBookmarksLimit,
		Browser: opts.Browser,
		Profile: opts.Profile,
		Force:   opts.Force,
		Timeout: opts.XTimeout,
		Logger:  opts.Logger,
	})
	duration := time.Since(start)
	if err == nil {
		progressf(opts.Progress, "X bookmarks import complete: created=%d updated=%d unchanged=%d rendered=%d pages=%d stopped=%s (%s)\n", bookmarkStats.Created, bookmarkStats.Updated, bookmarkStats.Unchanged, bookmarkStats.Rendered, bookmarkStats.PagesFetched, bookmarkStats.StoppedReason, duration)
	}
	return bookmarkStats, duration, err
}

func runXHydratePass(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (xapi.Stats, time.Duration, error) {
	progressf(opts.Progress, "==> hydrate x\n")
	start := time.Now()
	xStats, err := runXHydrate(ctx, cfg, st, xapi.Options{
		Limit:       opts.XLimit,
		Force:       opts.Force,
		Concurrency: opts.XConcurrency,
		Browser:     opts.Browser,
		Profile:     opts.Profile,
		Timeout:     opts.XTimeout,
		Logger:      opts.Logger,
	})
	if err != nil {
		return xStats, time.Since(start), err
	}
	if !opts.Force && xStats.Candidates > 0 {
		for pass := 1; pass <= maxXQuoteDrainPasses; pass++ {
			quoteStats, quoteErr := runXHydrate(ctx, cfg, st, xapi.Options{
				Limit:       opts.XLimit,
				Force:       false,
				QuoteOnly:   true,
				Concurrency: opts.XConcurrency,
				Browser:     opts.Browser,
				Profile:     opts.Profile,
				Timeout:     opts.XTimeout,
				Logger:      opts.Logger,
			})
			mergeXStats(&xStats, quoteStats)
			if quoteErr != nil {
				return xStats, time.Since(start), fmt.Errorf("hydrate x quote pass %d: %w", pass, quoteErr)
			}
			if quoteStats.Candidates == 0 {
				break
			}
			progressf(opts.Progress, "X quote hydration pass %d complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d rendered=%d\n", pass, quoteStats.Hydrated, quoteStats.Missing, quoteStats.APIErrors, quoteStats.MediaDownloaded, quoteStats.MediaErrors, quoteStats.Rendered)
			if pass == maxXQuoteDrainPasses {
				progressf(opts.Progress, "X quote hydration drain stopped after %d extra passes with candidates still present\n", maxXQuoteDrainPasses)
			}
		}
	}
	duration := time.Since(start)
	progressf(opts.Progress, "X hydration complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d rendered=%d (%s)\n", xStats.Hydrated, xStats.Missing, xStats.APIErrors, xStats.MediaDownloaded, xStats.MediaErrors, xStats.Rendered, duration)
	return xStats, duration, nil
}

func runLinksPass(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (linkextract.Stats, time.Duration, error) {
	progressf(opts.Progress, "==> extract links\n")
	start := time.Now()
	linkStats, err := runLinkExtract(ctx, cfg, st, linkextract.Options{
		DiscoverLimit: opts.LinkDiscoverLimit,
		Limit:         opts.LinkLimit,
		Concurrency:   opts.LinkConcurrency,
		Force:         opts.Force,
		Summarize:     opts.Summarize,
		Model:         opts.Model,
		CLI:           opts.CLI,
		Length:        opts.Length,
		Timeout:       opts.Timeout,
		Logger:        opts.Logger,
	})
	duration := time.Since(start)
	if err == nil {
		progressf(opts.Progress, "Link extraction complete: items_scanned=%d sources_queued=%d sources_summarized=%d errors=%d (%s)\n", linkStats.ItemsScanned, linkStats.SourcesQueued, linkStats.SourcesSummarized, linkStats.Errors, duration)
	}
	return linkStats, duration, err
}
