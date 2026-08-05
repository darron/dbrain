package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
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
	storefixture.PrepareCurrent(t, cfg.DBPath)

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
	storefixture.PrepareCurrent(t, cfg.DBPath)

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
	storefixture.PrepareCurrent(t, cfg.DBPath)

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
	wantEvents := []string{"test.scheduler.metrics", "semantic.refresh.started", "semantic.refresh.completed", "sync.run.completed"}
	if len(events) != len(wantEvents) {
		t.Fatalf("unexpected metrics events: %#v", events)
	}
	for index, want := range wantEvents {
		if events[index]["event"] != want || events[index]["invocation"] != "scheduler:interval" {
			t.Fatalf("metrics event %d = %#v, want %q under scheduler invocation", index, events[index], want)
		}
	}
}

func TestRunScheduledSyncAllSemanticMetricsReuseObservedStart(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: scheduled-semantic-start.jsonl
  strict: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
		return syncjob.Stats{StartedAt: at, CompletedAt: at}, nil
	}
	completedAt := time.Date(2030, 1, 2, 3, 4, 5, 678900000, time.UTC)
	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(semanticconfig.ModeOff), nil
		},
		now: func() time.Time { return completedAt },
	}
	if err := runScheduledSyncAllUnlockedWithSemanticDeps(
		t.Context(), cfg, scheduledSyncSemanticTestFlags(), io.Discard, deps,
	); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "logs", "scheduled-semantic-start.jsonl")
	events := readAppMetricEvents(t, path)
	var startedAt, terminalStartedAt string
	for _, event := range events {
		switch event["event"] {
		case "semantic.refresh.started":
			startedAt, _ = event["started_at"].(string)
		case "semantic.refresh.completed":
			terminalStartedAt, _ = event["started_at"].(string)
		}
	}
	if startedAt == "" || terminalStartedAt == "" || startedAt != terminalStartedAt {
		t.Fatalf("scheduler semantic start mismatch: started=%q terminal=%q", startedAt, terminalStartedAt)
	}
	window, err := metrics.NewReader(path).Read(t.Context(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !window.Semantic.Present || window.Semantic.TerminalIncomplete || window.Semantic.CountersIncomplete || window.Semantic.Latest.State != "skipped" {
		t.Fatalf("scheduled semantic lifecycle=%#v", window.Semantic)
	}
}

func TestRunScheduledSyncAllSourceErrorClosesStoreAndSkipsSemanticRefresh(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	sourceErr := errors.New("source sync failed")
	var sourceStore *store.Store
	runSyncAll = func(_ context.Context, _ config.Config, st *store.Store, _ syncjob.Options) (syncjob.Stats, error) {
		sourceStore = st
		return syncjob.Stats{}, sourceErr
	}
	admissions := 0
	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			admissions++
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
	}

	err = runScheduledSyncAllUnlockedWithSemanticDeps(
		t.Context(),
		cfg,
		scheduledSyncSemanticTestFlags(),
		io.Discard,
		deps,
	)
	if !errors.Is(err, sourceErr) {
		t.Fatalf("scheduled sync error = %v, want source error", err)
	}
	if admissions != 0 {
		t.Fatalf("semantic admissions = %d, want 0", admissions)
	}
	if sourceStore == nil {
		t.Fatal("source sync did not receive a store")
	}
	if _, probeErr := sourceStore.RetrievalPurgeEpoch(t.Context()); probeErr == nil {
		t.Fatal("source store remained open after source failure")
	}
}

