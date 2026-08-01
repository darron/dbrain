package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/sqlitearchive"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
)

func TestSchedulerSQLiteArchiveConfigDefaults(t *testing.T) {
	got, err := schedulerSQLiteArchiveConfigFromRuntime(t.TempDir())
	if err != nil {
		t.Fatalf("schedulerSQLiteArchiveConfigFromRuntime: %v", err)
	}
	if got.Enabled {
		t.Fatal("scheduled SQLite archives must be opt-in")
	}
	if got.Interval != 24*time.Hour {
		t.Fatalf("interval = %s, want 24h", got.Interval)
	}
	if !got.RunOnStart {
		t.Fatal("run_on_start = false, want true")
	}
}

type schedulerTestWriter struct{}

func (schedulerTestWriter) PutObject(context.Context, string, io.Reader, string, int64) (string, error) {
	return "test-etag", nil
}

type blockingSchedulerWriter struct {
	started chan struct{}
	release chan struct{}
}

func (w blockingSchedulerWriter) PutObject(ctx context.Context, _ string, _ io.Reader, _ string, _ int64) (string, error) {
	close(w.started)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-w.release:
		return "etag", nil
	}
}

func TestSQLiteArchiveSchedulerRunOnStartAndAtMostOncePerInterval(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	ticks := make(chan struct{}, 2)
	waits := make(chan time.Duration, 3)
	started := make(chan struct{}, 3)
	var calls atomic.Int32
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{
		Enabled: true, Interval: 24 * time.Hour, RunOnStart: true,
	}, schedulerTestWriter{}, io.Discard)
	clockNow := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clockNow }
	s.wait = func(ctx context.Context, duration time.Duration) bool {
		waits <- duration
		select {
		case <-ctx.Done():
			return false
		case <-ticks:
			return true
		}
	}
	s.archive = func(_ context.Context, _ config.Config, opts sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		if opts.Writer == nil {
			t.Error("scheduler archive did not receive writer")
		}
		calls.Add(1)
		started <- struct{}{}
		return sqlitearchive.ArchiveResult{SnapshotSize: 12, ArchiveSize: 7}, nil
	}

	s.Start(t.Context())
	defer s.Stop()
	awaitSignal(t, started, "run-on-start archive")
	if got := calls.Load(); got != 1 {
		t.Fatalf("startup archive calls = %d, want 1", got)
	}
	if got := awaitDuration(t, waits, "first interval wait"); got != 24*time.Hour {
		t.Fatalf("first wait duration = %s, want 24h", got)
	}

	clockNow = clockNow.Add(24 * time.Hour)
	ticks <- struct{}{}
	awaitSignal(t, started, "interval archive")
	if got := calls.Load(); got != 2 {
		t.Fatalf("archive calls after one interval = %d, want 2", got)
	}
	if got := awaitDuration(t, waits, "second interval wait"); got != 24*time.Hour {
		t.Fatalf("second wait duration = %s, want 24h", got)
	}
	if got := s.Status().NextRunAt; !got.Equal(clockNow.Add(24 * time.Hour)) {
		t.Fatalf("next run = %s, want %s", got, clockNow.Add(24*time.Hour))
	}
	if got := calls.Load(); got > 2 {
		t.Fatalf("more than one archive was created per interval: %d", got)
	}
}

func TestSQLiteArchiveSchedulerPersistsIntervalAcrossRestarts(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	newScheduler := func() *sqliteArchiveScheduler {
		s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: 24 * time.Hour, RunOnStart: true}, schedulerTestWriter{}, io.Discard)
		s.now = func() time.Time { return now }
		s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
			calls.Add(1)
			return sqlitearchive.ArchiveResult{}, nil
		}
		return s
	}

	first := newScheduler()
	first.run(t.Context(), "startup")
	stateInfo, err := os.Stat(scheduledSQLiteArchiveAttemptPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if got := stateInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("scheduler state mode = %o, want 600", got)
	}
	second := newScheduler()
	second.run(t.Context(), "startup")
	if got := calls.Load(); got != 1 {
		t.Fatalf("archives inside one durable interval = %d, want 1", got)
	}
	if got := second.Status(); got.LastStatus != "skipped" || got.LastError != "interval_not_elapsed" {
		t.Fatalf("restart interval status = %+v", got)
	}

	now = now.Add(24 * time.Hour)
	second.run(t.Context(), "interval")
	if got := calls.Load(); got != 2 {
		t.Fatalf("archives after interval elapsed = %d, want 2", got)
	}
}

