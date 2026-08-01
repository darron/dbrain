package app

import (
	"context"
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
	postRun func(context.Context)

	mu     sync.Mutex
	status schedulerstate.SyncAllStatus
	cancel context.CancelFunc
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
	s.cancel = cancel
	s.mu.Unlock()
	go s.loop(childCtx)
}

func (s *syncScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *syncScheduler) Status() schedulerstate.SyncAllStatus {
	if s == nil {
		return schedulerstate.SyncAllStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *syncScheduler) loop(ctx context.Context) {
	emitSchedulerSyncMarker(s.cfg, "scheduler.sync.enabled")
	defer emitSchedulerSyncMarker(s.cfg, "scheduler.sync.stopped")
	_, _ = fmt.Fprintf(s.logOut, "scheduler sync all enabled: interval=%s run_on_start=%t\n", s.opts.Interval, s.opts.RunOnStart)
	if s.opts.RunOnStart {
		go s.runAndPost(ctx, "startup")
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
			go s.runAndPost(ctx, "interval")
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
	if s.run(ctx, reason) && s.postRun != nil {
		s.postRun(ctx)
	}
}

func (s *syncScheduler) run(ctx context.Context, reason string) bool {
	s.mu.Lock()
	if s.status.Running {
		emitSchedulerSyncMarker(s.cfg, "scheduler.sync.overlap_skipped")
		s.status.LastReason = reason
		s.status.LastStatus = "skipped"
		s.status.LastError = "previous run still active"
		s.status.LastFinishedAt = time.Now().UTC()
		s.mu.Unlock()
		_, _ = fmt.Fprintf(s.logOut, "scheduler sync all skipped: previous run still active\n")
		return false
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
		return false
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
		s.finishRun("error", err.Error())
		_, _ = fmt.Fprintf(s.logOut, "scheduler sync all failed: duration=%s error=%v\n", time.Since(start).Round(time.Second), err)
		return true
	}
	s.finishRun("ok", "")
	_, _ = fmt.Fprintf(s.logOut, "scheduler sync all finished: duration=%s\n", time.Since(start).Round(time.Second))
	return true
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

func (s *syncScheduler) finishRun(status string, errorText string) {
	s.mu.Lock()
	s.status.LastStatus = status
	s.status.LastError = errorText
	s.status.LastFinishedAt = time.Now().UTC()
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
		return err
	}
	metricsRun, closeMetrics, err := openSyncMetrics(cfg, "scheduler:interval")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeMetrics(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
	if err != nil {
		return err
	}
	defer func() {
		if st != nil {
			_ = st.Close()
		}
	}()

	options, err := syncOptionsFromFlags(ctx, cfg, resolvedFlags, newLogger(false, logOut), logOut)
	if err != nil {
		return err
	}
	options.Metrics = metricsRun
	stats, err := runSyncAll(ctx, cfg, st, options)
	if err != nil {
		_ = st.Close()
		st = nil
		_ = closeMetrics()
		closeMetrics = func() error { return nil }
		return err
	}
	if err := st.Close(); err != nil {
		st = nil
		return err
	}
	st = nil
	if err := closeMetrics(); err != nil {
		closeMetrics = func() error { return nil }
		return err
	}
	closeMetrics = func() error { return nil }

	if err := logScheduledSyncStats(logOut, stats); err != nil {
		return err
	}
	result, err := runConfiguredSemanticRefresh(
		ctx,
		cfg,
		deps,
		func(progress semanticrefresh.Progress) error {
			return writeSemanticRefreshProgress(logOut, progress)
		},
	)
	if err != nil {
		return err
	}
	logScheduledSemanticRefreshResult(logOut, result)
	return nil
}

func logScheduledSyncStats(logOut io.Writer, stats syncjob.Stats) error {
	if logOut == nil {
		return nil
	}
	if stats.XBookmarks != nil {
		_, _ = fmt.Fprintf(logOut, "scheduler sync all stage: x_bookmarks created=%d updated=%d unchanged=%d\n", stats.XBookmarks.Stats.Created, stats.XBookmarks.Stats.Updated, stats.XBookmarks.Stats.Unchanged)
	}
	if stats.X != nil {
		_, _ = fmt.Fprintf(logOut, "scheduler sync all stage: x hydrated=%d rendered=%d missing=%d\n", stats.X.Stats.Hydrated, stats.X.Stats.Rendered, stats.X.Stats.Missing)
	}
	if stats.Links != nil {
		_, _ = fmt.Fprintf(logOut, "scheduler sync all stage: links scanned=%d sources_queued=%d errors=%d\n", stats.Links.Stats.ItemsScanned, stats.Links.Stats.SourcesQueued, stats.Links.Stats.Errors)
	}
	if stats.Sources != nil {
		_, _ = fmt.Fprintf(logOut, "scheduler sync all stage: sources cycles=%d summarized=%d errors=%d\n", stats.Sources.Stats.WorkCycles, stats.Sources.Stats.SourcesSummarized, stats.Sources.Stats.Errors)
	}
	if stats.Categorize != nil {
		_, _ = fmt.Fprintf(logOut, "scheduler sync all stage: categorize succeeded=%d skipped=%d errors=%d\n", stats.Categorize.Stats.Succeeded, stats.Categorize.Stats.Skipped, stats.Categorize.Stats.Errors)
	}
	if stats.OKFExport != nil {
		_, _ = fmt.Fprintf(logOut, "scheduler sync all stage: okf bundle=%s concepts=%d errors=%d\n", stats.OKFExport.Stats.Bundle, stats.OKFExport.Stats.ConceptsWritten, len(stats.OKFExport.Stats.Errors))
	}
	return nil
}

func logScheduledSemanticRefreshResult(logOut io.Writer, result semanticrefresh.Result) {
	if logOut == nil {
		return
	}
	if result.Outcome == semanticrefresh.OutcomeSkipped {
		_, _ = fmt.Fprintf(
			logOut,
			"scheduler semantic refresh: skipped reason=%s capability=%s backend=%s version=%s\n",
			safeSemanticRefreshOutputField(result.SkipReason, 64),
			safeSemanticRefreshOutputField(string(result.Capability.State), 64),
			safeSemanticRefreshOutputField(result.Capability.Backend, 64),
			safeSemanticRefreshOutputField(result.Capability.Version, 64),
		)
		return
	}

	run := result.Run
	if run == nil {
		_, _ = fmt.Fprintf(
			logOut,
			"scheduler semantic refresh: completed capability=%s backend=%s version=%s run= profile= generation= state= stage= readiness=\n",
			safeSemanticRefreshOutputField(string(result.Capability.State), 64),
			safeSemanticRefreshOutputField(result.Capability.Backend, 64),
			safeSemanticRefreshOutputField(result.Capability.Version, 64),
		)
		return
	}
	_, _ = fmt.Fprintf(
		logOut,
		"scheduler semantic refresh: completed capability=%s backend=%s version=%s run=%s profile=%s generation=%s state=%s stage=%s readiness=%s\n",
		safeSemanticRefreshOutputField(string(result.Capability.State), 64),
		safeSemanticRefreshOutputField(result.Capability.Backend, 64),
		safeSemanticRefreshOutputField(result.Capability.Version, 64),
		safeSemanticRefreshOutputField(run.RunID, 64),
		safeSemanticRefreshOutputField(run.ProfileID, 192),
		safeSemanticRefreshOutputField(run.CurrentGenerationID, 64),
		safeSemanticRefreshOutputField(string(run.State), 64),
		safeSemanticRefreshOutputField(string(run.Stage), 64),
		safeSemanticRefreshOutputField(run.ReadinessState, 64),
	)
}
