package sourceenrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
	"github.com/darron/dbrain/internal/vault"
)

type Options struct {
	Limit                     int
	Concurrency               int
	Force                     bool
	AcceptCurrentSummary      bool
	Summarize                 bool
	Model                     string
	CLI                       string
	Length                    string
	Language                  string
	Timeout                   time.Duration
	ProgressInterval          time.Duration
	Logger                    *slog.Logger
	ExactSummaryFreshness     bool
	EnvFor                    func(source model.SourceDocument) map[string]string
	ArgsFor                   func(source model.SourceDocument) []string
	ResolveHost               func(ctx context.Context, host string) error
	ResolveRedirectURL        func(ctx context.Context, rawURL string) (string, error)
	FallbackExtractFor        func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error)
	HTTPReaderFallbackDomains []string
	HTTPReaderBaseURL         string
	WaybackFallbackEnabled    bool
	WaybackAvailabilityURL    string
	Binary                    string
	YouTubeBrowser            string
	YouTubeProfile            string
	YouTubeCookiesArg         string
	YouTubeTranscriber        string
	YTDLPBinary               string
	WhisperBinary             string
	WhisperModelPath          string
	MacWhisperBinary          string
}

type Stats struct {
	SourcesQueued     int `json:"sources_queued"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}

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
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.ProgressInterval == 0 {
		opts.ProgressInterval = 15 * time.Second
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE")
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if strings.TrimSpace(opts.YouTubeBrowser) == "" {
		opts.YouTubeBrowser = "chrome"
	}
	if strings.TrimSpace(opts.YouTubeTranscriber) == "" {
		opts.YouTubeTranscriber = "auto"
	}
	if strings.TrimSpace(opts.YTDLPBinary) == "" {
		opts.YTDLPBinary = "yt-dlp"
	}
	if strings.TrimSpace(opts.WhisperBinary) == "" {
		opts.WhisperBinary = "whisper-cli"
	}
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		opts.WhisperModelPath = defaultWhisperModelPath()
	}
	if strings.TrimSpace(opts.MacWhisperBinary) == "" {
		opts.MacWhisperBinary = "mw"
	}
	if opts.HTTPReaderFallbackDomains == nil {
		opts.HTTPReaderFallbackDomains = parseCommaList(firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_SOURCE_READER_DOMAINS", "DBRAIN_HTTP_READER_DOMAINS"), "canada.ca"))
	}
	if strings.TrimSpace(opts.HTTPReaderBaseURL) == "" {
		opts.HTTPReaderBaseURL = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_SOURCE_READER_BASE_URL", "DBRAIN_HTTP_READER_BASE_URL"), "https://r.jina.ai/")
	}
	opts.WaybackFallbackEnabled = runtimeenv.FirstBoolDefault(cfg.RootDir, true, "DBRAIN_SOURCE_WAYBACK_ENABLED", "DBRAIN_WAYBACK_ENABLED")
	if strings.TrimSpace(opts.WaybackAvailabilityURL) == "" {
		opts.WaybackAvailabilityURL = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_SOURCE_WAYBACK_AVAILABILITY_URL", "DBRAIN_WAYBACK_AVAILABILITY_URL"), defaultWaybackAvailabilityURL)
	}

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

func renderSourceNote(ctx context.Context, cfg config.Config, st *store.Store, sourceID int64) error {
	source, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	backlinks, err := st.ListBacklinksForSource(ctx, sourceID)
	if err != nil {
		return err
	}
	return vault.WriteSource(cfg, source, backlinks)
}

func persistExtractAndSummaryFromExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, extractToolVersion string, summaryToolVersion string) (Stats, error) {
	var stats Stats

	if failure, invalid := rejectExtractFailure(source, extract); invalid {
		if failure.Status == "error" {
			if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
				failure.Status = status
				if errorText != "" {
					failure.Error = errorText
				}
			}
		}
		stats.Errors++
		debugLog(opts.Logger, "source extraction rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
			return stats, err
		}
		return stats, nil
	}

	contentHash := hashText(extract.Content)
	if changed, err := st.SaveSourceExtraction(ctx, source.ID, extract, contentHash); err != nil {
		return stats, err
	} else if changed {
		stats.SourcesExtracted++
		debugLog(opts.Logger, "source extraction saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", extract.Status, "content_chars", len(extract.Content), "tool", extract.Tool)
	} else {
		stats.SourcesUnchanged++
	}

	if !opts.Summarize {
		return stats, nil
	}

	if changed, status, err := summarizeFromExtract(ctx, cfg, st, source, extract, opts, summaryToolVersion); err != nil {
		return stats, err
	} else if changed && status == "ok" {
		stats.SourcesSummarized++
	}

	return stats, nil
}

func needsEnrichment(source model.SourceDocument, opts Options, promptVersion string, toolName string, toolVersion string) bool {
	if source.ExtractStatus == "" || source.ExtractStatus == "error" {
		return true
	}
	if !opts.Summarize {
		return false
	}
	if source.ExtractStatus != "ok" && source.ExtractStatus != "empty" {
		return false
	}
	if source.SummaryStatus == "" || source.SummaryStatus == "error" {
		return true
	}
	if source.SummaryContentHash != source.ContentHash {
		return true
	}
	if opts.AcceptCurrentSummary {
		return false
	}
	if promptVersion != "" && source.SummaryPromptVersion != promptVersion {
		return true
	}
	if toolName != "" && source.SummaryTool != toolName {
		return true
	}
	if toolVersion != "" && source.SummaryToolVersion != toolVersion {
		return true
	}
	return false
}

func envFor(opts Options, source model.SourceDocument) map[string]string {
	if opts.EnvFor == nil {
		if source.SourceType != "youtube" {
			return nil
		}
		return map[string]string{
			"SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER": firstNonEmpty(strings.TrimSpace(opts.YouTubeCookiesArg), cookiesFromBrowserArg(opts.YouTubeBrowser, opts.YouTubeProfile)),
		}
	}
	return opts.EnvFor(source)
}

func argsFor(opts Options, source model.SourceDocument) []string {
	if opts.ArgsFor == nil {
		if source.SourceType != "youtube" {
			return nil
		}
		args := []string{"--youtube", "auto", "--video-mode", "transcript"}
		if value := strings.TrimSpace(opts.YouTubeTranscriber); value != "" {
			args = append(args, "--transcriber", value)
		}
		return args
	}
	return opts.ArgsFor(source)
}

func sourceExtractionInput(source model.SourceDocument, opts Options) (string, bool) {
	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	return sourceURL, false
}

func parseCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeReaderExtract(source model.SourceDocument, extract model.ExtractResult) model.ExtractResult {
	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	if sourceURL == "" {
		return extract
	}
	extract.CanonicalURL = sourceURL
	extract.FinalURL = sourceURL
	if strings.TrimSpace(extract.Title) == "" {
		extract.Title = source.Title
	}
	if strings.TrimSpace(extract.SiteName) == "" {
		extract.SiteName = firstNonEmpty(source.SiteName, source.Domain, siteNameFromURL(sourceURL))
	}
	return extract
}

func fallbackExtract(ctx context.Context, cfg config.Config, opts Options, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error) {
	if opts.FallbackExtractFor == nil {
		fallback, changed, err := MaybeTranscribeYouTubeAudioFallback(ctx, cfg, source, extract, opts)
		if err != nil {
			debugLog(opts.Logger, "youtube audio fallback failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			return model.ExtractResult{}, false, nil
		}
		if changed {
			debugLog(opts.Logger, "youtube audio fallback succeeded", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(fallback.Content), "tool", fallback.Tool)
		}
		return fallback, changed, nil
	}
	return opts.FallbackExtractFor(ctx, source, extract)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hashText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func uniqueSorted(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func selectSourceDocuments(ordered []int64, byID map[int64]model.SourceDocument, opts Options, promptVersion string, toolName string, toolVersion string) []model.SourceDocument {
	filtered := make([]model.SourceDocument, 0, len(ordered))
	for _, sourceID := range ordered {
		source, ok := byID[sourceID]
		if !ok {
			continue
		}
		if !opts.Force && !needsEnrichment(source, opts, promptVersion, toolName, toolVersion) {
			continue
		}
		filtered = append(filtered, source)
		if opts.Limit > 0 && len(filtered) >= opts.Limit {
			break
		}
	}
	return filtered
}

func mapKeys(values map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
