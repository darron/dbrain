package runtimeenv

import (
	"strconv"
	"strings"
)

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
