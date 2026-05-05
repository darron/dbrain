package xmediatranscribe

import (
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
