package sourceenrich

import (
	"errors"

	"github.com/darron/dbrain/internal/model"
)

func processPreflightTerminal(processCtx sourceProcessContext) (sourceProcessResult, bool) {
	var result sourceProcessResult
	ctx := processCtx.ctx
	cfg := processCtx.cfg
	st := processCtx.st
	source := processCtx.source
	opts := processCtx.opts

	failure, terminal := preflightTerminalSourceFailure(ctx, source, opts, processCtx.extractToolVersion)
	if !terminal {
		return result, false
	}

	if extract, recovered, recoverErr := waybackExtractForTerminalFailure(ctx, source, opts, errors.New(failure.Error)); recoverErr != nil {
		debugLog(opts.Logger, "source wayback recovery failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", recoverErr.Error())
	} else if recovered {
		debugLog(opts.Logger, "source extraction recovered via wayback", "source_key", source.SourceKey, "url", source.CanonicalURL, "final_url", extract.FinalURL, "content_chars", len(extract.Content))
		waybackStats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, extract, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion)
		if err != nil {
			result.Err = err
			return result, true
		}
		result.Stats.SourcesExtracted += waybackStats.SourcesExtracted
		result.Stats.SourcesSummarized += waybackStats.SourcesSummarized
		result.Stats.SourcesUnchanged += waybackStats.SourcesUnchanged
		result.Stats.Errors += waybackStats.Errors
		result.TouchedSourceID = source.ID
		return result, true
	}

	result.Stats.Errors++
	debugLog(opts.Logger, "source preflight failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "error", failure.Error)
	if err := saveSourceFailure(ctx, st, source, failure, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion); err != nil {
		result.Err = err
		return result, true
	}
	result.TouchedSourceID = source.ID
	return result, true
}

func processHTTPReaderFallback(processCtx sourceProcessContext) (sourceProcessResult, bool) {
	var result sourceProcessResult
	ctx := processCtx.ctx
	cfg := processCtx.cfg
	st := processCtx.st
	source := processCtx.source
	opts := processCtx.opts

	if !sourceMatchesHTTPReaderFallbackDomain(source, opts) {
		return result, false
	}

	readerExtract, recovered, readerErr := extractKnownReaderDomainSource(ctx, source, opts)
	if readerErr != nil && !recovered {
		result.Stats.Errors++
		debugLog(opts.Logger, "source reader extraction failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", readerErr.Error())
		failure := model.ExtractResult{
			Status:      model.SourceExtractStatusError,
			Error:       readerErr.Error(),
			Tool:        protectedFetchToolName,
			ToolVersion: httpReaderToolVersion,
		}
		if status, errorText, terminal := classifyTerminalExtractError(source, readerErr); terminal {
			failure.Status = status
			if errorText != "" {
				failure.Error = errorText
			}
		}
		if err := saveSourceFailure(ctx, st, source, failure, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion); err != nil {
			result.Err = err
			return result, true
		}
		result.TouchedSourceID = source.ID
		return result, true
	}

	if recovered {
		if readerErr != nil {
			debugLog(opts.Logger, "source reader extraction failed; using direct http fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", readerErr.Error(), "content_chars", len(readerExtract.Content))
		} else {
			readerURL := buildHTTPReaderURL(opts.HTTPReaderBaseURL, firstNonEmpty(source.CanonicalURL, source.NormalizedURL))
			debugLog(opts.Logger, "source extraction using reader fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "reader_url", readerURL, "content_chars", len(readerExtract.Content))
		}
		readerExtract = normalizeReaderExtract(source, readerExtract)
		readerStats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, readerExtract, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion)
		if err != nil {
			result.Err = err
			return result, true
		}
		result.Stats.SourcesExtracted += readerStats.SourcesExtracted
		result.Stats.SourcesSummarized += readerStats.SourcesSummarized
		result.Stats.SourcesUnchanged += readerStats.SourcesUnchanged
		result.Stats.Errors += readerStats.Errors
		result.TouchedSourceID = source.ID
		return result, true
	}

	return result, false
}