func TestSQLiteArchiveSchedulerStateRejectsParentSymlink(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	outside := t.TempDir()
	outsideState := filepath.Join(outside, "sqlite-archive-last-attempt")
	if err := os.WriteFile(outsideState, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cfg.DataDir, "scheduler")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readScheduledSQLiteArchiveAttempt(cfg); err == nil {
		t.Fatal("read followed scheduler parent symlink")
	}
	if err := writeScheduledSQLiteArchiveAttempt(cfg, time.Now()); err == nil {
		t.Fatal("write followed scheduler parent symlink")
	}
	data, err := os.ReadFile(outsideState)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside\n" {
		t.Fatalf("outside state changed to %q", data)
	}
}

func TestSQLiteArchiveSchedulerStateRejectsLeafSymlink(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	dir := filepath.Join(cfg.DataDir, "scheduler")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-state")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, scheduledSQLiteArchiveAttemptPath(cfg)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readScheduledSQLiteArchiveAttempt(cfg); err == nil {
		t.Fatal("read followed scheduler state symlink")
	}
	if err := writeScheduledSQLiteArchiveAttempt(cfg, time.Now()); err == nil {
		t.Fatal("write replaced scheduler state symlink instead of failing closed")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside\n" {
		t.Fatalf("outside state changed to %q", data)
	}
}

func TestSQLiteArchiveSchedulerFailedAttemptStillRateLimitsRestart(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	newScheduler := func() *sqliteArchiveScheduler {
		s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: 24 * time.Hour}, schedulerTestWriter{}, io.Discard)
		s.now = func() time.Time { return now }
		s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
			calls.Add(1)
			return sqlitearchive.ArchiveResult{}, fmt.Errorf("synthetic failure")
		}
		return s
	}
	first := newScheduler()
	first.run(t.Context(), "startup")
	second := newScheduler()
	second.run(t.Context(), "startup")
	if got := calls.Load(); got != 1 {
		t.Fatalf("failed attempts inside one durable interval = %d, want 1", got)
	}
	if got := second.Status(); got.LastStatus != "skipped" || got.LastError != "interval_not_elapsed" {
		t.Fatalf("failed-attempt restart status = %+v", got)
	}
}

func TestSQLiteArchiveSchedulerBacksOffWhenOverdueLeaseIsHeld(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	if err := writeScheduledSQLiteArchiveAttempt(cfg, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	lease, err := sqlitearchive.AcquireOperationLease(cfg, "test:held")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()

	var calls atomic.Int32
	var waited time.Duration
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{
		Enabled: true, Interval: time.Hour, RunOnStart: true,
	}, schedulerTestWriter{}, io.Discard)
	s.now = func() time.Time { return now }
	s.wait = func(_ context.Context, duration time.Duration) bool {
		waited = duration
		return false
	}
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		calls.Add(1)
		return sqlitearchive.ArchiveResult{}, nil
	}

	s.loop(t.Context())
	if calls.Load() != 0 {
		t.Fatal("archive ran while operation lease was held")
	}
	if waited != 5*time.Minute {
		t.Fatalf("lock-contention retry delay = %s, want 5m", waited)
	}
}

