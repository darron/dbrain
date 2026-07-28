package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

func TestSyncFamilySourceFailureSkipsSemanticAdmission(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sourceErr := errors.New("source sync failed")
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncjob.Stats{}, sourceErr
	}

	semanticCalls := map[string]int{}
	cmd := newSyncCommandWithSemanticDeps(
		&rootOptions{root: root},
		semanticRefreshDeps{
			resolve: func(string) (semanticconfig.Config, error) {
				semanticCalls["resolve"]++
				return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
			},
			capability: func() semanticindex.Capability {
				semanticCalls["capability"]++
				return semanticindex.Capability{}
			},
			openWritable: func(string) (*store.Store, error) {
				semanticCalls["open"]++
				return nil, nil
			},
			provider: func(semanticconfig.Config) (embedding.Provider, error) {
				semanticCalls["provider"]++
				return nil, nil
			},
			nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
				semanticCalls["native"]++
				return nil, nil
			},
			runRefresh: func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error) {
				semanticCalls["refresh"]++
				return semanticrefresh.Result{}, nil
			},
		},
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))

	err = cmd.ExecuteContext(t.Context())
	if !errors.Is(err, sourceErr) {
		t.Fatalf("ExecuteContext error = %v, want source error", err)
	}
	if len(semanticCalls) != 0 {
		t.Fatalf("semantic calls = %#v, want none", semanticCalls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty source failure output", stdout.String())
	}
	released, lockErr := acquireSyncAllLock(cfg, "source-error-probe")
	if lockErr != nil {
		t.Fatalf("source failure left coarse sync lock held: %v", lockErr)
	}
	_ = released.Close()
}

func TestSyncFamilyJSONSuccessFlattensSourceStatsAndSemanticResult(t *testing.T) {
	root := t.TempDir()
	stats := syncSemanticTestStats()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return stats, nil
	}

	cmd := newSyncCommandWithSemanticDeps(
		&rootOptions{root: root},
		semanticRefreshDeps{
			resolve: func(string) (semanticconfig.Config, error) {
				return semanticRefreshTestConfig(semanticconfig.ModeOff), nil
			},
			capability: func() semanticindex.Capability {
				return semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
			},
		},
	)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	document := decodeOneSyncJSONDocument(t, stdout.Bytes())
	assertFlattenedSyncFields(t, document, stats, "semantic")
	if _, exists := document["semantic_error"]; exists {
		t.Fatal("JSON success unexpectedly contained semantic_error")
	}
	var semantic semanticRefreshResultOutput
	if err := json.Unmarshal(document["semantic"], &semantic); err != nil {
		t.Fatalf("decode semantic result: %v", err)
	}
	if semantic.Outcome != semanticrefresh.OutcomeSkipped || semantic.SkipReason != "semantic_mode_off" {
		t.Fatalf("semantic result = %#v, want explicit mode-off skip", semantic)
	}
}

func TestSyncFamilyJSONFailureFlattensSourceStatsAndPreservesRefreshError(t *testing.T) {
	root := t.TempDir()
	stats := syncSemanticTestStats()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return stats, nil
	}

	run := store.SemanticRefreshRun{
		RunID:          "run-failed",
		ProfileID:      "profile-1",
		Stage:          store.SemanticRefreshFlush,
		Checkpoint:     "flush:segment-4",
		ReadinessState: "not_ready",
	}
	debt := semanticrefresh.Debt{DirtyParents: 2, PendingEmbeddings: 3, Segments: 4}
	refreshErr := semanticrefresh.NewError(
		semanticrefresh.ErrorFlush,
		run,
		run.ReadinessState,
		debt,
		errors.New("private backend failure"),
	)
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		return semanticrefresh.Result{Run: &run, Debt: debt}, refreshErr
	})

	cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))
	err := cmd.ExecuteContext(t.Context())
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("ExecuteContext error = %#v, want silent exit code 1", err)
	}
	var gotRefreshErr *semanticrefresh.RefreshError
	if !errors.As(err, &gotRefreshErr) || gotRefreshErr != refreshErr {
		t.Fatalf("ExecuteContext lost RefreshError pointer: got %#v want %#v", gotRefreshErr, refreshErr)
	}

	document := decodeOneSyncJSONDocument(t, stdout.Bytes())
	assertFlattenedSyncFields(t, document, stats, "semantic_error")
	if _, exists := document["semantic"]; exists {
		t.Fatal("JSON failure unexpectedly contained semantic")
	}
	var encodedError semanticrefresh.RefreshError
	if err := json.Unmarshal(document["semantic_error"], &encodedError); err != nil {
		t.Fatalf("decode semantic_error: %v", err)
	}
	if encodedError.Code != semanticrefresh.ErrorFlush ||
		encodedError.Stage != store.SemanticRefreshFlush ||
		encodedError.RunID != "run-failed" ||
		encodedError.Checkpoint != "flush:segment-4" ||
		encodedError.Readiness != "not_ready" ||
		encodedError.Debt != debt {
		t.Fatalf("semantic_error = %#v, want bounded refresh diagnostics", encodedError)
	}
}

