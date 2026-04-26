package sourceenrich

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
	"dbrain/internal/vault"
)

const SummaryPromptVersion = "dbrain-v1"

const summaryPrompt = `Summarize this source for a local second-brain knowledge base.
Focus on durable knowledge, concrete facts, named entities, tools, libraries, APIs, claims, and actionable takeaways.
Use only the provided extracted text and explicit source metadata below.
If the extract is partial, teaser text, or truncated, say that plainly and summarize only what is actually present.
Do not infer facts, timelines, citations, linked sources, or entities that are not explicitly stated in the extracted text.
Do not use outside knowledge.
Preserve source framing. If the piece is opinion, satire, irony, marketing, advocacy, or a personal essay, say so explicitly.
Attribute subjective, speculative, promotional, or self-reported claims to the author or source. Do not rewrite claims as established fact.
If the source is a walkthrough, guide, or pitch, summarize it as a walkthrough, guide, or pitch rather than as neutral documentation.
Use Markdown with exactly these headings:
### What It Is
### Key Ideas
### Why It Matters
### Entities
### Follow-ups
Keep it factual and concise.
Use bullets only in Entities and Follow-ups.
Do not mention ads, sponsors, or irrelevant boilerplate.`

type Options struct {
	Limit                int
	Concurrency          int
	Force                bool
	AcceptCurrentSummary bool
	Summarize            bool
	Model                string
	CLI                  string
	Length               string
	Timeout              time.Duration
	Logger               *slog.Logger
	EnvFor               func(source model.SourceDocument) map[string]string
	ArgsFor              func(source model.SourceDocument) []string
	ResolveHost          func(ctx context.Context, host string) error
	ResolveRedirectURL   func(ctx context.Context, rawURL string) (string, error)
	FallbackExtractFor   func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error)
	Binary               string
	YouTubeBrowser       string
	YouTubeProfile       string
	YouTubeCookiesArg    string
	YouTubeTranscriber   string
	YTDLPBinary          string
	WhisperBinary        string
	WhisperModelPath     string
	MacWhisperBinary     string
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
	summaryToolName := summarizecli.SummaryToolName(opts.Model)
	extractToolVersion := summarizecli.Version(ctx, opts.Binary)
	summaryToolVersion := summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
	sources, err := st.ListSourcesForEnrichment(ctx, opts.Limit, opts.Force, opts.Summarize, SummaryPromptVersion, summaryToolName, summaryToolVersion)
	if err != nil {
		return Stats{}, nil, err
	}
	return runSources(ctx, cfg, st, sources, opts, extractToolVersion, summaryToolVersion)
}

