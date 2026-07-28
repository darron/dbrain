package semanticrefresh

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/store"
)

type fakeProgressTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newFakeProgressTicker() *fakeProgressTicker {
	return &fakeProgressTicker{
		ticks:   make(chan time.Time, 8),
		stopped: make(chan struct{}),
	}
}

func (f *fakeProgressTicker) Chan() <-chan time.Time { return f.ticks }
func (f *fakeProgressTicker) Stop() {
	f.once.Do(func() { close(f.stopped) })
}

type fakeProgressClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeProgressClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeProgressClock) Set(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

type recordingProgressLedger struct {
	mu      sync.Mutex
	touches []progressTouch
	errAt   int
	err     error
}

type progressTouch struct {
	runID string
	at    time.Time
}

func (l *recordingProgressLedger) TouchSemanticRefreshRunProgress(
	_ context.Context,
	runID string,
	at time.Time,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touches = append(l.touches, progressTouch{runID: runID, at: at})
	if l.errAt > 0 && len(l.touches) == l.errAt {
		return l.err
	}
	return nil
}

func (l *recordingProgressLedger) Touches() []progressTouch {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]progressTouch(nil), l.touches...)
}

func TestProgressEmitterWritesImmediateAndFiveSecondHeartbeatsDurableFirst(t *testing.T) {
	t0 := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(ProgressInterval)
	clock := &fakeProgressClock{now: t0}
	ticker := newFakeProgressTicker()
	ledger := &recordingProgressLedger{}
	var interval time.Duration
	events := make(chan Progress, 2)
	run := persistedProgressRun("run-1", "profile-1", "projection:start", 1)

	emitter, err := startProgressEmitter(context.Background(), ledger, run, Debt{DirtyParents: 2}, func(progress Progress) error {
		touches := ledger.Touches()
		if len(touches) == 0 || !touches[len(touches)-1].at.Equal(progress.At) {
			t.Errorf("callback observed progress before durable touch: touches=%v progress=%+v", touches, progress)
		}
		events <- progress
		return nil
	}, progressEmitterOptions{
		now: clock.Now,
		newTicker: func(got time.Duration) progressTicker {
			interval = got
			return ticker
		},
	})
	if err != nil {
		t.Fatalf("startProgressEmitter: %v", err)
	}
	t.Cleanup(func() { _ = emitter.Stop() })

	immediate := receiveProgress(t, events)
	if !immediate.At.Equal(t0) || immediate.RunID != "run-1" || immediate.Counters.ProjectedParents != 1 {
		t.Fatalf("immediate progress = %+v", immediate)
	}
	if interval != 5*time.Second {
		t.Fatalf("ticker interval = %s, want 5s", interval)
	}

	clock.Set(t1)
	ticker.ticks <- t1
	heartbeat := receiveProgress(t, events)
	if !heartbeat.At.Equal(t1) || heartbeat.Checkpoint != "projection:start" {
		t.Fatalf("heartbeat progress = %+v", heartbeat)
	}
	touches := ledger.Touches()
	if len(touches) != 2 || touches[0].runID != "run-1" || !touches[1].at.Equal(t1) {
		t.Fatalf("durable touches = %+v", touches)
	}
}