func TestSyncFamilyHumanOutputIncludesSourceSummaryThenSemanticOutcome(t *testing.T) {
	tests := []struct {
		name       string
		deps       func() semanticRefreshDeps
		wantResult string
	}{
		{
			name: "completed",
			deps: func() semanticRefreshDeps {
				return successfulSyncSemanticDeps(func(
					context.Context,
					semanticrefresh.RunLedger,
					semanticrefresh.StageExecutor,
					semanticrefresh.Request,
				) (semanticrefresh.Result, error) {
					return completedSyncSemanticResult(), nil
				})
			},
			wantResult: "Semantic refresh: completed",
		},
		{
			name: "mode off",
			deps: func() semanticRefreshDeps {
				return semanticRefreshDeps{
					resolve: func(string) (semanticconfig.Config, error) {
						return semanticRefreshTestConfig(semanticconfig.ModeOff), nil
					},
					capability: func() semanticindex.Capability {
						return semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
					},
				}
			},
			wantResult: "Semantic refresh: skipped reason=semantic_mode_off",
		},
		{
			name: "unsupported",
			deps: func() semanticRefreshDeps {
				return semanticRefreshDeps{
					resolve: func(string) (semanticconfig.Config, error) {
						return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
					},
					capability: func() semanticindex.Capability {
						return semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
					},
				}
			},
			wantResult: "Semantic refresh: skipped reason=native_backend_unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			oldRunSyncAll := runSyncAll
			t.Cleanup(func() { runSyncAll = oldRunSyncAll })
			runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
				return syncSemanticTestStats(), nil
			}
			cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, test.deps())
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(false))
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("ExecuteContext: %v", err)
			}
			output := stdout.String()
			summaryIndex := strings.Index(output, "Sync Summary")
			semanticIndex := strings.Index(output, test.wantResult)
			if summaryIndex < 0 || semanticIndex < 0 || summaryIndex >= semanticIndex {
				t.Fatalf("human output did not order source summary before semantic result:\n%s", output)
			}
		})
	}
}

func TestSyncFamilyHumanRefreshFailureShowsCommittedSourceAndBoundedError(t *testing.T) {
	root := t.TempDir()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncSemanticTestStats(), nil
	}
	run := store.SemanticRefreshRun{
		RunID:          "run-human",
		Stage:          store.SemanticRefreshVerify,
		Checkpoint:     "verify:root",
		ReadinessState: "not_ready",
	}
	debt := semanticrefresh.Debt{
		DirtyParents:      1,
		PendingEmbeddings: 2,
		DueRetries:        3,
		ScheduledRetries:  4,
		BlockedEmbeddings: 5,
		FailedEmbeddings:  6,
		Indexed:           7,
		L0Ready:           8,
		Tombstones:        9,
		Segments:          10,
	}
	refreshErr := semanticrefresh.NewError(
		semanticrefresh.ErrorVerify,
		run,
		run.ReadinessState,
		debt,
		errors.New("private verify path"),
	)
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		return semanticrefresh.Result{Run: &run, Debt: debt}, refreshErr
	})

	cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(false))
	err := cmd.ExecuteContext(t.Context())
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Silent {
		t.Fatalf("ExecuteContext error = %#v, want visible exit code 1", err)
	}
	var gotRefreshErr *semanticrefresh.RefreshError
	if !errors.As(err, &gotRefreshErr) || gotRefreshErr != refreshErr {
		t.Fatalf("ExecuteContext lost RefreshError pointer: %#v", gotRefreshErr)
	}
	if !strings.Contains(stdout.String(), "Sync Summary") {
		t.Fatalf("human failure omitted committed source summary:\n%s", stdout.String())
	}
	for _, want := range []string{
		"Semantic refresh failed: code=semantic_verify_failed",
		"run=run-human",
		"stage=verify",
		"checkpoint=verify:root",
		"readiness=not_ready",
		"dirty_parents=1",
		"pending_embeddings=2",
		"due_retries=3",
		"scheduled_retries=4",
		"blocked_embeddings=5",
		"failed_embeddings=6",
		"indexed=7",
		"l0=8",
		"tombstones=9",
		"segments=10",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("bounded human error omitted %q: %s", want, err)
		}
	}
	if strings.Contains(err.Error(), "private verify path") {
		t.Fatalf("human error leaked private cause: %s", err)
	}
}

