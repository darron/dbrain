package semanticrefresh

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

const (
	firstRunnerRunID  = "00112233445566778899aabbccddeeff"
	secondRunnerRunID = "ffeeddccbbaa99887766554433221100"
)

type runnerLedger struct {
	mu sync.Mutex

	resume       *store.SemanticRefreshRun
	runs         map[string]store.SemanticRefreshRun
	starts       []store.StartSemanticRefreshRunInput
	updates      []store.SemanticRefreshRunUpdate
	touches      []string
	events       []string
	updateErrors map[int]error
	updateHook   func(context.Context, store.SemanticRefreshRunUpdate, int)
	startHook    func(context.Context, store.StartSemanticRefreshRunInput, int)
}

type stageLeaseRunnerLedger struct {
	*runnerLedger

	leaseMu      sync.Mutex
	lease        chan struct{}
	leaseActive  bool
	acquisitions int
	gateTouches  bool
}

func newRunnerLedger() *runnerLedger {
	return &runnerLedger{
		runs:         make(map[string]store.SemanticRefreshRun),
		updateErrors: make(map[int]error),
	}
}

func newStageLeaseRunnerLedger() *stageLeaseRunnerLedger {
	ledger := &stageLeaseRunnerLedger{
		runnerLedger: newRunnerLedger(),
		lease:        make(chan struct{}, 1),
		gateTouches:  true,
	}
	ledger.lease <- struct{}{}
	return ledger
}

func (l *stageLeaseRunnerLedger) AcquireSemanticRefreshStage(
	ctx context.Context,
) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.lease:
	}
	l.leaseMu.Lock()
	l.leaseActive = true
	l.acquisitions++
	l.leaseMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.leaseMu.Lock()
			l.leaseActive = false
			l.leaseMu.Unlock()
			l.lease <- struct{}{}
		})
	}, nil
}

func (l *stageLeaseRunnerLedger) TouchSemanticRefreshRunProgress(
	ctx context.Context,
	runID string,
	at time.Time,
) error {
	if !l.gateTouches {
		return l.runnerLedger.TouchSemanticRefreshRunProgress(ctx, runID, at)
	}
	release, err := l.AcquireSemanticRefreshStage(ctx)
	if err != nil {
		return err
	}
	defer release()
	return l.runnerLedger.TouchSemanticRefreshRunProgress(ctx, runID, at)
}

func (l *stageLeaseRunnerLedger) stageLeaseSnapshot() (bool, int) {
	l.leaseMu.Lock()
	defer l.leaseMu.Unlock()
	return l.leaseActive, l.acquisitions
}

func (l *runnerLedger) StartOrResumeSemanticRefreshRun(
	ctx context.Context,
	in store.StartSemanticRefreshRunInput,
) (store.SemanticRefreshRun, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.starts = append(l.starts, in)
	call := len(l.starts)
	l.events = append(l.events, "start:"+in.RunID)
	if l.resume != nil {
		run := *l.resume
		l.resume = nil
		run.State = store.SemanticRefreshRunRunning
		run.ErrorCode = ""
		run.ErrorText = ""
		run.Version++
		l.runs[run.RunID] = run
		if l.startHook != nil {
			l.startHook(ctx, in, call)
		}
		return run, true, nil
	}
	run := store.SemanticRefreshRun{
		RunID:               in.RunID,
		ProfileID:           in.ProfileID,
		PurgeEpoch:          in.PurgeEpoch,
		ProjectionWatermark: in.ProjectionWatermark,
		Stage:               store.SemanticRefreshProjection,
		State:               store.SemanticRefreshRunRunning,
		Counters:            in.InitialCounters,
		Version:             1,
		CreatedAt:           in.Now.UTC(),
		UpdatedAt:           in.Now.UTC(),
		LastProgressAt:      in.Now.UTC(),
	}
	l.runs[run.RunID] = run
	if l.startHook != nil {
		l.startHook(ctx, in, call)
	}
	return run, false, nil
}

func (l *runnerLedger) UpdateSemanticRefreshRun(
	ctx context.Context,
	in store.SemanticRefreshRunUpdate,
) (store.SemanticRefreshRun, error) {
	l.mu.Lock()
	call := len(l.updates) + 1
	l.updates = append(l.updates, in)
	l.events = append(l.events, fmt.Sprintf("update:%d:%s:%s", call, in.State, in.Checkpoint))
	hook := l.updateHook
	injectedErr := l.updateErrors[call]
	if injectedErr != nil {
		l.mu.Unlock()
		if hook != nil {
			hook(ctx, in, call)
		}
		return store.SemanticRefreshRun{}, injectedErr
	}
	current, ok := l.runs[in.RunID]
	if !ok || current.Version != in.ExpectedVersion {
		l.mu.Unlock()
		if hook != nil {
			hook(ctx, in, call)
		}
		return store.SemanticRefreshRun{}, store.ErrSemanticRefreshRunStale
	}
	current.EmbeddingRevision = in.EmbeddingRevision
	current.Stage = in.Stage
	current.Checkpoint = in.Checkpoint
	current.Counters = in.Counters
	current.CurrentGenerationID = in.CurrentGenerationID
	current.State = in.State
	current.ErrorCode = in.ErrorCode
	current.ErrorText = in.ErrorText
	current.ReadinessState = in.ReadinessState
	current.Version++
	current.UpdatedAt = in.Now.UTC()
	current.LastProgressAt = in.Now.UTC()
	l.runs[in.RunID] = current
	l.mu.Unlock()
	if hook != nil {
		hook(ctx, in, call)
	}
	return current, nil
}

func (l *runnerLedger) TouchSemanticRefreshRunProgress(
	_ context.Context,
	runID string,
	_ time.Time,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	run, ok := l.runs[runID]
	if !ok || run.State != store.SemanticRefreshRunRunning {
		return store.ErrSemanticRefreshRunStale
	}
	l.touches = append(l.touches, runID)
	l.events = append(l.events, "touch:"+run.Checkpoint)
	return nil
}

func (l *runnerLedger) snapshot(runID string) store.SemanticRefreshRun {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.runs[runID]
}

func (l *runnerLedger) snapshotStarts() []store.StartSemanticRefreshRunInput {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]store.StartSemanticRefreshRunInput(nil), l.starts...)
}

func (l *runnerLedger) snapshotUpdates() []store.SemanticRefreshRunUpdate {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]store.SemanticRefreshRunUpdate(nil), l.updates...)
}

