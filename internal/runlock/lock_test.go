package runlock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type lockAcquisition struct {
	name string
	lock *Lock
	err  error
}

func TestAcquireRejectsConcurrentHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync-all.lock")
	first, err := Acquire(path, "owner=test\n")
	if err != nil {
		t.Fatalf("Acquire first: %v", err)
	}
	defer func() {
		_ = first.Close()
	}()

	second, err := Acquire(path, "owner=second\n")
	if err == nil {
		_ = second.Close()
		t.Fatal("expected second acquire to fail")
	}
	if !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("expected ErrAlreadyLocked, got %v", err)
	}
}

func TestAcquireRejectsSymlinkedParentAndLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix no-follow regression")
	}
	t.Run("parent", func(t *testing.T) {
		base := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(base, "locks")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		lock, err := Acquire(filepath.Join(base, "locks", "archive.lock"), "owner=test\n")
		if err == nil {
			_ = lock.Close()
			t.Fatal("Acquire followed parent symlink")
		}
		if _, err := os.Stat(filepath.Join(outside, "archive.lock")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("outside lock was created: %v", err)
		}
	})

	t.Run("leaf", func(t *testing.T) {
		base := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.lock")
		if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "archive.lock")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		lock, err := Acquire(link, "owner=test\n")
		if err == nil {
			_ = lock.Close()
			t.Fatal("Acquire followed leaf symlink")
		}
		data, err := os.ReadFile(outside)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "outside\n" {
			t.Fatalf("outside lock changed to %q", data)
		}
	})
}

type failingFileLock struct {
	err error
}

func (f failingFileLock) close() error { return f.err }

func TestLockCloseSurfacesReleaseFailure(t *testing.T) {
	closeErr := errors.New("synthetic release failure")
	lock := &Lock{file: failingFileLock{err: closeErr}}
	if err := lock.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
}

func TestAcquireContextSharedHoldersCoexist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	first, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("AcquireContext first shared: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("AcquireContext second shared: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close second shared: %v", err)
	}
}

func TestAcquireContextExclusiveExcludesOtherModes(t *testing.T) {
	tests := []struct {
		name       string
		holderMode Mode
		waiterMode Mode
	}{
		{name: "shared blocks exclusive", holderMode: Shared, waiterMode: Exclusive},
		{name: "exclusive blocks shared", holderMode: Exclusive, waiterMode: Shared},
		{name: "exclusive blocks exclusive", holderMode: Exclusive, waiterMode: Exclusive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "semantic.lock")
			holder, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: test.holderMode})
			if err != nil {
				t.Fatalf("AcquireContext holder: %v", err)
			}
			defer func() { _ = holder.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			waiter, err := AcquireContext(ctx, path, AcquireOptions{Mode: test.waiterMode})
			if waiter != nil {
				_ = waiter.Close()
				t.Fatal("blocked waiter unexpectedly acquired")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("AcquireContext blocked waiter error = %v, want deadline exceeded", err)
			}
		})
	}
}

func TestAcquireContextRejectsInvalidMode(t *testing.T) {
	lock, err := AcquireContext(context.Background(), filepath.Join(t.TempDir(), "semantic.lock"), AcquireOptions{})
	if lock != nil {
		_ = lock.Close()
		t.Fatal("invalid mode unexpectedly acquired")
	}
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("invalid mode error = %v, want mode diagnostic", err)
	}
}

func TestAcquireContextCancellationLeavesNoIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	holder, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("AcquireContext shared holder: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	waiter, err := AcquireContext(ctx, path, AcquireOptions{Mode: Exclusive})
	if waiter != nil {
		_ = waiter.Close()
		t.Fatal("cancelled writer unexpectedly acquired")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireContext writer error = %v, want deadline exceeded", err)
	}
	if err := holder.Close(); err != nil {
		t.Fatalf("Close shared holder: %v", err)
	}

	readerCtx, readerCancel := context.WithTimeout(context.Background(), time.Second)
	defer readerCancel()
	reader, err := AcquireContext(readerCtx, path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("reader blocked by cancelled writer intent: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}
}

func TestAcquireContextWriterSequenceRecoversFromEmptyCoordinator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	coordinator, err := acquireCoordinator(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire coordinator: %v", err)
	}
	if err := replaceFileLockMetadata(coordinator.file, ""); err != nil {
		_ = coordinator.close()
		t.Fatalf("empty coordinator metadata: %v", err)
	}
	if err := coordinator.close(); err != nil {
		t.Fatalf("close coordinator: %v", err)
	}

	const liveSequence = uint64(7)
	liveTicketPath := writerTicketPath(path, liveSequence)
	liveTicket, err := acquireHeldFileContext(context.Background(), liveTicketPath, Exclusive)
	if err != nil {
		t.Fatalf("acquire live writer ticket: %v", err)
	}
	defer func() {
		coordinator, acquireErr := acquireCoordinator(context.Background(), path)
		if acquireErr == nil {
			_ = removeLockedFile(liveTicket.file, liveTicketPath)
			_ = coordinator.close()
		}
		_ = liveTicket.close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		lock, acquireErr := AcquireContext(ctx, path, AcquireOptions{Mode: Exclusive})
		if lock != nil {
			_ = lock.Close()
		}
		result <- acquireErr
	}()

	nextTicketPath := writerTicketPath(path, liveSequence+1)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(nextTicketPath); statErr == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, statErr := os.Stat(nextTicketPath)
	cancel()
	acquireErr := <-result
	if statErr != nil {
		t.Fatalf("next writer ticket was not sequenced after live ticket: %v", statErr)
	}
	if !errors.Is(acquireErr, context.Canceled) {
		t.Fatalf("cancelled writer error = %v, want context canceled", acquireErr)
	}
}

func TestAcquireContextCancellationDoesNotWaitForCoordinatorCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	holder, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("AcquireContext shared holder: %v", err)
	}
	defer func() { _ = holder.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		lock, acquireErr := AcquireContext(ctx, path, AcquireOptions{Mode: Exclusive})
		if lock != nil {
			_ = lock.Close()
		}
		result <- acquireErr
	}()
	waitForWriterIntentCount(t, path, 1)

	coordinator, err := acquireCoordinator(context.Background(), path)
	if err != nil {
		cancel()
		t.Fatalf("acquire coordinator: %v", err)
	}
	cancel()

	var (
		acquireErr error
		prompt     bool
	)
	select {
	case acquireErr = <-result:
		prompt = true
	case <-time.After(250 * time.Millisecond):
	}
	if err := coordinator.close(); err != nil {
		t.Fatalf("close coordinator: %v", err)
	}
	if !prompt {
		<-result
		t.Fatal("cancelled writer waited for indefinitely held coordinator cleanup")
	}
	if !errors.Is(acquireErr, context.Canceled) {
		t.Fatalf("cancelled writer error = %v, want context canceled", acquireErr)
	}

	readerCtx, readerCancel := context.WithTimeout(context.Background(), time.Second)
	defer readerCancel()
	reader, err := AcquireContext(readerCtx, path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("reader did not clean released stale intent: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}
}

func TestAcquireContextWritersAreFIFOAndReadersDoNotBarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	holder, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Shared})
	if err != nil {
		t.Fatalf("AcquireContext initial reader: %v", err)
	}

	results := make(chan lockAcquisition, 3)
	start := func(name string, mode Mode) {
		go func() {
			lock, acquireErr := AcquireContext(context.Background(), path, AcquireOptions{
				Mode:     mode,
				Metadata: "owner=" + name + "\n",
			})
			results <- lockAcquisition{name: name, lock: lock, err: acquireErr}
		}()
	}

	start("writer-1", Exclusive)
	waitForWriterIntentCount(t, path, 1)
	start("writer-2", Exclusive)
	waitForWriterIntentCount(t, path, 2)
	start("reader", Shared)

	if err := holder.Close(); err != nil {
		t.Fatalf("Close initial reader: %v", err)
	}

	first := receiveAcquired(t, results)
	if first.err != nil {
		t.Fatalf("%s acquire: %v", first.name, first.err)
	}
	if first.name != "writer-1" {
		_ = first.lock.Close()
		t.Fatalf("first acquisition = %s, want writer-1", first.name)
	}
	if err := first.lock.Close(); err != nil {
		t.Fatalf("Close writer-1: %v", err)
	}

	second := receiveAcquired(t, results)
	if second.err != nil {
		t.Fatalf("%s acquire: %v", second.name, second.err)
	}
	if second.name != "writer-2" {
		_ = second.lock.Close()
		t.Fatalf("second acquisition = %s, want writer-2", second.name)
	}
	if err := second.lock.Close(); err != nil {
		t.Fatalf("Close writer-2: %v", err)
	}

	third := receiveAcquired(t, results)
	if third.err != nil {
		t.Fatalf("%s acquire: %v", third.name, third.err)
	}
	if third.name != "reader" {
		_ = third.lock.Close()
		t.Fatalf("third acquisition = %s, want reader", third.name)
	}
	if err := third.lock.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}
}

func TestAcquireContextWaiterAcquiresAfterSameProcessClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic.lock")
	holder, err := AcquireContext(context.Background(), path, AcquireOptions{Mode: Exclusive})
	if err != nil {
		t.Fatalf("AcquireContext exclusive holder: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		waiter, acquireErr := AcquireContext(ctx, path, AcquireOptions{Mode: Shared})
		if acquireErr == nil {
			acquireErr = waiter.Close()
		}
		result <- acquireErr
	}()

	select {
	case err := <-result:
		t.Fatalf("waiter completed before holder close: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := holder.Close(); err != nil {
		t.Fatalf("Close holder: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("waiter after close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire after same-process close")
	}
}

func TestAcquireContextRejectsSymlinkedLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by Windows reparse-point tests")
	}
	outside := filepath.Join(t.TempDir(), "outside.lock")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "semantic.lock")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	lock, err := AcquireContext(context.Background(), link, AcquireOptions{Mode: Shared})
	if err == nil {
		_ = lock.Close()
		t.Fatal("AcquireContext followed leaf symlink")
	}
	data, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "outside\n" {
		t.Fatalf("outside lock changed to %q", data)
	}
}

func receiveAcquired(t *testing.T, results <-chan lockAcquisition) lockAcquisition {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lock acquisition")
		return lockAcquisition{}
	}
}

func waitForWriterIntentCount(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	pattern := path + ".writer-*.intent"
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob writer intents: %v", err)
		}
		if len(matches) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("writer intent count did not reach %d", want)
}
