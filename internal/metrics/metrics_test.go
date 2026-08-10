package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	if cfg.RotateMaxBytes != DefaultRotateMaxBytes || cfg.RotateKeepFiles != DefaultRotateKeepFiles {
		t.Fatalf("rotation defaults = (%d, %d), want (%d, %d)", cfg.RotateMaxBytes, cfg.RotateKeepFiles, DefaultRotateMaxBytes, DefaultRotateKeepFiles)
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

func TestResolveConfigRotationOverridesAndExplicitDisable(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	writeMetricsConfig(t, root, `metrics:
  enabled: true
  rotate_max_bytes: 4096
  rotate_keep_files: 7
`)

	cfg, err := ResolveConfig(root, logDir)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.RotateMaxBytes != 4096 || cfg.RotateKeepFiles != 7 {
		t.Fatalf("rotation config = (%d, %d), want (4096, 7)", cfg.RotateMaxBytes, cfg.RotateKeepFiles)
	}

	t.Setenv("DBRAIN_METRICS_ROTATE_MAX_BYTES", "0")
	t.Setenv("DBRAIN_METRICS_ROTATE_KEEP_FILES", "0")
	cfg, err = ResolveConfig(root, logDir)
	if err != nil {
		t.Fatalf("ResolveConfig with env override: %v", err)
	}
	if cfg.RotateMaxBytes != 0 || cfg.RotateKeepFiles != 0 {
		t.Fatalf("explicit rotation disable = (%d, %d), want (0, 0)", cfg.RotateMaxBytes, cfg.RotateKeepFiles)
	}
}

func TestResolveConfigRejectsInvalidRotationValues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "negative bytes", body: "metrics:\n  rotate_max_bytes: -1\n", want: "metrics.rotate_max_bytes"},
		{name: "overflow bytes", body: "metrics:\n  rotate_max_bytes: \"9223372036854775808\"\n", want: "metrics.rotate_max_bytes"},
		{name: "negative backups", body: "metrics:\n  rotate_keep_files: -1\n", want: "metrics.rotate_keep_files"},
		{name: "too many backups", body: "metrics:\n  rotate_keep_files: 129\n", want: "metrics.rotate_keep_files"},
		{name: "overflow backups", body: "metrics:\n  rotate_keep_files: \"9223372036854775808\"\n", want: "metrics.rotate_keep_files"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeMetricsConfig(t, root, test.body)
			_, err := ResolveConfig(root, filepath.Join(root, "logs"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveConfig error = %v, want %q", err, test.want)
			}
		})
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

func TestSinksSharingPathSerializeWithoutLostOrPartialLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	cfg := Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: 1 << 20, RotateKeepFiles: 2}
	first, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open first: %v", err)
	}
	second, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}
	ctx1 := RunContext{RunID: "writer-one", Command: "test", Sink: first}
	ctx2 := RunContext{RunID: "writer-two", Command: "test", Sink: second}

	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			ctx := ctx1
			if index%2 == 1 {
				ctx = ctx2
			}
			if err := ctx.Emit(Event{"event": "test.metrics.line", "id": index}); err != nil {
				t.Errorf("Emit %d: %v", index, err)
			}
		}(index)
	}
	wg.Wait()
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close second: %v", err)
	}

	events := readMetricEvents(t, path)
	if len(events) != 100 {
		t.Fatalf("events = %d, want 100", len(events))
	}
	seen := make(map[int]bool, len(events))
	for _, event := range events {
		id, ok := event["id"].(float64)
		if !ok || id < 0 || id >= 100 || id != float64(int(id)) || seen[int(id)] {
			t.Fatalf("duplicate, invalid, or partial event: %#v", event)
		}
		seen[int(id)] = true
	}
}

