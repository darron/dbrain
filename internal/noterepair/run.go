package noterepair

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"dbrain/internal/config"
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
	ItemsConsidered        int `json:"items_considered"`
	ItemsWritten           int `json:"items_written"`
	ItemsSkippedExisting   int `json:"items_skipped_existing"`
	SourcesConsidered      int `json:"sources_considered"`
	SourcesWritten         int `json:"sources_written"`
	SourcesSkippedExisting int `json:"sources_skipped_existing"`
	Errors                 int `json:"errors"`
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
				stats.ItemsSkippedExisting++
				continue
			}
			if err := vault.WriteItem(cfg, item); err != nil {
				stats.Errors++
				continue
			}
			stats.ItemsWritten++
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
				stats.SourcesSkippedExisting++
				continue
			}
			backlinks, err := st.ListBacklinksForSource(ctx, source.ID)
			if err != nil {
				stats.Errors++
				continue
			}
			if err := vault.WriteSource(cfg, source, backlinks); err != nil {
				stats.Errors++
				continue
			}
			stats.SourcesWritten++
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
