package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/semanticgc"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
)

func TestMaybeRunAutomaticSemanticGCGatesOnConfigAndCompletedRefresh(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		result  semanticrefresh.Result
		err     error
		wantRun bool
	}{
		{name: "default off", result: semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted}},
		{name: "semantic skipped", enabled: true, result: semanticrefresh.Result{Outcome: semanticrefresh.OutcomeSkipped}},
		{name: "semantic failed", enabled: true, result: semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted}, err: errors.New("refresh failed")},
		{name: "completed", enabled: true, result: semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted}, wantRun: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			got := maybeRunAutomaticSemanticGC(t.Context(), config.Config{}, test.enabled, test.result, test.err, syncSemanticGCDeps{
				now: time.Now,
				run: func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
					calls++
					return semanticgc.Result{Applied: true}, "", nil
				},
			})
			if test.wantRun {
				if calls != 1 || got == nil || got.Status != syncSemanticGCStatusOK {
					t.Fatalf("calls=%d result=%+v", calls, got)
				}
				return
			}
			if calls != 0 || got != nil {
				t.Fatalf("gated stage calls=%d result=%+v", calls, got)
			}
		})
	}
}

func TestRunAutomaticSemanticGCUsesSafeApplyOptionsAndReportsCounts(t *testing.T) {
	started := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	times := []time.Time{started, started.Add(2 * time.Second)}
	var gotOpts semanticgc.Options
	result := runAutomaticSemanticGC(t.Context(), config.Config{CacheDir: "/cache"}, syncSemanticGCDeps{
		now: func() time.Time {
			value := times[0]
			times = times[1:]
			return value
		},
		run: func(_ context.Context, cfg config.Config, opts semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
			if cfg.CacheDir != "/cache" {
				t.Fatalf("config=%+v", cfg)
			}
			gotOpts = opts
			return semanticgc.Result{
				Applied: true,
				Catalog: store.RetrievalSemanticGCPlan{
					PrunableGenerations: []store.RetrievalSemanticGCArtifact{{ID: "g1"}, {ID: "g2"}},
					PrunableSegments:    []store.RetrievalSemanticGCArtifact{{ID: "s1"}},
					PrunableMemberRows:  42,
				},
				FilesystemArtifacts:    []semanticgc.Artifact{{Path: "one"}, {Path: "two"}},
				PrunableBytes:          1024,
				DeletedFilesystemDirs:  2,
				DeletedFilesystemBytes: 1024,
			}, "", nil
		},
	})
	if !gotOpts.Apply || gotOpts.Vacuum || gotOpts.GracePeriod != defaultSemanticGCGracePeriod || gotOpts.RetainPublished != defaultSemanticGCRetainPublished || gotOpts.LockTimeout != defaultSyncSemanticGCLockTimeout {
		t.Fatalf("automatic GC options=%+v", gotOpts)
	}
	if result.Status != syncSemanticGCStatusOK || result.Duration != 2*time.Second || result.DurationMS != 2000 || result.GenerationsPruned != 2 || result.SegmentsPruned != 1 || result.MemberRowsPruned != 42 || result.FilesystemCandidates != 2 || result.FilesystemDeleted != 2 || result.PrunableBytes != 1024 || result.DeletedBytes != 1024 {
		t.Fatalf("automatic GC result=%+v", result)
	}
}

func TestRunAutomaticSemanticGCClassifiesAdmissionTimeoutAndOperationalError(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		phase      syncSemanticGCFailurePhase
		wantStatus syncSemanticGCStatus
		wantReason string
		wantError  string
	}{
		{name: "timeout", err: errors.Join(errors.New("acquire maintenance"), context.DeadlineExceeded), wantStatus: syncSemanticGCStatusSkipped, wantReason: "semantic_lease_timeout"},
		{name: "cancelled", err: context.Canceled, wantStatus: syncSemanticGCStatusSkipped, wantReason: "semantic_gc_cancelled"},
		{name: "failure", err: errors.New("unlink failed"), phase: syncSemanticGCPhaseFilesystemUnlink, wantStatus: syncSemanticGCStatusError, wantError: "filesystem_unlink"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runAutomaticSemanticGC(t.Context(), config.Config{}, syncSemanticGCDeps{
				now: time.Now,
				run: func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
					return semanticgc.Result{}, test.phase, test.err
				},
			})
			if result.Status != test.wantStatus || result.SkipReason != test.wantReason || !errors.Is(result.err, test.err) {
				t.Fatalf("result=%+v", result)
			}
			if result.Error != test.wantError {
				t.Fatalf("safe error=%q", result.Error)
			}
		})
	}
}

