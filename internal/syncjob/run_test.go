package syncjob

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/bskyapi"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
	"github.com/darron/dbrain/internal/youtubeimport"
)

func TestEmitSyncImportMetricsEmitsAllEightContentFreeFamilies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "sync_imports", Command: "sync all", Invocation: "scheduler:interval", Sink: sink}
	stats := Stats{
		AppleNotes:       &AppleNotesStage{Duration: time.Second, Stats: applenotes.Stats{NotesCreated: 1}},
		SafariTabs:       &SafariTabsStage{Duration: time.Second, Stats: safaritabs.Stats{TabsCreated: 1}},
		BlueskyBookmarks: &BlueskyBookmarksStage{Duration: time.Second, Stats: bskyapi.BookmarkStats{Created: 1}},
		XBookmarks:       &XBookmarksStage{Duration: time.Second, Stats: xapi.BookmarkStats{Created: 1}},
		GitHub:           &GitHubStage{Duration: time.Second, Stats: githubimport.Stats{ItemsCreated: 1}},
		YouTube:          &YouTubeStage{Duration: time.Second, Stats: youtubeimport.Stats{WatchLater: youtubeimport.FeedStats{ItemsCreated: 1}, Liked: youtubeimport.FeedStats{ItemsUpdated: 1}}},
		Feeds:            &FeedsStage{Duration: time.Second, Stats: feedimport.Stats{ItemsCreated: 1}},
	}
	emitSyncImportMetrics(run, stats, nil)
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	events := readSyncMetricEvents(t, path)
	if len(events) != 8 {
		t.Fatalf("events = %d, want 8", len(events))
	}
	want := []string{"apple_notes", "safari_tabs", "bluesky_bookmarks", "x_bookmarks", "github_stars", "youtube_watch_later", "youtube_liked", "feeds"}
	for i, event := range events {
		if event["event"] != "sync.import.completed" || event["source"] != want[i] {
			t.Fatalf("event[%d] = %#v", i, event)
		}
		raw, _ := json.Marshal(event)
		for _, forbidden := range []string{"title", "url", "source_key", "path", "token", "error"} {
			if strings.Contains(strings.ToLower(string(raw)), forbidden) {
				t.Fatalf("event leaked forbidden field %q: %s", forbidden, raw)
			}
		}
	}
}

func TestEmitSyncImportMetricRecordsFailureWithoutStageStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "failed_import", Command: "sync all", Sink: sink}
	opts := newStageOptions(Options{GitHubEnabled: true})
	emitSyncImportStageMetric(run, opts, syncStageGitHub, Stats{}, errors.New("failed before stats"))
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	events := readSyncMetricEvents(t, path)
	if len(events) != 1 || events[0]["source"] != "github_stars" || events[0]["status"] != "error" {
		t.Fatalf("events = %#v", events)
	}
}

func TestEmitYouTubeImportMetricsOnlyForSelectedFeedsWithPerFeedStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: path, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatal(err)
	}
	run := metrics.RunContext{RunID: "youtube_import", Command: "sync all", Sink: sink}
	opts := newStageOptions(Options{YouTubeEnabled: true, WatchLater: true, Liked: false})
	stats := Stats{YouTube: &YouTubeStage{Duration: time.Second, Stats: youtubeimport.Stats{WatchLater: youtubeimport.FeedStats{Errors: 1}, Liked: youtubeimport.FeedStats{ItemsCreated: 5}}}}
	emitSyncImportStageMetric(run, opts, syncStageYouTube, stats, nil)
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	events := readSyncMetricEvents(t, path)
	if len(events) != 1 || events[0]["source"] != "youtube_watch_later" || events[0]["status"] != "error" {
		t.Fatalf("events = %#v", events)
	}
}

