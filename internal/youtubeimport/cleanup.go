package youtubeimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func pruneHistorySignals(ctx context.Context, cfg config.Config, st *store.Store) (cleanupStats, error) {
	itemResult, err := st.DeleteItemsBySourceType(ctx, "youtube_history")
	if err != nil {
		return cleanupStats{}, err
	}
	if err := removeNoteFiles(cfg, itemResult.NotePaths); err != nil {
		return cleanupStats{}, err
	}

	sourceResult, err := st.DeleteOrphanSources(ctx, "youtube")
	if err != nil {
		return cleanupStats{}, err
	}
	if err := removeNoteFiles(cfg, sourceResult.NotePaths); err != nil {
		return cleanupStats{}, err
	}

	return cleanupStats{
		ItemsDeleted:   itemResult.Count,
		SourcesDeleted: sourceResult.Count,
	}, nil
}

func removeNoteFiles(cfg config.Config, notePaths []string) error {
	for _, notePath := range notePaths {
		notePath = strings.TrimSpace(notePath)
		if notePath == "" {
			continue
		}
		absolute := filepath.Join(cfg.VaultDir, filepath.FromSlash(notePath))
		if err := os.Remove(absolute); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove note %s: %w", absolute, err)
		}
	}
	return nil
}
