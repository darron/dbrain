package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareStateDirCreatesSecureLeaf(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")
	resolved, err := PrepareStateDir(dir)
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if resolved != want {
		t.Fatalf("resolved = %q, want %q", resolved, want)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %04o, want 0700", got)
	}
}

func TestPrepareStateDirRejectsLooseLeaf(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := PrepareStateDir(dir); err == nil {
		t.Fatalf("PrepareStateDir succeeded, want permission error")
	}
}

func TestAcquireStateLockRejectsSecondHolder(t *testing.T) {
	t.Parallel()

	dir, err := PrepareStateDir(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	lock, err := AcquireStateLock(dir)
	if err != nil {
		t.Fatalf("AcquireStateLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	if _, err := AcquireStateLock(dir); !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("second AcquireStateLock error = %v, want ErrAlreadyLocked", err)
	}
	if lock.Path() != filepath.Join(dir, StateLockName) {
		t.Fatalf("lock path = %q", lock.Path())
	}
}

func TestLooksLikeSyncedPath(t *testing.T) {
	t.Parallel()

	if !LooksLikeSyncedPath("/Users/alice/Library/Mobile Documents/com~apple~CloudDocs/dbrain") {
		t.Fatalf("expected iCloud-like path to be detected")
	}
	if LooksLikeSyncedPath("/Users/alice/.local/share/dbrain") {
		t.Fatalf("expected local path to be allowed")
	}
}
