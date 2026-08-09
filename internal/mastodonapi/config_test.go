package mastodonapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfigCanonicalizesOriginAndKeepsOnlyRedactedSecretMetadata(t *testing.T) {
	raw := map[string]any{
		"enabled": true,
		"accounts": map[string]any{
			"hachyderm": map[string]any{
				"enabled":           true,
				"origin":            "HTTPS://HACHYDERM.IO./",
				"access_token_ref":  "keychain://dbrain/mastodon-hachyderm-access-token",
				"client_id_ref":     "env:DBRAIN_MASTODON_CLIENT_ID",
				"client_secret_ref": "op://dbrain/mastodon/client-secret",
			},
		},
	}

	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if !cfg.Enabled || len(cfg.Accounts) != 1 {
		t.Fatalf("config = %#v, want enabled config with one account", cfg)
	}
	account := cfg.Accounts[0]
	if account.Key != "hachyderm" || !account.Enabled {
		t.Fatalf("account identity = %#v", account)
	}
	if account.Origin != "https://hachyderm.io:443" {
		t.Fatalf("canonical origin = %q", account.Origin)
	}
	metadata := account.RedactedMetadata()
	if metadata.AccessTokenRefPresent != true || metadata.ClientIDRefPresent != true || metadata.ClientSecretRefPresent != true {
		t.Fatalf("redacted secret metadata = %#v", metadata)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal redacted metadata: %v", err)
	}
	if strings.Contains(string(encoded), "mastodon-hachyderm-access-token") || strings.Contains(string(encoded), "client-secret") {
		t.Fatalf("redacted metadata leaked secret refs = %s", encoded)
	}
}

func TestParseConfigRejectsUnsafeOriginAndBareSecretRefs(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		secret string
	}{
		{name: "http origin", origin: "http://hachyderm.io", secret: "keychain://dbrain/access"},
		{name: "path origin", origin: "https://hachyderm.io/account", secret: "keychain://dbrain/access"},
		{name: "query origin", origin: "https://hachyderm.io?x=1", secret: "keychain://dbrain/access"},
		{name: "bare access token", origin: "https://hachyderm.io", secret: "plaintext-token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{
				"accounts": map[string]any{
					"account": map[string]any{
						"origin":            tc.origin,
						"access_token_ref":  tc.secret,
						"client_id_ref":     "keychain://dbrain/client-id",
						"client_secret_ref": "keychain://dbrain/client-secret",
					},
				},
			}
			if _, err := ParseConfig(raw); err == nil {
				t.Fatal("ParseConfig succeeded for unsafe account configuration")
			}
		})
	}
}

func TestValidateSecretRefRejectsStructuredReferenceWithQueryOrFragment(t *testing.T) {
	for _, ref := range []string{
		"keychain://dbrain/access?scope=all",
		"keychain://dbrain/access#fragment",
		"op://vault/item/field?version=1",
		"op://vault/item/field#fragment",
	} {
		t.Run(ref, func(t *testing.T) {
			if err := ValidateSecretRef(ref); err == nil {
				t.Fatalf("ValidateSecretRef(%q) succeeded", ref)
			}
		})
	}
}
