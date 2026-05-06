package xphotoocr

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

const CompareSchemaVersion = "x_photo_ocr_compare.v1"

func Compare(ctx context.Context, cfg config.Config, st *store.Store, opts CompareOptions) (CompareResult, error) {
	started := time.Now().UTC()
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	models := normalizeCompareModels(cfg, opts.Models)
	if len(models) == 0 {
		return CompareResult{}, fmt.Errorf("at least one OCR model is required")
	}

	samples, err := collectComparePhotoSamples(ctx, st, opts.Limit, opts.DownloadMissing)
	if err != nil {
		return CompareResult{}, err
	}

	result := CompareResult{
		SchemaVersion: CompareSchemaVersion,
		StartedAt:     started,
		Limit:         opts.Limit,
		Models:        models,
		Images:        make([]CompareImageResult, len(samples)),
	}
	if len(samples) == 0 {
		result.FinishedAt = time.Now().UTC()
		result.DurationMS = result.FinishedAt.Sub(started).Milliseconds()
		return result, nil
	}

	concurrency := opts.Concurrency
	if concurrency > len(samples) {
		concurrency = len(samples)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				image := samples[idx].result
				var inputPath string
				image.Runs, inputPath, image.InputSource = runComparePhoto(ctx, cfg, opts, models, samples[idx].ref)
				image.InputPath = inputPath
				annotateBaselineOverlap(image.Runs)
				result.Images[idx] = image
			}
		}()
	}
dispatch:
	for idx := range samples {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- idx:
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}

	result.Summary = summarizeCompareRuns(models, result.Images)
	for _, image := range result.Images {
		for _, run := range image.Runs {
			if run.Status != "ok" {
				result.Errors++
			}
		}
	}
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = result.FinishedAt.Sub(started).Milliseconds()
	return result, nil
}
