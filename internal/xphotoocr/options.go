package xphotoocr

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

func stripOpenRouterPrefix(model string) string {
	model = strings.TrimSpace(model)
	return strings.TrimPrefix(model, "openrouter/")
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

func parseFrankenOCRModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	if value == "" {
		return "", false
	}
	lower := strings.ToLower(value)
	switch {
	case lower == "focr" || lower == "franken_ocr":
		return "default", true
	case strings.HasPrefix(lower, "focr/"):
		resolved := strings.TrimSpace(value[len("focr/"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "focr:"):
		resolved := strings.TrimSpace(value[len("focr:"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "franken_ocr/"):
		resolved := strings.TrimSpace(value[len("franken_ocr/"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "franken_ocr:"):
		resolved := strings.TrimSpace(value[len("franken_ocr:"):])
		return resolved, resolved != ""
	default:
		return "", false
	}
}

func ResolveModel(cfg config.Config, model string) string {
	if strings.TrimSpace(model) != "" {
		return strings.TrimSpace(model)
	}
	return firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OCR_MODEL", "DBRAIN_X_PHOTO_OCR_MODEL"), defaultOCRModel)
}

func resolveOptions(ctx context.Context, cfg config.Config, opts Options) (Options, error) {
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = ResolveModel(cfg, "")
	}
	if strings.TrimSpace(opts.TesseractBinary) == "" {
		opts.TesseractBinary = "tesseract"
	}
	if strings.TrimSpace(opts.FOCRBinary) == "" {
		opts.FOCRBinary = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_FOCR_BINARY"), "focr")
	}
	if strings.TrimSpace(opts.OpenRouterBase) == "" {
		opts.OpenRouterBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"), defaultOpenRouterBaseURL)
	}
	if _, ok := parseOpenRouterModel(opts.Model); ok && strings.TrimSpace(opts.OpenRouterKey) == "" {
		value, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
		if err != nil {
			return Options{}, err
		}
		opts.OpenRouterKey = value
	}
	if strings.TrimSpace(opts.OpenRouterTitle) == "" {
		opts.OpenRouterTitle = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"), "dbrain X photo OCR")
	}
	if strings.TrimSpace(opts.OpenRouterRef) == "" {
		opts.OpenRouterRef = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"), "https://local.dbrain")
	}
	if strings.TrimSpace(opts.UserAgent) == "" {
		opts.UserAgent = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_USER_AGENT")
	}
	if strings.TrimSpace(opts.OllamaBase) == "" {
		opts.OllamaBase = ollamaBaseURL(cfg.RootDir)
	}
	if _, ok := parseOllamaModel(opts.Model); ok && strings.TrimSpace(opts.OllamaKey) == "" {
		value, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY")
		if err != nil {
			return Options{}, err
		}
		opts.OllamaKey = firstNonEmpty(value, defaultOllamaAPIKey)
	}
	return opts, nil
}

func ollamaBaseURL(rootDir string) string {
	value := runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST")
	if value == "" {
		value = defaultOllamaBaseURL
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimRight(value, "/")
	value = strings.TrimSuffix(value, "/v1")
	value = strings.TrimSuffix(value, "/api")
	return value
}
