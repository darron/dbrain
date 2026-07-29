package store

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticlock"
)

func TestAuthoritativeWriteLockSubprocessRefreshFIFOThroughCommit(t *testing.T) {
	fixture := newAuthoritativeWriteSubprocessFixture(t)

	firstSource := fixture.start(t, "source-hold", "source-first")
	firstSource.awaitLine(t, "SOURCE_PROCESS_READY", 2*time.Second)
	laterSource := fixture.start(t, "source-once", "source-later")
	laterSource.awaitLine(t, "SOURCE_PROCESS_READY", 2*time.Second)

	firstSource.begin(t)
	firstSource.awaitLine(t, "ATTEMPTING_SOURCE_TX", 2*time.Second)
	firstSource.awaitLine(t, "SOURCE_TX_READY", 2*time.Second)

	refresh := fixture.start(t, "refresh-hold", "refresh")
	refresh.awaitLine(t, "ATTEMPTING_MAINTENANCE_EXCLUSIVE", 2*time.Second)
	waitForAuthoritativeWriterIntent(t, fixture.maintenancePath(), 2*time.Second)
	refresh.assertNoLine(t, 100*time.Millisecond)

	laterSource.begin(t)
	laterSource.awaitLine(t, "ATTEMPTING_SOURCE_TX", 2*time.Second)
	laterSource.assertNoLine(t, 100*time.Millisecond)

	firstSource.release(t)
	refresh.awaitLine(t, "MAINTENANCE_EXCLUSIVE_ACQUIRED", 2*time.Second)
	laterSource.assertNoLine(t, 100*time.Millisecond)

	refresh.release(t)
	laterSource.awaitLine(t, "SOURCE_TX_READY", 2*time.Second)
	laterSource.awaitLine(t, "DONE", 2*time.Second)
	laterSource.waitSuccess(t)

	if got, want := fixture.events(t), []string{"source-first", "refresh", "source-later"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("acquisition events=%v want=%v", got, want)
	}
	if got, want := fixture.committedTransactions(t), []string{"source-first", "source-later"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed source transactions=%v want=%v", got, want)
	}
}

