package mcpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/vaultfs"
)

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func secondsTimeout(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}

func readNote(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readVaultNote(vaultDir string, notePath string) (string, error) {
	root, err := vaultfs.Open(vaultDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(notePath)
	if err != nil {
		return "", noteReadError(notePath, err)
	}
	return string(data), nil
}

func noteReadError(notePath string, err error) error {
	reason := "failed"
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		reason = pathErr.Err.Error()
	}
	return fmt.Errorf("read note %s: %s", filepath.ToSlash(notePath), reason)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
