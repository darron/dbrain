package sourceenrich

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	Limit              int
	Concurrency        int
	Force              bool
	Summarize          bool
	Model              string
	CLI                string
	Length             string
	Timeout            time.Duration
	Logger             *slog.Logger
	EnvFor             func(source model.SourceDocument) map[string]string
	ArgsFor            func(source model.SourceDocument) []string
	ResolveHost        func(ctx context.Context, host string) error
	FallbackExtractFor func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error)
	Binary             string
	YouTubeBrowser     string
	YouTubeProfile     string
	YouTubeTranscriber string
	YTDLPBinary        string
	WhisperBinary      string
	WhisperModelPath   string
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
	toolVersion := summarizecli.Version(ctx, opts.Binary)
	sources, err := st.ListSourcesForEnrichment(ctx, opts.Limit, opts.Force, opts.Summarize, SummaryPromptVersion, summarizecli.ToolName, toolVersion)
	if err != nil {
		return Stats{}, nil, err
	}
	return runSources(ctx, cfg, st, sources, opts, toolVersion)
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

	toolVersion := summarizecli.Version(ctx, opts.Binary)
	filtered := selectSourceDocuments(ordered, byID, opts, toolVersion)

	return runSources(ctx, cfg, st, filtered, opts, toolVersion)
}

func runSources(ctx context.Context, cfg config.Config, st *store.Store, sources []model.SourceDocument, opts Options, toolVersion string) (Stats, []int64, error) {
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

	stats := Stats{SourcesQueued: len(sources)}
	touchedSourceIDs := map[int64]struct{}{}

	debugLog(opts.Logger, "source enrichment candidates loaded", "sources", len(sources), "limit", opts.Limit, "summarize", opts.Summarize, "concurrency", opts.Concurrency)

	results, err := processSourcesConcurrently(ctx, st, sources, opts, toolVersion)
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
	if err != nil {
		return stats, nil, err
	}

	orderedSourceIDs := uniqueSorted(mapKeys(touchedSourceIDs))
	for _, sourceID := range orderedSourceIDs {
		source, err := st.GetSourceByID(ctx, sourceID)
		if err != nil {
			return stats, nil, err
		}
		backlinks, err := st.ListBacklinksForSource(ctx, sourceID)
		if err != nil {
			return stats, nil, err
		}
		if err := vault.WriteSource(cfg, source, backlinks); err != nil {
			return stats, nil, err
		}
		stats.SourcesRendered++
	}

	return stats, orderedSourceIDs, nil
}

type sourceProcessResult struct {
	Stats           Stats
	TouchedSourceID int64
	Err             error
}