func (l *runnerLedger) snapshotTouches() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.touches...)
}

type runnerExecuteStep struct {
	outcome StageOutcome
	err     error
	check   func(context.Context, store.SemanticRefreshRun)
}

type runnerExecutor struct {
	mu    sync.Mutex
	steps []runnerExecuteStep
	runs  []store.SemanticRefreshRun
}

func (e *runnerExecutor) Execute(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	e.mu.Lock()
	call := len(e.runs)
	e.runs = append(e.runs, run)
	if call >= len(e.steps) {
		e.mu.Unlock()
		return StageOutcome{}, fmt.Errorf("unexpected executor call %d", call+1)
	}
	step := e.steps[call]
	e.mu.Unlock()
	if step.check != nil {
		step.check(ctx, run)
	}
	return step.outcome, step.err
}

func (e *runnerExecutor) snapshotRuns() []store.SemanticRefreshRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]store.SemanticRefreshRun(nil), e.runs...)
}

func TestRunnerHoldsOptionalStageLeaseAcrossExecuteAndCheckpoint(t *testing.T) {
	ledger := newStageLeaseRunnerLedger()
	ledger.updateHook = func(_ context.Context, in store.SemanticRefreshRunUpdate, _ int) {
		active, _ := ledger.stageLeaseSnapshot()
		if in.State == store.SemanticRefreshRunRunning && !active {
			t.Fatal("checkpoint update ran outside semantic refresh stage lease")
		}
		if in.State == store.SemanticRefreshRunCompleted && active {
			t.Fatal("terminal update ran inside semantic refresh stage lease")
		}
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:  store.SemanticRefreshReadiness,
			Checkpoint: "readiness:ready",
			Readiness:  "ready",
			Complete:   true,
		},
		check: func(_ context.Context, _ store.SemanticRefreshRun) {
			if active, _ := ledger.stageLeaseSnapshot(); !active {
				t.Fatal("executor ran outside semantic refresh stage lease")
			}
		},
	}}}

	result, err := Run(
		t.Context(),
		ledger,
		executor,
		runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunCompleted {
		t.Fatalf("result=%+v, want completed run", result)
	}
	if active, acquisitions := ledger.stageLeaseSnapshot(); active || acquisitions < 2 {
		t.Fatalf("stage lease active=%t acquisitions=%d, want false and at least 2", active, acquisitions)
	}
}

func TestRunnerStageLeaseAcquisitionIsContextCancellable(t *testing.T) {
	ledger := newStageLeaseRunnerLedger()
	ledger.gateTouches = false
	release, err := ledger.AcquireSemanticRefreshStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	request := runnerRequest(func() (string, error) { return firstRunnerRunID, nil })
	request.Progress = func(Progress) error {
		cancel()
		return nil
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{{outcome: StageOutcome{
		NextStage:  store.SemanticRefreshReadiness,
		Checkpoint: "should-not-run",
		Complete:   true,
	}}}}

	result, err := Run(ctx, ledger, executor, request)
	_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
	if runs := executor.snapshotRuns(); len(runs) != 0 {
		t.Fatalf("executor calls=%d, want 0 while cancelled stage acquisition was blocked", len(runs))
	}
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunCancelled {
		t.Fatalf("result=%+v, want cancelled run", result)
	}
}

func TestRunnerStartsAtProjectionPersistsBeforeNextExecuteAndCompletes(t *testing.T) {
	ledger := newRunnerLedger()
	var callbackMu sync.Mutex
	var callbackCheckpoints []string
	request := runnerRequest(func() (string, error) { return firstRunnerRunID, nil })
	request.Progress = func(progress Progress) error {
		if touches := ledger.snapshotTouches(); len(touches) == 0 {
			t.Fatal("progress callback ran before durable touch")
		}
		callbackMu.Lock()
		callbackCheckpoints = append(callbackCheckpoints, progress.Checkpoint)
		callbackMu.Unlock()
		return nil
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{
		{
			outcome: StageOutcome{
				NextStage:           store.SemanticRefreshEmbedding,
				Checkpoint:          "projection:parent-10",
				EmbeddingRevision:   4,
				Counters:            store.SemanticRefreshCounters{ProjectedParents: 10},
				CurrentGenerationID: "generation-a",
				Readiness:           "building",
				Debt:                Debt{PendingEmbeddings: 8},
			},
			check: func(_ context.Context, run store.SemanticRefreshRun) {
				if run.Stage != store.SemanticRefreshProjection || run.Checkpoint != "" {
					t.Fatalf("new run = %+v, want initial projection checkpoint", run)
				}
			},
		},
		{
			outcome: StageOutcome{
				NextStage:           store.SemanticRefreshReadiness,
				Checkpoint:          "readiness:ready",
				EmbeddingRevision:   5,
				Counters:            store.SemanticRefreshCounters{ProjectedParents: 10, EmbeddedChunks: 8},
				CurrentGenerationID: "generation-b",
				Readiness:           "ready",
				Debt:                Debt{},
				Complete:            true,
			},
			check: func(_ context.Context, run store.SemanticRefreshRun) {
				if run.Stage != store.SemanticRefreshEmbedding ||
					run.Checkpoint != "projection:parent-10" ||
					run.EmbeddingRevision != 4 ||
					run.Counters.ProjectedParents != 10 ||
					run.CurrentGenerationID != "generation-a" ||
					run.ReadinessState != "building" {
					t.Fatalf("second execute did not receive persisted checkpoint: %+v", run)
				}
				if got := ledger.snapshot(run.RunID); got.Version != run.Version {
					t.Fatalf("ledger version = %d, executor version = %d", got.Version, run.Version)
				}
			},
		},
	}}

	result, err := Run(t.Context(), ledger, executor, request)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCompleted || result.Run == nil {
		t.Fatalf("result = %+v, want completed run", result)
	}
	if result.Run.State != store.SemanticRefreshRunCompleted ||
		result.Run.Checkpoint != "readiness:ready" ||
		result.Run.Version != 4 {
		t.Fatalf("completed run = %+v", result.Run)
	}
	if starts := ledger.snapshotStarts(); len(starts) != 1 ||
		starts[0].RunID != firstRunnerRunID ||
		starts[0].ProjectionWatermark != request.ProjectionWatermark {
		t.Fatalf("starts = %+v", starts)
	}
	if runs := executor.snapshotRuns(); len(runs) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(runs))
	}
	updates := ledger.snapshotUpdates()
	if len(updates) != 3 ||
		updates[0].State != store.SemanticRefreshRunRunning ||
		updates[1].State != store.SemanticRefreshRunRunning ||
		updates[2].State != store.SemanticRefreshRunCompleted {
		t.Fatalf("updates = %+v, want two checkpoints then terminal completion", updates)
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	if got, want := strings.Join(callbackCheckpoints, ","), ",projection:parent-10,readiness:ready"; got != want {
		t.Fatalf("published checkpoints = %q, want %q", got, want)
	}
}

