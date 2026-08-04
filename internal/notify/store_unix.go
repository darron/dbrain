//go:build unix

package notify

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type unixStateStoreFS struct {
	dirFD int
}

type unixStateReaderFS struct {
	logDir string
}

func openStateStoreFS(logDir string) (stateStoreFS, error) {
	if logDir == "" {
		return nil, fmt.Errorf("notification log directory is required")
	}
	logFD, err := openNotificationAbsoluteDirNoFollow(logDir)
	if err != nil {
		return nil, fmt.Errorf("open notification log directory without symlinks: %w", err)
	}
	dirFD, err := openOrCreateNotificationDir(logFD)
	_ = unix.Close(logFD)
	if err != nil {
		return nil, err
	}
	return &unixStateStoreFS{dirFD: dirFD}, nil
}

func openStateReaderFS(logDir string) (stateFS, error) {
	if logDir == "" {
		return nil, fmt.Errorf("notification log directory is required")
	}
	return &unixStateReaderFS{logDir: logDir}, nil
}

func (f *unixStateReaderFS) openDir() (int, error) {
	logFD, err := openNotificationAbsoluteDirNoFollow(f.logDir)
	if err != nil {
		return -1, err
	}
	dirFD, err := unix.Openat(logFD, "notifications", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	_ = unix.Close(logFD)
	return dirFD, err
}

func openNotificationAbsoluteDirNoFollow(path string) (int, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return -1, err
	}
	abs = filepath.Clean(abs)
	trustedTemp := filepath.Clean(os.TempDir())
	if abs == trustedTemp || strings.HasPrefix(abs, trustedTemp+string(filepath.Separator)) {
		resolved, resolveErr := filepath.EvalSymlinks(trustedTemp)
		if resolveErr != nil {
			return -1, fmt.Errorf("resolve trusted system temporary root: %w", resolveErr)
		}
		relative, relErr := filepath.Rel(trustedTemp, abs)
		if relErr != nil {
			return -1, relErr
		}
		abs = filepath.Join(resolved, relative)
	}
	anchor := filepath.VolumeName(abs) + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return -1, fmt.Errorf("path is outside its anchor")
	}
	fd, err := unix.Open(anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, openErr
		}
		fd = next
	}
	return fd, nil
}

func openOrCreateNotificationDir(logFD int) (int, error) {
	dirFD, err := unix.Openat(logFD, "notifications", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		if err := unix.Mkdirat(logFD, "notifications", 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create private notification directory: %w", err)
		}
		if err := unix.Fsync(logFD); err != nil {
			return -1, fmt.Errorf("sync notification log directory: %w", err)
		}
		dirFD, err = unix.Openat(logFD, "notifications", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return -1, fmt.Errorf("open private notification directory without symlinks: %w", err)
	}
	if err := unix.Fchmod(dirFD, 0o700); err != nil {
		_ = unix.Close(dirFD)
		return -1, fmt.Errorf("enforce private notification directory mode: %w", err)
	}
	return dirFD, nil
}

func (f *unixStateStoreFS) ValidateStateLeaf() error {
	var stat unix.Stat_t
	err := unix.Fstatat(f.dirFD, "state.json", &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("notification state leaf is not regular")
	}
	fd, err := unix.Openat(f.dirFD, "state.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return unix.Fchmod(fd, 0o600)
}

func (f *unixStateStoreFS) ReadState(limit int64) ([]byte, error) {
	return readUnixState(f.dirFD, limit, true)
}

func (f *unixStateReaderFS) ReadState(limit int64) ([]byte, error) {
	dirFD, err := f.openDir()
	if errors.Is(err, unix.ENOENT) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(dirFD) }()
	return readUnixState(dirFD, limit, false)
}

func readUnixState(dirFD int, limit int64, enforceMode bool) ([]byte, error) {
	fd, err := unix.Openat(dirFD, "state.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "state.json")
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open notification state descriptor")
	}
	defer func() { _ = file.Close() }()
	if enforceMode {
		if err := file.Chmod(0o600); err != nil {
			return nil, err
		}
	}
	return readBoundedState(file, limit)
}

func (f *unixStateReaderFS) ReplaceState([]byte) error {
	return fmt.Errorf("notification state reader is read-only")
}

func (f *unixStateStoreFS) ReplaceState(data []byte) error {
	if err := f.ValidateStateLeaf(); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	tempName := ".state-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(f.dirFD, tempName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), tempName)
	if file == nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(f.dirFD, tempName, 0)
		return fmt.Errorf("open notification state temp descriptor")
	}
	defer func() {
		_ = file.Close()
		_ = unix.Unlinkat(f.dirFD, tempName, 0)
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := syncNotificationStateFile(file); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(f.dirFD, tempName, f.dirFD, "state.json"); err != nil {
		return err
	}
	return syncNotificationStateDirectory(func() error { return unix.Fsync(f.dirFD) })
}
