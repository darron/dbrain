//go:build unix

package vaultfs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func openRootNoFollow(path string) (*os.Root, error) {
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("temporary root is outside its confinement anchor")
	}
	fd, err := unix.Open(anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open temporary root anchor: %w", err)
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, fmt.Errorf("open temporary root without symlinks: %w", openErr)
		}
		fd = next
	}
	descriptor := fmt.Sprintf("/dev/fd/%d", fd)
	if runtime.GOOS == "linux" {
		descriptor = fmt.Sprintf("/proc/self/fd/%d", fd)
	}
	root, err := os.OpenRoot(descriptor)
	_ = unix.Close(fd)
	if err != nil {
		return nil, fmt.Errorf("open confined temporary root: %w", err)
	}
	return root, nil
}

func privateCreateFlags() int { return os.O_CREATE | os.O_EXCL | os.O_WRONLY | unix.O_NOFOLLOW }
func privateOpenFlags() int   { return os.O_RDONLY | unix.O_NOFOLLOW }

func availableBytes(root *os.Root) (uint64, error) {
	dir, err := root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("open private temporary directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return 0, fmt.Errorf("inspect temporary free space: %w", err)
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
