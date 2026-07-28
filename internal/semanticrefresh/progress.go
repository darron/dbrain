package semanticrefresh

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/store"
)

const ProgressInterval = 5 * time.Second

type ProgressLedger interface {
	TouchSemanticRefreshRunProgress(context.Context, string, time.Time) error
}

type progressTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type progressEmitterOptions struct {
	now       func() time.Time
	newTicker func(time.Duration) progressTicker
}

type wallProgressTicker struct {
	ticker *time.Ticker
}

func (t wallProgressTicker) Chan() <-chan time.Time { return t.ticker.C }
func (t wallProgressTicker) Stop()                  { t.ticker.Stop() }

// ProgressEmitter serializes durable heartbeats and checkpoint callbacks for
// one running refresh run. Callers must pass only rows returned by a completed
// ledger update to Publish.
type ProgressEmitter struct {
	ledger   ProgressLedger
	callback ProgressCallback
	now      func() time.Time
	ticker   progressTicker

	workCtx context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	serialMu sync.Mutex
	run      store.SemanticRefreshRun
	debt     Debt
	stopped  bool

	errorMu       sync.Mutex
	firstError    error
	errorReturned bool
	stopOnce      sync.Once
}

// StartProgressEmitter durably emits the initial running snapshot before it
// starts the five-second heartbeat loop.
func StartProgressEmitter(
	ctx context.Context,
	ledger ProgressLedger,
	run store.SemanticRefreshRun,
	debt Debt,
	callback ProgressCallback,
) (*ProgressEmitter, error) {
	return startProgressEmitter(ctx, ledger, run, debt, callback, progressEmitterOptions{})
}

func startProgressEmitter(
	ctx context.Context,
	ledger ProgressLedger,
	run store.SemanticRefreshRun,
	debt Debt,
	callback ProgressCallback,
	options progressEmitterOptions,
) (*ProgressEmitter, error) {
	if ctx == nil {
		return nil, fmt.Errorf("semantic refresh progress context is required")
	}
	if ledger == nil {
		return nil, fmt.Errorf("semantic refresh progress ledger is required")
	}
	if run.State != store.SemanticRefreshRunRunning {
		return nil, fmt.Errorf("semantic refresh progress requires a running run")
	}
	if options.now == nil {
		options.now = time.Now
	}
	if options.newTicker == nil {
		options.newTicker = func(interval time.Duration) progressTicker {
			return wallProgressTicker{ticker: time.NewTicker(interval)}
		}
	}

	workCtx, cancel := context.WithCancel(ctx)
	emitter := &ProgressEmitter{
		ledger:   ledger,
		callback: callback,
		now:      options.now,
		workCtx:  workCtx,
		cancel:   cancel,
		done:     make(chan struct{}),
		run:      run,
		debt:     debt,
	}
	if err := emitter.emitLocked(run, debt, true); err != nil {
		close(emitter.done)
		return nil, emitter.takeError()
	}
	emitter.ticker = options.newTicker(ProgressInterval)
	if emitter.ticker == nil {
		emitter.recordError(fmt.Errorf("semantic refresh progress ticker is required"))
		close(emitter.done)
		return nil, emitter.takeError()
	}
	go emitter.heartbeat()
	return emitter, nil
}

// Context returns the derived work context cancelled by a progress ledger or
// callback failure.
func (e *ProgressEmitter) Context() context.Context {
	if e == nil {
		return nil
	}
	return e.workCtx
}

// Publish emits a checkpoint row only after the caller has durably persisted
// it. Heartbeats after this call reuse that exact snapshot.
func (e *ProgressEmitter) Publish(run store.SemanticRefreshRun, debt Debt) error {
	if e == nil {
		return fmt.Errorf("semantic refresh progress emitter is required")
	}
	if run.State != store.SemanticRefreshRunRunning {
		return fmt.Errorf("semantic refresh progress requires a running run")
	}
	if err := e.emitLocked(run, debt, true); err != nil {
		if progressErr := e.takeError(); progressErr != nil {
			return progressErr
		}
		return err
	}
	return nil
}

// Stop prevents new ticks, cancels derived work, joins the heartbeat goroutine,
// and returns an asynchronous ledger or callback error at most once.
func (e *ProgressEmitter) Stop() error {
	if e == nil {
		return nil
	}
	e.stopOnce.Do(func() {
		if e.ticker != nil {
			e.ticker.Stop()
		}
		e.serialMu.Lock()
		e.stopped = true
		e.cancel()
		e.serialMu.Unlock()
		<-e.done
	})
	return e.takeError()
}

func (e *ProgressEmitter) heartbeat() {
	defer close(e.done)
	for {
		select {
		case <-e.workCtx.Done():
			return
		case <-e.ticker.Chan():
			e.serialMu.Lock()
			if e.stopped {
				e.serialMu.Unlock()
				return
			}
			run, debt := e.run, e.debt
			err := e.emitWhileLocked(run, debt)
			e.serialMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (e *ProgressEmitter) emitLocked(
	run store.SemanticRefreshRun,
	debt Debt,
	updateSnapshot bool,
) error {
	e.serialMu.Lock()
	defer e.serialMu.Unlock()
	if e.stopped {
		return context.Canceled
	}
	if updateSnapshot {
		e.run, e.debt = run, debt
	}
	return e.emitWhileLocked(run, debt)
}

func (e *ProgressEmitter) emitWhileLocked(run store.SemanticRefreshRun, debt Debt) error {
	at := e.now().UTC()
	if err := e.ledger.TouchSemanticRefreshRunProgress(e.workCtx, run.RunID, at); err != nil {
		e.recordError(err)
		return err
	}
	if e.callback == nil {
		return nil
	}
	progress := Progress{
		RunID:     run.RunID,
		ProfileID: run.ProfileID,
		Checkpoint: boundedDiagnostic(
			run.Checkpoint,
			errorCheckpointLimit,
		),
		Readiness: boundedDiagnostic(run.ReadinessState, errorReadinessLimit),
		Stage:     run.Stage,
		Counters:  run.Counters,
		Debt:      debt,
		At:        at,
	}
	if err := e.callback(progress); err != nil {
		e.recordError(err)
		return err
	}
	return nil
}

func (e *ProgressEmitter) recordError(err error) {
	if err == nil {
		return
	}
	e.errorMu.Lock()
	if e.firstError == nil {
		e.firstError = err
		e.cancel()
	}
	e.errorMu.Unlock()
}

func (e *ProgressEmitter) takeError() error {
	e.errorMu.Lock()
	defer e.errorMu.Unlock()
	if e.firstError == nil || e.errorReturned {
		return nil
	}
	e.errorReturned = true
	return e.firstError
}
