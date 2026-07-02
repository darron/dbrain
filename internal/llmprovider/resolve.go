package llmprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/version"
)

type ProviderOverrides struct {
	BaseURL   string
	APIKey    string
	Referer   string
	Title     string
	UserAgent string
}

type ResolveOptions struct {
	RootDir   string
	Env       map[string]string
	Model     string
	Task      Task
	Overrides map[Provider]ProviderOverrides
}

type Target struct {
	Ref         ModelRef
	Spec        ProviderSpec
	BaseURL     string
	APIKey      string
	Headers     map[string]string
	DisplayName string
}

func (t Target) AuthorizationHeader() string {
	if strings.TrimSpace(t.APIKey) == "" {
		return ""
	}
	return "Bearer " + strings.TrimSpace(t.APIKey)
}

func ResolveTarget(ctx context.Context, opts ResolveOptions) (Target, error) {
	reg, err := RegistryForRoot(opts.RootDir)
	if err != nil {
		return Target{}, err
	}
	ref := reg.ParseModelRef(opts.Model)
	if !ref.ProviderQualified || ref.Spec == nil {
		return Target{}, fmt.Errorf("direct model requested without a supported direct model")
	}
	spec := *ref.Spec
	override := opts.Overrides[ref.Provider]
	baseURL := firstString(
		override.BaseURL,
		firstEnvMapValue(opts.Env, spec.BaseURLEnvKeys...),
		runtimeenv.FirstNonEmpty(opts.RootDir, spec.BaseURLEnvKeys...),
		aliasConfigValue(opts.RootDir, ref.Provider, "base_url"),
		spec.DefaultBaseURL,
	)
	apiKey, err := resolveAPIKey(ctx, opts, ref.Provider, spec, override)
	if err != nil {
		return Target{}, err
	}
	if spec.RequiresAPIKey && strings.TrimSpace(apiKey) == "" {
		return Target{}, fmt.Errorf("%s API key is required", spec.DisplayName)
	}
	baseURL = normalizeBaseURL(baseURL, spec.Transport, ref.Provider)
	headers := resolveHeaders(opts, spec, override)
	return Target{
		Ref:         ref,
		Spec:        spec,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Headers:     headers,
		DisplayName: ref.Original,
	}, nil
}

func resolveAPIKey(ctx context.Context, opts ResolveOptions, provider Provider, spec ProviderSpec, override ProviderOverrides) (string, error) {
	if value := strings.TrimSpace(override.APIKey); value != "" {
		return runtimeenv.ResolveSecretRef(ctx, value)
	}
	if value := firstEnvMapValue(opts.Env, spec.APIKeyEnvKeys...); value != "" {
		return runtimeenv.ResolveSecretRef(ctx, value)
	}
	if len(spec.APIKeyEnvKeys) > 0 {
		value, err := runtimeenv.FirstNonEmptySecret(ctx, opts.RootDir, spec.APIKeyEnvKeys...)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(value) != "" {
			return value, nil
		}
	}
	if value := aliasConfigValue(opts.RootDir, provider, "api_key"); strings.TrimSpace(value) != "" {
		resolved, err := runtimeenv.ResolveSecretRef(ctx, value)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}
	return spec.DefaultAPIKey, nil
}

func resolveHeaders(opts ResolveOptions, spec ProviderSpec, override ProviderOverrides) map[string]string {
	headers := map[string]string{}
	if spec.ID != ProviderOpenRouter {
		return headers
	}
	referer := firstString(
		override.Referer,
		firstEnvMapValue(opts.Env, spec.HeaderPolicy.RefererEnvKeys...),
		runtimeenv.FirstNonEmpty(opts.RootDir, spec.HeaderPolicy.RefererEnvKeys...),
		spec.HeaderPolicy.DefaultRefererByTask[opts.Task],
	)
	title := firstString(
		override.Title,
		firstEnvMapValue(opts.Env, spec.HeaderPolicy.TitleEnvKeys...),
		runtimeenv.FirstNonEmpty(opts.RootDir, spec.HeaderPolicy.TitleEnvKeys...),
		spec.HeaderPolicy.DefaultTitleByTask[opts.Task],
	)
	userAgent := firstString(
		override.UserAgent,
		firstEnvMapValue(opts.Env, spec.HeaderPolicy.UserAgentEnvKeys...),
		runtimeenv.FirstNonEmpty(opts.RootDir, spec.HeaderPolicy.UserAgentEnvKeys...),
	)
	headers["HTTP-Referer"] = referer
	headers["X-Title"] = title
	headers["User-Agent"] = version.UserAgent(userAgent)
	return headers
}

func normalizeBaseURL(raw string, transport Transport, provider Provider) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return value
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimRight(value, "/")
	switch transport {
	case TransportOllamaChat:
		value = strings.TrimSuffix(value, "/v1")
		value = strings.TrimSuffix(value, "/api")
	case TransportOpenAIChat:
		suffix := "/v1"
		if provider == ProviderOpenRouter {
			suffix = "/api/v1"
		}
		if !strings.HasSuffix(value, suffix) {
			if provider == ProviderOpenRouter && strings.HasSuffix(value, "/api") {
				value += "/v1"
			} else {
				value += suffix
			}
		}
	}
	return value
}

func firstEnvMapValue(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	return ""
}

func aliasConfigValue(rootDir string, provider Provider, key string) string {
	if strings.TrimSpace(rootDir) == "" || provider == "" {
		return ""
	}
	raw, ok := runtimeenv.ConfigMap(rootDir, "llm_backends")
	if !ok {
		return ""
	}
	value, ok := lookupMapValue(raw, string(provider))
	if !ok {
		return ""
	}
	entry, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(entry, key)
}
