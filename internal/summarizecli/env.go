package summarizecli

import (
	"os"
	"strings"

	"github.com/darron/dbrain/internal/runtimeenv"
)

func resolveModelAndEnv(model string, env map[string]string) (string, map[string]string) {
	trimmedModel := strings.TrimSpace(model)
	ollamaModel, ok := parseOllamaModel(trimmedModel)
	if !ok {
		return trimmedModel, env
	}

	out := cloneEnv(env)
	if !hasEnvValue(out, "OPENAI_BASE_URL") {
		out["OPENAI_BASE_URL"] = ollamaBaseURLWithEnv(out)
	}
	if !hasEnvValue(out, "OPENAI_API_KEY") {
		out["OPENAI_API_KEY"] = ollamaAPIKeyWithEnv(out)
	}
	if !hasEnvValue(out, "OPENAI_USE_CHAT_COMPLETIONS") {
		out["OPENAI_USE_CHAT_COMPLETIONS"] = "1"
	}

	return "openai/" + ollamaModel, out
}

func ollamaBaseURLWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST", "OPENAI_BASE_URL")
	if value == "" {
		value = defaultOllamaBaseURL
	}
	return normalizeBaseURLWithV1(value)
}

func ollamaNativeBaseURLWithEnv(env map[string]string) string {
	return strings.TrimSuffix(ollamaBaseURLWithEnv(env), "/v1")
}

func ollamaAPIKeyWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY", "OPENAI_API_KEY")
	if value == "" {
		value = defaultOllamaAPIKey
	}
	return value
}

func openRouterBaseURLWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL")
	if value == "" {
		value = defaultOpenRouterBaseURL
	}
	return normalizeBaseURLWithPath(value, "/api/v1", defaultOpenRouterBaseURL)
}

func openRouterAPIKeyWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
}

func openRouterRefererWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER")
}

func openRouterTitleWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE")
}

func userAgentWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_USER_AGENT")
}

func summaryLanguageWithEnv(env map[string]string) string {
	if value := firstEnvValue(env, "DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE"); value != "" {
		return value
	}
	return defaultSummaryLanguage
}

func normalizeBaseURLWithV1(raw string) string {
	return normalizeBaseURLWithPath(raw, "/v1", defaultOllamaBaseURL)
}

func normalizeBaseURLWithPath(raw string, suffix string, fallback string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimRight(value, "/")
	if strings.HasSuffix(value, suffix) {
		return value
	}
	return value + suffix
}

func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}

func envWithRuntimeConfig(rootDir string, env map[string]string) map[string]string {
	if strings.TrimSpace(rootDir) == "" {
		return env
	}
	out := cloneEnv(env)
	for _, keys := range [][]string{
		{"DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"},
		{"DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"},
		{"OPENAI_BASE_URL"},
		{"OPENAI_API_KEY"},
		{"OPENAI_USE_CHAT_COMPLETIONS"},
		{"DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"},
		{"DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY"},
		{"DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"},
		{"DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"},
		{"DBRAIN_USER_AGENT"},
		{"DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE"},
	} {
		value := runtimeenv.FirstNonEmpty(rootDir, keys...)
		if value == "" {
			continue
		}
		for _, key := range keys {
			if strings.TrimSpace(out[key]) == "" {
				out[key] = value
				break
			}
		}
	}
	return out
}

func hasEnvValue(env map[string]string, key string) bool {
	if value := strings.TrimSpace(env[key]); value != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv(key)) != ""
}

func firstEnvValue(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
