package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/runlock"
)

const metricsHelperEnv = "DBRAIN_METRICS_HELPER"

func TestMetricsHelperProcess(t *testing.T) {
	switch os.Getenv(metricsHelperEnv) {
	case "writer":
		if err := runMetricsWriterHelper(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	case "reader":
		if err := runMetricsReaderHelper(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func TestIndependentProcessWritersPreserveEveryCompleteLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	const writers = 4
	const eventsPerWriter = 40
	commands := make([]*exec.Cmd, 0, writers)
	for writer := 0; writer < writers; writer++ {
		cmd := metricsHelperCommand(t, map[string]string{
			metricsHelperEnv:                   "writer",
			"DBRAIN_METRICS_PATH":              path,
			"DBRAIN_METRICS_ROTATE_MAX_BYTES":  "900",
			"DBRAIN_METRICS_ROTATE_KEEP_FILES": "128",
			"DBRAIN_METRICS_WRITER_START":      strconv.Itoa(writer * eventsPerWriter),
			"DBRAIN_METRICS_WRITER_COUNT":      strconv.Itoa(eventsPerWriter),
		})
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper: %v", err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer helper: %v", err)
		}
	}

	lines := readAllRotatedMetricLines(t, path)
	if len(lines) != writers*eventsPerWriter {
		t.Fatalf("complete lines = %d, want %d", len(lines), writers*eventsPerWriter)
	}
	seen := make(map[int]bool, len(lines))
	for _, line := range lines {
		id, ok := line["id"].(float64)
		if !ok || id != float64(int(id)) || id < 0 || int(id) >= writers*eventsPerWriter {
			t.Fatalf("invalid writer event: %#v", line)
		}
		if seen[int(id)] {
			t.Fatalf("duplicate writer event %d", int(id))
		}
		seen[int(id)] = true
	}
}

func TestReaderLockExcludesCrossProcessRolloverWithoutSkipOrDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	const initialEvents = 12
	events := make([]map[string]any, 0, initialEvents)
	for index := 0; index < initialEvents; index++ {
		events = append(events, completedRunEvent(fmt.Sprintf("before-%02d", index), base.Add(time.Duration(index)*time.Second)))
	}
	writeMetricEvents(t, path, events)

	readerReady := filepath.Join(dir, "reader.ready")
	readerRelease := filepath.Join(dir, "reader.release")
	readerResult := filepath.Join(dir, "reader.result")
	readerCmd := metricsHelperCommand(t, map[string]string{
		metricsHelperEnv:                   "reader",
		"DBRAIN_METRICS_PATH":              path,
		"DBRAIN_METRICS_READER_READY":      readerReady,
		"DBRAIN_METRICS_READER_RELEASE":    readerRelease,
		"DBRAIN_METRICS_READER_RESULT":     readerResult,
		"DBRAIN_METRICS_READER_MAX_BYTES":  "1048576",
		"DBRAIN_METRICS_READER_MAX_EVENTS": "1000",
		"DBRAIN_METRICS_READER_START_UNIX": strconv.FormatInt(base.Add(-time.Minute).Unix(), 10),
	})
	if err := readerCmd.Start(); err != nil {
		t.Fatalf("start reader helper: %v", err)
	}
	waitForPath(t, readerReady, 5*time.Second)

	writerReady := filepath.Join(dir, "writer.ready")
	writerRelease := filepath.Join(dir, "writer.release")
	writerStarted := filepath.Join(dir, "writer.started")
	writerCmd := metricsHelperCommand(t, map[string]string{
		metricsHelperEnv:                   "writer",
		"DBRAIN_METRICS_PATH":              path,
		"DBRAIN_METRICS_ROTATE_MAX_BYTES":  "64",
		"DBRAIN_METRICS_ROTATE_KEEP_FILES": "2",
		"DBRAIN_METRICS_WRITER_START":      "1000",
		"DBRAIN_METRICS_WRITER_COUNT":      "1",
		"DBRAIN_METRICS_WRITER_STARTED":    writerStarted,
		"DBRAIN_METRICS_WRITER_READY":      writerReady,
		"DBRAIN_METRICS_WRITER_RELEASE":    writerRelease,
	})
	if err := writerCmd.Start(); err != nil {
		t.Fatalf("start writer helper: %v", err)
	}
	waitForPath(t, writerStarted, 5*time.Second)
	if pathExistsWithin(writerReady, 150*time.Millisecond) {
		t.Fatal("writer acquired exclusive lock while reader held shared lock")
	}
	if err := os.WriteFile(readerRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForPath(t, writerReady, 5*time.Second)
	if err := os.WriteFile(writerRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := readerCmd.Wait(); err != nil {
		t.Fatalf("reader helper: %v", err)
	}
	if err := writerCmd.Wait(); err != nil {
		t.Fatalf("writer helper: %v", err)
	}
	var result struct {
		Runs       []string `json:"runs"`
		EventsRead int      `json:"events_read"`
	}
	data, err := os.ReadFile(readerResult)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode reader result: %v", err)
	}
	if result.EventsRead != initialEvents || len(result.Runs) != initialEvents {
		t.Fatalf("reader snapshot = events:%d runs:%d, want %d/%d", result.EventsRead, len(result.Runs), initialEvents, initialEvents)
	}
	seen := make(map[string]bool, len(result.Runs))
	for _, id := range result.Runs {
		if seen[id] || !strings.HasPrefix(id, "before-") {
			t.Fatalf("reader result contains duplicate or unexpected run %q", id)
		}
		seen[id] = true
	}
	for index := 0; index < initialEvents; index++ {
		id := fmt.Sprintf("before-%02d", index)
		if !seen[id] {
			t.Fatalf("reader result skipped run %q", id)
		}
	}

	all := readAllRotatedMetricLines(t, path)
	if len(all) != initialEvents+1 {
		t.Fatalf("post-rollover lines = %d, want %d", len(all), initialEvents+1)
	}
	postIDs := make(map[string]int, initialEvents)
	writerLines := 0
	for _, line := range all {
		if id, ok := line["run_id"].(string); ok && strings.HasPrefix(id, "before-") {
			postIDs[id]++
		}
		if id, ok := line["id"].(float64); ok && id == float64(1000) {
			writerLines++
		}
	}
	if writerLines != 1 {
		t.Fatalf("post-rollover writer lines = %d, want 1", writerLines)
	}
	for index := 0; index < initialEvents; index++ {
		id := fmt.Sprintf("before-%02d", index)
		if postIDs[id] != 1 {
			t.Fatalf("post-rollover run %q count = %d, want 1", id, postIDs[id])
		}
	}
}

func metricsHelperCommand(t *testing.T, values map[string]string) *exec.Cmd {
	t.Helper()
	env := append([]string(nil), os.Environ()...)
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMetricsHelperProcess$")
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd
}

func runMetricsWriterHelper() error {
	path := os.Getenv("DBRAIN_METRICS_PATH")
	maxBytes, err := strconv.ParseInt(os.Getenv("DBRAIN_METRICS_ROTATE_MAX_BYTES"), 10, 64)
	if err != nil {
		return fmt.Errorf("parse helper max bytes: %w", err)
	}
	keepFiles, err := strconv.Atoi(os.Getenv("DBRAIN_METRICS_ROTATE_KEEP_FILES"))
	if err != nil {
		return fmt.Errorf("parse helper keep files: %w", err)
	}
	if started := os.Getenv("DBRAIN_METRICS_WRITER_STARTED"); started != "" {
		if err := touchBarrier(started); err != nil {
			return err
		}
	}
	installMetricsLockBarrier(runlock.Exclusive, os.Getenv("DBRAIN_METRICS_WRITER_READY"), os.Getenv("DBRAIN_METRICS_WRITER_RELEASE"))
	defer clearMetricsLockBarrier()
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: maxBytes, RotateKeepFiles: keepFiles})
	if err != nil {
		return fmt.Errorf("helper Open: %w", err)
	}
	defer func() { _ = sink.Close() }()
	start, err := strconv.Atoi(os.Getenv("DBRAIN_METRICS_WRITER_START"))
	if err != nil {
		return fmt.Errorf("parse helper start: %w", err)
	}
	count, err := strconv.Atoi(os.Getenv("DBRAIN_METRICS_WRITER_COUNT"))
	if err != nil {
		return fmt.Errorf("parse helper count: %w", err)
	}
	ctx := RunContext{RunID: "process-writer", Command: "test", Sink: sink}
	for index := 0; index < count; index++ {
		if err := ctx.Emit(Event{"event": "test.metrics.line", "id": start + index, "payload": strings.Repeat("p", 16)}); err != nil {
			return fmt.Errorf("helper Emit %d: %w", start+index, err)
		}
	}
	return nil
}