func TestRunnerResumesExactImmutableAndCheckpointState(t *testing.T) {
	ledger := newRunnerLedger()
	resumed := store.SemanticRefreshRun{
		RunID:               firstRunnerRunID,
		ProfileID:           "profile-a",
		PurgeEpoch:          7,
		ProjectionWatermark: 91,
		EmbeddingRevision:   13,
		Stage:               store.SemanticRefreshFlush,
		Checkpoint:          "flush:segment-4",
		Counters: store.SemanticRefreshCounters{
			ProjectedParents: 9,
			EmbeddedChunks:   20,
			SuccessorRuns:    2,
		},
		CurrentGenerationID: "generation-resume",
		State:               store.SemanticRefreshRunCancelled,
		ReadinessState:      "building",
		Version:             8,
	}
	ledger.resume = &resumed
	request := runnerRequest(func() (string, error) { return secondRunnerRunID, nil })
	request.PurgeEpoch = 7
	request.ProjectionWatermark = 999
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:           store.SemanticRefreshReadiness,
			Checkpoint:          "done",
			EmbeddingRevision:   13,
			Counters:            resumed.Counters,
			CurrentGenerationID: resumed.CurrentGenerationID,
			Readiness:           "ready",
			Complete:            true,
		},
		check: func(_ context.Context, run store.SemanticRefreshRun) {
			if run.RunID != resumed.RunID ||
				run.ProfileID != resumed.ProfileID ||
				run.PurgeEpoch != resumed.PurgeEpoch ||
				run.ProjectionWatermark != resumed.ProjectionWatermark ||
				run.EmbeddingRevision != resumed.EmbeddingRevision ||
				run.Stage != resumed.Stage ||
				run.Checkpoint != resumed.Checkpoint ||
				run.Counters != resumed.Counters ||
				run.CurrentGenerationID != resumed.CurrentGenerationID ||
				run.Version != resumed.Version+1 {
				t.Fatalf("resumed run changed durable state: got %+v want %+v", run, resumed)
			}
		},
	}}}

	result, err := Run(t.Context(), ledger, executor, request)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Run == nil || result.Run.RunID != resumed.RunID ||
		result.Run.ProjectionWatermark != resumed.ProjectionWatermark {
		t.Fatalf("result run = %+v, want original run and watermark", result.Run)
	}
}

func TestRunnerClearsUnsafeGenerationFromResumedRunBeforeUse(t *testing.T) {
	for name, unsafeGeneration := range map[string]string{
		"path_like": "/Users/alice/private/semantic-index",
		"oversized": strings.Repeat("g", generationIDLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			ledger := newRunnerLedger()
			resumed := store.SemanticRefreshRun{
				RunID:               firstRunnerRunID,
				ProfileID:           "profile-a",
				PurgeEpoch:          7,
				ProjectionWatermark: 91,
				EmbeddingRevision:   13,
				Stage:               store.SemanticRefreshFlush,
				Checkpoint:          "flush:segment-4",
				Counters:            store.SemanticRefreshCounters{FlushedVectors: 8},
				CurrentGenerationID: unsafeGeneration,
				State:               store.SemanticRefreshRunFailed,
				ReadinessState:      "building",
				Version:             8,
			}
			ledger.resume = &resumed
			var progressSnapshots []string
			request := runnerRequest(func() (string, error) { return secondRunnerRunID, nil })
			request.Progress = func(progress Progress) error {
				progressSnapshots = append(progressSnapshots, fmt.Sprintf("%+v", progress))
				return nil
			}
			executor := &runnerExecutor{steps: []runnerExecuteStep{{}}}

			result, err := Run(t.Context(), ledger, executor, request)
			_ = assertRefreshError(t, err, ErrorFlush, nil)

			executed := executor.snapshotRuns()
			if len(executed) != 1 || executed[0].CurrentGenerationID != "" {
				t.Fatalf("executor received unsafe resumed generation: %+v", executed)
			}
			if len(progressSnapshots) == 0 {
				t.Fatal("resume emitted no observable progress")
			}
			for _, progress := range progressSnapshots {
				if strings.Contains(progress, unsafeGeneration) {
					t.Fatalf("progress exposed unsafe resumed generation: %q", progress)
				}
			}
			updates := ledger.snapshotUpdates()
			if len(updates) != 1 || updates[0].CurrentGenerationID != "" {
				t.Fatalf("terminal updates retained unsafe resumed generation: %+v", updates)
			}
			if result.Run == nil || result.Run.CurrentGenerationID != "" ||
				strings.Contains(fmt.Sprintf("%+v", result.Run), unsafeGeneration) {
				t.Fatalf("result exposed unsafe resumed generation: %+v", result)
			}
			durable := ledger.snapshot(resumed.RunID)
			if durable.State != store.SemanticRefreshRunFailed ||
				durable.CurrentGenerationID != "" {
				t.Fatalf("terminal row did not clear unsafe resumed generation: %+v", durable)
			}
		})
	}
}

