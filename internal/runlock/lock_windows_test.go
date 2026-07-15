//go:build windows

package runlock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsAcquireReusesCrashLeftSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.lock")
	if err := os.WriteFile(path, []byte("stale owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(path, "owner=current\n")
	if err != nil {
		t.Fatalf("Acquire with crash-left sentinel: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent lock file missing after close: %v", err)
	}
}

func TestWindowsAcquireRejectsAncestorReparseWithoutExternalMutation(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	redirect := filepath.Join(base, "redirect")
	if err := os.Symlink(outside, redirect); err != nil {
		t.Skipf("Windows symlinks unavailable: %v", err)
	}
	path := filepath.Join(redirect, "locks", "archive.lock")
	lock, err := Acquire(path, "owner=test\n")
	if err == nil {
		_ = lock.Close()
		t.Fatal("Acquire followed an ancestor reparse point")
	}
	if _, err := os.Stat(filepath.Join(outside, "locks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external lock directory was created: %v", err)
	}
}
