package noterepair

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

type Options struct {
	Items       bool
	Sources     bool
	MissingOnly bool
	Limit       int
}

type Stats struct {
	ItemsConsidered           int `json:"items_considered"`
	ItemsWritten              int `json:"items_written"`
	ItemsAlreadyCurrent       int `json:"items_already_current"`
	ItemsSkippedMissingOnly   int `json:"items_skipped_missing_only"`
	ItemsSkippedExisting      int `json:"items_skipped_existing"`
	SourcesConsidered         int `json:"sources_considered"`
	SourcesWritten            int `json:"sources_written"`
	SourcesAlreadyCurrent     int `json:"sources_already_current"`
	SourcesSkippedMissingOnly int `json:"sources_skipped_missing_only"`
	SourcesSkippedExisting    int `json:"sources_skipped_existing"`
	Errors                    int `json:"errors"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if !opts.Items && !opts.Sources {
		opts.Items = true
		opts.Sources = true
	}

	var stats Stats

	if opts.Items {
		items, err := st.ListAllItems(ctx, opts.Limit)
		if err != nil {
			return stats, err
		}
		for _, item := range items {
			stats.ItemsConsidered++
			shouldWrite, err := shouldWriteNote(cfg, item.NotePath, opts.MissingOnly)
			if err != nil {
				stats.Errors++
				continue
			}
			if !shouldWrite {
				stats.ItemsSkippedMissingOnly++
				stats.ItemsSkippedExisting++
				continue
			}
			changed, err := writeItemNote(cfg, item)
			if err != nil {
				stats.Errors++
				continue
			}
			if changed {
				stats.ItemsWritten++
			} else {
				stats.ItemsAlreadyCurrent++
			}
		}
	}

	if opts.Sources {
		sources, err := st.ListAllSources(ctx, opts.Limit)
		if err != nil {
			return stats, err
		}
		for _, source := range sources {
			stats.SourcesConsidered++
			shouldWrite, err := shouldWriteNote(cfg, source.NotePath, opts.MissingOnly)
			if err != nil {
				stats.Errors++
				continue
			}
			if !shouldWrite {
				stats.SourcesSkippedMissingOnly++
				stats.SourcesSkippedExisting++
				continue
			}
			backlinks, err := st.ListBacklinksForSource(ctx, source.ID)
			if err != nil {
				stats.Errors++
				continue
			}
			changed, err := writeSourceNote(cfg, source, backlinks)
			if err != nil {
				stats.Errors++
				continue
			}
			if changed {
				stats.SourcesWritten++
			} else {
				stats.SourcesAlreadyCurrent++
			}
		}
	}

	return stats, nil
}

func shouldWriteNote(cfg config.Config, relPath string, missingOnly bool) (bool, error) {
	if strings.TrimSpace(relPath) == "" {
		return false, fmt.Errorf("empty note path")
	}
	if !missingOnly {
		return true, nil
	}
	_, err := vault.StatNote(cfg, relPath)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func writeItemNote(cfg config.Config, item model.Item) (bool, error) {
	body, err := vault.RenderItem(item)
	if err != nil {
		return false, err
	}
	return writeNoteBody(cfg, item.NotePath, body)
}

func writeSourceNote(cfg config.Config, source model.SourceDocument, backlinks []model.SourceBacklink) (bool, error) {
	body, err := vault.RenderSource(source, backlinks)
	if err != nil {
		return false, err
	}
	return writeNoteBody(cfg, source.NotePath, body)
}

func writeNoteBody(cfg config.Config, relPath string, body string) (bool, error) {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return false, fmt.Errorf("create note dir: %w", err)
	}

	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read note %s: %w", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return false, fmt.Errorf("write note: %w", err)
	}
	return true, nil
}
