package youtubeimport

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/sourceenrich"
)

func summarizeEnvFor(cookies string) func(source model.SourceDocument) map[string]string {
	return func(source model.SourceDocument) map[string]string {
		if source.SourceType != "youtube" {
			return nil
		}
		return map[string]string{
			"SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER": cookies,
		}
	}
}

func summarizeArgsFor(opts Options) func(source model.SourceDocument) []string {
	return func(source model.SourceDocument) []string {
		if source.SourceType != "youtube" {
			return nil
		}
		args := []string{"--youtube", "auto", "--video-mode", "transcript"}
		if value := strings.TrimSpace(opts.Transcriber); value != "" {
			args = append(args, "--transcriber", value)
		}
		return args
	}
}

func fallbackExtractFor(cfg config.Config, opts Options, cookiesArg string) func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error) {
	return func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error) {
		fallback, changed, err := sourceenrich.MaybeTranscribeYouTubeAudioFallback(ctx, cfg, source, extract, sourceenrich.Options{
			Logger:             opts.Logger,
			Timeout:            opts.Timeout,
			YouTubeBrowser:     opts.Browser,
			YouTubeProfile:     opts.Profile,
			YouTubeCookiesArg:  cookiesArg,
			YouTubeTranscriber: opts.Transcriber,
			YTDLPBinary:        opts.YTDLPBinary,
			WhisperBinary:      opts.WhisperBinary,
			WhisperModelPath:   opts.WhisperModelPath,
			MacWhisperBinary:   opts.MacWhisperBinary,
		})
		if err != nil {
			debugLog(opts.Logger, "youtube audio fallback failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			return model.ExtractResult{}, false, nil
		}
		if changed {
			debugLog(opts.Logger, "youtube audio fallback succeeded", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(fallback.Content), "tool", fallback.Tool)
		}
		return fallback, changed, nil
	}
}