func TestSQLiteArchiveSchedulerPreflightFailuresApplyRetryFloor(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	overdue := now.Add(-2 * time.Hour)
	tests := []struct {
		name       string
		wantStatus string
		setup      func(*sqliteArchiveScheduler)
	}{
		{
			name:       "lock I/O failure with no marker",
			wantStatus: "lock_failed",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) { return time.Time{}, nil }
				s.acquireLease = func(config.Config, string) (*sqlitearchive.OperationLease, error) {
					return nil, fmt.Errorf("synthetic lock I/O failure")
				}
			},
		},
		{
			name:       "lock I/O failure with overdue marker",
			wantStatus: "lock_failed",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) { return overdue, nil }
				s.acquireLease = func(config.Config, string) (*sqlitearchive.OperationLease, error) {
					return nil, fmt.Errorf("synthetic lock I/O failure")
				}
			},
		},
		{
			name:       "state read failure",
			wantStatus: "state_failed",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) {
					return time.Time{}, fmt.Errorf("synthetic state read failure")
				}
			},
		},
		{
			name:       "marker write failure",
			wantStatus: "state_failed",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) { return overdue, nil }
				s.writeAttempt = func(config.Config, time.Time) error {
					return fmt.Errorf("synthetic marker write failure")
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := openSchedulerTestConfig(t)
			s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{
				Enabled: true, Interval: time.Hour,
			}, schedulerTestWriter{}, io.Discard)
			s.now = func() time.Time { return now }
			test.setup(s)

			s.run(t.Context(), "interval")
			if got := s.Status().LastError; got != test.wantStatus {
				t.Fatalf("status code = %q, want %q", got, test.wantStatus)
			}
			if got := s.nextDelay(); got != 5*time.Minute {
				t.Fatalf("retry delay = %s, want 5m", got)
			}
		})
	}
}

func TestSQLiteArchiveSchedulerRetryFloorDoesNotShortenRecentMarkerDelay(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	cfg := openSchedulerTestConfig(t)
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{
		Enabled: true, Interval: time.Hour,
	}, schedulerTestWriter{}, io.Discard)
	s.now = func() time.Time { return now }
	s.readAttempt = func(config.Config) (time.Time, error) { return now.Add(-10 * time.Minute), nil }
	s.deferPreflightRetry()

	if got := s.nextDelay(); got != 50*time.Minute {
		t.Fatalf("retry delay = %s, want recent marker's remaining 50m interval", got)
	}
}

func TestSQLiteArchiveSchedulerRunOnStartFalseWaitsFullIntervalWhenStateIsOverdue(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	if err := writeScheduledSQLiteArchiveAttempt(cfg, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	var waited time.Duration
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{
		Enabled: true, Interval: 24 * time.Hour, RunOnStart: false,
	}, schedulerTestWriter{}, io.Discard)
	s.now = func() time.Time { return now }
	s.wait = func(_ context.Context, duration time.Duration) bool {
		waited = duration
		return false
	}
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		calls.Add(1)
		return sqlitearchive.ArchiveResult{}, nil
	}

	s.loop(t.Context())
	if calls.Load() != 0 {
		t.Fatalf("archive calls before first configured interval = %d, want 0", calls.Load())
	}
	if waited != 24*time.Hour {
		t.Fatalf("first wait = %s, want 24h", waited)
	}
}

func TestSQLiteArchiveSchedulerDoesNotOverlapItself(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	if err := os.WriteFile(cfg.ConfigPath, []byte("metrics:\n  enabled: true\n  path: sqlite-archive-overlap.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, io.Discard)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.archive = func(ctx context.Context, _ config.Config, _ sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-ctx.Done():
			return sqlitearchive.ArchiveResult{}, ctx.Err()
		case <-release:
			return sqlitearchive.ArchiveResult{}, nil
		}
	}

	firstDone := make(chan struct{})
	go func() {
		s.run(t.Context(), "first")
		close(firstDone)
	}()
	awaitSignal(t, entered, "first archive")
	s.run(t.Context(), "overlap")
	close(release)
	awaitSignal(t, firstDone, "first archive completion")
	if got := calls.Load(); got != 1 {
		t.Fatalf("overlapping archive calls = %d, want 1", got)
	}
	if got := s.Status(); got.LastStatus != "ok" {
		t.Fatalf("final archive status = %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(cfg.LogDir, "sqlite-archive-overlap.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "scheduler.sqlite_archive.overlap_skipped") {
		t.Fatalf("missing overlap metric: %s", data)
	}
	if strings.Contains(string(data), "scheduler.sqlite_archive.lock_skipped") {
		t.Fatalf("in-process overlap was mislabeled as lock-skip: %s", data)
	}
}

func TestSQLiteArchiveSchedulerSerializesWithArchiveAndRestore(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	metricsPath := filepath.Join(cfg.LogDir, "sqlite-archive-lock-skip.jsonl")
	if err := os.WriteFile(cfg.ConfigPath, []byte("metrics:\n  enabled: true\n  path: sqlite-archive-lock-skip.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := sqlitearchive.AcquireOperationLease(cfg, "restore:test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()

	var calls atomic.Int32
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, io.Discard)
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		calls.Add(1)
		return sqlitearchive.ArchiveResult{}, nil
	}
	s.run(t.Context(), "locked")
	if calls.Load() != 0 {
		t.Fatal("archive ran while shared archive/restore lock was held")
	}
	if got := s.Status(); got.LastStatus != "skipped" {
		t.Fatalf("locked status = %+v", got)
	} else if got.LastError != "lock_held" || strings.Contains(got.LastError, cfg.DataDir) {
		t.Fatalf("lock status was not sanitized: %+v", got)
	}
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "scheduler.sqlite_archive.lock_skipped") || strings.Contains(string(data), cfg.DataDir) {
		t.Fatalf("unexpected lock-skip metrics: %s", data)
	}
}

