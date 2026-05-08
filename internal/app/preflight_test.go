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
