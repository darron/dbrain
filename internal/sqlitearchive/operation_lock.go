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
	path    string
	release func() error
}

type operationLeaseAcquirer func(config.Config, string) (*OperationLease, error)

func operationLockPath(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "locks", "sqlite-archive.lock")
}

func AcquireOperationLease(cfg config.Config, owner string) (*OperationLease, error) {
	path := operationLockPath(cfg)
	metadata := fmt.Sprintf("owner=%s\npid=%d\nstarted_at=%s\n", strings.TrimSpace(owner), os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	lock, err := runlock.Acquire(path, metadata)
	if err == nil {
		return &OperationLease{path: path, release: lock.Close}, nil
	}
	if errors.Is(err, runlock.ErrAlreadyLocked) {
		return nil, fmt.Errorf("%w: %s", ErrOperationLocked, path)
	}
	return nil, err
}

func (l *OperationLease) Close() error {
	if l == nil || l.release == nil {
		return nil
	}
	release := l.release
	l.release = nil
	return release()
}

func operationLease(cfg config.Config, opts Options, owner string) (*OperationLease, bool, error) {
	if opts.OperationLease != nil {
		if opts.OperationLease.release == nil || opts.OperationLease.path != operationLockPath(cfg) {
			return nil, false, fmt.Errorf("invalid SQLite archive operation lease")
		}
		return opts.OperationLease, false, nil
	}
	acquire := opts.acquireLease
	if acquire == nil {
		acquire = AcquireOperationLease
	}
	lease, err := acquire(cfg, owner)
	return lease, true, err
}
