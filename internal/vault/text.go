package vault

import (
	"encoding/json"
	"strings"
	"time"
)

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func sameNormalizedText(a, b string) bool {
	return normalizeBodyText(a) == normalizeBodyText(b)
}

func normalizeBodyText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.TrimSpace(value)
}

func decodeStringArray(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	var raw []interface{}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, err
	}

	result := make([]string, 0, len(raw))
	for _, entry := range raw {
		if asString, ok := entry.(string); ok && strings.TrimSpace(asString) != "" {
			result = append(result, asString)
		}
	}
	return result, nil
}