func TestRunnerPersistsPartialOutcomeBeforeBoundedFailure(t *testing.T) {
	ledger := newRunnerLedger()
	stageErr := errors.New(`provider failed with private path /Users/alice/corpus.db and vector [0.1,0.2]`)
	longCheckpoint := "projection:" + strings.Repeat("a", 300)
	longReadiness := "not_ready_" + strings.Repeat("a", 80)
	outcome := StageOutcome{
		NextStage:           store.SemanticRefreshEmbedding,
		Checkpoint:          longCheckpoint,
		EmbeddingRevision:   44,
		Counters:            store.SemanticRefreshCounters{ProjectedParents: 17, EmbeddedChunks: 3},
		CurrentGenerationID: "generation-partial",
		Readiness:           longReadiness,
		Debt:                Debt{PendingEmbeddings: 14, Segments: 3},
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{{outcome: outcome, err: stageErr}}}

	result, err := Run(
		t.Context(),
		ledger,
		executor,
		runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
	)
	refreshErr := assertRefreshError(t, err, ErrorProjection, stageErr)
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunFailed {
		t.Fatalf("failure result = %+v, want persisted failed row", result)
	}
	if result.Debt != outcome.Debt {
		t.Fatalf("failure debt = %+v, want %+v", result.Debt, outcome.Debt)
	}
	updates := ledger.snapshotUpdates()
	if len(updates) != 2 {
		t.Fatalf("updates = %d, want partial checkpoint then failure", len(updates))
	}
	checkpoint, failed := updates[0], updates[1]
	if checkpoint.State != store.SemanticRefreshRunRunning ||
		checkpoint.Stage != outcome.NextStage ||
		checkpoint.EmbeddingRevision != outcome.EmbeddingRevision ||
		checkpoint.Counters != outcome.Counters ||
		checkpoint.CurrentGenerationID != outcome.CurrentGenerationID {
		t.Fatalf("partial checkpoint not fully persisted: %+v", checkpoint)
	}
	if len(checkpoint.Checkpoint) > errorCheckpointLimit ||
		len(checkpoint.ReadinessState) > errorReadinessLimit {
		t.Fatalf("checkpoint fields not bounded: checkpoint=%d readiness=%d", len(checkpoint.Checkpoint), len(checkpoint.ReadinessState))
	}
	if failed.State != store.SemanticRefreshRunFailed ||
		failed.Checkpoint != checkpoint.Checkpoint ||
		failed.Stage != checkpoint.Stage ||
		failed.ErrorCode != ErrorProjection ||
		failed.ErrorText != ErrorProjection ||
		failed.ReadinessState != checkpoint.ReadinessState {
		t.Fatalf("failed update did not preserve current checkpoint: checkpoint=%+v failed=%+v", checkpoint, failed)
	}
	if refreshErr.Stage != store.SemanticRefreshProjection ||
		refreshErr.Checkpoint != checkpoint.Checkpoint ||
		refreshErr.Readiness != checkpoint.ReadinessState ||
		refreshErr.Debt != outcome.Debt {
		t.Fatalf("typed failure does not describe durable checkpoint: %+v", refreshErr)
	}
	for _, leaked := range []string{"/Users/alice", "corpus.db", "0.1"} {
		if strings.Contains(failed.ErrorText, leaked) {
			t.Fatalf("failed diagnostics leaked %q: %+v", leaked, failed)
		}
	}
}

func TestRunnerEmptySuccessfulOutcomeFailsOnceWithoutHotLoop(t *testing.T) {
	ledger := newRunnerLedger()
	executor := &runnerExecutor{steps: []runnerExecuteStep{{}}}

	result, err := Run(
		t.Context(),
		ledger,
		executor,
		runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
	)
	_ = assertRefreshError(t, err, ErrorProjection, nil)
	if calls := len(executor.snapshotRuns()); calls != 1 {
		t.Fatalf("executor calls = %d, want one bounded attempt", calls)
	}
	updates := ledger.snapshotUpdates()
	if len(updates) != 1 || updates[0].State != store.SemanticRefreshRunFailed {
		t.Fatalf("empty outcome updates = %+v, want one terminal failed checkpoint", updates)
	}
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunFailed ||
		result.Outcome == OutcomeCompleted {
		t.Fatalf("empty outcome result = %+v, want non-success failed row", result)
	}
}

func TestRunnerPreservesExecutorRefreshErrorPointerOverProgressFailure(t *testing.T) {
	ledger := newRunnerLedger()
	rawCause := errors.New("embedding circuit raw cause")
	typed := NewError(
		ErrorEmbeddingCircuit,
		store.SemanticRefreshRun{
			RunID:      "stale-run",
			Stage:      store.SemanticRefreshEmbedding,
			Checkpoint: "stale-checkpoint",
		},
		"stale",
		Debt{PendingEmbeddings: 99},
		rawCause,
	)
	progressErr := errors.New("progress sink also failed")
	callbacks := 0
	request := runnerRequest(func() (string, error) { return firstRunnerRunID, nil })
	request.Progress = func(Progress) error {
		callbacks++
		if callbacks == 2 {
			return progressErr
		}
		return nil
	}
	outcome := StageOutcome{
		NextStage:           store.SemanticRefreshEmbedding,
		Checkpoint:          "projection:typed-error",
		EmbeddingRevision:   12,
		Counters:            store.SemanticRefreshCounters{ProjectedParents: 4},
		CurrentGenerationID: "semantic-root-v1:00112233445566778899aabbccddeeff",
		Readiness:           "building",
		Debt:                Debt{PendingEmbeddings: 4},
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{{outcome: outcome, err: typed}}}

	result, err := Run(t.Context(), ledger, executor, request)
	if err != typed {
		t.Fatalf("returned error pointer = %p (%v), want executor pointer %p", err, err, typed)
	}
	if typed.Code != ErrorEmbeddingCircuit ||
		typed.Stage != store.SemanticRefreshProjection ||
		typed.RunID != firstRunnerRunID ||
		typed.Checkpoint != outcome.Checkpoint ||
		typed.Readiness != outcome.Readiness ||
		typed.Debt != outcome.Debt {
		t.Fatalf("executor typed error not refreshed from persisted state: %+v", typed)
	}
	if !errors.Is(typed, rawCause) {
		t.Fatal("executor typed error lost its raw cause")
	}
	if errors.Is(typed, progressErr) {
		t.Fatal("simultaneous progress failure replaced executor typed cause")
	}
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunFailed ||
		result.Run.ErrorCode != ErrorEmbeddingCircuit {
		t.Fatalf("typed executor failure result = %+v", result)
	}
}

func TestRunnerTypedExecutorErrorStillLosesToCheckpointCASFailure(t *testing.T) {
	ledger := newRunnerLedger()
	updateErr := errors.New("checkpoint CAS failed")
	ledger.updateErrors[1] = updateErr
	typed := NewError(
		ErrorEmbeddingCircuit,
		store.SemanticRefreshRun{Stage: store.SemanticRefreshEmbedding},
		"",
		Debt{},
		errors.New("provider circuit"),
	)
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{NextStage: store.SemanticRefreshEmbedding, Checkpoint: "projection:cas"},
		err:     typed,
	}}}

	_, err := Run(
		t.Context(),
		ledger,
		executor,
		runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
	)
	if err == typed {
		t.Fatal("typed executor error escaped despite failed checkpoint CAS")
	}
	_ = assertRefreshError(t, err, ErrorRunConflict, updateErr)
}

