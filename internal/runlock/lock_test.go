package runlock

import (
	"errors"
	"path/filepath"
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
