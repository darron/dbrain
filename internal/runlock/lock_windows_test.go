//go:build windows

package runlock

import (
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