func TestRunScheduledSyncAllUnchangedSuccessClosesSourceThenRefreshes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: scheduled-semantic-metrics.jsonl
  strict: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	var sourceStore *store.Store
	var sourceMetrics metrics.RunContext
	runSyncAll = func(_ context.Context, _ config.Config, st *store.Store, options syncjob.Options) (syncjob.Stats, error) {
		sourceStore = st
		sourceMetrics = options.Metrics
		now := time.Unix(1_000, 0).UTC()
		return syncjob.Stats{StartedAt: now, CompletedAt: now}, nil
	}
	refreshes := 0
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		refreshes++
		return completedSyncSemanticResult(), nil
	})
	resolve := deps.resolve
	deps.resolve = func(rootDir string) (semanticconfig.Config, error) {
		if sourceStore == nil {
			t.Fatal("semantic admission ran before source store opened")
		}
		if _, probeErr := sourceStore.RetrievalPurgeEpoch(t.Context()); probeErr == nil {
			t.Fatal("semantic admission observed source store still open")
		}
		if !sourceMetrics.Enabled() {
			t.Fatal("source sync did not receive the enabled metrics sink")
		}
		if emitErr := sourceMetrics.Emit(metrics.Event{"event": "test.semantic_boundary"}); emitErr != nil {
			t.Fatalf("semantic admission did not retain source metrics sink: %v", emitErr)
		}
		return resolve(rootDir)
	}

	var out bytes.Buffer
	if err := runScheduledSyncAllUnlockedWithSemanticDeps(
		t.Context(),
		cfg,
		scheduledSyncSemanticTestFlags(),
		&out,
		deps,
	); err != nil {
		t.Fatalf("scheduled sync: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("semantic refreshes = %d, want 1", refreshes)
	}
	if emitErr := sourceMetrics.Emit(metrics.Event{"event": "test.after_full_completion"}); emitErr == nil {
		t.Fatal("full scheduled sync left metrics sink open")
	}
	if count := strings.Count(out.String(), "scheduler semantic refresh:"); count != 1 {
		t.Fatalf("semantic terminal log lines = %d, want 1:\n%s", count, out.String())
	}
	if !strings.Contains(out.String(), "scheduler semantic refresh: completed") {
		t.Fatalf("semantic completion was not logged explicitly:\n%s", out.String())
	}
}

func TestRunScheduledSyncAllUnsupportedSemanticRefreshLogsOneExplicitSkip(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncjob.Stats{}, nil
	}
	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
		capability: func() semanticindex.Capability {
			return semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
		},
	}

	var out bytes.Buffer
	if err := runScheduledSyncAllUnlockedWithSemanticDeps(
		t.Context(),
		cfg,
		scheduledSyncSemanticTestFlags(),
		&out,
		deps,
	); err != nil {
		t.Fatalf("scheduled sync: %v", err)
	}
	want := "scheduler semantic refresh: skipped reason=native_backend_unsupported capability=unsupported duration="
	if count := strings.Count(out.String(), "scheduler semantic refresh:"); count != 1 {
		t.Fatalf("semantic terminal log lines = %d, want 1:\n%s", count, out.String())
	}
	if !strings.Contains(out.String(), want) {
		t.Fatalf("semantic skip log = %q, want line %q", out.String(), want)
	}
}

