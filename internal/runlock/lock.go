package runlock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrAlreadyLocked = errors.New("run lock already held")

type Lock struct {
	path   string
	file   fileLock
	local  *localLease
	intent *writerIntent
	mu     sync.Mutex
}

func Acquire(path string, metadata string) (*Lock, error) {
	path, err := canonicalLockPath(path)
	if err != nil {
		return nil, err
	}
	local, ok := tryAcquireProcessGate(path, Exclusive)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyLocked, path)
	}
	file, err := tryAcquireFileLock(path, Exclusive)
	if err != nil {
		local.release()
		return nil, err
	}
	if err := replaceFileLockMetadata(file, metadata); err != nil {
		_ = file.close()
		local.release()
		return nil, fmt.Errorf("write lock metadata %s: %w", path, err)
	}
	return &Lock{path: path, file: file, local: local}, nil
}

func AcquireContext(ctx context.Context, path string, options AcquireOptions) (*Lock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := options.Mode.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonicalPath, err := canonicalLockPath(path)
	if err != nil {
		return nil, err
	}
	if options.Mode == Shared {
		return acquireSharedContext(ctx, canonicalPath)
	}
	return acquireExclusiveContext(ctx, canonicalPath, options.Metadata)
}

func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	file := l.file
	l.file = nil
	local := l.local
	l.local = nil
	intent := l.intent
	l.intent = nil
	l.mu.Unlock()

	var errs []error
	if file != nil {
		errs = append(errs, file.close())
	}
	if local != nil {
		local.release()
	}
	if intent != nil {
		errs = append(errs, intent.close())
	}
	return errors.Join(errs...)
}

func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

type fileLock interface {
	close() error
}

type metadataFileLock interface {
	metadataFile() *os.File
}

type heldFile struct {
	path  string
	file  fileLock
	local *localLease
}

func acquireHeldFileContext(ctx context.Context, path string, mode Mode) (*heldFile, error) {
	local, err := acquireProcessGate(ctx, path, mode)
	if err != nil {
		return nil, err
	}
	for {
		file, acquireErr := tryAcquireFileLock(path, mode)
		if acquireErr == nil {
			return &heldFile{path: path, file: file, local: local}, nil
		}
		if !errors.Is(acquireErr, ErrAlreadyLocked) {
			local.release()
			return nil, acquireErr
		}
		if err := waitForRetry(ctx); err != nil {
			local.release()
			return nil, err
		}
	}
}

func (h *heldFile) close() error {
	if h == nil {
		return nil
	}
	var err error
	if h.file != nil {
		err = h.file.close()
		h.file = nil
	}
	if h.local != nil {
		h.local.release()
		h.local = nil
	}
	return err
}

func replaceFileLockMetadata(lock fileLock, metadata string) error {
	metadataLock, ok := lock.(metadataFileLock)
	if !ok {
		return errors.New("lock file does not expose metadata")
	}
	file := metadataLock.metadataFile()
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if metadata == "" {
		return nil
	}
	_, err := file.WriteString(metadata)
	return err
}

func readFileLockMetadata(lock fileLock) (string, error) {
	metadataLock, ok := lock.(metadataFileLock)
	if !ok {
		return "", errors.New("lock file does not expose metadata")
	}
	file := metadataLock.metadataFile()
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func waitForRetry(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func canonicalLockPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve run lock path %s: %w", path, err)
	}
	return normalizeLockPath(absolute), nil
}
