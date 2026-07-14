package metrics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReaderReconstructsRunsPollsArrivalsAndExplicitMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	events := []map[string]any{
		{"schema": SchemaVersion, "event": "scheduler.sync.enabled", "emitted_at": base.Format(time.RFC3339), "interval_seconds": 3600},
		{"schema": SchemaVersion, "event": "sync.run.started", "run_id": "run-1", "invocation": "scheduler:interval", "emitted_at": base.Add(time.Minute).Format(time.RFC3339), "started_at": base.Add(time.Minute).Format(time.RFC3339), "selected_stages": []string{"github", "youtube"}},
		{"schema": SchemaVersion, "event": "sync.stage.completed", "run_id": "run-1", "emitted_at": base.Add(2 * time.Minute).Format(time.RFC3339), "stage": "github", "status": "error", "duration_ms": 1000},
		{"schema": SchemaVersion, "event": "sync.stage.completed", "run_id": "run-1", "emitted_at": base.Add(3 * time.Minute).Format(time.RFC3339), "stage": "youtube", "status": "ok", "duration_ms": 2000},
		{"schema": SchemaVersion, "event": "sync.import.completed", "run_id": "run-1", "emitted_at": base.Add(3 * time.Minute).Format(time.RFC3339), "source": "github_stars", "status": "ok", "attempted_at": base.Add(time.Minute).Format(time.RFC3339), "completed_at": base.Add(3 * time.Minute).Format(time.RFC3339), "counts": map[string]int{"created": 2, "updated": 1, "unchanged": 4, "skipped": 0, "linked": 3, "blocked": 0, "failed": 0}},
		{"schema": SchemaVersion, "event": "sync.run.completed", "run_id": "run-1", "invocation": "scheduler:interval", "emitted_at": base.Add(4 * time.Minute).Format(time.RFC3339), "started_at": base.Add(time.Minute).Format(time.RFC3339), "completed_at": base.Add(4 * time.Minute).Format(time.RFC3339), "duration_ms": 180000, "status": "ok"},
		{"schema": SchemaVersion, "event": "scheduler.sync.lock_skipped", "emitted_at": base.Add(2 * time.Hour).Format(time.RFC3339)},
		{"schema": SchemaVersion, "event": "scheduler.sync.overlap_skipped", "emitted_at": base.Add(3 * time.Hour).Format(time.RFC3339)},
		{"schema": SchemaVersion, "event": "scheduler.sync.stopped", "emitted_at": base.Add(4 * time.Hour).Format(time.RFC3339)},
	}
	writeMetricEvents(t, path, events)

	window, err := NewReader(path).Read(context.Background(), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Runs) != 1 {
		t.Fatalf("runs = %d", len(window.Runs))
	}
	run := window.Runs[0]
	if !run.RecordComplete || run.Status != "ok" || len(run.SelectedStages) != 2 || len(run.CompletedStages) != 2 || run.ToleratedStageErrors != 1 {
		t.Fatalf("run reconstruction = %#v", run)
	}
	got := window.Imports["github_stars"]
	if got.AttemptCount != 1 || got.SuccessCount != 1 || got.FailureCount != 0 || len(got.Daily) != 1 || got.Daily[0].Created != 2 || got.Daily[0].Linked != 3 {
		t.Fatalf("import aggregate = %#v", got)
	}
	if len(window.Markers) != 4 || !window.Markers[1].ExplainsContinuity || window.Markers[2].ExplainsContinuity || !window.Markers[3].ExplainsContinuity {
		t.Fatalf("markers = %#v", window.Markers)
	}
}

func TestReaderBoundsLinesBytesEventsAndKeepsMalformedPositionsInternal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	valid, _ := json.Marshal(map[string]any{"schema": SchemaVersion, "event": "sync.run.completed", "run_id": "r", "emitted_at": base.Format(time.RFC3339), "started_at": base.Format(time.RFC3339), "completed_at": base.Add(time.Second).Format(time.RFC3339), "status": "ok"})
	content := strings.Repeat("x", 513) + "\n{" + "\n" + string(valid) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(path)
	reader.MaxLineBytes = 512
	reader.MaxBytes = 4096
	reader.MaxEvents = 10
	window, err := reader.Read(context.Background(), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if window.ParseErrorCount != 2 || len(window.ParseErrorPositions) != 2 {
		t.Fatalf("parse errors = %d positions=%v", window.ParseErrorCount, window.ParseErrorPositions)
	}
	if strings.Contains(window.String(), "xxxxx") || strings.Contains(window.String(), "{") {
		t.Fatalf("window String leaked malformed content: %q", window.String())
	}
	reader.MaxEvents = 1
	window, err = reader.Read(context.Background(), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !window.EventBudgetExhausted {
		t.Fatal("expected event budget exhaustion")
	}
}

func TestReaderUsesOnlyResolvedPathAndRequestedWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolved.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	writeMetricEvents(t, other, []map[string]any{{"schema": SchemaVersion, "event": "sync.run.completed", "run_id": "wrong", "emitted_at": base.Format(time.RFC3339), "status": "ok"}})
	writeMetricEvents(t, path, []map[string]any{
		{"schema": SchemaVersion, "event": "sync.run.completed", "run_id": "old", "emitted_at": base.Add(-48 * time.Hour).Format(time.RFC3339), "status": "ok"},
		{"schema": SchemaVersion, "event": "sync.run.completed", "run_id": "right", "emitted_at": base.Format(time.RFC3339), "started_at": base.Add(-time.Minute).Format(time.RFC3339), "completed_at": base.Format(time.RFC3339), "status": "ok"},
	})
	window, err := NewReader(path).Read(context.Background(), base.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Runs) != 1 || window.Runs[0].ID != "right" {
		t.Fatalf("runs = %#v", window.Runs)
	}
}

func writeMetricEvents(t *testing.T, path string, events []map[string]any) {
	t.Helper()
	var out strings.Builder
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(data)
		out.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