func TestSyncFamilySupportedBrokenAndCancellationReturnTypedNonzeroErrors(t *testing.T) {
	tests := []struct {
		name     string
		deps     semanticRefreshDeps
		context  func() context.Context
		wantCode string
	}{
		{
			name: "supported broken",
			deps: semanticRefreshDeps{
				resolve: func(string) (semanticconfig.Config, error) {
					return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
				},
				capability: func() semanticindex.Capability {
					return semanticindex.Capability{
						State:   semanticindex.CapabilitySupportedBroken,
						Backend: semanticindex.BackendUSearch,
						Version: semanticindex.USearchVersion,
						Reason:  "load failed at /private/native/root",
					}
				},
			},
			context:  context.Background,
			wantCode: semanticrefresh.ErrorBackendBroken,
		},
		{
			name: "cancelled",
			deps: successfulSyncSemanticDeps(func(
				context.Context,
				semanticrefresh.RunLedger,
				semanticrefresh.StageExecutor,
				semanticrefresh.Request,
			) (semanticrefresh.Result, error) {
				t.Fatal("cancelled refresh reached stage execution")
				return semanticrefresh.Result{}, nil
			}),
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantCode: semanticrefresh.ErrorCancelled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			oldRunSyncAll := runSyncAll
			t.Cleanup(func() { runSyncAll = oldRunSyncAll })
			runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
				return syncSemanticTestStats(), nil
			}
			cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, test.deps)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(true))
			err := cmd.ExecuteContext(test.context())
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
				t.Fatalf("ExecuteContext error = %#v, want silent exit code 1", err)
			}
			var refreshErr *semanticrefresh.RefreshError
			if !errors.As(err, &refreshErr) || refreshErr.Code != test.wantCode {
				t.Fatalf("RefreshError = %#v, want code %q", refreshErr, test.wantCode)
			}
		})
	}
}

func TestSyncFamilyUnchangedSuccessClosesSourceThenRefreshesUnderLock(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })

	now := time.Unix(1_000, 0).UTC()
	stats := syncjob.Stats{
		StartedAt:   now,
		CompletedAt: now,
	}
	var sourceStore *store.Store
	runSyncAll = func(_ context.Context, _ config.Config, st *store.Store, _ syncjob.Options) (syncjob.Stats, error) {
		sourceStore = st
		return stats, nil
	}

	admissions := 0
	refreshes := 0
	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			admissions++
			if sourceStore == nil {
				t.Fatal("semantic admission ran before source store opened")
			}
			if _, sourceErr := sourceStore.RetrievalPurgeEpoch(t.Context()); sourceErr == nil {
				t.Fatal("semantic admission observed source store still open")
			}
			probe, lockErr := acquireSyncAllLock(cfg, "semantic-admission-probe")
			if lockErr == nil {
				_ = probe.Close()
				t.Fatal("coarse sync lock was released before semantic admission")
			}
			if !isSyncAllAlreadyRunning(lockErr) {
				t.Fatalf("semantic admission lock probe error = %v", lockErr)
			}
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
		capability: func() semanticindex.Capability {
			return semanticindex.Capability{
				State:   semanticindex.CapabilitySupportedReady,
				Backend: semanticindex.BackendUSearch,
				Version: semanticindex.USearchVersion,
			}
		},
		openWritable: store.Open,
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return &semanticRefreshTestProvider{info: embedding.Info{
				Provider:   "ollama",
				Model:      "test-embedding-v1",
				Dimensions: 2,
			}}, nil
		},
		nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
			return &semanticRefreshTestNative{}, nil
		},
		runRefresh: func(
			context.Context,
			semanticrefresh.RunLedger,
			semanticrefresh.StageExecutor,
			semanticrefresh.Request,
		) (semanticrefresh.Result, error) {
			refreshes++
			return completedSyncSemanticResult(), nil
		},
	}

	cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if admissions != 1 || refreshes != 1 {
		t.Fatalf("semantic admissions=%d refreshes=%d, want 1 each", admissions, refreshes)
	}
	released, err := acquireSyncAllLock(cfg, "post-command-probe")
	if err != nil {
		t.Fatalf("coarse sync lock remained held after command: %v", err)
	}
	_ = released.Close()
}

