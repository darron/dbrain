package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

const defaultSchedulerSyncInterval = time.Hour

type schedulerSyncConfig struct {
	Enabled    bool
	Interval   time.Duration
	RunOnStart bool
	Jitter     time.Duration
	Flags      syncAllFlags
}

func schedulerSyncConfigFromRuntime(rootDir string) (schedulerSyncConfig, error) {
	cfg := schedulerSyncConfig{
		Interval: defaultSchedulerSyncInterval,
	}
	cfg.Enabled = runtimeenv.FirstBool(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_ENABLED")
	cfg.RunOnStart = runtimeenv.FirstBool(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_RUN_ON_START")

	if raw := runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return schedulerSyncConfig{}, fmt.Errorf("parse DBRAIN_SCHEDULER_SYNC_ALL_INTERVAL: %q", raw)
		}
		cfg.Interval = parsed
	}
	if raw := runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_JITTER"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 {
			return schedulerSyncConfig{}, fmt.Errorf("parse DBRAIN_SCHEDULER_SYNC_ALL_JITTER: %q", raw)
		}
		cfg.Jitter = parsed
	}

	flags, err := schedulerSyncFlagsFromRuntime(rootDir)
	if err != nil {
		return schedulerSyncConfig{}, err
	}
	cfg.Flags = flags
	return cfg, nil
}