func RunSourceIDs(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (Stats, []int64, error) {
	ordered := uniqueSorted(sourceIDs)
	sources, err := st.GetSourcesByIDs(ctx, ordered)
	if err != nil {
		return Stats{}, nil, err
	}

	byID := make(map[int64]model.SourceDocument, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}

	summaryToolName := summarizecli.SummaryToolName(opts.Model)
	extractToolVersion := summarizecli.Version(ctx, opts.Binary)
	summaryToolVersion := summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
	filtered := selectSourceDocuments(ordered, byID, opts, summaryToolName, summaryToolVersion)

	return runSources(ctx, cfg, st, filtered, opts, extractToolVersion, summaryToolVersion)
}

func runSources(ctx context.Context, cfg config.Config, st *store.Store, sources []model.SourceDocument, opts Options, extractToolVersion string, summaryToolVersion string) (Stats, []int64, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
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

	stats := Stats{SourcesQueued: len(sources)}
	touchedSourceIDs := map[int64]struct{}{}

	debugLog(opts.Logger, "source enrichment candidates loaded", "sources", len(sources), "limit", opts.Limit, "summarize", opts.Summarize, "concurrency", opts.Concurrency)

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
	if opts.Concurrency <= 1 || len(sources) == 1 {
		results := make([]sourceProcessResult, 0, len(sources))
		for _, source := range sources {
			result := processSingleSource(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
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
				result := processSingleSource(ctx, cfg, st, source, opts, extractToolVersion, summaryToolVersion)
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
		result.Stats.Errors++
		debugLog(opts.Logger, "source preflight failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "error", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
			result.Err = err
			return result
		}
		result.TouchedSourceID = source.ID
		return result
	}

	if opts.Summarize && (len(sourceArgs) > 0 || summarizecli.UsesDirectSummary(opts.Model)) {
		extractResult, err := runSummarizeWithRedirectRetry(ctx, source, opts, summarizecli.Options{
			Binary:    opts.Binary,
			Input:     source.CanonicalURL,
			Summarize: false,
			Length:    opts.Length,
			Timeout:   opts.Timeout,
			Env:       sourceEnv,
			Args:      sourceArgs,
		})
		if err != nil {
			if isUserCancellation(ctx, err) {
				result.Err = context.Canceled
				return result
			}
			if fallbackExtract, recovered, fallbackErr := fallbackExtractForFetchError(ctx, source, opts, err); fallbackErr != nil {
				debugLog(opts.Logger, "source protected fetch recovery failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", fallbackErr.Error())
			} else if recovered {
				debugLog(opts.Logger, "source extraction recovered via protected fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "final_url", fallbackExtract.FinalURL, "tool", fallbackExtract.Tool, "content_chars", len(fallbackExtract.Content))
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

	runResult, err := runSummarizeWithRedirectRetry(ctx, source, opts, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     source.CanonicalURL,
		Summarize: opts.Summarize,
		Model:     opts.Model,
		CLI:       cli,
		Prompt:    summaryPrompt,
		Length:    opts.Length,
		Timeout:   opts.Timeout,
		Env:       sourceEnv,
		Args:      sourceArgs,
	})
	if err != nil {
		if isUserCancellation(ctx, err) {
			result.Err = context.Canceled
			return result
		}
		if fallbackExtract, recovered, fallbackErr := fallbackExtractForFetchError(ctx, source, opts, err); fallbackErr != nil {
			debugLog(opts.Logger, "source protected fetch recovery failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", fallbackErr.Error())
		} else if recovered {
			debugLog(opts.Logger, "source extraction recovered via protected fetch", "source_key", source.SourceKey, "url", source.CanonicalURL, "final_url", fallbackExtract.FinalURL, "tool", fallbackExtract.Tool, "content_chars", len(fallbackExtract.Content))
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

func runSummarizeWithRedirectRetry(ctx context.Context, source model.SourceDocument, opts Options, runOpts summarizecli.Options) (summarizecli.Result, error) {
	result, err := summarizecli.Run(ctx, runOpts)
	if err == nil {
		return result, nil
	}
	if !isRedirectFetchError(err) {
		return summarizecli.Result{}, err
	}
	if _, ok := sourceHost(runOpts.Input); !ok {
		return summarizecli.Result{}, err
	}

	resolveRedirectURL := opts.ResolveRedirectURL
	if resolveRedirectURL == nil {
		resolveRedirectURL = defaultResolveRedirectURL
	}

	resolveTimeout := 15 * time.Second
	if opts.Timeout > 0 && opts.Timeout < resolveTimeout {
		resolveTimeout = opts.Timeout
	}
	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	resolvedURL, resolveErr := resolveRedirectURL(resolveCtx, runOpts.Input)
	if resolveErr != nil {
		debugLog(opts.Logger, "source redirect resolution failed",
			"source_key", source.SourceKey,
			"url", source.CanonicalURL,
			"error", resolveErr.Error(),
		)
		return summarizecli.Result{}, err
	}
	resolvedURL = strings.TrimSpace(resolvedURL)
	if resolvedURL == "" || resolvedURL == runOpts.Input {
		return summarizecli.Result{}, err
	}

	debugLog(opts.Logger, "retrying source extraction after redirect resolution",
		"source_key", source.SourceKey,
		"url", source.CanonicalURL,
		"resolved_url", resolvedURL,
	)

	retryOpts := runOpts
	retryOpts.Input = resolvedURL
	return summarizecli.Run(ctx, retryOpts)
}

func needsEnrichment(source model.SourceDocument, opts Options, toolName string, toolVersion string) bool {
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
	if source.SummaryPromptVersion != SummaryPromptVersion {
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

func saveSourceFailure(ctx context.Context, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, extractToolVersion string, summaryToolVersion string) error {
	if strings.TrimSpace(extract.Tool) == "" {
		extract.Tool = summarizecli.ToolName
	}
	if strings.TrimSpace(extract.ToolVersion) == "" {
		extract.ToolVersion = extractToolVersion
	}
	if _, err := st.SaveSourceExtraction(ctx, source.ID, extract, source.ContentHash); err != nil {
		return err
	}
	if !opts.Summarize {
		return nil
	}

	summaryStatus := "error"
	if extract.Status != "error" {
		summaryStatus = "skipped"
	}
	_, err := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
		Status:        summaryStatus,
		Error:         extract.Error,
		Model:         opts.Model,
		PromptVersion: SummaryPromptVersion,
		Tool:          summarizecli.SummaryToolName(opts.Model),
		ToolVersion:   summaryToolVersion,
	})
	return err
}

func preflightTerminalSourceFailure(ctx context.Context, source model.SourceDocument, opts Options, toolVersion string) (model.ExtractResult, bool) {
	host, ok := sourceHost(source.CanonicalURL)
	if !ok {
		return model.ExtractResult{}, false
	}

	resolveHost := opts.ResolveHost
	if resolveHost == nil {
		resolveHost = defaultResolveHost
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := resolveHost(lookupCtx, host); err != nil {
		if isHostNotFoundError(err) {
			return model.ExtractResult{
				Status:      "dead",
				Error:       fmt.Sprintf("host does not resolve: %s", host),
				Tool:        summarizecli.ToolName,
				ToolVersion: toolVersion,
			}, true
		}
	}

	return model.ExtractResult{}, false
}

func sourceHost(rawURL string) (string, bool) {
	if strings.TrimSpace(rawURL) == "" {
		return "", false
	}
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", false
	}
	return host, true
}

func defaultResolveHost(ctx context.Context, host string) error {
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err
}

func defaultResolveRedirectURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create redirect resolution request: %w", err)
	}
	req.Header.Set("user-agent", "dbrain/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve redirect: %w", err)
	}
	defer func() {
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		_ = resp.Body.Close()
	}()

	if resp.Request == nil || resp.Request.URL == nil {
		return rawURL, nil
	}
	return resp.Request.URL.String(), nil
}

func isHostNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "no such host") || strings.Contains(value, "nxdomain")
}

func isRedirectFetchError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, status := range []string{"status 301", "status 302", "status 303", "status 307", "status 308"} {
		if strings.Contains(value, status) {
			return true
		}
	}
	return false
}

func classifyTerminalExtractError(source model.SourceDocument, err error) (string, string, bool) {
	if err == nil {
		return "", "", false
	}
	errorText := strings.TrimSpace(err.Error())
	value := strings.ToLower(errorText)
	switch {
	case strings.Contains(value, "status 404"),
		strings.Contains(value, "404 not found"),
		strings.Contains(value, "status 410"),
		strings.Contains(value, "410 gone"):
		return "gone", "", true
	default:
		kind := classifyExtractFailureKind(errorText)
		threshold := deadThresholdForFailureKind(kind)
		if threshold <= 0 {
			return "", "", false
		}
		nextCount := nextFailureCount(source, kind)
		if nextCount < threshold {
			return "", "", false
		}
		return "dead", fmt.Sprintf("marking source dead after %d consecutive %s failures: %s", nextCount, failureKindLabel(kind), errorText), true
	}
}

func classifyExtractFailureKind(errorText string) string {
	value := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case strings.Contains(value, "host does not resolve"),
		strings.Contains(value, "no such host"),
		strings.Contains(value, "nxdomain"):
		return "dns_nxdomain"
	case strings.Contains(value, "self signed certificate"),
		strings.Contains(value, "unable to verify the first certificate"),
		strings.Contains(value, "err_tls_cert_altname_invalid"),
		strings.Contains(value, "altname invalid"),
		strings.Contains(value, "x509"),
		strings.Contains(value, "certificate"):
		return "tls_certificate"
	case strings.Contains(value, "status 522"),
		strings.Contains(value, "status 523"),
		strings.Contains(value, "status 524"),
		strings.Contains(value, "status 525"),
		strings.Contains(value, "status 526"):
		return "cloudflare_edge"
	case strings.Contains(value, "x article returned an x error shell"):
		return "x_article_shell"
	case strings.Contains(value, "unable to connect"),
		strings.Contains(value, "connection refused"),
		strings.Contains(value, "network is unreachable"),
		strings.Contains(value, "no route to host"):
		return "connectivity"
	case strings.Contains(value, "status 502"),
		strings.Contains(value, "status 503"),
		strings.Contains(value, "status 504"):
		return "http_5xx"
	default:
		return ""
	}
}

func deadThresholdForFailureKind(kind string) int {
	switch kind {
	case "dns_nxdomain":
		return 1
	case "tls_certificate", "cloudflare_edge", "connectivity":
		return 3
	case "x_article_shell":
		return 3
	case "http_5xx":
		return 5
	default:
		return 0
	}
}

func nextFailureCount(source model.SourceDocument, kind string) int {
	if kind == "" {
		return 1
	}
	if source.ExtractFailureKind == kind && source.ExtractFailureCount > 0 {
		return source.ExtractFailureCount + 1
	}
	return 1
}

func failureKindLabel(kind string) string {
	switch kind {
	case "dns_nxdomain":
		return "dns resolution"
	case "tls_certificate":
		return "tls certificate"
	case "cloudflare_edge":
		return "cloudflare edge"
	case "connectivity":
		return "connectivity"
	case "x_article_shell":
		return "x article shell"
	case "http_5xx":
		return "http 5xx"
	default:
		return "terminal"
	}
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

func buildSummaryPrompt(source model.SourceDocument, extract model.ExtractResult) string {
	var b strings.Builder
	b.WriteString(summaryPrompt)

	contextLines := make([]string, 0, 3)
	if value := strings.TrimSpace(source.CanonicalURL); value != "" {
		contextLines = append(contextLines, "Source URL: "+value)
	}
	title := strings.TrimSpace(extract.Title)
	if title == "" {
		title = strings.TrimSpace(source.Title)
	}
	if title != "" {
		contextLines = append(contextLines, "Source Title: "+title)
	}
	site := strings.TrimSpace(extract.SiteName)
	if site == "" {
		site = strings.TrimSpace(source.SiteName)
	}
	if site == "" {
		site = strings.TrimSpace(source.Domain)
	}
	if site != "" {
		contextLines = append(contextLines, "Source Site: "+site)
	}

	if len(contextLines) == 0 {
		return b.String()
	}

	b.WriteString("\n\nAdditional context:\n")
	for _, line := range contextLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
}

func summarizeFromExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, toolVersion string) (bool, string, error) {
	summaryToolName := summarizecli.SummaryToolName(opts.Model)
	if reason, ok := skipSummaryReason(source, extract); ok {
		changed, err := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "skipped",
			Error:         reason,
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
			FetchedAt:     time.Now().UTC(),
		})
		if err == nil && changed {
			debugLog(opts.Logger, "source summary skipped", "source_key", source.SourceKey, "url", source.CanonicalURL, "reason", reason)
		}
		return changed, "skipped", err
	}

	input, cleanup, err := summaryInputFile(cfg, extract)
	if err != nil {
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "error",
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		})
		return changed, "error", saveErr
	}
	defer cleanup()
	if strings.TrimSpace(input) == "" {
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "blocked",
			Error:         "no extracted content available for summary",
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		})
		return changed, "blocked", saveErr
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     input,
		Summarize: true,
		Model:     opts.Model,
		CLI:       summaryCLI(opts),
		Prompt:    buildSummaryPrompt(source, extract),
		Length:    opts.Length,
		Timeout:   opts.Timeout,
	})
	if err != nil {
		if isUserCancellation(ctx, err) {
			return false, "", context.Canceled
		}
		status := "error"
		if reason, blocked := blockedSummaryReason(err); blocked {
			status = "blocked"
			err = errors.New(reason)
		}
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        status,
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		})
		return changed, status, saveErr
	}

	runResult.Summary.PromptVersion = SummaryPromptVersion
	changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary)
	if err == nil && changed && runResult.Summary.Status == "ok" {
		debugLog(opts.Logger, "source summary saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "summary_chars", len(runResult.Summary.Text), "model", runResult.Summary.Model, "tool", runResult.Summary.Tool)
	}
	return changed, runResult.Summary.Status, err
}

