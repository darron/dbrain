//go:build windows

package runlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type windowsFileLock struct {
	path string
	file *os.File
}

func acquireFileLock(path string, metadata string) (fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyLocked, path)
		}
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if metadata != "" {
		if _, err := file.WriteString(metadata); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("write lock file %s: %w", path, err)
		}
	}
	return &windowsFileLock{path: path, file: file}, nil
}

func (l *windowsFileLock) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	closeErr := l.file.Close()
	l.file = nil
	removeErr := os.Remove(l.path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
