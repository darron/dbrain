package applenotes

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errAttachmentTooLarge = errors.New("attachment too large")
var errAttachmentOutsideNotesContainer = errors.New("attachment outside Notes container")

func resolveAttachmentSourcePath(value, sourceContainer string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if filepath.IsAbs(value) {
		relative, err := filepath.Rel(sourceContainer, filepath.Clean(value))
		if err != nil {
			return "", false
		}
		return filepath.Clean(relative), true
	}
	return filepath.Clean(value), true
}

func copyAttachmentFile(sourceRoot *os.Root, rootEscapeErr error, sourcePath, tempDir, fileName string, maxBytes int64) (string, func(), error) {
	info, err := sourceRoot.Stat(sourcePath)
	if err != nil {
		return "", nil, attachmentRootOperationError(rootEscapeErr, sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("attachment source %s is not a regular file", sourcePath)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return "", nil, fmt.Errorf("%w: %s is %d bytes, limit %d", errAttachmentTooLarge, sourcePath, info.Size(), maxBytes)
	}

	in, err := sourceRoot.Open(sourcePath)
	if err != nil {
		return "", nil, attachmentRootOperationError(rootEscapeErr, sourcePath, err)
	}
	defer func() {
		_ = in.Close()
	}()
	openedInfo, err := in.Stat()
	if err != nil {
		return "", nil, err
	}
	if !openedInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("attachment source %s is not a regular file", sourcePath)
	}
	if maxBytes > 0 && openedInfo.Size() > maxBytes {
		return "", nil, fmt.Errorf("%w: %s is %d bytes, limit %d", errAttachmentTooLarge, sourcePath, openedInfo.Size(), maxBytes)
	}

	pattern := "attachment-*"
	if ext := filepath.Ext(fileName); ext != "" {
		pattern += ext
	} else if ext := filepath.Ext(sourcePath); ext != "" {
		pattern += ext
	}
	out, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		return "", nil, err
	}
	localPath := out.Name()
	cleanup := func() {
		_ = os.Remove(localPath)
	}
	var copied int64
	if maxBytes > 0 {
		copied, err = io.Copy(out, io.LimitReader(in, maxBytes+1))
	} else {
		copied, err = io.Copy(out, in)
	}
	if err != nil {
		_ = out.Close()
		cleanup()
		return "", nil, err
	}
	if maxBytes > 0 && copied > maxBytes {
		_ = out.Close()
		cleanup()
		return "", nil, fmt.Errorf("%w: %s grew beyond limit %d while copying", errAttachmentTooLarge, sourcePath, maxBytes)
	}
	if err := out.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	localInfo, statErr := os.Stat(localPath)
	if statErr == nil && os.SameFile(openedInfo, localInfo) {
		cleanup()
		return "", nil, fmt.Errorf("attachment copy %s aliases source %s", localPath, sourcePath)
	}
	return localPath, cleanup, nil
}

func attachmentRootEscapeError(sourceRoot *os.Root) error {
	// os.Root does not export its path-escape sentinel. Capture the sentinel
	// from a known rejected path so callers can distinguish containment errors
	// from ordinary filesystem failures without matching error text.
	_, err := sourceRoot.Stat("..")
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return nil
	}
	return pathErr.Err
}

func attachmentRootOperationError(rootEscapeErr error, sourcePath string, err error) error {
	if rootEscapeErr != nil && errors.Is(err, rootEscapeErr) {
		return fmt.Errorf("%w: %s: %v", errAttachmentOutsideNotesContainer, sourcePath, err)
	}
	return err
}