func summarizeExtract(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options, env map[string]string) (summarizecli.Result, error) {
	input, cleanup, err := summaryInputFile(cfg, extract)
	if err != nil {
		return summarizecli.Result{}, err
	}
	defer cleanup()

	return summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     input,
		Summarize: true,
		Model:     opts.Model,
		CLI:       summaryCLI(opts),
		Prompt:    buildSummaryPrompt(source, extract),
		Length:    opts.Length,
		Timeout:   opts.Timeout,
		Env:       env,
	})
}

func isUserCancellation(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return true
	}
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "signal: interrupt") || strings.Contains(message, "context canceled")
}

func summaryCLI(opts Options) string {
	return summarizecli.ResolveCLIProvider(opts.CLI, opts.Model)
}

func summaryInputFile(cfg config.Config, extract model.ExtractResult) (string, func(), error) {
	input := summaryInput(extract)
	if strings.TrimSpace(input) == "" {
		return "", func() {}, nil
	}

	file, err := cfg.CreateTemp("dbrain-summary-*.md")
	if err != nil {
		return "", nil, err
	}
	if _, err := file.WriteString(input); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() {
		_ = os.Remove(file.Name())
	}, nil
}

func summaryInput(extract model.ExtractResult) string {
	content := strings.TrimSpace(extract.Content)
	if content == "" {
		return ""
	}
	parts := make([]string, 0, 4)
	if title := strings.TrimSpace(extract.Title); title != "" {
		parts = append(parts, "Title: "+title)
	}
	if description := strings.TrimSpace(extract.Description); description != "" {
		parts = append(parts, "Description: "+description)
	}
	if siteName := strings.TrimSpace(extract.SiteName); siteName != "" {
		parts = append(parts, "Site: "+siteName)
	}
	parts = append(parts, content)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func canSummarizeStoredExtract(source model.SourceDocument) bool {
	if source.ExtractStatus != "ok" {
		return false
	}
	return strings.TrimSpace(source.ExtractedText) != ""
}

const minXArticlePreviewExtractChars = 300

func isShortXArticlePreviewExtract(extract model.ExtractResult) bool {
	return extract.Tool == "x-hydration" &&
		extract.ToolVersion == "local-article-preview-cache" &&
		len(strings.TrimSpace(extract.Content)) < minXArticlePreviewExtractChars
}

func shouldRetryRemoteAfterLocalExtractReject(source model.SourceDocument, extract model.ExtractResult, failure model.ExtractResult) bool {
	if !isXArticleURL(firstNonEmpty(source.CanonicalURL, extract.CanonicalURL, extract.FinalURL)) {
		return false
	}
	if failure.Status != "error" {
		return false
	}
	if looksLikeXArticleErrorShell(extract.Content) {
		return false
	}
	return isShortXArticlePreviewExtract(extract)
}

func rejectExtractFailure(source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool) {
	if !isXArticleURL(firstNonEmpty(source.CanonicalURL, extract.CanonicalURL, extract.FinalURL)) {
		if looksLikeSubstackSubscriptionShell(extract.Content) {
			return model.ExtractResult{
				Status:      "empty",
				Error:       "substack returned subscription boilerplate instead of article content",
				Tool:        extract.Tool,
				ToolVersion: extract.ToolVersion,
			}, true
		}
		if looksLikeSubstackInboxNavigationShell(extract.Content) {
			return model.ExtractResult{
				Status:      "empty",
				Error:       "substack returned inbox/navigation chrome instead of article content",
				Tool:        extract.Tool,
				ToolVersion: extract.ToolVersion,
			}, true
		}
		return model.ExtractResult{}, false
	}
	if looksLikeXArticleErrorShell(extract.Content) {
		return model.ExtractResult{
			Status:      "error",
			Error:       "x article returned an X error shell instead of article content",
			Tool:        extract.Tool,
			ToolVersion: extract.ToolVersion,
		}, true
	}
	if isShortXArticlePreviewExtract(extract) {
		return model.ExtractResult{
			Status:      "error",
			Error:       fmt.Sprintf("x article hydration only exposed a short preview snippet (%d chars) instead of article content", len(strings.TrimSpace(extract.Content))),
			Tool:        extract.Tool,
			ToolVersion: extract.ToolVersion,
		}, true
	}
	return model.ExtractResult{}, false
}

func normalizeExtract(source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool) {
	cleaned := stripKnownPaywallNoise(extract.Content)
	if strings.TrimSpace(cleaned) == strings.TrimSpace(extract.Content) {
		return extract, false
	}
	normalized := extract
	normalized.Content = cleaned
	return normalized, true
}

func stripKnownPaywallNoise(content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isKnownPaywallNoiseLine(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isKnownPaywallNoiseLine(line string) bool {
	value := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(value, "continue reading this post for free"):
		return true
	case strings.HasPrefix(value, "continue reading this post"):
		return true
	case strings.HasPrefix(value, "or purchase a paid subscription"):
		return true
	default:
		return false
	}
}

func looksLikeSubstackSubscriptionShell(content string) bool {
	value := strings.ToLower(strings.TrimSpace(content))
	if value == "" || len(value) > 600 {
		return false
	}
	return strings.Contains(value, "by subscribing, you agree substack's terms of use") &&
		strings.Contains(value, "information collection notice") &&
		strings.Contains(value, "privacy policy")
}

func looksLikeSubstackInboxNavigationShell(content string) bool {
	value := strings.ToLower(strings.TrimSpace(content))
	if value == "" || len(value) > 400 {
		return false
	}
	compact := compactAlphaNumeric(value)
	return strings.Contains(compact, "homesubscriptionschatactivityexploreprofilecreatealllistenpaidsavedhistorysortbypriorityrecentgetapp")
}

func compactAlphaNumeric(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func extractFromSource(source model.SourceDocument) model.ExtractResult {
	return model.ExtractResult{
		CanonicalURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        source.Title,
		Description:  source.Description,
		SiteName:     source.SiteName,
		Content:      source.ExtractedText,
		RawJSON:      source.ExtractJSON,
		Status:       source.ExtractStatus,
		Error:        source.ExtractError,
		FetchedAt:    source.ExtractedAt,
		Tool:         source.ExtractTool,
		ToolVersion:  source.ExtractToolVersion,
	}
}

type youtubeExtractEnvelope struct {
	Extracted struct {
		TranscriptSource      *string `json:"transcriptSource"`
		TranscriptionProvider *string `json:"transcriptionProvider"`
		TranscriptCharacters  *int    `json:"transcriptCharacters"`
	} `json:"extracted"`
}

func shouldUseAudioTranscriptionFallback(extract model.ExtractResult) bool {
	var payload youtubeExtractEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(extract.RawJSON)), &payload); err != nil {
		return false
	}

	transcriptSource := ""
	if payload.Extracted.TranscriptSource != nil {
		transcriptSource = strings.TrimSpace(*payload.Extracted.TranscriptSource)
	}
	transcriptionProvider := ""
	if payload.Extracted.TranscriptionProvider != nil {
		transcriptionProvider = strings.TrimSpace(*payload.Extracted.TranscriptionProvider)
	}
	transcriptChars := 0
	if payload.Extracted.TranscriptCharacters != nil {
		transcriptChars = *payload.Extracted.TranscriptCharacters
	}

	return transcriptSource == "unavailable" && transcriptionProvider == "" && transcriptChars == 0
}

func MaybeTranscribeYouTubeAudioFallback(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options) (model.ExtractResult, bool, error) {
	if source.SourceType != "youtube" {
		return model.ExtractResult{}, false, nil
	}
	if !shouldUseAudioTranscriptionFallback(extract) {
		return model.ExtractResult{}, false, nil
	}

	fallback, err := transcribeYouTubeAudioFallback(ctx, cfg, source, extract, opts)
	if err != nil {
		return model.ExtractResult{}, false, err
	}
	return fallback, true, nil
}

func transcribeYouTubeAudioFallback(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options) (model.ExtractResult, error) {
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		opts.WhisperModelPath = defaultWhisperModelPath()
	}
	if strings.TrimSpace(opts.MacWhisperBinary) == "" {
		opts.MacWhisperBinary = "mw"
	}

	timeout := opts.Timeout
	if timeout < 10*time.Minute {
		timeout = 10 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempDir, err := cfg.MkdirTemp("dbrain-youtube-whisper-*")
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	audioTemplate := filepath.Join(tempDir, "audio.%(ext)s")
	downloadArgs := []string{
		"--no-playlist",
		"--cookies-from-browser", firstNonEmpty(strings.TrimSpace(opts.YouTubeCookiesArg), cookiesFromBrowserArg(opts.YouTubeBrowser, opts.YouTubeProfile)),
		"-f", "bestaudio/best",
		"-o", audioTemplate,
		source.CanonicalURL,
	}
	downloadCmd := exec.CommandContext(commandCtx, opts.YTDLPBinary, downloadArgs...)
	var downloadStderr bytes.Buffer
	downloadCmd.Stderr = &downloadStderr
	if err := downloadCmd.Run(); err != nil {
		return model.ExtractResult{}, fmt.Errorf("yt-dlp audio download: %s", strings.TrimSpace(downloadStderr.String()))
	}

	audioPath, err := firstDownloadedAudio(tempDir)
	if err != nil {
		return model.ExtractResult{}, err
	}

	transcript, provider, err := transcribeAudioFile(commandCtx, audioPath, opts)
	if err != nil {
		return model.ExtractResult{}, err
	}

	title := strings.TrimSpace(extract.Title)
	if title == "" || title == "- YouTube" {
		title = strings.TrimSpace(source.Title)
	}
	description := strings.TrimSpace(extract.Description)
	if description == "" {
		description = strings.TrimSpace(source.Description)
	}
	siteName := strings.TrimSpace(extract.SiteName)
	if siteName == "" || siteName == "youtube.com" {
		siteName = "YouTube"
	}

	rawJSONBytes, err := json.Marshal(map[string]any{
		"extracted": map[string]any{
			"url":                   source.CanonicalURL,
			"title":                 title,
			"description":           description,
			"siteName":              siteName,
			"content":               "Transcript:\n" + transcript,
			"transcriptSource":      provider,
			"transcriptionProvider": provider,
			"transcriptCharacters":  len(transcript),
		},
	})
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("marshal whisper transcript json: %w", err)
	}

	return model.ExtractResult{
		CanonicalURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        title,
		Description:  description,
		SiteName:     siteName,
		Content:      "Transcript:\n" + transcript,
		RawJSON:      string(rawJSONBytes),
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         provider,
		ToolVersion:  "",
	}, nil
}

