package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/sqlitearchive"
)

const defaultSchedulerSQLiteArchiveInterval = 24 * time.Hour

var buildScheduledSQLiteArchiveWriter = newScheduledSQLiteArchiveWriter

type schedulerSQLiteArchiveConfig struct {
	Enabled    bool
	Interval   time.Duration
	RunOnStart bool
}

type sqliteArchiveScheduler struct {
	cfg    config.Config
	opts   schedulerSQLiteArchiveConfig
	writer sqlitearchive.ObjectWriter
	logOut io.Writer

	now          func() time.Time
	wait         func(context.Context, time.Duration) bool
	archive      func(context.Context, config.Config, sqlitearchive.Options) (sqlitearchive.ArchiveResult, error)
	acquireLease func(config.Config, string) (*sqlitearchive.OperationLease, error)

	mu     sync.Mutex
	status schedulerstate.SQLiteArchiveStatus
	cancel context.CancelFunc
	done   chan struct{}
}

func schedulerSQLiteArchiveConfigFromRuntime(rootDir string) (schedulerSQLiteArchiveConfig, error) {
	cfg := schedulerSQLiteArchiveConfig{
		Enabled:    runtimeenv.FirstBoolDefault(rootDir, false, "DBRAIN_SCHEDULER_SQLITE_ARCHIVE_ENABLED"),
		Interval:   defaultSchedulerSQLiteArchiveInterval,
		RunOnStart: runtimeenv.FirstBoolDefault(rootDir, true, "DBRAIN_SCHEDULER_SQLITE_ARCHIVE_RUN_ON_START"),
	}
	if raw := runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval <= 0 {
			return schedulerSQLiteArchiveConfig{}, fmt.Errorf("parse DBRAIN_SCHEDULER_SQLITE_ARCHIVE_INTERVAL: %q", raw)
		}
		cfg.Interval = interval
	}
	return cfg, nil
}

func newSQLiteArchiveScheduler(cfg config.Config, opts schedulerSQLiteArchiveConfig, writer sqlitearchive.ObjectWriter, logOut io.Writer) *sqliteArchiveScheduler {
	if logOut == nil {
		logOut = io.Discard
	}
	s := &sqliteArchiveScheduler{
		cfg: cfg, opts: opts, writer: writer,
		logOut: newTimestampedLineWriter(logOut, time.Now),
		now:    func() time.Time { return time.Now().UTC() },
		wait: func(ctx context.Context, duration time.Duration) bool {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-timer.C:
				return true
			}
		},
		archive:      sqlitearchive.Archive,
		acquireLease: sqlitearchive.AcquireOperationLease,
	}
	s.status = schedulerstate.SQLiteArchiveStatus{
		Enabled: opts.Enabled, Interval: opts.Interval.String(), RunOnStart: opts.RunOnStart,
	}
	return s
}

func (s *sqliteArchiveScheduler) Start(ctx context.Context) {
	if s == nil || !s.opts.Enabled {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			if s.done == done {
				s.cancel = nil
				s.done = nil
			}
			s.mu.Unlock()
			close(done)
		}()
		s.loop(childCtx)
	}()
}

