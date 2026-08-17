package syncjob

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func TestRunSyncStagePlanWrapsFatalStage(t *testing.T) {
	cause := fs.ErrPermission
	plan := []syncStage{{
		ID:      syncStageAppleNotes,
		Enabled: func(stageOptions) bool { return true },
		Run: func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error {
			return cause
		},
	}}

	err := runSyncStagePlanWithPlan(t.Context(), config.Config{}, nil, stageOptions{}, &Stats{}, plan)
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage() != "apple_notes" || !errors.Is(err, cause) {
		t.Fatalf("stage error = %#v", err)
	}
	if err.Error() != cause.Error() {
		t.Fatalf("user-facing error changed: %q", err)
	}
}

func TestRunSyncStagePlanContinuesAfterStageError(t *testing.T) {
	firstCause := errors.New("github unavailable")
	secondCause := errors.New("feeds unavailable")
	var ran []syncStageID
	plan := []syncStage{
		{
			ID:      syncStageGitHub,
			Enabled: func(stageOptions) bool { return true },
			Run: func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error {
				ran = append(ran, syncStageGitHub)
				return firstCause
			},
		},
		{
			ID:      syncStageYouTube,
			After:   []syncStageID{syncStageGitHub},
			Enabled: func(stageOptions) bool { return true },
			Run: func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error {
				ran = append(ran, syncStageYouTube)
				return nil
			},
		},
		{
			ID:      syncStageFeeds,
			After:   []syncStageID{syncStageYouTube},
			Enabled: func(stageOptions) bool { return true },
			Run: func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error {
				ran = append(ran, syncStageFeeds)
				return secondCause
			},
		},
	}

	err := runSyncStagePlanWithPlan(t.Context(), config.Config{}, nil, stageOptions{}, &Stats{}, plan)
	if !slices.Equal(ran, []syncStageID{syncStageGitHub, syncStageYouTube, syncStageFeeds}) {
		t.Fatalf("ran stages = %v", ran)
	}
	if !errors.Is(err, firstCause) || !errors.Is(err, secondCause) {
		t.Fatalf("aggregate stage error = %#v", err)
	}
	if got := erroredStageCount(err); got != 2 {
		t.Fatalf("errored stage count = %d, want 2", got)
	}
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage() != string(syncStageGitHub) {
		t.Fatalf("first stage error = %#v", err)
	}
}

func TestRunSyncStagePlanStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	var ran []syncStageID
	plan := []syncStage{
		{
			ID:      syncStageGitHub,
			Enabled: func(stageOptions) bool { return true },
			Run: func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error {
				ran = append(ran, syncStageGitHub)
				cancel()
				return context.Canceled
			},
		},
		{
			ID:      syncStageYouTube,
			After:   []syncStageID{syncStageGitHub},
			Enabled: func(stageOptions) bool { return true },
			Run: func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error {
				ran = append(ran, syncStageYouTube)
				return nil
			},
		},
	}

	err := runSyncStagePlanWithPlan(ctx, config.Config{}, nil, stageOptions{}, &Stats{}, plan)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %#v", err)
	}
	if !slices.Equal(ran, []syncStageID{syncStageGitHub}) {
		t.Fatalf("ran stages after cancellation = %v", ran)
	}
}

func TestWrapStageErrorReturnsNilForNilCause(t *testing.T) {
	if err := WrapStageError("apple_notes", nil); err != nil {
		t.Fatalf("nil cause wrapped as %#v", err)
	}
}

func TestWrapStageErrorLeavesUnknownStageUnwrapped(t *testing.T) {
	cause := fs.ErrPermission
	err := WrapStageError("not_a_sync_stage", cause)
	if err != cause {
		t.Fatalf("unknown stage error = %#v, want original cause", err)
	}
	var stageErr *StageError
	if errors.As(err, &stageErr) {
		t.Fatalf("unknown stage gained identity: %#v", stageErr)
	}
}
