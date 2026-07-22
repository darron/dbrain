package youtubeimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
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
	root, err := vaultfs.Open(cfg.VaultDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, notePath := range notePaths {
		notePath = strings.TrimSpace(notePath)
		if notePath == "" {
			continue
		}
		if err := root.Remove(notePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove note %q: %w", notePath, err)
		}
	}
	return nil
}
