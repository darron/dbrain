package applenotes

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func firstHTTPURLValue(row map[string]any, names ...string) string {
	for _, name := range names {
		value := firstStringValue(row, name)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return value
		}
	}
	return ""
}

func firstFilePathValue(row map[string]any, names ...string) string {
	for _, name := range names {
		value := firstStringValue(row, name)
		if value == "" {
			continue
		}
		if filePath := filePathFromString(value); filePath != "" {
			return filePath
		}
	}
	return ""
}

func filePathFromString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ""
	}
	if strings.HasPrefix(lower, "file://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		path := strings.TrimSpace(parsed.Path)
		if path == "" {
			return ""
		}
		if unescaped, err := url.PathUnescape(path); err == nil {
			path = unescaped
		}
		return path
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if filepath.IsAbs(value) {
		return value
	}
	if strings.Contains(value, "/") || strings.Contains(value, `\`) {
		return value
	}
	return ""
}
