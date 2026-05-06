package mediaarchive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
)

func pruneLocalPathIfSafe(ctx context.Context, cfg config.Config, st *store.Store, localPath string, logger *slog.Logger) (bool, int64, error) {
	assets, err := st.ListMediaAssetsByLocalPath(ctx, localPath)
	if err != nil {
		return false, 0, err
	}
	if len(assets) == 0 {
		return false, 0, nil
	}
	for _, asset := range assets {
		if strings.TrimSpace(asset.ArchiveStatus) != model.MediaArchiveStatusArchived {
			debugLog(logger, "media local prune deferred", "local_path", localPath, "blocking_asset_id", asset.ID, "archive_status", asset.ArchiveStatus)
			return false, 0, nil
		}
	}

	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath))
	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, 0, fmt.Errorf("remove local media %s: %w", fullPath, err)
	}

	rows, err := st.MarkMediaLocalPrunedByPath(ctx, localPath, time.Now().UTC())
	if err != nil {
		return false, 0, err
	}
	return true, rows, nil
}

func refreshNotesForLocalPath(ctx context.Context, cfg config.Config, st *store.Store, localPath string, logger *slog.Logger) error {
	sourceKeys, err := st.ListItemSourceKeysByMediaLocalPath(ctx, localPath)
	if err != nil {
		return err
	}
	renderer := projection.NewRenderer(cfg, st)
	for _, sourceKey := range sourceKeys {
		item, err := renderer.RefreshItem(ctx, sourceKey)
		if err != nil {
			return fmt.Errorf("refresh item note %s for media prune: %w", sourceKey, err)
		}
		if item.NotePath == "" {
			continue
		}
		debugLog(logger, "media prune note refreshed", "source_key", sourceKey, "note_path", item.NotePath, "local_path", localPath)
	}
	return nil
}
