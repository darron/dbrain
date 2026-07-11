package youtubeimport

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

func cookiesFromBrowserArg(browser, profile string) string {
	browser = strings.TrimSpace(browser)
	if browser == "" {
		browser = "chrome"
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return browser
	}
	return browser + ":" + profile
}

func cookiesFromBrowserArgs(browser, profile string) []string {
	primary := cookiesFromBrowserArg(browser, profile)
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	add(primary)
	if strings.TrimSpace(profile) != "" {
		return out
	}

	for _, discovered := range discoverBrowserProfiles(browser) {
		add(browser + ":" + discovered)
	}
	for _, fallback := range []string{"Default", "Profile 1", "Profile 2", "Profile 3", "Profile 4", "Profile 5", "Profile 6"} {
		add(browser + ":" + fallback)
	}
	return out
}

func discoverBrowserProfiles(browser string) []string {
	baseDir := browserProfileBaseDir(browser)
	if baseDir == "" {
		return nil
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	profiles := make([]string, 0, 8)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "Default" || strings.HasPrefix(name, "Profile ") {
			profiles = append(profiles, name)
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i] == "Default" {
			return true
		}
		if profiles[j] == "Default" {
			return false
		}
		return profileSortKey(profiles[i]) < profileSortKey(profiles[j])
	})
	return profiles
}

func browserProfileBaseDir(browser string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "darwin":
		switch strings.ToLower(strings.TrimSpace(browser)) {
		case "chrome":
			return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
		case "chromium":
			return filepath.Join(home, "Library", "Application Support", "Chromium")
		case "brave":
			return filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser")
		case "edge":
			return filepath.Join(home, "Library", "Application Support", "Microsoft Edge")
		case "vivaldi":
			return filepath.Join(home, "Library", "Application Support", "Vivaldi")
		case "opera":
			return filepath.Join(home, "Library", "Application Support", "com.operasoftware.Opera")
		}
	case "linux":
		switch strings.ToLower(strings.TrimSpace(browser)) {
		case "chrome":
			return filepath.Join(home, ".config", "google-chrome")
		case "chromium":
			return filepath.Join(home, ".config", "chromium")
		case "brave":
			return filepath.Join(home, ".config", "BraveSoftware", "Brave-Browser")
		case "edge":
			return filepath.Join(home, ".config", "microsoft-edge")
		case "vivaldi":
			return filepath.Join(home, ".config", "vivaldi")
		case "opera":
			return filepath.Join(home, ".config", "opera")
		}
	}

	return ""
}

func profileSortKey(value string) int {
	if value == "Default" {
		return -1
	}
	trimmed := strings.TrimPrefix(value, "Profile ")
	number, err := strconv.Atoi(trimmed)
	if err != nil {
		return 1 << 30
	}
	return number
}
