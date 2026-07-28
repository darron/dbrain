package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

func TestSemanticRefreshCommandIsRegisteredWithBoundedFlags(t *testing.T) {
	root := &rootOptions{}
	cmd := newSemanticCommandWithDeps(root, defaultSemanticDeps())
	target, _, err := cmd.Find([]string{"refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if target.Name() != "refresh" {
		t.Fatalf("semantic subcommand=%q want refresh", target.Name())
	}
	if target.Flags().Lookup("max-duration") == nil || target.Flags().Lookup("json") == nil {
		t.Fatalf("refresh flags=%s", target.Flags().FlagUsages())
	}
	if target.Flags().Lookup("until-idle") != nil {
		t.Fatalf("refresh unexpectedly exposes --until-idle: %s", target.Flags().FlagUsages())
	}
}

func TestSemanticRefreshCommandRejectsArgsAndNegativeDurationBeforeAdmission(t *testing.T) {
	for _, args := range [][]string{
		{"extra"},
		{"--max-duration=-1ns"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			called := false
			cmd := newSemanticRefreshCommand(&rootOptions{root: t.TempDir()}, semanticRefreshDeps{
				resolve: func(string) (semanticconfig.Config, error) {
					called = true
					return semanticconfig.Config{}, nil
				},
			})
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(args)
			if err := cmd.ExecuteContext(t.Context()); err == nil {
				t.Fatalf("semantic refresh %v unexpectedly succeeded", args)
			}
			if called {
				t.Fatalf("semantic refresh %v reached admission", args)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("semantic refresh %v output stdout=%q stderr=%q", args, stdout.String(), stderr.String())
			}
		})
	}
}

func TestSemanticRefreshCommandZeroDurationIsUnlimited(t *testing.T) {
	var deadlineSet bool
	deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOn)
	deps.runRefresh = func(
		ctx context.Context,
		_ semanticrefresh.RunLedger,
		_ semanticrefresh.StageExecutor,
		request semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		_, deadlineSet = ctx.Deadline()
		return completedSemanticRefreshResult(request.ProfileID), nil
	}
	stdout, stderr, err := executeSemanticRefreshCommand(
		t,
		t.Context(),
		deps,
		"--max-duration=0",
	)
	if err != nil {
		t.Fatal(err)
	}
	if deadlineSet {
		t.Fatal("--max-duration=0 installed a deadline")
	}
	if !strings.Contains(stdout, "Semantic refresh: completed") {
		t.Fatalf("stdout=%q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr=%q want empty", stderr)
	}
}

func TestSemanticRefreshCommandUnsupportedSkipsAndBrokenFails(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOff)
		stdout, stderr, err := executeSemanticRefreshCommand(t, t.Context(), deps)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "Semantic refresh: skipped reason=semantic_mode_off") {
			t.Fatalf("stdout=%q", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr=%q want empty", stderr)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOn)
		deps.capability = func() semanticindex.Capability {
			return semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
		}
		stdout, stderr, err := executeSemanticRefreshCommand(t, t.Context(), deps)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "Semantic refresh: skipped reason=native_backend_unsupported") {
			t.Fatalf("stdout=%q", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr=%q want empty", stderr)
		}
	})

	t.Run("broken", func(t *testing.T) {
		deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOn)
		deps.capability = func() semanticindex.Capability {
			return semanticindex.Capability{
				State:   semanticindex.CapabilitySupportedBroken,
				Backend: semanticindex.BackendUSearch,
				Version: semanticindex.USearchVersion,
				Reason:  "load /Users/alice/private/libusearch.dylib failed",
			}
		}
		stdout, stderr, err := executeSemanticRefreshCommand(t, t.Context(), deps)
		var exitErr *ExitError
		var refreshErr *semanticrefresh.RefreshError
		if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Silent {
			t.Fatalf("error=%#v want non-silent exit code 1", err)
		}
		if !errors.As(err, &refreshErr) || refreshErr.Code != semanticrefresh.ErrorBackendBroken {
			t.Fatalf("error=%#v want backend-broken RefreshError", err)
		}
		for _, want := range []string{"code=semantic_backend_broken", "run=", "stage=", "checkpoint=", "readiness=", "dirty_parents=", "l0="} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%q missing %q", err, want)
			}
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout=%q stderr=%q want command output deferred to process error handling", stdout, stderr)
		}
		if strings.Contains(err.Error(), "/Users/alice") || strings.Contains(err.Error(), "libusearch") {
			t.Fatalf("human error leaked backend detail: %q", err)
		}
	})
}

