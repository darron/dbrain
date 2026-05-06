package sourceenrich

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

func defaultOptions(cfg config.Config, opts Options) Options {
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
	return opts
}
