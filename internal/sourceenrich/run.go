package sourceenrich

import (
	"context"
	"errors"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func RunPending(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []int64, error) {
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)
	summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion := summaryFreshnessTarget(ctx, opts)
	extractToolVersion := summarizecli.Version(ctx, opts.Binary)
	summaryToolVersion := summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
	sources, err := st.ListSourcesForEnrichment(ctx, opts.Limit, opts.Force, opts.Summarize, summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion)
	if err != nil {
		return Stats{}, nil, err
	}
	return runSources(ctx, cfg, st, sources, opts, extractToolVersion, summaryToolVersion)
}

func RunSourceIDs(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (Stats, []int64, error) {
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)
	ordered := uniqueSorted(sourceIDs)
	sources, err := st.GetSourcesByIDs(ctx, ordered)
	if err != nil {
		return Stats{}, nil, err
	}

	byID := make(map[int64]model.SourceDocument, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}

	summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion := summaryFreshnessTarget(ctx, opts)
	extractToolVersion := summarizecli.Version(ctx, opts.Binary)
	summaryToolVersion := summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
	filtered := selectSourceDocuments(ordered, byID, opts, summaryPromptVersion, summaryToolName, freshnessSummaryToolVersion)

	return runSources(ctx, cfg, st, filtered, opts, extractToolVersion, summaryToolVersion)
}