func runMetricsReaderHelper() error {
	installMetricsLockBarrier(runlock.Shared, os.Getenv("DBRAIN_METRICS_READER_READY"), os.Getenv("DBRAIN_METRICS_READER_RELEASE"))
	defer clearMetricsLockBarrier()
	startUnix, err := strconv.ParseInt(os.Getenv("DBRAIN_METRICS_READER_START_UNIX"), 10, 64)
	if err != nil {
		return fmt.Errorf("parse helper reader start: %w", err)
	}
	maxBytes, err := strconv.ParseInt(os.Getenv("DBRAIN_METRICS_READER_MAX_BYTES"), 10, 64)
	if err != nil {
		return fmt.Errorf("parse helper reader bytes: %w", err)
	}
	maxEvents, err := strconv.Atoi(os.Getenv("DBRAIN_METRICS_READER_MAX_EVENTS"))
	if err != nil {
		return fmt.Errorf("parse helper reader events: %w", err)
	}
	reader := NewReader(os.Getenv("DBRAIN_METRICS_PATH"))
	reader.MaxBytes = maxBytes
	reader.MaxEvents = maxEvents
	window, err := reader.Read(context.Background(), time.Unix(startUnix, 0).UTC())
	if err != nil {
		return fmt.Errorf("helper Read: %w", err)
	}
	runs := make([]string, 0, len(window.Runs))
	for _, run := range window.Runs {
		runs = append(runs, run.ID)
	}
	data, err := json.Marshal(struct {
		Runs       []string `json:"runs"`
		EventsRead int      `json:"events_read"`
	}{Runs: runs, EventsRead: window.EventsRead})
	if err != nil {
		return err
	}
	return os.WriteFile(os.Getenv("DBRAIN_METRICS_READER_RESULT"), data, 0o600)
}

