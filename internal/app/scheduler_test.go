package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

func TestSchedulerSyncConfigFromRuntimeReadsConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
scheduler:
  sync_all:
    enabled: true
    interval: 30m
    run_on_start: true
    jitter: 2m
    source_limit: 12
    source_concurrency: 2
    categorize_limit: 5
    categorize_concurrency: 1
    categorize_timeout: 42s
    categorize_model: ollama/test-categorizer
    ocr_model: ollama/test-ocr
    okf_export: true
    skip_categorize_images: true
    skip_apple_notes: true
    skip_github: true
    skip_youtube: true
    skip_x_bookmarks: true
    skip_feeds: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := schedulerSyncConfigFromRuntime(root)
	if err != nil {
		t.Fatalf("schedulerSyncConfigFromRuntime: %v", err)
	}
	if !got.Enabled || !got.RunOnStart {
		t.Fatalf("expected enabled run_on_start config, got %+v", got)
	}
	if got.Interval != 30*time.Minute || got.Jitter != 2*time.Minute {
		t.Fatalf("unexpected interval/jitter: interval=%s jitter=%s", got.Interval, got.Jitter)
	}
	if got.Flags.sourceLimit != 12 || got.Flags.sourceConcurrency != 2 {
		t.Fatalf("unexpected source flags: %+v", got.Flags)
	}
	if got.Flags.categorizeLimit != 5 || got.Flags.categorizeModel != "ollama/test-categorizer" || got.Flags.ocrModel != "ollama/test-ocr" {
		t.Fatalf("unexpected model/categorize flags: %+v", got.Flags)
	}
	if got.Flags.categorizeConcurrency != 1 || got.Flags.categorizeTimeout != 42*time.Second {
		t.Fatalf("unexpected categorize controls: %+v", got.Flags)
	}
	if !got.Flags.skipAppleNotes || !got.Flags.skipGitHub || !got.Flags.skipYouTube || !got.Flags.skipXBookmarks || !got.Flags.skipFeeds {
		t.Fatalf("expected skip flags from config, got %+v", got.Flags)
	}
	if !got.Flags.okfExport {
		t.Fatalf("expected OKF export flag from config, got %+v", got.Flags)
	}
	if !got.Flags.watchLater || !got.Flags.liked || !got.Flags.summarize || got.Flags.categorizeImages {
		t.Fatalf("expected scheduled sync defaults plus image skip override, got %+v", got.Flags)
	}
}

func TestSchedulerSyncMarkersAreContentFreeAndExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "scheduler", Command: "sync all", Invocation: "scheduler:lifecycle", Sink: sink}
	for _, name := range []string{"scheduler.sync.enabled", "scheduler.sync.stopped", "scheduler.sync.lock_skipped", "scheduler.sync.overlap_skipped"} {
		emitSchedulerSyncMarkerEvent(run, name)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range []string{"scheduler.sync.enabled", "scheduler.sync.stopped", "scheduler.sync.lock_skipped", "scheduler.sync.overlap_skipped"} {
		if !strings.Contains(text, name) {
			t.Fatalf("missing marker %q in %s", name, text)
		}
	}
	for _, forbidden := range []string{"previous run still active", "sync all already running", "path", "token", "url"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("marker leaked %q: %s", forbidden, text)
		}
	}
}

func TestRunScheduledSyncAllUsesSyncOptions(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	oldRunSyncAll := runSyncAll
	defer func() {
		runSyncAll = oldRunSyncAll
	}()

	var captured syncjob.Options
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, opts syncjob.Options) (syncjob.Stats, error) {
		captured = opts
		return syncjob.Stats{StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}, nil
	}

	var out bytes.Buffer
	err = runScheduledSyncAll(context.Background(), cfg, syncAllFlags{
		sourceLimit:       7,
		sourceConcurrency: 1,
		skipGitHub:        true,
		skipXPhotoOCR:     true,
		skipCategorize:    true,
		skipYouTube:       true,
		skipXBookmarks:    true,
		skipX:             true,
		skipXMedia:        true,
		skipLinks:         true,
		skipSources:       false,
		okfExport:         true,
		summarize:         false,
	}, &out)
	if err != nil {
		t.Fatalf("runScheduledSyncAll: %v", err)
	}
	if captured.SourceLimit != 7 || captured.SourceConcurrency != 1 {
		t.Fatalf("unexpected source options: %+v", captured)
	}
	if captured.GitHubEnabled || captured.XPhotoOCREnabled || captured.CategorizeEnabled || captured.YouTubeEnabled || captured.XBookmarksEnabled || captured.XEnabled || captured.XMediaEnabled || captured.LinksEnabled {
		t.Fatalf("expected skipped stages to be disabled, got %+v", captured)
	}
	if !captured.SourcesEnabled {
		t.Fatalf("expected sources stage enabled")
	}
	if !captured.OKFExportEnabled {
		t.Fatalf("expected OKF export enabled")
	}
}

