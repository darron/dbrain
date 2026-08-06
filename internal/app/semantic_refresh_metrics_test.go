package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
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

func TestEmitFullSyncCompletionWithGCEmitsStageBeforeSuccessfulTerminalRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync-parent", Command: "sync all", Invocation: "cli", Sink: sink}
	result := semanticrefresh.Result{
		Outcome:  semanticrefresh.OutcomeCompleted,
		Duration: 4 * time.Second,
	}
	gcErr := errors.New("unlink failed")
	gc := &syncSemanticGCResult{
		Status:               syncSemanticGCStatusError,
		Duration:             3 * time.Second,
		DurationMS:           3000,
		GenerationsPruned:    2,
		SegmentsPruned:       1,
		MemberRowsPruned:     7,
		FilesystemCandidates: 3,
		FilesystemDeleted:    1,
		PrunableBytes:        4096,
		DeletedBytes:         1024,
		Error:                "filesystem_unlink",
		err:                  gcErr,
	}
	stats := completeSyncStatsWithSemanticGC(syncSemanticTestStats(), result, gc)
	if err := emitFullSyncCompletionWithGC(run, stats, result, gc, nil); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	events := readAppMetricEvents(t, path)
	if len(events) != 3 || events[0]["event"] != "semantic.refresh.completed" || events[1]["event"] != "semantic.gc.completed" || events[2]["event"] != "sync.run.completed" {
		t.Fatalf("events=%#v", events)
	}
	if events[1]["status"] != "error" || events[1]["duration_ms"] != float64(3000) || events[1]["member_rows_pruned"] != float64(7) {
		t.Fatalf("semantic GC metric=%#v", events[1])
	}
	errorObject, ok := events[1]["error"].(map[string]any)
	if !ok || errorObject["message"] != "filesystem_unlink" {
		t.Fatalf("semantic GC safe error=%#v", events[1]["error"])
	}
	if events[2]["status"] != "ok" || events[2]["duration_ms"] != float64(9000) {
		t.Fatalf("sync terminal metric=%#v", events[2])
	}
}