func TestRunnerValidatesGenerationIDBeforeLedgerUpdate(t *testing.T) {
	valid := "semantic-root-v1:00112233445566778899aabbccddeeff"
	t.Run("actual root ID is accepted", func(t *testing.T) {
		ledger := newRunnerLedger()
		executor := &runnerExecutor{steps: []runnerExecuteStep{{
			outcome: StageOutcome{
				NextStage:           store.SemanticRefreshReadiness,
				Checkpoint:          "ready",
				CurrentGenerationID: valid,
				Readiness:           "ready",
				Complete:            true,
			},
		}}}
		result, err := Run(
			t.Context(),
			ledger,
			executor,
			runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
		)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Run == nil || result.Run.CurrentGenerationID != valid {
			t.Fatalf("valid generation result = %+v", result)
		}
	})

	for _, invalid := range []string{
		"/Users/alice/private/corpus.db",
		strings.Repeat("a", 65),
		strings.Repeat("界", 30),
	} {
		t.Run(fmt.Sprintf("reject_%d_bytes", len(invalid)), func(t *testing.T) {
			ledger := newRunnerLedger()
			outcome := StageOutcome{
				NextStage:           store.SemanticRefreshVerify,
				Checkpoint:          "flush:committed",
				EmbeddingRevision:   17,
				Counters:            store.SemanticRefreshCounters{FlushedVectors: 8},
				CurrentGenerationID: invalid,
				Readiness:           "building",
				Debt:                Debt{Segments: 2},
				Complete:            true,
			}
			executor := &runnerExecutor{steps: []runnerExecuteStep{{outcome: outcome}}}

			result, err := Run(
				t.Context(),
				ledger,
				executor,
				runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
			)
			_ = assertRefreshError(t, err, ErrorProjection, nil)
			if result.Run == nil || result.Run.State != store.SemanticRefreshRunFailed ||
				result.Run.Checkpoint != outcome.Checkpoint ||
				result.Run.EmbeddingRevision != outcome.EmbeddingRevision ||
				result.Run.Counters != outcome.Counters ||
				result.Debt != outcome.Debt {
				t.Fatalf("invalid generation did not preserve safe committed outcome fields: %+v", result)
			}
			for _, update := range ledger.snapshotUpdates() {
				if update.CurrentGenerationID == invalid ||
					len(update.CurrentGenerationID) > errorRunIDLimit ||
					strings.Contains(update.CurrentGenerationID, "/") {
					t.Fatalf("invalid generation reached ledger: %+v", update)
				}
			}
			if calls := len(executor.snapshotRuns()); calls != 1 {
				t.Fatalf("executor calls = %d, want one", calls)
			}
		})
	}
}

func TestRunnerTerminalUpdateFailureWinsOverExecutorFailure(t *testing.T) {
	ledger := newRunnerLedger()
	updateErr := errors.New("terminal checkpoint unavailable")
	executeErr := NewError(
		ErrorEmbeddingCircuit,
		store.SemanticRefreshRun{Stage: store.SemanticRefreshEmbedding},
		"",
		Debt{},
		errors.New("projection provider failed"),
	)
	ledger.updateErrors[1] = updateErr
	executor := &runnerExecutor{steps: []runnerExecuteStep{{err: executeErr}}}

	result, err := Run(
		t.Context(),
		ledger,
		executor,
		runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
	)
	_ = assertRefreshError(t, err, ErrorRunConflict, updateErr)
	if err == executeErr || errors.Is(err, executeErr) {
		t.Fatalf("terminal update failure did not take precedence over typed executor error: %v", err)
	}
	if result.Outcome == OutcomeCompleted {
		t.Fatalf("terminal update failure returned false completion: %+v", result)
	}
	if updates := ledger.snapshotUpdates(); len(updates) != 1 ||
		updates[0].State != store.SemanticRefreshRunFailed {
		t.Fatalf("terminal update attempts = %+v, want one failed checkpoint", updates)
	}
}

func TestRunnerImmediateProgressFailureCheckpointsWithoutExecuting(t *testing.T) {
	ledger := newRunnerLedger()
	progressErr := errors.New("initial progress output failed")
	request := runnerRequest(func() (string, error) { return firstRunnerRunID, nil })
	request.Progress = func(Progress) error { return progressErr }
	executor := &runnerExecutor{}

	result, err := Run(t.Context(), ledger, executor, request)
	_ = assertRefreshError(t, err, ErrorProjection, progressErr)
	if calls := len(executor.snapshotRuns()); calls != 0 {
		t.Fatalf("executor calls = %d, want no work after immediate progress failure", calls)
	}
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunFailed {
		t.Fatalf("initial progress failure result = %+v", result)
	}
	if updates := ledger.snapshotUpdates(); len(updates) != 1 ||
		updates[0].State != store.SemanticRefreshRunFailed {
		t.Fatalf("initial progress failure checkpoints = %+v, want exactly one", updates)
	}
}

func TestRunnerCASFailureWinsAndNeverCallsNextStage(t *testing.T) {
	for _, test := range []struct {
		name        string
		updateErr   error
		executorErr error
	}{
		{
			name:      "stale CAS",
			updateErr: store.ErrSemanticRefreshRunStale,
		},
		{
			name:        "checkpoint write failure beats executor failure",
			updateErr:   errors.New("ledger disk unavailable"),
			executorErr: errors.New("stage also failed"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := newRunnerLedger()
			ledger.updateErrors[1] = test.updateErr
			executor := &runnerExecutor{steps: []runnerExecuteStep{
				{
					outcome: StageOutcome{
						NextStage:  store.SemanticRefreshEmbedding,
						Checkpoint: "projection:10",
					},
					err: test.executorErr,
				},
				{outcome: StageOutcome{Complete: true}},
			}}

			result, err := Run(
				t.Context(),
				ledger,
				executor,
				runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
			)
			_ = assertRefreshError(t, err, ErrorRunConflict, test.updateErr)
			if test.executorErr != nil && errors.Is(err, test.executorErr) {
				t.Fatalf("returned error retained lower-priority executor error: %v", err)
			}
			if result.Outcome == OutcomeCompleted {
				t.Fatalf("CAS failure returned false completion: %+v", result)
			}
			if calls := len(executor.snapshotRuns()); calls != 1 {
				t.Fatalf("executor calls = %d, want no next stage after CAS failure", calls)
			}
			if updates := ledger.snapshotUpdates(); len(updates) != 1 {
				t.Fatalf("updates = %d, want no terminal overwrite after CAS failure", len(updates))
			}
		})
	}
}

