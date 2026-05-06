package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func jsonResourceContents(uri string, payload interface{}) ([]map[string]string, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal resource payload: %w", err)
	}
	return []map[string]string{{
		"uri":      uri,
		"mimeType": "application/json",
		"text":     string(data),
	}}, nil
}

func resourceLookup(parsed *url.URL, queryKey string) (string, error) {
	raw := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if raw == "" {
		raw = strings.TrimSpace(parsed.Query().Get(queryKey))
	}
	if raw == "" {
		return "", fmt.Errorf("resource uri %q is missing %s", parsed.String(), queryKey)
	}
	value, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("decode resource lookup: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("resource uri %q resolved to an empty %s", parsed.String(), queryKey)
	}
	return value, nil
}

func intFromQuery(values url.Values, key string) int {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func boolFromQuery(values url.Values, key string) bool {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func boolPtrFromQuery(values url.Values, key string) *bool {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return nil
	}
	value := false
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		value = true
	}
	return &value
}

func firstQueryValue(values url.Values, key string) string {
	return strings.TrimSpace(values.Get(key))
}

func listFromQuery(values url.Values, key string) []string {
	rawValues := values[key]
	if len(rawValues) == 0 {
		return nil
	}
	out := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