func TestSyncAllAutomaticSemanticGCRunsBeforeTerminalMetricsAndIsNonFatal(t *testing.T) {
	for _, test := range []struct {
		name       string
		sourceErr  error
		gcErr      error
		wantStatus syncSemanticGCStatus
		wantError  string
		wantRunErr string
	}{
		{name: "success", wantStatus: syncSemanticGCStatusOK, wantRunErr: "ok"},
		{name: "operational error", gcErr: errors.New("injected unlink failure"), wantStatus: syncSemanticGCStatusError, wantError: "filesystem_unlink", wantRunErr: "ok"},
		{name: "source error does not block GC", sourceErr: errors.New("source sync failed"), wantStatus: syncSemanticGCStatusOK, wantRunErr: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: automatic-semantic-gc.jsonl
  strict: true
sync_all:
  semantic_gc: true
`), 0o600); err != nil {
				t.Fatal(err)
			}
			oldRunSyncAll := runSyncAll
			t.Cleanup(func() { runSyncAll = oldRunSyncAll })
			runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
				return syncSemanticTestStats(), test.sourceErr
			}

			deps := successfulSyncSemanticDeps(func(
				context.Context,
				semanticrefresh.RunLedger,
				semanticrefresh.StageExecutor,
				semanticrefresh.Request,
			) (semanticrefresh.Result, error) {
				return completedSyncSemanticResult(), nil
			})
			gcCalls := 0
			deps.semanticGC = syncSemanticGCDeps{
				now: time.Now,
				run: func(_ context.Context, cfg config.Config, _ semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
					gcCalls++
					lock, lockErr := acquireSyncAllLock(cfg, "automatic-gc-test")
					if lockErr == nil {
						_ = lock.Close()
						t.Fatal("automatic GC ran after the coarse sync-all lock was released")
					}
					if !isSyncAllAlreadyRunning(lockErr) {
						t.Fatalf("probe coarse sync-all lock: %v", lockErr)
					}
					return semanticgc.Result{
						Applied:                true,
						Catalog:                store.RetrievalSemanticGCPlan{PrunableMemberRows: 7},
						DeletedFilesystemDirs:  1,
						DeletedFilesystemBytes: 99,
					}, syncSemanticGCPhaseFilesystemUnlink, test.gcErr
				},
			}

			cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(true))
			execErr := cmd.ExecuteContext(t.Context())
			if test.sourceErr == nil {
				if execErr != nil {
					t.Fatalf("automatic GC changed successful sync exit: %v", execErr)
				}
			} else if !errors.Is(execErr, test.sourceErr) {
				t.Fatalf("source failure = %v, want %v", execErr, test.sourceErr)
			}
			if gcCalls != 1 {
				t.Fatalf("automatic GC calls=%d", gcCalls)
			}
			var document struct {
				SemanticGC *syncSemanticGCResult `json:"semantic_gc"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
				t.Fatalf("decode sync JSON: %v\n%s", err, stdout.String())
			}
			if document.SemanticGC == nil || document.SemanticGC.Status != test.wantStatus || document.SemanticGC.Error != test.wantError {
				t.Fatalf("semantic_gc=%+v", document.SemanticGC)
			}

			events := readAppMetricEvents(t, filepath.Join(root, "logs", "automatic-semantic-gc.jsonl"))
			want := []string{"semantic.refresh.started", "semantic.refresh.completed", "semantic.gc.completed", "sync.run.completed"}
			if len(events) != len(want) {
				t.Fatalf("events=%#v", events)
			}
			for index, event := range want {
				if events[index]["event"] != event {
					t.Fatalf("event %d=%#v want=%s", index, events[index], event)
				}
			}
			if events[2]["status"] != string(test.wantStatus) || events[3]["status"] != test.wantRunErr {
				t.Fatalf("terminal statuses: gc=%#v sync=%#v", events[2], events[3])
			}
		})
	}
}

