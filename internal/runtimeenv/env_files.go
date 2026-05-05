package runtimeenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

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
