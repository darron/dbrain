package summarizecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func PreferredCLIProvider() string {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		path := filepath.Join(home, ".summarize", "cli-state.json")
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			var state cliState
			if err := json.Unmarshal(data, &state); err == nil {
				if provider := strings.TrimSpace(state.LastSuccessfulProvider); provider != "" {
					return provider
				}
			}
		}
	}
	return "codex"
}

func ResolveCLIProvider(cli, model string) string {
	if strings.TrimSpace(model) != "" {
		return ""
	}
	if value := strings.TrimSpace(cli); value != "" {
		return value
	}
	return PreferredCLIProvider()
}

func parseOllamaModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	if value == "" {
		return "", false
	}

	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "ollama/"):
		resolved := strings.TrimSpace(value[len("ollama/"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "ollama:"):
		resolved := strings.TrimSpace(value[len("ollama:"):])
		return resolved, resolved != ""
	default:
		return "", false
	}
}

func parseOpenRouterModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	if value == "" {
		return "", false
	}

	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "openrouter/"):
		resolved := strings.TrimSpace(value[len("openrouter/"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "openrouter:"):
		resolved := strings.TrimSpace(value[len("openrouter:"):])
		return resolved, resolved != ""
	default:
		return "", false
	}
}

func promptWithLengthAndLanguageHints(prompt string, length string, language string) string {
	base := strings.TrimSpace(prompt)
	hints := make([]string, 0, 2)
	if hint := strings.TrimSpace(lengthHint(length)); hint != "" {
		hints = append(hints, hint)
	}
	if hint := strings.TrimSpace(languageHint(language)); hint != "" {
		hints = append(hints, hint)
	}
	hint := strings.Join(hints, "\n")
	switch {
	case base == "":
		return hint
	case hint == "":
		return base
	default:
		return base + "\n\n" + hint
	}
}

func languageHint(language string) string {
	value := strings.TrimSpace(language)
	if value == "" || strings.EqualFold(value, "auto") {
		return ""
	}
	return "Write the summary in this output language: " + value + "."
}

func lengthHint(length string) string {
	switch strings.ToLower(strings.TrimSpace(length)) {
	case "short":
		return "Target response length: short. Compress aggressively."
	case "long":
		return "Target response length: long. Preserve more detail while remaining meaningfully shorter than the source."
	case "medium", "":
		return "Target response length: medium. Keep the response meaningfully shorter than the source."
	default:
		return ""
	}
}
