package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
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
    categorize_model: ollama/test-categorizer
    ocr_model: ollama/test-ocr
    skip_github: true
    skip_youtube: true
    skip_x_bookmarks: true
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
	if !got.Flags.skipGitHub || !got.Flags.skipYouTube || !got.Flags.skipXBookmarks {
		t.Fatalf("expected skip flags from config, got %+v", got.Flags)
	}
	if !got.Flags.watchLater || !got.Flags.liked || !got.Flags.summarize || !got.Flags.categorizeImages {
		t.Fatalf("expected sync all defaults to match CLI defaults, got %+v", got.Flags)
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
