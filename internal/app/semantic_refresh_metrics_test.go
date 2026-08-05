package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

func TestEmitFullSyncCompletionKeepsSemanticEventsUnderParentRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync-parent", Command: "sync all", Invocation: "cli", Sink: sink}
	startedAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	result := semanticrefresh.Result{
		Outcome:     semanticrefresh.OutcomeCompleted,
		StartedAt:   startedAt,
		CompletedAt: startedAt.Add(4 * time.Second),
		Duration:    4 * time.Second,
		Stages: []semanticrefresh.StageStats{{
			Stage:    store.SemanticRefreshEmbedding,
			Duration: 3 * time.Second,
			Units:    2,
			Status:   "ok",
			RunIDs:   []string{"semantic-ledger-run"},
			Counters: store.SemanticRefreshCounters{EmbeddedChunks: 8},
		}},
	}
	stats := completeSyncStatsWithSemantic(syncSemanticTestStats(), result)
	if err := emitSemanticRefreshStarted(run, startedAt); err != nil {
		t.Fatal(err)
	}
	if err := emitFullSyncCompletion(run, stats, result, nil); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	events := readAppMetricEvents(t, path)
	wantNames := []string{
		"semantic.refresh.started",
		"semantic.stage.completed",
		"semantic.refresh.completed",
		"sync.run.completed",
	}
	if len(events) != len(wantNames) {
		t.Fatalf("events = %#v", events)
	}
	for index, event := range events {
		if event["event"] != wantNames[index] || event["run_id"] != "sync-parent" {
			t.Fatalf("event %d = %#v", index, event)
		}
	}
	stage := events[1]
	runIDs, ok := stage["semantic_run_ids"].([]any)
	if !ok || len(runIDs) != 1 || runIDs[0] != "semantic-ledger-run" {
		t.Fatalf("semantic stage run namespace = %#v", stage)
	}
	if events[3]["duration_ms"] != float64(6000) {
		t.Fatalf("full duration event = %#v", events[3])
	}
}

func TestEmitSemanticRefreshMetricsWritesClosedTerminalFailureCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync-parent", Command: "sync all", Invocation: "cli", Sink: sink}
	result := semanticrefresh.Result{StartedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), CompletedAt: time.Date(2026, 8, 4, 12, 0, 1, 0, time.UTC), Duration: time.Second}
	refreshErr := semanticrefresh.NewError(semanticrefresh.ErrorEmbedding, store.SemanticRefreshRun{RunID: "private-run", Stage: store.SemanticRefreshEmbedding, Checkpoint: "/private/checkpoint"}, "", semanticrefresh.Debt{}, errors.New("private provider error"))
	if err := emitSemanticRefreshMetrics(run, result, refreshErr); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	events := readAppMetricEvents(t, path)
	if len(events) != 1 || events[0]["semantic_error_code"] != semanticrefresh.ErrorEmbedding {
		t.Fatalf("terminal event = %#v", events)
	}
}

func TestEmitSemanticRefreshMetricsFallsBackToClosedGenericFailureCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync-parent", Command: "sync all", Invocation: "cli", Sink: sink}
	result := semanticrefresh.Result{StartedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), CompletedAt: time.Date(2026, 8, 4, 12, 0, 1, 0, time.UTC), Duration: time.Second}
	if err := emitSemanticRefreshMetrics(run, result, errors.New("private provider error")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	events := readAppMetricEvents(t, path)
	if len(events) != 1 || events[0]["semantic_error_code"] != "semantic_refresh_failed" {
		t.Fatalf("terminal event = %#v", events)
	}
}