func TestRunScheduledSyncAllUsesSharedImportPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
sync_all:
  imports:
    x_bookmarks: false
    github_stars: false
    youtube_watch_later: true
    youtube_liked: false
    feeds: false
    apple_notes: true
    safari_tabs: false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	oldRunSyncAll := runSyncAll
	defer func() {
		runSyncAll = oldRunSyncAll
	}()

	var captured syncjob.Options
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, opts syncjob.Options) (syncjob.Stats, error) {
		captured = opts
		return syncjob.Stats{StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}, nil
	}

	if err := runScheduledSyncAll(context.Background(), cfg, syncAllFlags{
		skipCategorize: true,
		skipLinks:      true,
		skipSources:    true,
	}, io.Discard); err != nil {
		t.Fatalf("runScheduledSyncAll: %v", err)
	}
	if captured.XBookmarksEnabled || captured.XEnabled || captured.XMediaEnabled || captured.XPhotoOCREnabled || captured.GitHubEnabled || captured.FeedsEnabled {
		t.Fatalf("shared import policy did not disable scheduled stages: %+v", captured)
	}
	if !captured.YouTubeEnabled || !captured.WatchLater || captured.Liked {
		t.Fatalf("shared YouTube selection was not applied to scheduled sync: %+v", captured)
	}
	if !captured.AppleNotesEnabled || captured.SafariTabsEnabled {
		t.Fatalf("shared local import selection was not applied to scheduled sync: %+v", captured)
	}
}

func TestRunScheduledSyncAllUsesSchedulerMetricsInvocation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: scheduled-metrics.jsonl
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	oldRunSyncAll := runSyncAll
	defer func() {
		runSyncAll = oldRunSyncAll
	}()

	var captured syncjob.Options
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, opts syncjob.Options) (syncjob.Stats, error) {
		captured = opts
		if !opts.Metrics.Enabled() {
			t.Fatal("expected metrics enabled")
		}
		if opts.Metrics.Invocation != "scheduler:interval" {
			t.Fatalf("metrics invocation = %q, want scheduler:interval", opts.Metrics.Invocation)
		}
		if err := opts.Metrics.Emit(map[string]any{"event": "test.scheduler.metrics", "status": "ok"}); err != nil {
			t.Fatalf("emit metrics: %v", err)
		}
		now := time.Now().UTC()
		return syncjob.Stats{StartedAt: now, CompletedAt: now}, nil
	}

	var out bytes.Buffer
	err = runScheduledSyncAll(context.Background(), cfg, syncAllFlags{
		skipGitHub:     true,
		skipXPhotoOCR:  true,
		skipCategorize: true,
		skipYouTube:    true,
		skipXBookmarks: true,
		skipX:          true,
		skipXMedia:     true,
		skipLinks:      true,
		skipSources:    true,
	}, &out)
	if err != nil {
		t.Fatalf("runScheduledSyncAll: %v", err)
	}
	if captured.Metrics.RunID == "" || captured.Metrics.Command != "sync all" {
		t.Fatalf("unexpected metrics context: %+v", captured.Metrics)
	}
	events := readAppMetricEvents(t, filepath.Join(root, "logs", "scheduled-metrics.jsonl"))
	if len(events) != 1 || events[0]["invocation"] != "scheduler:interval" {
		t.Fatalf("unexpected metrics events: %#v", events)
	}
}

func TestSyncSchedulerStatusTracksRuns(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	oldRunSyncAll := runSyncAll
	defer func() {
		runSyncAll = oldRunSyncAll
	}()
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, _ syncjob.Options) (syncjob.Stats, error) {
		return syncjob.Stats{StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}, nil
	}

	s := newSyncScheduler(cfg, schedulerSyncConfig{
		Enabled:    true,
		Interval:   time.Hour,
		RunOnStart: true,
		Flags: syncAllFlags{
			skipGitHub:     true,
			skipXPhotoOCR:  true,
			skipCategorize: true,
		},
	}, io.Discard)
	before := s.Status()
	if !before.Enabled || before.Interval != "1h0m0s" || !before.RunOnStart {
		t.Fatalf("unexpected initial status: %#v", before)
	}

	s.setNextRunAt(time.Now().UTC().Add(time.Hour))
	s.run(context.Background(), "test")

	after := s.Status()
	if after.Running {
		t.Fatalf("expected scheduler idle after run: %#v", after)
	}
	if after.LastReason != "test" || after.LastStatus != "ok" || after.LastStartedAt.IsZero() || after.LastFinishedAt.IsZero() || after.NextRunAt.IsZero() {
		t.Fatalf("unexpected status after run: %#v", after)
	}
}

func TestSyncSchedulerSkipsWhenSyncAllLockHeld(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	lock, err := acquireSyncAllLock(cfg, "test")
	if err != nil {
		t.Fatalf("acquireSyncAllLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	var out bytes.Buffer
	s := newSyncScheduler(cfg, schedulerSyncConfig{
		Enabled:  true,
		Interval: time.Hour,
	}, &out)
	s.run(context.Background(), "test")

	after := s.Status()
	if after.Running {
		t.Fatalf("expected scheduler idle after skipped run: %#v", after)
	}
	if after.LastReason != "test" || after.LastStatus != "skipped" || !strings.Contains(after.LastError, "sync all already running") {
		t.Fatalf("unexpected skipped status: %#v", after)
	}
	if !strings.Contains(out.String(), "scheduler sync all skipped") {
		t.Fatalf("expected skip log, got %q", out.String())
	}
}

func TestSyncSchedulerPrefixesLogLinesWithTimestamps(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	lock, err := acquireSyncAllLock(cfg, "test")
	if err != nil {
		t.Fatalf("acquireSyncAllLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	var out bytes.Buffer
	s := newSyncScheduler(cfg, schedulerSyncConfig{
		Enabled:  true,
		Interval: time.Hour,
	}, &out)
	s.run(context.Background(), "test")

	got := out.String()
	want := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2}) scheduler sync all skipped:`)
	if !want.MatchString(got) {
		t.Fatalf("expected scheduler log line to start with timestamp, got %q", got)
	}
}
