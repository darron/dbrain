package sourceenrich

import (
	"context"
	"errors"
	"strings"

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

func processFeedLinkedHTTPExtract(processCtx sourceProcessContext) (sourceProcessResult, bool) {
	var result sourceProcessResult
	ctx := processCtx.ctx
	cfg := processCtx.cfg
	st := processCtx.st
	source := processCtx.source
	opts := processCtx.opts

	linkedFromFeed, err := st.SourceHasLinkedItemType(ctx, source.ID, "feed_entry")
	if err != nil {
		result.Err = err
		return result, true
	}
	if !linkedFromFeed {
		return result, false
	}

	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	if sourceURL == "" {
		return result, false
	}
	extract, recovered, err := extractHTTPReadableSource(ctx, sourceURL)
	if err != nil || !recovered {
		if err != nil {
			debugLog(opts.Logger, "feed-linked source markdown fetch failed; falling back to local or CLI extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
		}
		return result, false
	}
	if normalized, changed := normalizeExtract(source, extract); changed {
		extract = normalized
	}
	merged, mergeErr := mergeFeedEntryContext(ctx, st, source, extract)
	if mergeErr != nil {
		result.Err = mergeErr
		return result, true
	}
	extract = merged
	if failure, invalid := rejectExtractFailure(source, extract); invalid {
		debugLog(opts.Logger, "feed-linked source markdown extract rejected; falling back to local or CLI extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
		return result, false
	}

	debugLog(opts.Logger, "using feed-linked HTTP extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(extract.Content), "tool", extract.Tool)
	stats, err := persistExtractAndSummaryFromExtract(ctx, cfg, st, source, extract, opts, processCtx.extractToolVersion, processCtx.summaryToolVersion)
	if err != nil {
		result.Err = err
		return result, true
	}
	result.Stats.SourcesExtracted += stats.SourcesExtracted
	result.Stats.SourcesSummarized += stats.SourcesSummarized
	result.Stats.SourcesUnchanged += stats.SourcesUnchanged
	result.Stats.Errors += stats.Errors
	result.TouchedSourceID = source.ID
	return result, true
}

func mergeFeedEntryContext(ctx context.Context, st interface {
	ListLinkedItemsBySourceType(context.Context, int64, string, int) ([]model.Item, error)
}, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, error) {
	items, err := st.ListLinkedItemsBySourceType(ctx, source.ID, "feed_entry", 3)
	if err != nil {
		return extract, err
	}
	contextText := feedEntryContextText(items, extract.Content)
	if contextText == "" {
		return extract, nil
	}

	merged := extract
	var b strings.Builder
	b.WriteString("# Linked Source Text\n\n")
	b.WriteString(strings.TrimSpace(extract.Content))
	b.WriteString("\n\n# Feed Entry Context\n\n")
	b.WriteString(contextText)
	merged.Content = strings.TrimSpace(b.String())
	merged.Status = extractStatusForContent(merged.Content)
	if strings.TrimSpace(merged.Description) == "" {
		merged.Description = "Linked source text plus feed entry context."
	}
	return merged, nil
}

func feedEntryContextText(items []model.Item, linkedContent string) string {
	linkedNorm := normalizeContextComparison(linkedContent)
	parts := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		text := firstNonEmpty(item.ArticleText, item.Text, item.SummaryText)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		textNorm := normalizeContextComparison(text)
		if textNorm == linkedNorm || seen[textNorm] {
			continue
		}
		seen[textNorm] = true
		var b strings.Builder
		if title := strings.TrimSpace(item.Title); title != "" {
			b.WriteString("Feed entry title: ")
			b.WriteString(title)
			b.WriteString("\n\n")
		}
		b.WriteString(text)
		parts = append(parts, strings.TrimSpace(b.String()))
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateUTF8(strings.Join(parts, "\n\n---\n\n"), 12000)
}

func normalizeContextComparison(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	var out strings.Builder
	for _, r := range value {
		next := string(r)
		if out.Len()+len(next) > maxBytes {
			break
		}
		out.WriteString(next)
	}
	return out.String()
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
