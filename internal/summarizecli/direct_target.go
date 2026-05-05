package summarizecli

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/version"
)

func resolveDirectSummaryTarget(ctx context.Context, opts Options) (directSummaryTarget, error) {
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		return directSummaryTarget{
			model:        ollamaModel,
			displayName:  defaultDirectDisplayName(opts.Model, "ollama/"+ollamaModel),
			baseURL:      ollamaNativeBaseURLWithEnv(opts.Env),
			apiKey:       ollamaAPIKeyWithEnv(opts.Env),
			toolName:     SummaryToolName(opts.Model),
			toolVersion:  SummaryToolVersion(ctx, opts.Binary, opts.Model),
			label:        "ollama",
			nativeOllama: true,
		}, nil
	}
	if openrouterModel, ok := parseOpenRouterModel(opts.Model); ok {
		apiKey := openRouterAPIKeyWithEnv(opts.Env)
		if strings.TrimSpace(apiKey) == "" {
			return directSummaryTarget{}, fmt.Errorf("direct openrouter summary requested without DBRAIN_OPENROUTER_API_KEY or OPENROUTER_API_KEY")
		}
		headers := map[string]string{}
		if value := openRouterRefererWithEnv(opts.Env); value != "" {
			headers["HTTP-Referer"] = value
		}
		if value := openRouterTitleWithEnv(opts.Env); value != "" {
			headers["X-Title"] = value
		}
		headers["User-Agent"] = version.UserAgent(userAgentWithEnv(opts.Env))
		return directSummaryTarget{
			model:       openrouterModel,
			displayName: defaultDirectDisplayName(opts.Model, "openrouter/"+openrouterModel),
			baseURL:     openRouterBaseURLWithEnv(opts.Env),
			apiKey:      apiKey,
			toolName:    SummaryToolName(opts.Model),
			toolVersion: SummaryToolVersion(ctx, opts.Binary, opts.Model),
			headers:     headers,
			label:       "openrouter",
		}, nil
	}
	return directSummaryTarget{}, fmt.Errorf("direct summary requested without a supported direct model")
}

func defaultDirectDisplayName(current string, fallback string) string {
	value := strings.TrimSpace(current)
	if value != "" {
		return value
	}
	return fallback
}
