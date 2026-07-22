package xmediatranscribe

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func normalizeOptions(cfg config.Config, opts Options) Options {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if strings.TrimSpace(opts.Transcriber) == "" {
		opts.Transcriber = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TRANSCRIPTION_BACKEND")
	}
	if strings.TrimSpace(opts.Transcriber) == "" {
		opts.Transcriber = "auto"
	}
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TRANSCRIPTION_LANGUAGE")
	}
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = "auto"
	}
	if strings.TrimSpace(opts.WhisperBinary) == "" {
		opts.WhisperBinary = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TRANSCRIPTION_WHISPER_BINARY")
	}
	if strings.TrimSpace(opts.WhisperBinary) == "" {
		opts.WhisperBinary = "whisper-cli"
	}
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		opts.WhisperModelPath = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TRANSCRIPTION_MODEL_PATH")
	}
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		opts.WhisperModelPath = filepath.Join(cfg.CacheDir, "whisper-cpp", "ggml-base.bin")
	}
	if strings.TrimSpace(opts.WhisperVADPath) == "" {
		opts.WhisperVADPath = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TRANSCRIPTION_VAD_MODEL_PATH")
	}
	if strings.TrimSpace(opts.WhisperVADPath) == "" {
		opts.WhisperVADPath = filepath.Join(cfg.CacheDir, "whisper-cpp", "ggml-silero-v6.2.0.bin")
	}
	if strings.TrimSpace(opts.MacWhisperBinary) == "" {
		opts.MacWhisperBinary = "mw"
	}
	if strings.TrimSpace(opts.FFprobeBinary) == "" {
		opts.FFprobeBinary = "ffprobe"
	}
	if strings.TrimSpace(opts.SummaryLength) == "" {
		opts.SummaryLength = "medium"
	}
	opts.SummaryModel = summaryconfig.Model(cfg.RootDir, opts.SummaryModel)
	if strings.TrimSpace(opts.SummaryLanguage) == "" {
		opts.SummaryLanguage = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE")
	}
	return opts
}
