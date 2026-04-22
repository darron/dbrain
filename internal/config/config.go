package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	RootDir  string
	DataDir  string
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
		MediaDir: filepath.Join(absRoot, "vault", "media"),
		VaultDir: filepath.Join(absRoot, "vault"),
		DBPath:   filepath.Join(absRoot, "data", "brain.db"),
	}

	return cfg, nil
}

func (c Config) EnsureDirs() error {
	for _, dir := range []string{c.RootDir, c.DataDir, c.MediaDir, c.VaultDir, filepath.Join(c.VaultDir, "items")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}
