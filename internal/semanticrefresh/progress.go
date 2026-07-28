package semanticrefresh

import (
	"context"
	"errors"
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
	testHooks progressEmitterTestHooks
}

type progressEmitterTestHooks struct {
	afterTickQueueCheck func()
	afterPublishEnqueue func()
}

type wallProgressTicker struct {
	ticker *time.Ticker
}

func (t wallProgressTicker) Chan() <-chan time.Time { return t.ticker.C }
func (t wallProgressTicker) Stop()                  { t.ticker.Stop() }

type progressDispatch struct {
	run    store.SemanticRefreshRun
	debt   Debt
	result chan error
}

// ProgressEmitter serializes durable heartbeats and checkpoint callbacks for
// one running refresh run. Callers must pass only rows returned by a completed
// ledger update to Publish.
type ProgressEmitter struct {
	ledger    ProgressLedger
	callback  ProgressCallback
	now       func() time.Time
	ticker    progressTicker
	testHooks progressEmitterTestHooks

	workCtx context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	wake    chan struct{}

	stateMu        sync.Mutex
	run            store.SemanticRefreshRun
	debt           Debt
	queue          []progressDispatch
	callbackActive bool
	stopped        bool
	tickerStopOnce sync.Once

	errorMu       sync.Mutex
	firstError    error
	errorReturned bool
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
		ledger:    ledger,
		callback:  callback,
		now:       options.now,
		testHooks: options.testHooks,
		workCtx:   workCtx,
		cancel:    cancel,
		done:      make(chan struct{}),
		wake:      make(chan struct{}, 1),
		run:       run,
		debt:      debt,
	}
	if err := emitter.dispatch(run, debt); err != nil {
		emitter.recordError(err)
		close(emitter.done)
		return nil, emitter.takeError()
	}
	emitter.ticker = options.newTicker(ProgressInterval)
	if emitter.ticker == nil {
		emitter.recordError(fmt.Errorf("semantic refresh progress ticker is required"))
		close(emitter.done)
		return nil, emitter.takeError()
	}
	go emitter.runDispatcher()
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

// Publish queues a checkpoint row only after the caller has durably persisted
// it. Heartbeats after this call reuse that exact snapshot. A Publish invoked
// while a callback is active is accepted asynchronously so callback-to-Publish
// reentrancy can unwind before the queued callback is dispatched.
func (e *ProgressEmitter) Publish(run store.SemanticRefreshRun, debt Debt) error {
	if e == nil {
		return fmt.Errorf("semantic refresh progress emitter is required")
	}
	if run.State != store.SemanticRefreshRunRunning {
		return fmt.Errorf("semantic refresh progress requires a running run")
	}

	request := progressDispatch{
		run:    run,
		debt:   debt,
		result: make(chan error, 1),
	}
	e.stateMu.Lock()
	if e.stopped {
		e.stateMu.Unlock()
		return context.Canceled
	}
	reentrant := e.callbackActive
	e.run, e.debt = run, debt
	e.queue = append(e.queue, request)
	e.stateMu.Unlock()
	if e.testHooks.afterPublishEnqueue != nil {
		e.testHooks.afterPublishEnqueue()
	}
	e.signal()
	if reentrant {
		return nil
	}

	err := <-request.result
	if err == nil {
		return nil
	}
	if progressErr := e.takeError(); progressErr != nil {
		return progressErr
	}
	return err
}

// Stop prevents new ticks and checkpoint events and cancels derived work.
// Outside an active callback it joins the dispatcher before returning. A Stop
// observed while a callback is active requests stop and returns without joining
// so callback-to-Stop reentrancy cannot self-join; a later Stop is idempotent
// and joins after that callback unwinds.
func (e *ProgressEmitter) Stop() error {
	if e == nil {
		return nil
	}
	e.stateMu.Lock()
	callbackActive := e.callbackActive
	if !e.stopped {
		e.stopped = true
		e.cancel()
	}
	e.stateMu.Unlock()
	e.stopTicker()
	e.signal()
	if !callbackActive {
		<-e.done
	}
	return e.takeError()
}

func (e *ProgressEmitter) runDispatcher() {
	defer close(e.done)
	defer e.stopTicker()
	defer e.failQueued(context.Canceled)
	for {
		if request, ok := e.nextQueued(); ok {
			if err := e.dispatch(request.run, request.debt); err != nil {
				e.handleDispatchError(err)
				request.result <- err
				return
			}
			request.result <- nil
			continue
		}
		if e.isStopped() {
			return
		}

		select {
		case <-e.workCtx.Done():
			e.requestStop()
		case <-e.wake:
		case <-e.ticker.Chan():
			request, queued, stopped := e.nextTickDispatch()
			if stopped {
				return
			}
			if err := e.dispatch(request.run, request.debt); err != nil {
				e.handleDispatchError(err)
				if queued {
					request.result <- err
				}
				return
			}
			if queued {
				request.result <- nil
			}
		}
	}
}

func (e *ProgressEmitter) dispatch(run store.SemanticRefreshRun, debt Debt) error {
	at := e.now().UTC()
	if err := e.ledger.TouchSemanticRefreshRunProgress(e.workCtx, run.RunID, at); err != nil {
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
	e.stateMu.Lock()
	e.callbackActive = true
	e.stateMu.Unlock()
	err := e.callback(progress)
	e.stateMu.Lock()
	e.callbackActive = false
	e.stateMu.Unlock()
	e.signal()
	return err
}

func (e *ProgressEmitter) nextQueued() (progressDispatch, bool) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.stopped || len(e.queue) == 0 {
		return progressDispatch{}, false
	}
	request := e.queue[0]
	e.queue = e.queue[1:]
	return request, true
}

func (e *ProgressEmitter) nextTickDispatch() (progressDispatch, bool, bool) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.stopped {
		return progressDispatch{}, false, true
	}
	if len(e.queue) > 0 {
		request := e.queue[0]
		e.queue = e.queue[1:]
		return request, true, false
	}
	if e.testHooks.afterTickQueueCheck != nil {
		e.testHooks.afterTickQueueCheck()
	}
	return progressDispatch{run: e.run, debt: e.debt}, false, false
}

func (e *ProgressEmitter) isStopped() bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	return e.stopped
}

func (e *ProgressEmitter) requestStop() {
	e.stateMu.Lock()
	e.stopped = true
	e.stateMu.Unlock()
	e.cancel()
	e.stopTicker()
	e.signal()
}

func (e *ProgressEmitter) failQueued(err error) {
	e.stateMu.Lock()
	queue := e.queue
	e.queue = nil
	e.stateMu.Unlock()
	for _, request := range queue {
		request.result <- err
	}
}

func (e *ProgressEmitter) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *ProgressEmitter) stopTicker() {
	e.tickerStopOnce.Do(func() {
		if e.ticker != nil {
			e.ticker.Stop()
		}
	})
}

func (e *ProgressEmitter) handleDispatchError(err error) {
	if errors.Is(err, context.Canceled) && e.workCtx.Err() != nil {
		e.requestStop()
		return
	}
	e.recordError(err)
}

func (e *ProgressEmitter) recordError(err error) {
	if err == nil {
		return
	}
	e.errorMu.Lock()
	if e.firstError == nil {
		e.firstError = err
	}
	e.errorMu.Unlock()
	e.requestStop()
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
