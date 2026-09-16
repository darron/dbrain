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

const sourceSummaryFailureOnlyCycleLimit = 3

// ErrSourceBacklogStalled identifies a source worker that found eligible work
// but exhausted a bounded pass without successful durable advancement. A
// retryable failure may still move a candidate into deferred retry state; the
// worker treats repeated failure-only batches as stalled after its bound. The
// sentinel is intentionally independent of provider error text so the
// scheduled notification classifier can use the existing sources-stage type.
var ErrSourceBacklogStalled = errors.New("source backlog stalled")

type SourceBacklogStalledError struct {
	Attempted    int
	Succeeded    int
	Errors       int
	FinalBacklog store.BacklogStats
}

func (e *SourceBacklogStalledError) Error() string {
	if e == nil {
		return ErrSourceBacklogStalled.Error()
	}
	return fmt.Sprintf(
		"%s: attempted=%d succeeded=%d errors=%d eligible_extraction=%d eligible_summary=%d deferred_summary=%d",
		ErrSourceBacklogStalled,
		e.Attempted,
		e.Succeeded,
		e.Errors,
		e.FinalBacklog.SourceExtractionPending,
		e.FinalBacklog.SourceSummaryPending,
		e.FinalBacklog.SourceSummaryRetryDeferred,
	)
}

func (e *SourceBacklogStalledError) Unwrap() error {
	return ErrSourceBacklogStalled
}

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
	failureOnlyCycles := 0

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
			afterBacklog, err := backlogFn(ctx)
			if err != nil {
				stats.StoppedReason = "backlog_error"
				return finalizeSourceStats(stats, opts.Now), fmt.Errorf("load source backlog after cycle: %w", err)
			}
			stats.FinalBacklog = afterBacklog
			if !sourceBacklogProgressed(backlog, afterBacklog, batchStats) {
				stats.StoppedReason = "backlog_stalled"
				return finalizeSourceStats(stats, opts.Now), newSourceBacklogStalledError(stats)
			}
			if batchStats.Errors > 0 &&
				batchStats.SourcesExtracted == 0 &&
				batchStats.SourcesSummarized == 0 &&
				afterBacklog.SourceSummaryRetryDeferred > backlog.SourceSummaryRetryDeferred {
				failureOnlyCycles++
				if failureOnlyCycles >= sourceSummaryFailureOnlyCycleLimit {
					stats.StoppedReason = "backlog_stalled"
					return finalizeSourceStats(stats, opts.Now), newSourceBacklogStalledError(stats)
				}
			} else {
				failureOnlyCycles = 0
			}
			debugLog(opts.Logger, "worker source cycle completed", "sources_queued", batchStats.SourcesQueued, "sources_extracted", batchStats.SourcesExtracted, "sources_summarized", batchStats.SourcesSummarized, "sources_rendered", batchStats.SourcesRendered, "errors", batchStats.Errors)
			continue
		}

		if stats.WorkCycles > 0 && backlog.SourceSummaryRetryDeferred > 0 {
			stats.StoppedReason = "backlog_stalled"
			return finalizeSourceStats(stats, opts.Now), newSourceBacklogStalledError(stats)
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

func sourceBacklogProgressed(before store.BacklogStats, after store.BacklogStats, batch sourceenrich.Stats) bool {
	if after.SourceExtractionPending < before.SourceExtractionPending ||
		after.SourceSummaryPending < before.SourceSummaryPending ||
		after.SourceSummaryRetryDeferred > before.SourceSummaryRetryDeferred {
		return true
	}
	return batch.SourcesExtracted > 0 || batch.SourcesSummarized > 0
}

func newSourceBacklogStalledError(stats SourceStats) *SourceBacklogStalledError {
	return &SourceBacklogStalledError{
		Attempted:    stats.SourcesQueued,
		Succeeded:    stats.SourcesExtracted + stats.SourcesSummarized,
		Errors:       stats.Errors,
		FinalBacklog: stats.FinalBacklog,
	}
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