func schedulerSyncFlagsFromRuntime(rootDir string) (syncAllFlags, error) {
	flags := syncAllFlags{
		watchLater:       true,
		liked:            true,
		summarize:        true,
		categorizeImages: true,
	}
	var err error

	flags.xBookmarksLimit, err = schedulerInt(rootDir, "X_BOOKMARKS_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.xLimit, err = schedulerInt(rootDir, "X_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.xMediaLimit, err = schedulerInt(rootDir, "X_MEDIA_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.xPhotoOCRLimit, err = schedulerInt(rootDir, "X_PHOTO_OCR_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.xConcurrency, err = schedulerInt(rootDir, "X_CONCURRENCY")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.linkDiscoverLimit, err = schedulerInt(rootDir, "LINK_DISCOVER_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.linkLimit, err = schedulerInt(rootDir, "LINK_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.linkConcurrency, err = schedulerInt(rootDir, "LINK_CONCURRENCY")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.githubLimit, err = schedulerInt(rootDir, "GITHUB_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.youtubeLimit, err = schedulerInt(rootDir, "YOUTUBE_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.sourceLimit, err = schedulerInt(rootDir, "SOURCE_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.sourceConcurrency, err = schedulerInt(rootDir, "SOURCE_CONCURRENCY")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.archiveMediaLimit, err = schedulerInt(rootDir, "ARCHIVE_MEDIA_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.categorizeLimit, err = schedulerInt(rootDir, "CATEGORIZE_LIMIT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.categorizeConcurrency, err = schedulerInt(rootDir, "CATEGORIZE_CONCURRENCY")
	if err != nil {
		return syncAllFlags{}, err
	}

	flags.xTimeout, err = schedulerDuration(rootDir, "X_TIMEOUT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.xMediaTimeout, err = schedulerDuration(rootDir, "X_MEDIA_DOWNLOAD_TIMEOUT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.timeout, err = schedulerDuration(rootDir, "TIMEOUT")
	if err != nil {
		return syncAllFlags{}, err
	}
	flags.categorizeTimeout, err = schedulerDuration(rootDir, "CATEGORIZE_TIMEOUT")
	if err != nil {
		return syncAllFlags{}, err
	}

	flags.ocrModel = schedulerString(rootDir, "OCR_MODEL")
	flags.model = schedulerString(rootDir, "MODEL")
	flags.categorizeModel = schedulerString(rootDir, "CATEGORIZE_MODEL")
	flags.cliProvider = schedulerString(rootDir, "CLI")
	flags.length = schedulerString(rootDir, "LENGTH")
	flags.browser = schedulerString(rootDir, "BROWSER")
	flags.profile = schedulerString(rootDir, "PROFILE")

	flags.appleNotes = schedulerBool(rootDir, "APPLE_NOTES")
	flags.safariTabs = schedulerBool(rootDir, "SAFARI_TABS")
	flags.archiveMedia = schedulerBool(rootDir, "ARCHIVE_MEDIA")
	flags.okfExport = schedulerBool(rootDir, "OKF_EXPORT")
	flags.force = schedulerBool(rootDir, "FORCE")
	flags.summarize = !schedulerBool(rootDir, "SKIP_SUMMARIZE")
	flags.categorizeImages = !schedulerBool(rootDir, "SKIP_CATEGORIZE_IMAGES")

	flags.skipXBookmarks = schedulerBool(rootDir, "SKIP_X_BOOKMARKS")
	flags.skipX = schedulerBool(rootDir, "SKIP_X")
	flags.skipXMedia = schedulerBool(rootDir, "SKIP_X_MEDIA")
	flags.skipXPhotoOCR = schedulerBool(rootDir, "SKIP_X_PHOTO_OCR")
	flags.skipLinks = schedulerBool(rootDir, "SKIP_LINKS")
	flags.skipGitHub = schedulerBool(rootDir, "SKIP_GITHUB")
	flags.skipYouTube = schedulerBool(rootDir, "SKIP_YOUTUBE")
	flags.skipAppleNotes = schedulerBool(rootDir, "SKIP_APPLE_NOTES")
	flags.skipSafariTabs = schedulerBool(rootDir, "SKIP_SAFARI_TABS")
	flags.skipFeeds = schedulerBool(rootDir, "SKIP_FEEDS")
	flags.skipSources = schedulerBool(rootDir, "SKIP_SOURCES")
	flags.skipCategorize = schedulerBool(rootDir, "SKIP_CATEGORIZE")
	flags.skipOKFExport = schedulerBool(rootDir, "SKIP_OKF_EXPORT")

	return flags, nil
}

func schedulerString(rootDir string, suffix string) string {
	return runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_"+suffix)
}

func schedulerBool(rootDir string, suffix string) bool {
	return runtimeenv.FirstBool(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_"+suffix)
}

func schedulerInt(rootDir string, suffix string) (int, error) {
	raw := schedulerString(rootDir, suffix)
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("parse DBRAIN_SCHEDULER_SYNC_ALL_%s: %q", suffix, raw)
	}
	return parsed, nil
}

func schedulerDuration(rootDir string, suffix string) (time.Duration, error) {
	raw := schedulerString(rootDir, suffix)
	if raw == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("parse DBRAIN_SCHEDULER_SYNC_ALL_%s: %q", suffix, raw)
	}
	return parsed, nil
}

type syncScheduler struct {
	cfg     config.Config
	opts    schedulerSyncConfig
	logOut  io.Writer
	runSync func(context.Context, config.Config, syncAllFlags, io.Writer) error
	postRun func(context.Context, scheduledSyncOutcome)

	mu     sync.Mutex
	status schedulerstate.SyncAllStatus
	cancel context.CancelFunc
	done   chan struct{}
}

func newSyncScheduler(cfg config.Config, opts schedulerSyncConfig, logOut io.Writer) *syncScheduler {
	if logOut == nil {
		logOut = io.Discard
	}
	logOut = newTimestampedLineWriter(logOut, time.Now)
	status := schedulerstate.SyncAllStatus{
		Enabled:    opts.Enabled,
		Interval:   opts.Interval.String(),
		RunOnStart: opts.RunOnStart,
	}
	if opts.Jitter > 0 {
		status.Jitter = opts.Jitter.String()
	}
	return &syncScheduler{cfg: cfg, opts: opts, logOut: logOut, status: status, runSync: runScheduledSyncAllUnlocked}
}

func (s *syncScheduler) Start(ctx context.Context) {
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
	go s.loop(childCtx, done)
}

func (s *syncScheduler) Stop() {
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
	s.mu.Lock()
	if s.done == done {
		s.cancel = nil
		s.done = nil
	}
	s.mu.Unlock()
}

func (s *syncScheduler) Status() schedulerstate.SyncAllStatus {
	if s == nil {
		return schedulerstate.SyncAllStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *syncScheduler) loop(ctx context.Context, done chan struct{}) {
	emitSchedulerSyncMarker(s.cfg, "scheduler.sync.enabled")
	var runs sync.WaitGroup
	defer close(done)
	defer emitSchedulerSyncMarker(s.cfg, "scheduler.sync.stopped")
	defer runs.Wait()
	launch := func(reason string) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		runs.Add(1)
		go func() {
			defer runs.Done()
			s.runAndPost(ctx, reason)
		}()
	}
	_, _ = fmt.Fprintf(s.logOut, "scheduler sync all enabled: interval=%s run_on_start=%t\n", s.opts.Interval, s.opts.RunOnStart)
	if s.opts.RunOnStart {
		launch("startup")
	}

	delay := s.nextDelay()
	s.setNextRunAt(time.Now().UTC().Add(delay))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			launch("interval")
			delay := s.nextDelay()
			s.setNextRunAt(time.Now().UTC().Add(delay))
			timer.Reset(delay)
		}
	}
}

func (s *syncScheduler) nextDelay() time.Duration {
	delay := s.opts.Interval
	if s.opts.Jitter <= 0 {
		return delay
	}
	// Deterministic bounded jitter avoids synchronized launches without making
	// tests or logs depend on randomness.
	n := time.Now().UnixNano()
	jitterNanos := n % int64(s.opts.Jitter)
	return delay + time.Duration(jitterNanos)
}

func (s *syncScheduler) runAndPost(ctx context.Context, reason string) {
	outcome, actual := s.run(ctx, reason)
	if actual && s.postRun != nil {
		s.postRun(ctx, outcome)
	}
}

func (s *syncScheduler) run(ctx context.Context, reason string) (scheduledSyncOutcome, bool) {
	s.mu.Lock()
	if s.status.Running {
		emitSchedulerSyncMarker(s.cfg, "scheduler.sync.overlap_skipped")
		s.status.LastReason = reason
		s.status.LastStatus = "skipped"
		s.status.LastError = "previous run still active"
		s.status.LastFinishedAt = time.Now().UTC()
		s.mu.Unlock()
		_, _ = fmt.Fprintf(s.logOut, "scheduler sync all skipped: previous run still active\n")
		return scheduledSyncOutcome{}, false
	}
	lock, err := acquireSyncAllLock(s.cfg, "scheduler:"+reason)
	if err != nil {
		emitSchedulerSyncMarker(s.cfg, "scheduler.sync.lock_skipped")
		now := time.Now().UTC()
		s.status.LastReason = reason
		s.status.LastStatus = "skipped"
		s.status.LastError = err.Error()
		s.status.LastFinishedAt = now
		s.mu.Unlock()
		_, _ = fmt.Fprintf(s.logOut, "scheduler sync all skipped: %v\n", err)
		return scheduledSyncOutcome{}, false
	}
	start := time.Now().UTC()
	s.status.Running = true
	s.status.CurrentReason = reason
	s.status.CurrentStartedAt = start
	s.status.LastReason = reason
	s.status.LastStartedAt = start
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

	_, _ = fmt.Fprintf(s.logOut, "scheduler sync all started: reason=%s at=%s\n", reason, start.Format(time.RFC3339))
	runSync := s.runSync
	if runSync == nil {
		runSync = runScheduledSyncAllUnlocked
	}
	if err := runSync(ctx, s.cfg, s.opts.Flags, s.logOut); err != nil {
		finishedAt := time.Now().UTC()
		status := scheduledSyncStatusError
		if isScheduledSyncCancellation(err) {
			status = scheduledSyncStatusCancelled
		}
		s.finishRunAt(string(status), err.Error(), finishedAt)
		if status == scheduledSyncStatusCancelled {
			_, _ = fmt.Fprintf(s.logOut, "scheduler sync all cancelled: duration=%s\n", time.Since(start).Round(time.Second))
		} else {
			_, _ = fmt.Fprintf(s.logOut, "scheduler sync all failed: duration=%s error=%v\n", time.Since(start).Round(time.Second), err)
		}
		return scheduledSyncOutcome{
			Reason:     reason,
			Status:     status,
			StartedAt:  start,
			FinishedAt: finishedAt,
			Err:        err,
		}, true
	}
	finishedAt := time.Now().UTC()
	s.finishRunAt(string(scheduledSyncStatusOK), "", finishedAt)
	_, _ = fmt.Fprintf(
		s.logOut,
		"scheduler sync all completed: started=%s completed=%s duration=%s status=ok\n",
		start.Format(time.RFC3339),
		finishedAt.Format(time.RFC3339),
		finishedAt.Sub(start).Round(time.Second),
	)
	return scheduledSyncOutcome{
		Reason:     reason,
		Status:     scheduledSyncStatusOK,
		StartedAt:  start,
		FinishedAt: finishedAt,
	}, true
}

func isScheduledSyncCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var refreshErr *semanticrefresh.RefreshError
	return errors.As(err, &refreshErr) && refreshErr.Code == semanticrefresh.ErrorCancelled
}

func emitSchedulerSyncMarker(cfg config.Config, name string) {
	run, closeMetrics, err := openSyncMetrics(cfg, "scheduler:lifecycle")
	if err != nil {
		return
	}
	emitSchedulerSyncMarkerEvent(run, name)
	_ = closeMetrics()
}

func emitSchedulerSyncMarkerEvent(run metrics.RunContext, name string) {
	if !run.Enabled() {
		return
	}
	switch name {
	case "scheduler.sync.enabled", "scheduler.sync.stopped", "scheduler.sync.lock_skipped", "scheduler.sync.overlap_skipped":
		_ = run.Emit(metrics.Event{"event": name, "status": "ok"})
	}
}

func (s *syncScheduler) setNextRunAt(at time.Time) {
	s.mu.Lock()
	s.status.NextRunAt = at
	s.mu.Unlock()
}

func (s *syncScheduler) finishRunAt(status string, errorText string, finishedAt time.Time) {
	s.mu.Lock()
	s.status.LastStatus = status
	s.status.LastError = errorText
	s.status.LastFinishedAt = finishedAt
	s.mu.Unlock()
}

func runScheduledSyncAll(ctx context.Context, cfg config.Config, flags syncAllFlags, logOut io.Writer) error {
	lock, err := acquireSyncAllLock(cfg, "scheduler")
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Close()
	}()
	return runScheduledSyncAllUnlocked(ctx, cfg, flags, logOut)
}

func runScheduledSyncAllUnlocked(ctx context.Context, cfg config.Config, flags syncAllFlags, logOut io.Writer) (err error) {
	return runScheduledSyncAllUnlockedWithSemanticDeps(
		ctx,
		cfg,
		flags,
		logOut,
		defaultSemanticRefreshDeps(),
	)
}

func runScheduledSyncAllUnlockedWithSemanticDeps(
	ctx context.Context,
	cfg config.Config,
	flags syncAllFlags,
	logOut io.Writer,
	deps semanticRefreshDeps,
) (err error) {
	resolvedFlags, err := resolveSyncAllFlags(cfg.RootDir, flags)
	if err != nil {
		return wrapScheduledSyncBoundary(scheduledBoundaryConfigResolution, err)
	}
	metricsRun, closeMetrics, err := openSyncMetrics(cfg, "scheduler:interval")
	if err != nil {
		return wrapScheduledSyncBoundary(scheduledBoundaryMetricsOpen, err)
	}
	defer func() {
		if closeErr := closeMetrics(); closeErr != nil {
			err = errors.Join(err, wrapScheduledSyncBoundary(scheduledBoundaryMetricsClose, closeErr))
		}
	}()
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		return wrapScheduledSyncBoundary(scheduledBoundaryStoreOpen, err)
	}
	defer func() {
		if st != nil {
			_ = st.Close()
		}
	}()

	options, err := syncOptionsFromFlags(ctx, cfg, resolvedFlags, newLogger(false, logOut), logOut)
	if err != nil {
		return wrapScheduledSyncBoundary(scheduledBoundaryOptions, err)
	}
	options.Metrics = metricsRun
	options.ParentOwnsRunCompletion = true
	stats, err := runSyncAll(ctx, cfg, st, options)
	if err != nil {
		_ = st.Close()
		st = nil
		syncjob.EmitRunCompleted(metricsRun, stats, err)
		closeErr := closeMetrics()
		closeMetrics = func() error { return nil }
		if closeErr != nil {
			return errors.Join(err, wrapScheduledSyncBoundary(scheduledBoundaryMetricsClose, closeErr))
		}
		return err
	}
	if err := st.Close(); err != nil {
		st = nil
		boundaryErr := wrapScheduledSyncBoundary(scheduledBoundaryStoreClose, err)
		syncjob.EmitRunCompleted(metricsRun, stats, boundaryErr)
		return boundaryErr
	}
	st = nil

	if err := logScheduledSyncStats(logOut, stats); err != nil {
		boundaryErr := wrapScheduledSyncBoundary(scheduledBoundaryOutput, err)
		syncjob.EmitRunCompleted(metricsRun, stats, boundaryErr)
		return boundaryErr
	}
	reporter := newSemanticProgressReporter(logOut, time.Now)
	semanticStartedAt := time.Now().UTC()
	metricsErr := emitSemanticRefreshStarted(metricsRun, semanticStartedAt)
	refreshDeps := deps
	refreshDeps.startedAt = semanticStartedAt
	result, err := runConfiguredSemanticRefresh(
		ctx,
		cfg,
		refreshDeps,
		func(progress semanticrefresh.Progress) error {
			if reportErr := reporter.Report(progress); reportErr != nil {
				return wrapScheduledSyncBoundary(scheduledBoundaryOutput, reportErr)
			}
			return nil
		},
	)
	if finishErr := reporter.Finish(err == nil); finishErr != nil {
		err = errors.Join(err, wrapScheduledSyncBoundary(scheduledBoundaryOutput, finishErr))
	}
	stats = completeSyncStatsWithSemantic(stats, result)
	metricsErr = errors.Join(metricsErr, emitFullSyncCompletion(metricsRun, stats, result, err))
	if closeErr := closeMetrics(); closeErr != nil {
		metricsErr = errors.Join(metricsErr, wrapScheduledSyncBoundary(scheduledBoundaryMetricsClose, closeErr))
	}
	closeMetrics = func() error { return nil }
	if err != nil {
		return errors.Join(err, metricsErr)
	}
	if metricsErr != nil {
		return metricsErr
	}
	if outputErr := logScheduledSemanticRefreshResult(logOut, result); outputErr != nil {
		return wrapScheduledSyncBoundary(scheduledBoundaryOutput, outputErr)
	}
	return nil
}

func logScheduledSyncStats(logOut io.Writer, stats syncjob.Stats) error {
	if logOut == nil {
		return nil
	}
	if stats.XBookmarks != nil {
		if _, err := fmt.Fprintf(logOut, "scheduler sync all stage: x_bookmarks created=%d updated=%d unchanged=%d\n", stats.XBookmarks.Stats.Created, stats.XBookmarks.Stats.Updated, stats.XBookmarks.Stats.Unchanged); err != nil {
			return err
		}
	}
	if stats.X != nil {
		if _, err := fmt.Fprintf(logOut, "scheduler sync all stage: x hydrated=%d rendered=%d missing=%d\n", stats.X.Stats.Hydrated, stats.X.Stats.Rendered, stats.X.Stats.Missing); err != nil {
			return err
		}
	}
	if stats.Links != nil {
		if _, err := fmt.Fprintf(logOut, "scheduler sync all stage: links scanned=%d sources_queued=%d errors=%d\n", stats.Links.Stats.ItemsScanned, stats.Links.Stats.SourcesQueued, stats.Links.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.Sources != nil {
		if _, err := fmt.Fprintf(logOut, "scheduler sync all stage: sources cycles=%d summarized=%d errors=%d\n", stats.Sources.Stats.WorkCycles, stats.Sources.Stats.SourcesSummarized, stats.Sources.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.Categorize != nil {
		if _, err := fmt.Fprintf(logOut, "scheduler sync all stage: categorize succeeded=%d skipped=%d errors=%d\n", stats.Categorize.Stats.Succeeded, stats.Categorize.Stats.Skipped, stats.Categorize.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.OKFExport != nil {
		if _, err := fmt.Fprintf(logOut, "scheduler sync all stage: okf bundle=%s concepts=%d errors=%d\n", stats.OKFExport.Stats.Bundle, stats.OKFExport.Stats.ConceptsWritten, len(stats.OKFExport.Stats.Errors)); err != nil {
			return err
		}
	}
	return nil
}

func logScheduledSemanticRefreshResult(logOut io.Writer, result semanticrefresh.Result) error {
	if logOut == nil {
		return nil
	}
	if result.Outcome == semanticrefresh.OutcomeSkipped {
		_, err := fmt.Fprintf(
			logOut,
			"scheduler semantic refresh: skipped reason=%s capability=%s duration=%s\n",
			safeSemanticRefreshOutputField(result.SkipReason, 64),
			safeSemanticRefreshOutputField(string(result.Capability.State), 64),
			semanticRefreshElapsed(result.Duration),
		)
		return err
	}
	_, err := fmt.Fprintf(
		logOut,
		"scheduler semantic refresh: completed capability=%s duration=%s stages=%d\n",
		safeSemanticRefreshOutputField(string(result.Capability.State), 64),
		semanticRefreshElapsed(result.Duration),
		len(result.Stages),
	)
	return err
}
