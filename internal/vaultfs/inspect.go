package vaultfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type LogicalFileMetadata struct {
	Exists    bool  `json:"exists"`
	Regular   bool  `json:"regular"`
	SizeBytes int64 `json:"size_bytes"`
}

type LogicalFileError struct {
	Code string `json:"code"`
}

func (e *LogicalFileError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (r *Root) Inspect(name string) (LogicalFileMetadata, error) {
	trimmed := strings.TrimSpace(name)
	cleaned := filepath.Clean(filepath.FromSlash(trimmed))
	if trimmed == "" || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return LogicalFileMetadata{}, &LogicalFileError{Code: "outside_root"}
	}
	file, err := r.root.Open(cleaned)
	if err != nil {
		return LogicalFileMetadata{}, inspectLogicalError(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return LogicalFileMetadata{}, inspectLogicalError(err)
	}
	return LogicalFileMetadata{Exists: true, Regular: info.Mode().IsRegular(), SizeBytes: info.Size()}, nil
}

func inspectLogicalError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return &LogicalFileError{Code: "missing"}
	case strings.Contains(strings.ToLower(err.Error()), "symlink"),
		strings.Contains(strings.ToLower(err.Error()), "escapes"),
		strings.Contains(strings.ToLower(err.Error()), "outside root"):
		return &LogicalFileError{Code: "symlink_rejected"}
	default:
		return &LogicalFileError{Code: "unreadable"}
	}
}
