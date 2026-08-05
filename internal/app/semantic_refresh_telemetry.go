package app

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

type semanticStageTracker struct {
	now    func() time.Time
	stages map[store.SemanticRefreshStage]*semanticrefresh.StageStats
}

type semanticStageMeasuringExecutor struct {
	delegate semanticrefresh.StageExecutor
	tracker  *semanticStageTracker
}

func newSemanticStageTracker(now func() time.Time) *semanticStageTracker {
	if now == nil {
		now = time.Now
	}
	return &semanticStageTracker{
		now:    now,
		stages: make(map[store.SemanticRefreshStage]*semanticrefresh.StageStats, len(semanticRefreshStageOrder)),
	}
}

func (t *semanticStageTracker) Wrap(executor semanticrefresh.StageExecutor) semanticrefresh.StageExecutor {
	return semanticStageMeasuringExecutor{delegate: executor, tracker: t}
}

func (e semanticStageMeasuringExecutor) Execute(
	ctx context.Context,
	run store.SemanticRefreshRun,
	progress semanticrefresh.StageProgressCallback,
) (semanticrefresh.StageOutcome, error) {
	startedAt := e.tracker.now()
	outcome, err := e.delegate.Execute(ctx, run, progress)
	e.tracker.record(run, outcome, err, nonnegativeDuration(e.tracker.now().Sub(startedAt)))
	return outcome, err
}

func (t *semanticStageTracker) record(run store.SemanticRefreshRun, outcome semanticrefresh.StageOutcome, runErr error, duration time.Duration) {
	stage := run.Stage
	value := t.stages[stage]
	if value == nil {
		value = &semanticrefresh.StageStats{
			Stage:  stage,
			Status: "ok",
			RunIDs: []string{},
		}
		t.stages[stage] = value
	}
	value.Duration = saturatingDurationAdd(value.Duration, duration)
	if value.Units < math.MaxInt {
		value.Units++
	}
	if runID := safeSemanticRefreshOutputField(run.RunID, 64); runID != "" && !containsSemanticRunID(value.RunIDs, runID) {
		value.RunIDs = append(value.RunIDs, runID)
	}
	addSemanticStageCounterDelta(&value.Counters, stage, run.Counters, outcome.Counters)
	value.RemainingDebt = outcome.Debt
	if runErr != nil {
		value.Status = "error"
		var refreshErr *semanticrefresh.RefreshError
		if errors.As(runErr, &refreshErr) {
			value.ErrorCode = safeSemanticRefreshOutputField(refreshErr.Code, 64)
		}
	}
}

func (t *semanticStageTracker) Stages() []semanticrefresh.StageStats {
	out := make([]semanticrefresh.StageStats, 0, len(t.stages))
	for _, stage := range semanticRefreshStageOrder {
		if value := t.stages[stage]; value != nil {
			copyValue := *value
			copyValue.RunIDs = append([]string{}, value.RunIDs...)
			out = append(out, copyValue)
		}
	}
	return out
}

var semanticRefreshStageOrder = []store.SemanticRefreshStage{
	store.SemanticRefreshProjection,
	store.SemanticRefreshEmbedding,
	store.SemanticRefreshFlush,
	store.SemanticRefreshCompaction,
	store.SemanticRefreshVerify,
	store.SemanticRefreshReadiness,
}

func addSemanticStageCounterDelta(dst *store.SemanticRefreshCounters, stage store.SemanticRefreshStage, before, after store.SemanticRefreshCounters) {
	switch stage {
	case store.SemanticRefreshProjection:
		dst.ProjectedParents = saturatingCounterAdd(dst.ProjectedParents, counterDelta(before.ProjectedParents, after.ProjectedParents))
	case store.SemanticRefreshEmbedding:
		dst.EmbeddedChunks = saturatingCounterAdd(dst.EmbeddedChunks, counterDelta(before.EmbeddedChunks, after.EmbeddedChunks))
		// Embedding may perform a protective flush before a provider call.
		dst.FlushedVectors = saturatingCounterAdd(dst.FlushedVectors, counterDelta(before.FlushedVectors, after.FlushedVectors))
	case store.SemanticRefreshFlush:
		dst.FlushedVectors = saturatingCounterAdd(dst.FlushedVectors, counterDelta(before.FlushedVectors, after.FlushedVectors))
	case store.SemanticRefreshCompaction:
		dst.CompactedVectors = saturatingCounterAdd(dst.CompactedVectors, counterDelta(before.CompactedVectors, after.CompactedVectors))
	case store.SemanticRefreshVerify:
		dst.VerifiedVectors = saturatingCounterAdd(dst.VerifiedVectors, counterDelta(before.VerifiedVectors, after.VerifiedVectors))
	}
}

func counterDelta(before, after int64) int64 {
	if before < 0 || after <= before {
		return 0
	}
	return after - before
}

func saturatingCounterAdd(current, delta int64) int64 {
	if current < 0 {
		current = 0
	}
	if delta <= 0 {
		return current
	}
	if delta > math.MaxInt64-current {
		return math.MaxInt64
	}
	return current + delta
}

func nonnegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

func saturatingDurationAdd(current, delta time.Duration) time.Duration {
	current = nonnegativeDuration(current)
	delta = nonnegativeDuration(delta)
	if delta > time.Duration(math.MaxInt64)-current {
		return time.Duration(math.MaxInt64)
	}
	return current + delta
}

func containsSemanticRunID(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