func TestSyncAllAutomaticSemanticGCDefaultOffDoesNotRunOrChangeJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("sync_all:\n  browser: chrome\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncSemanticTestStats(), nil
	}
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		return completedSyncSemanticResult(), nil
	})
	gcCalls := 0
	deps.semanticGC = syncSemanticGCDeps{
		now: time.Now,
		run: func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
			gcCalls++
			return semanticgc.Result{}, "", nil
		},
	}
	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if gcCalls != 0 {
		t.Fatalf("default-off automatic GC calls=%d", gcCalls)
	}
	document := decodeOneSyncJSONDocument(t, stdout.Bytes())
	if _, exists := document["semantic_gc"]; exists {
		t.Fatalf("default-off JSON included semantic_gc: %s", stdout.String())
	}
}

func TestScheduledSyncAllAutomaticSemanticGCUsesSharedConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: scheduled-automatic-semantic-gc.jsonl
  strict: true
sync_all:
  semantic_gc: true
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
		return syncSemanticTestStats(), nil
	}
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		return completedSyncSemanticResult(), nil
	})
	gcCalls := 0
	deps.semanticGC = syncSemanticGCDeps{
		now: time.Now,
		run: func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
			gcCalls++
			return semanticgc.Result{Applied: true}, "", nil
		},
	}
	var output bytes.Buffer
	if err := runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, scheduledSyncSemanticTestFlags(), &output, deps); err != nil {
		t.Fatal(err)
	}
	if gcCalls != 1 || !strings.Contains(output.String(), "scheduler semantic GC: status=ok") {
		t.Fatalf("calls=%d output=%q", gcCalls, output.String())
	}
	events := readAppMetricEvents(t, filepath.Join(root, "logs", "scheduled-automatic-semantic-gc.jsonl"))
	want := []string{"semantic.refresh.started", "semantic.refresh.completed", "semantic.gc.completed", "sync.run.completed"}
	if len(events) != len(want) {
		t.Fatalf("events=%#v", events)
	}
	for index, event := range want {
		if events[index]["event"] != event {
			t.Fatalf("event %d=%#v want=%s", index, events[index], event)
		}
	}
}

func TestScheduledSyncAllAutomaticSemanticGCContinuesAfterSourceError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: scheduled-automatic-semantic-gc-source-error.jsonl
  strict: true
sync_all:
  semantic_gc: true
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
	sourceErr := syncjob.WrapStageError("github", errors.New("github unavailable"))
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncSemanticTestStats(), sourceErr
	}
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		return completedSyncSemanticResult(), nil
	})
	gcCalls := 0
	deps.semanticGC = syncSemanticGCDeps{
		now: time.Now,
		run: func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
			gcCalls++
			return semanticgc.Result{Applied: true}, "", nil
		},
	}
	var output bytes.Buffer
	err = runScheduledSyncAllUnlockedWithSemanticDeps(t.Context(), cfg, scheduledSyncSemanticTestFlags(), &output, deps)
	if !errors.Is(err, sourceErr) {
		t.Fatalf("scheduled source error = %v, want %v", err, sourceErr)
	}
	if gcCalls != 1 || !strings.Contains(output.String(), "scheduler semantic GC: status=ok") {
		t.Fatalf("GC calls=%d output=%q", gcCalls, output.String())
	}
	events := readAppMetricEvents(t, filepath.Join(root, "logs", "scheduled-automatic-semantic-gc-source-error.jsonl"))
	if len(events) != 4 || events[1]["status"] != "ok" || events[2]["status"] != "ok" || events[3]["status"] != "error" {
		t.Fatalf("terminal events=%#v", events)
	}
}

func TestScheduledSemanticGCResultIncludesSafeFailurePhase(t *testing.T) {
	var output bytes.Buffer
	if err := logScheduledSemanticGCResult(&output, &syncSemanticGCResult{
		Status: syncSemanticGCStatusError,
		Error:  "store_open",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "status=error") || !strings.Contains(output.String(), "error=store_open") {
		t.Fatalf("scheduled output=%q", output.String())
	}
}
