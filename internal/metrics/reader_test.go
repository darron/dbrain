package metrics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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

func TestReaderRejectsNonRegularDescriptorAfterNonblockingOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes use Unix filesystem semantics")
	}
	path := filepath.Join(t.TempDir(), "metrics.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := NewReader(path).Read(ctx, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error = %v, want non-regular rejection", err)
	}
}

func TestReaderNeverBlocksWhenRegularMetricsPathIsReplacedByFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes use Unix filesystem semantics")
	}
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(path)
			_ = syscall.Mkfifo(path, 0o600)
			_ = os.Remove(path)
			_ = os.WriteFile(path, []byte("\n"), 0o600)
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()
	for index := 0; index < 100; index++ {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, _ = NewReader(path).Read(ctx, time.Time{})
		cancel()
		if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
			t.Fatalf("replacement read blocked for %s on iteration %d", elapsed, index)
		}
	}
}

func TestReaderRejectsWrongSchemaAndTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	valid, _ := json.Marshal(map[string]any{"schema": SchemaVersion, "event": "sync.run.started", "run_id": "valid", "emitted_at": base.Format(time.RFC3339), "started_at": base.Format(time.RFC3339)})
	wrongSchema, _ := json.Marshal(map[string]any{"schema": "dbrain.metrics.v0", "event": "sync.run.started", "run_id": "wrong", "emitted_at": base.Format(time.RFC3339)})
	missingSchema, _ := json.Marshal(map[string]any{"event": "sync.run.started", "run_id": "missing", "emitted_at": base.Format(time.RFC3339)})
	content := string(wrongSchema) + "\n" + string(missingSchema) + "\n" + string(valid) + ` {"trailing":true}` + "\n" + string(valid) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	window, err := NewReader(path).Read(t.Context(), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if window.ParseErrorCount != 3 || len(window.Runs) != 1 || window.Runs[0].ID != "valid" {
		t.Fatalf("window = %#v", window)
	}
}

func TestReaderReadsNewestFirstUntilPreWindowBoundaryBeforeEventBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	events := make([]map[string]any, 0, 7)
	for i := 0; i < 5; i++ {
		events = append(events, map[string]any{"schema": SchemaVersion, "event": "sync.run.started", "run_id": "old-" + string(rune('a'+i)), "emitted_at": base.Add(time.Duration(i-10) * time.Hour).Format(time.RFC3339), "started_at": base.Add(time.Duration(i-10) * time.Hour).Format(time.RFC3339)})
	}
	for i := 0; i < 2; i++ {
		events = append(events, map[string]any{"schema": SchemaVersion, "event": "sync.run.started", "run_id": "recent-" + string(rune('a'+i)), "emitted_at": base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339), "started_at": base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)})
	}
	writeMetricEvents(t, path, events)
	reader := NewReader(path)
	reader.MaxEvents = 3
	reader.BlockBytes = 64
	window, err := reader.Read(t.Context(), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Runs) != 2 || window.EventBudgetExhausted || !window.CoverageStart.Before(base.Add(-time.Hour)) {
		t.Fatalf("window = %#v", window)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if window.BytesRead <= 0 || window.BytesRead >= info.Size() {
		t.Fatalf("bytes read = %d, file size = %d", window.BytesRead, info.Size())
	}
}

func TestReaderSeparatesAttemptsCompletionsAndActualArrivals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	writeMetricEvents(t, path, []map[string]any{
		{"schema": SchemaVersion, "event": "sync.run.started", "run_id": "complete", "emitted_at": base.Format(time.RFC3339), "started_at": base.Format(time.RFC3339)},
		{"schema": SchemaVersion, "event": "sync.run.completed", "run_id": "complete", "emitted_at": base.Add(time.Minute).Format(time.RFC3339), "completed_at": base.Add(time.Minute).Format(time.RFC3339), "status": "ok"},
		{"schema": SchemaVersion, "event": "sync.import.completed", "source": "feeds", "emitted_at": base.Add(2 * time.Minute).Format(time.RFC3339), "status": "ok", "counts": map[string]int{"created": 2}},
		{"schema": SchemaVersion, "event": "sync.run.started", "run_id": "incomplete", "emitted_at": base.Add(3 * time.Minute).Format(time.RFC3339), "started_at": base.Add(3 * time.Minute).Format(time.RFC3339)},
		{"schema": SchemaVersion, "event": "sync.import.completed", "source": "feeds", "emitted_at": base.Add(4 * time.Minute).Format(time.RFC3339), "status": "ok", "counts": map[string]int{"unchanged": 4}},
	})
	window, err := NewReader(path).Read(t.Context(), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if window.AttemptCount != 2 || window.CompletedCount != 1 || !window.LatestAttemptPresent || !window.LatestCompletedPresent {
		t.Fatalf("attempt/completion truth = %#v", window)
	}
	if got := window.Imports["feeds"].LastArrivalAt; !got.Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("last arrival = %s", got)
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