func TestSemanticRefreshCommandCompletionAndProgressOutput(t *testing.T) {
	deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOn)
	deps.runRefresh = func(
		_ context.Context,
		_ semanticrefresh.RunLedger,
		_ semanticrefresh.StageExecutor,
		request semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		progress := semanticrefresh.Progress{
			RunID:      "run-progress",
			ProfileID:  request.ProfileID,
			Stage:      store.SemanticRefreshEmbedding,
			Checkpoint: "embedding:revision=8",
			Counters: store.SemanticRefreshCounters{
				ProjectedParents: 2,
				EmbeddedChunks:   3,
			},
			Debt: semanticrefresh.Debt{PendingEmbeddings: 4, L0Ready: 5},
			At:   time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC),
		}
		if err := request.Progress(progress); err != nil {
			return semanticrefresh.Result{}, err
		}
		return completedSemanticRefreshResult(request.ProfileID), nil
	}

	t.Run("human", func(t *testing.T) {
		stdout, stderr, err := executeSemanticRefreshCommand(t, t.Context(), deps)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"Semantic refresh: completed",
			"capability=supported_ready",
			"run=run-complete",
			"profile=",
			"generation=generation-complete",
			"readiness=ready",
			"elapsed=",
			"projected_parents=2",
			"embedded_chunks=3",
			"flushed_vectors=5000",
			"compacted_vectors=4000",
			"verified_vectors=7000",
			"successor_runs=1",
			"indexed=7000",
			"l0=6",
			"tombstones=7",
			"segments=8",
		} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("stdout=%q missing %q", stdout, want)
			}
		}
		if !strings.Contains(stderr, "Semantic refresh progress:") ||
			!strings.Contains(stderr, "run=run-progress") ||
			!strings.Contains(stderr, "pending_embeddings=4") {
			t.Fatalf("stderr=%q", stderr)
		}
	})

	t.Run("json stdout is one document and progress stays on stderr", func(t *testing.T) {
		stdout, stderr, err := executeSemanticRefreshCommand(t, t.Context(), deps, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var result semanticrefresh.Result
		decodeOneJSONDocument(t, stdout, &result)
		if result.Outcome != semanticrefresh.OutcomeCompleted ||
			result.Run == nil ||
			result.Run.RunID != "run-complete" {
			t.Fatalf("result=%+v output=%s", result, stdout)
		}
		if strings.Contains(stdout, "Semantic refresh progress:") {
			t.Fatalf("stdout mixed progress with JSON: %q", stdout)
		}
		if !strings.Contains(stderr, "Semantic refresh progress:") {
			t.Fatalf("stderr=%q missing progress", stderr)
		}
	})
}

func TestSemanticRefreshCommandJSONErrorIsBoundedSilentAndSingleDocument(t *testing.T) {
	deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOn)
	deps.runRefresh = func(
		_ context.Context,
		_ semanticrefresh.RunLedger,
		_ semanticrefresh.StageExecutor,
		request semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		run := store.SemanticRefreshRun{
			RunID:          "run-failed",
			ProfileID:      request.ProfileID,
			Stage:          store.SemanticRefreshEmbedding,
			Checkpoint:     "embedding:revision=9",
			ReadinessState: "needs_embeddings",
		}
		debt := semanticrefresh.Debt{PendingEmbeddings: 9, DueRetries: 2, L0Ready: 10}
		result := semanticrefresh.Result{
			Capability: request.Capability,
			Run:        &run,
			Debt:       debt,
		}
		return result, semanticrefresh.NewError(
			semanticrefresh.ErrorEmbedding,
			run,
			run.ReadinessState,
			debt,
			errors.New(`provider body {"vectors":[0.1],"text":"secret"} /Users/alice/private/cache`),
		)
	}
	stdout, stderr, err := executeSemanticRefreshCommand(t, t.Context(), deps, "--json")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("error=%#v want silent exit code 1", err)
	}
	var payload semanticrefresh.RefreshError
	decodeOneJSONDocument(t, stdout, &payload)
	if payload.Code != semanticrefresh.ErrorEmbedding ||
		payload.RunID != "run-failed" ||
		payload.Stage != store.SemanticRefreshEmbedding ||
		payload.Checkpoint != "embedding:revision=9" ||
		payload.Readiness != "needs_embeddings" ||
		payload.Debt.PendingEmbeddings != 9 {
		t.Fatalf("payload=%+v output=%s", payload, stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr=%q want no duplicate JSON error", stderr)
	}
	for _, forbidden := range []string{"vectors", "secret", "/Users/alice", "provider body"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("JSON error leaked %q: %s", forbidden, stdout)
		}
	}
	if len(stdout) > 2048 {
		t.Fatalf("JSON error length=%d want bounded document", len(stdout))
	}
}

