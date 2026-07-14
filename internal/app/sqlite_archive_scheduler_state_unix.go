//go:build unix

package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"golang.org/x/sys/unix"
)

const scheduledSQLiteArchiveAttemptName = "sqlite-archive-last-attempt"

func readScheduledSQLiteArchiveAttempt(cfg config.Config) (time.Time, error) {
	dirFD, err := openScheduledSQLiteArchiveStateDir(cfg, false, nil)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	defer func() { _ = unix.Close(dirFD) }()
	fd, err := unix.Openat(dirFD, scheduledSQLiteArchiveAttemptName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("open SQLite archive scheduler state without symlinks: %w", err)
	}
	file := os.NewFile(uintptr(fd), scheduledSQLiteArchiveAttemptName)
	if file == nil {
		_ = unix.Close(fd)
		return time.Time{}, fmt.Errorf("open SQLite archive scheduler state descriptor")
	}
	defer func() { _ = file.Close() }()
	return parseScheduledSQLiteArchiveAttempt(file)
}

func writeScheduledSQLiteArchiveAttempt(cfg config.Config, attemptedAt time.Time) error {
	return writeScheduledSQLiteArchiveAttemptWithDirSync(cfg, attemptedAt, unix.Fsync)
}

func writeScheduledSQLiteArchiveAttemptWithDirSync(cfg config.Config, attemptedAt time.Time, syncDir func(int) error) error {
	dirFD, err := openScheduledSQLiteArchiveStateDir(cfg, true, syncDir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(dirFD) }()
	if err := rejectScheduledSQLiteArchiveStateLeaf(dirFD); err != nil {
		return err
	}
	tempName, temp, err := createScheduledSQLiteArchiveStateTemp(dirFD)
	if err != nil {
		return err
	}
	defer func() {
		_ = temp.Close()
		_ = unix.Unlinkat(dirFD, tempName, 0)
	}()
	if _, err := io.WriteString(temp, attemptedAt.UTC().Format(time.RFC3339Nano)+"\n"); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := rejectScheduledSQLiteArchiveStateLeaf(dirFD); err != nil {
		return err
	}
	if err := unix.Renameat(dirFD, tempName, dirFD, scheduledSQLiteArchiveAttemptName); err != nil {
		return fmt.Errorf("replace SQLite archive scheduler state: %w", err)
	}
	if syncDir == nil {
		return fmt.Errorf("sync SQLite archive scheduler state directory: missing sync function")
	}
	if err := syncDir(dirFD); err != nil {
		return fmt.Errorf("sync SQLite archive scheduler state directory: %w", err)
	}
	return nil
}

func parseScheduledSQLiteArchiveAttempt(file *os.File) (time.Time, error) {
	info, err := file.Stat()
	if err != nil {
		return time.Time{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128 {
		return time.Time{}, fmt.Errorf("invalid SQLite archive scheduler state")
	}
	data, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil {
		return time.Time{}, err
	}
	if len(data) > 128 {
		return time.Time{}, fmt.Errorf("invalid SQLite archive scheduler state")
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid SQLite archive scheduler state")
	}
	return parsed.UTC(), nil
}

func openScheduledSQLiteArchiveStateDir(cfg config.Config, create bool, syncDir func(int) error) (int, error) {
	path := filepath.Join(cfg.DataDir, "scheduler")
	path = normalizeSchedulerStatePath(path)
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return -1, fmt.Errorf("SQLite archive scheduler state is outside its path anchor")
	}
	fd, err := unix.Open(anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open SQLite archive scheduler state anchor: %w", err)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) && create && index == len(parts)-1 {
			mkdirErr := unix.Mkdirat(fd, part, 0o700)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, fmt.Errorf("create SQLite archive scheduler state directory: %w", mkdirErr)
			}
			if mkdirErr == nil {
				if syncDir == nil {
					_ = unix.Close(fd)
					return -1, fmt.Errorf("sync SQLite archive scheduler state parent: missing sync function")
				}
				if syncErr := syncDir(fd); syncErr != nil {
					_ = unix.Close(fd)
					return -1, fmt.Errorf("sync SQLite archive scheduler state parent: %w", syncErr)
				}
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, fmt.Errorf("open SQLite archive scheduler state directory without symlinks: %w", openErr)
		}
		fd = next
	}
	return fd, nil
}

func normalizeSchedulerStatePath(path string) string {
	path = filepath.Clean(path)
	trustedTemp := filepath.Clean(os.TempDir())
	if path == trustedTemp || strings.HasPrefix(path, trustedTemp+string(filepath.Separator)) {
		if resolved, err := filepath.EvalSymlinks(trustedTemp); err == nil {
			if relative, relErr := filepath.Rel(trustedTemp, path); relErr == nil {
				return filepath.Join(resolved, relative)
			}
		}
	}
	return path
}

func createScheduledSQLiteArchiveStateTemp(dirFD int) (string, *os.File, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return "", nil, fmt.Errorf("generate SQLite archive scheduler state temp name: %w", err)
		}
		name := ".sqlite-archive-attempt-" + hex.EncodeToString(value[:])
		fd, err := unix.Openat(dirFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("create SQLite archive scheduler state temp file: %w", err)
		}
		file := os.NewFile(uintptr(fd), name)
		if file == nil {
			_ = unix.Close(fd)
			return "", nil, fmt.Errorf("open SQLite archive scheduler state temp descriptor")
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("create SQLite archive scheduler state temp file: name collision budget exhausted")
}

func rejectScheduledSQLiteArchiveStateLeaf(dirFD int) error {
	var stat unix.Stat_t
	err := unix.Fstatat(dirFD, scheduledSQLiteArchiveAttemptName, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect SQLite archive scheduler state: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("SQLite archive scheduler state is not a regular no-follow file")
	}
	return nil
}
