//go:build windows

package semanticlock

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type semanticDirectoryAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func ensureLockRoot(cacheRoot string, databaseID string) error {
	parent, err := openExistingWindowsDirectoryNoFollow(cacheRoot)
	if err != nil {
		return fmt.Errorf("open semantic cache root without reparse points: %w", err)
	}
	defer func() {
		if parent != 0 {
			_ = windows.CloseHandle(parent)
		}
	}()

	for _, name := range []string{"semantic", databaseID, "locks"} {
		next, err := openSemanticWindowsDirectoryAt(
			parent,
			name,
			windows.FILE_OPEN_IF,
			windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.SYNCHRONIZE,
		)
		if err != nil {
			return fmt.Errorf("open semantic lock directory %s without reparse points: %w", name, err)
		}
		previous := parent
		parent = 0
		if err := windows.CloseHandle(previous); err != nil {
			_ = windows.CloseHandle(next)
			return fmt.Errorf("close semantic lock parent directory: %w", err)
		}
		parent = next
	}
	return nil
}

func openExistingWindowsDirectoryNoFollow(path string) (windows.Handle, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return 0, errors.New("semantic cache directory is outside its path anchor")
	}
	rootAccess := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	handle, err := openSemanticWindowsDirectoryAbsolute(anchor, rootAccess)
	if err != nil {
		return 0, err
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		access := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
		if index == len(parts)-1 {
			access = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.SYNCHRONIZE
		}
		next, openErr := openSemanticWindowsDirectoryAt(handle, part, windows.FILE_OPEN, access)
		_ = windows.CloseHandle(handle)
		if openErr != nil {
			return 0, openErr
		}
		handle = next
	}
	return handle, nil
}

func openSemanticWindowsDirectoryAbsolute(path string, access uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(semanticWindowsNTPath(path))
	if err != nil {
		return 0, fmt.Errorf("encode semantic lock directory path: %w", err)
	}
	return openSemanticWindowsDirectory(0, objectName, windows.FILE_OPEN, access)
}

func openSemanticWindowsDirectoryAt(
	parent windows.Handle,
	name string,
	disposition uint32,
	access uint32,
) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, fmt.Errorf("encode semantic lock directory name: %w", err)
	}
	return openSemanticWindowsDirectory(parent, objectName, disposition, access)
}

func openSemanticWindowsDirectory(
	parent windows.Handle,
	objectName *windows.NTUnicodeString,
	disposition uint32,
	access uint32,
) (windows.Handle, error) {
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
	options := uint32(
		windows.FILE_DIRECTORY_FILE |
			windows.FILE_OPEN_REPARSE_POINT |
			windows.FILE_SYNCHRONOUS_IO_NONALERT,
	)
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
		return 0, err
	}
	if err := rejectSemanticWindowsReparseHandle(handle); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func rejectSemanticWindowsReparseHandle(handle windows.Handle) error {
	var info semanticDirectoryAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return fmt.Errorf("inspect semantic lock directory reparse attributes: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("semantic lock directory is a reparse point")
	}
	return nil
}

func semanticWindowsNTPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\??\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\??\` + path
}