func TestSemanticRefreshCommandCancellationLeavesLatestRunCancelled(t *testing.T) {
	testSemanticRefreshCommandInterruption(t, "1h", true)
}

func TestSemanticRefreshCommandTimeoutLeavesLatestRunCancelled(t *testing.T) {
	testSemanticRefreshCommandInterruption(t, "2s", false)
}

func testSemanticRefreshCommandInterruption(
	t *testing.T,
	maxDuration string,
	cancelAfterEnter bool,
) {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	deps := semanticRefreshCommandDeps(t, semanticconfig.ModeOn)
	deps.runRefresh = func(
		ctx context.Context,
		ledger semanticrefresh.RunLedger,
		_ semanticrefresh.StageExecutor,
		request semanticrefresh.Request,
	) (semanticrefresh.Result, error) {
		return semanticrefresh.Run(ctx, ledger, semanticRefreshBlockingExecutor{entered: entered}, request)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type commandResult struct {
		stdout, stderr string
		err            error
	}
	finished := make(chan commandResult, 1)
	go func() {
		cmd := newSemanticRefreshCommand(&rootOptions{root: root}, deps)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"--max-duration=" + maxDuration, "--json"})
		err := cmd.ExecuteContext(ctx)
		finished <- commandResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
			err:    err,
		}
	}()
	select {
	case <-entered:
		if cancelAfterEnter {
			cancel()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh runner did not enter a stage")
	}
	var got commandResult
	select {
	case got = <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted refresh command did not return")
	}
	var exitErr *ExitError
	var refreshErr *semanticrefresh.RefreshError
	if !errors.As(got.err, &exitErr) || !exitErr.Silent || exitErr.Code != 1 {
		t.Fatalf("error=%#v want silent JSON exit code 1", got.err)
	}
	if !errors.As(got.err, &refreshErr) || refreshErr.Code != semanticrefresh.ErrorCancelled {
		t.Fatalf("error=%#v want cancellation RefreshError", got.err)
	}
	var payload semanticrefresh.RefreshError
	decodeOneJSONDocument(t, got.stdout, &payload)
	if payload.Code != semanticrefresh.ErrorCancelled {
		t.Fatalf("payload=%+v output=%s", payload, got.stdout)
	}

	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	profileID, err := semanticbuild.Profile(semanticRefreshTestInfo()).ID()
	if err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestSemanticRefreshRun(t.Context(), profileID)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.State != store.SemanticRefreshRunCancelled {
		t.Fatalf("latest=%+v want cancelled", latest)
	}
}