func TestAuthoritativeWriteLockSubprocessHelper(t *testing.T) {
	if os.Getenv(authoritativeWriteHelperEnv) != "1" {
		return
	}
	if err := runAuthoritativeWriteSubprocess(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

const (
	authoritativeWriteHelperEnv        = "DBRAIN_AUTHORITATIVE_WRITE_HELPER"
	authoritativeWriteHelperOperation  = "DBRAIN_AUTHORITATIVE_WRITE_OPERATION"
	authoritativeWriteHelperDBPath     = "DBRAIN_AUTHORITATIVE_WRITE_DB_PATH"
	authoritativeWriteHelperCacheDir   = "DBRAIN_AUTHORITATIVE_WRITE_CACHE_DIR"
	authoritativeWriteHelperDatabaseID = "DBRAIN_AUTHORITATIVE_WRITE_DATABASE_ID"
	authoritativeWriteHelperEventPath  = "DBRAIN_AUTHORITATIVE_WRITE_EVENT_PATH"
	authoritativeWriteHelperLabel      = "DBRAIN_AUTHORITATIVE_WRITE_LABEL"
)

type authoritativeWriteSubprocessFixture struct {
	dbPath     string
	cacheDir   string
	databaseID string
	eventPath  string
}

func newAuthoritativeWriteSubprocessFixture(t *testing.T) authoritativeWriteSubprocessFixture {
	t.Helper()
	cacheDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "brain.db")
	st, err := OpenWithSemanticCache(dbPath, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	databaseID, err := st.RetrievalDatabaseID(t.Context())
	if err == nil {
		_, err = st.db.ExecContext(
			t.Context(),
			`CREATE TABLE authoritative_write_lock_test_transactions (label TEXT NOT NULL UNIQUE)`,
		)
	}
	closeErr := st.Close()
	if err := errors.Join(err, closeErr); err != nil {
		t.Fatal(err)
	}
	return authoritativeWriteSubprocessFixture{
		dbPath:     dbPath,
		cacheDir:   cacheDir,
		databaseID: databaseID,
		eventPath:  filepath.Join(t.TempDir(), "events.log"),
	}
}

func (f authoritativeWriteSubprocessFixture) start(
	t *testing.T,
	operation string,
	label string,
) *authoritativeWriteHelperProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAuthoritativeWriteLockSubprocessHelper$")
	cmd.Env = append(os.Environ(),
		authoritativeWriteHelperEnv+"=1",
		authoritativeWriteHelperOperation+"="+operation,
		authoritativeWriteHelperDBPath+"="+f.dbPath,
		authoritativeWriteHelperCacheDir+"="+f.cacheDir,
		authoritativeWriteHelperDatabaseID+"="+f.databaseID,
		authoritativeWriteHelperEventPath+"="+f.eventPath,
		authoritativeWriteHelperLabel+"="+label,
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
	process := &authoritativeWriteHelperProcess{
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

func (f authoritativeWriteSubprocessFixture) maintenancePath() string {
	return filepath.Join(f.cacheDir, "semantic", f.databaseID, "locks", "maintenance.lock")
}

func (f authoritativeWriteSubprocessFixture) events(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(f.eventPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(raw))
}

func (f authoritativeWriteSubprocessFixture) committedTransactions(t *testing.T) []string {
	t.Helper()
	st, err := OpenWithSemanticCache(f.dbPath, f.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.db.QueryContext(
		t.Context(),
		`SELECT label FROM authoritative_write_lock_test_transactions ORDER BY rowid`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return labels
}

type authoritativeWriteHelperProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan string
	scanErr  chan error
	stderr   *bytes.Buffer
	waitOnce sync.Once
	waitErr  error
}

func (p *authoritativeWriteHelperProcess) awaitLine(t *testing.T, want string, timeout time.Duration) {
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

func (p *authoritativeWriteHelperProcess) assertNoLine(t *testing.T, duration time.Duration) {
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

func (p *authoritativeWriteHelperProcess) release(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, "release\n"); err != nil {
		t.Fatal(err)
	}
	p.awaitLine(t, "DONE", 2*time.Second)
	p.waitSuccess(t)
}

func (p *authoritativeWriteHelperProcess) begin(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, "start\n"); err != nil {
		t.Fatal(err)
	}
}

func (p *authoritativeWriteHelperProcess) waitSuccess(t *testing.T) {
	t.Helper()
	if err := p.wait(); err != nil {
		t.Fatalf("helper failed: %v stderr=%s", err, p.stderr.String())
	}
}

func (p *authoritativeWriteHelperProcess) wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
	})
	return p.waitErr
}

func (p *authoritativeWriteHelperProcess) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if p.cmd.ProcessState == nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.wait()
}

func runAuthoritativeWriteSubprocess(ctx context.Context) error {
	switch os.Getenv(authoritativeWriteHelperOperation) {
	case "source-hold":
		return runAuthoritativeWriteSource(ctx, true)
	case "source-once":
		return runAuthoritativeWriteSource(ctx, false)
	case "refresh-hold":
		return runAuthoritativeWriteRefresh(ctx)
	default:
		return fmt.Errorf(
			"unknown authoritative write helper operation %q",
			os.Getenv(authoritativeWriteHelperOperation),
		)
	}
}

func runAuthoritativeWriteSource(ctx context.Context, hold bool) (resultErr error) {
	st, err := OpenWithSemanticCache(
		os.Getenv(authoritativeWriteHelperDBPath),
		os.Getenv(authoritativeWriteHelperCacheDir),
	)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, st.Close()) }()

	fmt.Println("SOURCE_PROCESS_READY")
	if err := awaitAuthoritativeWriteCommand("start"); err != nil {
		return err
	}
	fmt.Println("ATTEMPTING_SOURCE_TX")
	_, err = withAuthoritativeWriteTx(ctx, st, "owner=subprocess-source\n", func(
		_ context.Context,
		tx authoritativeWriteTx,
	) (struct{}, error) {
		label := os.Getenv(authoritativeWriteHelperLabel)
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO authoritative_write_lock_test_transactions (label) VALUES (?)`,
			label,
		); err != nil {
			return struct{}{}, err
		}
		if err := appendAuthoritativeWriteEvent(label); err != nil {
			return struct{}{}, err
		}
		fmt.Println("SOURCE_TX_READY")
		if hold {
			if err := awaitAuthoritativeWriteCommand("release"); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	fmt.Println("DONE")
	return nil
}

func runAuthoritativeWriteRefresh(ctx context.Context) (resultErr error) {
	scope, err := semanticlock.NewScope(
		os.Getenv(authoritativeWriteHelperCacheDir),
		os.Getenv(authoritativeWriteHelperDatabaseID),
	)
	if err != nil {
		return err
	}
	fmt.Println("ATTEMPTING_MAINTENANCE_EXCLUSIVE")
	lease, err := scope.AcquireMaintenanceExclusive(ctx, "owner=subprocess-refresh\n")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
	if err := appendAuthoritativeWriteEvent(os.Getenv(authoritativeWriteHelperLabel)); err != nil {
		return err
	}
	fmt.Println("MAINTENANCE_EXCLUSIVE_ACQUIRED")
	if err := awaitAuthoritativeWriteCommand("release"); err != nil {
		return err
	}
	if err := lease.Close(); err != nil {
		return err
	}
	fmt.Println("DONE")
	return nil
}

func appendAuthoritativeWriteEvent(event string) error {
	file, err := os.OpenFile(
		os.Getenv(authoritativeWriteHelperEventPath),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, event+"\n")
	return errors.Join(writeErr, file.Close())
}

func awaitAuthoritativeWriteCommand(want string) error {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return errors.Join(io.EOF, scanner.Err())
	}
	if scanner.Text() != want {
		return fmt.Errorf("unexpected helper command %q", scanner.Text())
	}
	return nil
}

func waitForAuthoritativeWriterIntent(t *testing.T, lockPath string, timeout time.Duration) {
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
