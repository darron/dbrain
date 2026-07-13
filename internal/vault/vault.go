package vault

import (
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/vaultfs"
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
	root, err := vaultfs.Open(cfg.VaultDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Stat(relPath)
}
