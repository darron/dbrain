//go:build !windows

package remote

import (
	"errors"
	"path/filepath"
	"testing"
)

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
