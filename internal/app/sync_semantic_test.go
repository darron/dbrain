package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
)

func TestSyncFamilyAutomaticInitialBackfillResumesCommittedWork(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 19, 0, 0, 0, time.UTC)
	sourceCalls := 0
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(
		ctx context.Context,
		_ config.Config,
		st *store.Store,
		_ syncjob.Options,
	) (syncjob.Stats, error) {
		sourceCalls++
		if sourceCalls == 1 || sourceCalls == 3 {
			notePath := "items/initial-backfill.md"
			if sourceCalls == 3 {
				notePath = "items/initial-backfill-moved.md"
			}
			seedSyncSemanticBackfillItem(
				t,
				ctx,
				st,
				syncSemanticDistinctFlushText(store.RetrievalSegmentTarget),
				notePath,
				now,
			)
		}
		stats := syncSemanticTestStats()
		if sourceCalls > 1 {
			stats = syncjob.Stats{
				StartedAt:   stats.StartedAt,
				CompletedAt: stats.CompletedAt,
				Duration:    stats.Duration,
			}
		}
		return stats, nil
	}

	provider := newSyncSemanticResumeProvider()
	native := &syncSemanticResumeNative{}
	var progressMu sync.Mutex
	progressByRun := make(map[int][]semanticrefresh.Progress)
	refreshCalls := 0
	deps := semanticRefreshDeps{
		resolve: func(root string) (semanticconfig.Config, error) {
			if root != cfg.RootDir {
				t.Fatalf("semantic root=%q want=%q", root, cfg.RootDir)
			}
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
		capability: semanticRefreshReadyCapability,
		openWritable: func(path string, _ string) (*store.Store, error) {
			if path != cfg.DBPath {
				t.Fatalf("semantic DB=%q want=%q", path, cfg.DBPath)
			}
			return store.Open(path)
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return provider, nil
		},
		nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
			return native, nil
		},
		embeddingBatch: semanticbuild.MaxEmbeddingBatchSize,
		runRefresh: func(
			ctx context.Context,
			ledger semanticrefresh.RunLedger,
			executor semanticrefresh.StageExecutor,
			request semanticrefresh.Request,
		) (semanticrefresh.Result, error) {
			refreshCalls++
			execution := refreshCalls
			runCtx := ctx
			cancel := func() {}
			if execution == 1 {
				runCtx, cancel = context.WithCancel(ctx)
			}
			defer cancel()
			downstreamProgress := request.Progress
			request.Now = func() time.Time { return now }
			request.Progress = func(progress semanticrefresh.Progress) error {
				progressMu.Lock()
				progressByRun[execution] = append(progressByRun[execution], progress)
				progressMu.Unlock()
				if downstreamProgress != nil {
					if err := downstreamProgress(progress); err != nil {
						return err
					}
				}
				if execution == 1 && progress.Counters.FlushedVectors > 0 {
					cancel()
				}
				return nil
			}
			return semanticrefresh.Run(runCtx, ledger, executor, request)
		},
	}

	firstCommand := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var firstOutput bytes.Buffer
	firstCommand.SetOut(&firstOutput)
	firstCommand.SetErr(io.Discard)
	firstCommand.SilenceUsage = true
	firstCommand.SetArgs(syncSemanticTestArgs(true))
	err = firstCommand.ExecuteContext(t.Context())
	var firstExit *ExitError
	if !errors.As(err, &firstExit) || firstExit.Code != 1 || !firstExit.Silent {
		t.Fatalf("first ExecuteContext error=%#v want silent exit 1", err)
	}
	var cancelled *semanticrefresh.RefreshError
	if !errors.As(err, &cancelled) || cancelled.Code != semanticrefresh.ErrorCancelled {
		t.Fatalf("first refresh error=%#v want %q", cancelled, semanticrefresh.ErrorCancelled)
	}
	firstDocument := decodeOneSyncJSONDocument(t, firstOutput.Bytes())
	if _, exists := firstDocument["semantic_error"]; !exists {
		t.Fatal("cancelled initial backfill omitted semantic_error")
	}
	var firstSemantic semanticRefreshResultOutput
	if err := json.Unmarshal(firstDocument["semantic"], &firstSemantic); err != nil {
		t.Fatalf("decode cancelled semantic lifecycle: %v", err)
	}
	if firstSemantic.Outcome == semanticrefresh.OutcomeCompleted {
		t.Fatalf("cancelled initial backfill emitted completed semantic outcome: %+v", firstSemantic)
	}

	profileID, err := semanticbuild.Profile(provider.Info()).ID()
	if err != nil {
		t.Fatal(err)
	}
	firstStore, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := firstStore.LatestSemanticRefreshRun(t.Context(), profileID)
	if err != nil {
		_ = firstStore.Close()
		t.Fatal(err)
	}
	firstProfile, err := firstStore.RetrievalEmbeddingProfile(t.Context(), profileID)
	if err != nil {
		_ = firstStore.Close()
		t.Fatal(err)
	}
	firstSegments, err := firstStore.RetrievalIndexGenerationSegments(
		t.Context(),
		firstProfile.ActiveGenerationID,
	)
	if err != nil {
		_ = firstStore.Close()
		t.Fatal(err)
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	firstProviderCalls, firstProviderTexts, firstDuplicateTexts := provider.snapshot()
	if firstRun == nil ||
		firstRun.State != store.SemanticRefreshRunCancelled ||
		firstRun.Stage != store.SemanticRefreshFlush ||
		firstRun.Counters.ProjectedParents != 1 ||
		firstRun.Counters.EmbeddedChunks <= 0 ||
		firstRun.Counters.FlushedVectors != int64(store.RetrievalSegmentTarget) {
		t.Fatalf("cancelled durable run=%+v", firstRun)
	}
	if firstProviderCalls <= 0 ||
		firstProviderTexts != int(firstRun.Counters.EmbeddedChunks) ||
		firstProviderTexts != store.RetrievalSegmentTarget+2 ||
		firstDuplicateTexts != 0 ||
		firstProfile.ActiveGenerationID == "" ||
		firstProfile.ActiveGenerationID != firstRun.CurrentGenerationID ||
		firstProfile.ActiveIndexedCount != store.RetrievalSegmentTarget ||
		firstProfile.L0ReadyCount != 2 ||
		len(firstSegments) != 1 ||
		firstSegments[0].IndexedChunkCount != store.RetrievalSegmentTarget {
		t.Fatalf(
			"first committed work: provider_calls=%d provider_texts=%d duplicate_texts=%d profile=%+v segments=%+v run=%+v",
			firstProviderCalls,
			firstProviderTexts,
			firstDuplicateTexts,
			firstProfile,
			firstSegments,
			firstRun,
		)
	}
	if builds, verifies := native.snapshot(); builds != 1 || verifies != 0 {
		t.Fatalf("native work at cancelled flush: builds=%d verifies=%d", builds, verifies)
	}

	secondCommand := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var secondOutput bytes.Buffer
	secondCommand.SetOut(&secondOutput)
	secondCommand.SetErr(io.Discard)
	secondCommand.SilenceUsage = true
	secondCommand.SetArgs(syncSemanticTestArgs(true))
	if err := secondCommand.ExecuteContext(t.Context()); err != nil {
		diagnosticStore, openErr := store.Open(cfg.DBPath)
		if openErr != nil {
			t.Fatalf("second ExecuteContext: %v output=%s diagnostic_open=%v", err, secondOutput.String(), openErr)
		}
		snapshot, snapshotErr := diagnosticStore.SemanticReadinessSnapshotAt(
			t.Context(),
			semanticbuild.Profile(provider.Info()),
			semanticreadiness.DefaultExactMaxChunks,
			now,
		)
		_ = diagnosticStore.Close()
		t.Fatalf(
			"second ExecuteContext: %v output=%s snapshot=%+v decision=%+v snapshot_err=%v",
			err,
			secondOutput.String(),
			snapshot,
			semanticreadiness.Evaluate(snapshot),
			snapshotErr,
		)
	}
	secondDocument := decodeOneSyncJSONDocument(t, secondOutput.Bytes())
	var completed semanticRefreshResultOutput
	if err := json.Unmarshal(secondDocument["semantic"], &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Outcome != semanticrefresh.OutcomeCompleted ||
		completed.Run == nil ||
		completed.Run.State != store.SemanticRefreshRunCompleted ||
		completed.Run.Stage != store.SemanticRefreshReadiness ||
		completed.Run.ReadinessState != "ready" {
		t.Fatalf("final supported-enabled result=%+v want completed ready", completed)
	}
	if completed.Run.RunID != firstRun.RunID {
		t.Fatalf("final run ID=%q want resumed %q", completed.Run.RunID, firstRun.RunID)
	}
	if completed.Run.Counters.ProjectedParents != firstRun.Counters.ProjectedParents {
		t.Fatalf(
			"projection counter repeated: first=%d final=%d",
			firstRun.Counters.ProjectedParents,
			completed.Run.Counters.ProjectedParents,
		)
	}
	if completed.Run.Counters.EmbeddedChunks != firstRun.Counters.EmbeddedChunks ||
		completed.Run.Counters.FlushedVectors != firstRun.Counters.FlushedVectors ||
		completed.Run.CurrentGenerationID != firstRun.CurrentGenerationID {
		t.Fatalf("resumed run repeated committed work: first=%+v final=%+v", firstRun, completed.Run)
	}
	finalProviderCalls, finalProviderTexts, finalDuplicateTexts := provider.snapshot()
	if finalProviderCalls != firstProviderCalls ||
		finalProviderTexts != firstProviderTexts ||
		finalDuplicateTexts != 0 ||
		finalProviderTexts != int(completed.Run.Counters.EmbeddedChunks) {
		t.Fatalf(
			"resumed provider work: first=%d/%d final=%d/%d duplicate_texts=%d counters=%+v",
			firstProviderCalls,
			firstProviderTexts,
			finalProviderCalls,
			finalProviderTexts,
			finalDuplicateTexts,
			completed.Run.Counters,
		)
	}
	if builds, verifies := native.snapshot(); builds != 1 || verifies != 1 {
		t.Fatalf("native lifecycle builds=%d verifies=%d want physical flush and root proof once", builds, verifies)
	}

	finalStore, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	finalProfile, err := finalStore.RetrievalEmbeddingProfile(t.Context(), profileID)
	if err != nil {
		t.Fatal(err)
	}
	if finalProfile.ActiveIndexedCount != store.RetrievalSegmentTarget ||
		finalProfile.L0ReadyCount != 2 ||
		finalProfile.ActiveGenerationID != firstProfile.ActiveGenerationID ||
		finalProfile.ActiveIndexedCount+finalProfile.L0ReadyCount != finalProviderTexts {
		t.Fatalf(
			"final index units=%+v provider_texts=%d",
			finalProfile,
			finalProviderTexts,
		)
	}
	runtimeSnapshot, err := finalStore.SemanticRuntimeReadinessSnapshotAt(
		t.Context(),
		semanticbuild.Profile(provider.Info()),
		semanticreadiness.DefaultExactMaxChunks,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSnapshot.Configured = true
	runtimeSnapshot.Enabled = true
	if decision := semanticreadiness.Evaluate(runtimeSnapshot); decision.State != semanticreadiness.StateReady {
		t.Fatalf("final runtime readiness=%+v snapshot=%+v", decision, runtimeSnapshot)
	}
	if err := finalStore.Close(); err != nil {
		t.Fatal(err)
	}

	thirdCommand := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var thirdOutput bytes.Buffer
	thirdCommand.SetOut(&thirdOutput)
	thirdCommand.SetErr(io.Discard)
	thirdCommand.SilenceUsage = true
	thirdCommand.SetArgs(syncSemanticTestArgs(true))
	if err := thirdCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("third ExecuteContext after projected metadata update: %v output=%s", err, thirdOutput.String())
	}
	thirdDocument := decodeOneSyncJSONDocument(t, thirdOutput.Bytes())
	var refreshed semanticRefreshResultOutput
	if err := json.Unmarshal(thirdDocument["semantic"], &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Outcome != semanticrefresh.OutcomeCompleted || refreshed.Run == nil ||
		refreshed.Run.State != store.SemanticRefreshRunCompleted ||
		refreshed.Run.ReadinessState != "ready" {
		t.Fatalf("metadata-only follow-up result=%+v want completed ready", refreshed)
	}
	if refreshed.Run.CurrentGenerationID != firstProfile.ActiveGenerationID ||
		refreshed.Run.Counters.FlushedVectors != 0 {
		t.Fatalf("metadata-only follow-up rebuilt root: first=%q follow-up=%+v", firstProfile.ActiveGenerationID, refreshed.Run)
	}
	thirdProviderCalls, thirdProviderTexts, thirdDuplicateTexts := provider.snapshot()
	if thirdProviderCalls != finalProviderCalls || thirdProviderTexts != finalProviderTexts || thirdDuplicateTexts != 0 {
		t.Fatalf(
			"metadata-only follow-up repeated embedding work: before=%d/%d after=%d/%d duplicates=%d",
			finalProviderCalls,
			finalProviderTexts,
			thirdProviderCalls,
			thirdProviderTexts,
			thirdDuplicateTexts,
		)
	}
	thirdStore, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = thirdStore.Close() }()
	thirdProfile, err := thirdStore.RetrievalEmbeddingProfile(t.Context(), profileID)
	if err != nil {
		t.Fatal(err)
	}
	if thirdProfile.ActiveGenerationID != firstProfile.ActiveGenerationID ||
		thirdProfile.ActiveIndexedCount != store.RetrievalSegmentTarget ||
		thirdProfile.L0ReadyCount != 2 {
		t.Fatalf("metadata-only follow-up profile=%+v", thirdProfile)
	}

	progressMu.Lock()
	firstProgress := append([]semanticrefresh.Progress(nil), progressByRun[1]...)
	secondProgress := append([]semanticrefresh.Progress(nil), progressByRun[2]...)
	progressMu.Unlock()
	if !syncSemanticProgressIncludes(firstProgress, store.SemanticRefreshProjection) ||
		!syncSemanticProgressIncludes(firstProgress, store.SemanticRefreshEmbedding) ||
		!syncSemanticProgressIncludes(firstProgress, store.SemanticRefreshFlush) {
		t.Fatalf("first progress stages=%v want projection, embedding, and flush", syncSemanticProgressStages(firstProgress))
	}
	if syncSemanticProgressIncludes(secondProgress, store.SemanticRefreshProjection) ||
		syncSemanticProgressIncludes(secondProgress, store.SemanticRefreshEmbedding) {
		t.Fatalf("resumed progress repeated projection or embedding: %v", syncSemanticProgressStages(secondProgress))
	}
	for _, stage := range []store.SemanticRefreshStage{
		store.SemanticRefreshVerify,
		store.SemanticRefreshReadiness,
	} {
		if !syncSemanticProgressIncludes(secondProgress, stage) {
			t.Fatalf("resumed progress stages=%v missing %s", syncSemanticProgressStages(secondProgress), stage)
		}
	}
	if sourceCalls != 3 || refreshCalls != 3 {
		t.Fatalf("source calls=%d refresh calls=%d want three real command executions", sourceCalls, refreshCalls)
	}
}

func TestSyncFamilySemanticAdmissionSkipsWritableDependencies(t *testing.T) {
	tests := []struct {
		name       string
		mode       semanticconfig.Mode
		capability semanticindex.Capability
		wantSkip   string
		wantCode   string
	}{
		{
			name:       "mode off",
			mode:       semanticconfig.ModeOff,
			capability: semanticRefreshReadyCapability(),
			wantSkip:   "semantic_mode_off",
		},
		{
			name:       "unsupported build",
			mode:       semanticconfig.ModeOn,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantSkip:   "native_backend_unsupported",
		},
		{
			name: "supported broken",
			mode: semanticconfig.ModeOn,
			capability: semanticindex.Capability{
				State:   semanticindex.CapabilitySupportedBroken,
				Backend: semanticindex.BackendUSearch,
				Version: semanticindex.USearchVersion,
				Reason:  "deterministic load failure",
			},
			wantCode: semanticrefresh.ErrorBackendBroken,
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
			writableCalls := make(map[string]int)
			deps := semanticRefreshDeps{
				resolve: func(string) (semanticconfig.Config, error) {
					return semanticRefreshTestConfig(test.mode), nil
				},
				capability: func() semanticindex.Capability {
					return test.capability
				},
				openWritable: func(string, string) (*store.Store, error) {
					writableCalls["store"]++
					return nil, errors.New("unexpected writable store")
				},
				provider: func(semanticconfig.Config) (embedding.Provider, error) {
					writableCalls["provider"]++
					return nil, errors.New("unexpected provider")
				},
				nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
					writableCalls["native"]++
					return nil, errors.New("unexpected native")
				},
				runRefresh: func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error) {
					writableCalls["runner"]++
					return semanticrefresh.Result{}, errors.New("unexpected runner")
				},
			}
			cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(true))
			err := cmd.ExecuteContext(t.Context())
			if len(writableCalls) != 0 {
				t.Fatalf("admission constructed writable dependencies: %v", writableCalls)
			}
			if test.wantCode != "" {
				var refreshErr *semanticrefresh.RefreshError
				if !errors.As(err, &refreshErr) || refreshErr.Code != test.wantCode {
					t.Fatalf("error=%v want typed code %q", err, test.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			document := decodeOneSyncJSONDocument(t, stdout.Bytes())
			var result semanticRefreshResultOutput
			if err := json.Unmarshal(document["semantic"], &result); err != nil {
				t.Fatal(err)
			}
			if result.Outcome != semanticrefresh.OutcomeSkipped ||
				result.SkipReason != test.wantSkip {
				t.Fatalf("semantic result=%+v want skip %q", result, test.wantSkip)
			}
		})
	}
}

