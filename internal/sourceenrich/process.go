package sourceenrich

import (
	"context"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func processSingleSource(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options, extractToolVersion string, summaryToolVersion string) (result sourceProcessResult) {
	debugLog(opts.Logger, "enriching source", "source_key", source.SourceKey, "url", source.CanonicalURL)

	defer func() {
		if result.TouchedSourceID <= 0 {
			return
		}
		renderCtx := context.WithoutCancel(ctx)
		if err := renderSourceNote(renderCtx, cfg, st, result.TouchedSourceID); err != nil {
			if result.Err == nil {
				result.Err = err
			}
			return
		}
		result.Stats.SourcesRendered++
	}()

	sourceArgs := argsFor(opts, source)
	sourceEnv := envFor(opts, source)
	localResult, skipStoredExtract, handled := processPreferredLocalExtract(ctx, cfg, st, source, opts, sourceEnv, extractToolVersion, summaryToolVersion)
	if handled {
		return localResult
	}

	if !skipStoredExtract {
		storedResult, handled := processStoredExtractSummary(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
		if handled {
			return storedResult
		}
	}

	preflightResult, handled := processPreflightTerminal(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
	if handled {
		return preflightResult
	}

	readerResult, handled := processHTTPReaderFallback(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
	if handled {
		return readerResult
	}

	directResult, handled := processDirectSummaryExtract(ctx, cfg, st, source, opts, sourceArgs, sourceEnv, extractToolVersion, summaryToolVersion)
	if handled {
		return directResult
	}

	return processDefaultCLIExtract(ctx, cfg, st, source, opts, sourceArgs, sourceEnv, extractToolVersion, summaryToolVersion)
}
