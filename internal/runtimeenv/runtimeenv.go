package runtimeenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func FirstNonEmpty(rootDir string, keys ...string) string {
	for _, key := range keys {
		if value, ok := Lookup(rootDir, key); ok {
			return value
		}
	}
	return ""
}

func Lookup(rootDir string, key string) (string, bool) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value, true
	}
	if value := loadEnvValueFromFiles(rootDir, key); value != "" {
		return value, true
	}
	if value, ok := loadConfigValueOK(rootDir, key); ok && strings.TrimSpace(value) != "" {
		return value, true
	}
	return "", false
}

func FirstBool(rootDir string, keys ...string) bool {
	for _, key := range keys {
		if parsed, ok := LookupBool(rootDir, key); ok {
			return parsed
		}
	}
	return false
}

func FirstBoolDefault(rootDir string, fallback bool, keys ...string) bool {
	for _, key := range keys {
		if parsed, ok := LookupBool(rootDir, key); ok {
			return parsed
		}
	}
	return fallback
}

func LookupBool(rootDir string, key string) (bool, bool) {
	value, ok := Lookup(rootDir, key)
	if !ok {
		return false, false
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		return parsed, true
	}
	switch strings.ToLower(value) {
	case "yes", "on":
		return true, true
	case "no", "off":
		return false, true
	default:
		return false, false
	}
}

func FirstList(rootDir string, keys ...string) []string {
	for _, key := range keys {
		if values := LookupList(rootDir, key); len(values) > 0 {
			return values
		}
	}
	return nil
}

func LookupList(rootDir string, key string) []string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return splitList(value)
	}
	if value := loadEnvValueFromFiles(rootDir, key); value != "" {
		return splitList(value)
	}
	values, ok := loadConfigList(rootDir, key)
	if !ok {
		return nil
	}
	return values
}

func ConfigList(rootDir string, key string) []string {
	values, _ := loadConfigList(rootDir, key)
	return values
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
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

func loadConfigValueOK(rootDir string, key string) (string, bool) {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(key) == "" {
		return "", false
	}

	cfg, ok := loadConfigFile(filepath.Join(rootDir, "config.yaml"))
	if !ok {
		cfg, ok = loadConfigFile(filepath.Join(rootDir, "config.yml"))
	}
	if !ok {
		return "", false
	}

	for _, path := range configValuePaths(key) {
		if value, ok := configPathValueOK(cfg, path); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

func loadConfigList(rootDir string, key string) ([]string, bool) {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(key) == "" {
		return nil, false
	}

	cfg, ok := loadConfigFile(filepath.Join(rootDir, "config.yaml"))
	if !ok {
		cfg, ok = loadConfigFile(filepath.Join(rootDir, "config.yml"))
	}
	if !ok {
		return nil, false
	}

	for _, path := range configValuePaths(key) {
		if values, ok := configPathList(cfg, path); ok {
			return values, true
		}
		if value, ok := configPathValueOK(cfg, path); ok && value != "" {
			return splitList(value), true
		}
	}
	return nil, false
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
	if short, ok := strings.CutPrefix(key, "DBRAIN_APPLE_NOTES_"); ok {
		paths = append(paths, []string{"apple_notes", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_SAFARI_TABS_"); ok {
		paths = append(paths, []string{"safari_tabs", strings.ToLower(strings.Trim(short, "_"))})
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

func configPathValueOK(cfg map[string]any, path []string) (string, bool) {
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return "", false
		}
		current = next
	}

	switch value := current.(type) {
	case string:
		return strings.TrimSpace(value), true
	case bool:
		return strconv.FormatBool(value), true
	case int:
		return strconv.Itoa(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	default:
		return "", false
	}
}

func configPathList(cfg map[string]any, path []string) ([]string, bool) {
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return nil, false
		}
		current = next
	}

	raw, ok := current.([]any)
	if !ok {
		return nil, false
	}
	values := make([]string, 0, len(raw))
	for _, entry := range raw {
		if value := strings.TrimSpace(fmt.Sprint(entry)); value != "" {
			values = append(values, value)
		}
	}
	return values, true
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