func TestSinkRotatesBeforeAppendAndRetainsCanonicalBackups(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	const maxBytes = int64(260)
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: maxBytes, RotateKeepFiles: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := RunContext{RunID: "rotation-test", Command: "test", Sink: sink}
	for index := 0; index < 5; index++ {
		if err := ctx.Emit(Event{"event": "test.metrics.line", "id": index, "payload": strings.Repeat("x", 24)}); err != nil {
			t.Fatalf("Emit %d: %v", index, err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, suffix := range []string{"", ".1", ".2"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("stat backup %q: %v", suffix, err)
		}
		if !info.Mode().IsRegular() || info.Size() > maxBytes {
			t.Fatalf("backup %q info = %+v, want regular file at most %d bytes", suffix, info, maxBytes)
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".3 stat error = %v, want absent", err)
	}
	if got := readMetricEvents(t, path); len(got) != 1 {
		t.Fatalf("active events = %d, want one newest event", len(got))
	}
	if got := readMetricEvents(t, path+".1"); len(got) != 1 {
		t.Fatalf(".1 events = %d, want one event", len(got))
	}
	if got := readMetricEvents(t, path+".2"); len(got) != 1 {
		t.Fatalf(".2 events = %d, want one event", len(got))
	}
}

func TestOpenRepairsOversizedActiveFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	original := []byte(strings.Repeat("oversized\n", 20))
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: 16, RotateKeepFiles: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || string(backup) != string(original) {
		t.Fatalf("repaired files: active=%d bytes backup=%d bytes", len(active), len(backup))
	}
}

func TestSinkKeepsSingleOversizedEventIntact(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: 64, RotateKeepFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := RunContext{RunID: "oversized", Sink: sink}
	if err := ctx.Emit(Event{"event": "test.metrics.line", "id": 1, "payload": strings.Repeat("z", 512)}); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Emit(Event{"event": "test.metrics.line", "id": 2}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	backup := readMetricEvents(t, path+".1")
	active := readMetricEvents(t, path)
	if len(backup) != 1 || backup[0]["id"] != float64(1) || len(active) != 1 || active[0]["id"] != float64(2) {
		t.Fatalf("oversized rotation = backup:%#v active:%#v", backup, active)
	}
	data, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), strings.Repeat("z", 512)) {
		t.Fatal("oversized event was truncated")
	}
}

func TestRotationNeverFollowsSiblingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are not portable on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "metrics.jsonl")
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path+".1"); err != nil {
		t.Fatal(err)
	}
	sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: 64, RotateKeepFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := RunContext{RunID: "symlink", Sink: sink}
	if err := ctx.Emit(Event{"event": "test.metrics.line", "id": 1, "payload": strings.Repeat("x", 100)}); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Emit(Event{"event": "test.metrics.line", "id": 2}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "sentinel\n" {
		t.Fatalf("symlink target changed: %q, %v", got, err)
	}
	if info, err := os.Lstat(path + ".1"); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("rotation destination was not preserved as symlink: %v", err)
	}
}

func TestRotationZeroValuesPreserveExplicitDisableSemantics(t *testing.T) {
	t.Run("max zero disables rotation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "metrics.jsonl")
		sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: 0, RotateKeepFiles: 0})
		if err != nil {
			t.Fatal(err)
		}
		ctx := RunContext{RunID: "zero-max", Sink: sink}
		for index := 0; index < 3; index++ {
			if err := ctx.Emit(Event{"event": "test.metrics.line", "id": index}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}
		if got := len(readMetricEvents(t, path)); got != 3 {
			t.Fatalf("events = %d, want 3", got)
		}
		if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup stat error = %v, want absent", err)
		}
	})

	t.Run("keep zero removes backups", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "metrics.jsonl")
		sink, err := Open(Config{Enabled: true, Path: path, Detail: DetailStage, RotateMaxBytes: 100, RotateKeepFiles: 0})
		if err != nil {
			t.Fatal(err)
		}
		ctx := RunContext{RunID: "zero-keep", Sink: sink}
		for index := 0; index < 3; index++ {
			if err := ctx.Emit(Event{"event": "test.metrics.line", "id": index, "payload": strings.Repeat("x", 24)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup stat error = %v, want absent", err)
		}
		if got := len(readMetricEvents(t, path)); got != 1 {
			t.Fatalf("active events = %d, want newest event", got)
		}
	})
}

