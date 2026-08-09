package mastodonapi

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/safehttp"
)

// Config is the parsed Mastodon runtime configuration. Secret values are
// intentionally never part of this type; only typed references are retained.
type Config struct {
	Enabled  bool
	Accounts []AccountConfig
}

// AccountConfig identifies one configured viewer account on one Mastodon
// origin. The remote account ID is learned from verify_credentials and is
// persisted separately in sync state.
type AccountConfig struct {
	Key             string
	Enabled         bool
	Origin          string
	AccessTokenRef  string
	ClientIDRef     string
	ClientSecretRef string
}

// AccountMetadata is safe for status and diagnostics output. It contains
// presence bits instead of the configured secret references themselves.
type AccountMetadata struct {
	Key                    string `json:"key"`
	Enabled                bool   `json:"enabled"`
	Origin                 string `json:"origin"`
	AccessTokenRefPresent  bool   `json:"access_token_ref_present"`
	ClientIDRefPresent     bool   `json:"client_id_ref_present"`
	ClientSecretRefPresent bool   `json:"client_secret_ref_present"`
}

func (a AccountConfig) RedactedMetadata() AccountMetadata {
	return AccountMetadata{
		Key:                    a.Key,
		Enabled:                a.Enabled,
		Origin:                 a.Origin,
		AccessTokenRefPresent:  strings.TrimSpace(a.AccessTokenRef) != "",
		ClientIDRefPresent:     strings.TrimSpace(a.ClientIDRef) != "",
		ClientSecretRefPresent: strings.TrimSpace(a.ClientSecretRef) != "",
	}
}

// ParseConfig validates the mastodon section returned by runtimeenv.ConfigMap.
// It rejects bare secret values because ResolveSecretRef deliberately retains
// literal values for legacy integrations.
func ParseConfig(raw map[string]any) (Config, error) {
	cfg := Config{}
	if raw == nil {
		return cfg, fmt.Errorf("mastodon config is missing")
	}
	if enabled, ok := raw["enabled"]; ok {
		value, ok := enabled.(bool)
		if !ok {
			return cfg, fmt.Errorf("mastodon.enabled must be a boolean")
		}
		cfg.Enabled = value
	}

	accountsRaw, ok := raw["accounts"]
	if !ok {
		return cfg, fmt.Errorf("mastodon.accounts is required")
	}
	accounts, ok := accountsRaw.(map[string]any)
	if !ok {
		return cfg, fmt.Errorf("mastodon.accounts must be a map")
	}
	keys := make([]string, 0, len(accounts))
	for key := range accounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		accountMap, ok := accounts[key].(map[string]any)
		if !ok {
			return cfg, fmt.Errorf("mastodon.accounts.%s must be a map", key)
		}
		account, err := parseAccountConfig(key, accountMap)
		if err != nil {
			return cfg, err
		}
		cfg.Accounts = append(cfg.Accounts, account)
	}
	return cfg, nil
}

func parseAccountConfig(key string, raw map[string]any) (AccountConfig, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return AccountConfig{}, fmt.Errorf("mastodon account key is required")
	}
	origin, err := requiredString(raw, "origin")
	if err != nil {
		return AccountConfig{}, fmt.Errorf("mastodon.accounts.%s: %w", key, err)
	}
	origin, err = canonicalOrigin(origin)
	if err != nil {
		return AccountConfig{}, fmt.Errorf("mastodon.accounts.%s.origin: %w", key, err)
	}
	accessTokenRef, err := requiredSecretRef(raw, "access_token_ref")
	if err != nil {
		return AccountConfig{}, fmt.Errorf("mastodon.accounts.%s: %w", key, err)
	}
	clientIDRef, err := requiredSecretRef(raw, "client_id_ref")
	if err != nil {
		return AccountConfig{}, fmt.Errorf("mastodon.accounts.%s: %w", key, err)
	}
	clientSecretRef, err := requiredSecretRef(raw, "client_secret_ref")
	if err != nil {
		return AccountConfig{}, fmt.Errorf("mastodon.accounts.%s: %w", key, err)
	}
	enabled := true
	if rawEnabled, ok := raw["enabled"]; ok {
		var typeOK bool
		enabled, typeOK = rawEnabled.(bool)
		if !typeOK {
			return AccountConfig{}, fmt.Errorf("mastodon.accounts.%s.enabled must be a boolean", key)
		}
	}
	return AccountConfig{
		Key:             key,
		Enabled:         enabled,
		Origin:          origin,
		AccessTokenRef:  accessTokenRef,
		ClientIDRef:     clientIDRef,
		ClientSecretRef: clientSecretRef,
	}, nil
}

func requiredString(raw map[string]any, key string) (string, error) {
	value, ok := raw[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return strings.TrimSpace(text), nil
}

func requiredSecretRef(raw map[string]any, key string) (string, error) {
	value, err := requiredString(raw, key)
	if err != nil {
		return "", err
	}
	if err := ValidateSecretRef(value); err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

// ValidateSecretRef accepts only the reference syntaxes that the runtime
// resolver understands. The Mastodon config layer intentionally does not
// inherit runtimeenv's legacy behavior of treating bare strings as secrets.
func ValidateSecretRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.IndexFunc(ref, func(r rune) bool { return r == '\n' || r == '\r' || r == '\t' || r == ' ' }) >= 0 {
		return fmt.Errorf("secret ref must be a non-empty typed reference")
	}
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		if !validEnvName(name) {
			return fmt.Errorf("env secret ref must name an environment variable")
		}
	case strings.HasPrefix(ref, "op://"):
		if len(strings.TrimPrefix(ref, "op://")) == 0 || strings.ContainsAny(ref, "?#") {
			return fmt.Errorf("op secret ref is malformed")
		}
	case strings.HasPrefix(ref, "keychain://"):
		if err := validateKeychainRef(ref); err != nil {
			return err
		}
	default:
		return fmt.Errorf("secret ref must use env:, op://, or keychain://")
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func validateKeychainRef(ref string) error {
	if strings.ContainsAny(ref, "?#") {
		return fmt.Errorf("keychain ref must not contain a query or fragment")
	}
	value := strings.TrimPrefix(ref, "keychain://")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("keychain ref must be keychain://service/account")
	}
	return nil
}

func canonicalOrigin(raw string) (string, error) {
	canonical, err := safehttp.CanonicalOriginEndpoint(raw)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(canonical)
	if err != nil || strings.ToLower(parsed.Scheme) != "https" {
		return "", fmt.Errorf("mastodon origin must use HTTPS")
	}
	return canonical, nil
}

// CanonicalOrigin validates the user-facing Mastodon instance origin.
func CanonicalOrigin(raw string) (string, error) {
	return canonicalOrigin(raw)
}