func TestSyncFamilySourceFailureContinuesSemanticAdmission(t *testing.T) {
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
	cmd := newSyncSemanticTestCommand(t,
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
			openWritable: func(string, string) (*store.Store, error) {
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
	if semanticCalls["resolve"] != 1 || semanticCalls["capability"] != 1 {
		t.Fatalf("semantic calls = %#v, want one admission", semanticCalls)
	}
	if !strings.Contains(stdout.String(), `"semantic"`) {
		t.Fatalf("stdout = %q, want semantic result output", stdout.String())
	}
	released, lockErr := acquireSyncAllLock(cfg, "source-error-probe")
	if lockErr != nil {
		t.Fatalf("source failure left coarse sync lock held: %v", lockErr)
	}
	_ = released.Close()
}

func TestSyncFamilySourceAndSemanticFailuresRemainJoined(t *testing.T) {
	root := t.TempDir()
	sourceErr := errors.New("source sync failed")
	semanticErr := semanticrefresh.NewError(
		semanticrefresh.ErrorEmbedding,
		store.SemanticRefreshRun{RunID: "source-plus-semantic", Stage: store.SemanticRefreshEmbedding},
		"",
		semanticrefresh.Debt{},
		errors.New("private semantic failure"),
	)
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncSemanticTestStats(), sourceErr
	}
	refreshes := 0
	deps := successfulSyncSemanticDeps(func(
		context.Context,
		semanticrefresh.RunLedger,
		semanticrefresh.StageExecutor,
		semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		refreshes++
		return semanticrefresh.Result{}, semanticErr
	})

	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))

	err := cmd.ExecuteContext(t.Context())
	if !errors.Is(err, sourceErr) || !errors.Is(err, semanticErr) {
		t.Fatalf("joined execution error = %v, want source and semantic causes", err)
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || !exitErr.Silent {
		t.Fatalf("execution error = %#v, want silent JSON ExitError", err)
	}
	if refreshes != 1 {
		t.Fatalf("semantic refreshes = %d, want 1", refreshes)
	}
	document := decodeOneSyncJSONDocument(t, stdout.Bytes())
	if _, exists := document["semantic_error"]; !exists {
		t.Fatal("joined source and semantic failure omitted semantic_error")
	}
}

