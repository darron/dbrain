package app

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
)

func TestSemanticLockSubprocessProjectionStageHoldsMaintenanceThroughExecute(t *testing.T) {
	fixture := newSemanticLockSubprocessFixture(t, "")

	refresh := fixture.start(t, "projection-stage-hold", "refresh", 0)
	refresh.awaitLine(t, "ATTEMPTING_MAINTENANCE_EXCLUSIVE", 2*time.Second)
	refresh.awaitLine(t, "STAGE_EXECUTING", 2*time.Second)

	source := fixture.start(t, "source-once", "source-after-refresh", 0)
	source.awaitLine(t, "ATTEMPTING_SOURCE_TX", 2*time.Second)
	source.assertNoLine(t, 100*time.Millisecond)

	refresh.release(t)
	source.awaitLine(t, "SOURCE_TX_READY", 2*time.Second)
	source.awaitLine(t, "DONE", 2*time.Second)
	source.waitSuccess(t)

	if got, want := fixture.events(t), []string{"refresh", "source-after-refresh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("acquisition events=%v want=%v", got, want)
	}
}

func TestSemanticLockSubprocessActivationWaitsForHydrationAndUsesLockOrder(t *testing.T) {
	fixture := newSemanticLockSubprocessFixture(t, "")

	query := fixture.start(t, "query-hold", "query", 0)
	query.awaitLine(t, "QUERY_HYDRATION_BLOCKED", 2*time.Second)

	activation := fixture.start(t, "activation-once", "activation", 0)
	activation.awaitLine(t, "ATTEMPTING_ACTIVATION_STAGE", 2*time.Second)
	waitForSemanticWriterIntent(t, fixture.generationPath(), 2*time.Second)
	activation.assertNoLine(t, 100*time.Millisecond)
	assertSemanticMaintenanceHeld(t, fixture)
	if fixture.activationCount(t) != 0 {
		t.Fatal("activation mutated SQLite while query hydration held generation")
	}
	if _, err := os.Stat(fixture.rootPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published root before hydration release: %v", err)
	}

	query.release(t)
	activation.awaitLine(t, "GENERATION_STAGE_EXECUTING", 2*time.Second)
	activation.awaitLine(t, "DONE", 2*time.Second)
	activation.waitSuccess(t)

	if got, want := fixture.events(t), []string{"query", "activation-executing", "activation-published"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("activation events=%v want=%v", got, want)
	}
	fixture.requireActivatedGeneration(t)
}

func TestSemanticLockSubprocessActivationHoldsGenerationThroughExecute(t *testing.T) {
	fixture := newSemanticLockSubprocessFixture(t, "")

	activation := fixture.start(t, "activation-hold", "activation", 0)
	activation.awaitLine(t, "ATTEMPTING_ACTIVATION_STAGE", 2*time.Second)
	activation.awaitLine(t, "GENERATION_STAGE_EXECUTING", 2*time.Second)

	query := fixture.start(t, "query-once", "query-after-activation", 0)
	query.awaitLine(t, "ATTEMPTING_QUERY", 2*time.Second)
	query.assertNoLine(t, 100*time.Millisecond)
	if fixture.activationCount(t) != 0 {
		t.Fatal("blocked activation mutated SQLite before its stage was released")
	}

	activation.release(t)
	query.awaitLine(t, "QUERY_HYDRATING", 2*time.Second)
	query.awaitLine(t, "DONE", 2*time.Second)
	query.waitSuccess(t)

	if got, want := fixture.events(t), []string{"activation-executing", "activation-published", "query-after-activation"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("activation/query events=%v want=%v", got, want)
	}
	fixture.requireActivatedGeneration(t)
}

