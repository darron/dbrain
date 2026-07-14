//go:build windows

package runlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func acquireFileLock(path string, metadata string) (fileLock, error) {
	parent, err := openWindowsLockParent(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(parent) }()

	file, err := openWindowsLockFileAt(parent, filepath.Base(path), path)
	if err != nil {
		return nil, err
	}
	lock := &windowsFileLock{file: file}
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyLocked, path)
		}
		return nil, fmt.Errorf("lock file %s: %w", path, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, &lock.overlapped)
		_ = file.Close()
		return nil, fmt.Errorf("truncate lock file %s: %w", path, err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, &lock.overlapped)
		_ = file.Close()
		return nil, fmt.Errorf("seek lock file %s: %w", path, err)
	}
	if metadata != "" {
		if _, err := file.WriteString(metadata); err != nil {
			_ = windows.UnlockFileEx(handle, 0, 1, 0, &lock.overlapped)
			_ = file.Close()
			return nil, fmt.Errorf("write lock file %s: %w", path, err)
		}
	}
	return lock, nil
}

func openWindowsLockParent(path string) (windows.Handle, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return 0, fmt.Errorf("create lock dir: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return 0, fmt.Errorf("inspect lock dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return 0, fmt.Errorf("lock directory is not a regular no-follow directory")
	}
	objectName, err := windows.NewNTUnicodeString(windowsNTPath(path))
	if err != nil {
		return 0, fmt.Errorf("encode lock directory path: %w", err)
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:     uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var (
		handle windows.Handle
		iosb   windows.IO_STATUS_BLOCK
	)
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_GENERIC_READ|windows.SYNCHRONIZE,
		attributes,
		&iosb,
		nil,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("open lock directory without symlinks: %w", err)
	}
	if err := rejectWindowsReparseHandle(handle, "lock directory"); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func openWindowsLockFileAt(parent windows.Handle, name string, path string) (*os.File, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, fmt.Errorf("encode lock file name: %w", err)
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var (
		handle windows.Handle
		iosb   windows.IO_STATUS_BLOCK
	)
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.SYNCHRONIZE,
		attributes,
		&iosb,
		nil,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := rejectWindowsReparseHandle(handle, "lock file"); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open lock file descriptor %s", path)
	}
	return file, nil
}

func rejectWindowsReparseHandle(handle windows.Handle, label string) error {
	var info fileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return fmt.Errorf("inspect %s reparse attributes: %w", label, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s is a reparse point", label)
	}
	return nil
}

func windowsNTPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, "\\\\") {
		return "\\??\\UNC\\" + strings.TrimPrefix(path, "\\\\")
	}
	return "\\??\\" + path
}

func (l *windowsFileLock) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	handle := windows.Handle(l.file.Fd())
	unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
