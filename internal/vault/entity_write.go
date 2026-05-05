package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
)

func EntityIndexRelativePath() string {
	return filepath.ToSlash(filepath.Join("entities", "index.md"))
}

func WriteEntity(cfg config.Config, entity entities.Entity) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(entity.NotePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create entity note dir: %w", err)
	}

	body := RenderEntity(entity)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write entity note: %w", err)
	}
	return nil
}

func WriteEntityIndex(cfg config.Config, entitiesList []entities.Entity) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(EntityIndexRelativePath()))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create entity index dir: %w", err)
	}

	body := RenderEntityIndex(entitiesList)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write entity index: %w", err)
	}
	return nil
}