func TestRunEmitsMetricsRunAndStageEvents(t *testing.T) {
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXMediaStage = origXMedia
	})
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		if opts.SummaryModel != "test-summary" {
			t.Fatalf("SummaryModel = %q, want test-summary", opts.SummaryModel)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 2, ItemsSummarized: 1, Errors: 0}, nil
	}

	_, err = Run(context.Background(), cfg, st, Options{
		XMediaEnabled: true,
		Summarize:     true,
		Model:         "test-summary",
		Metrics: metrics.RunContext{
			RunID:      "sync_test_00000000",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{"sync.run.started", "sync.stage.completed", "sync.run.completed"}) {
		t.Fatalf("metric events = %v", got)
	}
	stage := events[1]
	if stage["stage"] != "x_media" || stage["status"] != "ok" {
		t.Fatalf("unexpected stage event: %#v", stage)
	}
	counts := stage["counts"].(map[string]any)
	if counts["items_processed"] != float64(2) || counts["items_summarized"] != float64(1) {
		t.Fatalf("unexpected stage counts: %#v", counts)
	}
	if events[0]["run_id"] != "sync_test_00000000" || events[0]["invocation"] != "cli" {
		t.Fatalf("unexpected run envelope: %#v", events[0])
	}
}

func TestRunParentOwnsCompletionEmitsPipelineBoundaryOnly(t *testing.T) {
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	_, err = Run(t.Context(), cfg, st, Options{
		ParentOwnsRunCompletion: true,
		Metrics: metrics.RunContext{
			RunID:      "sync_parent_owned",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{"sync.run.started", "sync.pipeline.completed"}) {
		t.Fatalf("metric events = %v, want parent-owned pipeline boundary without final run completion", got)
	}
	if events[1]["status"] != "ok" {
		t.Fatalf("pipeline status = %#v, want ok", events[1]["status"])
	}
}

func TestRunEmitsResolvedModelConfigInMetrics(t *testing.T) {
	t.Setenv("DBRAIN_CATEGORIZE_MODEL", "ollama/test-categorize")
	t.Setenv("DBRAIN_OCR_MODEL", "ollama/test-ocr")
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	origOCR := runXPhotoOCRStage
	origItems := runItemCategorize
	origSources := runSourceCategorize
	t.Cleanup(func() {
		runXPhotoOCRStage = origOCR
		runItemCategorize = origItems
		runSourceCategorize = origSources
	})

	runXPhotoOCRStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xphotoocr.Options) (xphotoocr.Stats, error) {
		if opts.Model != "ollama/test-ocr" {
			t.Fatalf("ocr model = %q, want resolved env model", opts.Model)
		}
		return xphotoocr.Stats{ItemsProcessed: 1, PhotosOCRed: 1}, nil
	}
	runItemCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		if opts.Model != "ollama/test-categorize" {
			t.Fatalf("item categorize model = %q, want resolved env model", opts.Model)
		}
		return itemcategorize.Stats{Queued: 1, Succeeded: 1, Applied: 1}, nil, nil
	}
	runSourceCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		if opts.Model != "ollama/test-categorize" {
			t.Fatalf("source categorize model = %q, want resolved env model", opts.Model)
		}
		return itemcategorize.Stats{}, nil, nil
	}

	_, err = Run(context.Background(), cfg, st, Options{
		XPhotoOCREnabled:  true,
		CategorizeEnabled: true,
		Metrics: metrics.RunContext{
			RunID:      "sync_test_00000000",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{
		"sync.run.started",
		"sync.stage.completed",
		"sync.stage.completed",
		"sync.run.completed",
	}) {
		t.Fatalf("metric events = %v", got)
	}
	runConfig := events[0]["config"].(map[string]any)
	if runConfig["categorize_model"] != "ollama/test-categorize" {
		t.Fatalf("run categorize_model = %#v, want resolved model", runConfig["categorize_model"])
	}
	ocrConfig := events[1]["config"].(map[string]any)
	if events[1]["stage"] != "x_photo_ocr" || ocrConfig["ocr_model"] != "ollama/test-ocr" {
		t.Fatalf("unexpected ocr metric: %#v", events[1])
	}
	categorizeConfig := events[2]["config"].(map[string]any)
	if events[2]["stage"] != "categorize" || categorizeConfig["categorize_model"] != "ollama/test-categorize" {
		t.Fatalf("unexpected categorize metric: %#v", events[2])
	}
}

func TestRunEmitsCategorizationDetailMetrics(t *testing.T) {
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailItem})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	origItems := runItemCategorize
	origSources := runSourceCategorize
	t.Cleanup(func() {
		runItemCategorize = origItems
		runSourceCategorize = origSources
	})

	runItemCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		result := itemcategorize.ItemResult{
			Item: model.Item{
				ID:        42,
				SourceKey: "x:secret-item-key",
			},
			Result: itemcategorize.Result{
				Model:      "omlx/qwen3.5-coder",
				Provider:   "omlx",
				APIModel:   "qwen3.5-coder",
				Transport:  "openai_chat_completions",
				Tool:       "omlx-direct",
				Categories: []string{"private-category"},
				Tags:       []string{"private-tag"},
			},
			Duration: 1500 * time.Millisecond,
		}
		if opts.OnStart != nil {
			opts.OnStart(1)
		}
		if opts.OnResult != nil {
			opts.OnResult(result)
		}
		return itemcategorize.Stats{Queued: 1, Succeeded: 1, Applied: 1}, []itemcategorize.ItemResult{result}, nil
	}
	runSourceCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		result := itemcategorize.SourceResult{
			Source: model.SourceDocument{
				ID:        77,
				SourceKey: "src:secret-source-key",
			},
			Result: itemcategorize.Result{
				Model:      "omlx/qwen3.5-coder",
				Provider:   "omlx",
				APIModel:   "qwen3.5-coder",
				Transport:  "openai_chat_completions",
				Tool:       "omlx-direct",
				Categories: []string{"private-source-category"},
				Tags:       []string{"private-source-tag"},
			},
			Duration: 2200 * time.Millisecond,
		}
		if opts.OnStart != nil {
			opts.OnStart(1)
		}
		if opts.OnSourceResult != nil {
			opts.OnSourceResult(result)
		}
		return itemcategorize.Stats{Queued: 1, Succeeded: 1, Applied: 1}, []itemcategorize.SourceResult{result}, nil
	}

	_, err = Run(context.Background(), cfg, st, Options{
		CategorizeEnabled: true,
		CategorizeModel:   "omlx/qwen3.5-coder",
		Metrics: metrics.RunContext{
			RunID:      "sync_test_00000000",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{
		"sync.run.started",
		"categorize.item.completed",
		"categorize.source.completed",
		"sync.stage.completed",
		"sync.run.completed",
	}) {
		t.Fatalf("metric events = %v", got)
	}
	itemEvent := events[1]
	if itemEvent["subject_key"] != nil || itemEvent["subject_kind"] != "item" || itemEvent["model"] != "omlx/qwen3.5-coder" || itemEvent["duration_ms"] != float64(1500) {
		t.Fatalf("unexpected item metric: %#v", itemEvent)
	}
	output := itemEvent["output"].(map[string]any)
	if output["categories"] != float64(1) || output["tags"] != float64(1) {
		t.Fatalf("unexpected output counts: %#v", output)
	}
	raw, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	for _, forbidden := range []string{"x:secret-item-key", "src:secret-source-key", "private-tag", "private-category", "private-source-tag", "private-source-category"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("metrics leaked %q:\n%s", forbidden, raw)
		}
	}
}

