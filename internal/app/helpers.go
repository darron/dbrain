package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"dbrain/internal/config"
)

func loadConfig(root string) (config.Config, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return config.Config{}, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return config.Config{}, err
	}
	if err := cleanupLegacySummaryTempFiles(cfg); err != nil {
		return config.Config{}, err
	}
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