func TestRunScheduledSyncAllStreamsBoundedPeriodicSemanticProgress(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncjob.Stats{}, nil
	}
	firstAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	deps := successfulSyncSemanticDeps(func(
		_ context.Context,
		_ semanticrefresh.RunLedger,
		_ semanticrefresh.StageExecutor,
		request semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		if request.Progress == nil {
			t.Fatal("scheduled semantic refresh omitted progress callback")
		}
		for _, progress := range []semanticrefresh.Progress{
			{
				RunID:      "run-progress",
				ProfileID:  "profile-progress",
				Stage:      store.SemanticRefreshEmbedding,
				Checkpoint: "embedding:batch-1",
				Readiness:  "not_ready",
				Counters: store.SemanticRefreshCounters{
					ProjectedParents: 2,
					EmbeddedChunks:   3,
				},
				Debt: semanticrefresh.Debt{
					DirtyParents:      5,
					PendingEmbeddings: 8,
				},
				At: firstAt,
			},
			{
				RunID:      strings.Repeat("r", 65),
				ProfileID:  strings.Repeat("p", 193),
				Stage:      store.SemanticRefreshEmbedding,
				Checkpoint: "unsafe\ncheckpoint",
				Readiness:  "not_ready",
				Counters: store.SemanticRefreshCounters{
					ProjectedParents: 2,
					EmbeddedChunks:   3,
				},
				Debt: semanticrefresh.Debt{
					DirtyParents:      5,
					PendingEmbeddings: 8,
				},
				At: firstAt.Add(semanticrefresh.ProgressInterval),
			},
		} {
			if err := request.Progress(progress); err != nil {
				t.Fatalf("scheduled progress callback: %v", err)
			}
		}
		return completedSyncSemanticResult(), nil
	})

	var out bytes.Buffer
	if err := runScheduledSyncAllUnlockedWithSemanticDeps(
		t.Context(),
		cfg,
		scheduledSyncSemanticTestFlags(),
		&out,
		deps,
	); err != nil {
		t.Fatalf("scheduled sync: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	progressLines := make([]string, 0, 1)
	for _, line := range lines {
		if strings.HasPrefix(line, "Semantic embedding progress:") {
			progressLines = append(progressLines, line)
		}
	}
	if len(progressLines) != 1 {
		t.Fatalf("semantic progress lines = %d, want one initial line with identical five-second snapshot suppressed:\n%s", len(progressLines), out.String())
	}
	for _, line := range progressLines {
		if len(line) > 1024 {
			t.Fatalf("semantic progress line exceeded fixed bound: bytes=%d", len(line))
		}
	}
	for _, want := range []string{
		"==> semantic embedding",
		"Semantic embedding progress: embedded_chunks=3 pending_embeddings=8",
		"Semantic embedding complete: embedded_chunks=3 pending_embeddings=8",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("semantic progress omitted %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "unsafe") || strings.Contains(out.String(), strings.Repeat("r", 65)) {
		t.Fatalf("semantic progress leaked unbounded or unsafe fields:\n%s", out.String())
	}
}

func TestRunScheduledSyncAllSemanticFailuresPreserveStableTypedCodes(t *testing.T) {
	tests := []struct {
		name     string
		context  func() context.Context
		deps     func(*testing.T) semanticRefreshDeps
		wantCode string
	}{
		{
			name:    "supported broken",
			context: context.Background,
			deps: func(*testing.T) semanticRefreshDeps {
				return semanticRefreshDeps{
					resolve: func(string) (semanticconfig.Config, error) {
						return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
					},
					capability: func() semanticindex.Capability {
						return semanticindex.Capability{
							State:   semanticindex.CapabilitySupportedBroken,
							Backend: semanticindex.BackendUSearch,
							Version: semanticindex.USearchVersion,
							Reason:  "private native failure",
						}
					},
				}
			},
			wantCode: semanticrefresh.ErrorBackendBroken,
		},
		{
			name:    "stage failure",
			context: context.Background,
			deps: func(t *testing.T) semanticRefreshDeps {
				run := store.SemanticRefreshRun{
					RunID:      "run-scheduled",
					Stage:      store.SemanticRefreshFlush,
					Checkpoint: "flush:segment",
				}
				refreshErr := semanticrefresh.NewError(
					semanticrefresh.ErrorFlush,
					run,
					"not_ready",
					semanticrefresh.Debt{},
					errors.New("private flush failure"),
				)
				return successfulSyncSemanticDeps(func(
					context.Context,
					semanticrefresh.RunLedger,
					semanticrefresh.StageExecutor,
					semanticrefresh.Request,
				) (semanticrefresh.Result, error) {
					return semanticrefresh.Result{Run: &run}, refreshErr
				})
			},
			wantCode: semanticrefresh.ErrorFlush,
		},
		{
			name: "cancelled",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			deps: func(*testing.T) semanticRefreshDeps {
				return successfulSyncSemanticDeps(func(
					context.Context,
					semanticrefresh.RunLedger,
					semanticrefresh.StageExecutor,
					semanticrefresh.Request,
				) (semanticrefresh.Result, error) {
					return completedSyncSemanticResult(), nil
				})
			},
			wantCode: semanticrefresh.ErrorCancelled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			storefixture.PrepareCurrent(t, cfg.DBPath)
			oldRunSyncAll := runSyncAll
			t.Cleanup(func() { runSyncAll = oldRunSyncAll })
			runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
				return syncjob.Stats{}, nil
			}

			err = runScheduledSyncAllUnlockedWithSemanticDeps(
				test.context(),
				cfg,
				scheduledSyncSemanticTestFlags(),
				io.Discard,
				test.deps(t),
			)
			var refreshErr *semanticrefresh.RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("scheduled sync error = %T %v, want typed RefreshError", err, err)
			}
			if refreshErr.Code != test.wantCode || err.Error() != test.wantCode {
				t.Fatalf("scheduled refresh code = %q error=%q, want %q", refreshErr.Code, err, test.wantCode)
			}
		})
	}
}

