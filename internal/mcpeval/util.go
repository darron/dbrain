package mcpeval

import "strings"

func containsStringFold(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAnySourceKey(keys map[string]struct{}, candidates []string) bool {
	for _, candidate := range candidates {
		if _, ok := keys[candidate]; ok {
			return true
		}
	}
	return false
}