func (s *sqliteArchiveScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *sqliteArchiveScheduler) Status() schedulerstate.SQLiteArchiveStatus {
	if s == nil {
		return schedulerstate.SQLiteArchiveStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *sqliteArchiveScheduler) loop(ctx context.Context) {
	_, _ = fmt.Fprintf(s.logOut, "scheduler sqlite archive enabled: interval=%s run_on_start=%t\n", s.opts.Interval, s.opts.RunOnStart)
	if s.opts.RunOnStart {
		s.run(ctx, "startup")
	}
	for ctx.Err() == nil {
		delay := s.nextDelay()
		s.setNextRunAt(s.now().Add(delay))
		if !s.wait(ctx, delay) {
			return
		}
		s.run(ctx, "interval")
	}
}

func (s *sqliteArchiveScheduler) nextDelay() time.Duration {
	lastAttempt, err := readScheduledSQLiteArchiveAttempt(s.cfg)
	if err != nil || lastAttempt.IsZero() {
		return s.opts.Interval
	}
	remaining := lastAttempt.Add(s.opts.Interval).Sub(s.now())
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (s *sqliteArchiveScheduler) run(ctx context.Context, reason string) {
	if s == nil || !s.opts.Enabled || ctx.Err() != nil {
		return
	}
	metricsRun, closeMetrics, metricsErr := openSQLiteArchiveMetrics(s.cfg, reason)
	if metricsErr != nil {
		_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive metrics unavailable")
		metricsRun = metrics.RunContext{Sink: metrics.NoopSink()}
		closeMetrics = func() error { return nil }
	}
	defer func() { _ = closeMetrics() }()
	_ = metricsRun.Emit(metrics.SQLiteArchiveAttemptEvent())
	s.mu.Lock()
	if s.status.Running {
		s.status.LastReason = reason
		s.status.LastStatus = "skipped"
		s.status.LastError = "overlap"
		s.status.LastFinishedAt = s.now()
		s.mu.Unlock()
		_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive skipped: previous archive still active")
		_ = metricsRun.Emit(metrics.SQLiteArchiveOverlapSkippedEvent())
		return
	}
	s.mu.Unlock()
	lock, err := s.acquireLease(s.cfg, "scheduler:"+reason)
	if err != nil {
		s.mu.Lock()
		s.status.LastReason = reason
		s.status.LastFinishedAt = s.now()
		if errors.Is(err, sqlitearchive.ErrOperationLocked) {
			s.status.LastStatus = "skipped"
			s.status.LastError = "lock_held"
			s.mu.Unlock()
			_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive skipped: archive or restore lock held")
			_ = metricsRun.Emit(metrics.SQLiteArchiveLockSkippedEvent())
			return
		}
		s.status.LastStatus = "error"
		s.status.LastError = string(metrics.SQLiteArchiveFailureLock)
		s.mu.Unlock()
		_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive failed: operation lock unavailable")
		_ = metricsRun.Emit(metrics.SQLiteArchiveFailedEvent(0, metrics.SQLiteArchiveFailureLock))
		return
	}
	started := s.now()
	lastAttempt, err := readScheduledSQLiteArchiveAttempt(s.cfg)
	if err != nil {
		_ = lock.Close()
		s.recordPreflightFailure(reason, started, metrics.SQLiteArchiveFailureState)
		_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive failed: interval state unavailable")
		_ = metricsRun.Emit(metrics.SQLiteArchiveFailedEvent(0, metrics.SQLiteArchiveFailureState))
		return
	}
	if !lastAttempt.IsZero() {
		remaining := lastAttempt.Add(s.opts.Interval).Sub(started)
		if remaining > 0 {
			_ = lock.Close()
			s.mu.Lock()
			s.status.LastReason = reason
			s.status.LastStatus = "skipped"
			s.status.LastError = "interval_not_elapsed"
			s.status.LastFinishedAt = started
			s.mu.Unlock()
			_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive skipped: interval not elapsed")
			_ = metricsRun.Emit(metrics.SQLiteArchiveIntervalSkippedEvent(remaining))
			return
		}
	}
	if err := writeScheduledSQLiteArchiveAttempt(s.cfg, started); err != nil {
		_ = lock.Close()
		s.recordPreflightFailure(reason, started, metrics.SQLiteArchiveFailureState)
		_, _ = fmt.Fprintln(s.logOut, "scheduler sqlite archive failed: interval state unavailable")
		_ = metricsRun.Emit(metrics.SQLiteArchiveFailedEvent(0, metrics.SQLiteArchiveFailureState))
		return
	}
	s.mu.Lock()
	s.status.Running = true
	s.status.CurrentReason = reason
	s.status.CurrentStartedAt = started
	s.status.LastReason = reason
	s.status.LastStartedAt = started
	s.status.LastStatus = "running"
	s.status.LastError = ""
	s.mu.Unlock()
	defer func() {
		_ = lock.Close()
		s.mu.Lock()
		s.status.Running = false
		s.status.CurrentReason = ""
		s.status.CurrentStartedAt = time.Time{}
		s.mu.Unlock()
	}()

	_, _ = fmt.Fprintf(s.logOut, "scheduler sqlite archive started: reason=%s at=%s\n", reason, started.Format(time.RFC3339))
	_ = metricsRun.Emit(metrics.SQLiteArchiveStartedEvent())
	result, err := s.archive(ctx, s.cfg, sqlitearchive.Options{
		Prefix:         sqlitearchive.DefaultPrefix,
		Writer:         s.writer,
		OperationLease: lock,
	})
	if err != nil {
		failureCode := metrics.SQLiteArchiveFailureArchive
		if errors.Is(err, context.Canceled) {
			failureCode = metrics.SQLiteArchiveFailureCanceled
		} else if errors.Is(err, context.DeadlineExceeded) {
			failureCode = metrics.SQLiteArchiveFailureTimeout
		}
		s.finishRun("error", string(failureCode))
		_ = metricsRun.Emit(metrics.SQLiteArchiveFailedEvent(s.now().Sub(started), failureCode))
		_, _ = fmt.Fprintf(s.logOut, "scheduler sqlite archive failed: duration=%s\n", s.now().Sub(started).Round(time.Second))
		return
	}
	s.finishRun("ok", "")
	_ = metricsRun.Emit(metrics.SQLiteArchiveCompletedEvent(s.now().Sub(started), result.SnapshotSize, result.ArchiveSize))
	_, _ = fmt.Fprintf(s.logOut, "scheduler sqlite archive finished: duration=%s\n", s.now().Sub(started).Round(time.Second))
}

func (s *sqliteArchiveScheduler) recordPreflightFailure(reason string, at time.Time, code metrics.SQLiteArchiveFailureCode) {
	s.mu.Lock()
	s.status.LastReason = reason
	s.status.LastStatus = "error"
	s.status.LastError = string(code)
	s.status.LastFinishedAt = at
	s.mu.Unlock()
}

func (s *sqliteArchiveScheduler) finishRun(status string, errorText string) {
	s.mu.Lock()
	s.status.LastStatus = status
	s.status.LastError = errorText
	s.status.LastFinishedAt = s.now()
	s.mu.Unlock()
}

func (s *sqliteArchiveScheduler) setNextRunAt(at time.Time) {
	s.mu.Lock()
	s.status.NextRunAt = at.UTC()
	s.mu.Unlock()
}

func scheduledSQLiteArchiveAttemptPath(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "scheduler", "sqlite-archive-last-attempt")
}

func readScheduledSQLiteArchiveAttempt(cfg config.Config) (time.Time, error) {
	path := scheduledSQLiteArchiveAttemptPath(cfg)
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return time.Time{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128 {
		return time.Time{}, fmt.Errorf("invalid SQLite archive scheduler state")
	}
	data, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil {
		return time.Time{}, err
	}
	if len(data) > 128 {
		return time.Time{}, fmt.Errorf("invalid SQLite archive scheduler state")
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid SQLite archive scheduler state")
	}
	return parsed.UTC(), nil
}

func writeScheduledSQLiteArchiveAttempt(cfg config.Config, attemptedAt time.Time) (err error) {
	path := scheduledSQLiteArchiveAttemptPath(cfg)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".sqlite-archive-attempt-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := io.WriteString(temp, attemptedAt.UTC().Format(time.RFC3339Nano)+"\n"); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
