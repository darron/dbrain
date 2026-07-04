package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/runtimeenv"
)

func TestResolveConfigDisabledByDefault(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")

	cfg, err := ResolveConfig(root, logDir)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if cfg.Detail != DetailStage {
		t.Fatalf("Detail = %q, want %q", cfg.Detail, DetailStage)
	}
	if cfg.Path != "" {
		t.Fatalf("Path = %q, want empty when disabled", cfg.Path)
	}
}

func TestResolveConfigEnabledDefaultAndRelativePath(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	writeMetricsConfig(t, root, `metrics:
  enabled: true
  detail: item
  path: local-model-metrics.jsonl
  include_subject_keys: true
  strict: true
`)

	cfg, err := ResolveConfig(root, logDir)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if !cfg.Enabled || cfg.Detail != DetailItem || !cfg.IncludeSubjectKeys || !cfg.Strict {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	wantPath := filepath.Join(logDir, "local-model-metrics.jsonl")
	if cfg.Path != wantPath {
		t.Fatalf("Path = %q, want %q", cfg.Path, wantPath)
	}
}

func TestResolveConfigRejectsInvalidDetail(t *testing.T) {
	root := t.TempDir()
	writeMetricsConfig(t, root, `metrics:
  enabled: true
  detail: noisy
`)

	_, err := ResolveConfig(root, filepath.Join(root, "logs"))
	if err == nil || !strings.Contains(err.Error(), "metrics.detail") {
		t.Fatalf("ResolveConfig err = %v, want metrics.detail error", err)
	}
}

func TestSinkWritesJSONLAndOmitsSubjectKeysByDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailItem})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := RunContext{RunID: "sync_test_00000000", Command: "sync all", Invocation: "cli", Sink: sink}
	longErr := strings.Repeat("x", 400)

	if err := ctx.Emit(Event{
		"event":        "categorize.item.completed",
		"status":       "error",
		"subject_kind": "item",
		"duration_ms":  DurationMillis(1234 * time.Millisecond),
		"error":        ErrorObject(longErr),
	}.WithSubject("item", "x:secret-source-key", false)); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readMetricEvents(t, path)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event["schema"] != SchemaVersion || event["run_id"] != "sync_test_00000000" || event["command"] != "sync all" {
		t.Fatalf("missing envelope fields: %#v", event)
	}
	if _, ok := event["subject_key"]; ok {
		t.Fatalf("subject_key leaked by default: %#v", event)
	}
	if got := event["subject_hash"].(string); !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("subject_hash = %q, want sha256 prefix", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if strings.Contains(string(raw), "x:secret-source-key") {
		t.Fatalf("raw subject key leaked in JSONL: %s", raw)
	}
	errObj := event["error"].(map[string]any)
	if len(errObj["message"].(string)) > 300 {
		t.Fatalf("error message was not capped: %d", len(errObj["message"].(string)))
	}
}

func TestSinkIncludesSubjectKeysOnlyWhenEnabled(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailItem, IncludeSubjectKeys: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := RunContext{RunID: "sync_test_00000000", Command: "sync all", Invocation: "cli", Sink: sink}

	if err := ctx.Emit(Event{"event": "categorize.source.completed", "status": "ok"}.WithSubject("source", "src:test", true)); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events := readMetricEvents(t, path)
	if got := events[0]["subject_key"]; got != "src:test" {
		t.Fatalf("subject_key = %v, want src:test", got)
	}
}

func TestSinkConcurrentEmitsWriteValidJSONLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := RunContext{RunID: "sync_test_00000000", Command: "sync all", Invocation: "cli", Sink: sink}

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ctx.Emit(Event{"event": "sync.stage.completed", "stage": "categorize", "status": "ok"}); err != nil {
				t.Errorf("Emit: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readMetricEvents(t, path)
	if len(events) != 25 {
		t.Fatalf("events = %d, want 25", len(events))
	}
}

func TestStrictSinkReturnsWriteFailureOnClose(t *testing.T) {
	writeErr := errors.New("disk full")
	sink := &jsonlSink{
		cfg: Config{Enabled: true, Path: "metrics.jsonl", Detail: DetailStage, Strict: true},
		w:   failingWriter{err: writeErr},
		c:   noopCloser{},
	}

	if err := sink.Emit(Event{"event": "sync.run.started"}); !errors.Is(err, writeErr) {
		t.Fatalf("Emit error = %v, want %v", err, writeErr)
	}
	if err := sink.Close(); !errors.Is(err, writeErr) {
		t.Fatalf("Close error = %v, want %v", err, writeErr)
	}
}

func writeMetricsConfig(t *testing.T, root string, body string) {
	t.Helper()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimeenv.RegisterConfigFile(root, path)
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

func readMetricEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open metrics: %v", err)
	}
	defer func() {
		_ = file.Close()
	}()
	var events []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("parse metric line %q: %v", scanner.Text(), err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan metrics: %v", err)
	}
	return events
}
