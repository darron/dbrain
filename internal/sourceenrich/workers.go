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
	SourceResult    SourceResult
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
			startedAt := time.Now()
			tracker.start(source, startedAt)
			result := processSingleSource(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
			result = finalizeSourceProcessResult(source, result, time.Since(startedAt))
			tracker.finish(source, result)
			results = append(results, result)
			notifySourceResult(opts, result.SourceResult)
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
				startedAt := time.Now()
				tracker.start(source, startedAt)
				result := processSingleSource(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
				result = finalizeSourceProcessResult(source, result, time.Since(startedAt))
				tracker.finish(source, result)
				notifySourceResult(opts, result.SourceResult)
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

func finalizeSourceProcessResult(source model.SourceDocument, result sourceProcessResult, duration time.Duration) sourceProcessResult {
	result.SourceResult.SourceID = source.ID
	result.SourceResult.SourceKey = source.SourceKey
	result.SourceResult.Duration = duration
	if result.Err != nil && result.SourceResult.Error == "" {
		result.SourceResult.Error = result.Err.Error()
	}
	return result
}

func notifySourceResult(opts Options, result SourceResult) {
	if opts.OnSourceResult == nil {
		return
	}
	opts.OnSourceResult(result)
}
