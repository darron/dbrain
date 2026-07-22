package runlock

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
