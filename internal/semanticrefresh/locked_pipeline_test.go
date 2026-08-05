package semanticrefresh

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/store"
)

func TestLockedPipelineHoldsMaintenanceForEveryBoundedUnit(t *testing.T) {
	for _, stage := range []store.SemanticRefreshStage{
		store.SemanticRefreshProjection,
		store.SemanticRefreshEmbedding,
		store.SemanticRefreshFlush,
		store.SemanticRefreshCompaction,
		store.SemanticRefreshVerify,
		store.SemanticRefreshReadiness,
	} {
		t.Run(string(stage), func(t *testing.T) {
			locks := &recordingRefreshLocks{}
			executor := stageExecutorFunc(func(context.Context, store.SemanticRefreshRun, StageProgressCallback) (StageOutcome, error) {
				locks.events = append(locks.events, "execute")
				if !locks.maintenanceHeld {
					t.Fatal("stage executed without exclusive maintenance")
				}
				wantGeneration := stage == store.SemanticRefreshFlush ||
					stage == store.SemanticRefreshCompaction
				if locks.generationHeld != wantGeneration {
					t.Fatalf("generation held=%v want=%v", locks.generationHeld, wantGeneration)
				}
				return StageOutcome{NextStage: stage}, nil
			})
			locked, err := newLockedPipeline(executor, locks)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := locked.Execute(t.Context(), pipelineRun(stage, 1), nil); err != nil {
				t.Fatal(err)
			}
			want := []string{"maintenance.acquire"}
			if stage == store.SemanticRefreshFlush || stage == store.SemanticRefreshCompaction {
				want = append(want, "generation.acquire")
			}
			want = append(want, "execute")
			if stage == store.SemanticRefreshFlush || stage == store.SemanticRefreshCompaction {
				want = append(want, "generation.close")
			}
			want = append(want, "maintenance.close")
			if !reflect.DeepEqual(locks.events, want) {
				t.Fatalf("events=%v want=%v", locks.events, want)
			}
			if locks.maintenanceHeld || locks.generationHeld {
				t.Fatalf("locks remained held: %+v", locks)
			}
		})
	}
}

