package notify

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/google/uuid"
)

const defaultRepeatAfter = 6 * time.Hour

type Config struct {
	Enabled     bool
	RepeatAfter time.Duration
	Buzz        BuzzConfig
	Slack       SlackConfig
}

type BuzzConfig struct {
	Enabled            bool
	RelayURL           string
	ChannelID          string
	PrivateKeyRef      string
	AllowPrivateOrigin bool
}

type SlackConfig struct {
	Enabled       bool
	WebhookURLRef string
}

func LoadConfig(rootDir string) (Config, error) {
	config := Config{RepeatAfter: defaultRepeatAfter}
	var err error
	if config.Enabled, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_ENABLED", false); err != nil {
		return Config{}, err
	}
	if !config.Enabled {
		return config, nil
	}
	if config.RepeatAfter, err = loadRepeatAfter(rootDir); err != nil {
		return Config{}, err
	}
	if config.Buzz, err = LoadBuzzConfig(rootDir); err != nil {
		return Config{}, err
	}
	if config.Slack, err = LoadSlackConfig(rootDir); err != nil {
		return Config{}, err
	}
	if !config.Buzz.Enabled && !config.Slack.Enabled {
		return Config{}, fmt.Errorf("notifications requires at least one enabled provider")
	}
	return config, nil
}

// LoadInspectionConfig loads the redacted operator view independently from the
// automatic-delivery kill switch. Unlike LoadConfig, it validates the repeat
// interval and any explicitly enabled provider even when delivery is disabled.
func LoadInspectionConfig(rootDir string) (Config, error) {
	config := Config{}
	var err error
	if config.Enabled, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_ENABLED", false); err != nil {
		return Config{}, err
	}
	if config.RepeatAfter, err = loadRepeatAfter(rootDir); err != nil {
		return Config{}, err
	}
	if config.Buzz, err = LoadBuzzConfig(rootDir); err != nil {
		return Config{}, err
	}
	if config.Slack, err = LoadSlackConfig(rootDir); err != nil {
		return Config{}, err
	}
	if config.Enabled && !config.Buzz.Enabled && !config.Slack.Enabled {
		return Config{}, fmt.Errorf("notifications requires at least one enabled provider")
	}
	return config, nil
}

// LoadSlackConfig loads and validates the Slack provider independently from
// the global notification kill switch. Webhook credentials remain typed
// references until a delivery attempt resolves them.
func LoadSlackConfig(rootDir string) (SlackConfig, error) {
	config := SlackConfig{}
	var err error
	if config.Enabled, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_SLACK_ENABLED", false); err != nil {
		return SlackConfig{}, err
	}
	if !config.Enabled {
		return config, nil
	}
	config.WebhookURLRef = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_SLACK_WEBHOOK_URL_REF")
	if err := config.validate(); err != nil {
		return SlackConfig{}, err
	}
	return config, nil
}

// LoadBuzzConfig loads and validates the Buzz provider independently from the
// global notification kill switch. It is intended for read-only inspection and
// explicit provider tests; automatic delivery must continue to use LoadConfig.
func LoadBuzzConfig(rootDir string) (BuzzConfig, error) {
	config := BuzzConfig{}
	var err error
	if config.Enabled, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_ENABLED", false); err != nil {
		return BuzzConfig{}, err
	}
	if !config.Enabled {
		return config, nil
	}
	if config.AllowPrivateOrigin, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_ALLOW_PRIVATE_ORIGIN", false); err != nil {
		return BuzzConfig{}, err
	}
	config.RelayURL = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_RELAY_URL")
	config.ChannelID = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_CHANNEL_ID")
	config.PrivateKeyRef = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_PRIVATE_KEY_REF")
	if err := config.validate(); err != nil {
		return BuzzConfig{}, err
	}
	return config, nil
}

func loadRepeatAfter(rootDir string) (time.Duration, error) {
	repeatAfter := defaultRepeatAfter
	if value, ok := runtimeenv.Lookup(rootDir, "DBRAIN_NOTIFICATIONS_REPEAT_AFTER"); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("notifications.repeat_after must be a positive duration")
		}
		repeatAfter = parsed
	}
	return repeatAfter, nil
}

func notificationBool(rootDir, key string, fallback bool) (bool, error) {
	value, ok := runtimeenv.Lookup(rootDir, key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be boolean", strings.ToLower(strings.TrimPrefix(key, "DBRAIN_")))
	}
	return parsed, nil
}

func (c BuzzConfig) validate() error {
	if err := validateBuzzRelayURL(c.RelayURL, c.AllowPrivateOrigin); err != nil {
		return err
	}
	if _, err := uuid.Parse(strings.TrimSpace(c.ChannelID)); err != nil {
		return fmt.Errorf("notifications.buzz.channel_id must be a UUID")
	}
	if err := validateTypedSecretRef(c.PrivateKeyRef); err != nil {
		return fmt.Errorf("notifications.buzz.private_key_ref %w", err)
	}
	return nil
}

func (c SlackConfig) validate() error {
	if err := validateTypedSecretRef(c.WebhookURLRef); err != nil {
		return fmt.Errorf("notifications.slack.webhook_url_ref %w", err)
	}
	return nil
}

func validateTypedSecretRef(raw string) error {
	value := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(value, "env:"):
		if strings.TrimSpace(strings.TrimPrefix(value, "env:")) != "" {
			return nil
		}
	case strings.HasPrefix(value, "op://"):
		if strings.TrimSpace(strings.TrimPrefix(value, "op://")) != "" {
			return nil
		}
	case strings.HasPrefix(value, "keychain://"):
		parts := strings.SplitN(strings.TrimPrefix(value, "keychain://"), "/", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			return nil
		}
	}
	return fmt.Errorf("must be a typed secret ref")
}

func validateBuzzRelayURL(raw string, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" || strings.ToLower(parsed.Scheme) != "wss" {
		return fmt.Errorf("notifications.buzz.relay_url must be a wss origin")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return fmt.Errorf("notifications.buzz.relay_url must be an origin only")
	}
	if isPrivateOriginHost(parsed.Hostname()) && !allowPrivate {
		return fmt.Errorf("private notifications.buzz.relay_url origin requires explicit opt-in")
	}
	return nil
}

func isPrivateOriginHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	return !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
}