func TestSyncFamilyExecutionStateDoesNotLeakAcrossBareRepeatedOrFailedRuns(t *testing.T) {
	root := t.TempDir()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	sourceCalls := 0
	sourceErr := errors.New("later source failure")
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		sourceCalls++
		if sourceCalls == 3 {
			return syncjob.Stats{}, sourceErr
		}
		return syncSemanticTestStats(), nil
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
	cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true

	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("bare sync: %v", err)
	}
	if refreshes != 0 {
		t.Fatalf("bare sync refreshes = %d, want 0", refreshes)
	}

	for execution := 1; execution <= 2; execution++ {
		cmd.SetArgs(syncSemanticTestArgs(true))
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Fatalf("successful execution %d: %v", execution, err)
		}
		if refreshes != execution {
			t.Fatalf("refreshes after execution %d = %d, want %d", execution, refreshes, execution)
		}
	}

	cmd.SetArgs(syncSemanticTestArgs(true))
	err := cmd.ExecuteContext(t.Context())
	if !errors.Is(err, sourceErr) {
		t.Fatalf("failed execution error = %v, want source error", err)
	}
	if refreshes != 2 {
		t.Fatalf("source failure reused stale completion; refreshes = %d, want 2", refreshes)
	}
}

func TestSyncFamilyRegisteredLeavesUseCentralPostHookWithoutShadowing(t *testing.T) {
	root := t.TempDir()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncSemanticTestStats(), nil
	}
	refreshes := 0
	cmd := newSyncCommandWithSemanticDeps(
		&rootOptions{root: root},
		successfulSyncSemanticDeps(func(
			context.Context,
			semanticrefresh.RunLedger,
			semanticrefresh.StageExecutor,
			semanticrefresh.Request,
		) (semanticrefresh.Result, error) {
			refreshes++
			return completedSyncSemanticResult(), nil
		}),
	)
	if cmd.PersistentPostRunE == nil {
		t.Fatal("sync family omitted central PersistentPostRunE")
	}
	leaves := cmd.Commands()
	if len(leaves) == 0 {
		t.Fatal("sync family has no registered leaves")
	}
	for _, leaf := range leaves {
		assertNoDescendantPersistentPostHook(t, leaf)
		switch leaf.Name() {
		case "all":
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(true))
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("execute registered leaf %q: %v", leaf.Name(), err)
			}
		default:
			t.Fatalf("registered sync leaf %q lacks a central-boundary invariant case", leaf.Name())
		}
	}
	if refreshes != len(leaves) {
		t.Fatalf("central refreshes = %d, want one for each of %d registered leaves", refreshes, len(leaves))
	}
}

func TestSyncFamilyCoarseLockSpansTerminalJSONOutput(t *testing.T) {
	tests := []struct {
		name      string
		refresh   func() (semanticrefresh.Result, error)
		wantError bool
	}{
		{
			name: "success",
			refresh: func() (semanticrefresh.Result, error) {
				return completedSyncSemanticResult(), nil
			},
		},
		{
			name: "failure",
			refresh: func() (semanticrefresh.Result, error) {
				run := store.SemanticRefreshRun{RunID: "run-lock", Stage: store.SemanticRefreshFlush}
				return semanticrefresh.Result{Run: &run}, semanticrefresh.NewError(
					semanticrefresh.ErrorFlush,
					run,
					"not_ready",
					semanticrefresh.Debt{},
					errors.New("private flush failure"),
				)
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load(root)
			if err != nil {
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
				probe, lockErr := acquireSyncAllLock(cfg, "refresh-lock-probe")
				if lockErr == nil {
					_ = probe.Close()
					t.Fatal("coarse sync lock was released during semantic refresh")
				}
				if !isSyncAllAlreadyRunning(lockErr) {
					t.Fatalf("refresh lock probe error = %v", lockErr)
				}
				return test.refresh()
			})
			output := &syncLockObservingWriter{cfg: cfg}
			cmd := newSyncCommandWithSemanticDeps(&rootOptions{root: root}, deps)
			cmd.SetOut(output)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(true))
			err = cmd.ExecuteContext(t.Context())
			if test.wantError && err == nil {
				t.Fatal("ExecuteContext succeeded, want refresh failure")
			}
			if !test.wantError && err != nil {
				t.Fatalf("ExecuteContext: %v", err)
			}
			if output.writes == 0 {
				t.Fatal("terminal JSON output was not written")
			}
			if output.lockReleased {
				t.Fatal("coarse sync lock was released before terminal JSON output")
			}
			released, lockErr := acquireSyncAllLock(cfg, "terminal-lock-probe")
			if lockErr != nil {
				t.Fatalf("coarse sync lock remained held after terminal result: %v", lockErr)
			}
			_ = released.Close()
		})
	}
}

