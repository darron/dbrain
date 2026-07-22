package app

import (
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
)

func openSyncMetrics(cfg config.Config, invocation string) (metrics.RunContext, func() error, error) {
	metricsConfig, err := metrics.ResolveConfig(cfg.RootDir, cfg.LogDir)
	if err != nil {
		return metrics.RunContext{}, nil, err
	}
	sink, err := metrics.Open(metricsConfig)
	if err != nil {
		return metrics.RunContext{}, nil, err
	}
	run := metrics.RunContext{
		RunID:      metrics.NewRunID("sync", time.Now().UTC()),
		Command:    "sync all",
		Invocation: invocation,
		Sink:       sink,
	}
	return run, sink.Close, nil
}

func openSQLiteArchiveMetrics(cfg config.Config, reason string) (metrics.RunContext, func() error, error) {
	metricsConfig, err := metrics.ResolveConfig(cfg.RootDir, cfg.LogDir)
	if err != nil {
		return metrics.RunContext{}, nil, err
	}
	sink, err := metrics.Open(metricsConfig)
	if err != nil {
		return metrics.RunContext{}, nil, err
	}
	invocation := "scheduler:interval"
	if reason == "startup" {
		invocation = "scheduler:startup"
	}
	run := metrics.RunContext{
		RunID:      metrics.NewRunID("sqlite_archive", time.Now().UTC()),
		Command:    "sqlite archive",
		Invocation: invocation,
		Sink:       sink,
	}
	return run, sink.Close, nil
}

func emitSyncMetricsSkipped(run metrics.RunContext, err error) {
	if !run.Enabled() {
		return
	}
	now := time.Now().UTC()
	_ = run.Emit(metrics.Event{
		"event":        "sync.run.completed",
		"status":       "skipped",
		"completed_at": now.Format(time.RFC3339Nano),
		"duration_ms":  int64(0),
		"error":        metrics.ErrorObject(err),
	})
}
