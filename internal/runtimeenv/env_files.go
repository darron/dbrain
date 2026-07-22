package runtimeenv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnvSnapshot reads supported .envrc/.env assignments once. .envrc has
// precedence over .env, matching ordinary runtime lookup. Values remain raw;
// this function does not execute shell syntax or resolve secret references.
func LoadDotEnvSnapshot(ctx context.Context, rootDir string, maxBytes int64) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, fmt.Errorf("dotenv snapshot root is required")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("dotenv snapshot byte limit must be positive")
	}
	values := map[string]string{}
	remaining := maxBytes
	for _, name := range []string{".envrc", ".env"} {
		path := filepath.Join(rootDir, name)
		data, err := readBoundedRegularFile(ctx, path, remaining, "dotenv snapshot "+name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		remaining -= int64(len(data))
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			name, value, ok := strings.Cut(line, "=")
			name = strings.TrimSpace(name)
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if !ok || name == "" || value == "" {
				continue
			}
			if _, exists := values[name]; !exists {
				values[name] = value
			}
		}
	}
	return values, nil
}

func loadEnvValueFromFiles(rootDir string, key string) string {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(key) == "" {
		return ""
	}
	for _, name := range []string{".envrc", ".env"} {
		path := filepath.Join(rootDir, name)
		value, ok := loadEnvValue(path, key)
		if ok {
			return value
		}
	}
	return ""
}

func loadEnvValue(path string, key string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value == "" {
			return "", false
		}
		return value, true
	}

	return "", false
}
