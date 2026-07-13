package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/vaultfs"
)

func writeRenderedNote(cfg config.Config, notePath string, body string, noteKind string) error {
	if err := os.MkdirAll(cfg.VaultDir, 0o755); err != nil {
		return fmt.Errorf("create vault root: %w", err)
	}
	root, err := vaultfs.Open(cfg.VaultDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	dir := filepath.Dir(filepath.FromSlash(notePath))
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s dir for %q: %w", noteKind, notePath, err)
	}
	existing, err := root.ReadFile(notePath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := root.WriteFile(notePath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s %q: %w", noteKind, notePath, err)
	}
	return nil
}
