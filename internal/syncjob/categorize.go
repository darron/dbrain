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

func executeCategorizeStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*CategorizeStage, error) {
	common := opts.Common
	stageOpts := opts.Categorize
	archiveOpts := opts.Archive
	progressf(common.Progress, "==> categorize items and sources\n")
	start := time.Now()
	var itemProcessed atomic.Int64
	var itemErrors atomic.Int64
	var itemTotal atomic.Int64
	itemStats, _, err := runItemCategorize(ctx, cfg, st, itemcategorize.Options{
		Model:           stageOpts.Model,
		Timeout:         stageOpts.Timeout,
		Concurrency:     stageOpts.Concurrency,
		Limit:           stageOpts.Limit,
		Force:           common.Force,
		Apply:           true,
		IncludeImages:   stageOpts.Images,
		S3Endpoint:      archiveOpts.Endpoint,
		S3Region:        archiveOpts.Region,
		S3AccessKey:     archiveOpts.AccessKeyID,
		S3SecretKey:     archiveOpts.SecretKey,
		OpenRouterTitle: "dbrain sync categorize",
		Metrics:         common.Metrics,
		OnStart: func(total int) {
			itemTotal.Store(int64(total))
			if common.Logger != nil {
				common.Logger.Debug("item categorization candidates loaded",
					"processed", 0,
					"total", total,
					"remaining", total,
					"items", total,
					"limit", stageOpts.Limit,
					"force", common.Force,
					"concurrency", stageOpts.Concurrency,
				)
			}
		},
		OnResult: func(ir itemcategorize.ItemResult) {
			emitCategorizeItemMetric(common.Metrics, ir, stageOpts)
			processed := itemProcessed.Add(1)
			total := itemTotal.Load()
			remaining := total - processed
			if remaining < 0 {
				remaining = 0
			}
			if common.Logger == nil {
				return
			}
			if ir.Error != "" {
				errors := itemErrors.Add(1)
				common.Logger.Debug("item categorization failed",
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
			common.Logger.Debug("item categorized",
				"source_key", ir.Item.SourceKey,
				"item_id", ir.Item.ID,
				"processed", processed,
				"total", total,
				"remaining", remaining,
				"errors", itemErrors.Load(),
				"model", ir.Result.Model,
				"tags", strings.Join(ir.Result.Tags, ","),
				"categories", strings.Join(ir.Result.Categories, ","),
			)
		},
	})
	if err != nil {
		categorizeStats := mergeCategorizeStats(itemStats, itemcategorize.Stats{})
		return &CategorizeStage{
			Duration:  time.Since(start),
			Stats:     categorizeStats,
			ItemStats: itemStats,
		}, fmt.Errorf("categorize items: %w", err)
	}

	var sourceProcessed atomic.Int64
	var sourceErrors atomic.Int64
	var sourceTotal atomic.Int64
	sourceStats, _, err := runSourceCategorize(ctx, cfg, st, itemcategorize.Options{
		Model:           stageOpts.Model,
		Timeout:         stageOpts.Timeout,
		Concurrency:     stageOpts.Concurrency,
		Limit:           stageOpts.Limit,
		Force:           common.Force,
		Apply:           true,
		OpenRouterTitle: "dbrain sync categorize",
		Metrics:         common.Metrics,
		OnStart: func(total int) {
			sourceTotal.Store(int64(total))
			if common.Logger != nil {
				common.Logger.Debug("source categorization candidates loaded",
					"processed", 0,
					"total", total,
					"remaining", total,
					"sources", total,
					"limit", stageOpts.Limit,
					"force", common.Force,
					"concurrency", stageOpts.Concurrency,
				)
			}
		},
		OnSourceResult: func(sr itemcategorize.SourceResult) {
			emitCategorizeSourceMetric(common.Metrics, sr, stageOpts)
			processed := sourceProcessed.Add(1)
			total := sourceTotal.Load()
			remaining := total - processed
			if remaining < 0 {
				remaining = 0
			}
			if common.Logger == nil {
				return
			}
			if sr.Error != "" {
				errors := sourceErrors.Add(1)
				common.Logger.Debug("source categorization failed",
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
			common.Logger.Debug("source categorized",
				"source_key", sr.Source.SourceKey,
				"source_id", sr.Source.ID,
				"processed", processed,
				"total", total,
				"remaining", remaining,
				"errors", sourceErrors.Load(),
				"model", sr.Result.Model,
				"tags", strings.Join(sr.Result.Tags, ","),
				"categories", strings.Join(sr.Result.Categories, ","),
			)
		},
	})
	if err != nil {
		categorizeStats := mergeCategorizeStats(itemStats, sourceStats)
		return &CategorizeStage{
			Duration:    time.Since(start),
			Stats:       categorizeStats,
			ItemStats:   itemStats,
			SourceStats: sourceStats,
		}, fmt.Errorf("categorize sources: %w", err)
	}

	categorizeStats := mergeCategorizeStats(itemStats, sourceStats)
	stage := &CategorizeStage{
		Duration:    time.Since(start),
		Stats:       categorizeStats,
		ItemStats:   itemStats,
		SourceStats: sourceStats,
	}
	progressf(common.Progress, "Categorization complete: item_queued=%d item_applied=%d source_queued=%d source_applied=%d succeeded=%d skipped=%d errors=%d (%s)\n", itemStats.Queued, itemStats.Applied, sourceStats.Queued, sourceStats.Applied, categorizeStats.Succeeded, categorizeStats.Skipped, categorizeStats.Errors, stage.Duration)
	return stage, nil
}