func TestSemanticLockSubprocessQueuedCrashRecoversWithoutBargingBlockage(t *testing.T) {
	fixture := newSemanticLockSubprocessFixture(t, "")
	scope, err := semanticlock.NewScope(fixture.cacheDir, fixture.databaseID)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := scope.AcquireMaintenanceShared(t.Context(), "owner=test-holder\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()

	crashed := fixture.start(t, "projection-stage-hold", "crashed-refresh", 0)
	crashed.awaitLine(t, "ATTEMPTING_MAINTENANCE_EXCLUSIVE", 2*time.Second)
	waitForSemanticWriterIntent(t, fixture.maintenancePath(), 2*time.Second)
	crashed.killAndWait(t)

	recovered := fixture.start(t, "source-once", "source-after-crash", 0)
	recovered.awaitLine(t, "ATTEMPTING_SOURCE_TX", 2*time.Second)
	recovered.awaitLine(t, "SOURCE_TX_READY", 2*time.Second)
	recovered.awaitLine(t, "DONE", 2*time.Second)
	recovered.waitSuccess(t)
}

func TestSemanticLockSubprocessDeadlineIsAtomicAndRetrySucceeds(t *testing.T) {
	fixture := newSemanticLockSubprocessFixture(t, "")
	scope, err := semanticlock.NewScope(fixture.cacheDir, fixture.databaseID)
	if err != nil {
		t.Fatal(err)
	}
	queryLease, err := scope.AcquireGenerationShared(t.Context(), "owner=blocked-query\n")
	if err != nil {
		t.Fatal(err)
	}

	failed := fixture.start(t, "activation-once", "activation-timeout", 150*time.Millisecond)
	failed.awaitLine(t, "ATTEMPTING_ACTIVATION_STAGE", 2*time.Second)
	waitForSemanticWriterIntent(t, fixture.generationPath(), 2*time.Second)
	failed.waitFailure(t, context.DeadlineExceeded.Error())
	if fixture.activationCount(t) != 0 {
		t.Fatal("deadline-expired activation wrote SQLite state")
	}
	if _, err := os.Stat(fixture.rootPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deadline-expired activation published root: %v", err)
	}

	if err := queryLease.Close(); err != nil {
		t.Fatal(err)
	}
	retry := fixture.start(t, "activation-once", "activation-retry", 2*time.Second)
	retry.awaitLine(t, "ATTEMPTING_ACTIVATION_STAGE", 2*time.Second)
	retry.awaitLine(t, "GENERATION_STAGE_EXECUTING", 2*time.Second)
	retry.awaitLine(t, "DONE", 2*time.Second)
	retry.waitSuccess(t)
	fixture.requireActivatedGeneration(t)
}

func TestSemanticLockSubprocessDifferentDatabaseIDsDoNotBlock(t *testing.T) {
	cacheDir := t.TempDir()
	first := newSemanticLockSubprocessFixture(t, cacheDir)
	second := newSemanticLockSubprocessFixture(t, cacheDir)
	if first.databaseID == second.databaseID {
		t.Fatalf("isolated stores reused database ID %q", first.databaseID)
	}

	blocker := first.start(t, "projection-stage-hold", "database-a", 0)
	blocker.awaitLine(t, "ATTEMPTING_MAINTENANCE_EXCLUSIVE", 2*time.Second)
	blocker.awaitLine(t, "STAGE_EXECUTING", 2*time.Second)

	independent := second.start(t, "activation-once", "database-b", 2*time.Second)
	independent.awaitLine(t, "ATTEMPTING_ACTIVATION_STAGE", 2*time.Second)
	independent.awaitLine(t, "GENERATION_STAGE_EXECUTING", 2*time.Second)
	independent.awaitLine(t, "DONE", 2*time.Second)
	independent.waitSuccess(t)
	second.requireActivatedGeneration(t)

	blocker.release(t)
}

func TestSemanticLockHelperProcess(t *testing.T) {
	if !semanticLockSubprocessRequested() {
		return
	}
	if err := runSemanticLockSubprocess(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

const (
	semanticLockHelperEnv        = "DBRAIN_SEMANTIC_LOCK_HELPER"
	semanticLockHelperOperation  = "DBRAIN_SEMANTIC_LOCK_OPERATION"
	semanticLockHelperDBPath     = "DBRAIN_SEMANTIC_LOCK_DB_PATH"
	semanticLockHelperCacheDir   = "DBRAIN_SEMANTIC_LOCK_CACHE_DIR"
	semanticLockHelperDatabaseID = "DBRAIN_SEMANTIC_LOCK_DATABASE_ID"
	semanticLockHelperEventPath  = "DBRAIN_SEMANTIC_LOCK_EVENT_PATH"
	semanticLockHelperLabel      = "DBRAIN_SEMANTIC_LOCK_LABEL"
	semanticLockHelperTimeoutMS  = "DBRAIN_SEMANTIC_LOCK_TIMEOUT_MS"
	semanticLockHelperProfileID  = "DBRAIN_SEMANTIC_LOCK_PROFILE_ID"
	semanticLockHelperGeneration = "DBRAIN_SEMANTIC_LOCK_GENERATION_ID"
)

type semanticLockSubprocessFixture struct {
	dbPath       string
	cacheDir     string
	databaseID   string
	eventPath    string
	profileID    string
	generationID string
}

func newSemanticLockSubprocessFixture(t *testing.T, cacheDir string) semanticLockSubprocessFixture {
	t.Helper()
	if cacheDir == "" {
		cacheDir = t.TempDir()
	}
	dbPath := filepath.Join(t.TempDir(), "brain.db")
	storefixture.PrepareCurrent(t, dbPath)
	st, err := store.OpenWithSemanticCache(dbPath, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	databaseID, idErr := st.RetrievalDatabaseID(t.Context())
	closeErr := st.Close()
	if err := errors.Join(idErr, closeErr); err != nil {
		t.Fatal(err)
	}
	db := openSemanticLockTestDB(t, dbPath)
	for _, statement := range []string{
		`CREATE TABLE semantic_lock_test_transactions (label TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE semantic_lock_test_hydration (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`,
		`INSERT INTO semantic_lock_test_hydration (id, body) VALUES (1, 'hydrated evidence')`,
		`CREATE TABLE semantic_lock_test_activation (generation_id TEXT NOT NULL UNIQUE)`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			_ = db.Close()
			t.Fatalf("prepare semantic subprocess fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return semanticLockSubprocessFixture{
		dbPath:       dbPath,
		cacheDir:     cacheDir,
		databaseID:   databaseID,
		eventPath:    filepath.Join(t.TempDir(), "events.log"),
		profileID:    "task6-profile",
		generationID: "task6-generation",
	}
}

func openSemanticLockTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db
}

func (f semanticLockSubprocessFixture) start(t *testing.T, operation, label string, timeout time.Duration) *semanticLockHelperProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSemanticLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		semanticLockHelperEnv+"=1",
		semanticLockHelperOperation+"="+operation,
		semanticLockHelperDBPath+"="+f.dbPath,
		semanticLockHelperCacheDir+"="+f.cacheDir,
		semanticLockHelperDatabaseID+"="+f.databaseID,
		semanticLockHelperEventPath+"="+f.eventPath,
		semanticLockHelperLabel+"="+label,
		semanticLockHelperTimeoutMS+"="+strconv.FormatInt(timeout.Milliseconds(), 10),
		semanticLockHelperProfileID+"="+f.profileID,
		semanticLockHelperGeneration+"="+f.generationID,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &semanticLockHelperProcess{
		cmd:     cmd,
		stdin:   stdin,
		lines:   make(chan string, 8),
		scanErr: make(chan error, 1),
		stderr:  &stderr,
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			process.lines <- scanner.Text()
		}
		process.scanErr <- scanner.Err()
		close(process.lines)
	}()
	t.Cleanup(process.stop)
	return process
}

func (f semanticLockSubprocessFixture) maintenancePath() string {
	return filepath.Join(f.cacheDir, "semantic", f.databaseID, "locks", "maintenance.lock")
}

func (f semanticLockSubprocessFixture) generationPath() string {
	return filepath.Join(f.cacheDir, "semantic", f.databaseID, "locks", "generation.lock")
}

func (f semanticLockSubprocessFixture) rootPath() string {
	return filepath.Join(
		f.cacheDir,
		"semantic",
		f.databaseID,
		f.profileID,
		"generations",
		f.generationID,
		semanticsegment.RootFileName,
	)
}

func (f semanticLockSubprocessFixture) events(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(f.eventPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(raw))
}

func (f semanticLockSubprocessFixture) activationCount(t *testing.T) int {
	t.Helper()
	db := openSemanticLockTestDB(t, f.dbPath)
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM semantic_lock_test_activation`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f semanticLockSubprocessFixture) requireActivatedGeneration(t *testing.T) {
	t.Helper()
	if count := f.activationCount(t); count != 1 {
		t.Fatalf("activated generation rows=%d want=1", count)
	}
	root, err := semanticsegment.OpenRoot(f.cacheDir, f.databaseID, f.profileID, f.generationID)
	if err != nil {
		t.Fatalf("open published semantic root: %v", err)
	}
	if root.Manifest.GenerationID != f.generationID {
		t.Fatalf("published root generation=%q want=%q", root.Manifest.GenerationID, f.generationID)
	}
}

type semanticLockHelperProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan string
	scanErr  chan error
	stderr   *bytes.Buffer
	waitOnce sync.Once
	waitErr  error
}

func (p *semanticLockHelperProcess) awaitLine(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	select {
	case got, ok := <-p.lines:
		if !ok {
			err := p.wait()
			t.Fatalf("helper exited before %q: err=%v stderr=%s", want, err, p.stderr.String())
		}
		if got != want {
			t.Fatalf("helper line=%q want=%q", got, want)
		}
	case <-time.After(timeout):
		t.Fatalf("helper did not report %q within %s", want, timeout)
	}
}

func (p *semanticLockHelperProcess) assertNoLine(t *testing.T, duration time.Duration) {
	t.Helper()
	select {
	case got, ok := <-p.lines:
		if !ok {
			err := p.wait()
			t.Fatalf("blocked helper exited early: err=%v stderr=%s", err, p.stderr.String())
		}
		t.Fatalf("blocked helper unexpectedly reported %q", got)
	case <-time.After(duration):
	}
}

func (p *semanticLockHelperProcess) release(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, "release\n"); err != nil {
		t.Fatal(err)
	}
	p.awaitLine(t, "DONE", 2*time.Second)
	p.waitSuccess(t)
}

func (p *semanticLockHelperProcess) waitSuccess(t *testing.T) {
	t.Helper()
	if err := p.wait(); err != nil {
		t.Fatalf("helper failed: %v stderr=%s", err, p.stderr.String())
	}
}

func (p *semanticLockHelperProcess) waitFailure(t *testing.T, wantCause string) {
	t.Helper()
	if err := p.wait(); err == nil {
		t.Fatal("helper unexpectedly succeeded")
	}
	if !strings.Contains(p.stderr.String(), wantCause) {
		t.Fatalf("helper failure stderr=%q want cause %q", p.stderr.String(), wantCause)
	}
}

func (p *semanticLockHelperProcess) killAndWait(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	if err := p.wait(); err == nil {
		t.Fatal("killed helper exited successfully")
	}
}

func (p *semanticLockHelperProcess) wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
	})
	return p.waitErr
}

func (p *semanticLockHelperProcess) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if p.cmd.ProcessState == nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.wait()
}

func semanticLockSubprocessRequested() bool {
	return os.Getenv(semanticLockHelperEnv) == "1"
}

func runSemanticLockSubprocess(parent context.Context) error {
	timeout, err := strconv.ParseInt(os.Getenv(semanticLockHelperTimeoutMS), 10, 64)
	if err != nil {
		return fmt.Errorf("parse helper timeout: %w", err)
	}
	ctx := parent
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	}
	defer cancel()
	scope, err := semanticlock.NewScope(
		os.Getenv(semanticLockHelperCacheDir),
		os.Getenv(semanticLockHelperDatabaseID),
	)
	if err != nil {
		return err
	}
	switch os.Getenv(semanticLockHelperOperation) {
	case "source-hold":
		return runSemanticSourceTransaction(ctx, scope, true)
	case "source-once":
		return runSemanticSourceTransaction(ctx, scope, false)
	case "projection-stage-hold":
		return runSemanticProjectionStageHolder(ctx, scope)
	case "query-hold":
		return runSemanticQueryHydration(ctx, scope, true)
	case "query-once":
		return runSemanticQueryHydration(ctx, scope, false)
	case "activation-once":
		return runSemanticActivation(ctx, scope, false)
	case "activation-hold":
		return runSemanticActivation(ctx, scope, true)
	default:
		return fmt.Errorf("unknown semantic lock helper operation %q", os.Getenv(semanticLockHelperOperation))
	}
}

func runSemanticSourceTransaction(ctx context.Context, scope *semanticlock.Scope, hold bool) (resultErr error) {
	fmt.Println("ATTEMPTING_SOURCE_TX")
	lease, err := scope.AcquireMaintenanceShared(ctx, "owner=subprocess-source\n")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
	db, err := sql.Open("sqlite", os.Getenv(semanticLockHelperDBPath))
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	label := os.Getenv(semanticLockHelperLabel)
	if _, err := tx.ExecContext(ctx, `INSERT INTO semantic_lock_test_transactions (label) VALUES (?)`, label); err != nil {
		return err
	}
	if err := appendSemanticLockEvent(label); err != nil {
		return err
	}
	fmt.Println("SOURCE_TX_READY")
	if hold {
		if err := awaitSemanticLockRelease(); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := lease.Close(); err != nil {
		return err
	}
	fmt.Println("DONE")
	return nil
}

func runSemanticProjectionStageHolder(ctx context.Context, scope *semanticlock.Scope) error {
	fmt.Println("ATTEMPTING_MAINTENANCE_EXCLUSIVE")
	executor, err := semanticrefresh.NewLockedPipeline(
		semanticLockStageExecutorFunc(func(context.Context, store.SemanticRefreshRun, semanticrefresh.StageProgressCallback) (semanticrefresh.StageOutcome, error) {
			if err := appendSemanticLockEvent(os.Getenv(semanticLockHelperLabel)); err != nil {
				return semanticrefresh.StageOutcome{}, err
			}
			fmt.Println("STAGE_EXECUTING")
			if err := awaitSemanticLockRelease(); err != nil {
				return semanticrefresh.StageOutcome{}, err
			}
			return semanticrefresh.StageOutcome{}, nil
		}),
		scope,
	)
	if err != nil {
		return err
	}
	if _, err := executor.Execute(ctx, store.SemanticRefreshRun{
		RunID: "subprocess-projection", Stage: store.SemanticRefreshProjection,
	}, nil); err != nil {
		return err
	}
	fmt.Println("DONE")
	return nil
}

func runSemanticQueryHydration(ctx context.Context, scope *semanticlock.Scope, hold bool) error {
	db, err := sql.Open("sqlite", os.Getenv(semanticLockHelperDBPath))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	profile := semanticLockEmbeddingProfile()
	profileID, err := profile.ID()
	if err != nil {
		return err
	}
	retriever := researchsemantic.NewWithGenerationLease(
		semanticLockEmbeddingProvider{},
		semanticLockSearcher{profileID: profileID},
		semanticLockHydrator{db: db, hold: hold},
		func(ctx context.Context) (researchsemantic.GenerationLease, error) {
			return scope.AcquireGenerationShared(ctx, "owner=subprocess-query\n")
		},
	)
	if !hold {
		fmt.Println("ATTEMPTING_QUERY")
	}
	docs, status, err := retriever.Retrieve(ctx, "semantic lock query", researchsemantic.Options{
		Profile: profile,
		Limit:   1,
	})
	if err != nil {
		return err
	}
	if status.State != semanticindex.StateSearched || len(docs) != 1 || docs[0].Excerpt != "hydrated evidence" {
		return fmt.Errorf("unexpected retrieval result: status=%+v docs=%+v", status, docs)
	}
	if err := retriever.Close(); err != nil {
		return err
	}
	fmt.Println("DONE")
	return nil
}

func runSemanticActivation(ctx context.Context, scope *semanticlock.Scope, hold bool) error {
	executor, err := semanticrefresh.NewLockedPipeline(
		semanticLockStageExecutorFunc(func(
			ctx context.Context,
			run store.SemanticRefreshRun,
			_ semanticrefresh.StageProgressCallback,
		) (semanticrefresh.StageOutcome, error) {
			return runSemanticActivationStage(ctx, run, hold)
		}),
		scope,
	)
	if err != nil {
		return err
	}
	fmt.Println("ATTEMPTING_ACTIVATION_STAGE")
	if _, err := executor.Execute(ctx, store.SemanticRefreshRun{
		RunID: "subprocess-activation", Stage: store.SemanticRefreshFlush,
	}, nil); err != nil {
		return err
	}
	fmt.Println("DONE")
	return nil
}

func runSemanticActivationStage(
	ctx context.Context,
	_ store.SemanticRefreshRun,
	hold bool,
) (semanticrefresh.StageOutcome, error) {
	if err := appendSemanticLockEvent("activation-executing"); err != nil {
		return semanticrefresh.StageOutcome{}, err
	}
	fmt.Println("GENERATION_STAGE_EXECUTING")
	if hold {
		if err := awaitSemanticLockRelease(); err != nil {
			return semanticrefresh.StageOutcome{}, err
		}
	}
	databaseID := os.Getenv(semanticLockHelperDatabaseID)
	profileID := os.Getenv(semanticLockHelperProfileID)
	generationID := os.Getenv(semanticLockHelperGeneration)
	cacheDir := os.Getenv(semanticLockHelperCacheDir)
	segment, err := semanticsegment.PublishSegment(cacheDir, semanticsegment.SegmentInput{
		DatabaseID: databaseID, ProfileID: profileID, Backend: "test",
		BackendVersion: "1", DistanceMetric: "cosine", Dimensions: 2,
		Members: []semanticsegment.Member{{
			Ordinal: 0, ChunkID: "chunk-1", Revision: 1, VectorHash: "vector-hash",
		}},
		Payload: func(writer io.Writer) error {
			_, err := io.WriteString(writer, "opaque semantic test payload")
			return err
		},
	})
	if err != nil {
		return semanticrefresh.StageOutcome{}, err
	}
	if _, err := semanticsegment.PublishRoot(cacheDir, semanticsegment.RootInput{
		DatabaseID: databaseID, ProfileID: profileID, GenerationID: generationID,
		SnapshotRevision: 1, PurgeEpoch: 0,
		Segments: []semanticsegment.RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}},
	}); err != nil {
		return semanticrefresh.StageOutcome{}, err
	}
	db, err := sql.Open("sqlite", os.Getenv(semanticLockHelperDBPath))
	if err != nil {
		return semanticrefresh.StageOutcome{}, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO semantic_lock_test_activation (generation_id) VALUES (?)`, generationID); err != nil {
		_ = db.Close()
		return semanticrefresh.StageOutcome{}, err
	}
	if err := db.Close(); err != nil {
		return semanticrefresh.StageOutcome{}, err
	}
	if err := appendSemanticLockEvent("activation-published"); err != nil {
		return semanticrefresh.StageOutcome{}, err
	}
	return semanticrefresh.StageOutcome{CurrentGenerationID: generationID}, nil
}

type semanticLockStageExecutorFunc func(context.Context, store.SemanticRefreshRun, semanticrefresh.StageProgressCallback) (semanticrefresh.StageOutcome, error)

func (f semanticLockStageExecutorFunc) Execute(
	ctx context.Context,
	run store.SemanticRefreshRun,
	progress semanticrefresh.StageProgressCallback,
) (semanticrefresh.StageOutcome, error) {
	return f(ctx, run, progress)
}

type semanticLockEmbeddingProvider struct{}

func (semanticLockEmbeddingProvider) Info() embedding.Info {
	return embedding.Info{Provider: "task6", Model: "lock-test", Dimensions: 2}
}

func (p semanticLockEmbeddingProvider) Embed(_ context.Context, request embedding.Request) (embedding.Response, error) {
	return embedding.Response{
		Vectors:    [][]float32{{0.6, 0.8}},
		Provider:   p.Info().Provider,
		Model:      p.Info().Model,
		Dimensions: p.Info().Dimensions,
	}, nil
}

func semanticLockEmbeddingProfile() embedding.Profile {
	info := semanticLockEmbeddingProvider{}.Info()
	return embedding.Profile{
		Provider:          info.Provider,
		Model:             info.Model,
		ProjectionVersion: "task6-projection",
		ChunkerVersion:    "task6-chunker",
		Representation:    embedding.RepresentationDenseF32,
		Normalization:     embedding.NormalizationL2,
		Dimensions:        info.Dimensions,
	}
}

type semanticLockSearcher struct {
	profileID string
}

func (s semanticLockSearcher) Search(
	context.Context,
	[]float32,
	semanticindex.SearchOptions,
) ([]semanticindex.Hit, semanticindex.Status, error) {
	return []semanticindex.Hit{{
			ChunkID:  "chunk-1",
			Rank:     1,
			Distance: 0.1,
		}}, semanticindex.Status{
			State:        semanticindex.StateSearched,
			Backend:      semanticindex.BackendExact,
			ProfileID:    s.profileID,
			GenerationID: os.Getenv(semanticLockHelperGeneration),
			Scanned:      1,
		}, nil
}

type semanticLockHydrator struct {
	db   *sql.DB
	hold bool
}

func (h semanticLockHydrator) HydrateRetrievalChunks(
	ctx context.Context,
	chunkIDs []string,
) ([]store.RetrievalChunkEvidenceRow, error) {
	if len(chunkIDs) != 1 || chunkIDs[0] != "chunk-1" {
		return nil, fmt.Errorf("unexpected hydration chunk IDs %v", chunkIDs)
	}
	if err := appendSemanticLockEvent(os.Getenv(semanticLockHelperLabel)); err != nil {
		return nil, err
	}
	if h.hold {
		fmt.Println("QUERY_HYDRATION_BLOCKED")
		if err := awaitSemanticLockRelease(); err != nil {
			return nil, err
		}
	} else {
		fmt.Println("QUERY_HYDRATING")
	}
	var evidence string
	if err := h.db.QueryRowContext(
		ctx,
		`SELECT body FROM semantic_lock_test_hydration WHERE id=1`,
	).Scan(&evidence); err != nil {
		return nil, err
	}
	return []store.RetrievalChunkEvidenceRow{{
		ChunkID:         "chunk-1",
		ParentKind:      "source",
		ParentSourceKey: "source:task6",
		EvidenceRole:    "body",
		ChunkTextHash:   "chunk-hash",
		Text:            evidence,
		Title:           "Task 6 evidence",
		SourceType:      "test",
	}}, nil
}

func appendSemanticLockEvent(event string) error {
	file, err := os.OpenFile(os.Getenv(semanticLockHelperEventPath), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, event+"\n")
	return errors.Join(writeErr, file.Close())
}

func awaitSemanticLockRelease() error {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return errors.Join(io.EOF, scanner.Err())
	}
	if scanner.Text() != "release" {
		return fmt.Errorf("unexpected helper command %q", scanner.Text())
	}
	return nil
}

func waitForSemanticWriterIntent(t *testing.T, lockPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	pattern := lockPath + ".writer-*.intent"
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("writer intent did not appear at %s", pattern)
}

func assertSemanticMaintenanceHeld(t *testing.T, fixture semanticLockSubprocessFixture) {
	t.Helper()
	scope, err := semanticlock.NewScope(fixture.cacheDir, fixture.databaseID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	lease, err := scope.AcquireMaintenanceShared(ctx, "owner=lock-order-probe\n")
	if lease != nil {
		_ = lease.Close()
		t.Fatal("activation did not hold maintenance while waiting for generation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("maintenance probe error=%v want deadline exceeded", err)
	}
}
