package mcpserver

import (
	"fmt"
	"strconv"
	"strings"
)

func argumentString(args map[string]interface{}, key string) string {
	if len(args) == 0 {
		return ""
	}
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func argumentBool(args map[string]interface{}, key string) bool {
	if len(args) == 0 {
		return false
	}
	value, ok := args[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	default:
		return false
	}
}

func argumentInt(args map[string]interface{}, key string, fallback int) int {
	if len(args) == 0 {
		return fallback
	}
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case int:
		if typed > 0 {
			return typed
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func quotedCSV(raw string) string {
	parts := strings.Split(raw, ",")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			quoted = append(quoted, fmt.Sprintf("%q", part))
		}
	}
	return strings.Join(quoted, ", ")
}
