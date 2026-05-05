package syncjob

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	opts = normalizeOptions(opts)
	stages := newStageOptions(opts)

	stats := Stats{StartedAt: time.Now().UTC()}
	progressf(stages.Common.Progress, "Sync started at %s\n", stats.StartedAt.Format(time.RFC3339))

	if stages.AppleNotes.Enabled {
		stage, err := executeAppleNotesStage(ctx, cfg, st, stages)
		stats.AppleNotes = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if stages.SafariTabs.Enabled {
		stage, err := executeSafariTabsStage(ctx, cfg, st, stages)
		stats.SafariTabs = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if err := executeXFrontierStages(ctx, cfg, st, stages, &stats); err != nil {
		return finishStats(stats), err
	}

	if stages.XMedia.Enabled {
		stage, err := executeXMediaStage(ctx, cfg, st, stages)
		stats.XMedia = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if stages.XPhotoOCR.Enabled {
		stage, err := executeXPhotoOCRStage(ctx, cfg, st, stages)
		stats.XPhotoOCR = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if stages.GitHub.Enabled {
		stage, err := executeGitHubStage(ctx, cfg, st, stages)
		stats.GitHub = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if stages.YouTube.Enabled {
		stage, err := executeYouTubeStage(ctx, cfg, st, stages)
		stats.YouTube = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if stages.Sources.Enabled {
		stage, err := executeSourcesStage(ctx, cfg, st, stages)
		stats.Sources = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	if stages.Categorize.Enabled {
		stage, err := executeCategorizeStage(ctx, cfg, st, stages)
		if err != nil {
			return finishStats(stats), err
		}
		stats.Categorize = stage
	}

	if stages.Archive.Enabled {
		stage, err := executeMediaArchiveStage(ctx, cfg, st, stages)
		stats.MediaArchive = stage
		if err != nil {
			return finishStats(stats), err
		}
	}

	stats = finishStats(stats)
	progressf(stages.Common.Progress, "Sync completed in %s\n", stats.Duration)
	return stats, nil
}

func finishStats(stats Stats) Stats {
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	return stats
}
