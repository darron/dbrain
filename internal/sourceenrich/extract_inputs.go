package sourceenrich

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

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
