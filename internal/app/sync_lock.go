package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runlock"
)

func syncAllLockPath(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "locks", "sync-all.lock")
}

func acquireSyncAllLock(cfg config.Config, owner string) (*runlock.Lock, error) {
	path := syncAllLockPath(cfg)
	metadata := fmt.Sprintf("owner=%s\npid=%d\nstarted_at=%s\n", owner, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	lock, err := runlock.Acquire(path, metadata)
	if err == nil {
		return lock, nil
	}
	if errors.Is(err, runlock.ErrAlreadyLocked) {
		return nil, fmt.Errorf("%w: sync all already running (lock: %s)", runlock.ErrAlreadyLocked, path)
	}
	return nil, err
}

func isSyncAllAlreadyRunning(err error) bool {
	return errors.Is(err, runlock.ErrAlreadyLocked)
}
