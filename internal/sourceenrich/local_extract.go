package sourceenrich

import (
	"context"
	"errors"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/summarizecli"
)

func processPreferredLocalExtract(processCtx sourceProcessContext) (sourceProcessResult, bool, bool) {
	var result sourceProcessResult
	ctx := processCtx.ctx
	cfg := processCtx.cfg
	st := processCtx.st
	source := processCtx.source
	opts := processCtx.opts

	localExtract, hasLocalExtract, err := st.GetPreferredLocalSourceExtract(ctx, source.ID)
	if err != nil {
		result.Err = err
		return result, false, true
	}
	if !hasLocalExtract {
		return result, false, false
	}

	if normalized, changed := normalizeExtract(source, localExtract); changed {
		localExtract = normalized
	}
	if failure, invalid := rejectExtractFailure(source, localExtract); invalid {
		if shouldRetryRemoteAfterLocalExtractReject(source, localExtract, failure) {
			debugLog(opts.Logger, "local source extract insufficient; falling back to remote fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "reason", failure.Error)
			return result, true, false
		}
		if failure.Status == model.SourceExtractStatusError {
			if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
				failure.Status = status
				if errorText != "" {
					failure.Error = errorText
				}
			}
		}
		result.Stats.Errors++
		debugLog(opts.Logger, "local source extract rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion); err != nil {
			result.Err = err
			return result, false, true
		}
		result.TouchedSourceID = source.ID
		return result, false, true
	}

	debugLog(opts.Logger, "using local cached extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(localExtract.Content))
	contentHash := hashText(localExtract.Content)
	if changed, err := st.SaveSourceExtraction(ctx, source.ID, localExtract, contentHash); err != nil {
		result.Err = err
		return result, false, true
	} else if changed {
		result.Stats.SourcesExtracted++
		result.SourceResult.Extracted = true
		debugLog(opts.Logger, "source extraction saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", localExtract.Status, "content_chars", len(localExtract.Content), "tool", localExtract.Tool)
	} else {
		result.Stats.SourcesUnchanged++
	}

	if opts.Summarize {
		runResult, err := summarizeExtract(ctx, cfg, source, localExtract, opts, processCtx.sourceEnv)
		if err != nil {
			if isUserCancellation(ctx, err) {
				result.Err = context.Canceled
				return result, false, true
			}
			debugLog(opts.Logger, "local source summarization failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			summary := model.SummaryResult{
				Status:        model.SourceSummaryStatusError,
				Error:         err.Error(),
				Model:         opts.Model,
				PromptVersion: SummaryPromptVersion,
				Tool:          summarizecli.SummaryToolName(opts.Model),
				ToolVersion:   processCtx.summaryToolVersion,
			}
			changed, saveErr := st.SaveSourceSummary(ctx, source.ID, summary)
			if saveErr != nil {
				result.Stats.Errors++
				result.Err = saveErr
				return result, false, true
			}
			recordSummaryOutcome(&result.Stats, &result.SourceResult, sourceSummaryResult(cfg.RootDir, source, summary, changed), changed)
			result.TouchedSourceID = source.ID
			return result, false, true
		}
		runResult.Summary.PromptVersion = SummaryPromptVersion
		if changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary); err != nil {
			result.Err = err
			return result, false, true
		} else {
			recordSummaryOutcome(&result.Stats, &result.SourceResult, sourceSummaryResult(cfg.RootDir, source, runResult.Summary, changed), changed)
			if changed && runResult.Summary.Status == model.SourceSummaryStatusOK {
				debugLog(opts.Logger, "source summary saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "summary_chars", len(runResult.Summary.Text), "model", runResult.Summary.Model, "tool", runResult.Summary.Tool)
			}
		}
	}

	result.TouchedSourceID = source.ID
	return result, false, true
}

func processStoredExtractSummary(processCtx sourceProcessContext) (sourceProcessResult, bool) {
	var result sourceProcessResult
	ctx := processCtx.ctx
	cfg := processCtx.cfg
	st := processCtx.st
	source := processCtx.source
	opts := processCtx.opts

	if !opts.Summarize || opts.Force || !canSummarizeStoredExtract(source) {
		return result, false
	}

	storedExtract := extractFromSource(source)
	if normalized, changed := normalizeExtract(source, storedExtract); changed {
		storedExtract = normalized
		contentHash := hashText(storedExtract.Content)
		if changed, err := st.SaveSourceExtraction(ctx, source.ID, storedExtract, contentHash); err != nil {
			result.Err = err
			return result, true
		} else if changed {
			result.Stats.SourcesExtracted++
			result.SourceResult.Extracted = true
		}
	}
	if failure, invalid := rejectExtractFailure(source, storedExtract); invalid {
		if failure.Status == model.SourceExtractStatusError {
			if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
				failure.Status = status
				if errorText != "" {
					failure.Error = errorText
				}
			}
		}
		result.Stats.Errors++
		debugLog(opts.Logger, "stored source extract rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion); err != nil {
			result.Err = err
			return result, true
		}
		result.TouchedSourceID = source.ID
		return result, true
	}
	debugLog(opts.Logger, "using stored extract for summary", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(storedExtract.Content))
	if changed, _, summaryResult, err := summarizeFromExtract(ctx, cfg, st, source, storedExtract, opts, processCtx.summaryToolVersion); err != nil {
		if isUserCancellation(ctx, err) {
			result.Err = context.Canceled
			return result, true
		}
		result.Err = err
		return result, true
	} else {
		recordSummaryOutcome(&result.Stats, &result.SourceResult, summaryResult, changed)
	}
	result.TouchedSourceID = source.ID
	return result, true
}
