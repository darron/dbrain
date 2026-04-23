package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateTempUsesConfigTempDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	file, err := cfg.CreateTemp("dbrain-*.md")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rel, err := filepath.Rel(cfg.TempDir, path)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("expected temp file under %s, got %s", cfg.TempDir, path)
	}
}

func TestMkdirTempUsesConfigTempDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	dir, err := cfg.MkdirTemp("dbrain-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got %s", dir)
	}

	rel, err := filepath.Rel(cfg.TempDir, dir)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("expected temp dir under %s, got %s", cfg.TempDir, dir)
	}
}
