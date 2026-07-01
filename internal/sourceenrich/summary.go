package sourceenrich

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
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

func summarizeFromExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, toolVersion string) (bool, string, error) {
	summaryToolName := summarizecli.SummaryToolName(opts.Model)
	if reason, ok := skipSummaryReason(source, extract); ok {
		changed, err := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        model.SourceSummaryStatusSkipped,
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
		return changed, model.SourceSummaryStatusSkipped, err
	}

	input, cleanup, err := summaryInputFile(cfg, extract)
	if err != nil {
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        model.SourceSummaryStatusError,
			Error:         err.Error(),
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		})
		return changed, model.SourceSummaryStatusError, saveErr
	}
	defer cleanup()
	if strings.TrimSpace(input) == "" {
		changed, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
			Status:        model.SourceSummaryStatusBlocked,
			Error:         "no extracted content available for summary",
			Model:         opts.Model,
			PromptVersion: SummaryPromptVersion,
			Tool:          summaryToolName,
			ToolVersion:   toolVersion,
		})
		return changed, model.SourceSummaryStatusBlocked, saveErr
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
		InferenceParams: opts.InferenceParams,
	})
	if err != nil {
		if isUserCancellation(ctx, err) {
			return false, "", context.Canceled
		}
		status := model.SourceSummaryStatusError
		if reason, blocked := blockedSummaryReason(err); blocked {
			status = model.SourceSummaryStatusBlocked
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
	if err == nil && changed && runResult.Summary.Status == model.SourceSummaryStatusOK {
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
