package sourceenrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
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
		Language:  opts.Language,
		Timeout:   opts.Timeout,
		RootDir:   cfg.RootDir,
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
		Language:  opts.Language,
		Timeout:   opts.Timeout,
		RootDir:   cfg.RootDir,
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
	if reason, ok := mediaURLSkipSummaryReason(extract); ok {
		return reason, true
	}
	if looksLikeNonTextExtractContent(content) {
		return "extracted content appears to be binary/non-text; text summarization skipped", true
	}
	if looksLikePlaceholderExtractContent(content) {
		return "extracted content appears to be redirect/login/placeholder boilerplate rather than substantive content", true
	}
	if reason, ok := waybackSkipSummaryReason(extract); ok {
		return reason, true
	}
	return "", false
}

func mediaURLSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	rawURL := firstNonEmpty(extract.FinalURL, extract.CanonicalURL)
	if strings.TrimSpace(rawURL) == "" {
		return "", false
	}
	ext := sourceURLPathExtension(rawURL)
	if ext == "" {
		return "", false
	}
	if !isUnsupportedTextSummaryMediaExtension(ext) {
		return "", false
	}
	return fmt.Sprintf("source URL points to %s content (%s); text summarization skipped", unsupportedTextSummaryMediaKind(ext), ext), true
}

func sourceURLPathExtension(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	path := parsed.EscapedPath()
	if path == "" {
		return ""
	}
	return strings.ToLower(filepath.Ext(path))
}

func isUnsupportedTextSummaryMediaExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".ico", ".svg":
		return true
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi", ".mpeg", ".mpg":
		return true
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus":
		return true
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".dmg", ".pkg":
		return true
	default:
		return false
	}
}

func unsupportedTextSummaryMediaKind(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".ico", ".svg":
		return "image/media"
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi", ".mpeg", ".mpg":
		return "video/media"
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus":
		return "audio/media"
	default:
		return "binary/media"
	}
}

func looksLikeNonTextExtractContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if strings.ContainsRune(content, '\x00') {
		return true
	}
	if !utf8.ValidString(content) {
		return true
	}

	runes := 0
	replacementRunes := 0
	controlRunes := 0
	for _, r := range content {
		runes++
		if r == utf8.RuneError {
			replacementRunes++
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			controlRunes++
		}
	}
	if runes == 0 {
		return false
	}
	if replacementRunes >= 3 && replacementRunes*20 >= runes {
		return true
	}
	return controlRunes >= 8 && controlRunes*10 >= runes
}

const maxLowSignalWaybackExtractChars = 500

func waybackSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	if strings.TrimSpace(extract.Tool) != waybackToolName {
		return "", false
	}
	content := strings.TrimSpace(extract.Content)
	if content == "" {
		return "", false
	}
	if len(content) < maxLowSignalWaybackExtractChars {
		return fmt.Sprintf("wayback extract is too short to summarize reliably (%d chars)", len(content)), true
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
		strings.Contains(normalized, "google drive"),
		strings.Contains(normalized, "your browser does not support frames"),
		strings.Contains(normalized, "click here to enter the site"):
		return len(normalized) <= 300
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
		strings.Contains(value, "input is too long"),
		strings.Contains(value, "context deadline exceeded"),
		strings.Contains(value, "timeout"),
		strings.Contains(value, "timed out"),
		strings.Contains(value, "signal: killed"):
		return err.Error(), true
	default:
		return "", false
	}
}
