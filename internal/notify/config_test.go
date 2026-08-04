package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotificationConfigLoadsYAMLAndEnvironmentPrecedence(t *testing.T) {
	root := t.TempDir()
	writeNotificationConfig(t, root, `
notifications:
  enabled: true
  repeat_after: "7h"
  buzz:
    enabled: true
    relay_url: "wss://yaml.example"
    channel_id: "00000000-0000-4000-8000-000000000001"
    private_key_ref: "keychain://dbrain/yaml-buzz"
    allow_private_origin: false
`)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(strings.Join([]string{
		"DBRAIN_NOTIFICATIONS_REPEAT_AFTER=8h",
		"DBRAIN_NOTIFICATIONS_BUZZ_RELAY_URL=wss://dotenv.example",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBRAIN_NOTIFICATIONS_REPEAT_AFTER", "9h")
	t.Setenv("DBRAIN_NOTIFICATIONS_BUZZ_CHANNEL_ID", "00000000-0000-4000-8000-000000000002")

	got, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !got.Enabled || !got.Buzz.Enabled || got.RepeatAfter != 9*time.Hour {
		t.Fatalf("loaded config = %#v", got)
	}
	if got.Buzz.RelayURL != "wss://dotenv.example" || got.Buzz.ChannelID != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("loaded Buzz config = %#v", got.Buzz)
	}
}

func TestNotificationConfigDisabledMayBeAbsentAndDefaultsRepeatAfter(t *testing.T) {
	got, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Enabled || got.RepeatAfter != 6*time.Hour {
		t.Fatalf("default config = %#v", got)
	}
}

func TestNotificationConfigDisabledIgnoresMalformedNestedSettings(t *testing.T) {
	root := t.TempDir()
	writeNotificationConfig(t, root, `
notifications:
  enabled: false
  repeat_after: not-a-duration
  buzz:
    enabled: maybe
    relay_url: https://credentials:are@not-checked.example/path?query=yes#fragment
    channel_id: not-a-uuid
    private_key_ref: inline-secret
    allow_private_origin: maybe
`)

	got, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig disabled config: %v", err)
	}
	if got.Enabled || got.RepeatAfter != 6*time.Hour {
		t.Fatalf("disabled config = %#v", got)
	}
}

func TestBuzzConfigLoadsValidProviderWhileNotificationsDisabled(t *testing.T) {
	root := t.TempDir()
	writeNotificationConfig(t, root, `
notifications:
  enabled: false
  buzz:
    enabled: true
    relay_url: "wss://relay.example"
    channel_id: "00000000-0000-4000-8000-000000000001"
    private_key_ref: "keychain://dbrain/notifications-buzz"
`)

	global, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if global.Enabled || global.Buzz.Enabled {
		t.Fatalf("global kill switch parsed nested provider: %#v", global)
	}
	buzz, err := LoadBuzzConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !buzz.Enabled || buzz.RelayURL != "wss://relay.example" || buzz.ChannelID != "00000000-0000-4000-8000-000000000001" || buzz.PrivateKeyRef != "keychain://dbrain/notifications-buzz" {
		t.Fatalf("Buzz inspection config = %#v", buzz)
	}
}

func TestBuzzConfigRejectsMalformedEnabledProviderWhileNotificationsDisabled(t *testing.T) {
	root := t.TempDir()
	writeNotificationConfig(t, root, `
notifications:
  enabled: false
  buzz:
    enabled: true
    relay_url: "https://user:private@relay.example/path"
    channel_id: "not-a-uuid"
    private_key_ref: "inline-secret"
`)

	_, err := LoadBuzzConfig(root)
	if err == nil || strings.Contains(err.Error(), "user:private") || strings.Contains(err.Error(), "inline-secret") {
		t.Fatalf("safe Buzz config error = %v", err)
	}
}

func TestNotificationConfigRejectsInvalidEnabledConfiguration(t *testing.T) {
	validBuzz := `
  buzz:
    enabled: true
    relay_url: "wss://relay.example"
    channel_id: "00000000-0000-4000-8000-000000000001"
    private_key_ref: "keychain://dbrain/notifications-buzz"
`
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"zero duration", "notifications:\n  enabled: true\n  repeat_after: 0s\n" + validBuzz, "repeat_after"},
		{"negative duration", "notifications:\n  enabled: true\n  repeat_after: -1h\n" + validBuzz, "repeat_after"},
		{"invalid duration", "notifications:\n  enabled: true\n  repeat_after: later\n" + validBuzz, "repeat_after"},
		{"no provider", "notifications:\n  enabled: true\n", "provider"},
		{"partial Buzz", "notifications:\n  enabled: true\n  buzz:\n    enabled: true\n", "relay_url"},
		{"inline secret", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "keychain://dbrain/notifications-buzz", "not-a-ref", 1), "private_key_ref"},
		{"non wss relay", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "wss://relay.example", "https://relay.example", 1), "relay_url"},
		{"relay credentials", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "wss://relay.example", "wss://user:pass@relay.example", 1), "relay_url"},
		{"relay path", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "wss://relay.example", "wss://relay.example/path", 1), "relay_url"},
		{"relay query", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "wss://relay.example", "wss://relay.example?x=1", 1), "relay_url"},
		{"relay fragment", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "wss://relay.example", "wss://relay.example#frag", 1), "relay_url"},
		// Buzz channels are UUIDs by the Buzz contract; generic NIP-29 identifiers need not be UUIDs.
		{"Buzz channel UUID", "notifications:\n  enabled: true\n" + strings.Replace(validBuzz, "00000000-0000-4000-8000-000000000001", "not-a-uuid", 1), "channel_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeNotificationConfig(t, root, tt.yaml)
			if _, err := LoadConfig(root); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNotificationConfigAllowsPrivateRelayOnlyWithExplicitOptIn(t *testing.T) {
	root := t.TempDir()
	writeNotificationConfig(t, root, `
notifications:
  enabled: true
  buzz:
    enabled: true
    relay_url: "wss://localhost"
    channel_id: "00000000-0000-4000-8000-000000000001"
    private_key_ref: "env:BUZZ_PRIVATE_KEY"
`)
	if _, err := LoadConfig(root); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("LoadConfig private relay error = %v", err)
	}
	writeNotificationConfig(t, root, `
notifications:
  enabled: true
  buzz:
    enabled: true
    relay_url: "wss://localhost"
    channel_id: "00000000-0000-4000-8000-000000000001"
    private_key_ref: "env:BUZZ_PRIVATE_KEY"
    allow_private_origin: true
`)
	if _, err := LoadConfig(root); err != nil {
		t.Fatalf("LoadConfig with private opt-in: %v", err)
	}
}

func writeNotificationConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