func TestSQLiteArchiveSchedulerLockSkipsWhileRealArchiveRuns(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	storefixture.PrepareCurrent(t, cfg.DBPath)
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	blocking := blockingSchedulerWriter{started: make(chan struct{}), release: make(chan struct{})}
	archiveDone := make(chan error, 1)
	go func() {
		_, err := sqlitearchive.Archive(t.Context(), cfg, sqlitearchive.Options{Writer: blocking})
		archiveDone <- err
	}()
	awaitSignal(t, blocking.started, "real SQLite archive upload")

	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, io.Discard)
	s.run(t.Context(), "interval")
	if got := s.Status(); got.LastStatus != "skipped" {
		t.Fatalf("scheduler did not lock-skip during real archive: %+v", got)
	}

	close(blocking.release)
	select {
	case err := <-archiveDone:
		if err != nil {
			t.Fatalf("real archive: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for real archive completion")
	}
}

func TestSQLiteArchiveSchedulerStopCancelsAndWaitsForArchive(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	started := make(chan struct{})
	canceled := make(chan struct{})
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{
		Enabled: true, Interval: time.Hour, RunOnStart: true,
	}, schedulerTestWriter{}, io.Discard)
	s.archive = func(ctx context.Context, _ config.Config, _ sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return sqlitearchive.ArchiveResult{}, ctx.Err()
	}
	s.Start(t.Context())
	awaitSignal(t, started, "scheduled archive start")
	s.Stop()
	awaitSignal(t, canceled, "scheduled archive cancellation")
	if got := s.Status(); got.Running {
		t.Fatalf("scheduler still running after Stop: %+v", got)
	}
}

func TestSQLiteArchiveSchedulerStatusIsIndependentFromSyncStatus(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	syncScheduler := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	before := syncScheduler.Status()

	archiveScheduler := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: 24 * time.Hour}, schedulerTestWriter{}, io.Discard)
	archiveScheduler.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		return sqlitearchive.ArchiveResult{}, context.DeadlineExceeded
	}
	archiveScheduler.run(t.Context(), "test")

	after := syncScheduler.Status()
	if before != after {
		t.Fatalf("archive status changed sync status: before=%+v after=%+v", before, after)
	}
	if got := archiveScheduler.Status(); got.LastStatus != "error" {
		t.Fatalf("archive status did not independently record failure: %+v", got)
	}
}