func TestRunnerParentCancellationUsesIndependentBoundedCheckpoint(t *testing.T) {
	t.Run("checkpoint succeeds", func(t *testing.T) {
		ledger := newRunnerLedger()
		var checkpointContextErr error
		var checkpointDeadline time.Time
		ledger.updateHook = func(ctx context.Context, in store.SemanticRefreshRunUpdate, _ int) {
			if in.State == store.SemanticRefreshRunCancelled {
				checkpointContextErr = ctx.Err()
				checkpointDeadline, _ = ctx.Deadline()
			}
		}
		parent, cancel := context.WithCancel(context.Background())
		executor := &runnerExecutor{steps: []runnerExecuteStep{{
			check: func(ctx context.Context, _ store.SemanticRefreshRun) {
				cancel()
				<-ctx.Done()
			},
			err: context.Canceled,
		}}}
		before := time.Now()

		result, err := Run(
			parent,
			ledger,
			executor,
			runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
		)
		_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
		if checkpointContextErr != nil {
			t.Fatalf("cancellation checkpoint inherited cancellation: %v", checkpointContextErr)
		}
		if checkpointDeadline.IsZero() ||
			checkpointDeadline.Before(before.Add(1500*time.Millisecond)) ||
			checkpointDeadline.After(time.Now().Add(2100*time.Millisecond)) {
			t.Fatalf("cancellation checkpoint deadline = %v, want hard two-second bound", checkpointDeadline)
		}
		if result.Run == nil || result.Run.State != store.SemanticRefreshRunCancelled {
			t.Fatalf("cancellation result = %+v, want persisted cancelled row", result)
		}
		updates := ledger.snapshotUpdates()
		if len(updates) != 1 || updates[0].State != store.SemanticRefreshRunCancelled {
			t.Fatalf("cancellation updates = %+v, want exactly one cancelled checkpoint", updates)
		}
	})

	t.Run("checkpoint failure still returns cancellation", func(t *testing.T) {
		ledger := newRunnerLedger()
		checkpointErr := errors.New("cannot persist cancellation")
		ledger.updateErrors[1] = checkpointErr
		parent, cancel := context.WithCancel(context.Background())
		executor := &runnerExecutor{steps: []runnerExecuteStep{{
			check: func(ctx context.Context, _ store.SemanticRefreshRun) {
				cancel()
				<-ctx.Done()
			},
			err: context.Canceled,
		}}}

		result, err := Run(
			parent,
			ledger,
			executor,
			runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
		)
		_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
		if !errors.Is(err, checkpointErr) {
			t.Fatalf("cancellation error does not retain checkpoint failure: %v", err)
		}
		if result.Outcome == OutcomeCompleted {
			t.Fatalf("failed cancellation checkpoint returned false success: %+v", result)
		}
		if updates := ledger.snapshotUpdates(); len(updates) != 1 {
			t.Fatalf("cancellation checkpoint attempts = %d, want exactly one", len(updates))
		}
	})

	t.Run("cancellation racing a stage checkpoint still pauses", func(t *testing.T) {
		ledger := newRunnerLedger()
		parent, cancel := context.WithCancel(context.Background())
		ledger.updateErrors[1] = context.Canceled
		ledger.updateHook = func(_ context.Context, _ store.SemanticRefreshRunUpdate, call int) {
			if call == 1 {
				cancel()
			}
		}
		executor := &runnerExecutor{steps: []runnerExecuteStep{{
			outcome: StageOutcome{
				NextStage:  store.SemanticRefreshEmbedding,
				Checkpoint: "projection:raced",
			},
		}}}

		result, err := Run(
			parent,
			ledger,
			executor,
			runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
		)
		_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
		if result.Run == nil || result.Run.State != store.SemanticRefreshRunCancelled {
			t.Fatalf("raced cancellation result = %+v, want cancelled prior checkpoint", result)
		}
		updates := ledger.snapshotUpdates()
		if len(updates) != 2 ||
			updates[0].State != store.SemanticRefreshRunRunning ||
			updates[1].State != store.SemanticRefreshRunCancelled {
			t.Fatalf("raced cancellation updates = %+v, want failed stage CAS then cancelled checkpoint", updates)
		}
	})
}

func TestRunnerSuccessorCompletesOldCarriesCountAndContinuesAtProjection(t *testing.T) {
	ledger := newRunnerLedger()
	ids := []string{firstRunnerRunID, secondRunnerRunID}
	request := runnerRequest(func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	request.ProjectionWatermark = 10
	successorWatermark := int64(14)
	executor := &runnerExecutor{steps: []runnerExecuteStep{
		{
			outcome: StageOutcome{
				NextStage:           store.SemanticRefreshReadiness,
				Checkpoint:          "ready:10",
				Counters:            store.SemanticRefreshCounters{ProjectedParents: 8, SuccessorRuns: 2},
				CurrentGenerationID: "generation-10",
				Readiness:           "ready",
				Complete:            true,
				SuccessorWatermark:  &successorWatermark,
			},
		},
		{
			outcome: StageOutcome{
				NextStage:  store.SemanticRefreshReadiness,
				Checkpoint: "ready:14",
				Counters:   store.SemanticRefreshCounters{SuccessorRuns: 3},
				Readiness:  "ready",
				Complete:   true,
			},
			check: func(_ context.Context, run store.SemanticRefreshRun) {
				if run.RunID != secondRunnerRunID ||
					run.ProjectionWatermark != successorWatermark ||
					run.Stage != store.SemanticRefreshProjection ||
					run.Checkpoint != "" ||
					run.Counters != (store.SemanticRefreshCounters{SuccessorRuns: 3}) {
					t.Fatalf("successor executor input = %+v", run)
				}
			},
		},
	}}

	result, err := Run(t.Context(), ledger, executor, request)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Run == nil ||
		result.Run.RunID != secondRunnerRunID ||
		result.Run.State != store.SemanticRefreshRunCompleted ||
		result.Run.Counters.SuccessorRuns != 3 {
		t.Fatalf("successor result = %+v", result)
	}
	starts := ledger.snapshotStarts()
	if len(starts) != 2 ||
		starts[0].ProjectionWatermark != 10 ||
		starts[1].ProjectionWatermark != successorWatermark ||
		starts[1].RunID != secondRunnerRunID ||
		starts[1].InitialCounters != (store.SemanticRefreshCounters{SuccessorRuns: 3}) {
		t.Fatalf("successor starts = %+v", starts)
	}
	updates := ledger.snapshotUpdates()
	if len(updates) != 4 {
		t.Fatalf("updates = %d, want old checkpoint/completion and successor checkpoint/completion with no initialization CAS", len(updates))
	}
	old := ledger.snapshot(firstRunnerRunID)
	if old.State != store.SemanticRefreshRunCompleted ||
		old.ProjectionWatermark != 10 ||
		old.Counters.SuccessorRuns != 2 {
		t.Fatalf("old run = %+v, want completed immutable watermark", old)
	}
}

func TestRunnerCancellationAfterCompletedCASReturnsNonSuccess(t *testing.T) {
	ledger := newRunnerLedger()
	parent, cancel := context.WithCancel(context.Background())
	ledger.updateHook = func(_ context.Context, update store.SemanticRefreshRunUpdate, _ int) {
		if update.State == store.SemanticRefreshRunCompleted {
			cancel()
		}
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:  store.SemanticRefreshReadiness,
			Checkpoint: "ready",
			Readiness:  "ready",
			Complete:   true,
		},
	}}}

	result, err := Run(
		parent,
		ledger,
		executor,
		runnerRequest(func() (string, error) { return firstRunnerRunID, nil }),
	)
	_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
	if result.Outcome == OutcomeCompleted || result.Run == nil ||
		result.Run.State != store.SemanticRefreshRunCompleted {
		t.Fatalf("completion-linearized cancellation result = %+v, want non-success with durable completed row", result)
	}
	updates := ledger.snapshotUpdates()
	if len(updates) != 2 || updates[1].State != store.SemanticRefreshRunCompleted {
		t.Fatalf("completion-linearized updates = %+v, want no impossible cancelled rewrite", updates)
	}
}

