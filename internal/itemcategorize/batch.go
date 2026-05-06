package itemcategorize

import (
	"context"
	"fmt"
	"sync"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

// Batch categorizes multiple items (those without user_tags unless force is set).
func Batch(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []ItemResult, error) {
	if err := resolveOpts(ctx, cfg, &opts); err != nil {
		return Stats{}, nil, err
	}

	items, err := st.ListItemsForCategorize(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, nil, fmt.Errorf("list items: %w", err)
	}

	stats := Stats{Queued: len(items)}
	if opts.OnStart != nil {
		opts.OnStart(len(items))
	}
	if len(items) == 0 {
		return stats, nil, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}

	var mu sync.Mutex
	var results []ItemResult
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
					ir := ItemResult{Item: item}
					res, runErr := Run(ctx, cfg, st, item, opts)
					if runErr != nil {
						ir.Error = runErr.Error()
						mu.Lock()
						stats.Errors++
						results = append(results, ir)
						mu.Unlock()
					} else {
						ir.Result = res
						mu.Lock()
						stats.Succeeded++
						if opts.Apply {
							stats.Applied++
						}
						results = append(results, ir)
						mu.Unlock()
					}
					if opts.OnResult != nil {
						opts.OnResult(ir)
					}
				}
			}
		}()
	}

dispatch:
	for _, item := range items {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()

	return stats, results, ctx.Err()
}

// BatchSources categorizes multiple sources (those without user_tags unless force is set).
func BatchSources(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []SourceResult, error) {
	if err := resolveOpts(ctx, cfg, &opts); err != nil {
		return Stats{}, nil, err
	}

	sources, err := st.ListSourcesForCategorize(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, nil, fmt.Errorf("list sources: %w", err)
	}

	stats := Stats{Queued: len(sources)}
	if opts.OnStart != nil {
		opts.OnStart(len(sources))
	}
	if len(sources) == 0 {
		return stats, nil, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	if concurrency > len(sources) {
		concurrency = len(sources)
	}

	var mu sync.Mutex
	var results []SourceResult
	var wg sync.WaitGroup
	jobs := make(chan model.SourceDocument)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case source, ok := <-jobs:
					if !ok {
						return
					}
					sr := SourceResult{Source: source}
					res, runErr := RunSource(ctx, cfg, st, source, opts)
					if runErr != nil {
						sr.Error = runErr.Error()
						mu.Lock()
						stats.Errors++
						results = append(results, sr)
						mu.Unlock()
					} else {
						sr.Result = res
						mu.Lock()
						stats.Succeeded++
						if opts.Apply {
							stats.Applied++
						}
						results = append(results, sr)
						mu.Unlock()
					}
					if opts.OnSourceResult != nil {
						opts.OnSourceResult(sr)
					}
				}
			}
		}()
	}

dispatch:
	for _, source := range sources {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- source:
		}
	}
	close(jobs)
	wg.Wait()

	return stats, results, ctx.Err()
}