func TestSyncSchedulerSemanticFailureSetsErrorStatusAndReleasesLock(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncjob.Stats{}, nil
	}
	run := store.SemanticRefreshRun{RunID: "run-status", Stage: store.SemanticRefreshVerify}
	refreshErr := semanticrefresh.NewError(
		semanticrefresh.ErrorVerify,
		run,
		"not_ready",
		semanticrefresh.Debt{},
		errors.New("private verification failure"),
	)
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		probe, lockErr := acquireSyncAllLock(cfg, "semantic-lock-probe")
		if lockErr == nil {
			_ = probe.Close()
			t.Fatal("scheduler coarse lock was released during semantic refresh")
		}
		if !isSyncAllAlreadyRunning(lockErr) {
			t.Fatalf("semantic lock probe error = %v", lockErr)
		}
		return semanticrefresh.Result{Run: &run}, refreshErr
	})
	var out bytes.Buffer
	s := newSyncScheduler(cfg, schedulerSyncConfig{
		Enabled:  true,
		Interval: time.Hour,
		Flags:    scheduledSyncSemanticTestFlags(),
	}, &out)
	s.runSync = func(ctx context.Context, cfg config.Config, flags syncAllFlags, logOut io.Writer) error {
		return runScheduledSyncAllUnlockedWithSemanticDeps(ctx, cfg, flags, logOut, deps)
	}

	outcome, actual := s.run(t.Context(), "semantic-failure")
	if !actual {
		t.Fatal("semantic failure was not recorded as an actual scheduler run")
	}
	if outcome.Status != scheduledSyncStatusError || !errors.Is(outcome.Err, refreshErr) {
		t.Fatalf("semantic failure outcome = %#v", outcome)
	}
	status := s.Status()
	if status.Running || status.LastStatus != "error" || status.LastError != semanticrefresh.ErrorVerify {
		t.Fatalf("scheduler status after semantic failure = %#v", status)
	}
	if !strings.Contains(out.String(), "scheduler sync all failed:") ||
		!strings.Contains(out.String(), "error="+semanticrefresh.ErrorVerify) {
		t.Fatalf("scheduler semantic failure log omitted stable code:\n%s", out.String())
	}
	lock, err := acquireSyncAllLock(cfg, "post-semantic-failure")
	if err != nil {
		t.Fatalf("scheduler coarse lock remained held after semantic failure: %v", err)
	}
	_ = lock.Close()
}

func TestSyncSchedulerPostRunAuditFollowsSemanticFailureSettlement(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	refreshErr := semanticrefresh.NewError(
		semanticrefresh.ErrorEmbedding,
		store.SemanticRefreshRun{RunID: "run-audit", Stage: store.SemanticRefreshEmbedding},
		"not_ready",
		semanticrefresh.Debt{},
		errors.New("private embedding failure"),
	)
	s := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	s.runSync = func(context.Context, config.Config, syncAllFlags, io.Writer) error {
		return refreshErr
	}
	audits := 0
	s.postRun = func(_ context.Context, outcome scheduledSyncOutcome) {
		audits++
		if outcome.Status != scheduledSyncStatusError || !errors.Is(outcome.Err, refreshErr) {
			t.Errorf("audit outcome = %#v", outcome)
		}
		status := s.Status()
		if status.Running || status.LastStatus != "error" || status.LastError != semanticrefresh.ErrorEmbedding {
			t.Errorf("audit observed unsettled scheduler status: %#v", status)
		}
		lock, lockErr := acquireSyncAllLock(cfg, "post-run-audit")
		if lockErr != nil {
			t.Errorf("audit observed unsettled scheduler lock: %v", lockErr)
			return
		}
		_ = lock.Close()
	}

	s.runAndPost(t.Context(), "semantic-failure")
	if audits != 1 {
		t.Fatalf("post-run audits = %d, want 1", audits)
	}
}

func TestSyncSchedulerPostRunReceivesSettledFailureOutcome(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	s := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	s.runSync = func(context.Context, config.Config, syncAllFlags, io.Writer) error {
		return syncjob.WrapStageError("apple_notes", fs.ErrPermission)
	}
	var got scheduledSyncOutcome
	s.postRun = func(_ context.Context, outcome scheduledSyncOutcome) { got = outcome }

	s.runAndPost(t.Context(), "interval")

	if got.Status != scheduledSyncStatusError || got.Reason != "interval" || got.StartedAt.IsZero() || got.FinishedAt.IsZero() {
		t.Fatalf("outcome = %#v", got)
	}
	if !errors.Is(got.Err, fs.ErrPermission) {
		t.Fatalf("outcome error = %v, want permission cause", got.Err)
	}
	if s.Status().Running {
		t.Fatal("post-run hook ran before settlement")
	}
	lock, err := acquireSyncAllLock(cfg, "outcome-probe")
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
}

func TestSyncSchedulerRunReturnsSettledSuccessOutcome(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	s := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	s.runSync = func(context.Context, config.Config, syncAllFlags, io.Writer) error { return nil }

	outcome, actual := s.run(t.Context(), "interval")
	if !actual || outcome.Status != scheduledSyncStatusOK || outcome.Reason != "interval" || outcome.Err != nil {
		t.Fatalf("success outcome = %#v actual=%t", outcome, actual)
	}
	status := s.Status()
	if status.Running || status.LastStatus != string(scheduledSyncStatusOK) || !status.LastFinishedAt.Equal(outcome.FinishedAt) {
		t.Fatalf("settled success status = %#v outcome=%#v", status, outcome)
	}
}