func TestRunnerCancellationAfterOldCompletionStartsNoSuccessor(t *testing.T) {
	ledger := newRunnerLedger()
	parent, cancel := context.WithCancel(context.Background())
	ledger.updateHook = func(_ context.Context, update store.SemanticRefreshRunUpdate, _ int) {
		if update.RunID == firstRunnerRunID &&
			update.State == store.SemanticRefreshRunCompleted {
			cancel()
		}
	}
	successorWatermark := int64(11)
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:          store.SemanticRefreshReadiness,
			Checkpoint:         "ready",
			Counters:           store.SemanticRefreshCounters{SuccessorRuns: 4},
			Readiness:          "ready",
			Complete:           true,
			SuccessorWatermark: &successorWatermark,
		},
	}}}
	ids := []string{firstRunnerRunID, secondRunnerRunID}
	request := runnerRequest(func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})

	result, err := Run(parent, ledger, executor, request)
	_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
	if result.Outcome == OutcomeCompleted || result.Run == nil ||
		result.Run.State != store.SemanticRefreshRunCompleted {
		t.Fatalf("pre-successor cancellation result = %+v", result)
	}
	if starts := ledger.snapshotStarts(); len(starts) != 1 {
		t.Fatalf("starts = %+v, want no successor after cancellation", starts)
	}
}

func TestRunnerCancellationAfterAtomicSuccessorStartPausesSuccessorOnce(t *testing.T) {
	ledger := newRunnerLedger()
	parent, cancel := context.WithCancel(context.Background())
	var cancelledContextErr error
	var cancelledDeadline time.Time
	ledger.startHook = func(_ context.Context, _ store.StartSemanticRefreshRunInput, call int) {
		if call == 2 {
			cancel()
		}
	}
	ledger.updateHook = func(ctx context.Context, update store.SemanticRefreshRunUpdate, _ int) {
		if update.RunID == secondRunnerRunID &&
			update.State == store.SemanticRefreshRunCancelled {
			cancelledContextErr = ctx.Err()
			cancelledDeadline, _ = ctx.Deadline()
		}
	}
	successorWatermark := int64(11)
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:          store.SemanticRefreshReadiness,
			Checkpoint:         "ready",
			Counters:           store.SemanticRefreshCounters{SuccessorRuns: 4},
			Readiness:          "ready",
			Complete:           true,
			SuccessorWatermark: &successorWatermark,
		},
	}}}
	ids := []string{firstRunnerRunID, secondRunnerRunID}
	request := runnerRequest(func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	before := time.Now()

	result, err := Run(parent, ledger, executor, request)
	_ = assertRefreshError(t, err, ErrorCancelled, context.Canceled)
	if result.Outcome == OutcomeCompleted || result.Run == nil ||
		result.Run.RunID != secondRunnerRunID ||
		result.Run.State != store.SemanticRefreshRunCancelled ||
		result.Run.Counters.SuccessorRuns != 5 {
		t.Fatalf("post-successor-start cancellation result = %+v", result)
	}
	if cancelledContextErr != nil {
		t.Fatalf("successor cancellation checkpoint inherited cancellation: %v", cancelledContextErr)
	}
	if cancelledDeadline.IsZero() ||
		cancelledDeadline.Before(before.Add(1500*time.Millisecond)) ||
		cancelledDeadline.After(time.Now().Add(2100*time.Millisecond)) {
		t.Fatalf("successor cancellation deadline = %v, want hard two-second bound", cancelledDeadline)
	}
	if calls := len(executor.snapshotRuns()); calls != 1 {
		t.Fatalf("executor calls = %d, want no successor execution after cancellation", calls)
	}
	starts := ledger.snapshotStarts()
	if len(starts) != 2 ||
		starts[1].InitialCounters != (store.SemanticRefreshCounters{SuccessorRuns: 5}) {
		t.Fatalf("successor starts = %+v", starts)
	}
	updates := ledger.snapshotUpdates()
	successorUpdates := 0
	for _, update := range updates {
		if update.RunID == secondRunnerRunID {
			successorUpdates++
			if update.State != store.SemanticRefreshRunCancelled {
				t.Fatalf("unexpected post-start successor update: %+v", update)
			}
		}
	}
	if successorUpdates != 1 {
		t.Fatalf("successor updates = %d, want exactly one cancelled checkpoint", successorUpdates)
	}
}

