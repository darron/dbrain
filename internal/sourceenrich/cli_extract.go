package sourceenrich

import (
	"context"
	"errors"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

func processDirectSummaryExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options, sourceArgs []string, sourceEnv map[string]string, extractToolVersion string, summaryToolVersion string) (sourceProcessResult, bool) {
	var result sourceProcessResult

	if !opts.Summarize || (len(sourceArgs) == 0 && !summarizecli.UsesDirectSummary(opts.Model)) {
		return result, false
	}

	inputURL, usingReaderInput := sourceExtractionInput(source, opts)
	extractResult, err := runSummarizeWithRedirectRetry(ctx, source, opts, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     inputURL,
		Summarize: false,
		Length:    opts.Length,
		Language:  opts.Language,
		Timeout:   opts.Timeout,
		RootDir:   cfg.RootDir,
		Env:       sourceEnv,
		Args:      sourceArgs,
	})
	if err != nil {
		return processExtractRunError(ctx, cfg, st, source, opts, err, "source extraction failed", extractToolVersion, summaryToolVersion), true
	}
	if fallback, changed, err := fallbackExtract(ctx, cfg, opts, source, extractResult.Extract); err != nil {
		result.Err = err
		return result, true
	} else if changed {
		extractResult.Extract = fallback
	}
	if usingReaderInput {
		extractResult.Extract = normalizeReaderExtract(source, extractResult.Extract)
	}
	if normalized, changed := normalizeExtract(source, extractResult.Extract); changed {
		extractResult.Extract = normalized
	}
	extractStats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, extractResult.Extract, opts, extractToolVersion, summaryToolVersion)
	if err != nil {
		result.Err = err
		return result, true
	}
	result.Stats.SourcesExtracted += extractStats.SourcesExtracted
	result.Stats.SourcesSummarized += extractStats.SourcesSummarized
	result.Stats.SourcesUnchanged += extractStats.SourcesUnchanged
	result.Stats.Errors += extractStats.Errors

	result.TouchedSourceID = source.ID
	return result, true
}

func processDefaultCLIExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options, sourceArgs []string, sourceEnv map[string]string, extractToolVersion string, summaryToolVersion string) sourceProcessResult {
	var result sourceProcessResult

	cli := opts.CLI
	if opts.Summarize {
		cli = summaryCLI(opts)
	}

	inputURL, usingReaderInput := sourceExtractionInput(source, opts)
	runResult, err := runSummarizeWithRedirectRetry(ctx, source, opts, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     inputURL,
		Summarize: opts.Summarize,
		Model:     opts.Model,
		CLI:       cli,
		Prompt:    summaryPrompt,
		Length:    opts.Length,
		Language:  opts.Language,
		Timeout:   opts.Timeout,
		RootDir:   cfg.RootDir,
		Env:       sourceEnv,
		Args:      sourceArgs,
	})
	if err != nil {
		return processExtractRunError(ctx, cfg, st, source, opts, err, "source enrichment failed", extractToolVersion, summaryToolVersion)
	}
	if usingReaderInput {
		runResult.Extract = normalizeReaderExtract(source, runResult.Extract)
	}
	if failure, invalid := rejectExtractFailure(source, runResult.Extract); invalid {
		if failure.Status == "error" {
			if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
				failure.Status = status
				if errorText != "" {
					failure.Error = errorText
				}
			}
		}
		result.Stats.Errors++
		debugLog(opts.Logger, "source extraction rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
			result.Err = err
			return result
		}
		result.TouchedSourceID = source.ID
		return result
	}

	contentHash := hashText(runResult.Extract.Content)
	if changed, err := st.SaveSourceExtraction(ctx, source.ID, runResult.Extract, contentHash); err != nil {
		result.Err = err
		return result
	} else if changed {
		result.Stats.SourcesExtracted++
		debugLog(opts.Logger, "source extraction saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", runResult.Extract.Status, "content_chars", len(runResult.Extract.Content), "tool", runResult.Extract.Tool)
	} else {
		result.Stats.SourcesUnchanged++
	}

	if opts.Summarize {
		runResult.Summary.PromptVersion = SummaryPromptVersion
		if changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary); err != nil {
			result.Err = err
			return result
		} else if changed && runResult.Summary.Status == "ok" {
			result.Stats.SourcesSummarized++
			debugLog(opts.Logger, "source summary saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "summary_chars", len(runResult.Summary.Text), "model", runResult.Summary.Model, "tool", runResult.Summary.Tool)
		}
	}

	result.TouchedSourceID = source.ID
	return result
}

func processExtractRunError(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options, runErr error, logMessage string, extractToolVersion string, summaryToolVersion string) sourceProcessResult {
	var result sourceProcessResult

	if isUserCancellation(ctx, runErr) {
		result.Err = context.Canceled
		return result
	}
	if fallbackExtract, recovered, fallbackErr := fallbackExtractForSourceError(ctx, source, opts, runErr); fallbackErr != nil {
		debugLog(opts.Logger, "source protected fetch recovery failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", fallbackErr.Error())
	} else if recovered {
		debugLog(opts.Logger, "source extraction recovered via fallback fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "final_url", fallbackExtract.FinalURL, "tool", fallbackExtract.Tool, "content_chars", len(fallbackExtract.Content))
		fallbackStats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, fallbackExtract, opts, extractToolVersion, summaryToolVersion)
		if err != nil {
			result.Err = err
			return result
		}
		result.Stats.SourcesExtracted += fallbackStats.SourcesExtracted
		result.Stats.SourcesSummarized += fallbackStats.SourcesSummarized
		result.Stats.SourcesUnchanged += fallbackStats.SourcesUnchanged
		result.Stats.Errors += fallbackStats.Errors
		result.TouchedSourceID = source.ID
		return result
	}
	result.Stats.Errors++
	debugLog(opts.Logger, logMessage, "source_key", source.SourceKey, "url", source.CanonicalURL, "error", runErr.Error())
	failure := model.ExtractResult{
		Status:      "error",
		Error:       runErr.Error(),
		Tool:        summarizecli.ToolName,
		ToolVersion: extractToolVersion,
	}
	if status, errorText, terminal := classifyTerminalExtractError(source, runErr); terminal {
		failure.Status = status
		if errorText != "" {
			failure.Error = errorText
		}
	}
	if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
		result.Err = err
		return result
	}
	result.TouchedSourceID = source.ID
	return result
}
