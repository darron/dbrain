package youtubeimport

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Browser == "" {
		opts.Browser = "chrome"
	}
	if opts.YTDLPBinary == "" {
		opts.YTDLPBinary = "yt-dlp"
	}
	if opts.WhisperBinary == "" {
		opts.WhisperBinary = "whisper-cli"
	}
	if opts.WhisperModelPath == "" {
		opts.WhisperModelPath = defaultWhisperModelPath()
	}
	if strings.TrimSpace(opts.MacWhisperBinary) == "" {
		opts.MacWhisperBinary = "mw"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}
	if strings.TrimSpace(opts.Transcriber) == "" {
		opts.Transcriber = "auto"
	}
	if !opts.WatchLater && !opts.Liked {
		opts.WatchLater = true
		opts.Liked = true
	}

	stats := Stats{}
	now := time.Now().UTC()
	touchedSourceIDs := map[int64]struct{}{}
	renderer := projection.NewRenderer(cfg, st)

	cleanupStats, err := pruneHistorySignals(ctx, cfg, st)
	if err != nil {
		return stats, err
	}
	stats.ItemsDeleted = cleanupStats.ItemsDeleted
	stats.SourcesDeleted = cleanupStats.SourcesDeleted
	resolvedCookiesArg := cookiesFromBrowserArg(opts.Browser, opts.Profile)

	for _, currentFeed := range selectedFeeds(opts) {
		debugLog(opts.Logger, "loading youtube feed", "feed", currentFeed.name, "url", currentFeed.url)
		envelope, cookiesArg, err := fetchFeed(ctx, currentFeed, opts)
		if err != nil {
			stats.Errors++
			if opts.Logger != nil {
				opts.Logger.Warn("youtube feed load failed", "feed", currentFeed.name, "url", currentFeed.url, "error", err.Error())
			}
			continue
		}
		stats.FeedsProcessed++
		resolvedCookiesArg = cookiesArg

		for _, entry := range envelope.Entries {
			item, skip, err := toItem(entry, currentFeed, now)
			if err != nil {
				return stats, err
			}
			if skip {
				stats.ItemsSkipped++
				continue
			}

			result, err := st.UpsertItem(ctx, item)
			if err != nil {
				return stats, err
			}
			stats.ItemsProcessed++
			switch result.Status {
			case model.UpsertCreated:
				stats.ItemsCreated++
			case model.UpsertUpdated:
				stats.ItemsUpdated++
			case model.UpsertUnchanged:
				stats.ItemsUnchanged++
			}

			shouldRender := result.Status != model.UpsertUnchanged
			if !shouldRender {
				if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
					shouldRender = true
				}
			}
			if shouldRender {
				if _, err := renderer.RefreshItem(ctx, item.SourceKey); err != nil {
					return stats, fmt.Errorf("render note %s: %w", item.SourceKey, err)
				}
				stats.ItemsRendered++
			}

			candidate := sourceCandidateForVideo(item.CanonicalURL)
			linkResult, err := st.UpsertSourceLink(ctx, result.ItemID, candidate)
			if err != nil {
				return stats, err
			}
			if linkResult.SourceCreated {
				stats.SourcesCreated++
			}
			if linkResult.LinkCreated {
				stats.LinksCreated++
			}
			touchedSourceIDs[linkResult.SourceID] = struct{}{}
		}
	}

	if len(touchedSourceIDs) == 0 {
		return stats, nil
	}

	enrichStats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, mapKeys(touchedSourceIDs), sourceenrich.Options{
		Force:                opts.Force,
		AcceptCurrentSummary: true,
		Summarize:            opts.Summarize,
		Model:                opts.Model,
		CLI:                  opts.CLI,
		Length:               opts.Length,
		Timeout:              opts.Timeout,
		Logger:               opts.Logger,
		Binary:               opts.SummarizeBinary,
		EnvFor:               summarizeEnvFor(resolvedCookiesArg),
		ArgsFor:              summarizeArgsFor(opts),
		FallbackExtractFor:   fallbackExtractFor(cfg, opts, resolvedCookiesArg),
	})
	if err != nil {
		return stats, err
	}

	stats.SourcesQueued = enrichStats.SourcesQueued
	stats.SourcesExtracted = enrichStats.SourcesExtracted
	stats.SourcesSummarized = enrichStats.SourcesSummarized
	stats.SourcesRendered = enrichStats.SourcesRendered
	stats.SourcesUnchanged = enrichStats.SourcesUnchanged
	stats.Errors += enrichStats.Errors

	return stats, nil
}