func processSourcesConcurrently(ctx context.Context, st *store.Store, sources []model.SourceDocument, opts Options, toolVersion string) ([]sourceProcessResult, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	if opts.Concurrency <= 1 || len(sources) == 1 {
		results := make([]sourceProcessResult, 0, len(sources))
		for _, source := range sources {
			result := processSingleSource(ctx, st, source, opts, toolVersion)
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
				result := processSingleSource(ctx, st, source, opts, toolVersion)
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

func processSingleSource(ctx context.Context, st *store.Store, source model.SourceDocument, opts Options, toolVersion string) sourceProcessResult {
	debugLog(opts.Logger, "enriching source", "source_key", source.SourceKey, "url", source.CanonicalURL)

	result := sourceProcessResult{}
	sourceArgs := argsFor(opts, source)
	sourceEnv := envFor(opts, source)
	localExtract, hasLocalExtract, err := st.GetPreferredLocalSourceExtract(ctx, source.ID)
	if err != nil {
		result.Err = err
		return result
	}
	if hasLocalExtract {
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
			runResult, err := summarizeExtract(ctx, source, localExtract, opts, sourceEnv)
			if err != nil {
				result.Stats.Errors++
				debugLog(opts.Logger, "local source summarization failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
				if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
					Status:        "error",
					Error:         err.Error(),
					Model:         opts.Model,
					PromptVersion: SummaryPromptVersion,
					Tool:          summarizecli.ToolName,
					ToolVersion:   toolVersion,
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

	if opts.Summarize && !opts.Force && canSummarizeStoredExtract(source) {
		storedExtract := extractFromSource(source)
		debugLog(opts.Logger, "using stored extract for summary", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(storedExtract.Content))
		if changed, err := summarizeFromExtract(ctx, st, source, storedExtract, opts, toolVersion); err != nil {
			result.Err = err
			return result
		} else if changed {
			result.Stats.SourcesSummarized++
		}
		result.TouchedSourceID = source.ID
		return result
	}

	if failure, terminal := preflightTerminalSourceFailure(ctx, source, opts, toolVersion); terminal {
		result.Stats.Errors++
		debugLog(opts.Logger, "source preflight failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "error", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, toolVersion); err != nil {
			result.Err = err
			return result
		}
		result.TouchedSourceID = source.ID
		return result
	}

	if opts.Summarize && len(sourceArgs) > 0 {
		extractResult, err := summarizecli.Run(ctx, summarizecli.Options{
			Binary:    opts.Binary,
			Input:     source.CanonicalURL,
			Summarize: false,
			Length:    opts.Length,
			Timeout:   opts.Timeout,
			Env:       sourceEnv,
			Args:      sourceArgs,
		})
		if err != nil {
			result.Stats.Errors++
			debugLog(opts.Logger, "source extraction failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			failure := model.ExtractResult{
				Status:      "error",
				Error:       err.Error(),
				Tool:        summarizecli.ToolName,
				ToolVersion: toolVersion,
			}
			if status, errorText, terminal := classifyTerminalExtractError(source, err); terminal {
				failure.Status = status
				if errorText != "" {
					failure.Error = errorText
				}
			}
			if err := saveSourceFailure(ctx, st, source, failure, opts, toolVersion); err != nil {
				result.Err = err
				return result
			}
			result.TouchedSourceID = source.ID
			return result
		}
		if fallback, changed, err := fallbackExtract(ctx, opts, source, extractResult.Extract); err != nil {
			result.Err = err
			return result
		} else if changed {
			extractResult.Extract = fallback
		}

		contentHash := hashText(extractResult.Extract.Content)
		if changed, err := st.SaveSourceExtraction(ctx, source.ID, extractResult.Extract, contentHash); err != nil {
			result.Err = err
			return result
		} else if changed {
			result.Stats.SourcesExtracted++
			debugLog(opts.Logger, "source extraction saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", extractResult.Extract.Status, "content_chars", len(extractResult.Extract.Content), "tool", extractResult.Extract.Tool)
		} else {
			result.Stats.SourcesUnchanged++
		}

		if changed, err := summarizeFromExtract(ctx, st, source, extractResult.Extract, opts, toolVersion); err != nil {
			result.Err = err
			return result
		} else if changed {
			result.Stats.SourcesSummarized++
		}

		result.TouchedSourceID = source.ID
		return result
	}

	cli := opts.CLI
	if opts.Summarize {
		cli = summaryCLI(opts)
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
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
		result.Stats.Errors++
		debugLog(opts.Logger, "source enrichment failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
		failure := model.ExtractResult{
			Status:      "error",
			Error:       err.Error(),
			Tool:        summarizecli.ToolName,
			ToolVersion: toolVersion,
		}
		if status, errorText, terminal := classifyTerminalExtractError(source, err); terminal {
			failure.Status = status
			if errorText != "" {
				failure.Error = errorText
			}
		}
		if err := saveSourceFailure(ctx, st, source, failure, opts, toolVersion); err != nil {
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

func saveSourceFailure(ctx context.Context, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, toolVersion string) error {
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
		Tool:          summarizecli.ToolName,
		ToolVersion:   toolVersion,
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
			"SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER": cookiesFromBrowserArg(opts.YouTubeBrowser, opts.YouTubeProfile),
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

func fallbackExtract(ctx context.Context, opts Options, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error) {
	if opts.FallbackExtractFor == nil {
		if source.SourceType != "youtube" {
			return model.ExtractResult{}, false, nil
		}
		if !shouldUseWhisperFallback(extract) {
			return model.ExtractResult{}, false, nil
		}
		debugLog(opts.Logger, "attempting whisper fallback for youtube source", "source_key", source.SourceKey, "url", source.CanonicalURL)
		fallback, err := transcribeYouTubeWithWhisper(ctx, source, extract, opts)
		if err != nil {
			debugLog(opts.Logger, "whisper fallback failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			return model.ExtractResult{}, false, nil
		}
		debugLog(opts.Logger, "whisper fallback succeeded", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(fallback.Content))
		return fallback, true, nil
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

func summarizeFromExtract(ctx context.Context, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, toolVersion string) (bool, error) {
	if reason, ok := skipSummaryReason(source, extract); ok {
		changed, err := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "skipped",
			Error:         reason,
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
			FetchedAt:     time.Now().UTC(),
		})
		if err == nil && changed {
			debugLog(opts.Logger, "source summary skipped", "source_key", source.SourceKey, "url", source.CanonicalURL, "reason", reason)
		}
		return changed, err
	}

	input, cleanup, err := summaryInputFile(extract)
	if err != nil {
		return st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "error",
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
		})
	}
	defer cleanup()
	if strings.TrimSpace(input) == "" {
		return st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "error",
			Error:         "no extracted content available for summary",
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
		})
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
		return st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        "error",
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summarizecli.ToolName,
			ToolVersion:   toolVersion,
		})
	}

	runResult.Summary.PromptVersion = SummaryPromptVersion
	changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary)
	if err == nil && changed && runResult.Summary.Status == "ok" {
		debugLog(opts.Logger, "source summary saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "summary_chars", len(runResult.Summary.Text), "model", runResult.Summary.Model, "tool", runResult.Summary.Tool)
	}
	return changed, err
}

func summarizeExtract(ctx context.Context, source model.SourceDocument, extract model.ExtractResult, opts Options, env map[string]string) (summarizecli.Result, error) {
	input, cleanup, err := summaryInputFile(extract)
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

func summaryCLI(opts Options) string {
	if value := strings.TrimSpace(opts.CLI); value != "" {
		return value
	}
	if strings.TrimSpace(opts.Model) != "" {
		return ""
	}
	return summarizecli.PreferredCLIProvider()
}

func summaryInputFile(extract model.ExtractResult) (string, func(), error) {
	input := summaryInput(extract)
	if strings.TrimSpace(input) == "" {
		return "", func() {}, nil
	}

	file, err := os.CreateTemp("", "dbrain-summary-*.md")
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
	if content := strings.TrimSpace(extract.Content); content != "" {
		parts = append(parts, content)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func canSummarizeStoredExtract(source model.SourceDocument) bool {
	if source.ExtractStatus != "ok" {
		return false
	}
	return strings.TrimSpace(source.ExtractedText) != ""
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

func shouldUseWhisperFallback(extract model.ExtractResult) bool {
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

func transcribeYouTubeWithWhisper(ctx context.Context, source model.SourceDocument, extract model.ExtractResult, opts Options) (model.ExtractResult, error) {
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		return model.ExtractResult{}, fmt.Errorf("whisper model path not configured")
	}
	if _, err := os.Stat(opts.WhisperModelPath); err != nil {
		return model.ExtractResult{}, fmt.Errorf("whisper model missing: %w", err)
	}

	timeout := opts.Timeout
	if timeout < 10*time.Minute {
		timeout = 10 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempDir, err := os.MkdirTemp("", "dbrain-youtube-whisper-*")
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	audioTemplate := filepath.Join(tempDir, "audio.%(ext)s")
	downloadArgs := []string{
		"--no-playlist",
		"--cookies-from-browser", cookiesFromBrowserArg(opts.YouTubeBrowser, opts.YouTubeProfile),
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

	outputBase := filepath.Join(tempDir, "transcript")
	whisperArgs := []string{
		"-m", opts.WhisperModelPath,
		"-l", "auto",
		"-otxt",
		"-of", outputBase,
		"-f", audioPath,
		"-np",
		"-nt",
	}
	whisperCmd := exec.CommandContext(commandCtx, opts.WhisperBinary, whisperArgs...)
	var whisperStderr bytes.Buffer
	whisperCmd.Stderr = &whisperStderr
	if err := whisperCmd.Run(); err != nil {
		return model.ExtractResult{}, fmt.Errorf("whisper transcription: %s", strings.TrimSpace(whisperStderr.String()))
	}

	transcriptBytes, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("read whisper transcript: %w", err)
	}
	transcript := strings.TrimSpace(string(transcriptBytes))
	if transcript == "" {
		return model.ExtractResult{}, fmt.Errorf("whisper transcript empty")
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
			"transcriptSource":      "whisper.cpp",
			"transcriptionProvider": "whisper.cpp",
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
		Tool:         "whisper.cpp",
		ToolVersion:  "",
	}, nil
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

func skipSummaryReason(source model.SourceDocument, extract model.ExtractResult) (string, bool) {
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

func selectSourceDocuments(ordered []int64, byID map[int64]model.SourceDocument, opts Options, toolVersion string) []model.SourceDocument {
	filtered := make([]model.SourceDocument, 0, len(ordered))
	for _, sourceID := range ordered {
		source, ok := byID[sourceID]
		if !ok {
			continue
		}
		if !opts.Force && !needsEnrichment(source, opts, summarizecli.ToolName, toolVersion) {
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
