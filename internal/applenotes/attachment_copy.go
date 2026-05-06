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

func resolveAttachmentSourcePath(value, sourceDBPath string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), true
	}
	if strings.TrimSpace(sourceDBPath) == "" {
		return "", false
	}
	base := filepath.Dir(sourceDBPath)
	return filepath.Clean(filepath.Join(base, value)), true
}

func copyAttachmentFile(sourcePath, tempDir, fileName string, maxBytes int64) (string, func(), error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("attachment source %s is not a regular file", sourcePath)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return "", nil, fmt.Errorf("%w: %s is %d bytes, limit %d", errAttachmentTooLarge, sourcePath, info.Size(), maxBytes)
	}

	in, err := os.Open(sourcePath)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		_ = in.Close()
	}()

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
	if sameFile(sourcePath, localPath) {
		cleanup()
		return "", nil, fmt.Errorf("attachment copy %s aliases source %s", localPath, sourcePath)
	}
	return localPath, cleanup, nil
}
