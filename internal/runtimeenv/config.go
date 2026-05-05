package runtimeenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

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