func TestSQLiteArchiveSchedulerMetricsDoNotLeakProviderFailure(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	metricsPath := filepath.Join(cfg.LogDir, "sqlite-archive-metrics.jsonl")
	if err := os.WriteFile(cfg.ConfigPath, []byte("metrics:\n  enabled: true\n  path: sqlite-archive-metrics.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logOut bytes.Buffer
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, &logOut)
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		return sqlitearchive.ArchiveResult{}, fmt.Errorf("provider https://objects.example.test/private token=super-secret path=/tmp/brain.db")
	}
	s.run(t.Context(), "test")

	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range []string{"scheduler.sqlite_archive.attempt", "scheduler.sqlite_archive.started", "scheduler.sqlite_archive.failed"} {
		if !strings.Contains(text, name) {
			t.Fatalf("missing %q metric: %s", name, text)
		}
	}
	for _, forbidden := range []string{"objects.example", "super-secret", "/tmp/brain.db", "provider https"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metrics leaked %q: %s", forbidden, text)
		}
		if strings.Contains(logOut.String(), forbidden) {
			t.Fatalf("logs leaked %q: %s", forbidden, logOut.String())
		}
		if strings.Contains(s.Status().LastError, forbidden) {
			t.Fatalf("status leaked %q: %+v", forbidden, s.Status())
		}
	}
	if got := s.Status().LastError; got != "archive_failed" {
		t.Fatalf("status error code = %q, want archive_failed", got)
	}
}

func TestSQLiteArchiveSchedulerCompletedMetricsContainAggregates(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	metricsPath := filepath.Join(cfg.LogDir, "sqlite-archive-success.jsonl")
	if err := os.WriteFile(cfg.ConfigPath, []byte("metrics:\n  enabled: true\n  path: sqlite-archive-success.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, io.Discard)
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		return sqlitearchive.ArchiveResult{SnapshotSize: 4096, ArchiveSize: 1024}, nil
	}
	s.run(t.Context(), "startup")

	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range []string{"scheduler.sqlite_archive.attempt", "scheduler.sqlite_archive.started", "scheduler.sqlite_archive.completed"} {
		if !strings.Contains(text, name) {
			t.Fatalf("missing %q metric: %s", name, text)
		}
	}
	if !strings.Contains(text, `"snapshot_bytes":4096`) || !strings.Contains(text, `"archive_bytes":1024`) {
		t.Fatalf("missing archive aggregates: %s", text)
	}
	if !strings.Contains(text, `"invocation":"scheduler:startup"`) {
		t.Fatalf("startup attempt mislabeled: %s", text)
	}
}

func TestSQLiteArchiveSchedulerLockIOFailureIsFailedNotSkipped(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	metricsPath := filepath.Join(cfg.LogDir, "sqlite-archive-lock-failure.jsonl")
	if err := os.WriteFile(cfg.ConfigPath, []byte("metrics:\n  enabled: true\n  path: sqlite-archive-lock-failure.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logOut bytes.Buffer
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, &logOut)
	s.acquireLease = func(config.Config, string) (*sqlitearchive.OperationLease, error) {
		return nil, fmt.Errorf("permission denied path=/secret/lock token=leak")
	}
	s.run(t.Context(), "test")

	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "scheduler.sqlite_archive.failed") || !strings.Contains(text, `"failure_code":"lock_failed"`) {
		t.Fatalf("missing sanitized lock failure metric: %s", text)
	}
	if strings.Contains(text, "scheduler.sqlite_archive.lock_skipped") {
		t.Fatalf("lock I/O failure was incorrectly reported as lock-skip: %s", text)
	}
	if got := s.Status(); got.LastStatus != "error" || got.LastError != "lock_failed" {
		t.Fatalf("unexpected lock failure status: %+v", got)
	}
	for _, output := range []string{text, logOut.String(), s.Status().LastError} {
		for _, forbidden := range []string{"/secret/lock", "token=leak", "permission denied"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("lock failure leaked %q: %s", forbidden, output)
			}
		}
	}
}

