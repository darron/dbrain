package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/notify"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
)

func TestClassifyScheduledFailureAppleNotesPermission(t *testing.T) {
	err := syncjob.WrapStageError("apple_notes", fs.ErrPermission)
	got := classifyScheduledSyncOutcome(scheduledSyncOutcome{Status: scheduledSyncStatusError, Err: err})
	if got.FailureType != notify.FailureAppleNotesPermission || got.ErrorCode != "apple_notes_permission_denied" {
		t.Fatalf("classification = %#v", got)
	}
}

func TestClassifyScheduledSyncOutcomesPreservesEveryDistinctJoinedFailure(t *testing.T) {
	started := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	finished := started.Add(2 * time.Minute)
	outcomes := classifyScheduledSyncOutcomes(scheduledSyncOutcome{
		Status:     scheduledSyncStatusError,
		StartedAt:  started,
		FinishedAt: finished,
		Err: errors.Join(
			syncjob.WrapStageError("github", errors.New("github unavailable")),
			syncjob.WrapStageError("feeds", errors.New("feeds unavailable")),
			syncjob.WrapStageError("github", errors.New("github still unavailable")),
			semanticrefresh.NewError(
				semanticrefresh.ErrorCancelled,
				store.SemanticRefreshRun{},
				"",
				semanticrefresh.Debt{},
				errors.New("shutdown detail"),
			),
		),
	})
	if len(outcomes) != 2 {
		t.Fatalf("classified outcomes = %#v", outcomes)
	}
	want := []notify.FailureType{"sync.stage.github.failed", "sync.stage.feeds.failed"}
	for index, outcome := range outcomes {
		if outcome.Status != notify.OutcomeFailure || outcome.FailureType != want[index] {
			t.Fatalf("outcome %d = %#v; want failure type %q", index, outcome, want[index])
		}
	}
}