func TestSyncSchedulerRunMapsCancellationOutcomes(t *testing.T) {
	semanticCancelled := semanticrefresh.NewError(
		semanticrefresh.ErrorCancelled,
		store.SemanticRefreshRun{},
		"",
		semanticrefresh.Debt{},
		nil,
	)
	tests := []struct {
		name string
		err  error
	}{
		{name: "direct context cancellation", err: context.Canceled},
		{name: "wrapped context cancellation", err: fmt.Errorf("sync interrupted: %w", context.Canceled)},
		{name: "semantic cancellation without unwrap cause", err: semanticCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			s := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, &logs)
			s.runSync = func(context.Context, config.Config, syncAllFlags, io.Writer) error { return test.err }

			outcome, actual := s.run(t.Context(), "shutdown")
			if !actual || outcome.Status != scheduledSyncStatusCancelled || !errors.Is(outcome.Err, test.err) {
				t.Fatalf("cancelled outcome = %#v actual=%t", outcome, actual)
			}
			status := s.Status()
			if status.Running || status.LastStatus != string(scheduledSyncStatusCancelled) || !status.LastFinishedAt.Equal(outcome.FinishedAt) {
				t.Fatalf("cancelled status = %#v outcome=%#v", status, outcome)
			}
			if !strings.Contains(logs.String(), "scheduler sync all cancelled:") || strings.Contains(logs.String(), "scheduler sync all failed:") {
				t.Fatalf("cancellation log = %q", logs.String())
			}
		})
	}
}

func TestSyncSchedulerSkipsOverlapWithoutPostRun(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	s.status.Running = true
	called := false
	s.postRun = func(context.Context, scheduledSyncOutcome) { called = true }

	outcome, actual := s.run(t.Context(), "overlap")
	if actual || outcome != (scheduledSyncOutcome{}) {
		t.Fatalf("overlap outcome = %#v actual=%t", outcome, actual)
	}
	s.runAndPost(t.Context(), "overlap")
	if called {
		t.Fatal("overlap skip invoked post-run hook")
	}
}

func TestSyncSchedulerStopWaitsForInflightRunAndPostRun(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	s := newSyncScheduler(cfg, schedulerSyncConfig{
		Enabled:    true,
		Interval:   time.Hour,
		RunOnStart: true,
	}, io.Discard)
	runStarted := make(chan struct{})
	postStarted := make(chan struct{})
	releasePost := make(chan struct{})
	s.runSync = func(ctx context.Context, _ config.Config, _ syncAllFlags, _ io.Writer) error {
		close(runStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	s.postRun = func(_ context.Context, outcome scheduledSyncOutcome) {
		if outcome.Status != scheduledSyncStatusCancelled {
			t.Errorf("shutdown outcome = %#v", outcome)
		}
		if s.Status().Running {
			t.Error("post-run transition began before scheduler status settled")
		}
		close(postStarted)
		<-releasePost
	}

	s.Start(t.Context())
	select {
	case <-runStarted:
	case <-time.After(time.Second):
		t.Fatal("startup run did not begin")
	}
	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()
	select {
	case <-postStarted:
	case <-time.After(time.Second):
		t.Fatal("post-run transition did not begin")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned before post-run transition completed")
	default:
	}
	close(releasePost)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not wait for the settled run")
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
	storefixture.PrepareCurrent(t, cfg.DBPath)

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
	outcome, actual := s.run(context.Background(), "test")
	if !actual || outcome.Status != scheduledSyncStatusOK {
		t.Fatalf("successful outcome = %#v actual=%t", outcome, actual)
	}

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
	outcome, actual := s.run(context.Background(), "test")
	if actual || outcome != (scheduledSyncOutcome{}) {
		t.Fatalf("lock skip outcome = %#v actual=%t", outcome, actual)
	}

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

func scheduledSyncSemanticTestFlags() syncAllFlags {
	return syncAllFlags{
		skipXBookmarks: true,
		skipX:          true,
		skipXMedia:     true,
		skipXPhotoOCR:  true,
		skipLinks:      true,
		skipGitHub:     true,
		skipYouTube:    true,
		skipAppleNotes: true,
		skipSafariTabs: true,
		skipFeeds:      true,
		skipSources:    true,
		skipCategorize: true,
		skipOKFExport:  true,
	}
}
