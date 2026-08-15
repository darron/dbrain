package linkcapture

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/store"
)

const (
	defaultPollInterval   = time.Second
	defaultCaptureTimeout = 2 * time.Minute
	maxRetryDelay         = 15 * time.Minute
)

type QueueEvent struct {
	CaptureID int64
	Attempt   int
	QueueWait time.Duration
	Duration  time.Duration
	Outcome   string
	ErrorKind string
}

type Options struct {
	PollInterval   time.Duration
	CaptureTimeout time.Duration
	Now            func() time.Time
	Logger         *slog.Logger
	Observe        func(QueueEvent)
	FeedFetcher    feedimport.Fetcher
	Process        func(context.Context, store.LinkCapture) error
}

type Worker struct {
	cfg            config.Config
	store          *store.Store
	pollInterval   time.Duration
	captureTimeout time.Duration
	now            func() time.Time
	logger         *slog.Logger
	observe        func(QueueEvent)
	feedFetcher    feedimport.Fetcher
	process        func(context.Context, store.LinkCapture) error

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func New(cfg config.Config, st *store.Store, opts Options) *Worker {
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	captureTimeout := opts.CaptureTimeout
	if captureTimeout <= 0 {
		captureTimeout = defaultCaptureTimeout
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Worker{
		cfg:            cfg,
		store:          st,
		pollInterval:   pollInterval,
		captureTimeout: captureTimeout,
		now:            now,
		logger:         opts.Logger,
		observe:        opts.Observe,
		feedFetcher:    opts.FeedFetcher,
		process:        opts.Process,
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	w.started = true
	w.cancel = cancel
	w.done = make(chan struct{})
	done := w.done
	w.mu.Unlock()

	go func() {
		defer close(done)
		w.run(workerCtx)
	}()
}

func (w *Worker) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) Close() error {
	return w.Shutdown(context.Background())
}

func (w *Worker) run(ctx context.Context) {
	for {
		worked, err := w.RunOnce(ctx)
		if err != nil && w.logger != nil {
			w.logger.Warn("deferred link capture failed", "error_kind", failureKind(err))
		}
		delay := w.pollInterval
		if worked && err == nil {
			delay = 10 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.store == nil {
		return false, errors.New("link capture worker store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := w.now().UTC()
	captures, err := w.store.ListPendingLinkCaptures(ctx, now, 1)
	if err != nil {
		return false, err
	}
	if len(captures) == 0 {
		return false, nil
	}
	capture := captures[0]
	if err := w.store.MarkLinkCaptureAttempt(ctx, capture.ID, now); err != nil {
		return true, err
	}
	capture.AttemptCount++
	started := time.Now()
	event := QueueEvent{
		CaptureID: capture.ID,
		Attempt:   capture.AttemptCount,
		QueueWait: boundedQueueWait(now, capture.EnqueuedAt),
		Outcome:   "started",
	}

	processCtx, cancel := context.WithTimeout(ctx, w.captureTimeout)
	processErr := w.processCapture(processCtx, capture)
	cancel()
	event.Duration = time.Since(started)
	if processErr != nil {
		event.Outcome = "retry"
		event.ErrorKind = failureKind(processErr)
		if capture.AttemptCount >= store.MaxLinkCaptureAttempts {
			event.Outcome = "dead_letter"
		}
		failedAt := w.now().UTC()
		nextAttempt := failedAt.Add(linkCaptureRetryDelay(capture.AttemptCount))
		markErr := w.store.MarkLinkCaptureFailed(ctx, capture.ID, failedAt, nextAttempt, event.ErrorKind)
		w.observeEvent(event)
		return true, errors.Join(processErr, markErr)
	}
	if err := w.store.MarkLinkCaptureProcessed(ctx, capture.ID, w.now().UTC()); err != nil {
		event.Outcome = "mark_processed_error"
		event.ErrorKind = "mark_processed"
		w.observeEvent(event)
		return true, err
	}
	event.Outcome = "processed"
	w.observeEvent(event)
	return true, nil
}

func (w *Worker) processCapture(ctx context.Context, capture store.LinkCapture) error {
	if w.process != nil {
		return w.process(ctx, capture)
	}
	rawURL := capture.Candidate.CanonicalURL
	if rawURL == "" {
		rawURL = capture.Candidate.NormalizedURL
	}
	if rawURL == "" {
		return &processingError{kind: "invalid_capture", err: errors.New("capture URL is empty")}
	}

	feedURLs := []string{}
	if feedimport.IsLikelyFeedURL(rawURL) {
		feedURLs = append(feedURLs, rawURL)
	} else {
		discovered, ok := feedimport.DiscoverURL(ctx, rawURL, feedimport.AddOptions{Fetch: true, Fetcher: w.feedFetcher})
		if ok && len(discovered) == 1 && discovered[0].Type == "feed" {
			feedURLs = append(feedURLs, discovered[0].URL)
		}
		// The synchronous endpoint returns ambiguous discovery candidates to an
		// interactive caller. A deferred extension save has no chooser, so an
		// ambiguous result falls through and preserves the user's URL as an
		// ordinary source instead of dropping it.
	}
	for _, feedURL := range feedURLs {
		_, _, stats, err := feedimport.Add(ctx, w.cfg, w.store, feedURL, feedimport.AddOptions{
			Enabled: true,
			Fetch:   true,
			Import:  true,
			Fetcher: w.feedFetcher,
		})
		if err != nil {
			return &processingError{kind: "feed_import", err: err}
		}
		if stats.Errors > 0 {
			return &processingError{kind: "feed_import", err: fmt.Errorf("feed import reported %d errors", stats.Errors)}
		}
	}
	if len(feedURLs) == 0 {
		if _, err := w.store.UpsertSource(ctx, capture.Candidate); err != nil {
			return &processingError{kind: "source_upsert", err: err}
		}
	}
	return nil
}

func (w *Worker) observeEvent(event QueueEvent) {
	if w.observe != nil {
		w.observe(event)
	}
	if w.logger != nil {
		w.logger.Info("deferred link capture queue event",
			"capture_id", event.CaptureID,
			"attempt", event.Attempt,
			"queue_wait", event.QueueWait.String(),
			"duration", event.Duration.String(),
			"outcome", event.Outcome,
			"error_kind", event.ErrorKind,
		)
	}
}

type processingError struct {
	kind string
	err  error
}

func (e *processingError) Error() string {
	if e == nil || e.err == nil {
		return "deferred link capture processing failed"
	}
	return e.err.Error()
}

func (e *processingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func failureKind(err error) string {
	var processing *processingError
	if errors.As(err, &processing) && processing != nil && processing.kind != "" {
		return processing.kind
	}
	return "worker_processing"
}

func linkCaptureRetryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Second
	}
	delay := time.Second
	for index := 1; index < attempt; index++ {
		if delay >= maxRetryDelay/2 {
			return maxRetryDelay
		}
		delay *= 2
	}
	return delay
}

func boundedQueueWait(now, enqueued time.Time) time.Duration {
	if enqueued.IsZero() || !now.After(enqueued) {
		return 0
	}
	return now.Sub(enqueued)
}