func TestRunEmitsSourceSummaryDetailMetrics(t *testing.T) {
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailItem})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	origWorker := runSourceWorker
	origPending := runSourceEnrichPending
	t.Cleanup(func() {
		runSourceWorker = origWorker
		runSourceEnrichPending = origPending
	})

	runSourceWorker = func(ctx context.Context, _ worker.SourceBacklogFunc, process worker.SourceRunFunc, _ worker.SourceOptions) (worker.SourceStats, error) {
		stats, err := process(ctx, 1)
		return worker.SourceStats{WorkCycles: 1, SourcesSummarized: stats.SourcesSummarized}, err
	}
	runSourceEnrichPending = func(_ context.Context, _ config.Config, _ *store.Store, opts sourceenrich.Options) (sourceenrich.Stats, []int64, error) {
		if opts.OnSourceResult == nil {
			t.Fatal("expected source result callback")
		}
		opts.OnSourceResult(sourceenrich.SourceResult{
			SourceID:         99,
			SourceKey:        "src:secret-summary-key",
			Duration:         2500 * time.Millisecond,
			SummaryCreated:   true,
			SummaryStatus:    "ok",
			SummaryModel:     "omlx/qwen3.5-coder",
			SummaryTool:      "omlx-direct",
			SummaryChars:     1234,
			SummaryProvider:  "omlx",
			SummaryAPIModel:  "qwen3.5-coder",
			SummaryTransport: "openai_chat_completions",
		})
		return sourceenrich.Stats{SourcesQueued: 1, SourcesSummarized: 1}, []int64{99}, nil
	}

	_, err = Run(context.Background(), cfg, st, Options{
		SourcesEnabled: true,
		Summarize:      true,
		Model:          "omlx/qwen3.5-coder",
		Metrics: metrics.RunContext{
			RunID:      "sync_test_00000000",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{
		"sync.run.started",
		"summary.source.completed",
		"sync.stage.completed",
		"sync.run.completed",
	}) {
		t.Fatalf("metric events = %v", got)
	}
	sourceEvent := events[1]
	if sourceEvent["subject_key"] != nil || sourceEvent["subject_kind"] != "source" || sourceEvent["model"] != "omlx/qwen3.5-coder" || sourceEvent["duration_ms"] != float64(2500) {
		t.Fatalf("unexpected source metric: %#v", sourceEvent)
	}
	output := sourceEvent["output"].(map[string]any)
	if output["summary_chars"] != float64(1234) {
		t.Fatalf("unexpected source output counts: %#v", output)
	}
	raw, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if strings.Contains(string(raw), "src:secret-summary-key") {
		t.Fatalf("metrics leaked raw source key:\n%s", raw)
	}
}

func TestRunSkipsSourceSummaryMetricWhenSummaryNotCreated(t *testing.T) {
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailItem})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	origWorker := runSourceWorker
	origPending := runSourceEnrichPending
	t.Cleanup(func() {
		runSourceWorker = origWorker
		runSourceEnrichPending = origPending
	})

	runSourceWorker = func(ctx context.Context, _ worker.SourceBacklogFunc, process worker.SourceRunFunc, _ worker.SourceOptions) (worker.SourceStats, error) {
		stats, err := process(ctx, 1)
		return worker.SourceStats{WorkCycles: 1, SourcesSummarized: stats.SourcesSummarized}, err
	}
	runSourceEnrichPending = func(_ context.Context, _ config.Config, _ *store.Store, opts sourceenrich.Options) (sourceenrich.Stats, []int64, error) {
		if opts.OnSourceResult == nil {
			t.Fatal("expected source result callback")
		}
		opts.OnSourceResult(sourceenrich.SourceResult{
			SourceID:       99,
			SourceKey:      "src:secret-summary-key",
			Duration:       2500 * time.Millisecond,
			SummaryCreated: false,
			SummaryStatus:  "skipped",
			SummaryModel:   "omlx/qwen3.5-coder",
			SummaryTool:    "omlx-direct",
		})
		return sourceenrich.Stats{SourcesQueued: 1}, []int64{99}, nil
	}

	_, err = Run(context.Background(), cfg, st, Options{
		SourcesEnabled: true,
		Summarize:      true,
		Model:          "omlx/qwen3.5-coder",
		Metrics: metrics.RunContext{
			RunID:      "sync_test_00000000",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{
		"sync.run.started",
		"sync.stage.completed",
		"sync.run.completed",
	}) {
		t.Fatalf("metric events = %v", got)
	}
}

func TestRunEmitsCategorizeStageMetricsOnError(t *testing.T) {
	cfg, st := testSyncStore(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	sink, err := metrics.Open(metrics.Config{Enabled: true, Path: metricsPath, Detail: metrics.DetailStage})
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}

	origItems := runItemCategorize
	origSources := runSourceCategorize
	t.Cleanup(func() {
		runItemCategorize = origItems
		runSourceCategorize = origSources
	})

	runItemCategorize = func(_ context.Context, _ config.Config, _ *store.Store, _ itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		return itemcategorize.Stats{Queued: 1, Succeeded: 1, Applied: 1}, nil, nil
	}
	cause := errors.New("source categorize failed")
	runSourceCategorize = func(_ context.Context, _ config.Config, _ *store.Store, _ itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		return itemcategorize.Stats{Queued: 1, Errors: 1}, nil, cause
	}

	_, err = Run(context.Background(), cfg, st, Options{
		CategorizeEnabled: true,
		CategorizeModel:   "omlx/qwen3.5-coder",
		Metrics: metrics.RunContext{
			RunID:      "sync_test_00000000",
			Command:    "sync all",
			Invocation: "cli",
			Sink:       sink,
		},
	})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage() != "categorize" || !errors.Is(err, cause) {
		t.Fatalf("Run stage err = %#v, want categorize identity and original cause", err)
	}
	if err.Error() != "categorize sources: source categorize failed" {
		t.Fatalf("Run err = %v, want existing categorized error text", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("metrics close: %v", err)
	}

	events := readSyncMetricEvents(t, metricsPath)
	if got := metricEventNames(events); !slices.Equal(got, []string{
		"sync.run.started",
		"sync.stage.completed",
		"sync.run.completed",
	}) {
		t.Fatalf("metric events = %v", got)
	}
	stage := events[1]
	if stage["stage"] != "categorize" || stage["status"] != "error" {
		t.Fatalf("unexpected stage metric: %#v", stage)
	}
	counts := events[2]["counts"].(map[string]any)
	if counts["stages_error"] != float64(1) {
		t.Fatalf("unexpected run counts: %#v", counts)
	}
}

func TestRunExecutesXMediaStageAfterXHydration(t *testing.T) {
	cfg, st := testSyncStore(t)

	origBookmarks := runXBookmarkImport
	origX := runXHydrate
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXBookmarkImport = origBookmarks
		runXHydrate = origX
		runXMediaStage = origXMedia
	})

	var calls []string
	runXBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.BookmarkOptions) (xapi.BookmarkStats, error) {
		calls = append(calls, "x-bookmarks")
		if opts.Limit != 9 {
			t.Fatalf("expected x bookmark limit 9, got %d", opts.Limit)
		}
		return xapi.BookmarkStats{Created: 2, PagesFetched: 1, StoppedReason: "end of bookmarks"}, nil
	}
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.Limit != 7 {
			t.Fatalf("expected x limit 7, got %d", opts.Limit)
		}
		if opts.MediaTimeout != 45*time.Minute {
			t.Fatalf("expected x media timeout 45m, got %s", opts.MediaTimeout)
		}
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 7, Hydrated: 7, Rendered: 7}, nil
	}
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		if opts.Limit != 7 {
			t.Fatalf("expected x media limit 7, got %d", opts.Limit)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 3, ItemsUpdated: 2, MediaTranscribed: 2}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XBookmarksEnabled: true,
		XBookmarksLimit:   9,
		XEnabled:          true,
		XLimit:            7,
		XMediaTimeout:     45 * time.Minute,
		XMediaEnabled:     true,
		Progress:          &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-quote", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.XBookmarks == nil {
		t.Fatal("expected x bookmark stage stats")
	}
	if stats.X == nil {
		t.Fatal("expected x stage stats")
	}
	if stats.XMedia == nil {
		t.Fatal("expected x media stage stats")
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> transcribe x-media")) {
		t.Fatalf("expected progress output to contain x media stage, got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("X media transcription complete")) {
		t.Fatalf("expected completion output to contain x media summary, got %q", output)
	}
}

