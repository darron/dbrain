package xapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MediaTimeout <= 0 {
		opts.MediaTimeout = mediadownload.DefaultTimeout
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Concurrency > 16 {
		opts.Concurrency = 16
	}

	var (
		items []model.Item
		err   error
	)
	if opts.QuoteOnly {
		items, err = st.ListItemsForXQuoteHydration(ctx, opts.Limit, opts.Force)
	} else {
		items, err = st.ListItemsForXHydration(ctx, opts.Limit, opts.Force)
	}
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{Candidates: len(items)}
	debugLog(opts.Logger, "x hydration candidates loaded",
		"candidates", len(items),
		"concurrency", opts.Concurrency,
		"force", opts.Force,
		"limit", opts.Limit,
	)
	if len(items) == 0 {
		return stats, nil
	}

	var client *Client
	if requiresRemoteFetch(items, opts.Force) {
		client, err = newClient(ctx, opts)
		if err != nil {
			return Stats{}, err
		}
	}

	jobs := make(chan model.Item)
	results := make(chan fetchResult, opts.Concurrency)

	var wg sync.WaitGroup
	for range opts.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				debugLog(opts.Logger, "hydrating x post",
					"source_key", item.SourceKey,
					"tweet_id", item.ExternalID,
				)
				hydration, requested, fetchErr := hydrateItem(ctx, client, item, opts.Force)
				results <- fetchResult{item: item, hydration: hydration, requested: requested, err: fetchErr}
			}
		}()
	}

	go func() {
		for _, item := range items {
			jobs <- item
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	processed := 0
	for result := range results {
		if result.err != nil {
			return stats, result.err
		}
		processed++

		normalizedHydration, snapshot, hydrationNormalized, err := normalizeHydration(result.hydration, result.item.ExternalID)
		if err != nil {
			return stats, err
		}
		result.hydration = normalizedHydration

		if result.requested {
			stats.Requested++
		}
		changed, saveErr := st.SaveXHydration(ctx, result.item.ID, result.hydration)
		if saveErr != nil {
			return stats, saveErr
		}

		mediaStats, mediaErr := mediadownload.RunForItem(ctx, cfg, st, result.item.ID, mediadownload.Options{
			Force:   opts.Force,
			Timeout: opts.MediaTimeout,
			Logger:  opts.Logger,
		})
		if mediaErr != nil {
			return stats, mediaErr
		}
		stats.MediaCandidates += mediaStats.Candidates
		stats.MediaRequested += mediaStats.Requested
		stats.MediaDownloaded += mediaStats.Downloaded
		stats.MediaGone += mediaStats.Gone
		stats.MediaErrors += mediaStats.Errors
		stats.MediaBlocked += mediaStats.Blocked

		quoteStats, quoteChanged, quoteRendered, quoteErr := syncQuotedPosts(ctx, cfg, st, result.item, result.hydration, snapshot, opts)
		if quoteErr != nil {
			return stats, quoteErr
		}
		stats.MediaCandidates += quoteStats.Candidates
		stats.MediaRequested += quoteStats.Requested
		stats.MediaDownloaded += quoteStats.Downloaded
		stats.MediaGone += quoteStats.Gone
		stats.MediaErrors += quoteStats.Errors
		stats.MediaBlocked += quoteStats.Blocked
		stats.Rendered += quoteRendered

		switch result.hydration.Status {
		case "ok_graphql", "ok_syndication":
			stats.Hydrated++
		case "not_found":
			stats.Missing++
		default:
			stats.APIErrors++
		}

		mediaChanged := mediaStats.Changed > 0
		if changed || mediaChanged || quoteChanged || hydrationNormalized {
			refreshed, err := st.GetItem(ctx, result.item.SourceKey)
			if err != nil {
				return stats, err
			}
			if err := vault.WriteItem(cfg, refreshed); err != nil {
				return stats, fmt.Errorf("render hydrated note %s: %w", result.item.SourceKey, err)
			}
			stats.Rendered++
		} else {
			stats.Unchanged++
		}

		debugLog(opts.Logger, "x hydration result",
			"source_key", result.item.SourceKey,
			"tweet_id", result.item.ExternalID,
			"status", result.hydration.Status,
			"changed", changed,
			"media_changed", mediaChanged,
			"media_requested", mediaStats.Requested,
			"media_downloaded", mediaStats.Downloaded,
			"media_blocked", mediaStats.Blocked,
		)
		if opts.Logger != nil && (processed%25 == 0 || result.hydration.Status != "ok_graphql" || mediaStats.Requested > 0) {
			opts.Logger.Info("x hydration progress",
				"processed", processed,
				"requested", stats.Requested,
				"candidates", stats.Candidates,
				"hydrated", stats.Hydrated,
				"missing", stats.Missing,
				"api_errors", stats.APIErrors,
				"rendered", stats.Rendered,
				"unchanged", stats.Unchanged,
				"media_candidates", stats.MediaCandidates,
				"media_requested", stats.MediaRequested,
				"media_downloaded", stats.MediaDownloaded,
				"media_gone", stats.MediaGone,
				"media_errors", stats.MediaErrors,
				"media_blocked", stats.MediaBlocked,
				"remaining", stats.Candidates-processed,
			)
		}
	}

	return stats, nil
}
