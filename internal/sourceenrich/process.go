package sourceenrich

import (
	"context"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type sourceProcessContext struct {
	ctx                context.Context
	cfg                config.Config
	st                 *store.Store
	source             model.SourceDocument
	opts               Options
	sourceArgs         []string
	sourceEnv          map[string]string
	extractToolVersion string
	summaryToolVersion string
}

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

	processCtx := sourceProcessContext{
		ctx:                ctx,
		cfg:                cfg,
		st:                 st,
		source:             source,
		opts:               opts,
		sourceArgs:         argsFor(opts, source),
		sourceEnv:          envFor(opts, source),
		extractToolVersion: extractToolVersion,
		summaryToolVersion: summaryToolVersion,
	}
	feedResult, handled := processFeedLinkedHTTPExtract(processCtx)
	if handled {
		return feedResult
	}

	localResult, skipStoredExtract, handled := processPreferredLocalExtract(processCtx)
	if handled {
		return localResult
	}

	if !skipStoredExtract {
		storedResult, handled := processStoredExtractSummary(processCtx)
		if handled {
			return storedResult
		}
	}

	preflightResult, handled := processPreflightTerminal(processCtx)
	if handled {
		return preflightResult
	}

	readerResult, handled := processHTTPReaderFallback(processCtx)
	if handled {
		return readerResult
	}

	directResult, handled := processDirectSummaryExtract(processCtx)
	if handled {
		return directResult
	}

	return processDefaultCLIExtract(processCtx)
}
