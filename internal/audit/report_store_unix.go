//go:build unix

package audit

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

type unixReportStoreFS struct {
	auditFD   int
	reportsFD int
}

func openReportStoreFS(logDir string) (reportStoreFS, error) {
	if logDir == "" {
		return nil, fmt.Errorf("audit log directory is required")
	}
	logFD, err := openAbsoluteDirNoFollow(logDir)
	if err != nil {
		return nil, fmt.Errorf("open audit log directory without symlinks: %w", err)
	}
	auditFD, err := openOrCreatePrivateDir(logFD, "audit")
	_ = unix.Close(logFD)
	if err != nil {
		return nil, err
	}
	reportsFD, err := openOrCreatePrivateDir(auditFD, "reports")
	if err != nil {
		_ = unix.Close(auditFD)
		return nil, err
	}
	return &unixReportStoreFS{auditFD: auditFD, reportsFD: reportsFD}, nil
}

func openAbsoluteDirNoFollow(path string) (int, error) {
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

func openOrCreatePrivateDir(parentFD int, name string) (int, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		if err := unix.Mkdirat(parentFD, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create private audit directory: %w", err)
		}
		if err := unix.Fsync(parentFD); err != nil {
			return -1, fmt.Errorf("sync private audit parent directory: %w", err)
		}
		fd, err = unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return -1, fmt.Errorf("open private audit directory without symlinks: %w", err)
	}
	if err := unix.Fchmod(fd, 0o700); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("enforce private audit directory mode: %w", err)
	}
	return fd, nil
}

func (f *unixReportStoreFS) AppendReport(name string, data []byte) error {
	if !reportFilePattern.MatchString(name) {
		return fmt.Errorf("invalid generated audit report name")
	}
	created := false
	var stat unix.Stat_t
	if err := unix.Fstatat(f.reportsFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		created = true
	} else if err != nil {
		return err
	} else if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit report leaf is not a regular file")
	}
	fd, err := unix.Openat(f.reportsFD, name, unix.O_APPEND|unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open audit report descriptor")
	}
	defer func() { _ = file.Close() }()
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit report descriptor is not regular")
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if created {
		return unix.Fsync(f.reportsFD)
	}
	return nil
}

func (f *unixReportStoreFS) ReadReport(name string, size int64) ([]byte, error) {
	if !reportFilePattern.MatchString(name) || size < 0 || size > reportRetentionBytes+maxReportLineBytes {
		return nil, fmt.Errorf("invalid generated audit report")
	}
	fd, err := unix.Openat(f.reportsFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open audit report descriptor")
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	return readBoundedRegular(file, reportRetentionBytes+maxReportLineBytes)
}

func (f *unixReportStoreFS) ListReports() ([]reportFileInfo, error) {
	dup, err := unix.Openat(f.reportsFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(dup), "audit-reports")
	if dir == nil {
		_ = unix.Close(dup)
		return nil, fmt.Errorf("open audit reports directory")
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]reportFileInfo, 0, len(entries))
	for _, entry := range entries {
		if !reportFilePattern.MatchString(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(f.reportsFD, entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
			continue
		}
		out = append(out, reportFileInfo{Name: entry.Name(), Size: stat.Size})
	}
	return out, nil
}

func (f *unixReportStoreFS) RemoveReport(name string) error {
	if !reportFilePattern.MatchString(name) {
		return fmt.Errorf("invalid generated audit report name")
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(f.reportsFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit report leaf is not regular")
	}
	if err := unix.Unlinkat(f.reportsFD, name, 0); err != nil {
		return err
	}
	return unix.Fsync(f.reportsFD)
}

func (f *unixReportStoreFS) ReadState(limit int64) ([]byte, error) {
	fd, err := unix.Openat(f.auditFD, "alert-state.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "alert-state.json")
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open audit alert state descriptor")
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	return readBoundedRegular(file, limit)
}

func (f *unixReportStoreFS) ReplaceState(data []byte) error {
	var existing unix.Stat_t
	if err := unix.Fstatat(f.auditFD, "alert-state.json", &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil && existing.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit alert state leaf is not regular")
	} else if err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	tempName := ".alert-state-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(f.auditFD, tempName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), tempName)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open audit alert state temp descriptor")
	}
	defer func() {
		_ = file.Close()
		_ = unix.Unlinkat(f.auditFD, tempName, 0)
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(f.auditFD, tempName, f.auditFD, "alert-state.json"); err != nil {
		return err
	}
	return unix.Fsync(f.auditFD)
}
