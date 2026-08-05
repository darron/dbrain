package app

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

const (
	semanticProgressChangeInterval    = 5 * time.Second
	semanticProgressHeartbeatInterval = 30 * time.Second
)

type semanticProgressSnapshot struct {
	stage     store.SemanticRefreshStage
	counters  store.SemanticRefreshCounters
	debt      semanticrefresh.Debt
	readiness string
}

// semanticProgressReporter turns the refresh runner's durable snapshots into
// bounded, stage-oriented sync output. The refresh emitter serializes callbacks,
// but the reporter also protects its own state so it remains safe as a callback
// outside that implementation.
type semanticProgressReporter struct {
	mu  sync.Mutex
	out io.Writer
	now func() time.Time

	started      bool
	finished     bool
	lastObserved semanticProgressSnapshot
	lastEmitted  semanticProgressSnapshot
	lastOutputAt time.Time
}

func newSemanticProgressReporter(out io.Writer, now func() time.Time) *semanticProgressReporter {
	if out == nil {
		out = io.Discard
	}
	if now == nil {
		now = time.Now
	}
	return &semanticProgressReporter{out: out, now: now}
}

// Callback returns the reporter in the exact form accepted by a semantic
// refresh request.
func (r *semanticProgressReporter) Callback() semanticrefresh.ProgressCallback {
	return r.Report
}

func (r *semanticProgressReporter) Report(progress semanticrefresh.Progress) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.finished {
		return nil
	}
	now := progress.At
	if now.IsZero() {
		now = r.now()
	}
	snapshot := newSemanticProgressSnapshot(progress)

	if !r.started {
		if err := r.writeStageStart(snapshot); err != nil {
			return err
		}
		if err := r.writeProgress(snapshot); err != nil {
			return err
		}
		r.started = true
		r.lastObserved = snapshot
		r.lastEmitted = snapshot
		r.lastOutputAt = now
		return nil
	}

	if snapshot.stage != r.lastObserved.stage {
		if err := r.writeComplete(r.lastObserved); err != nil {
			return err
		}
		if err := r.writeStageStart(snapshot); err != nil {
			return err
		}
		if err := r.writeProgress(snapshot); err != nil {
			return err
		}
		r.lastObserved = snapshot
		r.lastEmitted = snapshot
		r.lastOutputAt = now
		return nil
	}

	r.lastObserved = snapshot
	elapsed := now.Sub(r.lastOutputAt)
	changed := snapshot != r.lastEmitted
	if changed {
		if elapsed < semanticProgressChangeInterval {
			return nil
		}
	} else if elapsed < semanticProgressHeartbeatInterval {
		return nil
	}
	if err := r.writeProgress(snapshot); err != nil {
		return err
	}
	r.lastEmitted = snapshot
	r.lastOutputAt = now
	return nil
}

// Finish closes the active semantic stage. It is safe to call when no progress
// arrived and safe to call more than once.
func (r *semanticProgressReporter) Finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.finished || !r.started {
		return nil
	}
	if err := r.writeComplete(r.lastObserved); err != nil {
		return err
	}
	r.finished = true
	return nil
}

func newSemanticProgressSnapshot(progress semanticrefresh.Progress) semanticProgressSnapshot {
	return semanticProgressSnapshot{
		stage:     knownSemanticProgressStage(progress.Stage),
		counters:  progress.Counters,
		debt:      progress.Debt,
		readiness: safeSemanticRefreshOutputField(progress.Readiness, 64),
	}
}

func knownSemanticProgressStage(stage store.SemanticRefreshStage) store.SemanticRefreshStage {
	switch stage {
	case store.SemanticRefreshProjection,
		store.SemanticRefreshEmbedding,
		store.SemanticRefreshFlush,
		store.SemanticRefreshCompaction,
		store.SemanticRefreshVerify,
		store.SemanticRefreshReadiness:
		return stage
	default:
		return ""
	}
}

func semanticProgressStageName(stage store.SemanticRefreshStage) string {
	if stage == "" {
		return "unknown"
	}
	return string(stage)
}

func (r *semanticProgressReporter) writeStageStart(snapshot semanticProgressSnapshot) error {
	_, err := fmt.Fprintf(r.out, "==> semantic %s\n", semanticProgressStageName(snapshot.stage))
	return err
}

func (r *semanticProgressReporter) writeProgress(snapshot semanticProgressSnapshot) error {
	_, err := fmt.Fprintf(
		r.out,
		"Semantic %s progress: %s\n",
		semanticProgressStageName(snapshot.stage),
		semanticProgressFields(snapshot),
	)
	return err
}

func (r *semanticProgressReporter) writeComplete(snapshot semanticProgressSnapshot) error {
	_, err := fmt.Fprintf(
		r.out,
		"Semantic %s complete: %s\n",
		semanticProgressStageName(snapshot.stage),
		semanticProgressFields(snapshot),
	)
	return err
}

func semanticProgressFields(snapshot semanticProgressSnapshot) string {
	counters := snapshot.counters
	debt := snapshot.debt
	switch snapshot.stage {
	case store.SemanticRefreshProjection:
		return fmt.Sprintf(
			"projected_parents=%d dirty_parents=%d",
			counters.ProjectedParents,
			debt.DirtyParents,
		)
	case store.SemanticRefreshEmbedding:
		return fmt.Sprintf(
			"embedded_chunks=%d pending_embeddings=%d due_retries=%d scheduled_retries=%d blocked_embeddings=%d failed_embeddings=%d",
			counters.EmbeddedChunks,
			debt.PendingEmbeddings,
			debt.DueRetries,
			debt.ScheduledRetries,
			debt.BlockedEmbeddings,
			debt.FailedEmbeddings,
		)
	case store.SemanticRefreshFlush:
		return fmt.Sprintf(
			"flushed_vectors=%d indexed=%d pending_embeddings=%d l0=%d",
			counters.FlushedVectors,
			debt.Indexed,
			debt.PendingEmbeddings,
			debt.L0Ready,
		)
	case store.SemanticRefreshCompaction:
		return fmt.Sprintf(
			"compacted_vectors=%d l0=%d tombstones=%d segments=%d",
			counters.CompactedVectors,
			debt.L0Ready,
			debt.Tombstones,
			debt.Segments,
		)
	case store.SemanticRefreshVerify:
		return fmt.Sprintf(
			"verified_vectors=%d indexed=%d l0=%d tombstones=%d segments=%d",
			counters.VerifiedVectors,
			debt.Indexed,
			debt.L0Ready,
			debt.Tombstones,
			debt.Segments,
		)
	case store.SemanticRefreshReadiness:
		readiness := snapshot.readiness
		if readiness == "" {
			readiness = "unknown"
		}
		return fmt.Sprintf(
			"state=%s indexed=%d l0=%d tombstones=%d segments=%d blocked_embeddings=%d failed_embeddings=%d",
			readiness,
			debt.Indexed,
			debt.L0Ready,
			debt.Tombstones,
			debt.Segments,
			debt.BlockedEmbeddings,
			debt.FailedEmbeddings,
		)
	default:
		return fmt.Sprintf(
			"indexed=%d dirty_parents=%d pending_embeddings=%d",
			debt.Indexed,
			debt.DirtyParents,
			debt.PendingEmbeddings,
		)
	}
}
