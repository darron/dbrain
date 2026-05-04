package itemcategorize

import "strings"

func parseOllamaModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "ollama/"):
		return strings.TrimSpace(value[7:]), true
	case strings.HasPrefix(lower, "ollama:"):
		return strings.TrimSpace(value[7:]), true
	}
	return "", false
}

func parseOpenRouterModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "openrouter/"):
		return strings.TrimSpace(value[11:]), true
	case strings.HasPrefix(lower, "openrouter:"):
		return strings.TrimSpace(value[11:]), true
	}
	return "", false
}

func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