func TestEmitSyncSemanticGCMetricsReportsLeaseSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync-parent", Command: "sync all", Invocation: "scheduler", Sink: sink}
	gcErr := errors.Join(errors.New("acquire maintenance lock"), context.DeadlineExceeded)
	gc := &syncSemanticGCResult{
		Status:     syncSemanticGCStatusSkipped,
		Duration:   time.Second,
		DurationMS: 1000,
		SkipReason: "semantic_lease_timeout",
		err:        gcErr,
	}
	if err := emitSyncSemanticGCMetrics(run, gc); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	events := readAppMetricEvents(t, path)
	if len(events) != 1 || events[0]["status"] != "skipped" || events[0]["skip_reason"] != "semantic_lease_timeout" {
		t.Fatalf("semantic GC metric=%#v", events)
	}
	if _, ok := events[0]["error"]; !ok {
		t.Fatalf("semantic GC metric omitted structured error: %#v", events[0])
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

type semanticMetricsAuditInspector struct{}

func (semanticMetricsAuditInspector) InspectAuditSemantic(context.Context) (audit.SemanticAuditSnapshot, error) {
	return audit.SemanticAuditSnapshot{
		Configured: true, CapabilityAvailable: true, Backend: "ollama", Readiness: "ready",
		ProfileID: "embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, nil
}

func TestSemanticMetricsProducerReaderAuditRoundTrip(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	nonzero := store.SemanticRefreshCounters{
		ProjectedParents: 2, EmbeddedChunks: 3, FlushedVectors: 4,
		CompactedVectors: 5, VerifiedVectors: 6, SuccessorRuns: 7,
	}
	tests := []struct {
		name             string
		result           semanticrefresh.Result
		resultErr        error
		wantState        string
		wantCode         string
		wantCounters     store.SemanticRefreshCounters
		wantLatestStatus audit.Status
		wantStagesStatus audit.Status
	}{
		{
			name: "nonzero work", result: semanticMetricsResult(t, base, nonzero),
			wantState: "succeeded", wantCounters: nonzero,
			wantLatestStatus: audit.StatusPass, wantStagesStatus: audit.StatusPass,
		},
		{
			name: "zero work", result: semanticMetricsResult(t, base.Add(time.Minute), store.SemanticRefreshCounters{}),
			wantState: "succeeded", wantCounters: store.SemanticRefreshCounters{},
			wantLatestStatus: audit.StatusPass, wantStagesStatus: audit.StatusPass,
		},
	}
	for _, failure := range []struct {
		name  string
		stage store.SemanticRefreshStage
		code  string
	}{
		{name: "early projection failure", stage: store.SemanticRefreshProjection, code: semanticrefresh.ErrorProjection},
		{name: "early embedding failure", stage: store.SemanticRefreshEmbedding, code: semanticrefresh.ErrorEmbedding},
		{name: "cancellation", stage: store.SemanticRefreshEmbedding, code: semanticrefresh.ErrorCancelled},
	} {
		result, resultErr := semanticMetricsFailureResult(base.Add(time.Duration(len(tests)+1)*time.Minute), failure.stage, failure.code)
		state := "failed"
		if failure.code == semanticrefresh.ErrorCancelled {
			state = "canceled"
		}
		tests = append(tests, struct {
			name             string
			result           semanticrefresh.Result
			resultErr        error
			wantState        string
			wantCode         string
			wantCounters     store.SemanticRefreshCounters
			wantLatestStatus audit.Status
			wantStagesStatus audit.Status
		}{
			name: failure.name, result: result, resultErr: resultErr,
			wantState: state, wantCode: failure.code, wantCounters: result.Run.Counters,
			wantLatestStatus: audit.StatusFail, wantStagesStatus: audit.StatusUnknown,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window, report := emitReadAndAuditSemanticResult(t, test.result, test.resultErr)
			latest := window.Semantic.Latest
			wantStageIncomplete := test.wantStagesStatus == audit.StatusUnknown
			if window.Semantic.TerminalIncomplete || window.Semantic.CountersIncomplete || window.Semantic.StageActivityIncomplete != wantStageIncomplete {
				t.Fatalf("semantic completeness split=%#v", window.Semantic)
			}
			if !window.Semantic.Present || latest.State != test.wantState || latest.ErrorCode != test.wantCode || !latest.StartedAt.Equal(test.result.StartedAt) || !latest.CompletedAt.Equal(test.result.CompletedAt) {
				t.Fatalf("semantic metrics activity=%#v", window.Semantic)
			}
			gotCounters := store.SemanticRefreshCounters{
				ProjectedParents: latest.Counters.ProjectedParents, EmbeddedChunks: latest.Counters.EmbeddedChunks,
				FlushedVectors: latest.Counters.FlushedVectors, CompactedVectors: latest.Counters.CompactedVectors,
				VerifiedVectors: latest.Counters.VerifiedVectors, SuccessorRuns: latest.Counters.SuccessorRuns,
			}
			if gotCounters != test.wantCounters {
				t.Fatalf("semantic counters=%#v want=%#v", gotCounters, test.wantCounters)
			}
			latestCheck := semanticAuditCheck(t, report, audit.CheckSemanticLatestAttachedRefresh)
			stagesCheck := semanticAuditCheck(t, report, audit.CheckSemanticStageSummary)
			if latestCheck.Status != test.wantLatestStatus || stagesCheck.Status != test.wantStagesStatus {
				t.Fatalf("semantic audit latest=%#v stages=%#v", latestCheck, stagesCheck)
			}
			if test.wantCode != "" && latestCheck.Evidence["semantic_error_code"] != test.wantCode {
				t.Fatalf("semantic typed failure lost: %#v", latestCheck)
			}
			encoded, err := json.Marshal(struct {
				Activity metrics.SemanticActivity
				Report   audit.Report
			}{window.Semantic, report})
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"private-run", "/private/checkpoint", "private provider error"} {
				if strings.Contains(string(encoded), private) {
					t.Fatalf("semantic boundary leaked %q: %s", private, encoded)
				}
			}
		})
	}
}

func semanticMetricsResult(t *testing.T, startedAt time.Time, counters store.SemanticRefreshCounters) semanticrefresh.Result {
	t.Helper()
	tracker := newSemanticStageTracker(nil)
	before := store.SemanticRefreshCounters{}
	steps := []struct {
		stage store.SemanticRefreshStage
		after store.SemanticRefreshCounters
	}{
		{store.SemanticRefreshProjection, store.SemanticRefreshCounters{ProjectedParents: counters.ProjectedParents}},
		{store.SemanticRefreshEmbedding, store.SemanticRefreshCounters{ProjectedParents: counters.ProjectedParents, EmbeddedChunks: counters.EmbeddedChunks, FlushedVectors: min(counters.FlushedVectors, 1)}},
		{store.SemanticRefreshFlush, store.SemanticRefreshCounters{ProjectedParents: counters.ProjectedParents, EmbeddedChunks: counters.EmbeddedChunks, FlushedVectors: counters.FlushedVectors}},
		{store.SemanticRefreshCompaction, store.SemanticRefreshCounters{ProjectedParents: counters.ProjectedParents, EmbeddedChunks: counters.EmbeddedChunks, FlushedVectors: counters.FlushedVectors, CompactedVectors: counters.CompactedVectors}},
		{store.SemanticRefreshVerify, store.SemanticRefreshCounters{ProjectedParents: counters.ProjectedParents, EmbeddedChunks: counters.EmbeddedChunks, FlushedVectors: counters.FlushedVectors, CompactedVectors: counters.CompactedVectors, VerifiedVectors: counters.VerifiedVectors}},
		{store.SemanticRefreshReadiness, counters},
	}
	for _, step := range steps {
		run := store.SemanticRefreshRun{RunID: "private-run", Stage: step.stage, Counters: before}
		outcome := semanticrefresh.StageOutcome{Counters: step.after}
		tracker.record(run, outcome, nil, time.Second)
		before = step.after
	}
	run := store.SemanticRefreshRun{RunID: "private-run", State: store.SemanticRefreshRunCompleted, Stage: store.SemanticRefreshReadiness, Counters: counters}
	return semanticrefresh.Result{
		Outcome: semanticrefresh.OutcomeCompleted, Run: &run, StartedAt: startedAt,
		CompletedAt: startedAt.Add(6 * time.Second), Duration: 6 * time.Second, Stages: tracker.Stages(),
	}
}

func semanticMetricsFailureResult(startedAt time.Time, failedStage store.SemanticRefreshStage, code string) (semanticrefresh.Result, error) {
	tracker := newSemanticStageTracker(nil)
	counters := store.SemanticRefreshCounters{}
	if failedStage == store.SemanticRefreshEmbedding {
		projection := store.SemanticRefreshRun{RunID: "private-run", Stage: store.SemanticRefreshProjection}
		projectionOutcome := semanticrefresh.StageOutcome{Counters: store.SemanticRefreshCounters{ProjectedParents: 2}}
		tracker.record(projection, projectionOutcome, nil, time.Second)
		counters = projectionOutcome.Counters
	}
	run := store.SemanticRefreshRun{RunID: "private-run", Stage: failedStage, State: store.SemanticRefreshRunFailed, Counters: counters, Checkpoint: "/private/checkpoint"}
	refreshErr := semanticrefresh.NewError(code, run, "", semanticrefresh.Debt{}, errors.New("private provider error"))
	tracker.record(run, semanticrefresh.StageOutcome{Counters: counters}, refreshErr, time.Second)
	completedAt := startedAt.Add(time.Duration(len(tracker.Stages())) * time.Second)
	result := semanticrefresh.Result{Run: &run, StartedAt: startedAt, CompletedAt: completedAt, Duration: completedAt.Sub(startedAt), Stages: tracker.Stages()}
	return result, refreshErr
}

func emitReadAndAuditSemanticResult(t *testing.T, result semanticrefresh.Result, resultErr error) (metrics.Window, audit.Report) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync-parent", Command: "sync all", Invocation: "cli", Sink: sink}
	if err := emitSemanticRefreshStarted(run, result.StartedAt); err != nil {
		t.Fatal(err)
	}
	stats := syncjob.Stats{StartedAt: result.StartedAt.Add(-time.Second), CompletedAt: result.CompletedAt.Add(time.Second), Duration: result.Duration + 2*time.Second}
	if err := emitFullSyncCompletion(run, stats, result, resultErr); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	reader := metrics.NewReader(path)
	window, err := reader.Read(t.Context(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := audit.Run(t.Context(), audit.Request{
		Profile:  audit.ProfileStandard,
		CheckIDs: []audit.CheckID{audit.CheckSemanticLatestAttachedRefresh, audit.CheckSemanticStageSummary},
	}, audit.Dependencies{
		Features: audit.Features{Layout: "explicit_root", SemanticConfigured: true, SemanticCapabilityAvailable: true},
		Metrics:  reader, Semantic: semanticMetricsAuditInspector{},
		Clock: func() time.Time { return result.CompletedAt.Add(time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return window, report
}

func semanticAuditCheck(t *testing.T, report audit.Report, id audit.CheckID) audit.Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("semantic audit check %s missing", id)
	return audit.Check{}
}