func TestClassifyScheduledFailureKnownBoundaries(t *testing.T) {
	started := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	finished := started.Add(2 * time.Minute)
	semanticFailure := func(code string) error {
		return semanticrefresh.NewError(code, store.SemanticRefreshRun{}, "", semanticrefresh.Debt{}, errors.New("private provider detail"))
	}
	tests := []struct {
		name       string
		status     scheduledSyncStatus
		err        error
		wantStatus notify.OutcomeStatus
		wantType   notify.FailureType
		wantCode   string
	}{
		{name: "success", status: scheduledSyncStatusOK, wantStatus: notify.OutcomeSuccess},
		{name: "scheduler cancellation", status: scheduledSyncStatusCancelled, err: errors.New("shutdown detail"), wantStatus: notify.OutcomeCancelled},
		{name: "direct cancellation", status: scheduledSyncStatusError, err: context.Canceled, wantStatus: notify.OutcomeCancelled},
		{name: "wrapped cancellation", status: scheduledSyncStatusError, err: fmt.Errorf("shutdown: %w", context.Canceled), wantStatus: notify.OutcomeCancelled},
		{name: "semantic cancellation without cause", status: scheduledSyncStatusError, err: semanticFailure(semanticrefresh.ErrorCancelled), wantStatus: notify.OutcomeCancelled},
		{name: "config resolution", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryConfigResolution, errors.New("parse DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES: invalid")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureConfigResolution, wantCode: "sync_config_resolution_failed"},
		{name: "metrics open", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryMetricsOpen, errors.New("create metrics directory: permission denied")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureMetricsOpen, wantCode: "sync_metrics_open_failed"},
		{name: "metrics close", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryMetricsClose, errors.New("close /private/logs/dbrain/metrics.jsonl: input/output error")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureMetricsClose, wantCode: "sync_metrics_close_failed"},
		{name: "store open", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryStoreOpen, errors.New("apply migration 28: private database detail")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureStoreOpen, wantCode: "sync_store_open_failed"},
		{name: "store close", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryStoreClose, errors.New("close /private/data/dbrain/brain.db: input/output error")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureStoreClose, wantCode: "sync_store_close_failed"},
		{name: "options preflight", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryOptions, errors.New("resolve GITHUB_TOKEN secret ref: private detail")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureOptions, wantCode: "sync_options_failed"},
		{name: "output", status: scheduledSyncStatusError, err: wrapScheduledSyncBoundary(scheduledBoundaryOutput, errors.New("write scheduled sync output: broken pipe")), wantStatus: notify.OutcomeFailure, wantType: notify.FailureOutput, wantCode: "sync_output_failed"},
		{name: "semantic failure remains primary when metrics close also fails", status: scheduledSyncStatusError, err: errors.Join(semanticFailure(semanticrefresh.ErrorFlush), wrapScheduledSyncBoundary(scheduledBoundaryMetricsClose, errors.New("close metrics: input/output error"))), wantStatus: notify.OutcomeFailure, wantType: notify.FailureType("sync.semantic." + semanticrefresh.ErrorFlush), wantCode: semanticrefresh.ErrorFlush},
		{name: "unknown fallback", status: scheduledSyncStatusError, err: errors.New("new hard failure with secret detail"), wantStatus: notify.OutcomeFailure, wantType: notify.FailureUnknown, wantCode: "sync_unknown"},
	}
	stages := []string{"apple_notes", "safari_tabs", "x_frontier", "x_media", "x_photo_ocr", "github", "youtube", "feeds", "sources", "categorize", "media_archive", "okf_export"}
	for _, stage := range stages {
		tests = append(tests, struct {
			name       string
			status     scheduledSyncStatus
			err        error
			wantStatus notify.OutcomeStatus
			wantType   notify.FailureType
			wantCode   string
		}{
			name:       "sync stage " + stage,
			status:     scheduledSyncStatusError,
			err:        syncjob.WrapStageError(stage, errors.New("private stage detail")),
			wantStatus: notify.OutcomeFailure,
			wantType:   notify.FailureType("sync.stage." + stage + ".failed"),
			wantCode:   "sync_stage_" + stage + "_failed",
		})
	}
	semanticCodes := []string{
		semanticrefresh.ErrorBackendBroken,
		semanticrefresh.ErrorRunConflict,
		semanticrefresh.ErrorProjection,
		semanticrefresh.ErrorEmbedding,
		semanticrefresh.ErrorEmbeddingCircuit,
		semanticrefresh.ErrorFlush,
		semanticrefresh.ErrorCompaction,
		semanticrefresh.ErrorVerify,
		semanticrefresh.ErrorNativeRoot,
		semanticrefresh.ErrorReadiness,
		semanticrefresh.ErrorLockUnavailable,
	}
	for _, code := range semanticCodes {
		tests = append(tests, struct {
			name       string
			status     scheduledSyncStatus
			err        error
			wantStatus notify.OutcomeStatus
			wantType   notify.FailureType
			wantCode   string
		}{
			name:       "semantic " + code,
			status:     scheduledSyncStatusError,
			err:        semanticFailure(code),
			wantStatus: notify.OutcomeFailure,
			wantType:   notify.FailureType("sync.semantic." + code),
			wantCode:   code,
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyScheduledSyncOutcome(scheduledSyncOutcome{
				Status:     test.status,
				StartedAt:  started,
				FinishedAt: finished,
				Err:        test.err,
			})
			if got.Operation != notify.OperationScheduledSyncAll || got.Status != test.wantStatus || got.FailureType != test.wantType || got.ErrorCode != test.wantCode {
				t.Fatalf("classification = %#v; want status=%q type=%q code=%q", got, test.wantStatus, test.wantType, test.wantCode)
			}
			if !got.StartedAt.Equal(started) || !got.FinishedAt.Equal(finished) {
				t.Fatalf("classification lost timestamps: %#v", got)
			}
		})
	}
}

func TestClassifyScheduledFailureDoesNotTrustOuterErrorText(t *testing.T) {
	for _, raw := range []string{
		"apply migration 28 with provider token",
		"resolve GITHUB_TOKEN secret ref: private detail",
		"close /private/logs/dbrain/metrics.jsonl: private detail",
	} {
		got := classifyScheduledSyncOutcome(scheduledSyncOutcome{Status: scheduledSyncStatusError, Err: errors.New(raw)})
		if got.FailureType != notify.FailureUnknown || got.ErrorCode != "sync_unknown" {
			t.Fatalf("raw error text %q selected trusted type: %#v", raw, got)
		}
	}
}

func TestScheduledSyncBoundaryErrorPreservesCauseTextAndChain(t *testing.T) {
	cause := errors.New("private underlying detail")
	err := wrapScheduledSyncBoundary(scheduledBoundaryStoreOpen, cause)
	if err.Error() != cause.Error() || !errors.Is(err, cause) {
		t.Fatalf("boundary wrapper changed cause behavior: %T %v", err, err)
	}
	var boundaryErr *scheduledSyncBoundaryError
	if !errors.As(err, &boundaryErr) || boundaryErr.boundary != scheduledBoundaryStoreOpen {
		t.Fatalf("boundary wrapper = %#v", err)
	}
}

