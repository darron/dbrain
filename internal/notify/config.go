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
}

type BuzzConfig struct {
	Enabled            bool
	RelayURL           string
	ChannelID          string
	PrivateKeyRef      string
	AllowPrivateOrigin bool
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
	if config.Buzz.Enabled, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_ENABLED", false); err != nil {
		return Config{}, err
	}
	if config.Buzz.AllowPrivateOrigin, err = notificationBool(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_ALLOW_PRIVATE_ORIGIN", false); err != nil {
		return Config{}, err
	}
	if value, ok := runtimeenv.Lookup(rootDir, "DBRAIN_NOTIFICATIONS_REPEAT_AFTER"); ok {
		config.RepeatAfter, err = time.ParseDuration(value)
		if err != nil || config.RepeatAfter <= 0 {
			return Config{}, fmt.Errorf("notifications.repeat_after must be a positive duration")
		}
	}
	config.Buzz.RelayURL = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_RELAY_URL")
	config.Buzz.ChannelID = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_CHANNEL_ID")
	config.Buzz.PrivateKeyRef = runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_NOTIFICATIONS_BUZZ_PRIVATE_KEY_REF")
	if !config.Buzz.Enabled {
		return Config{}, fmt.Errorf("notifications requires at least one enabled provider")
	}
	if err := config.Buzz.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
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