func TestSQLiteArchiveSchedulerLeaseCloseFailureIsFailedBeforeCompletion(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	metricsPath := filepath.Join(cfg.LogDir, "sqlite-archive-close-failure.jsonl")
	if err := os.WriteFile(cfg.ConfigPath, []byte("metrics:\n  enabled: true\n  path: sqlite-archive-close-failure.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, io.Discard)
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		return sqlitearchive.ArchiveResult{SnapshotSize: 4096, ArchiveSize: 1024}, nil
	}
	s.releaseLease = func(lease *sqlitearchive.OperationLease) error {
		if err := lease.Close(); err != nil {
			t.Fatalf("close real lease: %v", err)
		}
		return fmt.Errorf("synthetic close failure path=/private/lock token=secret")
	}

	s.run(t.Context(), "interval")
	if got := s.Status(); got.LastStatus != "error" || got.LastError != "lock_failed" {
		t.Fatalf("close-failure status = %+v, want sanitized lock_failed", got)
	}
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "scheduler.sqlite_archive.failed") || strings.Contains(text, "scheduler.sqlite_archive.completed") {
		t.Fatalf("close failure emitted success metrics: %s", text)
	}
	for _, forbidden := range []string{"/private/lock", "token=secret", "synthetic close failure"} {
		if strings.Contains(text, forbidden) || strings.Contains(s.Status().LastError, forbidden) {
			t.Fatalf("close failure leaked %q", forbidden)
		}
	}
}

func TestSQLiteArchiveSchedulerPreservesArchiveFailureWhenLeaseCloseAlsoFails(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	var logOut bytes.Buffer
	s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, &logOut)
	s.archive = func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error) {
		return sqlitearchive.ArchiveResult{}, context.DeadlineExceeded
	}
	s.releaseLease = func(lease *sqlitearchive.OperationLease) error {
		if err := lease.Close(); err != nil {
			t.Fatalf("close real lease: %v", err)
		}
		return fmt.Errorf("synthetic close failure")
	}

	s.run(t.Context(), "interval")
	if got := s.Status(); got.LastStatus != "error" || got.LastError != "timeout" {
		t.Fatalf("dual-failure status = %+v, want original timeout", got)
	}
	if !strings.Contains(logOut.String(), "operation lock release also failed") {
		t.Fatalf("dual failure did not surface sanitized release failure: %s", logOut.String())
	}
	if strings.Contains(logOut.String(), "synthetic close failure") {
		t.Fatalf("dual failure leaked release error: %s", logOut.String())
	}
}

func TestSQLiteArchiveSchedulerPreflightLeaseCloseFailuresAreNotDiscarded(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		setup func(*sqliteArchiveScheduler)
	}{
		{
			name: "state read failure",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) {
					return time.Time{}, fmt.Errorf("state read failed path=/private/state")
				}
			},
		},
		{
			name: "interval skip",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) {
					return now, nil
				}
			},
		},
		{
			name: "state write failure",
			setup: func(s *sqliteArchiveScheduler) {
				s.readAttempt = func(config.Config) (time.Time, error) {
					return time.Time{}, nil
				}
				s.writeAttempt = func(config.Config, time.Time) error {
					return fmt.Errorf("state write failed path=/private/state")
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := openSchedulerTestConfig(t)
			var logOut bytes.Buffer
			s := newSQLiteArchiveScheduler(cfg, schedulerSQLiteArchiveConfig{Enabled: true, Interval: time.Hour}, schedulerTestWriter{}, &logOut)
			s.now = func() time.Time { return now }
			test.setup(s)
			s.releaseLease = func(lease *sqlitearchive.OperationLease) error {
				if err := lease.Close(); err != nil {
					t.Fatalf("close real lease: %v", err)
				}
				return fmt.Errorf("release failed path=/private/lock token=secret")
			}

			s.run(t.Context(), "interval")
			if got := s.Status(); got.LastStatus != "error" || got.LastError != "lock_failed" {
				t.Fatalf("preflight close-failure status = %+v, want lock_failed", got)
			}
			for _, forbidden := range []string{"/private/state", "/private/lock", "token=secret"} {
				if strings.Contains(logOut.String(), forbidden) || strings.Contains(s.Status().LastError, forbidden) {
					t.Fatalf("preflight close failure leaked %q", forbidden)
				}
			}
		})
	}
}