func assertNoDescendantPersistentPostHook(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	if cmd.PersistentPostRunE != nil || cmd.PersistentPostRun != nil {
		t.Fatalf("sync descendant %q shadows the family persistent post hook", cmd.CommandPath())
	}
	for _, child := range cmd.Commands() {
		assertNoDescendantPersistentPostHook(t, child)
	}
}

type syncLockObservingWriter struct {
	cfg          config.Config
	buffer       bytes.Buffer
	writes       int
	lockReleased bool
}

func (w *syncLockObservingWriter) Write(data []byte) (int, error) {
	w.writes++
	probe, err := acquireSyncAllLock(w.cfg, "output-lock-probe")
	if err == nil {
		w.lockReleased = true
		_ = probe.Close()
	}
	return w.buffer.Write(data)
}

func successfulSyncSemanticDeps(
	runRefresh func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error),
) semanticRefreshDeps {
	return semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
		capability: func() semanticindex.Capability {
			return semanticindex.Capability{
				State:   semanticindex.CapabilitySupportedReady,
				Backend: semanticindex.BackendUSearch,
				Version: semanticindex.USearchVersion,
			}
		},
		openWritable: store.Open,
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return &semanticRefreshTestProvider{info: embedding.Info{
				Provider:   "ollama",
				Model:      "test-embedding-v1",
				Dimensions: 2,
			}}, nil
		},
		nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
			return &semanticRefreshTestNative{}, nil
		},
		runRefresh: runRefresh,
	}
}

func completedSyncSemanticResult() semanticrefresh.Result {
	return semanticrefresh.Result{
		Outcome: semanticrefresh.OutcomeCompleted,
		Run: &store.SemanticRefreshRun{
			RunID:               "run-1",
			ProfileID:           "profile-1",
			CurrentGenerationID: "generation-1",
			State:               store.SemanticRefreshRunCompleted,
			Stage:               store.SemanticRefreshReadiness,
			ReadinessState:      "ready",
		},
		Debt: semanticrefresh.Debt{},
	}
}

func syncSemanticTestStats() syncjob.Stats {
	started := time.Unix(1_000, 123).UTC()
	return syncjob.Stats{
		StartedAt:   started,
		CompletedAt: started.Add(2 * time.Second),
		Duration:    2 * time.Second,
	}
}

func decodeOneSyncJSONDocument(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var document map[string]json.RawMessage
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode JSON document: %v\n%s", err, data)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON output contained trailing value/data: err=%v trailing=%#v\n%s", err, trailing, data)
	}
	return document
}

func assertFlattenedSyncFields(
	t *testing.T,
	document map[string]json.RawMessage,
	stats syncjob.Stats,
	semanticKey string,
) {
	t.Helper()
	encodedStats, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var sourceFields map[string]json.RawMessage
	if err := json.Unmarshal(encodedStats, &sourceFields); err != nil {
		t.Fatal(err)
	}
	if len(document) != len(sourceFields)+1 {
		t.Fatalf("JSON keys = %v, want source keys plus only %q", reflect.ValueOf(document).MapKeys(), semanticKey)
	}
	for key, want := range sourceFields {
		got, exists := document[key]
		if !exists {
			t.Fatalf("JSON omitted existing sync field %q", key)
		}
		var gotValue any
		var wantValue any
		if err := json.Unmarshal(got, &gotValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want, &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("JSON field %q = %#v, want %#v", key, gotValue, wantValue)
		}
	}
	if _, exists := document[semanticKey]; !exists {
		t.Fatalf("JSON omitted %q", semanticKey)
	}
}

func syncSemanticTestArgs(jsonOut bool) []string {
	args := []string{
		"all",
		"--skip-x-bookmarks",
		"--skip-x",
		"--skip-x-media",
		"--skip-x-photo-ocr",
		"--skip-links",
		"--skip-github",
		"--skip-youtube",
		"--skip-apple-notes",
		"--skip-safari-tabs",
		"--skip-feeds",
		"--skip-sources",
		"--skip-categorize",
		"--skip-okf-export",
	}
	if jsonOut {
		args = append(args, "--json")
	}
	return args
}
