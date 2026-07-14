package vault

import (
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
)

func EntityIndexRelativePath() string {
	return filepath.ToSlash(filepath.Join("entities", "index.md"))
}

func WriteEntity(cfg config.Config, entity entities.Entity) error {
	body := RenderEntity(entity)
	return writeRenderedNote(cfg, entity.NotePath, body, "entity note")
}

func WriteEntityIndex(cfg config.Config, entitiesList []entities.Entity) error {
	body := RenderEntityIndex(entitiesList)
	return writeRenderedNote(cfg, EntityIndexRelativePath(), body, "entity index")
}
