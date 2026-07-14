//go:build windows

package audit

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsReportStoreFS struct {
	audit   windows.Handle
	reports windows.Handle
}

type windowsAuditFileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

type windowsAuditFileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func openReportStoreFS(logDir string) (reportStoreFS, error) {
	if strings.TrimSpace(logDir) == "" {
		return nil, fmt.Errorf("audit log directory is required")
	}
	logHandle, err := openWindowsAuditAbsolutePath(logDir)
	if err != nil {
		return nil, fmt.Errorf("open audit log directory without reparse points: %w", err)
	}
	auditHandle, err := openWindowsAuditDirectoryAt(logHandle, "audit", windows.FILE_OPEN_IF, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.SYNCHRONIZE)
	_ = windows.CloseHandle(logHandle)
	if err != nil {
		return nil, err
	}
	reportsHandle, err := openWindowsAuditDirectoryAt(auditHandle, "reports", windows.FILE_OPEN_IF, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.SYNCHRONIZE)
	if err != nil {
		_ = windows.CloseHandle(auditHandle)
		return nil, err
	}
	return &windowsReportStoreFS{audit: auditHandle, reports: reportsHandle}, nil
}

func (f *windowsReportStoreFS) AppendReport(name string, data []byte) error {
	if !reportFilePattern.MatchString(name) {
		return fmt.Errorf("invalid generated audit report name")
	}
	access := uint32(windows.FILE_APPEND_DATA | windows.FILE_READ_ATTRIBUTES | windows.FILE_WRITE_ATTRIBUTES | windows.SYNCHRONIZE)
	file, err := openWindowsAuditFile(f.reports, name, windows.FILE_OPEN_IF, access)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("audit report is not regular")
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func (f *windowsReportStoreFS) ReadReport(name string, size int64) ([]byte, error) {
	if !reportFilePattern.MatchString(name) || size < 0 || size > reportRetentionBytes+maxReportLineBytes {
		return nil, fmt.Errorf("invalid generated audit report")
	}
	file, err := openWindowsAuditFile(f.reports, name, windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	return readBoundedRegular(file, reportRetentionBytes+maxReportLineBytes)
}

func (f *windowsReportStoreFS) ListReports() ([]reportFileInfo, error) {
	process := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(process, f.reports, process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(duplicate), "audit-reports")
	if dir == nil {
		_ = windows.CloseHandle(duplicate)
		return nil, fmt.Errorf("open audit reports directory descriptor")
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]reportFileInfo, 0, len(entries))
	for _, entry := range entries {
		if !reportFilePattern.MatchString(entry.Name()) {
			continue
		}
		file, openErr := openWindowsAuditFile(f.reports, entry.Name(), windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE)
		if openErr != nil {
			continue
		}
		info, statErr := file.Stat()
		_ = file.Close()
		if statErr == nil && info.Mode().IsRegular() {
			out = append(out, reportFileInfo{Name: entry.Name(), Size: info.Size()})
		}
	}
	return out, nil
}

func (f *windowsReportStoreFS) RemoveReport(name string) error {
	if !reportFilePattern.MatchString(name) {
		return fmt.Errorf("invalid generated audit report name")
	}
	file, err := openWindowsAuditFile(f.reports, name, windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.DELETE|windows.SYNCHRONIZE)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return deleteWindowsAuditHandle(windows.Handle(file.Fd()))
}

func (f *windowsReportStoreFS) ReadState(limit int64) ([]byte, error) {
	file, err := openWindowsAuditFile(f.audit, "alert-state.json", windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE)
	if err != nil {
		if isWindowsAuditNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	return readBoundedRegular(file, limit)
}

func (f *windowsReportStoreFS) ReplaceState(data []byte) error {
	if err := inspectWindowsAuditLeaf(f.audit, "alert-state.json"); err != nil {
		return err
	}
	_, temp, err := createWindowsAuditTemp(f.audit)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = deleteWindowsAuditHandle(windows.Handle(temp.Fd()))
		}
		_ = temp.Close()
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := inspectWindowsAuditLeaf(f.audit, "alert-state.json"); err != nil {
		return err
	}
	if err := renameWindowsAuditHandle(windows.Handle(temp.Fd()), f.audit, "alert-state.json"); err != nil {
		return err
	}
	renamed = true
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync replaced audit alert state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close audit alert state after replacement: %w", err)
	}
	return nil
}