func TestProgressEmitterSerializesHeartbeatAndCheckpointCallbacks(t *testing.T) {
	t0 := time.Date(2026, 7, 28, 11, 0, 0, 0, time.UTC)
	clock := &fakeProgressClock{now: t0}
	ticker := newFakeProgressTicker()
	ledger := &recordingProgressLedger{}
	heartbeatStarted := make(chan struct{})
	releaseHeartbeat := make(chan struct{})
	checkpointObserved := make(chan Progress, 1)
	var callbacks atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64

	emitter, err := startProgressEmitter(context.Background(), ledger, persistedProgressRun("run-1", "profile-1", "old", 1), Debt{}, func(progress Progress) error {
		call := callbacks.Add(1)
		current := active.Add(1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		defer active.Add(-1)
		if call == 2 {
			close(heartbeatStarted)
			<-releaseHeartbeat
		}
		if progress.Checkpoint == "persisted-checkpoint" {
			checkpointObserved <- progress
		}
		return nil
	}, progressEmitterOptions{
		now:       clock.Now,
		newTicker: func(time.Duration) progressTicker { return ticker },
	})
	if err != nil {
		t.Fatalf("startProgressEmitter: %v", err)
	}
	t.Cleanup(func() { _ = emitter.Stop() })

	ticker.ticks <- t0.Add(ProgressInterval)
	receiveSignal(t, heartbeatStarted, "heartbeat callback")

	persisted := persistedProgressRun("run-1", "profile-1", "persisted-checkpoint", 2)
	published := make(chan error, 1)
	go func() {
		published <- emitter.Publish(persisted, Debt{PendingEmbeddings: 3})
	}()

	select {
	case progress := <-checkpointObserved:
		t.Fatalf("checkpoint callback overlapped blocked heartbeat: %+v", progress)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHeartbeat)
	if err := receiveError(t, published); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	checkpoint := receiveProgress(t, checkpointObserved)
	if checkpoint.Counters.ProjectedParents != 2 || checkpoint.Debt.PendingEmbeddings != 3 {
		t.Fatalf("checkpoint progress = %+v", checkpoint)
	}
	if maxActive.Load() != 1 {
		t.Fatalf("maximum overlapping callbacks = %d, want 1", maxActive.Load())
	}
}

func TestProgressEmitterHeartbeatsUseLastPublishedCheckpoint(t *testing.T) {
	t0 := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	clock := &fakeProgressClock{now: t0}
	ticker := newFakeProgressTicker()
	events := make(chan Progress, 4)
	initial := persistedProgressRun("run-1", "profile-1", "persisted-1", 1)
	emitter, err := startProgressEmitter(context.Background(), &recordingProgressLedger{}, initial, Debt{}, func(progress Progress) error {
		events <- progress
		return nil
	}, progressEmitterOptions{
		now:       clock.Now,
		newTicker: func(time.Duration) progressTicker { return ticker },
	})
	if err != nil {
		t.Fatalf("startProgressEmitter: %v", err)
	}
	t.Cleanup(func() { _ = emitter.Stop() })
	_ = receiveProgress(t, events)

	unpersisted := initial
	unpersisted.Checkpoint = "not-persisted"
	unpersisted.Counters.ProjectedParents = 99
	clock.Set(t0.Add(ProgressInterval))
	ticker.ticks <- clock.Now()
	heartbeat := receiveProgress(t, events)
	if heartbeat.Checkpoint != "persisted-1" || heartbeat.Counters.ProjectedParents != 1 {
		t.Fatalf("heartbeat exposed unpersisted state: %+v (local=%+v)", heartbeat, unpersisted)
	}

	persisted := unpersisted
	persisted.Checkpoint = "persisted-2"
	persisted.Counters.ProjectedParents = 2
	if err := emitter.Publish(persisted, Debt{DirtyParents: 1}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = receiveProgress(t, events)
	clock.Set(t0.Add(2 * ProgressInterval))
	ticker.ticks <- clock.Now()
	heartbeat = receiveProgress(t, events)
	if heartbeat.Checkpoint != "persisted-2" || heartbeat.Counters.ProjectedParents != 2 {
		t.Fatalf("heartbeat did not use latest persisted checkpoint: %+v", heartbeat)
	}
}

func TestProgressEmitterCallbackFailureCancelsWorkAndReturnsErrorOnce(t *testing.T) {
	wantErr := errors.New("progress output closed")
	ticker := newFakeProgressTicker()
	var calls atomic.Int64
	emitter, err := startProgressEmitter(context.Background(), &recordingProgressLedger{}, persistedProgressRun("run-1", "profile-1", "projection", 0), Debt{}, func(Progress) error {
		if calls.Add(1) == 2 {
			return wantErr
		}
		return nil
	}, progressEmitterOptions{
		now:       time.Now,
		newTicker: func(time.Duration) progressTicker { return ticker },
	})
	if err != nil {
		t.Fatalf("startProgressEmitter: %v", err)
	}

	ticker.ticks <- time.Now()
	receiveSignal(t, emitter.Context().Done(), "derived work context cancellation")
	if err := emitter.Stop(); !errors.Is(err, wantErr) {
		t.Fatalf("first Stop error = %v, want %v", err, wantErr)
	}
	if err := emitter.Stop(); err != nil {
		t.Fatalf("second Stop error = %v, want nil", err)
	}
}

func TestProgressEmitterRunningOnlyLedgerFailureCancelsWorkAndReturnsErrorOnce(t *testing.T) {
	ticker := newFakeProgressTicker()
	ledger := &recordingProgressLedger{errAt: 2, err: store.ErrSemanticRefreshRunStale}
	var calls atomic.Int64
	emitter, err := startProgressEmitter(context.Background(), ledger, persistedProgressRun("run-1", "profile-1", "projection", 0), Debt{}, func(Progress) error {
		calls.Add(1)
		return nil
	}, progressEmitterOptions{
		now:       time.Now,
		newTicker: func(time.Duration) progressTicker { return ticker },
	})
	if err != nil {
		t.Fatalf("startProgressEmitter: %v", err)
	}

	ticker.ticks <- time.Now()
	receiveSignal(t, emitter.Context().Done(), "ledger failure cancellation")
	if err := emitter.Stop(); !errors.Is(err, store.ErrSemanticRefreshRunStale) {
		t.Fatalf("first Stop error = %v, want stale-run error", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("callbacks = %d, want only immediate callback after failed durable heartbeat", calls.Load())
	}
	if err := emitter.Stop(); err != nil {
		t.Fatalf("second Stop error = %v, want nil", err)
	}
}

func TestProgressEmitterStopPreventsLaterTicksAndJoinsGoroutine(t *testing.T) {
	ticker := newFakeProgressTicker()
	var calls atomic.Int64
	emitter, err := startProgressEmitter(context.Background(), &recordingProgressLedger{}, persistedProgressRun("run-1", "profile-1", "projection", 0), Debt{}, func(Progress) error {
		calls.Add(1)
		return nil
	}, progressEmitterOptions{
		now:       time.Now,
		newTicker: func(time.Duration) progressTicker { return ticker },
	})
	if err != nil {
		t.Fatalf("startProgressEmitter: %v", err)
	}
	if err := emitter.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	receiveSignal(t, ticker.stopped, "ticker stop")
	receiveSignal(t, emitter.done, "heartbeat goroutine exit")

	ticker.ticks <- time.Now()
	if calls.Load() != 1 {
		t.Fatalf("callbacks after Stop = %d, want immediate callback only", calls.Load())
	}
	if err := emitter.Publish(persistedProgressRun("run-1", "profile-1", "late", 1), Debt{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish after Stop error = %v, want context cancellation", err)
	}
}

func persistedProgressRun(runID, profileID, checkpoint string, projected int64) store.SemanticRefreshRun {
	return store.SemanticRefreshRun{
		RunID:      runID,
		ProfileID:  profileID,
		Stage:      store.SemanticRefreshProjection,
		Checkpoint: checkpoint,
		State:      store.SemanticRefreshRunRunning,
		Counters: store.SemanticRefreshCounters{
			ProjectedParents: projected,
		},
	}
}

func receiveProgress(t *testing.T, events <-chan Progress) Progress {
	t.Helper()
	select {
	case progress := <-events:
		return progress
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for progress event")
		return Progress{}
	}
}

func receiveError(t *testing.T, events <-chan error) error {
	t.Helper()
	select {
	case err := <-events:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for error")
		return nil
	}
}

func receiveSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
