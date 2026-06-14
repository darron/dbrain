package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	RootDir        string
	ConfigDir      string
	ConfigPath     string
	CategoriesPath string
	DataDir        string
	TempDir        string
	CacheDir       string
	LogDir         string
	MediaDir       string
	VaultDir       string
	OKFDir         string
	DBPath         string
}

func Load(root string) (Config, error) {
	if strings.TrimSpace(root) == "" {
		return loadDefault()
	}
	return loadExplicitRoot(root)
}

func LoadConfigFile(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("config file path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config file: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return Config{}, fmt.Errorf("stat config file %s: %w", absPath, err)
	}
	if info.IsDir() {
		return Config{}, fmt.Errorf("config file %s is a directory", absPath)
	}
	configDir := filepath.Dir(absPath)

	dataBase, err := xdgBaseDir("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return Config{}, err
	}
	dataDir := filepath.Join(dataBase, "dbrain")
	vaultDir := filepath.Join(dataDir, "vault")
	okfDir := filepath.Join(dataDir, "okf", "current")

	return Config{
		RootDir:        configDir,
		ConfigDir:      configDir,
		ConfigPath:     absPath,
		CategoriesPath: filepath.Join(configDir, "categories.yaml"),
		DataDir:        dataDir,
		TempDir:        filepath.Join(dataDir, "tmp"),
		CacheDir:       filepath.Join(dataDir, "cache"),
		LogDir:         filepath.Join(dataDir, "logs"),
		MediaDir:       filepath.Join(vaultDir, "media"),
		VaultDir:       vaultDir,
		OKFDir:         okfDir,
		DBPath:         filepath.Join(dataDir, "brain.db"),
	}, nil
}

func loadExplicitRoot(root string) (Config, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve root: %w", err)
	}

	cfg := Config{
		RootDir:        absRoot,
		ConfigDir:      absRoot,
		ConfigPath:     filepath.Join(absRoot, "config.yaml"),
		CategoriesPath: filepath.Join(absRoot, "categories.yaml"),
		DataDir:        filepath.Join(absRoot, "data"),
		TempDir:        filepath.Join(absRoot, "tmp"),
		CacheDir:       filepath.Join(absRoot, "cache"),
		LogDir:         filepath.Join(absRoot, "logs"),
		MediaDir:       filepath.Join(absRoot, "vault", "media"),
		VaultDir:       filepath.Join(absRoot, "vault"),
		OKFDir:         filepath.Join(absRoot, "okf", "current"),
		DBPath:         filepath.Join(absRoot, "data", "brain.db"),
	}

	return cfg, nil
}

func loadDefault() (Config, error) {
	configBase, err := xdgBaseDir("XDG_CONFIG_HOME", filepath.Join(".config"))
	if err != nil {
		return Config{}, err
	}
	dataBase, err := xdgBaseDir("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return Config{}, err
	}

	configDir := filepath.Join(configBase, "dbrain")
	dataDir := filepath.Join(dataBase, "dbrain")
	vaultDir := filepath.Join(dataDir, "vault")
	okfDir := filepath.Join(dataDir, "okf", "current")

	return Config{
		RootDir:        configDir,
		ConfigDir:      configDir,
		ConfigPath:     filepath.Join(configDir, "config.yaml"),
		CategoriesPath: filepath.Join(configDir, "categories.yaml"),
		DataDir:        dataDir,
		TempDir:        filepath.Join(dataDir, "tmp"),
		CacheDir:       filepath.Join(dataDir, "cache"),
		LogDir:         filepath.Join(dataDir, "logs"),
		MediaDir:       filepath.Join(vaultDir, "media"),
		VaultDir:       vaultDir,
		OKFDir:         okfDir,
		DBPath:         filepath.Join(dataDir, "brain.db"),
	}, nil
}

func xdgBaseDir(envKey string, fallbackFromHome string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		abs, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", envKey, err)
		}
		return abs, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home directory: empty HOME")
	}
	return filepath.Join(home, fallbackFromHome), nil
}

func (c Config) EnsureDirs() error {
	for _, dir := range []string{
		c.ConfigDir,
		c.DataDir,
		c.TempDir,
		c.CacheDir,
		c.LogDir,
		c.VaultDir,
		c.MediaDir,
		c.OKFDir,
		filepath.Join(c.VaultDir, "items"),
	} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
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
