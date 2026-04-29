//go:build !windows

package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type StateLock struct {
	file *os.File
	path string
}

func AcquireStateLock(stateDir string) (*StateLock, error) {
	resolved, err := ResolveStateDir(stateDir)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(resolved, StateLockName)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state lock %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyLocked, resolved)
		}
		return nil, fmt.Errorf("lock state dir %s: %w", resolved, err)
	}
	return &StateLock{file: file, path: lockPath}, nil
}

func (l *StateLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

func (l *StateLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func checkCurrentUserOwns(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("state dir is not owned by the current user")
	}
	return nil
}