func TestRunScheduledSyncAllTagsOuterFailureBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		boundary scheduledSyncFailureBoundary
		run      func(*testing.T, config.Config) error
	}{
		{
			name:     "config resolution",
			boundary: scheduledBoundaryConfigResolution,
			run: func(t *testing.T, cfg config.Config) error {
				t.Setenv("DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES", "invalid")
				return runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, syncAllFlags{}, io.Discard, defaultSemanticRefreshDeps())
			},
		},
		{
			name:     "metrics open",
			boundary: scheduledBoundaryMetricsOpen,
			run: func(t *testing.T, cfg config.Config) error {
				blocker := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("DBRAIN_METRICS_ENABLED", "true")
				t.Setenv("DBRAIN_METRICS_PATH", filepath.Join(blocker, "metrics.jsonl"))
				return runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, syncAllFlags{}, io.Discard, defaultSemanticRefreshDeps())
			},
		},
		{
			name:     "store open",
			boundary: scheduledBoundaryStoreOpen,
			run: func(t *testing.T, cfg config.Config) error {
				t.Setenv("DBRAIN_METRICS_ENABLED", "false")
				cfg.DBPath = filepath.Join(t.TempDir(), "database-directory")
				if err := os.MkdirAll(cfg.DBPath, 0o700); err != nil {
					t.Fatal(err)
				}
				return runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, syncAllFlags{}, io.Discard, defaultSemanticRefreshDeps())
			},
		},
		{
			name:     "options secret resolution",
			boundary: scheduledBoundaryOptions,
			run: func(t *testing.T, cfg config.Config) error {
				t.Setenv("DBRAIN_METRICS_ENABLED", "false")
				t.Setenv("GITHUB_TOKEN", "env:DBRAIN_TASK2_MISSING_SECRET")
				t.Setenv("DBRAIN_TASK2_MISSING_SECRET", "")
				storefixture.PrepareCurrent(t, cfg.DBPath)
				flags := syncAllFlags{
					skipXBookmarks: true,
					skipX:          true,
					skipXMedia:     true,
					skipXPhotoOCR:  true,
					skipLinks:      true,
					skipYouTube:    true,
					skipAppleNotes: true,
					skipSafariTabs: true,
					skipFeeds:      true,
					skipSources:    true,
					skipCategorize: true,
					skipOKFExport:  true,
				}
				return runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, flags, io.Discard, defaultSemanticRefreshDeps())
			},
		},
		{
			name:     "output",
			boundary: scheduledBoundaryOutput,
			run: func(t *testing.T, cfg config.Config) error {
				t.Setenv("DBRAIN_METRICS_ENABLED", "false")
				storefixture.PrepareCurrent(t, cfg.DBPath)
				oldRunSyncAll := runSyncAll
				t.Cleanup(func() { runSyncAll = oldRunSyncAll })
				runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
					return syncjob.Stats{XBookmarks: &syncjob.XBookmarksStage{}}, nil
				}
				flags := syncAllFlags{
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
				return runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, flags, classifyFailWriter{}, defaultSemanticRefreshDeps())
			},
		},
		{
			name:     "semantic progress output",
			boundary: scheduledBoundaryOutput,
			run: func(t *testing.T, cfg config.Config) error {
				t.Setenv("DBRAIN_METRICS_ENABLED", "false")
				storefixture.PrepareCurrent(t, cfg.DBPath)
				oldRunSyncAll := runSyncAll
				t.Cleanup(func() { runSyncAll = oldRunSyncAll })
				runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
					return syncjob.Stats{}, nil
				}
				deps := successfulSyncSemanticDeps(func(
					_ context.Context,
					_ semanticrefresh.RunLedger,
					_ semanticrefresh.StageExecutor,
					request semanticrefresh.Request,
				) (semanticrefresh.Result, error) {
					return semanticrefresh.Result{}, request.Progress(semanticrefresh.Progress{
						Stage: store.SemanticRefreshEmbedding,
						At:    stateTestSchedulerTime(),
					})
				})
				return runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, scheduledSyncSemanticTestFlags(), classifyFailWriter{}, deps)
			},
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
			err = test.run(t, cfg)
			var boundaryErr *scheduledSyncBoundaryError
			if !errors.As(err, &boundaryErr) || boundaryErr.boundary != test.boundary {
				t.Fatalf("outer error = %T %v, want boundary %q", err, err, test.boundary)
			}
		})
	}
}

type classifyFailWriter struct{}

func (classifyFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("private output detail")
}

func stateTestSchedulerTime() time.Time {
	return time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
}

func TestClassifyScheduledFailureNeverCarriesRawError(t *testing.T) {
	outcome := classifyScheduledSyncOutcome(scheduledSyncOutcome{
		Status: scheduledSyncStatusError,
		Err:    errors.New("secret token and private path"),
	})
	if strings.Contains(fmt.Sprintf("%#v", outcome), "secret token") || strings.Contains(fmt.Sprintf("%#v", outcome), "private path") {
		t.Fatalf("classification retained raw error: %#v", outcome)
	}
	if outcome.ErrorCode != "sync_unknown" || outcome.FailureType != notify.FailureUnknown {
		t.Fatalf("unknown classification = %#v", outcome)
	}
}
