package itemcategorize

import (
	"strings"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

func resolveOpts(cfg config.Config, opts *Options) {
	if opts.Vocab.Empty() {
		vocab, _ := categoryvocab.Load(cfg.CategoriesPath)
		opts.Vocab = vocab
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_CATEGORIZE_MODEL"), defaultModel)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if strings.TrimSpace(opts.OpenRouterBase) == "" {
		opts.OpenRouterBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"), defaultOpenRouterBase)
	}
	if strings.TrimSpace(opts.OpenRouterKey) == "" {
		opts.OpenRouterKey = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
	}
	if strings.TrimSpace(opts.OpenRouterRef) == "" {
		opts.OpenRouterRef = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"), "https://local.dbrain")
	}
	if strings.TrimSpace(opts.OpenRouterTitle) == "" {
		opts.OpenRouterTitle = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"), "dbrain categorize")
	}
	if strings.TrimSpace(opts.UserAgent) == "" {
		opts.UserAgent = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_USER_AGENT")
	}
	if strings.TrimSpace(opts.OllamaBase) == "" {
		opts.OllamaBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"), defaultOllamaBase)
	}
	if strings.TrimSpace(opts.OllamaKey) == "" {
		opts.OllamaKey = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"), defaultOllamaKey)
	}
	if strings.TrimSpace(opts.S3Endpoint) == "" {
		opts.S3Endpoint = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT")
	}
	if strings.TrimSpace(opts.S3AccessKey) == "" {
		opts.S3AccessKey = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	}
	if strings.TrimSpace(opts.S3SecretKey) == "" {
		opts.S3SecretKey = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	}
	if strings.TrimSpace(opts.S3Region) == "" {
		opts.S3Region = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION"), "auto")
	}
}
