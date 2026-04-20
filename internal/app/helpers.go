package app

import (
	"encoding/json"
	"io"
	"log/slog"

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
	return cfg, nil
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
