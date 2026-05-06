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

	if err := runSyncStagePlan(ctx, cfg, st, stages, &stats); err != nil {
		return finishStats(stats), err
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
