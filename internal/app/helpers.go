package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const rootEnvVar = "DBRAIN_ROOT"
const configFileEnvVar = "DBRAIN_CONFIG_FILE"

func loadConfig(root string, configFile ...string) (config.Config, error) {
	selectedConfigFile := ""
	if len(configFile) > 0 {
		selectedConfigFile = strings.TrimSpace(configFile[0])
	}
	var cfg config.Config
	var err error
	switch {
	case selectedConfigFile != "":
		cfg, err = config.LoadConfigFile(selectedConfigFile)
	case strings.TrimSpace(root) != "":
		cfg, err = config.Load(root)
	case strings.TrimSpace(os.Getenv(configFileEnvVar)) != "":
		cfg, err = config.LoadConfigFile(os.Getenv(configFileEnvVar))
	default:
		root = strings.TrimSpace(os.Getenv(rootEnvVar))
		cfg, err = config.Load(root)
	}
	if err != nil {
		return config.Config{}, err
	}
	runtimeenv.RegisterConfigFile(cfg.RootDir, cfg.ConfigPath)
	if err := cfg.EnsureDirs(); err != nil {
		return config.Config{}, err
	}
	if err := cleanupLegacySummaryTempFiles(cfg); err != nil {
		return config.Config{}, err
	}
	runStartupPreflight(cfg)
	return cfg, nil
}

func cleanupLegacySummaryTempFiles(cfg config.Config) error {
	matches, err := filepath.Glob(filepath.Join(cfg.RootDir, "dbrain-summary-*.md"))
	if err != nil {
		return fmt.Errorf("find legacy summary temp files: %w", err)
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy summary temp file %s: %w", path, err)
		}
	}
	return nil
}

func newLogger(debug bool, stderr io.Writer) *slog.Logger {
	if !debug {
		return nil
	}
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func writeJSON(dst io.Writer, value interface{}) error {
	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
