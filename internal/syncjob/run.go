package syncjob

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	opts = normalizeOptions(opts)
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)

	stats := Stats{StartedAt: time.Now().UTC()}
	if opts.Metrics.Enabled() && opts.Metrics.RunID == "" {
		opts.Metrics.RunID = newSyncRunID(stats.StartedAt)
	}
	stages := newStageOptions(opts)
	emitSyncRunStarted(opts.Metrics, stats.StartedAt, opts)
	progressf(stages.Common.Progress, "Sync started at %s\n", stats.StartedAt.Format(time.RFC3339))

	if err := runSyncStagePlan(ctx, cfg, st, stages, &stats); err != nil {
		stats = finishStats(stats)
		emitSyncRunCompleted(opts.Metrics, stats, err)
		return stats, err
	}

	stats = finishStats(stats)
	emitSyncRunCompleted(opts.Metrics, stats, nil)
	progressf(stages.Common.Progress, "Sync completed in %s\n", stats.Duration)
	return stats, nil
}

func finishStats(stats Stats) Stats {
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	return stats
}
