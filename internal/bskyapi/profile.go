package bskyapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveChromeProfile(browser, profile string) (string, error) {
	if strings.TrimSpace(browser) == "" {
		browser = "chrome"
	}
	if !strings.EqualFold(strings.TrimSpace(browser), "chrome") {
		return "", fmt.Errorf("unsupported Bluesky browser %q; only chrome is supported", browser)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "linux":
		base = filepath.Join(home, ".config", "google-chrome")
	default:
		return "", fmt.Errorf("unsupported operating system %q for Chrome profile lookup", runtime.GOOS)
	}
	return resolveChromeProfileDir(base, profile)
}

func resolveChromeProfileDir(baseDir, profile string) (string, error) {
	baseDir = filepath.Clean(strings.TrimSpace(baseDir))
	profile = strings.TrimSpace(profile)
	if baseDir == "." || baseDir == "" {
		return "", errors.New("chrome profile base directory is empty")
	}

	var candidate string
	if filepath.IsAbs(profile) {
		candidate = filepath.Clean(profile)
	} else {
		if profile == "" {
			profile = "Default"
		}
		candidate = filepath.Clean(filepath.Join(baseDir, profile))
		relative, err := filepath.Rel(baseDir, candidate)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", errors.New("chrome profile must stay within the browser profile directory")
		}
	}

	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("stat Chrome profile %q: %w", profileName(candidate, baseDir), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("chrome profile %q is not a directory", profileName(candidate, baseDir))
	}
	return candidate, nil
}

func profileName(path, baseDir string) string {
	if relative, err := filepath.Rel(baseDir, path); err == nil && relative != "." {
		return relative
	}
	return path
}

func readBSKYStorageFromProfile(browser, profile string) ([]byte, error) {
	profileDir, err := resolveChromeProfile(browser, profile)
	if err != nil {
		return nil, err
	}
	levelDBDir := filepath.Join(profileDir, "Local Storage", "leveldb")
	snapshot, err := snapshotLevelDB(levelDBDir)
	if err != nil {
		return nil, err
	}
	defer removeSnapshot(snapshot)
	return readBSKYStorageFromLevelDB(snapshot)
}
