package app

import (
	"errors"
	"time"

	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

func emitSemanticRefreshStarted(run metrics.RunContext, startedAt time.Time) error {
	if !run.Enabled() {
		return nil
	}
	return run.Emit(metrics.Event{
		"event":      "semantic.refresh.started",
		"status":     "started",
		"started_at": startedAt.UTC().Format(time.RFC3339Nano),
	})
}

func emitSemanticRefreshMetrics(
	run metrics.RunContext,
	result semanticrefresh.Result,
	resultErr error,
) error {
	if !run.Enabled() {
		return nil
	}
	var emitErr error
	for _, stage := range result.Stages {
		status := stage.Status
		if status == "" {
			status = "ok"
		}
		event := metrics.Event{
			"event":               "semantic.stage.completed",
			"stage":               string(stage.Stage),
			"status":              status,
			"duration_ms":         metrics.DurationMillis(stage.Duration),
			"units":               stage.Units,
			"semantic_run_ids":    append([]string{}, stage.RunIDs...),
			"counts":              stage.Counters,
			"remaining_debt":      stage.RemainingDebt,
			"semantic_error_code": stage.ErrorCode,
		}
		emitErr = errors.Join(emitErr, run.Emit(event))
	}

	status := "ok"
	if result.Outcome == semanticrefresh.OutcomeSkipped {
		status = "skipped"
	}
	if resultErr != nil {
		status = "error"
	}
	terminal := metrics.Event{
		"event":          "semantic.refresh.completed",
		"status":         status,
		"outcome":        string(result.Outcome),
		"skip_reason":    result.SkipReason,
		"started_at":     result.StartedAt.UTC().Format(time.RFC3339Nano),
		"completed_at":   result.CompletedAt.UTC().Format(time.RFC3339Nano),
		"duration_ms":    metrics.DurationMillis(result.Duration),
		"capability":     string(result.Capability.State),
		"backend":        result.Capability.Backend,
		"version":        result.Capability.Version,
		"remaining_debt": result.Debt,
		"counts":         terminalSemanticRefreshCounters(result),
	}
	if resultErr != nil {
		terminal["error"] = metrics.ErrorObject(resultErr)
		terminal["semantic_error_code"] = "semantic_refresh_failed"
		var refreshErr *semanticrefresh.RefreshError
		if errors.As(resultErr, &refreshErr) {
			terminal["semantic_error_code"] = refreshErr.Error()
		}
	}
	emitErr = errors.Join(emitErr, run.Emit(terminal))
	return emitErr
}

func terminalSemanticRefreshCounters(result semanticrefresh.Result) store.SemanticRefreshCounters {
	if result.Run == nil {
		return store.SemanticRefreshCounters{}
	}
	return result.Run.Counters
}

func emitFullSyncCompletion(
	run metrics.RunContext,
	stats syncjob.Stats,
	result semanticrefresh.Result,
	runErr error,
) error {
	emitErr := emitSemanticRefreshMetrics(run, result, runErr)
	syncjob.EmitRunCompleted(run, stats, runErr)
	return emitErr
}