func TestSyncFamilySourceCancellationSkipsSemanticAdmission(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(ctx context.Context, _ config.Config, _ *store.Store, _ syncjob.Options) (syncjob.Stats, error) {
		cancel()
		return syncjob.Stats{}, ctx.Err()
	}
	semanticCalls := 0
	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			semanticCalls++
			return semanticRefreshTestConfig(semanticconfig.ModeOff), nil
		},
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))

	err := cmd.ExecuteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if semanticCalls != 0 {
		t.Fatalf("semantic admissions after cancellation = %d, want 0", semanticCalls)
	}
}

func TestSyncFamilyJSONSuccessFlattensSourceStatsAndSemanticResult(t *testing.T) {
	root := t.TempDir()
	stats := syncSemanticTestStats()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return stats, nil
	}

	cmd := newSyncSemanticTestCommand(t,
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

	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
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
	if _, exists := document["semantic"]; !exists {
		t.Fatal("JSON failure omitted semantic lifecycle")
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

func TestSyncFamilyHumanOutputEndsWithUnifiedSummaryIncludingSemanticOutcome(t *testing.T) {
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
			wantResult: "status=completed",
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
			wantResult: "reason=semantic_mode_off",
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
			wantResult: "reason=native_backend_unsupported",
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
			cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, test.deps())
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(false))
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("ExecuteContext: %v", err)
			}
			output := stdout.String()
			if !strings.Contains(output, "Sync Summary") || !strings.Contains(output, "Semantic Refresh") || !strings.Contains(output, test.wantResult) {
				t.Fatalf("unified summary omitted semantic outcome:\n%s", output)
			}
			if strings.Contains(output, "Semantic refresh:") {
				t.Fatalf("human output appended a duplicate raw semantic terminal block:\n%s", output)
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

	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
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

func TestSyncFamilySemanticProgressFailureDoesNotRenderCompletion(t *testing.T) {
	root := t.TempDir()
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return syncSemanticTestStats(), nil
	}
	run := store.SemanticRefreshRun{RunID: "run-progress-failure", Stage: store.SemanticRefreshEmbedding}
	refreshErr := semanticrefresh.NewError(
		semanticrefresh.ErrorEmbedding,
		run,
		"not_ready",
		semanticrefresh.Debt{PendingEmbeddings: 75},
		errors.New("private embedding failure"),
	)
	deps := successfulSyncSemanticDeps(func(
		_ context.Context,
		_ semanticrefresh.RunLedger,
		_ semanticrefresh.StageExecutor,
		request semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		if request.Progress == nil {
			t.Fatal("semantic refresh omitted progress callback")
		}
		if err := request.Progress(semanticrefresh.Progress{
			Stage: store.SemanticRefreshEmbedding,
			Work:  semanticrefresh.StageWork{Current: 25, Total: 100, TotalKnown: true},
			Debt:  semanticrefresh.Debt{PendingEmbeddings: 75},
		}); err != nil {
			t.Fatalf("progress callback: %v", err)
		}
		return semanticrefresh.Result{Run: &run}, refreshErr
	})

	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var stderr bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(false))
	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Fatal("ExecuteContext succeeded, want semantic refresh failure")
	}

	got := stderr.String()
	if !strings.Contains(got, "Semantic embedding failed:") {
		t.Fatalf("semantic progress failure omitted terminal failure:\n%s", got)
	}
	if strings.Contains(got, "Semantic embedding complete:") || strings.Contains(got, "✓ Semantic embedding") {
		t.Fatalf("semantic progress failure rendered completion:\n%s", got)
	}
}

