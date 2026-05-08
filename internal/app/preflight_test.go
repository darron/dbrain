package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

func clearPreflightEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"GITHUB_TOKEN",
		"DBRAIN_CATEGORIZE_MODEL",
		"DBRAIN_OCR_MODEL",
		"DBRAIN_X_PHOTO_OCR_MODEL",
		"DBRAIN_OPENROUTER_API_KEY",
		"OPENROUTER_API_KEY",
		"DBRAIN_R2_BUCKET",
		"DBRAIN_ARCHIVE_BUCKET",
		"DBRAIN_S3_BUCKET",
		"DBRAIN_R2_ENDPOINT",
		"DBRAIN_S3_ENDPOINT",
		"DBRAIN_R2_PUBLIC_BASE_URL",
		"DBRAIN_MEDIA_PUBLIC_BASE_URL",
		"DBRAIN_R2_ACCESS_KEY_ID",
		"DBRAIN_S3_ACCESS_KEY_ID",
		"AWS_ACCESS_KEY_ID",
		"DBRAIN_R2_SECRET_ACCESS_KEY",
		"DBRAIN_S3_SECRET_ACCESS_KEY",
		"AWS_SECRET_ACCESS_KEY",
	} {
		t.Setenv(key, "")
	}
}

func TestPreflightRequireGitHubFailsWithoutToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	if err := preflightRequireGitHub(context.Background(), cfg); err == nil {
		t.Fatal("expected missing GitHub token preflight failure")
	}
}

func TestPreflightRequireGitHubReadsConfigSecret(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte("github:\n  token: test-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, configPath)

	if err := preflightRequireGitHub(context.Background(), cfg); err != nil {
		t.Fatalf("expected configured GitHub token to pass: %v", err)
	}
}

func TestPreflightRequireOpenRouterForDefaultCategorizeModel(t *testing.T) {
	t.Setenv("DBRAIN_CATEGORIZE_MODEL", "")
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	if err := preflightRequireOpenRouterForModel(context.Background(), cfg, ""); err == nil {
		t.Fatal("expected default OpenRouter categorization preflight failure")
	}
}

func TestPreflightOCRSkipsLocalModel(t *testing.T) {
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	if err := preflightOCRModel(context.Background(), cfg, "ollama/glm-ocr"); err != nil {
		t.Fatalf("expected local OCR model to skip OpenRouter preflight: %v", err)
	}
}

func TestPreflightRequireR2FailsWhenConfiguredWithoutCredentials(t *testing.T) {
	t.Setenv("DBRAIN_R2_BUCKET", "test-bucket")
	t.Setenv("DBRAIN_R2_ACCESS_KEY_ID", "")
	t.Setenv("DBRAIN_R2_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	if err := preflightRequireR2(context.Background(), cfg); err == nil {
		t.Fatal("expected missing R2 credentials preflight failure")
	}
}

func TestWarnMissingCategoriesEmitsWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "categories.yaml")
	oldStderr := os.Stderr
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writePipe
	t.Cleanup(func() {
		os.Stderr = oldStderr
		_ = readPipe.Close()
	})

	warnMissingCategories(config.Config{CategoriesPath: path})

	_ = writePipe.Close()
	out, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "preflight warning: categories.yaml not found") {
		t.Fatalf("expected missing categories warning, got %q", string(out))
	}
}

func TestSyncOptionsPreflightFailsWhenGitHubSelectedWithoutToken(t *testing.T) {
	clearPreflightEnv(t)
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	_, err := syncOptionsFromFlags(context.Background(), cfg, syncAllFlags{
		ocrModel:        "ollama/test-ocr",
		categorizeModel: "ollama/test-categorizer",
		summarize:       false,
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "GitHub import selected") {
		t.Fatalf("expected GitHub preflight failure, got %v", err)
	}
}

func TestSyncOptionsPreflightPassesSkippedSecretsAndLocalModels(t *testing.T) {
	clearPreflightEnv(t)
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	opts, err := syncOptionsFromFlags(context.Background(), cfg, syncAllFlags{
		skipGitHub:      true,
		ocrModel:        "ollama/test-ocr",
		categorizeModel: "ollama/test-categorizer",
		summarize:       false,
	}, nil, nil)
	if err != nil {
		t.Fatalf("expected skipped/local preflight to pass: %v", err)
	}
	if opts.GitHubEnabled {
		t.Fatal("expected GitHub stage to stay disabled")
	}
	if !opts.XPhotoOCREnabled {
		t.Fatal("expected local OCR stage to stay enabled")
	}
	if !opts.CategorizeEnabled {
		t.Fatal("expected local categorization stage to stay enabled")
	}
}

func TestSyncOptionsPreflightFailsWhenOpenRouterCategorizationSelectedWithoutKey(t *testing.T) {
	clearPreflightEnv(t)
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	_, err := syncOptionsFromFlags(context.Background(), cfg, syncAllFlags{
		skipGitHub:      true,
		skipXPhotoOCR:   true,
		categorizeModel: "openrouter/google/gemini-2.5-flash",
		summarize:       false,
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "OpenRouter model selected") {
		t.Fatalf("expected OpenRouter categorization preflight failure, got %v", err)
	}
}

func TestSyncOptionsPreflightFailsWhenR2ConfiguredWithoutCredentials(t *testing.T) {
	clearPreflightEnv(t)
	t.Setenv("DBRAIN_R2_BUCKET", "test-bucket")
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	runtimeenv.RegisterConfigFile(root, filepath.Join(root, "config.yaml"))

	_, err := syncOptionsFromFlags(context.Background(), cfg, syncAllFlags{
		skipGitHub:     true,
		skipXPhotoOCR:  true,
		skipCategorize: true,
		summarize:      false,
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "R2/S3 archive is configured") {
		t.Fatalf("expected R2 preflight failure, got %v", err)
	}
}