func transcribeAudioFile(ctx context.Context, audioPath string, opts Options) (string, string, error) {
	if shouldUseMacWhisper(opts.YouTubeTranscriber, opts.MacWhisperBinary) {
		transcript, err := transcribeAudioWithMacWhisper(ctx, audioPath, opts)
		if err == nil {
			return transcript, "macwhisper", nil
		}
		if explicitMacWhisper(opts.YouTubeTranscriber) {
			return "", "", err
		}
		debugLog(opts.Logger, "macwhisper transcription failed; falling back to whisper.cpp", "audio_path", audioPath, "error", err.Error())
	}

	transcript, err := transcribeAudioWithWhisperCLI(ctx, audioPath, opts)
	if err != nil {
		return "", "", err
	}
	return transcript, "whisper.cpp", nil
}

func transcribeAudioWithWhisperCLI(ctx context.Context, audioPath string, opts Options) (string, error) {
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		return "", fmt.Errorf("whisper model path not configured")
	}
	if _, err := os.Stat(opts.WhisperModelPath); err != nil {
		return "", fmt.Errorf("whisper model missing: %w", err)
	}

	outputBase := filepath.Join(filepath.Dir(audioPath), "transcript")
	whisperArgs := []string{
		"-m", opts.WhisperModelPath,
		"-l", "auto",
		"-otxt",
		"-of", outputBase,
		"-f", audioPath,
		"-np",
		"-nt",
	}
	whisperCmd := exec.CommandContext(ctx, opts.WhisperBinary, whisperArgs...)
	var whisperStderr bytes.Buffer
	whisperCmd.Stderr = &whisperStderr
	if err := whisperCmd.Run(); err != nil {
		return "", fmt.Errorf("whisper transcription: %s", strings.TrimSpace(whisperStderr.String()))
	}

	transcriptBytes, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return "", fmt.Errorf("read whisper transcript: %w", err)
	}
	transcript := strings.TrimSpace(string(transcriptBytes))
	if transcript == "" {
		return "", fmt.Errorf("whisper transcript empty")
	}
	return transcript, nil
}