func TestLockedPipelineForwardsProgressObserver(t *testing.T) {
	want := StageWork{Current: 2, Total: 3, TotalKnown: true}
	executor := stageExecutorFunc(func(_ context.Context, run store.SemanticRefreshRun, progress StageProgressCallback) (StageOutcome, error) {
		if err := progress(want); err != nil {
			return StageOutcome{}, err
		}
		return StageOutcome{NextStage: run.Stage}, nil
	})
	locked, err := newLockedPipeline(executor, &recordingRefreshLocks{})
	if err != nil {
		t.Fatal(err)
	}
	var got StageWork
	if _, err := locked.Execute(t.Context(), pipelineRun(store.SemanticRefreshProjection, 1), func(work StageWork) error {
		got = work
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("forwarded work = %+v, want %+v", got, want)
	}
}

func TestLockedPipelineEmbeddingEmergencyPublicationReusesMaintenanceLease(t *testing.T) {
	locks := &recordingRefreshLocks{}
	executor := stageExecutorFunc(func(ctx context.Context, run store.SemanticRefreshRun, _ StageProgressCallback) (StageOutcome, error) {
		if !locks.maintenanceHeld {
			t.Fatal("embedding stage executed without maintenance")
		}
		if err := withRefreshGenerationExclusive(ctx, "embedding_l0_flush", func(context.Context) error {
			locks.events = append(locks.events, "publish")
			if !locks.maintenanceHeld || !locks.generationHeld {
				t.Fatalf("publication locks maintenance=%v generation=%v", locks.maintenanceHeld, locks.generationHeld)
			}
			return nil
		}); err != nil {
			return StageOutcome{}, err
		}
		return StageOutcome{NextStage: run.Stage}, nil
	})
	locked, err := newLockedPipeline(executor, locks)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := locked.Execute(t.Context(), pipelineRun(store.SemanticRefreshEmbedding, 1), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"maintenance.acquire",
		"generation.acquire",
		"publish",
		"generation.close",
		"maintenance.close",
	}
	if !reflect.DeepEqual(locks.events, want) {
		t.Fatalf("events=%v want=%v", locks.events, want)
	}
	if locks.maintenanceAcquires != 1 {
		t.Fatalf("maintenance acquires=%d want=1", locks.maintenanceAcquires)
	}
}

func TestLockedPipelineReleasesBeforeReturningOutcomeForCheckpoint(t *testing.T) {
	locks := &recordingRefreshLocks{}
	locked, err := newLockedPipeline(
		stageExecutorFunc(func(_ context.Context, run store.SemanticRefreshRun, _ StageProgressCallback) (StageOutcome, error) {
			return StageOutcome{NextStage: run.Stage, Checkpoint: "durable"}, nil
		}),
		locks,
	)
	if err != nil {
		t.Fatal(err)
	}

	outcome, err := locked.Execute(t.Context(), pipelineRun(store.SemanticRefreshProjection, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Checkpoint != "durable" {
		t.Fatalf("outcome=%+v", outcome)
	}
	if locks.maintenanceHeld || locks.generationHeld {
		t.Fatal("lease remained held after Execute returned to the ledger boundary")
	}
}

func TestLockedPipelineMapsLockFailureAndPreservesCallerCancellation(t *testing.T) {
	lockErr := errors.New("lock filesystem unavailable")
	run := pipelineRun(store.SemanticRefreshProjection, 1)

	locked, err := newLockedPipeline(
		stageExecutorFunc(func(context.Context, store.SemanticRefreshRun, StageProgressCallback) (StageOutcome, error) {
			t.Fatal("executor called after lock acquisition failure")
			return StageOutcome{}, nil
		}),
		&recordingRefreshLocks{acquireErr: lockErr},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = locked.Execute(t.Context(), run, nil)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorLockUnavailable || !errors.Is(err, lockErr) {
		t.Fatalf("err=%v want %s wrapping lock cause", err, ErrorLockUnavailable)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = locked.Execute(ctx, run, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire err=%v want context.Canceled", err)
	}
	if errors.As(err, &refreshErr) {
		t.Fatalf("caller cancellation was converted to lock error: %+v", refreshErr)
	}
}

func TestLockedPipelineMapsGenerationFailureAndAlwaysReleasesMaintenance(t *testing.T) {
	generationErr := errors.New("generation lock unavailable")
	locks := &recordingRefreshLocks{generationErr: generationErr}
	locked, err := newLockedPipeline(
		stageExecutorFunc(func(context.Context, store.SemanticRefreshRun, StageProgressCallback) (StageOutcome, error) {
			t.Fatal("flush executed without generation lock")
			return StageOutcome{}, nil
		}),
		locks,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = locked.Execute(t.Context(), pipelineRun(store.SemanticRefreshFlush, 1), nil)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorLockUnavailable ||
		!errors.Is(err, generationErr) {
		t.Fatalf("err=%v want %s wrapping generation cause", err, ErrorLockUnavailable)
	}
	if locks.maintenanceHeld {
		t.Fatal("maintenance remained held after generation acquisition failure")
	}
}

type stageExecutorFunc func(context.Context, store.SemanticRefreshRun, StageProgressCallback) (StageOutcome, error)

func (f stageExecutorFunc) Execute(ctx context.Context, run store.SemanticRefreshRun, progress StageProgressCallback) (StageOutcome, error) {
	return f(ctx, run, progress)
}

type recordingRefreshLocks struct {
	events              []string
	maintenanceAcquires int
	maintenanceHeld     bool
	generationHeld      bool
	acquireErr          error
	generationErr       error
}

func (l *recordingRefreshLocks) acquireMaintenanceExclusive(context.Context, string) (refreshMaintenanceLease, error) {
	l.events = append(l.events, "maintenance.acquire")
	l.maintenanceAcquires++
	if l.acquireErr != nil {
		return nil, l.acquireErr
	}
	if l.maintenanceHeld {
		return nil, errors.New("maintenance reacquired")
	}
	l.maintenanceHeld = true
	return &recordingRefreshMaintenanceLease{locks: l}, nil
}

type recordingRefreshMaintenanceLease struct {
	locks *recordingRefreshLocks
}

func (l *recordingRefreshMaintenanceLease) acquireGenerationExclusive(context.Context, string) (refreshGenerationLease, error) {
	l.locks.events = append(l.locks.events, "generation.acquire")
	if l.locks.generationErr != nil {
		return nil, l.locks.generationErr
	}
	if !l.locks.maintenanceHeld {
		return nil, errors.New("generation acquired before maintenance")
	}
	if l.locks.generationHeld {
		return nil, errors.New("generation reacquired")
	}
	l.locks.generationHeld = true
	return &recordingRefreshGenerationLease{locks: l.locks}, nil
}

func (l *recordingRefreshMaintenanceLease) Close() error {
	l.locks.events = append(l.locks.events, "maintenance.close")
	if l.locks.generationHeld {
		return errors.New("maintenance closed before generation")
	}
	l.locks.maintenanceHeld = false
	return nil
}

type recordingRefreshGenerationLease struct {
	locks *recordingRefreshLocks
}

func (l *recordingRefreshGenerationLease) Close() error {
	l.locks.events = append(l.locks.events, "generation.close")
	l.locks.generationHeld = false
	return nil
}
