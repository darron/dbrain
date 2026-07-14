package sourceenrich

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

func summaryFreshnessTarget(ctx context.Context, opts Options) (string, string, string) {
	if !opts.ExactSummaryFreshness {
		return "", "", ""
	}
	return SummaryPromptVersion, summarizecli.SummaryToolName(opts.Model), summarizecli.SummaryToolVersion(ctx, opts.Binary, opts.Model)
}

func runSummarizeWithRedirectRetry(ctx context.Context, source model.SourceDocument, opts Options, runOpts summarizecli.Options) (summarizecli.Result, error) {
	originalInput := strings.TrimSpace(runOpts.Input)
	if err := validateImportedSourceURL(originalInput); err != nil {
		return summarizecli.Result{}, err
	}
	preparedInput := ""
	preparedFinalURL := ""
	preparedContentType := ""
	if _, ok := sourceHost(originalInput); ok {
		if source.SourceType == "youtube" {
			if err := validateYouTubeSubprocessURL(originalInput); err != nil {
				return summarizecli.Result{}, err
			}
		} else {
			var err error
			var prepared preparedSourceInput
			if opts.prepareSourceInput != nil {
				prepared, err = opts.prepareSourceInput(ctx, originalInput)
			} else {
				prepared, err = preparePublicSourceInput(ctx, originalInput, opts)
			}
			if err != nil {
				return summarizecli.Result{}, err
			}
			preparedInput = prepared.Path
			preparedFinalURL = prepared.FinalURL
			preparedContentType = prepared.ContentType
			if prepared.Cleanup != nil {
				defer prepared.Cleanup()
			}
			if err := ctx.Err(); err != nil {
				return summarizecli.Result{}, err
			}
			runOpts.Input = preparedInput
			preparedExtract, handled, err := extractPreparedTextSource(originalInput, preparedFinalURL, preparedContentType, preparedInput)
			if err != nil {
				return summarizecli.Result{}, err
			}
			if handled {
				if !runOpts.Summarize {
					return summarizecli.Result{Extract: preparedExtract}, nil
				}
				if strings.TrimSpace(preparedExtract.Content) == "" {
					return summarizecli.Result{
						Extract: preparedExtract,
						Summary: model.SummaryResult{
							Status:      model.SourceSummaryStatusBlocked,
							Error:       "no extracted content available for summary",
							Model:       runOpts.Model,
							FetchedAt:   time.Now().UTC(),
							Tool:        summarizecli.SummaryToolNameForRoot(runOpts.RootDir, runOpts.Model),
							ToolVersion: summarizecli.SummaryToolVersionForRoot(ctx, runOpts.RootDir, runOpts.Binary, runOpts.Model),
						},
					}, nil
				}
				summaryOpts := runOpts
				summaryOpts.Input = "-"
				summaryOpts.Stdin = preparedExtract.Content
				result, err := summarizecli.Run(ctx, summaryOpts)
				if err != nil {
					return summarizecli.Result{}, err
				}
				result.Extract = preparedExtract
				return result, nil
			}
		}
	}

	result, err := summarizecli.Run(ctx, runOpts)
	if err == nil {
		if preparedInput != "" {
			result.Extract.CanonicalURL = originalInput
			result.Extract.FinalURL = preparedFinalURL
		}
		return result, nil
	}
	if preparedInput != "" {
		return summarizecli.Result{}, err
	}
	if !isRedirectFetchError(err) {
		return summarizecli.Result{}, err
	}
	if _, ok := sourceHost(runOpts.Input); !ok {
		return summarizecli.Result{}, err
	}

	resolveRedirectURL := opts.ResolveRedirectURL
	if resolveRedirectURL == nil {
		resolveRedirectURL = func(ctx context.Context, rawURL string) (string, error) {
			return defaultResolveRedirectURL(ctx, rawURL, opts)
		}
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
	if source.SourceType == "youtube" {
		if policyErr := validateYouTubeSubprocessURL(resolvedURL); policyErr != nil {
			return summarizecli.Result{}, policyErr
		}
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

func validateImportedSourceURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return &safehttp.PolicyError{Reason: "imported source must be an HTTP or HTTPS URL"}
	}
	if parsed.User != nil {
		return &safehttp.PolicyError{Reason: "imported source URL userinfo is not allowed"}
	}
	return nil
}

func preparePublicSourceInput(ctx context.Context, rawURL string, opts Options) (preparedSourceInput, error) {
	client := newPublicHTTPClient(opts)
	if sameHTTPOrigin(rawURL, opts.configuredSourceOrigin) {
		client = newConfiguredOriginHTTPClient(opts.configuredSourceOrigin, opts)
	}
	resp, body, err := fetchHTTPText(ctx, client, rawURL)
	if err != nil {
		return preparedSourceInput{}, fmt.Errorf("fetch source for local parsing: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return preparedSourceInput{}, fmt.Errorf("fetch source for local parsing: unexpected status %d", resp.StatusCode)
	}

	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	suffix := sourceInputSuffix(finalURL)
	file, err := os.CreateTemp("", "dbrain-source-input-*"+suffix)
	if err != nil {
		return preparedSourceInput{}, fmt.Errorf("create local source input: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		cleanup()
		return preparedSourceInput{}, fmt.Errorf("write local source input: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return preparedSourceInput{}, fmt.Errorf("close local source input: %w", err)
	}
	return preparedSourceInput{
		Path:        path,
		FinalURL:    finalURL,
		ContentType: resp.Header.Get("content-type"),
		Cleanup:     cleanup,
	}, nil
}

func extractPreparedTextSource(originalURL string, finalURL string, declaredContentType string, path string) (model.ExtractResult, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("read local source input: %w", err)
	}
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	detectedContentType := strings.ToLower(http.DetectContentType(sniff))
	declaredContentType = strings.ToLower(strings.TrimSpace(declaredContentType))
	snippet := strings.ToLower(string(sniff))
	isHTML := strings.Contains(declaredContentType, "html") ||
		strings.Contains(detectedContentType, "html") ||
		strings.Contains(snippet, "<html") ||
		strings.Contains(snippet, "<!doctype html")
	isText := isTextContentType(declaredContentType) || isTextContentType(detectedContentType)
	if !isHTML && !isText {
		return model.ExtractResult{}, false, nil
	}

	parsedFinalURL, err := url.Parse(finalURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("parse final source URL: %w", err)
	}
	resp := &http.Response{
		Header:  make(http.Header),
		Request: &http.Request{URL: parsedFinalURL},
	}
	contentType := firstNonEmpty(declaredContentType, detectedContentType)
	resp.Header.Set("content-type", contentType)
	body := string(data)
	if isHTML {
		return extractHTMLSource(originalURL, resp, body, "http-html", httpReaderToolVersion, ""), true, nil
	}
	return extractPlainTextSource(originalURL, resp, body, "http-text", httpReaderToolVersion), true, nil
}

func isTextContentType(contentType string) bool {
	return strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml")
}

func sameHTTPOrigin(left string, right string) bool {
	leftOrigin, leftOK := normalizedHTTPOrigin(left)
	rightOrigin, rightOK := normalizedHTTPOrigin(right)
	return leftOK && rightOK && leftOrigin == rightOrigin
}

func normalizedHTTPOrigin(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.User != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", false
	}
	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), true
}

func sourceInputSuffix(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := filepath.Ext(parsed.Path)
	if len(ext) > 12 || strings.ContainsAny(ext, `/\\`) {
		return ""
	}
	return ext
}

func validateYouTubeSubprocessURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return &safehttp.PolicyError{Reason: "YouTube subprocess URL must be an HTTPS URL without userinfo"}
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return &safehttp.PolicyError{Reason: "YouTube subprocess URL must use the default HTTPS port"}
	}
	switch strings.ToLower(strings.TrimSuffix(parsed.Hostname(), ".")) {
	case "youtube.com", "www.youtube.com", "youtu.be":
		return nil
	default:
		return &safehttp.PolicyError{Reason: "YouTube subprocess URL host is not allowed"}
	}
}

func summarizeFromExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, toolVersion string) (bool, string, SourceResult, error) {
	summaryToolName := summarizecli.SummaryToolName(opts.Model)
	if reason, ok := skipSummaryReason(source, extract); ok {
		summary := model.SummaryResult{
			Status:        model.SourceSummaryStatusSkipped,
			Error:         reason,
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
			FetchedAt:     time.Now().UTC(),
		}
		changed, err := st.SaveSourceSummary(ctx, source.ID, summary)
		if err == nil && changed {
			debugLog(opts.Logger, "source summary skipped", "source_key", source.SourceKey, "url", source.CanonicalURL, "reason", reason)
		}
		return changed, model.SourceSummaryStatusSkipped, sourceSummaryResult(cfg.RootDir, source, summary, changed), err
	}

	input, cleanup, err := summaryInputFile(cfg, extract)
	if err != nil {
		summary := model.SummaryResult{
			Status:        model.SourceSummaryStatusError,
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		}
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, summary)
		return changed, model.SourceSummaryStatusError, sourceSummaryResult(cfg.RootDir, source, summary, changed), saveErr
	}
	defer cleanup()
	if strings.TrimSpace(input) == "" {
		summary := model.SummaryResult{
			Status:        model.SourceSummaryStatusBlocked,
			Error:         "no extracted content available for summary",
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		}
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, summary)
		return changed, model.SourceSummaryStatusBlocked, sourceSummaryResult(cfg.RootDir, source, summary, changed), saveErr
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:          opts.Binary,
		Input:           input,
		Summarize:       true,
		Model:           opts.Model,
		CLI:             summaryCLI(opts),
		Prompt:          buildSummaryPrompt(source, extract),
		Length:          opts.Length,
		Language:        opts.Language,
		Timeout:         opts.Timeout,
		RootDir:         cfg.RootDir,
		Metrics:         opts.Metrics,
		InferenceParams: opts.InferenceParams,
	})
	if err != nil {
		if isUserCancellation(ctx, err) {
			return false, "", SourceResult{}, context.Canceled
		}
		status := model.SourceSummaryStatusError
		if reason, blocked := blockedSummaryReason(err); blocked {
			status = model.SourceSummaryStatusBlocked
			err = errors.New(reason)
		}
		summary := model.SummaryResult{
			Status:        status,
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		}
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, summary)
		return changed, status, sourceSummaryResult(cfg.RootDir, source, summary, changed), saveErr
	}

	runResult.Summary.PromptVersion = SummaryPromptVersion
	changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary)
	if err == nil && changed && runResult.Summary.Status == model.SourceSummaryStatusOK {
		debugLog(opts.Logger, "source summary saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "summary_chars", len(runResult.Summary.Text), "model", runResult.Summary.Model, "tool", runResult.Summary.Tool)
	}
	return changed, runResult.Summary.Status, sourceSummaryResult(cfg.RootDir, source, runResult.Summary, changed), err
}

func summarizeExtract(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options, env map[string]string) (summarizecli.Result, error) {
	input, cleanup, err := summaryInputFile(cfg, extract)
	if err != nil {
		return summarizecli.Result{}, err
	}
	defer cleanup()

	return summarizecli.Run(ctx, summarizecli.Options{
		Binary:          opts.Binary,
		Input:           input,
		Summarize:       true,
		Model:           opts.Model,
		CLI:             summaryCLI(opts),
		Prompt:          buildSummaryPrompt(source, extract),
		Length:          opts.Length,
		Language:        opts.Language,
		Timeout:         opts.Timeout,
		RootDir:         cfg.RootDir,
		Env:             env,
		Metrics:         opts.Metrics,
		InferenceParams: opts.InferenceParams,
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
