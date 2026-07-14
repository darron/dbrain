package sqlitearchive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runlock"
)

var ErrOperationLocked = errors.New("SQLite archive operation already running")

type OperationLease struct {
	path string
	lock *runlock.Lock
}

func operationLockPath(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "locks", "sqlite-archive.lock")
}

func AcquireOperationLease(cfg config.Config, owner string) (*OperationLease, error) {
	path := operationLockPath(cfg)
	metadata := fmt.Sprintf("owner=%s\npid=%d\nstarted_at=%s\n", strings.TrimSpace(owner), os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	lock, err := runlock.Acquire(path, metadata)
	if err == nil {
		return &OperationLease{path: path, lock: lock}, nil
	}
	if errors.Is(err, runlock.ErrAlreadyLocked) {
		return nil, fmt.Errorf("%w: %s", ErrOperationLocked, path)
	}
	return nil, err
}

func (l *OperationLease) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	err := l.lock.Close()
	l.lock = nil
	return err
}

func operationLease(cfg config.Config, opts Options, owner string) (*OperationLease, bool, error) {
	if opts.OperationLease != nil {
		if opts.OperationLease.lock == nil || opts.OperationLease.path != operationLockPath(cfg) {
			return nil, false, fmt.Errorf("invalid SQLite archive operation lease")
		}
		return opts.OperationLease, false, nil
	}
	lease, err := AcquireOperationLease(cfg, owner)
	return lease, true, err
}
