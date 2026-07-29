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

func tryAcquireFileLock(path string, mode Mode) (fileLock, error) {
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
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if mode == Exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if err := windows.LockFileEx(handle, flags, 0, 1, 0, &lock.overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyLocked, path)
		}
		return nil, fmt.Errorf("lock file %s: %w", path, err)
	}
	return lock, nil
}

func openWindowsLockParent(path string) (windows.Handle, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return 0, fmt.Errorf("lock directory is outside its path anchor")
	}
	parts := strings.Split(relative, string(filepath.Separator))
	rootAccess := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if len(parts) == 1 {
		rootAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.SYNCHRONIZE
	}
	handle, err := openWindowsLockDirectoryAbsolute(anchor, rootAccess)
	if err != nil {
		return 0, err
	}
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		final := index == len(parts)-1
		disposition := uint32(windows.FILE_OPEN)
		if final {
			disposition = windows.FILE_OPEN_IF
		}
		access := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
		if index == len(parts)-2 || final {
			access = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.SYNCHRONIZE
		}
		next, openErr := openWindowsLockDirectoryAt(handle, part, disposition, access)
		_ = windows.CloseHandle(handle)
		if openErr != nil {
			return 0, openErr
		}
		handle = next
	}
	return handle, nil
}

func openWindowsLockDirectoryAbsolute(path string, access uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(windowsNTPath(path))
	if err != nil {
		return 0, fmt.Errorf("encode lock directory path: %w", err)
	}
	return openWindowsLockDirectory(0, objectName, windows.FILE_OPEN, access)
}

func openWindowsLockDirectoryAt(parent windows.Handle, name string, disposition uint32, access uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, fmt.Errorf("encode lock directory name: %w", err)
	}
	return openWindowsLockDirectory(parent, objectName, disposition, access)
}

func openWindowsLockDirectory(parent windows.Handle, objectName *windows.NTUnicodeString, disposition uint32, access uint32) (windows.Handle, error) {
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
	options := uint32(windows.FILE_DIRECTORY_FILE | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if disposition == windows.FILE_OPEN_IF {
		options |= windows.FILE_WRITE_THROUGH
	}
	err := windows.NtCreateFile(
		&handle,
		access,
		attributes,
		&iosb,
		nil,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition,
		options,
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
		windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE,
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

func normalizeLockPath(path string) string {
	return filepath.Clean(path)
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

func (l *windowsFileLock) metadataFile() *os.File {
	if l == nil {
		return nil
	}
	return l.file
}

func removeLockedFile(lock fileLock, path string) error {
	windowsLock, ok := lock.(*windowsFileLock)
	if !ok || windowsLock.file == nil {
		return fmt.Errorf("remove lock file %s: invalid Windows lock handle", path)
	}
	deleteFile := byte(1)
	if err := windows.SetFileInformationByHandle(
		windows.Handle(windowsLock.file.Fd()),
		windows.FileDispositionInfo,
		&deleteFile,
		1,
	); err != nil {
		return fmt.Errorf("remove lock file %s: %w", path, err)
	}
	return nil
}
