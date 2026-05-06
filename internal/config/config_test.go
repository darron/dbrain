package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultUsesXDGLayout(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "xdg-config")
	dataHome := filepath.Join(home, "xdg-data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	configDir := filepath.Join(configHome, "dbrain")
	dataDir := filepath.Join(dataHome, "dbrain")
	want := map[string]string{
		"RootDir":        configDir,
		"ConfigDir":      configDir,
		"ConfigPath":     filepath.Join(configDir, "config.yaml"),
		"CategoriesPath": filepath.Join(configDir, "categories.yaml"),
		"DataDir":        dataDir,
		"TempDir":        filepath.Join(dataDir, "tmp"),
		"CacheDir":       filepath.Join(dataDir, "cache"),
		"LogDir":         filepath.Join(dataDir, "logs"),
		"VaultDir":       filepath.Join(dataDir, "vault"),
		"MediaDir":       filepath.Join(dataDir, "vault", "media"),
		"DBPath":         filepath.Join(dataDir, "brain.db"),
	}
	got := map[string]string{
		"RootDir":        cfg.RootDir,
		"ConfigDir":      cfg.ConfigDir,
		"ConfigPath":     cfg.ConfigPath,
		"CategoriesPath": cfg.CategoriesPath,
		"DataDir":        cfg.DataDir,
		"TempDir":        cfg.TempDir,
		"CacheDir":       cfg.CacheDir,
		"LogDir":         cfg.LogDir,
		"VaultDir":       cfg.VaultDir,
		"MediaDir":       cfg.MediaDir,
		"DBPath":         cfg.DBPath,
	}
	for name, wantValue := range want {
		if got[name] != wantValue {
			t.Fatalf("%s = %q, want %q", name, got[name], wantValue)
		}
	}
}

func TestLoadExplicitRootKeepsRepoLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := map[string]string{
		"RootDir":        root,
		"ConfigDir":      root,
		"ConfigPath":     filepath.Join(root, "config.yaml"),
		"CategoriesPath": filepath.Join(root, "categories.yaml"),
		"DataDir":        filepath.Join(root, "data"),
		"TempDir":        filepath.Join(root, "tmp"),
		"CacheDir":       filepath.Join(root, "cache"),
		"LogDir":         filepath.Join(root, "logs"),
		"VaultDir":       filepath.Join(root, "vault"),
		"MediaDir":       filepath.Join(root, "vault", "media"),
		"DBPath":         filepath.Join(root, "data", "brain.db"),
	}
	got := map[string]string{
		"RootDir":        cfg.RootDir,
		"ConfigDir":      cfg.ConfigDir,
		"ConfigPath":     cfg.ConfigPath,
		"CategoriesPath": cfg.CategoriesPath,
		"DataDir":        cfg.DataDir,
		"TempDir":        cfg.TempDir,
		"CacheDir":       cfg.CacheDir,
		"LogDir":         cfg.LogDir,
		"VaultDir":       cfg.VaultDir,
		"MediaDir":       cfg.MediaDir,
		"DBPath":         cfg.DBPath,
	}
	for name, wantValue := range want {
		if got[name] != wantValue {
			t.Fatalf("%s = %q, want %q", name, got[name], wantValue)
		}
	}
}

func TestLoadConfigFileUsesFileAndXDGDataLayout(t *testing.T) {
	home := t.TempDir()
	dataHome := filepath.Join(home, "xdg-data")
	configDir := filepath.Join(home, "configs", "dbrain")
	configFile := filepath.Join(configDir, "stable.yaml")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("tsnet:\n  hostname: dbrain\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)

	cfg, err := LoadConfigFile(configFile)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}

	dataDir := filepath.Join(dataHome, "dbrain")
	want := map[string]string{
		"RootDir":        configDir,
		"ConfigDir":      configDir,
		"ConfigPath":     configFile,
		"CategoriesPath": filepath.Join(configDir, "categories.yaml"),
		"DataDir":        dataDir,
		"TempDir":        filepath.Join(dataDir, "tmp"),
		"CacheDir":       filepath.Join(dataDir, "cache"),
		"LogDir":         filepath.Join(dataDir, "logs"),
		"VaultDir":       filepath.Join(dataDir, "vault"),
		"MediaDir":       filepath.Join(dataDir, "vault", "media"),
		"DBPath":         filepath.Join(dataDir, "brain.db"),
	}
	got := map[string]string{
		"RootDir":        cfg.RootDir,
		"ConfigDir":      cfg.ConfigDir,
		"ConfigPath":     cfg.ConfigPath,
		"CategoriesPath": cfg.CategoriesPath,
		"DataDir":        cfg.DataDir,
		"TempDir":        cfg.TempDir,
		"CacheDir":       cfg.CacheDir,
		"LogDir":         cfg.LogDir,
		"VaultDir":       cfg.VaultDir,
		"MediaDir":       cfg.MediaDir,
		"DBPath":         cfg.DBPath,
	}
	for name, wantValue := range want {
		if got[name] != wantValue {
			t.Fatalf("%s = %q, want %q", name, got[name], wantValue)
		}
	}
}

func TestEnsureDirsCreatesConfigAndDataDirs(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "xdg-config")
	dataHome := filepath.Join(home, "xdg-data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	for _, dir := range []string{cfg.ConfigDir, cfg.DataDir, cfg.TempDir, cfg.CacheDir, cfg.LogDir, cfg.VaultDir, cfg.MediaDir, filepath.Join(cfg.VaultDir, "items")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat(%s): %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}
}

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
