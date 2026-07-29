package runlock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const runLockHelperEnv = "DBRAIN_RUNLOCK_HELPER"

func TestRunLockHelperProcess(t *testing.T) {
	if os.Getenv(runLockHelperEnv) != "1" {
		return
	}
	mode := Shared
	if os.Getenv("DBRAIN_RUNLOCK_MODE") == "exclusive" {
		mode = Exclusive
	}
	lock, err := AcquireContext(context.Background(), os.Getenv("DBRAIN_RUNLOCK_PATH"), AcquireOptions{
		Mode:     mode,
		Metadata: "owner=subprocess\n",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "acquire: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = lock.Close() }()
	fmt.Println("ACQUIRED")
	for {
		time.Sleep(time.Hour)
	}
}

func TestAcquireContextProcessExitReleasesLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	process := startRunLockHelper(t, path, "exclusive")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	lock, err := AcquireContext(ctx, path, AcquireOptions{Mode: Shared})
	if lock != nil {
		_ = lock.Close()
		t.Fatal("shared lock acquired while subprocess held exclusive")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked shared error = %v, want deadline exceeded", err)
	}

	process.killAndWait(t)
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer retryCancel()
	lock, err = AcquireContext(retryCtx, path, AcquireOptions{Mode: Exclusive})
	if err != nil {
		t.Fatalf("exclusive lock after process exit: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close exclusive lock: %v", err)
	}
}

func TestAcquireContextSharedLeaseCoexistsAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	process := startRunLockHelper(t, path, "shared")
	defer process.killAndWait(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock, err := AcquireContext(ctx, path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("shared lock beside subprocess reader: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close shared lock: %v", err)
	}
}

func TestAcquireContextRecoversCrashedQueuedWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	holder, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("AcquireContext shared holder: %v", err)
	}
	defer func() { _ = holder.Close() }()

	process := startQueuedRunLockHelper(t, path, "exclusive")
	waitForWriterIntentCount(t, path, 1)

	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	blockedReader, blockedErr := AcquireContext(blockedCtx, path, AcquireOptions{Mode: Shared})
	blockedCancel()
	if blockedReader != nil {
		_ = blockedReader.Close()
		t.Fatal("later reader barged past subprocess writer intent")
	}
	if !errors.Is(blockedErr, context.DeadlineExceeded) {
		t.Fatalf("reader behind writer intent error = %v, want deadline exceeded", blockedErr)
	}

	process.killAndWait(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reader, err := AcquireContext(ctx, path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("shared lock after queued writer crash: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}
}

type runLockHelperProcess struct {
	cmd *exec.Cmd
}

func startRunLockHelper(t *testing.T, path string, mode string) *runLockHelperProcess {
	t.Helper()
	cmd := newRunLockHelperCommand(path, mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper did not report acquisition: %v", scanner.Err())
	}
	if scanner.Text() != "ACQUIRED" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper output = %q, want ACQUIRED", scanner.Text())
	}
	return &runLockHelperProcess{cmd: cmd}
}

func startQueuedRunLockHelper(t *testing.T, path string, mode string) *runLockHelperProcess {
	t.Helper()
	cmd := newRunLockHelperCommand(path, mode)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start queued helper: %v", err)
	}
	return &runLockHelperProcess{cmd: cmd}
}

func newRunLockHelperCommand(path string, mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		runLockHelperEnv+"=1",
		"DBRAIN_RUNLOCK_PATH="+path,
		"DBRAIN_RUNLOCK_MODE="+mode,
	)
	return cmd
}

func (p *runLockHelperProcess) killAndWait(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Kill helper: %v", err)
	}
	if err := p.cmd.Wait(); err == nil {
		t.Fatal("killed helper exited successfully")
	}
}
