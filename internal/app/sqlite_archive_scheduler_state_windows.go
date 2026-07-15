//go:build windows

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
	"unsafe"

	"github.com/darron/dbrain/internal/config"
	"golang.org/x/sys/windows"
)

const scheduledSQLiteArchiveAttemptName = "sqlite-archive-last-attempt"

type windowsSchedulerStateDir struct {
	handle windows.Handle
}

type windowsFileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

type windowsFileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func readScheduledSQLiteArchiveAttempt(cfg config.Config) (time.Time, error) {
	dir, err := openWindowsSchedulerStateDir(cfg, false)
	if err != nil {
		if isWindowsStateNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	defer func() { _ = windows.CloseHandle(dir.handle) }()
	file, err := openWindowsSchedulerStateFile(dir.handle, scheduledSQLiteArchiveAttemptName, windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE)
	if err != nil {
		if isWindowsStateNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	defer func() { _ = file.Close() }()
	return parseScheduledSQLiteArchiveAttemptWindows(file)
}

func writeScheduledSQLiteArchiveAttempt(cfg config.Config, attemptedAt time.Time) error {
	dir, err := openWindowsSchedulerStateDir(cfg, true)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(dir.handle) }()
	if err := inspectWindowsSchedulerStateLeaf(dir.handle); err != nil {
		return err
	}
	_, temp, err := createWindowsSchedulerStateTemp(dir.handle)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = deleteWindowsSchedulerStateHandle(windows.Handle(temp.Fd()))
		}
		_ = temp.Close()
	}()
	if _, err := io.WriteString(temp, attemptedAt.UTC().Format(time.RFC3339Nano)+"\n"); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := inspectWindowsSchedulerStateLeaf(dir.handle); err != nil {
		return err
	}
	if err := renameWindowsSchedulerStateHandle(windows.Handle(temp.Fd()), dir.handle, scheduledSQLiteArchiveAttemptName); err != nil {
		return err
	}
	renamed = true
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync replaced SQLite archive scheduler state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close SQLite archive scheduler state after replacement: %w", err)
	}
	return nil
}

func openWindowsSchedulerStateDir(cfg config.Config, create bool) (*windowsSchedulerStateDir, error) {
	path := filepath.Clean(filepath.Join(cfg.DataDir, "scheduler"))
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("SQLite archive scheduler state is outside its path anchor")
	}
	rootAccess := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if create && len(strings.Split(relative, string(filepath.Separator))) == 1 {
		rootAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.SYNCHRONIZE
	}
	handle, err := openWindowsSchedulerDirectoryAbsolute(anchor, rootAccess)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		disposition := uint32(windows.FILE_OPEN)
		createPart := create && index == len(parts)-1
		if createPart {
			disposition = windows.FILE_OPEN_IF
		}
		access := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
		if create && index == len(parts)-2 {
			access = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.SYNCHRONIZE
		}
		if index == len(parts)-1 {
			access = windows.FILE_GENERIC_READ | windows.SYNCHRONIZE
			if create {
				access |= windows.FILE_GENERIC_WRITE
			}
		}
		next, openErr := openWindowsSchedulerDirectoryAt(handle, part, disposition, access)
		_ = windows.CloseHandle(handle)
		if openErr != nil {
			return nil, openErr
		}
		handle = next
	}
	return &windowsSchedulerStateDir{handle: handle}, nil
}

func openWindowsSchedulerDirectoryAbsolute(path string, access uint32) (windows.Handle, error) {
	name, err := windows.NewNTUnicodeString(windowsSchedulerNTPath(path))
	if err != nil {
		return 0, err
	}
	return openWindowsSchedulerDirectory(0, name, windows.FILE_OPEN, access)
}

func openWindowsSchedulerDirectoryAt(parent windows.Handle, name string, disposition uint32, access uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	return openWindowsSchedulerDirectory(parent, objectName, disposition, access)
}

func openWindowsSchedulerDirectory(parent windows.Handle, name *windows.NTUnicodeString, disposition uint32, access uint32) (windows.Handle, error) {
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    name,
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
		return 0, fmt.Errorf("open SQLite archive scheduler state directory without reparse points: %w", err)
	}
	if err := rejectWindowsSchedulerReparseHandle(handle, "SQLite archive scheduler state directory"); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func openWindowsSchedulerStateFile(parent windows.Handle, name string, disposition uint32, access uint32) (*os.File, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
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
	options := uint32(windows.FILE_NON_DIRECTORY_FILE | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if disposition != windows.FILE_OPEN {
		options |= windows.FILE_WRITE_THROUGH
	}
	err = windows.NtCreateFile(
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
		return nil, fmt.Errorf("open SQLite archive scheduler state file without reparse points: %w", err)
	}
	if err := rejectWindowsSchedulerReparseHandle(handle, "SQLite archive scheduler state file"); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open SQLite archive scheduler state descriptor")
	}
	return file, nil
}

func inspectWindowsSchedulerStateLeaf(parent windows.Handle) error {
	file, err := openWindowsSchedulerStateFile(parent, scheduledSQLiteArchiveAttemptName, windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE)
	if isWindowsStateNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func createWindowsSchedulerStateTemp(parent windows.Handle) (string, *os.File, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return "", nil, fmt.Errorf("generate SQLite archive scheduler state temp name: %w", err)
		}
		name := ".sqlite-archive-attempt-" + hex.EncodeToString(value[:])
		file, err := openWindowsSchedulerStateFile(
			parent,
			name,
			windows.FILE_CREATE,
			windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE,
		)
		if errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("create SQLite archive scheduler state temp file: name collision budget exhausted")
}

func renameWindowsSchedulerStateHandle(file windows.Handle, parent windows.Handle, name string) error {
	nameUTF16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	nameBytes := len(nameUTF16)*2 - 2
	var layout windowsFileRenameInformation
	size := int(unsafe.Offsetof(layout.FileName)) + nameBytes
	buffer := make([]byte, size)
	info := (*windowsFileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.ReplaceIfExists = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.RootDirectory = parent
	info.FileNameLength = uint32(nameBytes)
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&info.FileName[0]))[:nameBytes/2:nameBytes/2], nameUTF16)
	var iosb windows.IO_STATUS_BLOCK
	if err := windows.NtSetInformationFile(file, &iosb, &buffer[0], uint32(size), windows.FileRenameInformation); err != nil {
		return fmt.Errorf("replace SQLite archive scheduler state: %w", err)
	}
	return nil
}

func deleteWindowsSchedulerStateHandle(file windows.Handle) error {
	value := byte(1)
	var iosb windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(file, &iosb, &value, 1, windows.FileDispositionInformation)
}

func rejectWindowsSchedulerReparseHandle(handle windows.Handle, label string) error {
	var info windowsFileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return fmt.Errorf("inspect %s attributes: %w", label, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s is a reparse point", label)
	}
	return nil
}

func windowsSchedulerNTPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, "\\\\") {
		return "\\??\\UNC\\" + strings.TrimPrefix(path, "\\\\")
	}
	return "\\??\\" + path
}

func isWindowsStateNotExist(err error) bool {
	return errors.Is(err, windows.STATUS_NO_SUCH_FILE) ||
		errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) ||
		errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND)
}

func parseScheduledSQLiteArchiveAttemptWindows(file *os.File) (time.Time, error) {
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