func runSources(ctx context.Context, cfg config.Config, st *store.Store, sources []model.SourceDocument, opts Options, extractToolVersion string, summaryToolVersion string) (Stats, []int64, error) {
	opts = defaultOptions(cfg, opts)

	stats := Stats{SourcesQueued: len(sources)}
	touchedSourceIDs := map[int64]struct{}{}

	debugLog(opts.Logger, "source enrichment candidates loaded", "sources", len(sources), "limit", opts.Limit, "summarize", opts.Summarize, "concurrency", opts.Concurrency, "timeout", opts.Timeout, "progress_interval", opts.ProgressInterval)

	results, err := processSourcesConcurrently(ctx, cfg, st, sources, opts, extractToolVersion, summaryToolVersion)
	for _, result := range results {
		stats.SourcesExtracted += result.Stats.SourcesExtracted
		stats.SourcesSummarized += result.Stats.SourcesSummarized
		stats.SourcesRendered += result.Stats.SourcesRendered
		stats.SourcesUnchanged += result.Stats.SourcesUnchanged
		stats.Errors += result.Stats.Errors
		if result.TouchedSourceID > 0 {
			touchedSourceIDs[result.TouchedSourceID] = struct{}{}
		}
	}
	orderedSourceIDs := uniqueSorted(mapKeys(touchedSourceIDs))
	if err != nil {
		return stats, orderedSourceIDs, err
	}

	return stats, orderedSourceIDs, nil
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

	sourceArgs := argsFor(opts, source)
	sourceEnv := envFor(opts, source)
	skipStoredExtract := false
	localExtract, hasLocalExtract, err := st.GetPreferredLocalSourceExtract(ctx, source.ID)
	if err != nil {
		result.Err = err
		return result
	}
	if hasLocalExtract {
		if normalized, changed := normalizeExtract(source, localExtract); changed {
			localExtract = normalized
		}
		if failure, invalid := rejectExtractFailure(source, localExtract); invalid {
			if shouldRetryRemoteAfterLocalExtractReject(source, localExtract, failure) {
				skipStoredExtract = true
				debugLog(opts.Logger, "local source extract insufficient; falling back to remote fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "reason", failure.Error)
			} else {
				if failure.Status == "error" {
					if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
						failure.Status = status
						if errorText != "" {
							failure.Error = errorText
						}
					}
				}
				result.Stats.Errors++
				debugLog(opts.Logger, "local source extract rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
				if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
					result.Err = err
					return result
				}
				result.TouchedSourceID = source.ID
				return result
			}
		}
		if !skipStoredExtract {
			debugLog(opts.Logger, "using local cached extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(localExtract.Content))
			contentHash := hashText(localExtract.Content)
			if changed, err := st.SaveSourceExtraction(ctx, source.ID, localExtract, contentHash); err != nil {
				result.Err = err
				return result
			} else if changed {
				result.Stats.SourcesExtracted++
				debugLog(opts.Logger, "source extraction saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", localExtract.Status, "content_chars", len(localExtract.Content), "tool", localExtract.Tool)
			} else {
				result.Stats.SourcesUnchanged++
			}

			if opts.Summarize {
				runResult, err := summarizeExtract(ctx, cfg, source, localExtract, opts, sourceEnv)
				if err != nil {
					if isUserCancellation(ctx, err) {
						result.Err = context.Canceled
						return result
					}
					result.Stats.Errors++
					debugLog(opts.Logger, "local source summarization failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
					if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
						Status:        "error",
						Error:         err.Error(),
						Model:         opts.Model,
						PromptVersion: SummaryPromptVersion,
						Tool:          summarizecli.SummaryToolName(opts.Model),
						ToolVersion:   summaryToolVersion,
					}); saveErr != nil {
						result.Err = saveErr
						return result
					}
					result.TouchedSourceID = source.ID
					return result
				}
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
	}

	if !skipStoredExtract && opts.Summarize && !opts.Force && canSummarizeStoredExtract(source) {
		storedExtract := extractFromSource(source)
		if normalized, changed := normalizeExtract(source, storedExtract); changed {
			storedExtract = normalized
			contentHash := hashText(storedExtract.Content)
			if changed, err := st.SaveSourceExtraction(ctx, source.ID, storedExtract, contentHash); err != nil {
				result.Err = err
				return result
			} else if changed {
				result.Stats.SourcesExtracted++
			}
		}
		if failure, invalid := rejectExtractFailure(source, storedExtract); invalid {
			if failure.Status == "error" {
				if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
					failure.Status = status
					if errorText != "" {
						failure.Error = errorText
					}
				}
			}
			result.Stats.Errors++
			debugLog(opts.Logger, "stored source extract rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
			if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
				result.Err = err
				return result
			}
			result.TouchedSourceID = source.ID
			return result
		}
		debugLog(opts.Logger, "using stored extract for summary", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(storedExtract.Content))
		if changed, status, err := summarizeFromExtract(ctx, cfg, st, source, storedExtract, opts, summaryToolVersion); err != nil {
			if isUserCancellation(ctx, err) {
				result.Err = context.Canceled
				return result
			}
			result.Err = err
			return result
		} else if changed && status == "ok" {
			result.Stats.SourcesSummarized++
		}
		result.TouchedSourceID = source.ID
		return result
	}

	if failure, terminal := preflightTerminalSourceFailure(ctx, source, opts, extractToolVersion); terminal {
		if extract, recovered, recoverErr := waybackExtractForTerminalFailure(ctx, source, opts, errors.New(failure.Error)); recoverErr != nil {
			debugLog(opts.Logger, "source wayback recovery failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", recoverErr.Error())
		} else if recovered {
			debugLog(opts.Logger, "source extraction recovered via wayback", "source_key", source.SourceKey, "url", source.CanonicalURL, "final_url", extract.FinalURL, "content_chars", len(extract.Content))
			waybackStats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, extract, opts, extractToolVersion, summaryToolVersion)
			if err != nil {
				result.Err = err
				return result
			}
			result.Stats.SourcesExtracted += waybackStats.SourcesExtracted
			result.Stats.SourcesSummarized += waybackStats.SourcesSummarized
			result.Stats.SourcesUnchanged += waybackStats.SourcesUnchanged
			result.Stats.Errors += waybackStats.Errors
			result.TouchedSourceID = source.ID
			return result
		}
		result.Stats.Errors++
		debugLog(opts.Logger, "source preflight failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "error", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
			result.Err = err
			return result
		}
		result.TouchedSourceID = source.ID
		return result
	}

	if sourceMatchesHTTPReaderFallbackDomain(source, opts) {
		readerExtract, recovered, readerErr := extractKnownReaderDomainSource(ctx, source, opts)
		if readerErr != nil && !recovered {
			result.Stats.Errors++
			debugLog(opts.Logger, "source reader extraction failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", readerErr.Error())
			failure := model.ExtractResult{
				Status:      "error",
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
			if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
				result.Err = err
				return result
			}
			result.TouchedSourceID = source.ID
			return result
		}
		if recovered {
			if readerErr != nil {
				debugLog(opts.Logger, "source reader extraction failed; using direct http fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", readerErr.Error(), "content_chars", len(readerExtract.Content))
			} else {
				readerURL := buildHTTPReaderURL(opts.HTTPReaderBaseURL, firstNonEmpty(source.CanonicalURL, source.NormalizedURL))
				debugLog(opts.Logger, "source extraction using reader fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "reader_url", readerURL, "content_chars", len(readerExtract.Content))
			}
			readerExtract = normalizeReaderExtract(source, readerExtract)
			readerStats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, readerExtract, opts, extractToolVersion, summaryToolVersion)
			if err != nil {
				result.Err = err
				return result
			}
			result.Stats.SourcesExtracted += readerStats.SourcesExtracted
			result.Stats.SourcesSummarized += readerStats.SourcesSummarized
			result.Stats.SourcesUnchanged += readerStats.SourcesUnchanged
			result.Stats.Errors += readerStats.Errors
			result.TouchedSourceID = source.ID
			return result
		}
	}

	if opts.Summarize && (len(sourceArgs) > 0 || summarizecli.UsesDirectSummary(opts.Model)) {
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
			if isUserCancellation(ctx, err) {
				result.Err = context.Canceled
				return result
			}
			if fallbackExtract, recovered, fallbackErr := fallbackExtractForSourceError(ctx, source, opts, err); fallbackErr != nil {
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
			debugLog(opts.Logger, "source extraction failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			failure := model.ExtractResult{
				Status:      "error",
				Error:       err.Error(),
				Tool:        summarizecli.ToolName,
				ToolVersion: extractToolVersion,
			}
			if status, errorText, terminal := classifyTerminalExtractError(source, err); terminal {
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
		if fallback, changed, err := fallbackExtract(ctx, cfg, opts, source, extractResult.Extract); err != nil {
			result.Err = err
			return result
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
			return result
		}
		result.Stats.SourcesExtracted += extractStats.SourcesExtracted
		result.Stats.SourcesSummarized += extractStats.SourcesSummarized
		result.Stats.SourcesUnchanged += extractStats.SourcesUnchanged
		result.Stats.Errors += extractStats.Errors

		result.TouchedSourceID = source.ID
		return result
	}

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
		if isUserCancellation(ctx, err) {
			result.Err = context.Canceled
			return result
		}
		if fallbackExtract, recovered, fallbackErr := fallbackExtractForSourceError(ctx, source, opts, err); fallbackErr != nil {
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
		debugLog(opts.Logger, "source enrichment failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
		failure := model.ExtractResult{
			Status:      "error",
			Error:       err.Error(),
			Tool:        summarizecli.ToolName,
			ToolVersion: extractToolVersion,
		}
		if status, errorText, terminal := classifyTerminalExtractError(source, err); terminal {
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