func TestRunnerRejectsNonIncreasingSuccessorWithoutStartingAnotherRun(t *testing.T) {
	for _, successorWatermark := range []int64{9, 10} {
		t.Run(fmt.Sprintf("watermark_%d", successorWatermark), func(t *testing.T) {
			ledger := newRunnerLedger()
			request := runnerRequest(func() (string, error) { return firstRunnerRunID, nil })
			request.ProjectionWatermark = 10
			executor := &runnerExecutor{steps: []runnerExecuteStep{{
				outcome: StageOutcome{
					NextStage:          store.SemanticRefreshReadiness,
					Checkpoint:         "invalid-successor",
					Complete:           true,
					SuccessorWatermark: &successorWatermark,
				},
			}}}

			result, err := Run(t.Context(), ledger, executor, request)
			_ = assertRefreshError(t, err, ErrorRunConflict, nil)
			if result.Outcome == OutcomeCompleted {
				t.Fatalf("invalid successor returned false completion: %+v", result)
			}
			if starts := ledger.snapshotStarts(); len(starts) != 1 {
				t.Fatalf("starts = %d, want no successor", len(starts))
			}
			if old := ledger.snapshot(firstRunnerRunID); old.State != store.SemanticRefreshRunFailed ||
				old.Checkpoint != "invalid-successor" {
				t.Fatalf("invalid successor did not preserve a failed resumable checkpoint: %+v", old)
			}
		})
	}
}

func TestRunnerProgressCallbackFailureStopsFurtherExecutionAndCheckpointsOnce(t *testing.T) {
	ledger := newRunnerLedger()
	progressErr := errors.New("progress sink closed")
	var callbackCalls int
	request := runnerRequest(func() (string, error) { return firstRunnerRunID, nil })
	request.Progress = func(Progress) error {
		callbackCalls++
		if callbackCalls == 2 {
			return progressErr
		}
		return nil
	}
	executor := &runnerExecutor{steps: []runnerExecuteStep{
		{
			outcome: StageOutcome{
				NextStage:  store.SemanticRefreshEmbedding,
				Checkpoint: "projection:checkpoint",
				Debt:       Debt{PendingEmbeddings: 2},
			},
		},
		{outcome: StageOutcome{Complete: true}},
	}}

	result, err := Run(t.Context(), ledger, executor, request)
	_ = assertRefreshError(t, err, ErrorProjection, progressErr)
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunFailed {
		t.Fatalf("progress failure result = %+v", result)
	}
	if calls := len(executor.snapshotRuns()); calls != 1 {
		t.Fatalf("executor calls = %d, want callback failure to cancel before next execution", calls)
	}
	updates := ledger.snapshotUpdates()
	failed := 0
	for _, update := range updates {
		if update.State == store.SemanticRefreshRunFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed checkpoints = %d, updates=%+v", failed, updates)
	}
}

func TestRunnerSuccessorRunIDFailureLeavesOldCompleted(t *testing.T) {
	ledger := newRunnerLedger()
	idErr := errors.New("entropy source unavailable")
	generatorCalls := 0
	request := runnerRequest(func() (string, error) {
		generatorCalls++
		if generatorCalls == 1 {
			return firstRunnerRunID, nil
		}
		return "", idErr
	})
	successorWatermark := int64(11)
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:          store.SemanticRefreshReadiness,
			Checkpoint:         "ready",
			Readiness:          "ready",
			Complete:           true,
			SuccessorWatermark: &successorWatermark,
		},
	}}}

	result, err := Run(t.Context(), ledger, executor, request)
	_ = assertRefreshError(t, err, ErrorBackendBroken, idErr)
	if result.Run == nil || result.Run.State != store.SemanticRefreshRunCompleted {
		t.Fatalf("ID failure result = %+v, want old completed row", result)
	}
	if starts := ledger.snapshotStarts(); len(starts) != 1 {
		t.Fatalf("starts = %d, want no successor start after ID failure", len(starts))
	}
}

func TestRunnerRejectsMalformedGeneratedRunIDBeforeLedgerStart(t *testing.T) {
	ledger := newRunnerLedger()
	for _, id := range []string{
		"user-supplied-run-id",
		"00112233445566778899AABBCCDDEEFF",
		"00112233445566778899aabbccddeezz",
		"00112233445566778899aabbccddee",
	} {
		t.Run(id, func(t *testing.T) {
			_, err := Run(
				t.Context(),
				ledger,
				&runnerExecutor{},
				runnerRequest(func() (string, error) { return id, nil }),
			)
			_ = assertRefreshError(t, err, ErrorBackendBroken, nil)
		})
	}
	if starts := ledger.snapshotStarts(); len(starts) != 0 {
		t.Fatalf("malformed generated IDs reached ledger: %+v", starts)
	}
}

func TestRunnerProductionGeneratorUsesOpaqueSixteenByteID(t *testing.T) {
	ledger := newRunnerLedger()
	request := runnerRequest(nil)
	executor := &runnerExecutor{steps: []runnerExecuteStep{{
		outcome: StageOutcome{
			NextStage:  store.SemanticRefreshReadiness,
			Checkpoint: "ready",
			Readiness:  "ready",
			Complete:   true,
		},
	}}}

	if _, err := Run(t.Context(), ledger, executor, request); err != nil {
		t.Fatalf("Run with production generator: %v", err)
	}
	starts := ledger.snapshotStarts()
	if len(starts) != 1 {
		t.Fatalf("starts = %d, want one", len(starts))
	}
	decoded, err := hex.DecodeString(starts[0].RunID)
	if err != nil {
		t.Fatalf("run ID %q is not lowercase hexadecimal: %v", starts[0].RunID, err)
	}
	if len(decoded) != 16 || len(starts[0].RunID) != 32 ||
		starts[0].RunID != strings.ToLower(starts[0].RunID) {
		t.Fatalf("run ID = %q (%d decoded bytes), want 32 lowercase hex characters from 16 bytes", starts[0].RunID, len(decoded))
	}
}

func runnerRequest(generator func() (string, error)) Request {
	return Request{
		ProfileID:           "profile-a",
		PurgeEpoch:          3,
		ProjectionWatermark: 10,
		Capability: semanticindex.Capability{
			State:   semanticindex.CapabilitySupportedReady,
			Backend: semanticindex.BackendUSearch,
			Version: semanticindex.USearchVersion,
		},
		Now:          func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
		NewRunIDFunc: generator,
	}
}

func assertRefreshError(t *testing.T, err error, code string, cause error) *RefreshError {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", code)
	}
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) {
		t.Fatalf("error type = %T (%v), want *RefreshError", err, err)
	}
	if refreshErr.Code != code {
		t.Fatalf("error code = %q, want %q", refreshErr.Code, code)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cause %v", err, cause)
	}
	return refreshErr
}
