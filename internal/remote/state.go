package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const StateLockName = "dbrain.lock"

type StateLock struct {
	file *os.File
	path string
}

func ResolveStateDir(path string) (string, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return abs, nil
}

func PrepareStateDir(path string) (string, error) {
	abs, err := ResolveStateDir(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("create state dir %s: %w", abs, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve state dir symlinks %s: %w", abs, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat state dir %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("state dir is not a directory: %s", resolved)
	}
	if info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("state dir %s must have permissions 0700, got %04o", resolved, info.Mode().Perm())
	}
	if err := checkCurrentUserOwns(info); err != nil {
		return "", err
	}
	return resolved, nil
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
			return nil, fmt.Errorf("state dir is already locked by another dbrain process: %s", resolved)
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

func LooksLikeSyncedPath(path string) bool {
	lower := strings.ToLower(path)
	for _, marker := range []string{"icloud drive", "mobile documents", "dropbox", "onedrive", "google drive"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func expandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func checkCurrentUserOwns(info os.FileInfo) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("state dir is not owned by the current user")
	}
	return nil
}
