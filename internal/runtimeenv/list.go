package runtimeenv

import (
	"os"
	"strings"
)

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
	if hasRegisteredConfigSnapshot(rootDir) {
		if value, ok := frozenEnvValue(rootDir, key); ok {
			return splitList(value)
		}
	} else {
		if value := loadEnvValueFromFiles(rootDir, key); value != "" {
			return splitList(value)
		}
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
