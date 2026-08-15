package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestLinkCaptureQueueDuplicateReopensProcessedRows(t *testing.T) {
	t.Parallel()

	st := openStoreFromEmptyDatabase(t, filepath.Join(t.TempDir(), "brain.db"))
	candidate := linkCaptureTestCandidate()
	now := time.Date(2026, time.August, 15, 4, 0, 0, 0, time.UTC)

	first, err := st.EnqueueLinkCapture(t.Context(), candidate, now)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if !first.Created || first.Reopened || first.Capture.ID == 0 {
		t.Fatalf("first enqueue result = %+v", first)
	}

	if err := st.MarkLinkCaptureProcessed(t.Context(), first.Capture.ID, now.Add(time.Second)); err != nil {
		t.Fatalf("mark processed: %v", err)
	}
	reopened, err := st.EnqueueLinkCapture(t.Context(), candidate, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("reopen enqueue: %v", err)
	}
	if reopened.Created || !reopened.Reopened || reopened.Capture.ID != first.Capture.ID {
		t.Fatalf("reopen enqueue result = %+v", reopened)
	}

	pending, err := st.ListPendingLinkCaptures(t.Context(), now.Add(2*time.Second), 10)
	if err != nil {
		t.Fatalf("list reopened captures: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != first.Capture.ID || !pending[0].ProcessedAt.IsZero() {
		t.Fatalf("reopened pending captures = %+v", pending)
	}
	if err := st.MarkLinkCaptureAttempt(t.Context(), first.Capture.ID, now.Add(3*time.Second)); err != nil {
		t.Fatalf("mark pending attempt: %v", err)
	}
	if err := st.MarkLinkCaptureFailed(t.Context(), first.Capture.ID, now.Add(3*time.Second), now.Add(time.Hour), "temporary"); err != nil {
		t.Fatalf("mark pending failure: %v", err)
	}

	duplicate, err := st.EnqueueLinkCapture(t.Context(), candidate, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("pending duplicate enqueue: %v", err)
	}
	if duplicate.Created || duplicate.Reopened || duplicate.Capture.ID != first.Capture.ID {
		t.Fatalf("pending duplicate result = %+v", duplicate)
	}
	refreshed, err := st.GetLinkCapture(t.Context(), first.Capture.ID)
	if err != nil {
		t.Fatalf("get refreshed pending capture: %v", err)
	}
	if !refreshed.NextAttemptAt.IsZero() || refreshed.LastError != "" || refreshed.AttemptCount != 1 {
		t.Fatalf("pending duplicate retained stale retry state: %+v", refreshed)
	}
}

func TestLinkCaptureQueueDoesNotWaitForSemanticLease(t *testing.T) {
	// Keep this lock-file/SQLite boundary test out of the package-wide parallel
	// load: it verifies lease independence, not concurrent test scheduling.
	path := filepath.Join(t.TempDir(), "brain.db")
	st, err := OpenWithSemanticCacheOptions(path, t.TempDir(), OpenOptions{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exclusive, err := st.semanticLockScope.AcquireMaintenanceExclusive(t.Context(), "test-link-capture")
	if err != nil {
		t.Fatalf("acquire exclusive lease: %v", err)
	}
	t.Cleanup(func() { _ = exclusive.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := st.EnqueueLinkCapture(ctx, linkCaptureTestCandidate(), started.UTC())
	if err != nil {
		t.Fatalf("enqueue under exclusive semantic lease: %v", err)
	}
	if result.Capture.ID == 0 || time.Since(started) > 200*time.Millisecond {
		t.Fatalf("enqueue under lease was not independent: result=%+v elapsed=%s", result, time.Since(started))
	}
}

func TestLinkCaptureQueueAdmissionUsesConfiguredBusyTimeout(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreFromEmptyDatabase(t, path)
	blocker, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	if _, err := blocker.ExecContext(t.Context(), `PRAGMA busy_timeout = 60000; BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin blocker transaction: %v", err)
	}
	t.Cleanup(func() { _, _ = blocker.ExecContext(context.Background(), `ROLLBACK`) })

	started := time.Now()
	// The modernc driver reports a Go context error only after SQLite's busy
	// handler finishes waiting. The admission connection's per-connection
	// busy_timeout is therefore the real request bound; this test must assert
	// that mechanism rather than teach callers that a short context cancels the
	// SQLite lock wait.
	_, err = st.EnqueueLinkCapture(t.Context(), linkCaptureTestCandidate(), started.UTC())
	if err == nil {
		t.Fatal("expected bounded intake write to fail while SQLite writer is held")
	}
	elapsed := time.Since(started)
	if elapsed < LinkCaptureAdmissionBusyTimeout-500*time.Millisecond || elapsed > LinkCaptureAdmissionBusyTimeout+time.Second {
		t.Fatalf("intake write did not honor configured busy timeout: %s (%v)", elapsed, err)
	}
}

func TestLinkCaptureQueueAdmissionUsesWriteStatementBusyWait(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreFromEmptyDatabase(t, path)
	blocker, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	if _, err := blocker.ExecContext(t.Context(), `PRAGMA busy_timeout = 60000; BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin blocker transaction: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = blocker.ExecContext(context.Background(), `ROLLBACK`)
		close(released)
	}()
	t.Cleanup(func() {
		_, _ = blocker.ExecContext(context.Background(), `ROLLBACK`)
	})

	started := time.Now()
	result, err := st.EnqueueLinkCapture(t.Context(), linkCaptureTestCandidate(), started.UTC())
	if err != nil {
		t.Fatalf("enqueue behind competing writer: %v", err)
	}
	if result.Capture.ID == 0 {
		t.Fatal("enqueue returned no capture ID")
	}
	if elapsed := time.Since(started); elapsed < 75*time.Millisecond || elapsed > time.Second {
		t.Fatalf("enqueue did not wait for then pass the competing writer: %s", elapsed)
	}
	<-released
}

func TestLinkCaptureQueueDeadLettersAfterBoundedAttempts(t *testing.T) {
	t.Parallel()

	st := openStoreFromEmptyDatabase(t, filepath.Join(t.TempDir(), "brain.db"))
	enqueued, err := st.EnqueueLinkCapture(t.Context(), linkCaptureTestCandidate(), time.Now().UTC())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	for attempt := 1; attempt <= MaxLinkCaptureAttempts; attempt++ {
		now := time.Now().UTC().Add(time.Duration(attempt) * time.Second)
		if err := st.MarkLinkCaptureAttempt(t.Context(), enqueued.Capture.ID, now); err != nil {
			t.Fatalf("mark attempt %d: %v", attempt, err)
		}
		if err := st.MarkLinkCaptureFailed(t.Context(), enqueued.Capture.ID, now, now.Add(time.Minute), "worker_processing"); err != nil {
			t.Fatalf("mark failure %d: %v", attempt, err)
		}
	}
	capture, err := st.GetLinkCapture(t.Context(), enqueued.Capture.ID)
	if err != nil {
		t.Fatalf("get dead-lettered capture: %v", err)
	}
	if capture.DeadLetteredAt.IsZero() || !capture.NextAttemptAt.IsZero() || capture.AttemptCount != MaxLinkCaptureAttempts {
		t.Fatalf("dead-lettered capture = %+v", capture)
	}
	pending, err := st.ListPendingLinkCaptures(t.Context(), time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("list pending captures: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("dead-lettered capture remained pending: %+v", pending)
	}
	dead, err := st.ListDeadLetteredLinkCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead-lettered captures: %v", err)
	}
	if len(dead) != 1 || dead[0].ID != enqueued.Capture.ID || dead[0].AttemptCount != MaxLinkCaptureAttempts || dead[0].LastError != "worker_processing" || dead[0].DeadLetteredAt.IsZero() {
		t.Fatalf("dead-lettered captures = %+v", dead)
	}
	if reopened, err := st.EnqueueLinkCapture(t.Context(), linkCaptureTestCandidate(), time.Now().UTC()); err != nil {
		t.Fatalf("reopen dead-lettered capture: %v", err)
	} else if !reopened.Reopened || !reopened.Capture.DeadLetteredAt.IsZero() {
		t.Fatalf("reopened dead-lettered capture = %+v", reopened)
	}
}

func linkCaptureTestCandidate() model.SourceCandidate {
	return model.SourceCandidate{
		OriginalURL:   "https://example.com/article?utm_source=test",
		CanonicalURL:  "https://example.com/article",
		NormalizedURL: "https://example.com/article",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:link-capture-test",
		NotePath:      "sources/web/example-com-link-capture-test.md",
	}
}
