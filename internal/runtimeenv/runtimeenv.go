package runtimeenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func FirstNonEmpty(rootDir string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
		if value := loadEnvValueFromFiles(rootDir, key); value != "" {
			return value
		}
		if value := loadConfigValue(rootDir, key); value != "" {
			return value
		}
	}
	return ""
}

func FirstBool(rootDir string, keys ...string) bool {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			if fileValue := loadEnvValueFromFiles(rootDir, key); fileValue != "" {
				value = fileValue
			}
		}
		if value == "" {
			if configValue := loadConfigValue(rootDir, key); configValue != "" {
				value = configValue
			}
		}
		if value == "" {
			continue
		}
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
		switch strings.ToLower(value) {
		case "yes", "on":
			return true
		case "no", "off":
			return false
		}
	}
	return false
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

func loadConfigValue(rootDir string, key string) string {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(key) == "" {
		return ""
	}

	cfg, ok := loadConfigFile(filepath.Join(rootDir, "config.yaml"))
	if !ok {
		cfg, ok = loadConfigFile(filepath.Join(rootDir, "config.yml"))
	}
	if !ok {
		return ""
	}

	for _, path := range configValuePaths(key) {
		if value := configPathValue(cfg, path); value != "" {
			return value
		}
	}
	return ""
}

func loadConfigFile(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false
	}
	if len(cfg) == 0 {
		return nil, false
	}
	return cfg, true
}

func configValuePaths(key string) [][]string {
	key = strings.TrimSpace(key)
	lowerKey := strings.ToLower(key)
	paths := [][]string{
		{key},
		{"env", key},
		{lowerKey},
		{"env", lowerKey},
	}
	if strings.EqualFold(key, "DBRAIN_USER_AGENT") {
		paths = append(paths, []string{"http", "user_agent"})
	}

	for _, prefix := range []string{"DBRAIN_", "OPENROUTER_", "OLLAMA_", "SUMMARIZE_", "AWS_"} {
		if short, ok := strings.CutPrefix(key, prefix); ok {
			paths = append(paths, shortKeyPaths(short)...)
		}
	}
	paths = append(paths, shortKeyPaths(key)...)
	return paths
}

func shortKeyPaths(key string) [][]string {
	key = strings.ToLower(strings.Trim(key, "_"))
	if key == "" {
		return nil
	}

	paths := [][]string{{key}}
	parts := strings.Split(key, "_")
	if len(parts) > 1 {
		paths = append(paths,
			[]string{parts[0], strings.Join(parts[1:], "_")},
		)
	}
	if len(parts) > 2 {
		paths = append(paths, []string{parts[0], parts[1], strings.Join(parts[2:], "_")})
	}
	if len(parts) > 1 {
		paths = append(paths, parts)
	}
	return paths
}

func configPathValue(cfg map[string]any, path []string) string {
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return ""
		}
		current = next
	}

	switch value := current.(type) {
	case string:
		return strings.TrimSpace(value)
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func lookupMapValue(m map[string]any, key string) (any, bool) {
	if value, ok := m[key]; ok {
		return value, true
	}
	lowerKey := strings.ToLower(key)
	for k, value := range m {
		if strings.ToLower(k) == lowerKey {
			return value, true
		}
	}
	return nil, false
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