func TestBuildRemoteSchedulersConstructsSeparateWriteCapableSibling(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	t.Setenv("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_ENABLED", "true")
	t.Setenv("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL", "6h")
	t.Setenv("DBRAIN_SCHEDULER_SYNC_ALL_ENABLED", "false")

	oldBuild := buildScheduledSQLiteArchiveWriter
	defer func() { buildScheduledSQLiteArchiveWriter = oldBuild }()
	var builds atomic.Int32
	buildScheduledSQLiteArchiveWriter = func(context.Context, string) (sqlitearchive.ObjectWriter, error) {
		builds.Add(1)
		return schedulerTestWriter{}, nil
	}

	schedulers, err := buildRemoteSchedulers(t.Context(), cfg, io.Discard)
	if err != nil {
		t.Fatalf("buildRemoteSchedulers: %v", err)
	}
	if builds.Load() != 1 {
		t.Fatalf("writer builds = %d, want 1", builds.Load())
	}
	if schedulers.sqliteArchive == nil || schedulers.sqliteArchive.writer == nil || schedulers.sqliteArchive.opts.Interval != 6*time.Hour {
		t.Fatalf("unexpected SQLite archive scheduler: %+v", schedulers.sqliteArchive)
	}
	if schedulers.syncAll == nil {
		t.Fatal("missing independent sync scheduler")
	}
}

func TestBuildRemoteSchedulersDisabledArchiveDoesNotResolveWriter(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	oldBuild := buildScheduledSQLiteArchiveWriter
	defer func() { buildScheduledSQLiteArchiveWriter = oldBuild }()
	buildScheduledSQLiteArchiveWriter = func(context.Context, string) (sqlitearchive.ObjectWriter, error) {
		t.Fatal("disabled SQLite archive scheduler resolved a writer")
		return nil, nil
	}

	schedulers, err := buildRemoteSchedulers(t.Context(), cfg, io.Discard)
	if err != nil {
		t.Fatalf("buildRemoteSchedulers: %v", err)
	}
	if schedulers.sqliteArchive == nil || schedulers.sqliteArchive.opts.Enabled {
		t.Fatalf("unexpected disabled SQLite archive scheduler: %+v", schedulers.sqliteArchive)
	}
}

func openSchedulerTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func awaitSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func awaitDuration(t *testing.T, ch <-chan time.Duration, label string) time.Duration {
	t.Helper()
	select {
	case duration := <-ch:
		return duration
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return 0
	}
}

func TestSchedulerSQLiteArchiveConfigRejectsInvalidInterval(t *testing.T) {
	for _, raw := range []string{"not-a-duration", "0s", "-1h"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL", raw)
			if _, err := schedulerSQLiteArchiveConfigFromRuntime(t.TempDir()); err == nil {
				t.Fatalf("expected %q to be rejected", raw)
			}
		})
	}
}

func TestSchedulerSQLiteArchiveConfigReadsYAML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
scheduler:
  sqlite_archive:
    enabled: true
    interval: 48h
    run_on_start: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := schedulerSQLiteArchiveConfigFromRuntime(root)
	if err != nil {
		t.Fatalf("schedulerSQLiteArchiveConfigFromRuntime: %v", err)
	}
	if !got.Enabled || got.Interval != 48*time.Hour || got.RunOnStart {
		t.Fatalf("unexpected YAML config: %+v", got)
	}
}

func TestSchedulerSQLiteArchiveConfigRuntimePrecedence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
scheduler:
  sqlite_archive:
    enabled: false
    interval: 48h
    run_on_start: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_ENABLED=true\nDBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL=36h\nDBRAIN_SCHEDULER_SQLITE_ARCHIVE_RUN_ON_START=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL=30h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL", "12h")

	got, err := schedulerSQLiteArchiveConfigFromRuntime(root)
	if err != nil {
		t.Fatalf("schedulerSQLiteArchiveConfigFromRuntime: %v", err)
	}
	if !got.Enabled || !got.RunOnStart || got.Interval != 12*time.Hour {
		t.Fatalf("unexpected resolved config: %+v", got)
	}

	t.Setenv("DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL", "")
	got, err = schedulerSQLiteArchiveConfigFromRuntime(root)
	if err != nil {
		t.Fatalf("schedulerSQLiteArchiveConfigFromRuntime without shell: %v", err)
	}
	if got.Interval != 30*time.Hour {
		t.Fatalf(".envrc interval = %s, want 30h", got.Interval)
	}
}
