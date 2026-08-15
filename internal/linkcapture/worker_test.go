package linkcapture

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestWorkerProcessesCaptureAndRecordsQueueWait(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	capture, err := st.EnqueueLinkCapture(t.Context(), workerTestCandidate(), time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var observed QueueEvent
	worker := New(config.Config{}, st, Options{
		Now: func() time.Time { return time.Now().UTC() },
		Process: func(_ context.Context, got store.LinkCapture) error {
			if got.ID != capture.Capture.ID {
				t.Fatalf("processed capture ID=%d, want %d", got.ID, capture.Capture.ID)
			}
			return nil
		},
		Observe: func(event QueueEvent) { observed = event },
	})

	worked, err := worker.RunOnce(t.Context())
	if err != nil || !worked {
		t.Fatalf("RunOnce worked=%v err=%v", worked, err)
	}
	if observed.CaptureID != capture.Capture.ID || observed.QueueWait < time.Second || observed.Outcome != "processed" {
		t.Fatalf("queue event = %+v", observed)
	}
	pending, err := st.ListPendingLinkCaptures(t.Context(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("processed capture remained pending: %+v", pending)
	}
}

func TestWorkerFailureLeavesCaptureRetryableWithoutURLInError(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	privateURL := "https://private.example/should-not-be-logged"
	enqueued, err := st.EnqueueLinkCapture(t.Context(), workerTestCandidateWithURL(privateURL), time.Now().UTC())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	worker := New(config.Config{}, st, Options{
		Process: func(_ context.Context, _ store.LinkCapture) error { return errors.New(privateURL) },
	})
	worked, err := worker.RunOnce(t.Context())
	if !worked || err == nil {
		t.Fatalf("RunOnce worked=%v err=%v, want retryable error", worked, err)
	}
	captures, err := st.ListPendingLinkCaptures(t.Context(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(captures) != 0 {
		t.Fatalf("failed capture should be backed off, got %+v", captures)
	}
	capture, err := st.GetLinkCapture(t.Context(), enqueued.Capture.ID)
	if err != nil {
		t.Fatalf("get failed capture: %v", err)
	}
	if !capture.ProcessedAt.IsZero() || capture.LastError != "worker_processing" {
		t.Fatalf("failed capture state = %+v", capture)
	}
	if strings.Contains(capture.LastError, privateURL) {
		t.Fatalf("failure stored raw URL: %q", capture.LastError)
	}
}

func TestWorkerBackoffStartsAfterProcessingFinishes(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	base := time.Date(2026, time.August, 15, 4, 0, 0, 0, time.UTC)
	enqueued, err := st.EnqueueLinkCapture(t.Context(), workerTestCandidate(), base)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	clockCalls := 0
	worker := New(config.Config{}, st, Options{
		Now: func() time.Time {
			clockCalls++
			if clockCalls == 1 {
				return base
			}
			return base.Add(10 * time.Minute)
		},
		Process: func(context.Context, store.LinkCapture) error { return errors.New("temporary failure") },
	})
	if worked, err := worker.RunOnce(t.Context()); !worked || err == nil {
		t.Fatalf("RunOnce worked=%v err=%v, want failure", worked, err)
	}
	capture, err := st.GetLinkCapture(t.Context(), enqueued.Capture.ID)
	if err != nil {
		t.Fatalf("get failed capture: %v", err)
	}
	if !capture.NextAttemptAt.After(base.Add(10 * time.Minute)) {
		t.Fatalf("next attempt did not start after processing finished: %s", capture.NextAttemptAt)
	}
}

func TestWorkerExhaustedCaptureFallsBackToOrdinarySource(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	base := time.Date(2026, time.August, 15, 4, 0, 0, 0, time.UTC)
	candidate := workerTestCandidateWithURL("https://example.com/feed.xml")
	enqueued, err := st.EnqueueLinkCapture(t.Context(), candidate, base)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	clockCalls := 0
	fetcher := &failingWorkerFeedFetcher{}
	var observed QueueEvent
	worker := New(config.Config{}, st, Options{
		Now: func() time.Time {
			current := base.Add(time.Duration(clockCalls) * time.Hour)
			clockCalls++
			return current
		},
		FeedFetcher: fetcher,
		Observe:     func(event QueueEvent) { observed = event },
	})

	for attempt := 1; attempt <= store.MaxLinkCaptureAttempts; attempt++ {
		worked, runErr := worker.RunOnce(t.Context())
		if !worked {
			t.Fatalf("attempt %d did no work", attempt)
		}
		if attempt < store.MaxLinkCaptureAttempts && runErr == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
		if attempt == store.MaxLinkCaptureAttempts && runErr != nil {
			t.Fatalf("terminal fallback failed: %v", runErr)
		}
	}

	if observed.Outcome != "processed_fallback" || observed.ErrorKind != "feed_import" {
		t.Fatalf("terminal fallback event = %+v", observed)
	}
	if _, err := st.GetSource(t.Context(), candidate.SourceKey); err != nil {
		t.Fatalf("fallback source was not created: %v", err)
	}
	sources, err := st.ListSourcesForEnrichment(t.Context(), 10, false, true, "dbrain-v1", "summarize", "")
	if err != nil {
		t.Fatalf("list fallback source backlog: %v", err)
	}
	if len(sources) != 1 || sources[0].SourceKey != candidate.SourceKey || sources[0].ExtractStatus != "" {
		t.Fatalf("fallback source backlog = %+v", sources)
	}
	if fetcher.calls != store.MaxLinkCaptureAttempts {
		t.Fatalf("failing feed fetch calls = %d, want %d", fetcher.calls, store.MaxLinkCaptureAttempts)
	}
	capture, err := st.GetLinkCapture(t.Context(), enqueued.Capture.ID)
	if err != nil {
		t.Fatalf("get fallback capture: %v", err)
	}
	if capture.ProcessedAt.IsZero() || !capture.DeadLetteredAt.IsZero() {
		t.Fatalf("fallback capture state = %+v", capture)
	}
}

func TestWorkerFallbackFailureParksAsSourceUpsert(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	base := time.Date(2026, time.August, 15, 4, 0, 0, 0, time.UTC)
	conflicting := workerTestCandidateWithURL("https://example.com/existing")
	conflicting.SourceKey = "src:fallback-conflict"
	if _, err := st.UpsertSource(t.Context(), conflicting); err != nil {
		t.Fatalf("seed conflicting source: %v", err)
	}
	candidate := workerTestCandidateWithURL("https://example.com/exhausted-fallback")
	candidate.SourceKey = conflicting.SourceKey
	enqueued, err := st.EnqueueLinkCapture(t.Context(), candidate, base)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	clockCalls := 0
	var observed QueueEvent
	worker := New(config.Config{}, st, Options{
		Now: func() time.Time {
			current := base.Add(time.Duration(clockCalls) * time.Hour)
			clockCalls++
			return current
		},
		Process: func(context.Context, store.LinkCapture) error {
			return &processingError{kind: "feed_import", err: errors.New("feed import failed")}
		},
		Observe: func(event QueueEvent) { observed = event },
	})

	for attempt := 1; attempt <= store.MaxLinkCaptureAttempts; attempt++ {
		worked, runErr := worker.RunOnce(t.Context())
		if !worked {
			t.Fatalf("attempt %d did no work", attempt)
		}
		if runErr == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
	}

	if observed.Outcome != "dead_letter" || observed.ErrorKind != "source_upsert" {
		t.Fatalf("fallback failure event = %+v", observed)
	}
	capture, err := st.GetLinkCapture(t.Context(), enqueued.Capture.ID)
	if err != nil {
		t.Fatalf("get parked capture: %v", err)
	}
	if !capture.ProcessedAt.IsZero() || capture.DeadLetteredAt.IsZero() || capture.LastError != "source_upsert" {
		t.Fatalf("fallback failure capture state = %+v", capture)
	}
}

func TestWorkerDefaultProcessDiscoversThenQueuesOrdinarySource(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	candidate, ok := linkextract.NormalizeCandidate("https://example.com/article")
	if !ok {
		t.Fatal("test page URL did not normalize")
	}
	if _, err := st.EnqueueLinkCapture(t.Context(), candidate, time.Now().UTC()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	fetcher := &workerFeedFetcher{body: []byte("<html><head><title>Article</title></head><body>plain page</body></html>")}
	worker := New(config.Config{}, st, Options{CaptureTimeout: time.Minute, FeedFetcher: fetcher})
	worked, err := worker.RunOnce(t.Context())
	if err != nil || !worked {
		t.Fatalf("RunOnce worked=%v err=%v", worked, err)
	}
	if _, err := st.GetSource(t.Context(), candidate.SourceKey); err != nil {
		t.Fatalf("deferred source was not created: %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("feed discovery calls = %d, want 1", fetcher.calls)
	}
	if _, err := st.EnqueueLinkCapture(t.Context(), candidate, time.Now().UTC()); err != nil {
		t.Fatalf("re-enqueue processed source: %v", err)
	}
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("idempotent rerun worked=%v err=%v", worked, err)
	}
}

func TestWorkerDefaultProcessImportsDiscoveredFeed(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	candidate, ok := linkextract.NormalizeCandidate("https://example.com/article")
	if !ok {
		t.Fatal("test feed URL did not normalize")
	}
	if _, err := st.EnqueueLinkCapture(t.Context(), candidate, time.Now().UTC()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	fetcher := &workerFeedFetcher{body: []byte(`<rss version="2.0"><channel><title>Deferred feed</title><link>https://example.com/</link><item><guid>deferred-1</guid><title>Deferred item</title><link>https://example.com/deferred-1</link></item></channel></rss>`)}
	cfg := config.Config{VaultDir: t.TempDir()}
	worker := New(cfg, st, Options{CaptureTimeout: time.Minute, FeedFetcher: fetcher})
	worked, err := worker.RunOnce(t.Context())
	if err != nil || !worked {
		t.Fatalf("RunOnce worked=%v err=%v", worked, err)
	}
	_, feedKey, err := feedimport.NormalizeFeedURL(candidate.CanonicalURL)
	if err != nil {
		t.Fatalf("normalize imported feed: %v", err)
	}
	if _, err := st.GetFeed(t.Context(), feedKey); err != nil {
		feeds, listErr := st.ListFeeds(t.Context(), true)
		t.Fatalf("deferred feed was not imported: %v (feed key=%s feeds=%+v list err=%v)", err, feedKey, feeds, listErr)
	}
	if fetcher.calls < 2 {
		t.Fatalf("feed fetch calls = %d, want discovery and import fetches", fetcher.calls)
	}
}

func TestWorkerStartShutdownDrainsAndStops(t *testing.T) {
	t.Parallel()

	st := openLinkCaptureWorkerStore(t)
	if _, err := st.EnqueueLinkCapture(t.Context(), workerTestCandidate(), time.Now().UTC()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	processed := make(chan struct{})
	worker := New(config.Config{}, st, Options{
		PollInterval: time.Millisecond,
		Process: func(context.Context, store.LinkCapture) error {
			close(processed)
			return nil
		},
	})
	worker.Start(t.Context())
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("worker did not process capture after Start")
	}
	if err := worker.Shutdown(t.Context()); err != nil {
		t.Fatalf("worker shutdown: %v", err)
	}
}

func openLinkCaptureWorkerStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("open worker store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func workerTestCandidate() model.SourceCandidate {
	return workerTestCandidateWithURL("https://example.com/worker")
}

func workerTestCandidateWithURL(raw string) model.SourceCandidate {
	return model.SourceCandidate{
		OriginalURL: raw, CanonicalURL: raw, NormalizedURL: raw,
		SourceType: "web", Domain: "example.com", SourceKey: "src:worker-test",
		NotePath: "sources/web/worker-test.md",
	}
}

type workerFeedFetcher struct {
	body  []byte
	calls int
}

type failingWorkerFeedFetcher struct {
	calls int
}

func (f *failingWorkerFeedFetcher) Fetch(_ context.Context, feed store.Feed, _ feedimport.Options) (feedimport.FetchResult, error) {
	f.calls++
	return feedimport.FetchResult{RequestURL: feed.URL, FinalURL: feed.URL}, errors.New("feed fetch failed")
}

func (f *workerFeedFetcher) Fetch(_ context.Context, feed store.Feed, _ feedimport.Options) (feedimport.FetchResult, error) {
	f.calls++
	return feedimport.FetchResult{
		RequestURL:        feed.URL,
		FinalURL:          feed.URL,
		HTTPStatus:        200,
		DecodedBody:       f.body,
		WireResponseBytes: f.body,
		DecodedSizeBytes:  int64(len(f.body)),
		DecodedBodyHash:   "worker-test-hash",
	}, nil
}