func TestSyncFamilySupportedBrokenAndCancellationReturnTypedNonzeroErrors(t *testing.T) {
	tests := []struct {
		name          string
		deps          semanticRefreshDeps
		context       func() context.Context
		wantCode      string
		wantCancelled bool
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
			wantCancelled: true,
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
			cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, test.deps)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(true))
			err := cmd.ExecuteContext(test.context())
			if test.wantCancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled execution error = %v, want context cancellation", err)
				}
				return
			}
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
		openWritable: func(path string, _ string) (*store.Store, error) { return store.Open(path) },
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

	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
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
	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
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
	if refreshes != 3 {
		t.Fatalf("source failure reused stale completion; refreshes = %d, want 3", refreshes)
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
	cmd := newSyncSemanticTestCommand(t,
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
			cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
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

func newSyncSemanticTestCommand(
	t *testing.T,
	root *rootOptions,
	deps semanticRefreshDeps,
) *cobra.Command {
	t.Helper()
	cfg, err := config.Load(root.root)
	if err != nil {
		t.Fatalf("load sync semantic test config: %v", err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)
	return newSyncCommandWithSemanticDeps(root, deps)
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
		openWritable: func(path string, _ string) (*store.Store, error) { return store.Open(path) },
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
	extraKeys := 1
	if semanticKey == "semantic_error" {
		extraKeys = 2
	}
	if len(document) != len(sourceFields)+extraKeys {
		t.Fatalf("JSON keys = %v, want source keys plus semantic lifecycle and %q", reflect.ValueOf(document).MapKeys(), semanticKey)
	}
	if rawSemantic, exists := document["semantic"]; exists {
		var semantic struct {
			Duration time.Duration `json:"duration"`
		}
		if err := json.Unmarshal(rawSemantic, &semantic); err != nil {
			t.Fatal(err)
		}
		stats = completeSyncStatsWithSemantic(stats, semanticrefresh.Result{Duration: semantic.Duration})
		encodedStats, err = json.Marshal(stats)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encodedStats, &sourceFields); err != nil {
			t.Fatal(err)
		}
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

func seedSyncSemanticBackfillItem(
	t *testing.T,
	ctx context.Context,
	st *store.Store,
	text string,
	notePath string,
	now time.Time,
) {
	t.Helper()
	digest := sha256.Sum256([]byte(text + "\x00" + notePath))
	_, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "sync-semantic:initial-backfill",
		SourceType:   "sync_semantic_test",
		ExternalID:   "initial-backfill",
		CanonicalURL: "https://example.test/initial-backfill",
		Title:        "Initial semantic backfill",
		Text:         text,
		ContentHash:  hex.EncodeToString(digest[:]),
		NotePath:     notePath,
		RawJSON:      "{}",
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func syncSemanticDistinctFlushText(windows int) string {
	var text strings.Builder
	text.Grow(windows*1_800 + 1)
	padding := strings.Repeat("x", 1_792)
	for index := 0; index < windows; index++ {
		_, _ = fmt.Fprintf(&text, "%08x", index)
		text.WriteString(padding)
	}
	text.WriteByte('z')
	return text.String()
}

func syncSemanticProgressIncludes(
	progress []semanticrefresh.Progress,
	stage store.SemanticRefreshStage,
) bool {
	for _, update := range progress {
		if update.Stage == stage {
			return true
		}
	}
	return false
}

func syncSemanticProgressStages(progress []semanticrefresh.Progress) []store.SemanticRefreshStage {
	stages := make([]store.SemanticRefreshStage, 0, len(progress))
	for _, update := range progress {
		stages = append(stages, update.Stage)
	}
	return stages
}

type syncSemanticResumeProvider struct {
	mu    sync.Mutex
	info  embedding.Info
	calls int
	texts int
	seen  map[string]int
}

func newSyncSemanticResumeProvider() *syncSemanticResumeProvider {
	return &syncSemanticResumeProvider{
		info: semanticRefreshTestInfo(),
		seen: make(map[string]int),
	}
}

func (p *syncSemanticResumeProvider) Info() embedding.Info {
	return p.info
}

func (p *syncSemanticResumeProvider) Embed(
	_ context.Context,
	request embedding.Request,
) (embedding.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.texts += len(request.Texts)
	vectors := make([][]float32, len(request.Texts))
	for index, text := range request.Texts {
		p.seen[text]++
		vectors[index] = []float32{0.6, 0.8}
	}
	return embedding.Response{
		Vectors:    vectors,
		Provider:   p.info.Provider,
		Model:      p.info.Model,
		Dimensions: p.info.Dimensions,
	}, nil
}

func (p *syncSemanticResumeProvider) snapshot() (calls int, texts int, duplicates int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, count := range p.seen {
		if count > 1 {
			duplicates += count - 1
		}
	}
	return p.calls, p.texts, duplicates
}

type syncSemanticResumeNative struct {
	mu       sync.Mutex
	builds   int
	verifies int
}

func (n *syncSemanticResumeNative) Build(
	_ context.Context,
	rows []store.RetrievalEmbeddingRow,
) (func(io.Writer) error, error) {
	if len(rows) != store.RetrievalSegmentTarget {
		return nil, fmt.Errorf("native build rows=%d", len(rows))
	}
	n.mu.Lock()
	n.builds++
	n.mu.Unlock()
	return syncSemanticNativePayload, nil
}

func (n *syncSemanticResumeNative) Begin(
	_ context.Context,
	expected int,
) (semanticbuild.StreamingSegmentPayloadSession, error) {
	return &syncSemanticResumeNativeSession{owner: n, expected: expected}, nil
}

func (n *syncSemanticResumeNative) VerifyRoot(
	context.Context,
	semanticrefresh.RootExpectation,
) error {
	n.mu.Lock()
	n.verifies++
	n.mu.Unlock()
	return nil
}

func (n *syncSemanticResumeNative) snapshot() (builds int, verifies int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.builds, n.verifies
}

type syncSemanticResumeNativeSession struct {
	owner    *syncSemanticResumeNative
	expected int
	added    int
}

func (s *syncSemanticResumeNativeSession) Add(
	context.Context,
	store.RetrievalEmbeddingRow,
) error {
	s.added++
	return nil
}

func (s *syncSemanticResumeNativeSession) Finish(
	context.Context,
) (func(io.Writer) error, error) {
	if s.added != s.expected || s.added != store.RetrievalSegmentTarget {
		return nil, fmt.Errorf("native session rows=%d expected=%d", s.added, s.expected)
	}
	s.owner.mu.Lock()
	s.owner.builds++
	s.owner.mu.Unlock()
	return syncSemanticNativePayload, nil
}

func (*syncSemanticResumeNativeSession) Close() error {
	return nil
}

func syncSemanticNativePayload(writer io.Writer) error {
	_, err := io.WriteString(writer, "deterministic-sync-semantic-native-payload")
	return err
}
