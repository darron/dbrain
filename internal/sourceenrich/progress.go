package sourceenrich

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type sourceProgressEntry struct {
	SourceKey string
	URL       string
	StartedAt time.Time
}

type sourceProgressSnapshot struct {
	Total           int
	Processed       int
	Remaining       int
	Errors          int
	Active          int
	OldestElapsed   time.Duration
	OldestSourceKey string
	OldestURL       string
}

type sourceProgressTracker struct {
	mu        sync.Mutex
	total     int
	processed int
	errors    int
	active    map[string]sourceProgressEntry
}

func newSourceProgressTracker(total int) *sourceProgressTracker {
	return &sourceProgressTracker{
		total:  total,
		active: make(map[string]sourceProgressEntry, total),
	}
}

func (t *sourceProgressTracker) start(source model.SourceDocument, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.active[sourceProgressKey(source)] = sourceProgressEntry{
		SourceKey: source.SourceKey,
		URL:       source.CanonicalURL,
		StartedAt: now,
	}
}

func (t *sourceProgressTracker) finish(source model.SourceDocument, result sourceProcessResult) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.active, sourceProgressKey(source))
	t.processed++
	t.errors += result.Stats.Errors
}

func (t *sourceProgressTracker) snapshot(now time.Time) sourceProgressSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	snapshot := sourceProgressSnapshot{
		Total:     t.total,
		Processed: t.processed,
		Remaining: t.total - t.processed,
		Errors:    t.errors,
		Active:    len(t.active),
	}
	for _, entry := range t.active {
		elapsed := now.Sub(entry.StartedAt)
		if snapshot.OldestSourceKey == "" || elapsed > snapshot.OldestElapsed {
			snapshot.OldestElapsed = elapsed
			snapshot.OldestSourceKey = entry.SourceKey
			snapshot.OldestURL = entry.URL
		}
	}
	return snapshot
}

func sourceProgressKey(source model.SourceDocument) string {
	if source.SourceKey != "" {
		return source.SourceKey
	}
	if source.CanonicalURL != "" {
		return source.CanonicalURL
	}
	return fmt.Sprintf("source:%d", source.ID)
}

func startSourceProgressLogger(ctx context.Context, logger *slog.Logger, interval time.Duration, tracker *sourceProgressTracker) func() {
	if logger == nil || interval <= 0 || tracker == nil {
		return func() {}
	}

	progressCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-progressCtx.Done():
				return
			case tickTime := <-ticker.C:
				snapshot := tracker.snapshot(tickTime)
				if snapshot.Active == 0 {
					continue
				}
				debugLog(logger, "source enrichment progress",
					"processed", snapshot.Processed,
					"total", snapshot.Total,
					"remaining", snapshot.Remaining,
					"active", snapshot.Active,
					"errors", snapshot.Errors,
					"oldest_elapsed", snapshot.OldestElapsed,
					"oldest_source_key", snapshot.OldestSourceKey,
					"oldest_url", snapshot.OldestURL,
				)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
