package runtimeenv

import (
	"os"
	"strings"
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
	if !hasRegisteredConfigSnapshot(rootDir) {
		if value := loadEnvValueFromFiles(rootDir, key); value != "" {
			return value, true
		}
	}
	if value, ok := loadConfigValueOK(rootDir, key); ok && strings.TrimSpace(value) != "" {
		return value, true
	}
	return "", false
}
