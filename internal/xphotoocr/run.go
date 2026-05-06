package xphotoocr

import (
	"context"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	opts = resolveOptions(cfg, opts)

	items, err := st.ListItemsForXPhotoOCR(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		ItemsQueued:    len(items),
		ItemsProcessed: len(items),
	}
	if len(items) == 0 {
		return stats, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}
	debugLog(opts.Logger, "x photo ocr candidates loaded", "items", len(items), "limit", opts.Limit, "force", opts.Force, "concurrency", concurrency)

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan model.Item)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-jobs:
					if !ok {
						return
					}
					outcome := processOCRItem(ctx, cfg, st, opts, item)
					mu.Lock()
					stats.ItemsUpdated += outcome.itemsUpdated
					stats.ItemsUnchanged += outcome.itemsUnchanged
					stats.ItemsSkipped += outcome.itemsSkipped
					stats.PhotoCandidates += outcome.photoCandidates
					stats.PhotosOCRed += outcome.photosOCRed
					stats.HostedAttempts += outcome.hostedAttempts
					stats.HostedFallbacks += outcome.hostedFallbacks
					stats.Errors += outcome.errors
					mu.Unlock()
				}
			}
		}()
	}
dispatchLoop:
	for _, item := range items {
		select {
		case <-ctx.Done():
			break dispatchLoop
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}
