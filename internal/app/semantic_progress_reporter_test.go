package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

type recordingSemanticProgressTarget struct {
	bytes.Buffer
	events []recordedSemanticProgressEvent
}

type recordedSemanticProgressEvent struct {
	kind     string
	snapshot semanticProgressSnapshot
}

func (t *recordingSemanticProgressTarget) startSemanticProgress(snapshot semanticProgressSnapshot) error {
	t.events = append(t.events, recordedSemanticProgressEvent{kind: "start", snapshot: snapshot})
	return nil
}

func (t *recordingSemanticProgressTarget) updateSemanticProgress(snapshot semanticProgressSnapshot) error {
	t.events = append(t.events, recordedSemanticProgressEvent{kind: "update", snapshot: snapshot})
	return nil
}

func (t *recordingSemanticProgressTarget) finishSemanticProgress(snapshot semanticProgressSnapshot, success bool) error {
	kind := "failure"
	if success {
		kind = "success"
	}
	t.events = append(t.events, recordedSemanticProgressEvent{kind: kind, snapshot: snapshot})
	return nil
}

func TestSemanticProgressReporterUsesTypedTargetWithoutSerializingProgress(t *testing.T) {
	now := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	target := &recordingSemanticProgressTarget{}
	reporter := newSemanticProgressReporter(target, func() time.Time { return now })
	progress := semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 16, Total: 64, TotalKnown: true},
	}

	if err := reporter.Report(progress); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if got := target.String(); got != "" {
		t.Fatalf("typed target received serialized output %q", got)
	}
	if len(target.events) != 3 || target.events[0].kind != "start" ||
		target.events[1].kind != "update" || target.events[2].kind != "success" {
		t.Fatalf("typed events = %#v, want start, update, success", target.events)
	}
	for _, event := range target.events {
		if event.snapshot.stage != store.SemanticRefreshEmbedding ||
			event.snapshot.work != progress.Work {
			t.Fatalf("typed event = %#v, want embedding work %+v", event, progress.Work)
		}
	}
}

func TestSemanticProgressReporterFailureDoesNotCompleteTypedStage(t *testing.T) {
	now := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	target := &recordingSemanticProgressTarget{}
	reporter := newSemanticProgressReporter(target, func() time.Time { return now })
	if err := reporter.Report(semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 16, Total: 64, TotalKnown: true},
	}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := reporter.Finish(false); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if len(target.events) != 3 || target.events[2].kind != "failure" {
		t.Fatalf("typed events = %#v, want terminal failure", target.events)
	}
	for _, event := range target.events {
		if event.kind == "success" {
			t.Fatalf("failed stage emitted success: %#v", target.events)
		}
	}
}

func TestSemanticProgressReporterStageTransitionFinishesLatestObservedWork(t *testing.T) {
	base := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	now := base
	target := &recordingSemanticProgressTarget{}
	reporter := newSemanticProgressReporter(target, func() time.Time { return now })
	progress := semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 0, Total: 100, TotalKnown: true},
	}
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("initial report: %v", err)
	}
	now = base.Add(time.Second)
	progress.Work.Current = 100
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("coalesced final report: %v", err)
	}
	now = base.Add(2 * time.Second)
	if err := reporter.Report(semanticrefresh.Progress{
		Stage: store.SemanticRefreshFlush,
		Work:  semanticrefresh.StageWork{Current: 0, Total: 10, TotalKnown: true},
	}); err != nil {
		t.Fatalf("transition report: %v", err)
	}

	if len(target.events) != 5 {
		t.Fatalf("typed events = %#v, want embedding start/update/success then flush start/update", target.events)
	}
	transition := target.events[2]
	if transition.kind != "success" || transition.snapshot.stage != store.SemanticRefreshEmbedding ||
		transition.snapshot.work.Current != 100 {
		t.Fatalf("transition terminal event = %#v, want final observed embedding work", transition)
	}
}