func transcribeAudioWithMacWhisper(ctx context.Context, audioPath string, opts Options) (string, error) {
	args := []string{"transcribe"}
	if modelID := macWhisperModelOverride(opts.YouTubeTranscriber); modelID != "" {
		args = append(args, "--model", modelID)
	}
	args = append(args, audioPath)

	cmd := exec.CommandContext(ctx, opts.MacWhisperBinary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("macwhisper transcription: %s", errMsg)
	}

	transcript := strings.TrimSpace(stdout.String())
	if transcript == "" {
		return "", fmt.Errorf("macwhisper transcript empty")
	}
	return transcript, nil
}

func shouldUseMacWhisper(transcriber string, binary string) bool {
	if explicitMacWhisper(transcriber) {
		return true
	}
	if value := strings.TrimSpace(transcriber); value != "" && !strings.EqualFold(value, "auto") {
		return false
	}
	_, err := exec.LookPath(strings.TrimSpace(binary))
	return err == nil
}

func explicitMacWhisper(transcriber string) bool {
	value := strings.ToLower(strings.TrimSpace(transcriber))
	return value == "macwhisper" || strings.HasPrefix(value, "macwhisper:")
}

func macWhisperModelOverride(transcriber string) string {
	value := strings.TrimSpace(transcriber)
	if !strings.HasPrefix(strings.ToLower(value), "macwhisper:") {
		return ""
	}
	return strings.TrimSpace(value[len("macwhisper:"):])
}