func TestAuditRunCompletedEventUsesPrivacySafeAllowlist(t *testing.T) {
	event := AuditRunCompletedEvent("standard", "fail", 1500*time.Millisecond, AuditStatusCounts{Pass: 10, Warn: 2, Fail: 1, Unknown: 3, Skipped: 4})
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]bool{"event": true, "profile": true, "status": true, "duration_ms": true, "pass_count": true, "warn_count": true, "fail_count": true, "unknown_count": true, "skipped_count": true}
	if len(event) != len(wantKeys) {
		t.Fatalf("event keys = %#v", event)
	}
	for key := range event {
		if !wantKeys[key] {
			t.Fatalf("unexpected audit metric key %q", key)
		}
	}
	for _, forbidden := range []string{"audit_id", "check", "evidence", "identifier", "path", "url", "credential", "provider", "error", "transcript", "ocr"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("audit metric contains %q: %s", forbidden, data)
		}
	}
}

func TestNotificationDeliveryCompletedEventUsesClosedContentFreeFields(t *testing.T) {
	event := NotificationDeliveryCompletedEvent("slack", "failure", "sync.store.open.failed", "accepted", 1500*time.Millisecond)
	want := Event{
		"event": "notification.delivery.completed", "provider": "slack", "kind": "failure",
		"failure_type": "sync.store.open.failed", "status": "accepted", "duration_ms": int64(1500),
	}
	if !reflect.DeepEqual(event, want) {
		t.Fatalf("event = %#v, want %#v", event, want)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"body", "destination", "relay", "channel", "external_id", "error", "secret", "token", "url"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("notification metric contains %q: %s", forbidden, data)
		}
	}
}

func TestNotificationDeliveryCompletedEventRejectsValuesOutsideClosedAllowlists(t *testing.T) {
	event := NotificationDeliveryCompletedEvent(
		"https://relay.example/token", "raw-body", "private/path", "error: secret", time.Second,
	)
	if event["provider"] != "unknown" || event["kind"] != "unknown" || event["failure_type"] != "sync.unknown" || event["status"] != "unknown" {
		t.Fatalf("untrusted metric values survived: %#v", event)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"relay.example", "token", "raw-body", "private/path", "secret"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("notification metric leaked %q: %s", forbidden, data)
		}
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

func TestSQLiteArchiveEventsContainOnlyTimingStatusAndCounts(t *testing.T) {
	events := []Event{
		SQLiteArchiveAttemptEvent(),
		SQLiteArchiveStartedEvent(),
		SQLiteArchiveCompletedEvent(1500*time.Millisecond, 1024, 512),
		SQLiteArchiveFailedEvent(2*time.Second, SQLiteArchiveFailureArchive),
		SQLiteArchiveLockSkippedEvent(),
		SQLiteArchiveOverlapSkippedEvent(),
		SQLiteArchiveIntervalSkippedEvent(3 * time.Minute),
	}
	wantNames := []string{
		"scheduler.sqlite_archive.attempt",
		"scheduler.sqlite_archive.started",
		"scheduler.sqlite_archive.completed",
		"scheduler.sqlite_archive.failed",
		"scheduler.sqlite_archive.lock_skipped",
		"scheduler.sqlite_archive.overlap_skipped",
		"scheduler.sqlite_archive.interval_skipped",
	}
	for i, event := range events {
		if event["event"] != wantNames[i] {
			t.Fatalf("event %d name = %#v, want %q", i, event["event"], wantNames[i])
		}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(data))
		for _, forbidden := range []string{"key", "path", "credential", "token", "secret", "endpoint", "url", "message", "error"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("event %d contains forbidden field/content %q: %s", i, forbidden, data)
			}
		}
	}
	completed := events[2]
	if completed["duration_ms"] != int64(1500) || completed["snapshot_bytes"] != int64(1024) || completed["archive_bytes"] != int64(512) {
		t.Fatalf("completed aggregate fields = %#v", completed)
	}
}

func TestSQLiteArchiveFailedEventRejectsArbitraryFailureCode(t *testing.T) {
	event := SQLiteArchiveFailedEvent(time.Second, SQLiteArchiveFailureCode("token=secret path=/tmp/db"))
	if got := event["failure_code"]; got != string(SQLiteArchiveFailureArchive) {
		t.Fatalf("failure_code = %#v, want closed archive_failed fallback", got)
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
