package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const (
	authProviderGitHub       = "github"
	defaultAuthBaseURL       = "http://127.0.0.1:8742"
	defaultAuthSessionTTL    = 24 * time.Hour
	defaultAuthOAuthStateTTL = 5 * time.Minute
	minAuthSessionKeyLength  = 32
)

type authConfig struct {
	Enabled            bool
	Providers          []string
	BaseURL            string
	SessionKey         string
	GitHubClientID     string
	GitHubClientSecret string
	SessionTTL         time.Duration
	OAuthStateTTL      time.Duration
}

func loadAuthConfig(ctx context.Context, cfg config.Config) (authConfig, error) {
	enabled := runtimeenv.FirstBoolDefault(cfg.RootDir, false, "DBRAIN_AUTH_ENABLED")
	providers, err := normalizeOAuthProviders(runtimeenv.FirstList(cfg.RootDir, "DBRAIN_AUTH_PROVIDERS"))
	if err != nil {
		return authConfig{}, err
	}
	if enabled && len(providers) == 0 {
		providers = []string{authProviderGitHub}
	}

	baseURL := strings.TrimRight(strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_AUTH_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = defaultAuthBaseURL
	}

	authCfg := authConfig{
		Enabled:       enabled,
		Providers:     providers,
		BaseURL:       baseURL,
		SessionTTL:    defaultAuthSessionTTL,
		OAuthStateTTL: defaultAuthOAuthStateTTL,
	}
	if !enabled {
		return authCfg, nil
	}
	if err := validateAuthBaseURL(baseURL); err != nil {
		return authConfig{}, err
	}
	if len(providers) == 0 {
		return authConfig{}, fmt.Errorf("auth.enabled requires auth.providers to include %q", authProviderGitHub)
	}

	sessionKey, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_AUTH_SESSION_KEY")
	if err != nil {
		return authConfig{}, err
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return authConfig{}, fmt.Errorf("auth.enabled requires DBRAIN_AUTH_SESSION_KEY or auth.session_key")
	}
	if len(sessionKey) < minAuthSessionKeyLength {
		return authConfig{}, fmt.Errorf("auth.session_key must be at least %d characters", minAuthSessionKeyLength)
	}
	authCfg.SessionKey = sessionKey

	if oauthProviderAllowed(providers, authProviderGitHub) {
		clientID := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_AUTH_GITHUB_CLIENT_ID"))
		if clientID == "" {
			return authConfig{}, fmt.Errorf("GitHub OAuth requires DBRAIN_AUTH_GITHUB_CLIENT_ID or auth.github.client_id")
		}
		clientSecret, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_AUTH_GITHUB_CLIENT_SECRET")
		if err != nil {
			return authConfig{}, err
		}
		if strings.TrimSpace(clientSecret) == "" {
			return authConfig{}, fmt.Errorf("GitHub OAuth requires DBRAIN_AUTH_GITHUB_CLIENT_SECRET or auth.github.client_secret")
		}
		authCfg.GitHubClientID = clientID
		authCfg.GitHubClientSecret = clientSecret
	}

	return authCfg, nil
}

func validateAuthBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("auth.base_url must be an absolute URL")
	}
	if strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	if strings.EqualFold(u.Scheme, "http") {
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return fmt.Errorf("auth.base_url must use https for internet-exposed auth; http is only allowed for localhost development")
}

func normalizeOAuthProviders(raw []string) ([]string, error) {
	seen := map[string]struct{}{}
	providers := make([]string, 0, len(raw))
	for _, entry := range raw {
		provider := strings.ToLower(strings.TrimSpace(entry))
		if provider == "" {
			continue
		}
		if !knownOAuthProvider(provider) {
			return nil, fmt.Errorf("unsupported OAuth provider %q; supported providers: %s", provider, strings.Join(allowedOAuthProviders(), ", "))
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		providers = append(providers, provider)
	}
	return providers, nil
}

func allowedOAuthProviders() []string {
	return []string{authProviderGitHub}
}

func knownOAuthProvider(provider string) bool {
	for _, allowed := range allowedOAuthProviders() {
		if provider == allowed {
			return true
		}
	}
	return false
}

func oauthProviderAllowed(providers []string, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, allowed := range providers {
		if allowed == provider {
			return true
		}
	}
	return false
}
