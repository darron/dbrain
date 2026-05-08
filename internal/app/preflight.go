package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/darron/dbrain/internal/config"
)

var categoryWarningPaths sync.Map

func runStartupPreflight(cfg config.Config) {
	warnMissingCategories(cfg)
}

func warnMissingCategories(cfg config.Config) {
	path := strings.TrimSpace(cfg.CategoriesPath)
	if path == "" {
		return
	}
	if _, warned := categoryWarningPaths.LoadOrStore(path, true); warned {
		return
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() && info.Size() > 0 {
		return
	}
	if err == nil && info.IsDir() {
		_, _ = fmt.Fprintf(os.Stderr, "preflight warning: categories.yaml path is a directory: %s\n", path)
		return
	}
	if err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(os.Stderr, "preflight warning: cannot inspect categories.yaml at %s: %v\n", path, err)
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "preflight warning: categories.yaml not found at %s; categorization will run without canonical vocabulary rewrites\n", path)
}

func preflightRequireGitHub(ctx context.Context, cfg config.Config) error {
	token, err := firstNonEmptySecret(ctx, cfg.RootDir, "GITHUB_TOKEN")
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("preflight failed: GitHub import selected but GITHUB_TOKEN / github.token is not configured")
	}
	return nil
}

func preflightRequireR2(ctx context.Context, cfg config.Config) error {
	if !preflightR2Configured(cfg) {
		return nil
	}
	accessKeyID, err := firstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	if err != nil {
		return err
	}
	secretAccessKey, err := firstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	if err != nil {
		return err
	}
	if strings.TrimSpace(accessKeyID) == "" || strings.TrimSpace(secretAccessKey) == "" {
		return fmt.Errorf("preflight failed: R2/S3 archive is configured but access key or secret key is missing")
	}
	return nil
}

func preflightR2Configured(cfg config.Config) bool {
	return firstNonEmptyEnv(cfg.RootDir,
		"DBRAIN_R2_BUCKET",
		"DBRAIN_ARCHIVE_BUCKET",
		"DBRAIN_S3_BUCKET",
		"DBRAIN_R2_ENDPOINT",
		"DBRAIN_S3_ENDPOINT",
		"DBRAIN_R2_PUBLIC_BASE_URL",
		"DBRAIN_MEDIA_PUBLIC_BASE_URL",
	) != ""
}

func preflightRequireOpenRouterForModel(ctx context.Context, cfg config.Config, model string) error {
	if !preflightModelUsesOpenRouter(cfg, model) {
		return nil
	}
	key, err := firstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("preflight failed: OpenRouter model selected but DBRAIN_OPENROUTER_API_KEY / OPENROUTER_API_KEY / openrouter.api_key is not configured")
	}
	return nil
}

func preflightModelUsesOpenRouter(cfg config.Config, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		model = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_CATEGORIZE_MODEL")
		if model == "" {
			model = "openrouter/google/gemini-2.5-flash"
		}
	}
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "openrouter/") || strings.HasPrefix(lower, "openrouter:")
}

func preflightOCRModel(ctx context.Context, cfg config.Config, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		model = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_OCR_MODEL", "DBRAIN_X_PHOTO_OCR_MODEL")
		if model == "" {
			model = "openrouter/google/gemini-3.1-flash-lite-preview"
		}
	}
	return preflightRequireOpenRouterForModel(ctx, cfg, model)
}

func preflightSummaryModel(ctx context.Context, cfg config.Config, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		model = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_SUMMARY_MODEL", "SUMMARIZE_MODEL")
	}
	if model == "" {
		return nil
	}
	return preflightRequireOpenRouterForModel(ctx, cfg, model)
}
