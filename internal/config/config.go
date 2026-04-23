package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	RootDir  string
	DataDir  string
	TempDir  string
	MediaDir string
	VaultDir string
	DBPath   string
}

func Load(root string) (Config, error) {
	if root == "" {
		root = "."
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve root: %w", err)
	}

	cfg := Config{
		RootDir:  absRoot,
		DataDir:  filepath.Join(absRoot, "data"),
		TempDir:  filepath.Join(absRoot, "tmp"),
		MediaDir: filepath.Join(absRoot, "vault", "media"),
		VaultDir: filepath.Join(absRoot, "vault"),
		DBPath:   filepath.Join(absRoot, "data", "brain.db"),
	}

	return cfg, nil
}

func (c Config) EnsureDirs() error {
	for _, dir := range []string{c.RootDir, c.DataDir, c.TempDir, c.MediaDir, c.VaultDir, filepath.Join(c.VaultDir, "items")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

func (c Config) CreateTemp(pattern string) (*os.File, error) {
	if err := os.MkdirAll(c.TempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temp dir %s: %w", c.TempDir, err)
	}
	file, err := os.CreateTemp(c.TempDir, pattern)
	if err != nil {
		return nil, fmt.Errorf("create temp file in %s: %w", c.TempDir, err)
	}
	return file, nil
}

func (c Config) MkdirTemp(pattern string) (string, error) {
	if err := os.MkdirAll(c.TempDir, 0o755); err != nil {
		return "", fmt.Errorf("create temp dir %s: %w", c.TempDir, err)
	}
	dir, err := os.MkdirTemp(c.TempDir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp dir in %s: %w", c.TempDir, err)
	}
	return dir, nil
}
