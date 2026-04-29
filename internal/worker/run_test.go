package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

func TestRunSourcesDrainsQueueAndStops(t *testing.T) {
	t.Parallel()

	backlogs := []store.BacklogStats{
		{SourceExtractionPending: 3, SourceSummaryPending: 2},
		{Drained: true},
	}
	var backlogCalls int
	backlogFn := func(context.Context) (store.BacklogStats, error) {
		if backlogCalls >= len(backlogs) {
			return backlogs[len(backlogs)-1], nil
		}
		current := backlogs[backlogCalls]
		backlogCalls++
		return current, nil
	}

	runCalls := 0
	runFn := func(context.Context, int) (sourceenrich.Stats, error) {
		runCalls++
		return sourceenrich.Stats{
			SourcesQueued:     5,
			SourcesExtracted:  3,
			SourcesSummarized: 2,
			SourcesRendered:   2,
			SourcesUnchanged:  1,
			Errors:            0,
		}, nil
	}

	stats, err := RunSources(context.Background(), backlogFn, runFn, SourceOptions{
		Now: func() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunSources: %v", err)
	}
	if stats.StoppedReason != "queue_drained" {
		t.Fatalf("expected queue_drained, got %q", stats.StoppedReason)
	}
	if stats.Cycles != 1 || stats.WorkCycles != 1 {
		t.Fatalf("unexpected cycle counts: %+v", stats)
	}
	if runCalls != 1 {
		t.Fatalf("expected one run call, got %d", runCalls)
	}
	if stats.SourcesQueued != 5 || stats.SourcesExtracted != 3 || stats.SourcesSummarized != 2 {
		t.Fatalf("unexpected aggregated stats: %+v", stats)
	}
	if hasSourceBacklog(stats.FinalBacklog) {
		t.Fatalf("expected final backlog to be drained, got %+v", stats.FinalBacklog)
	}
	if stats.StartedAt.IsZero() || stats.CompletedAt.IsZero() || stats.Duration != 0 {
		t.Fatalf("expected zero-duration timestamps to be recorded, got %+v", stats)
	}
}

func TestRunSourcesWatchStopsAfterIdleExit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	backlogFn := func(context.Context) (store.BacklogStats, error) {
		return store.BacklogStats{}, nil
	}
	runFn := func(context.Context, int) (sourceenrich.Stats, error) {
		t.Fatal("runFn should not be called for empty backlog")
		return sourceenrich.Stats{}, nil
	}

	stats, err := RunSources(context.Background(), backlogFn, runFn, SourceOptions{
		Watch:         true,
		PollInterval:  time.Second,
		IdleExitAfter: 2 * time.Second,
		Now: func() time.Time {
			value := now
			now = now.Add(time.Second)
			return value
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("RunSources: %v", err)
	}
	if stats.StoppedReason != "idle_exit_after" {
		t.Fatalf("expected idle_exit_after, got %q", stats.StoppedReason)
	}
	if stats.IdlePolls < 2 {
		t.Fatalf("expected at least two idle polls, got %+v", stats)
	}
	if stats.Duration < 3*time.Second {
		t.Fatalf("expected duration to reflect idle polling, got %+v", stats)
	}
}

func TestRunSourcesReturnsRunError(t *testing.T) {
	t.Parallel()

	backlogFn := func(context.Context) (store.BacklogStats, error) {
		return store.BacklogStats{SourceSummaryPending: 1}, nil
	}
	expectedErr := errors.New("boom")
	runFn := func(context.Context, int) (sourceenrich.Stats, error) {
		return sourceenrich.Stats{Errors: 1}, expectedErr
	}

	stats, err := RunSources(context.Background(), backlogFn, runFn, SourceOptions{
		Now: func() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) },
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
	if stats.StoppedReason != "run_error" {
		t.Fatalf("expected run_error, got %q", stats.StoppedReason)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected aggregated errors, got %+v", stats)
	}
}

func TestRunSourcesStopsAtMaxSources(t *testing.T) {
	t.Parallel()

	backlogs := []store.BacklogStats{
		{SourceExtractionPending: 90},
		{SourceExtractionPending: 30},
		{SourceExtractionPending: 30},
	}
	var backlogCalls int
	backlogFn := func(context.Context) (store.BacklogStats, error) {
		if backlogCalls >= len(backlogs) {
			return backlogs[len(backlogs)-1], nil
		}
		current := backlogs[backlogCalls]
		backlogCalls++
		return current, nil
	}

	var limits []int
	runFn := func(_ context.Context, limit int) (sourceenrich.Stats, error) {
		limits = append(limits, limit)
		switch len(limits) {
		case 1:
			return sourceenrich.Stats{SourcesQueued: 60, SourcesExtracted: 60}, nil
		case 2:
			return sourceenrich.Stats{SourcesQueued: 40, SourcesExtracted: 40}, nil
		default:
			t.Fatalf("unexpected extra run invocation")
			return sourceenrich.Stats{}, nil
		}
	}

	stats, err := RunSources(context.Background(), backlogFn, runFn, SourceOptions{
		MaxSources: 100,
		Now:        func() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunSources: %v", err)
	}
	if stats.StoppedReason != "max_sources" {
		t.Fatalf("expected max_sources, got %q", stats.StoppedReason)
	}
	if stats.SourcesQueued != 100 || stats.SourcesExtracted != 100 {
		t.Fatalf("unexpected aggregated stats: %+v", stats)
	}
	if len(limits) != 2 || limits[0] != 100 || limits[1] != 40 {
		t.Fatalf("unexpected cycle limits: %#v", limits)
	}
}