func firstDownloadedAudio(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read audio dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") || strings.HasSuffix(name, ".txt") {
			continue
		}
		return filepath.Join(dir, name), nil
	}
	return "", fmt.Errorf("downloaded audio not found")
}

func defaultWhisperModelPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".summarize", "cache", "whisper-cpp", "models", "ggml-base.bin")
}

func cookiesFromBrowserArg(browser, profile string) string {
	browser = strings.TrimSpace(browser)
	if browser == "" {
		browser = "chrome"
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return browser
	}
	return browser + ":" + profile
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isXArticleURL(rawURL string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "x.com" && host != "www.x.com" && host != "twitter.com" && host != "www.twitter.com" {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(parsed.EscapedPath()))
	return strings.Contains(path, "/i/article/") || strings.Contains(path, "/article/")
}

func looksLikeXArticleErrorShell(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "something went wrong") &&
		strings.Contains(normalized, "privacy related extensions may cause issues on x.com")
}

func skipSummaryReason(source model.SourceDocument, extract model.ExtractResult) (string, bool) {
	if reason, ok := genericSkipSummaryReason(extract); ok {
		return reason, true
	}
	if source.SourceType != "youtube" {
		return "", false
	}
	if strings.TrimSpace(extract.RawJSON) == "" {
		return "", false
	}

	var payload youtubeExtractEnvelope
	if err := json.Unmarshal([]byte(extract.RawJSON), &payload); err != nil {
		return "", false
	}

	transcriptSource := ""
	if payload.Extracted.TranscriptSource != nil {
		transcriptSource = strings.TrimSpace(*payload.Extracted.TranscriptSource)
	}
	transcriptionProvider := ""
	if payload.Extracted.TranscriptionProvider != nil {
		transcriptionProvider = strings.TrimSpace(*payload.Extracted.TranscriptionProvider)
	}
	transcriptChars := 0
	if payload.Extracted.TranscriptCharacters != nil {
		transcriptChars = *payload.Extracted.TranscriptCharacters
	}

	if transcriptChars > 0 || transcriptionProvider != "" {
		return "", false
	}
	if transcriptSource == "captionTracks" || transcriptSource == "youtubei" {
		return "", false
	}
	if transcriptSource == "unavailable" && len(strings.TrimSpace(extract.Content)) <= 200 {
		return "youtube transcript unavailable and no audio transcription was produced", true
	}
	return "", false
}

func genericSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	content := strings.TrimSpace(extract.Content)
	if content == "" {
		return "", false
	}
	if looksLikePlaceholderExtractContent(content) {
		return "extracted content appears to be redirect/login/placeholder boilerplate rather than substantive content", true
	}
	return "", false
}

func looksLikePlaceholderExtractContent(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	if normalized == "" {
		return false
	}
	switch {
	case strings.Contains(normalized, "redirecting"),
		strings.Contains(normalized, "you will be redirected"),
		strings.Contains(normalized, "if you are not redirected automatically"),
		strings.Contains(normalized, "loading..."),
		strings.Contains(normalized, "coming soon"),
		strings.Contains(normalized, "<div></div>"),
		strings.Contains(normalized, "we use cookies to improve user experience"),
		strings.Contains(normalized, "nothing to see here"),
		strings.Contains(normalized, "google drive"):
		return len(normalized) <= 160
	case strings.Contains(normalized, "sign in or sign up"),
		strings.Contains(normalized, "you are not logged in"),
		strings.Contains(normalized, "manage account"),
		strings.Contains(normalized, "your profile"),
		strings.Contains(normalized, "continue with google"),
		strings.Contains(normalized, "continue with github"),
		strings.Contains(normalized, "open full screen to view more"),
		strings.Contains(normalized, "google apps"):
		return len(normalized) <= 300
	default:
		return false
	}
}

func blockedSummaryReason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(value, "maximum context length"),
		strings.Contains(value, "context length"),
		strings.Contains(value, "too many tokens"),
		strings.Contains(value, "input is too long"):
		return err.Error(), true
	default:
		return "", false
	}
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

func selectSourceDocuments(ordered []int64, byID map[int64]model.SourceDocument, opts Options, toolName string, toolVersion string) []model.SourceDocument {
	filtered := make([]model.SourceDocument, 0, len(ordered))
	for _, sourceID := range ordered {
		source, ok := byID[sourceID]
		if !ok {
			continue
		}
		if !opts.Force && !needsEnrichment(source, opts, toolName, toolVersion) {
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
