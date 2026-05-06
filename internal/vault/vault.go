package vault

import (
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
)

func NoteRelativePath(sourceKind, year, externalID string) string {
	if year == "" {
		year = "unknown"
	}
	if externalID == "" {
		externalID = "unknown"
	}
	return filepath.ToSlash(filepath.Join("items", sourceKind, year, externalID+".md"))
}

func StatNote(cfg config.Config, relPath string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(cfg.VaultDir, relPath))
}