func TestSemanticStatusCommandUnconfiguredShowsDatabaseLatestRun(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	started, _, err := st.StartOrResumeSemanticRefreshRun(t.Context(), store.StartSemanticRefreshRunInput{
		RunID:               "run-earlier-profile",
		ProfileID:           "embedding-profile-v1:" + strings.Repeat("a", 64),
		PurgeEpoch:          1,
		ProjectionWatermark: 2,
		Now:                 time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	stdout := runRootCommand(t, root, "semantic", "status", "--json")
	var status semanticbuild.Status
	decodeOneJSONDocument(t, stdout, &status)
	if status.LatestRun == nil ||
		status.LatestRun.RunID != started.RunID ||
		status.LatestRun.ProfileID != started.ProfileID {
		t.Fatalf("latest run=%+v want database latest %+v; output=%s", status.LatestRun, started, stdout)
	}
}

func TestSemanticStatusCommandUnconfiguredIgnoresPreRefreshSchema(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Clean(cfg.DBPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, source_key TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runRootCommandErr(t, root, "semantic", "status", "--json")
	if err != nil {
		t.Fatalf("semantic status: %v stderr=%q", err, stderr)
	}
	var status semanticbuild.Status
	decodeOneJSONDocument(t, stdout, &status)
	if status.Status != "not_configured" || status.LatestRun != nil {
		t.Fatalf("status=%+v output=%s", status, stdout)
	}
}

func TestSemanticStatusHumanOutputIncludesAtMostTwoRefreshLines(t *testing.T) {
	status := semanticbuild.Status{
		LatestRun: &store.SemanticRefreshRun{
			RunID:               "run-status",
			State:               store.SemanticRefreshRunFailed,
			Stage:               store.SemanticRefreshEmbedding,
			ProjectionWatermark: 21,
			EmbeddingRevision:   34,
			LastProgressAt:      time.Date(2026, 7, 28, 18, 2, 3, 0, time.UTC),
			ReadinessState:      "needs_embeddings",
			ErrorCode:           semanticrefresh.ErrorEmbedding,
			ErrorText:           "provider body that must not be printed",
			Checkpoint:          "embedding:revision=34",
		},
	}
	var output bytes.Buffer
	if err := writeSemanticStatus(&output, status); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "Refresh:") != 1 ||
		strings.Count(output.String(), "Refresh error:") != 1 {
		t.Fatalf("output=%q want exactly two refresh lines", output.String())
	}
	for _, want := range []string{
		"run=run-status",
		"state=failed",
		"stage=embedding",
		"watermark=21",
		"embedding_revision=34",
		"last_progress=2026-07-28T18:02:03Z",
		"readiness=needs_embeddings",
		"code=semantic_embedding_failed",
		"checkpoint=embedding:revision=34",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output=%q missing %q", output.String(), want)
		}
	}
	if strings.Contains(output.String(), "provider body") {
		t.Fatalf("status duplicated stored error text: %q", output.String())
	}
}

type semanticRefreshBlockingExecutor struct {
	entered chan<- struct{}
}

func (e semanticRefreshBlockingExecutor) Execute(
	ctx context.Context,
	_ store.SemanticRefreshRun,
) (semanticrefresh.StageOutcome, error) {
	select {
	case e.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return semanticrefresh.StageOutcome{}, ctx.Err()
}

func semanticRefreshCommandDeps(t *testing.T, mode semanticconfig.Mode) semanticRefreshDeps {
	t.Helper()
	return semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(mode), nil
		},
		capability: semanticRefreshReadyCapability,
		openWritable: func(path string) (*store.Store, error) {
			return store.Open(path)
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return &semanticRefreshTestProvider{info: semanticRefreshTestInfo()}, nil
		},
		nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
			return &semanticRefreshTestNative{}, nil
		},
		runRefresh: func(
			_ context.Context,
			_ semanticrefresh.RunLedger,
			_ semanticrefresh.StageExecutor,
			request semanticrefresh.Request,
		) (semanticrefresh.Result, error) {
			return completedSemanticRefreshResult(request.ProfileID), nil
		},
	}
}

func completedSemanticRefreshResult(profileID string) semanticrefresh.Result {
	return semanticrefresh.Result{
		Outcome: semanticrefresh.OutcomeCompleted,
		Capability: semanticindex.Capability{
			State:   semanticindex.CapabilitySupportedReady,
			Backend: semanticindex.BackendUSearch,
			Version: semanticindex.USearchVersion,
		},
		Run: &store.SemanticRefreshRun{
			RunID:               "run-complete",
			ProfileID:           profileID,
			CurrentGenerationID: "generation-complete",
			State:               store.SemanticRefreshRunCompleted,
			Stage:               store.SemanticRefreshReadiness,
			ReadinessState:      "ready",
			Counters: store.SemanticRefreshCounters{
				ProjectedParents: 2,
				EmbeddedChunks:   3,
				FlushedVectors:   5000,
				CompactedVectors: 4000,
				VerifiedVectors:  7000,
				SuccessorRuns:    1,
			},
		},
		Debt: semanticrefresh.Debt{
			DirtyParents:      1,
			PendingEmbeddings: 2,
			DueRetries:        3,
			ScheduledRetries:  4,
			BlockedEmbeddings: 5,
			FailedEmbeddings:  6,
			L0Ready:           6,
			Tombstones:        7,
			Segments:          8,
		},
	}
}

func executeSemanticRefreshCommand(
	t *testing.T,
	ctx context.Context,
	deps semanticRefreshDeps,
	args ...string,
) (string, string, error) {
	t.Helper()
	cmd := newSemanticRefreshCommand(&rootOptions{root: t.TempDir()}, deps)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func decodeOneJSONDocument(t *testing.T, value string, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode JSON %q: %v", value, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON has trailing document/data %q: %v", value, err)
	}
}