func TestSemanticProgressReporterStartsTransitionsAndFinishesStages(t *testing.T) {
	now := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	var output bytes.Buffer
	reporter := newSemanticProgressReporter(&output, func() time.Time { return now })

	projection := semanticrefresh.Progress{
		RunID:      "do-not-print-run",
		ProfileID:  "do-not-print-profile",
		Checkpoint: "do-not-print-checkpoint",
		Stage:      store.SemanticRefreshProjection,
		Counters:   store.SemanticRefreshCounters{ProjectedParents: 17},
		Debt:       semanticrefresh.Debt{DirtyParents: 3},
	}
	if err := reporter.Report(projection); err != nil {
		t.Fatalf("report projection: %v", err)
	}

	now = now.Add(time.Second)
	embedding := semanticrefresh.Progress{
		Stage:    store.SemanticRefreshEmbedding,
		Counters: store.SemanticRefreshCounters{ProjectedParents: 17, EmbeddedChunks: 42},
		Debt: semanticrefresh.Debt{
			PendingEmbeddings: 5,
			DueRetries:        2,
			ScheduledRetries:  1,
			BlockedEmbeddings: 4,
			FailedEmbeddings:  3,
		},
	}
	if err := reporter.Report(embedding); err != nil {
		t.Fatalf("report embedding: %v", err)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}

	want := strings.Join([]string{
		"==> semantic projection",
		"Semantic projection progress: projected_parents=17 dirty_parents=3",
		"Semantic projection complete: projected_parents=17 dirty_parents=3",
		"==> semantic embedding",
		"Semantic embedding progress: embedded_chunks=42 pending_embeddings=5 due_retries=2 scheduled_retries=1 blocked_embeddings=4 failed_embeddings=3",
		"Semantic embedding complete: embedded_chunks=42 pending_embeddings=5 due_retries=2 scheduled_retries=1 blocked_embeddings=4 failed_embeddings=3",
		"",
	}, "\n")
	if got := output.String(); got != want {
		t.Fatalf("output mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
	for _, secret := range []string{"do-not-print-run", "do-not-print-profile", "do-not-print-checkpoint"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("output leaked raw diagnostic field %q: %s", secret, output.String())
		}
	}
}

func TestSemanticProgressReporterRateLimitsChangesAndHeartbeats(t *testing.T) {
	base := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	now := base
	var output bytes.Buffer
	reporter := newSemanticProgressReporter(&output, func() time.Time { return now })
	progress := semanticrefresh.Progress{
		Stage:    store.SemanticRefreshCompaction,
		Counters: store.SemanticRefreshCounters{CompactedVectors: 10},
		Debt:     semanticrefresh.Debt{L0Ready: 7, Tombstones: 2, Segments: 4},
	}

	if err := reporter.Report(progress); err != nil {
		t.Fatalf("initial report: %v", err)
	}
	progress.Counters.CompactedVectors = 11
	now = base.Add(time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("early changed report: %v", err)
	}
	now = base.Add(4 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("coalesced report: %v", err)
	}
	now = base.Add(5 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("due changed report: %v", err)
	}

	now = base.Add(34 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("early heartbeat: %v", err)
	}
	now = base.Add(35 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("due heartbeat: %v", err)
	}

	if got := strings.Count(output.String(), "Semantic compaction progress:"); got != 3 {
		t.Fatalf("progress line count = %d, want 3; output:\n%s", got, output.String())
	}
	if strings.Contains(output.String(), "compacted_vectors=11\nSemantic compaction progress: compacted_vectors=11") {
		t.Fatalf("changed snapshots were not rate limited: %s", output.String())
	}
}

func TestSemanticProgressReporterSanitizesStageAndReadiness(t *testing.T) {
	now := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	var output bytes.Buffer
	reporter := newSemanticProgressReporter(&output, func() time.Time { return now })

	if err := reporter.Report(semanticrefresh.Progress{
		Stage:     store.SemanticRefreshStage("readiness\nforged"),
		Readiness: "ready\nforged",
		Debt:      semanticrefresh.Debt{Indexed: 9, L0Ready: 1, Segments: 2},
	}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if strings.Contains(output.String(), "forged") {
		t.Fatalf("unsafe field reached output: %q", output.String())
	}
	if !strings.Contains(output.String(), "==> semantic unknown\n") {
		t.Fatalf("unsafe stage did not use safe fallback: %q", output.String())
	}
}

func TestSemanticProgressReporterFinishIsIdempotent(t *testing.T) {
	now := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	var output bytes.Buffer
	reporter := newSemanticProgressReporter(&output, func() time.Time { return now })
	if err := reporter.Report(semanticrefresh.Progress{Stage: store.SemanticRefreshFlush}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("second finish: %v", err)
	}
	if got := strings.Count(output.String(), "Semantic flush complete:"); got != 1 {
		t.Fatalf("completion count = %d, want 1; output:\n%s", got, output.String())
	}
}
