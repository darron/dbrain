//go:build !windows

package semanticlock

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func ensureLockRoot(cacheRoot string, databaseID string) error {
	parent, err := openExistingDirectoryNoFollow(cacheRoot)
	if err != nil {
		return fmt.Errorf("open semantic cache root without symlinks: %w", err)
	}
	defer func() {
		if parent >= 0 {
			_ = unix.Close(parent)
		}
	}()

	for _, name := range []string{"semantic", databaseID, "locks"} {
		next, err := openOrCreateDirectoryAt(parent, name)
		if err != nil {
			return err
		}
		previous := parent
		parent = -1
		if err := unix.Close(previous); err != nil {
			_ = unix.Close(next)
			return fmt.Errorf("close semantic lock parent directory: %w", err)
		}
		parent = next
	}
	return nil
}

func openExistingDirectoryNoFollow(path string) (int, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return -1, errors.New("semantic cache directory is outside its path anchor")
	}
	fd, err := unix.Open(anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open semantic cache directory anchor: %w", err)
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, fmt.Errorf("open semantic cache directory component without symlinks: %w", openErr)
		}
		fd = next
	}
	return fd, nil
}

func openOrCreateDirectoryAt(parent int, name string) (int, error) {
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	next, err := unix.Openat(parent, name, flags, 0)
	if errors.Is(err, unix.ENOENT) {
		if mkdirErr := unix.Mkdirat(parent, name, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
			return -1, fmt.Errorf("create semantic lock directory %s: %w", name, mkdirErr)
		}
		next, err = unix.Openat(parent, name, flags, 0)
	}
	if err != nil {
		return -1, fmt.Errorf("open semantic lock directory %s without symlinks: %w", name, err)
	}
	return next, nil
}
