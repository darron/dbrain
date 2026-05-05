package mediadownload

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func shouldDownload(ref model.ItemMediaRef, cfg config.Config, force bool) bool {
	if force {
		return true
	}

	status := strings.TrimSpace(ref.DownloadStatus)
	switch status {
	case "", "pending", "error":
		return true
	case "downloaded":
		if strings.TrimSpace(ref.LocalPath) == "" {
			return true
		}
		if !ref.LocalPrunedAt.IsZero() && strings.TrimSpace(ref.ArchiveStatus) == "archived" {
			return false
		}
		fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
		_, err := os.Stat(fullPath)
		return err != nil
	default:
		return false
	}
}
