package syncjob

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/store"
)

func executeCategorizeStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*CategorizeStage, error) {
	progressf(opts.Progress, "==> categorize items and sources\n")
	start := time.Now()
	var itemProcessed atomic.Int64
	var itemErrors atomic.Int64
	var itemTotal atomic.Int64
	itemStats, _, err := runItemCategorize(ctx, cfg, st, itemcategorize.Options{
		Model:           opts.CategorizeModel,
		Timeout:         opts.CategorizeTimeout,
		Concurrency:     opts.CategorizeConcurrency,
		Limit:           opts.CategorizeLimit,
		Force:           opts.Force,
		Apply:           true,
		IncludeImages:   opts.CategorizeImages,
		S3Endpoint:      opts.ArchiveEndpoint,
		S3Region:        opts.ArchiveRegion,
		S3AccessKey:     opts.ArchiveAccessKeyID,
		S3SecretKey:     opts.ArchiveSecretKey,
		OpenRouterTitle: "dbrain sync categorize",
		OnStart: func(total int) {
			itemTotal.Store(int64(total))
			if opts.Logger != nil {
				opts.Logger.Debug("item categorization candidates loaded",
					"processed", 0,
					"total", total,
					"remaining", total,
					"items", total,
					"limit", opts.CategorizeLimit,
					"force", opts.Force,
					"concurrency", opts.CategorizeConcurrency,
				)
			}
		},
		OnResult: func(ir itemcategorize.ItemResult) {
			processed := itemProcessed.Add(1)
			total := itemTotal.Load()
			remaining := total - processed
			if remaining < 0 {
				remaining = 0
			}
			if opts.Logger == nil {
				return
			}
			if ir.Error != "" {
				errors := itemErrors.Add(1)
				opts.Logger.Debug("item categorization failed",
					"source_key", ir.Item.SourceKey,
					"item_id", ir.Item.ID,
					"processed", processed,
					"total", total,
					"remaining", remaining,
					"errors", errors,
					"error", ir.Error,
				)
				return
			}
			opts.Logger.Debug("item categorized",
				"source_key", ir.Item.SourceKey,
				"item_id", ir.Item.ID,
				"processed", processed,
				"total", total,
				"remaining", remaining,
				"errors", itemErrors.Load(),
				"tags", strings.Join(ir.Result.Tags, ","),
				"categories", strings.Join(ir.Result.Categories, ","),
			)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("categorize items: %w", err)
	}

	var sourceProcessed atomic.Int64
	var sourceErrors atomic.Int64
	var sourceTotal atomic.Int64
	sourceStats, _, err := runSourceCategorize(ctx, cfg, st, itemcategorize.Options{
		Model:           opts.CategorizeModel,
		Timeout:         opts.CategorizeTimeout,
		Concurrency:     opts.CategorizeConcurrency,
		Limit:           opts.CategorizeLimit,
		Force:           opts.Force,
		Apply:           true,
		OpenRouterTitle: "dbrain sync categorize",
		OnStart: func(total int) {
			sourceTotal.Store(int64(total))
			if opts.Logger != nil {
				opts.Logger.Debug("source categorization candidates loaded",
					"processed", 0,
					"total", total,
					"remaining", total,
					"sources", total,
					"limit", opts.CategorizeLimit,
					"force", opts.Force,
					"concurrency", opts.CategorizeConcurrency,
				)
			}
		},
		OnSourceResult: func(sr itemcategorize.SourceResult) {
			processed := sourceProcessed.Add(1)
			total := sourceTotal.Load()
			remaining := total - processed
			if remaining < 0 {
				remaining = 0
			}
			if opts.Logger == nil {
				return
			}
			if sr.Error != "" {
				errors := sourceErrors.Add(1)
				opts.Logger.Debug("source categorization failed",
					"source_key", sr.Source.SourceKey,
					"source_id", sr.Source.ID,
					"processed", processed,
					"total", total,
					"remaining", remaining,
					"errors", errors,
					"error", sr.Error,
				)
				return
			}
			opts.Logger.Debug("source categorized",
				"source_key", sr.Source.SourceKey,
				"source_id", sr.Source.ID,
				"processed", processed,
				"total", total,
				"remaining", remaining,
				"errors", sourceErrors.Load(),
				"tags", strings.Join(sr.Result.Tags, ","),
				"categories", strings.Join(sr.Result.Categories, ","),
			)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("categorize sources: %w", err)
	}

	categorizeStats := mergeCategorizeStats(itemStats, sourceStats)
	stage := &CategorizeStage{
		Duration:    time.Since(start),
		Stats:       categorizeStats,
		ItemStats:   itemStats,
		SourceStats: sourceStats,
	}
	progressf(opts.Progress, "Categorization complete: item_queued=%d item_applied=%d source_queued=%d source_applied=%d succeeded=%d skipped=%d errors=%d (%s)\n", itemStats.Queued, itemStats.Applied, sourceStats.Queued, sourceStats.Applied, categorizeStats.Succeeded, categorizeStats.Skipped, categorizeStats.Errors, stage.Duration)
	return stage, nil
}
