package applenotes

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func valuesByColumn(columns []string, values []any) map[string]any {
	row := make(map[string]any, len(columns))
	for i, column := range columns {
		row[column] = normalizeDBValue(values[i])
	}
	return row
}

func normalizeDBValue(value any) any {
	switch v := value.(type) {
	case []byte:
		copied := make([]byte, len(v))
		copy(copied, v)
		return copied
	default:
		return v
	}
}

func firstStringValue(row map[string]any, names ...string) string {
	for _, name := range names {
		for key, value := range row {
			if !strings.EqualFold(key, name) {
				continue
			}
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case []byte:
				if text := strings.TrimSpace(string(typed)); text != "" {
					return text
				}
			case int64, float64, int:
				return fmt.Sprint(typed)
			}
		}
	}
	return ""
}

func int64Value(row map[string]any, name string) (int64, bool) {
	for key, value := range row {
		if !strings.EqualFold(key, name) {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed, true
		case int:
			return int64(typed), true
		case float64:
			return int64(typed), true
		case []byte:
			parsed, err := strconv.ParseInt(strings.TrimSpace(string(typed)), 10, 64)
			return parsed, err == nil
		case string:
			parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
			return parsed, err == nil
		}
	}
	return 0, false
}

func bytesValue(row map[string]any, name string) []byte {
	for key, value := range row {
		if !strings.EqualFold(key, name) {
			continue
		}
		switch typed := value.(type) {
		case []byte:
			copied := make([]byte, len(typed))
			copy(copied, typed)
			return copied
		case string:
			return []byte(typed)
		}
	}
	return nil
}

func boolValue(row map[string]any, names ...string) bool {
	for _, name := range names {
		if value, ok := int64Value(row, name); ok {
			return value != 0
		}
		if text := strings.ToLower(firstStringValue(row, name)); text != "" {
			return text == "true" || text == "yes" || text == "1"
		}
	}
	return false
}

func macTimeString(row map[string]any, names ...string) string {
	for _, name := range names {
		for key, value := range row {
			if !strings.EqualFold(key, name) {
				continue
			}
			seconds, ok := numericValue(value)
			if !ok || seconds == 0 {
				continue
			}
			// Apple absolute time starts at 2001-01-01T00:00:00Z.
			return time.Unix(int64(seconds)+978307200, 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case []byte:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(string(typed)), 64)
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func rawRowForJSON(row map[string]any) map[string]any {
	raw := make(map[string]any, len(row))
	for key, value := range row {
		if strings.EqualFold(key, "ZDATA") {
			if data, ok := value.([]byte); ok && len(data) > 0 {
				raw[key] = map[string]any{
					"bytes":         len(data),
					"base64_prefix": base64.StdEncoding.EncodeToString(data[:min(len(data), 12)]),
				}
			}
			continue
		}
		raw[key] = value
	}
	return raw
}