func openWindowsAuditAbsolutePath(path string) (windows.Handle, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	abs = filepath.Clean(abs)
	volume := filepath.VolumeName(abs)
	anchor := volume + string(filepath.Separator)
	relative, err := filepath.Rel(anchor, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return 0, fmt.Errorf("audit log directory is outside its path anchor")
	}
	rootAccess := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if relative == "." || relative == "" {
		rootAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.SYNCHRONIZE
	}
	handle, err := openWindowsAuditDirectoryAbsolute(anchor, rootAccess)
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
		next, openErr := openWindowsAuditDirectoryAt(handle, part, windows.FILE_OPEN, access)
		_ = windows.CloseHandle(handle)
		if openErr != nil {
			return 0, openErr
		}
		handle = next
	}
	return handle, nil
}

func openWindowsAuditDirectoryAbsolute(path string, access uint32) (windows.Handle, error) {
	name, err := windows.NewNTUnicodeString(windowsAuditNTPath(path))
	if err != nil {
		return 0, err
	}
	return openWindowsAuditDirectory(0, name, windows.FILE_OPEN, access)
}

func openWindowsAuditDirectoryAt(parent windows.Handle, name string, disposition uint32, access uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	return openWindowsAuditDirectory(parent, objectName, disposition, access)
}

func openWindowsAuditDirectory(parent windows.Handle, name *windows.NTUnicodeString, disposition uint32, access uint32) (windows.Handle, error) {
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    name,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	options := uint32(windows.FILE_DIRECTORY_FILE | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if disposition != windows.FILE_OPEN {
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
		return 0, fmt.Errorf("open private audit directory without reparse points: %w", err)
	}
	if err := rejectWindowsAuditReparseHandle(handle, "private audit directory"); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func openWindowsAuditFile(parent windows.Handle, name string, disposition uint32, access uint32) (*os.File, error) {
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
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
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
		return nil, fmt.Errorf("open private audit file without reparse points: %w", err)
	}
	if err := rejectWindowsAuditReparseHandle(handle, "private audit file"); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open private audit file descriptor")
	}
	return file, nil
}

func inspectWindowsAuditLeaf(parent windows.Handle, name string) error {
	file, err := openWindowsAuditFile(parent, name, windows.FILE_OPEN, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE)
	if isWindowsAuditNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private audit leaf is not regular")
	}
	return nil
}

func createWindowsAuditTemp(parent windows.Handle) (string, *os.File, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return "", nil, fmt.Errorf("generate audit alert state temp name: %w", err)
		}
		name := ".alert-state-" + hex.EncodeToString(value[:])
		file, err := openWindowsAuditFile(parent, name, windows.FILE_CREATE, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE)
		if errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("create audit alert state temp file: name collision budget exhausted")
}

func renameWindowsAuditHandle(file windows.Handle, parent windows.Handle, name string) error {
	nameUTF16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	nameBytes := len(nameUTF16)*2 - 2
	var layout windowsAuditFileRenameInformation
	size := int(unsafe.Offsetof(layout.FileName)) + nameBytes
	buffer := make([]byte, size)
	info := (*windowsAuditFileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.ReplaceIfExists = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.RootDirectory = parent
	info.FileNameLength = uint32(nameBytes)
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&info.FileName[0]))[:nameBytes/2:nameBytes/2], nameUTF16)
	var iosb windows.IO_STATUS_BLOCK
	if err := windows.NtSetInformationFile(file, &iosb, &buffer[0], uint32(size), windows.FileRenameInformation); err != nil {
		return fmt.Errorf("replace private audit alert state: %w", err)
	}
	return nil
}

func deleteWindowsAuditHandle(file windows.Handle) error {
	value := byte(1)
	var iosb windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(file, &iosb, &value, 1, windows.FileDispositionInformation)
}

func rejectWindowsAuditReparseHandle(handle windows.Handle, label string) error {
	var info windowsAuditFileAttributeTagInfo
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

func windowsAuditNTPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\??\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\??\` + path
}

func isWindowsAuditNotExist(err error) bool {
	return errors.Is(err, windows.STATUS_NO_SUCH_FILE) ||
		errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) ||
		errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND)
}
