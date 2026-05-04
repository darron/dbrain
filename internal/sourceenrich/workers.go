package sourceenrich

import (
	"context"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type sourceProcessResult struct {
	Stats           Stats
	TouchedSourceID int64
	Err             error
}

func processSourcesConcurrently(ctx context.Context, cfg config.Config, st *store.Store, sources []model.SourceDocument, opts Options, extractToolVersion string, summaryToolVersion string) ([]sourceProcessResult, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	tracker := newSourceProgressTracker(len(sources))
	stopProgress := startSourceProgressLogger(ctx, opts.Logger, opts.ProgressInterval, tracker)
	defer stopProgress()

	if opts.Concurrency <= 1 || len(sources) == 1 {
		results := make([]sourceProcessResult, 0, len(sources))
		for _, source := range sources {
			tracker.start(source, time.Now())
			result := processSingleSource(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
			tracker.finish(source, result)
			results = append(results, result)
			if result.Err != nil {
				return results, result.Err
			}
		}
		return results, nil
	}

	workerCount := opts.Concurrency
	if workerCount > len(sources) {
		workerCount = len(sources)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan model.SourceDocument)
	results := make(chan sourceProcessResult, len(sources))

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for source := range jobs {
				tracker.start(source, time.Now())
				result := processSingleSource(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
				tracker.finish(source, result)
				select {
				case results <- result:
				case <-ctx.Done():
					return
				}
				if result.Err != nil {
					cancel()
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, source := range sources {
			select {
			case jobs <- source:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]sourceProcessResult, 0, len(sources))
	var firstErr error
	for result := range results {
		out = append(out, result)
		if result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
	}
	return out, firstErr
}
