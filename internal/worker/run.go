package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

type SourceBacklogFunc func(context.Context) (store.BacklogStats, error)

type SourceRunFunc func(context.Context, int) (sourceenrich.Stats, error)

type SourceOptions struct {
	Watch         bool
	PollInterval  time.Duration
	IdleExitAfter time.Duration
	MaxCycles     int
	MaxSources    int
	Logger        *slog.Logger
	Now           func() time.Time
	Sleep         func(context.Context, time.Duration) error
}

type SourceStats struct {
	Cycles             int                `json:"cycles"`
	WorkCycles         int                `json:"work_cycles"`
	IdlePolls          int                `json:"idle_polls"`
	SourcesQueued      int                `json:"sources_queued"`
	SourcesExtracted   int                `json:"sources_extracted"`
	SourcesSummarized  int                `json:"sources_summarized"`
	SourcesRendered    int                `json:"sources_rendered"`
	SourcesUnchanged   int                `json:"sources_unchanged"`
	Errors             int                `json:"errors"`
	StoppedReason      string             `json:"stopped_reason"`
	FinalBacklog       store.BacklogStats `json:"final_backlog"`
	StartedAt          time.Time          `json:"started_at,omitempty"`
	CompletedAt        time.Time          `json:"completed_at,omitempty"`
	Duration           time.Duration      `json:"duration"`
	LastWorkCompleted  time.Time          `json:"last_work_completed,omitempty"`
	LastIdleObservedAt time.Time          `json:"last_idle_observed_at,omitempty"`
}

func RunSources(ctx context.Context, backlogFn SourceBacklogFunc, runFn SourceRunFunc, opts SourceOptions) (SourceStats, error) {
	if backlogFn == nil {
		return SourceStats{}, errors.New("backlogFn cannot be nil")
	}
	if runFn == nil {
		return SourceStats{}, errors.New("runFn cannot be nil")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepWithContext
	}

	stats := SourceStats{StartedAt: opts.Now()}
	var idleSince time.Time

	for {
		if err := ctx.Err(); err != nil {
			stats.StoppedReason = "context_canceled"
			stats.FinalBacklog = latestBacklog(ctx, backlogFn, stats.FinalBacklog)
			return finalizeSourceStats(stats, opts.Now), err
		}
		if opts.MaxCycles > 0 && stats.Cycles >= opts.MaxCycles {
			stats.StoppedReason = "max_cycles"
			stats.FinalBacklog = latestBacklog(ctx, backlogFn, stats.FinalBacklog)
			return finalizeSourceStats(stats, opts.Now), nil
		}
		if opts.MaxSources > 0 && stats.SourcesQueued >= opts.MaxSources {
			stats.StoppedReason = "max_sources"
			stats.FinalBacklog = latestBacklog(ctx, backlogFn, stats.FinalBacklog)
			return finalizeSourceStats(stats, opts.Now), nil
		}

		backlog, err := backlogFn(ctx)
		if err != nil {
			return stats, fmt.Errorf("load source backlog: %w", err)
		}
		stats.FinalBacklog = backlog

		if hasSourceBacklog(backlog) {
			cycleLimit := 0
			if opts.MaxSources > 0 {
				cycleLimit = opts.MaxSources - stats.SourcesQueued
				if cycleLimit <= 0 {
					stats.StoppedReason = "max_sources"
					stats.FinalBacklog = latestBacklog(ctx, backlogFn, stats.FinalBacklog)
					return finalizeSourceStats(stats, opts.Now), nil
				}
			}

			debugLog(
				opts.Logger,
				"worker source cycle starting",
				"source_extraction_pending", backlog.SourceExtractionPending,
				"source_summary_pending", backlog.SourceSummaryPending,
				"cycle_limit", cycleLimit,
			)
			idleSince = time.Time{}
			batchStats, err := runFn(ctx, cycleLimit)
			stats.Cycles++
			stats.WorkCycles++
			stats.SourcesQueued += batchStats.SourcesQueued
			stats.SourcesExtracted += batchStats.SourcesExtracted
			stats.SourcesSummarized += batchStats.SourcesSummarized
			stats.SourcesRendered += batchStats.SourcesRendered
			stats.SourcesUnchanged += batchStats.SourcesUnchanged
			stats.Errors += batchStats.Errors
			stats.LastWorkCompleted = opts.Now()
			if err != nil {
				stats.StoppedReason = "run_error"
				stats.FinalBacklog = latestBacklog(ctx, backlogFn, stats.FinalBacklog)
				return finalizeSourceStats(stats, opts.Now), err
			}
			debugLog(opts.Logger, "worker source cycle completed", "sources_queued", batchStats.SourcesQueued, "sources_extracted", batchStats.SourcesExtracted, "sources_summarized", batchStats.SourcesSummarized, "sources_rendered", batchStats.SourcesRendered, "errors", batchStats.Errors)
			continue
		}

		now := opts.Now()
		stats.LastIdleObservedAt = now
		if !opts.Watch {
			stats.StoppedReason = "queue_drained"
			return finalizeSourceStats(stats, opts.Now), nil
		}
		if idleSince.IsZero() {
			idleSince = now
		}
		stats.IdlePolls++
		debugLog(opts.Logger, "worker idle", "idle_polls", stats.IdlePolls, "poll_interval", opts.PollInterval.String(), "idle_exit_after", opts.IdleExitAfter.String())

		if opts.IdleExitAfter > 0 && now.Sub(idleSince) >= opts.IdleExitAfter {
			stats.StoppedReason = "idle_exit_after"
			return finalizeSourceStats(stats, opts.Now), nil
		}
		if err := opts.Sleep(ctx, opts.PollInterval); err != nil {
			stats.StoppedReason = "context_canceled"
			stats.FinalBacklog = latestBacklog(ctx, backlogFn, stats.FinalBacklog)
			return finalizeSourceStats(stats, opts.Now), err
		}
	}
}

func hasSourceBacklog(backlog store.BacklogStats) bool {
	return backlog.SourceExtractionPending > 0 || backlog.SourceSummaryPending > 0
}

func latestBacklog(ctx context.Context, backlogFn SourceBacklogFunc, fallback store.BacklogStats) store.BacklogStats {
	backlog, err := backlogFn(ctx)
	if err != nil {
		return fallback
	}
	return backlog
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}

func finalizeSourceStats(stats SourceStats, now func() time.Time) SourceStats {
	if stats.StartedAt.IsZero() {
		stats.StartedAt = now()
	}
	if stats.CompletedAt.IsZero() {
		stats.CompletedAt = now()
		if stats.CompletedAt.Before(stats.StartedAt) {
			stats.CompletedAt = stats.StartedAt
		}
		stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	}
	return stats
}
