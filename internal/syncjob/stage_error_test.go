package syncjob

import (
	"context"
	"errors"
	"io/fs"
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