func TestRunPassesResolvedSummaryModelToXMediaStage(t *testing.T) {
	cfg, st := testSyncStore(t)
	if err := os.WriteFile(cfg.ConfigPath, []byte(`
summary:
  model: "omlx/Qwen3.6-35B-A3B-MLX-4bit"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXMediaStage = origXMedia
	})

	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		if opts.SummaryModel != "omlx/Qwen3.6-35B-A3B-MLX-4bit" {
			t.Fatalf("expected resolved summary model, got %q", opts.SummaryModel)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 1, ItemsSummarized: 1}, nil
	}

	if _, err := Run(context.Background(), cfg, st, Options{
		XMediaEnabled: true,
		Summarize:     true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunUsesSeparateXMediaAndPhotoOCRLimits(t *testing.T) {
	cfg, st := testSyncStore(t)

	origXMedia := runXMediaStage
	origXPhotoOCR := runXPhotoOCRStage
	t.Cleanup(func() {
		runXMediaStage = origXMedia
		runXPhotoOCRStage = origXPhotoOCR
	})

	var calls []string
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		if opts.Limit != 3 {
			t.Fatalf("expected x media limit 3, got %d", opts.Limit)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 3}, nil
	}
	runXPhotoOCRStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xphotoocr.Options) (xphotoocr.Stats, error) {
		calls = append(calls, "x-photo-ocr")
		if opts.Limit != 5 {
			t.Fatalf("expected x photo OCR limit 5, got %d", opts.Limit)
		}
		return xphotoocr.Stats{ItemsProcessed: 5}, nil
	}

	stats, err := Run(context.Background(), cfg, st, Options{
		XLimit:           7,
		XMediaEnabled:    true,
		XMediaLimit:      3,
		XPhotoOCREnabled: true,
		XPhotoOCRLimit:   5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-media", "x-photo-ocr"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.XMedia == nil || stats.XMedia.Stats.ItemsProcessed != 3 {
		t.Fatalf("expected x media stats, got %+v", stats.XMedia)
	}
	if stats.XPhotoOCR == nil || stats.XPhotoOCR.Stats.ItemsProcessed != 5 {
		t.Fatalf("expected x photo OCR stats, got %+v", stats.XPhotoOCR)
	}
}

func TestRunSocialOnlyImportsReachSharedMediaWorkersWithXDisabled(t *testing.T) {
	cfg, st := testSyncStore(t)
	t.Setenv("DBRAIN_TEST_MASTODON_TOKEN", "test-token")
	restoreConfig := runtimeenv.RegisterConfigSnapshot(cfg.RootDir, map[string]any{
		"mastodon": map[string]any{
			"enabled": true,
			"accounts": map[string]any{
				"test": map[string]any{
					"origin":            "https://mastodon.example",
					"access_token_ref":  "env:DBRAIN_TEST_MASTODON_TOKEN",
					"client_id_ref":     "env:DBRAIN_TEST_MASTODON_TOKEN",
					"client_secret_ref": "env:DBRAIN_TEST_MASTODON_TOKEN",
				},
			},
		},
	}, nil)
	t.Cleanup(restoreConfig)

	origBluesky := runBlueskyBookmarkImport
	origMastodon := runMastodonImport
	origX := runXHydrate
	origXMedia := runXMediaStage
	origOCR := runXPhotoOCRStage
	t.Cleanup(func() {
		runBlueskyBookmarkImport = origBluesky
		runMastodonImport = origMastodon
		runXHydrate = origX
		runXMediaStage = origXMedia
		runXPhotoOCRStage = origOCR
	})

	var calls []string
	runBlueskyBookmarkImport = func(ctx context.Context, _ config.Config, gotStore *store.Store, _ bskyapi.BookmarkOptions) (bskyapi.BookmarkStats, error) {
		calls = append(calls, "bluesky-import")
		seedSocialMediaWorkerFixture(t, ctx, gotStore, "bsky:fixture", "bsky_bookmark", "bsky")
		return bskyapi.BookmarkStats{Created: 1, MediaDownloaded: 2}, nil
	}
	runMastodonImport = func(ctx context.Context, _ config.Config, gotStore *store.Store, _ *mastodonapi.Client, _ mastodonapi.BookmarkOptions) (mastodonapi.BookmarkStats, error) {
		calls = append(calls, "mastodon-import")
		seedSocialMediaWorkerFixture(t, ctx, gotStore, "mastodon:fixture", "mastodon_bookmark", "mastodon")
		return mastodonapi.BookmarkStats{AccountKey: "test", Created: 1, MediaDownloaded: 2}, nil
	}
	runXHydrate = func(context.Context, config.Config, *store.Store, xapi.Options) (xapi.Stats, error) {
		t.Fatal("X hydration ran during social-only sync")
		return xapi.Stats{}, nil
	}
	runXMediaStage = func(ctx context.Context, _ config.Config, gotStore *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "transcription")
		items, err := gotStore.ListItemsForXMediaTranscription(ctx, 10, false)
		if err != nil {
			t.Fatalf("ListItemsForXMediaTranscription: %v", err)
		}
		if got := itemSourceKeys(items); !slices.Equal(got, []string{"mastodon:fixture", "bsky:fixture"}) {
			t.Fatalf("transcription worker fixtures = %v", got)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 2, MediaTranscribed: 2}, nil
	}
	runXPhotoOCRStage = func(ctx context.Context, _ config.Config, gotStore *store.Store, _ xphotoocr.Options) (xphotoocr.Stats, error) {
		calls = append(calls, "ocr")
		items, err := gotStore.ListItemsForXPhotoOCR(ctx, 10, false)
		if err != nil {
			t.Fatalf("ListItemsForXPhotoOCR: %v", err)
		}
		if got := itemSourceKeys(items); !slices.Equal(got, []string{"mastodon:fixture", "bsky:fixture"}) {
			t.Fatalf("OCR worker fixtures = %v", got)
		}
		return xphotoocr.Stats{ItemsProcessed: 2, PhotosOCRed: 2}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(t.Context(), cfg, st, Options{
		BlueskyBookmarksEnabled:  true,
		MastodonBookmarksEnabled: true,
		XMediaEnabled:            true,
		XPhotoOCREnabled:         true,
		Progress:                 &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"bluesky-import", "mastodon-import", "transcription", "ocr"}) {
		t.Fatalf("social-only stage calls = %v", calls)
	}
	if stats.BlueskyBookmarks == nil || stats.BlueskyBookmarks.Stats.Created != 1 ||
		stats.MastodonBookmarks == nil || stats.MastodonBookmarks.Stats.Created != 1 ||
		stats.XMedia == nil || stats.XMedia.Stats.MediaTranscribed != 2 ||
		stats.XPhotoOCR == nil || stats.XPhotoOCR.Stats.PhotosOCRed != 2 {
		t.Fatalf("typed social-only stats were not preserved: %+v", stats)
	}
	for _, want := range []string{"==> import bluesky-bookmarks", "==> import mastodon-bookmarks", "==> transcribe x-media", "==> ocr x-photos"} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("progress missing %q:\n%s", want, progress.String())
		}
	}
}

func seedSocialMediaWorkerFixture(t *testing.T, ctx context.Context, st *store.Store, sourceKey, sourceType, namespace string) {
	t.Helper()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey: sourceKey, SourceType: sourceType, ExternalID: sourceKey,
		CanonicalURL: "https://social.example/" + sourceKey, Title: sourceKey,
		ContentHash: sourceKey + "-hash", LinksJSON: "[]", NotePath: "items/" + namespace + "/fixture.md", RawJSON: "{}",
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}
	if _, err := st.SaveItemMediaCandidates(ctx, item.ItemID, []model.MediaCandidate{
		{RemoteURL: "https://media.example/" + namespace + "/photo.jpg", MediaType: "photo"},
		{RemoteURL: "https://media.example/" + namespace + "/video.mp4", MediaType: "video"},
	}); err != nil {
		t.Fatalf("SaveItemMediaCandidates %s: %v", sourceKey, err)
	}
	refs, err := st.ListItemMediaRefs(ctx, item.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs %s: %v", sourceKey, err)
	}
	for _, ref := range refs {
		ext := ".jpg"
		if ref.MediaType == "video" {
			ext = ".mp4"
		}
		if _, err := st.SaveMediaDownload(ctx, ref.MediaAssetID, model.MediaDownloadResult{
			LocalPath:   "media/" + namespace + "/" + ref.MediaType + "/fixture" + ext,
			ContentHash: sourceKey + "-" + ref.MediaType,
			Status:      model.MediaDownloadStatusDownloaded, DownloadedAt: now,
		}); err != nil {
			t.Fatalf("SaveMediaDownload %s/%s: %v", sourceKey, ref.MediaType, err)
		}
	}
}

func itemSourceKeys(items []model.Item) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.SourceKey)
	}
	return keys
}

func TestRunDrainsQuoteHydrationTailBeforeXMediaStage(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXHydrate = origX
		runXMediaStage = origXMedia
	})

	var calls []string
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			if len(calls) == 2 {
				return xapi.Stats{Candidates: 2, Hydrated: 2, Requested: 1, Rendered: 1}, nil
			}
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 4, Hydrated: 4, Requested: 2, Rendered: 3}, nil
	}
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		return xmediatranscribe.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XEnabled:      true,
		XMediaEnabled: true,
		Progress:      &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x", "x-quote", "x-quote", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.X == nil {
		t.Fatal("expected x stage stats")
	}
	if stats.X.Stats.Hydrated != 6 {
		t.Fatalf("expected aggregated hydrated count 6, got %d", stats.X.Stats.Hydrated)
	}
	if stats.X.Stats.Requested != 3 {
		t.Fatalf("expected aggregated requested count 3, got %d", stats.X.Stats.Requested)
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("X quote hydration pass 1 complete")) {
		t.Fatalf("expected progress output to contain quote drain pass, got %q", output)
	}
}

func TestRunStopsQuoteHydrationTailWhenOnlyRetryingUnchangedMediaErrors(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	t.Cleanup(func() {
		runXHydrate = origX
	})

	var calls []string
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{
				Candidates:      1,
				Hydrated:        1,
				Unchanged:       1,
				MediaCandidates: 1,
				MediaRequested:  1,
				MediaErrors:     1,
			}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 1, Hydrated: 1}, nil
	}

	_, err := Run(context.Background(), cfg, st, Options{XEnabled: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x", "x-quote"}) {
		t.Fatalf("unchanged quote media retry should not keep draining, got calls %v", calls)
	}
}

func TestRunSettlesXFrontierBeforeDownstreamStages(t *testing.T) {
	cfg, st := testSyncStore(t)

	origBookmarks := runXBookmarkImport
	origX := runXHydrate
	origLinks := runLinkExtract
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXBookmarkImport = origBookmarks
		runXHydrate = origX
		runLinkExtract = origLinks
		runXMediaStage = origXMedia
	})

	var calls []string
	bookmarkPasses := 0
	runXBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, _ xapi.BookmarkOptions) (xapi.BookmarkStats, error) {
		calls = append(calls, "x-bookmarks")
		bookmarkPasses++
		if bookmarkPasses == 1 {
			return xapi.BookmarkStats{Created: 1, Rendered: 1, PagesFetched: 1, StoppedReason: "overlap"}, nil
		}
		return xapi.BookmarkStats{Unchanged: 10, PagesFetched: 1, StoppedReason: "overlap"}, nil
	}

	hydratePasses := 0
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		hydratePasses++
		if hydratePasses == 1 {
			return xapi.Stats{Candidates: 2, Hydrated: 2, Rendered: 2}, nil
		}
		return xapi.Stats{}, nil
	}

	linkPasses := 0
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		linkPasses++
		if linkPasses == 1 {
			return linkextract.Stats{ItemsScanned: 2, SourcesQueued: 1, SourcesSummarized: 1}, nil
		}
		return linkextract.Stats{}, nil
	}

	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		return xmediatranscribe.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XBookmarksEnabled: true,
		XEnabled:          true,
		LinksEnabled:      true,
		XMediaEnabled:     true,
		Progress:          &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-quote", "links", "x-bookmarks", "x", "links", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.XBookmarks == nil || stats.XBookmarks.Stats.Created != 1 {
		t.Fatalf("expected aggregated x bookmark stats, got %+v", stats.XBookmarks)
	}
	if stats.X == nil || stats.X.Stats.Hydrated != 2 {
		t.Fatalf("expected aggregated x hydrate stats, got %+v", stats.X)
	}
	if stats.Links == nil || stats.Links.Stats.SourcesQueued != 1 {
		t.Fatalf("expected aggregated link stats, got %+v", stats.Links)
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> x settle pass 2")) {
		t.Fatalf("expected progress output to contain x settle pass, got %q", output)
	}
}

func TestRunStopsXFrontierSettleWhenOnlyRetryingUnchangedMediaErrors(t *testing.T) {
	cfg, st := testSyncStore(t)

	origBookmarks := runXBookmarkImport
	origX := runXHydrate
	origLinks := runLinkExtract
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXBookmarkImport = origBookmarks
		runXHydrate = origX
		runLinkExtract = origLinks
		runXMediaStage = origXMedia
	})

	var calls []string
	runXBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, _ xapi.BookmarkOptions) (xapi.BookmarkStats, error) {
		calls = append(calls, "x-bookmarks")
		return xapi.BookmarkStats{Unchanged: 40, PagesFetched: 2, StoppedReason: "overlap"}, nil
	}
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{
			Candidates:      1,
			Hydrated:        1,
			Unchanged:       1,
			MediaCandidates: 1,
			MediaRequested:  1,
			MediaErrors:     1,
		}, nil
	}
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		return linkextract.Stats{ItemsScanned: 1, ItemsMarked: 1}, nil
	}
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		return xmediatranscribe.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XBookmarksEnabled: true,
		XEnabled:          true,
		LinksEnabled:      true,
		XMediaEnabled:     true,
		Progress:          &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-quote", "links", "x-media"}) {
		t.Fatalf("unchanged media retry should not keep frontier active, got calls %v", calls)
	}
	if stats.X == nil || stats.X.Stats.MediaErrors != 1 {
		t.Fatalf("expected one aggregated media error, got %+v", stats.X)
	}
	if bytes.Contains(progress.Bytes(), []byte("==> x settle pass 2")) {
		t.Fatalf("unexpected extra settle pass for unchanged media retry: %q", progress.String())
	}
}

func TestRunSkipsQuoteDrainWhenForceEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	t.Cleanup(func() {
		runXHydrate = origX
	})

	var calls []string
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			t.Fatal("quote-only drain should not run during force sync")
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 4, Hydrated: 4}, nil
	}

	_, err := Run(context.Background(), cfg, st, Options{
		XEnabled: true,
		Force:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"x"}) {
		t.Fatalf("unexpected hydrate calls: %v", calls)
	}
}

func TestRunExecutesAppleNotesBeforeLinkExtractionWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origAppleNotes := runAppleNotesImport
	origLinks := runLinkExtract
	t.Cleanup(func() {
		runAppleNotesImport = origAppleNotes
		runLinkExtract = origLinks
	})

	var calls []string
	runAppleNotesImport = func(_ context.Context, _ config.Config, _ *store.Store, opts applenotes.Options) (applenotes.Stats, error) {
		calls = append(calls, "apple-notes")
		if opts.DryRun {
			t.Fatal("expected sync Apple Notes import to write by default")
		}
		if !opts.Summarize {
			t.Fatal("expected sync Apple Notes import to use global summarize setting")
		}
		if opts.Limit != 5 {
			t.Fatalf("expected Apple Notes limit 5, got %d", opts.Limit)
		}
		if opts.SummaryModel != "test/model" || opts.SummaryCLI != "cli" || opts.SummaryLength != "short" {
			t.Fatalf("unexpected Apple Notes summary options: %+v", opts)
		}
		if !slices.Equal(opts.ExcludeFolders, []string{"Private"}) {
			t.Fatalf("unexpected Apple Notes exclude folders: %#v", opts.ExcludeFolders)
		}
		if !opts.SkipAttachmentOCR || opts.AttachmentMaxBytes != 12345 || opts.TesseractBinary != "fake-tesseract" {
			t.Fatalf("unexpected Apple Notes attachment options: %+v", opts)
		}
		if opts.Progress == nil {
			t.Fatal("expected sync Apple Notes import progress callback")
		}
		opts.Progress(applenotes.ProgressEvent{
			Phase:     "processing",
			Index:     1,
			Total:     5,
			SourceKey: "apple-note:default:test",
			Reason:    "summary",
		})
		opts.Progress(applenotes.ProgressEvent{
			Phase:         "summarizing",
			Index:         1,
			Total:         5,
			SourceKey:     "apple-note:default:test",
			Status:        "unchanged",
			SummaryStatus: "running",
		})
		opts.Progress(applenotes.ProgressEvent{
			Phase:          "imported",
			Index:          1,
			Total:          5,
			SourceKey:      "apple-note:default:test",
			Status:         "unchanged",
			SummaryStatus:  "ok",
			SummaryChanged: true,
		})
		return applenotes.Stats{NotesSeen: 1, NotesImported: 1, NotesRendered: 1, SummariesCreated: 1}, nil
	}
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		return linkextract.Stats{ItemsScanned: 1, SourcesQueued: 1}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		AppleNotesEnabled:            true,
		AppleNotesLimit:              5,
		AppleNotesExcludeFolders:     []string{"Private"},
		AppleNotesSkipAttachmentOCR:  true,
		AppleNotesAttachmentMaxBytes: 12345,
		AppleNotesTesseractBinary:    "fake-tesseract",
		LinksEnabled:                 true,
		Summarize:                    true,
		Model:                        "test/model",
		CLI:                          "cli",
		Length:                       "short",
		Progress:                     &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"apple-notes", "links"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.AppleNotes == nil || stats.AppleNotes.Stats.NotesImported != 1 {
		t.Fatalf("expected Apple Notes stage stats, got %+v", stats.AppleNotes)
	}
	output := progress.String()
	for _, value := range []string{"==> import apple-notes", "Apple Note 1/5 summarizing source=apple-note:default:test item_status=unchanged summary=running", "Apple Notes import complete", "==> extract links"} {
		if !bytes.Contains([]byte(output), []byte(value)) {
			t.Fatalf("expected progress output to contain %q, got %q", value, output)
		}
	}
	for _, value := range []string{"processing source=apple-note:default:test", "summarized source=apple-note:default:test", "imported source=apple-note:default:test"} {
		if bytes.Contains([]byte(output), []byte(value)) {
			t.Fatalf("expected summary-only progress output to omit %q, got %q", value, output)
		}
	}
}

func TestRunExecutesSafariTabsBeforeLinkExtractionWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origSafariTabs := runSafariTabsImport
	origLinks := runLinkExtract
	t.Cleanup(func() {
		runSafariTabsImport = origSafariTabs
		runLinkExtract = origLinks
	})

	var calls []string
	runSafariTabsImport = func(_ context.Context, _ config.Config, _ *store.Store, opts safaritabs.Options) (safaritabs.Stats, error) {
		calls = append(calls, "safari-tabs")
		if opts.DryRun {
			t.Fatal("expected sync Safari Tabs import to write by default")
		}
		if opts.DBPath != "/tmp/CloudTabs.db" || opts.Device != "phone" {
			t.Fatalf("unexpected Safari Tabs source options: %+v", opts)
		}
		if opts.Limit != 25 || opts.OlderThan != 7*24*time.Hour {
			t.Fatalf("unexpected Safari Tabs filter options: %+v", opts)
		}
		if opts.Progress == nil {
			t.Fatal("expected sync Safari Tabs import progress callback")
		}
		opts.Progress(safaritabs.ProgressEvent{Phase: "loaded", Total: 2})
		opts.Progress(safaritabs.ProgressEvent{
			Phase:     "imported",
			Index:     1,
			Total:     2,
			SourceKey: "safari-tab:phone:test",
			Status:    "created",
			Rendered:  true,
		})
		return safaritabs.Stats{DeviceName: "phone", TabsSeen: 2, TabsMatched: 1, TabsImported: 1, TabsCreated: 1, TabsRendered: 1, LinksFound: 1}, nil
	}
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		return linkextract.Stats{ItemsScanned: 1, SourcesQueued: 1}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		SafariTabsEnabled:   true,
		SafariTabsDBPath:    "/tmp/CloudTabs.db",
		SafariTabsDevice:    "phone",
		SafariTabsLimit:     25,
		SafariTabsOlderThan: 7 * 24 * time.Hour,
		LinksEnabled:        true,
		Progress:            &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"safari-tabs", "links"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.SafariTabs == nil || stats.SafariTabs.Stats.TabsImported != 1 {
		t.Fatalf("expected Safari Tabs stage stats, got %+v", stats.SafariTabs)
	}
	output := progress.String()
	for _, value := range []string{"==> import safari-tabs", "Safari tabs loaded: candidates=2", "Safari Tab 1/2 imported source=safari-tab:phone:test status=created rendered=true", "Safari Tabs import complete: device=phone seen=2 matched=1 created=1 updated=0 unchanged=0 rendered=1 skipped=0 links=1 errors=0", "==> extract links"} {
		if !bytes.Contains([]byte(output), []byte(value)) {
			t.Fatalf("expected progress output to contain %q, got %q", value, output)
		}
	}
}

func TestRunSkipsXMediaStageWhenDisabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXMediaStage = origXMedia
	})

	runXMediaStage = func(context.Context, config.Config, *store.Store, xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		t.Fatal("x media stage should not be called when disabled")
		return xmediatranscribe.Stats{}, nil
	}

	stats, err := Run(context.Background(), cfg, st, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.XMedia != nil {
		t.Fatalf("expected no x media stage stats, got %+v", stats.XMedia)
	}
}

func TestRunExecutesArchiveStageAtEndWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origArchive := runMediaArchive
	t.Cleanup(func() {
		runMediaArchive = origArchive
	})

	var called bool
	runMediaArchive = func(_ context.Context, _ config.Config, _ *store.Store, opts mediaarchive.Options) (mediaarchive.Stats, error) {
		called = true
		if !opts.Upload || !opts.PruneLocal {
			t.Fatalf("expected archive stage to upload and prune, got %+v", opts)
		}
		if opts.Bucket != "dbrain" {
			t.Fatalf("expected bucket dbrain, got %q", opts.Bucket)
		}
		return mediaarchive.Stats{Candidates: 2, Uploaded: 2, Archived: 2, LocalFilesPruned: 2}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		ArchiveMediaEnabled: true,
		ArchiveBucket:       "dbrain",
		ArchiveEndpoint:     "https://example.invalid",
		ArchiveAccessKeyID:  "key",
		ArchiveSecretKey:    "secret",
		Progress:            &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("expected archive stage to run")
	}
	if stats.MediaArchive == nil {
		t.Fatal("expected media archive stats")
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> archive media")) {
		t.Fatalf("expected progress output to contain archive stage, got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("Media archive complete")) {
		t.Fatalf("expected archive completion output, got %q", output)
	}
}

func TestRunExecutesOKFExportAfterArchiveWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origArchive := runMediaArchive
	origOKFExport := runOKFExport
	t.Cleanup(func() {
		runMediaArchive = origArchive
		runOKFExport = origOKFExport
	})

	var calls []string
	runMediaArchive = func(context.Context, config.Config, *store.Store, mediaarchive.Options) (mediaarchive.Stats, error) {
		calls = append(calls, "archive")
		return mediaarchive.Stats{Candidates: 1, Uploaded: 1, Archived: 1}, nil
	}
	runOKFExport = func(_ context.Context, cfg config.Config, _ *store.Store, opts okf.ExportOptions) (okf.ExportResult, error) {
		calls = append(calls, "okf")
		if cfg.OKFDir == "" {
			t.Fatal("expected configured OKF dir")
		}
		if opts.Profile != okf.ProfilePrivate || !opts.IncludeItems || !opts.IncludeSources || !opts.IncludeEntities || !opts.IncludeTopics || !opts.IncludeRaw || opts.MediaPublicBaseURL != "https://cdn.example.com" || opts.MediaProxyBaseURL != "https://dbrain.example.test" {
			t.Fatalf("unexpected OKF export options: %+v", opts)
		}
		return okf.ExportResult{Bundle: cfg.OKFDir, ConceptsWritten: 7, ItemsWritten: 2, SourcesWritten: 3, EntitiesWritten: 1, TopicsWritten: 1}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		ArchiveMediaEnabled:  true,
		ArchiveBucket:        "dbrain",
		ArchivePublicBaseURL: "https://cdn.example.com",
		ArchiveEndpoint:      "https://example.invalid",
		ArchiveAccessKeyID:   "key",
		ArchiveSecretKey:     "secret",
		OKFExportEnabled:     true,
		OKFMediaProxyBaseURL: "https://dbrain.example.test",
		Progress:             &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"archive", "okf"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.OKFExport == nil || stats.OKFExport.Stats.ConceptsWritten != 7 {
		t.Fatalf("expected okf export stats, got %+v", stats.OKFExport)
	}
	output := progress.String()
	for _, want := range []string{"==> export okf", "OKF export complete"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected progress output to contain %q, got %q", want, output)
		}
	}
}

func TestRunExecutesCategorizeStageBeforeArchiveWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origArchive := runMediaArchive
	origCategorize := runItemCategorize
	origSourceCategorize := runSourceCategorize
	t.Cleanup(func() {
		runMediaArchive = origArchive
		runItemCategorize = origCategorize
		runSourceCategorize = origSourceCategorize
	})

	var calls []string
	var logs bytes.Buffer
	runMediaArchive = func(context.Context, config.Config, *store.Store, mediaarchive.Options) (mediaarchive.Stats, error) {
		calls = append(calls, "archive")
		return mediaarchive.Stats{Candidates: 1, Uploaded: 1, Archived: 1}, nil
	}
	runItemCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		calls = append(calls, "categorize-items")
		if !opts.Apply {
			t.Fatal("expected sync categorization to apply tags")
		}
		if opts.Force {
			t.Fatal("did not expect forced categorization")
		}
		if opts.Limit != 12 {
			t.Fatalf("expected categorize limit 12, got %d", opts.Limit)
		}
		if opts.Concurrency != 3 {
			t.Fatalf("expected categorize concurrency 3, got %d", opts.Concurrency)
		}
		if opts.OnStart == nil {
			t.Fatal("expected categorize progress start callback")
		}
		if opts.OnResult == nil {
			t.Fatal("expected categorize progress result callback")
		}
		opts.OnStart(2)
		opts.OnResult(itemcategorize.ItemResult{
			Item: model.Item{ID: 101, SourceKey: "x:101"},
			Result: itemcategorize.Result{
				Tags:       []string{"canada"},
				Categories: []string{"Canadian Politics"},
				Model:      "omlx/Qwen3.6-35B-A3B-MLX-4bit",
			},
		})
		return itemcategorize.Stats{Queued: 2, Succeeded: 2, Applied: 2}, nil, nil
	}
	runSourceCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		calls = append(calls, "categorize-sources")
		if !opts.Apply {
			t.Fatal("expected sync source categorization to apply tags")
		}
		if opts.Force {
			t.Fatal("did not expect forced source categorization")
		}
		if opts.Limit != 12 {
			t.Fatalf("expected source categorize limit 12, got %d", opts.Limit)
		}
		if opts.Concurrency != 3 {
			t.Fatalf("expected source categorize concurrency 3, got %d", opts.Concurrency)
		}
		if opts.IncludeImages {
			t.Fatal("did not expect source categorization to request images")
		}
		if opts.OnStart == nil {
			t.Fatal("expected source categorize progress start callback")
		}
		if opts.OnSourceResult == nil {
			t.Fatal("expected source categorize progress result callback")
		}
		opts.OnStart(1)
		opts.OnSourceResult(itemcategorize.SourceResult{
			Source: model.SourceDocument{ID: 201, SourceKey: "src:201"},
			Result: itemcategorize.Result{
				Tags:       []string{"payments"},
				Categories: []string{"financial-technology"},
				Model:      "omlx/Qwen3.6-35B-A3B-MLX-4bit",
			},
		})
		return itemcategorize.Stats{Queued: 1, Succeeded: 1, Applied: 1}, nil, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		ArchiveMediaEnabled:   true,
		CategorizeEnabled:     true,
		CategorizeLimit:       12,
		CategorizeConcurrency: 3,
		Logger:                slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Progress:              &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"categorize-items", "categorize-sources", "archive"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.Categorize == nil {
		t.Fatal("expected categorize stage stats")
	}
	if stats.Categorize.Stats.Queued != 3 || stats.Categorize.Stats.Applied != 3 {
		t.Fatalf("expected aggregate categorize stats, got %+v", stats.Categorize.Stats)
	}
	if stats.Categorize.ItemStats.Queued != 2 || stats.Categorize.SourceStats.Queued != 1 {
		t.Fatalf("expected item/source categorize stats, got items=%+v sources=%+v", stats.Categorize.ItemStats, stats.Categorize.SourceStats)
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> categorize items and sources")) {
		t.Fatalf("expected progress output to contain categorize stage, got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("Categorization complete")) {
		t.Fatalf("expected categorize completion output, got %q", output)
	}
	logOutput := logs.String()
	if !bytes.Contains([]byte(logOutput), []byte("item categorization candidates loaded")) {
		t.Fatalf("expected categorize candidate progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("item categorized")) {
		t.Fatalf("expected per-item categorize progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("source categorization candidates loaded")) {
		t.Fatalf("expected source categorize candidate progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("source categorized")) {
		t.Fatalf("expected per-source categorize progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("processed=1")) || !bytes.Contains([]byte(logOutput), []byte("total=2")) {
		t.Fatalf("expected categorize progress counters, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("model=omlx/Qwen3.6-35B-A3B-MLX-4bit")) {
		t.Fatalf("expected categorize logs to include model provenance, got %q", logOutput)
	}
}

func TestRunSkipsCategorizeStageWhenDisabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origCategorize := runItemCategorize
	origSourceCategorize := runSourceCategorize
	t.Cleanup(func() {
		runItemCategorize = origCategorize
		runSourceCategorize = origSourceCategorize
	})

	runItemCategorize = func(context.Context, config.Config, *store.Store, itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		t.Fatal("categorize stage should not be called when disabled")
		return itemcategorize.Stats{}, nil, nil
	}
	runSourceCategorize = func(context.Context, config.Config, *store.Store, itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		t.Fatal("source categorize stage should not be called when disabled")
		return itemcategorize.Stats{}, nil, nil
	}

	stats, err := Run(context.Background(), cfg, st, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Categorize != nil {
		t.Fatalf("expected no categorize stage stats, got %+v", stats.Categorize)
	}
}

func TestRunFeedsNoDueProgressIncludesScheduleState(t *testing.T) {
	cfg, st := testSyncStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	next := now.Add(time.Hour)

	result, err := st.UpsertFeed(ctx, store.FeedUpsert{
		FeedKey:             "feed:test",
		URL:                 "https://example.com/feed.xml",
		NormalizedURL:       "https://example.com/feed.xml",
		PollIntervalSeconds: 3600,
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}
	if err := st.UpdateFeedFailure(ctx, store.FeedFailureState{
		FeedID:         result.FeedID,
		HealthStatus:   store.FeedHealthError,
		FailureKind:    "network",
		Error:          "lookup example.com: no such host",
		FailedAt:       now,
		NextFetchAfter: next,
	}); err != nil {
		t.Fatalf("UpdateFeedFailure: %v", err)
	}

	origFeed := runFeedImport
	t.Cleanup(func() { runFeedImport = origFeed })
	runFeedImport = func(context.Context, config.Config, *store.Store, feedimport.Options) (feedimport.Stats, error) {
		return feedimport.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(ctx, cfg, st, Options{
		FeedsEnabled: true,
		Progress:     &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Feeds == nil {
		t.Fatal("expected feeds stage stats")
	}
	output := progress.String()
	for _, want := range []string{
		"Feeds import complete: checked=0 subscribed=1 due=0",
		"next_due=" + next.Format(time.RFC3339),
		"backing_off=1",
		`last_error="lookup example.com: no such host"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected progress to contain %q, got %q", want, output)
		}
	}
}

func testSyncStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	return cfg, st
}

func readSyncMetricEvents(t *testing.T, path string) []map[string]any {
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
			t.Fatalf("parse metrics line %q: %v", scanner.Text(), err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan metrics: %v", err)
	}
	return events
}

func metricEventNames(events []map[string]any) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		if name, _ := event["event"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}
