package syncjob

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	opts = normalizeOptions(opts)

	stats := Stats{StartedAt: time.Now().UTC()}
	progressf(opts.Progress, "Sync started at %s\n", stats.StartedAt.Format(time.RFC3339))

	if opts.AppleNotesEnabled {
		stage, err := executeAppleNotesStage(ctx, cfg, st, opts)
		stats.AppleNotes = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if opts.SafariTabsEnabled {
		stage, err := executeSafariTabsStage(ctx, cfg, st, opts)
		stats.SafariTabs = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if err := executeXFrontierStages(ctx, cfg, st, opts, &stats); err != nil {
		return finishStats(stats), err
	}

	if opts.XMediaEnabled {
		stage, err := executeXMediaStage(ctx, cfg, st, opts)
		stats.XMedia = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if opts.XPhotoOCREnabled {
		stage, err := executeXPhotoOCRStage(ctx, cfg, st, opts)
		stats.XPhotoOCR = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if opts.GitHubEnabled {
		stage, err := executeGitHubStage(ctx, cfg, st, opts)
		stats.GitHub = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if opts.YouTubeEnabled {
		stage, err := executeYouTubeStage(ctx, cfg, st, opts)
		stats.YouTube = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if opts.SourcesEnabled {
		stage, err := executeSourcesStage(ctx, cfg, st, opts)
		stats.Sources = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if opts.CategorizeEnabled {
		stage, err := executeCategorizeStage(ctx, cfg, st, opts)
		if err != nil {
			return finishStats(stats), err
		}
		stats.Categorize = stage
	}

	if opts.ArchiveMediaEnabled {
		stage, err := executeMediaArchiveStage(ctx, cfg, st, opts)
		stats.MediaArchive = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	stats = finishStats(stats)
	progressf(opts.Progress, "Sync completed in %s\n", stats.Duration)
	return stats, nil
}

func finishStats(stats Stats) Stats {
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	return stats
}
