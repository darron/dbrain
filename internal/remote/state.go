package remote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const StateLockName = "dbrain.lock"

var ErrAlreadyLocked = errors.New("state dir is already locked by another dbrain process")

func ResolveStateDir(path string) (string, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return abs, nil
}

func PrepareStateDir(path string) (string, error) {
	abs, err := ResolveStateDir(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("create state dir %s: %w", abs, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve state dir symlinks %s: %w", abs, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat state dir %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("state dir is not a directory: %s", resolved)
	}
	if info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("state dir %s must have permissions 0700, got %04o", resolved, info.Mode().Perm())
	}
	if err := checkCurrentUserOwns(info); err != nil {
		return "", err
	}
	return resolved, nil
}

func LooksLikeSyncedPath(path string) bool {
	lower := strings.ToLower(path)
	for _, marker := range []string{"icloud drive", "mobile documents", "dropbox", "onedrive", "google drive"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func expandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}