func installMetricsLockBarrier(mode runlock.Mode, readyPath, releasePath string) {
	var once sync.Once
	metricsLockHook.Lock()
	metricsLockHook.fn = func(acquiredMode runlock.Mode) {
		if acquiredMode != mode || readyPath == "" || releasePath == "" {
			return
		}
		once.Do(func() {
			_ = touchBarrier(readyPath)
			deadline := time.Now().Add(30 * time.Second)
			for !pathExists(releasePath) && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	metricsLockHook.Unlock()
}

func clearMetricsLockBarrier() {
	metricsLockHook.Lock()
	metricsLockHook.fn = nil
	metricsLockHook.Unlock()
}

func touchBarrier(path string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte("ready\n"), 0o600)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func pathExistsWithin(path string, duration time.Duration) bool {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if pathExists(path) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return pathExists(path)
}

func waitForPath(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !pathExists(path) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !pathExists(path) {
		t.Fatalf("timed out waiting for %s", path)
	}
}

func readAllRotatedMetricLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	lines := make([]map[string]any, 0)
	paths, err := discoverMetricPaths(path)
	if err != nil {
		t.Fatalf("discover metrics paths: %v", err)
	}
	for _, candidate := range paths {
		data, err := os.ReadFile(candidate)
		if err != nil {
			t.Fatalf("read metrics path %s: %v", candidate, err)
		}
		for _, raw := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if raw == "" {
				continue
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(raw), &line); err != nil {
				t.Fatalf("parse complete metric line %q: %v", raw, err)
			}
			lines = append(lines, line)
		}
	}
	return lines
}
