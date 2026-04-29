package remote

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestResolveAuthKeyPrecedenceWarning(t *testing.T) {
	t.Parallel()

	result, err := ResolveAuthKey(context.Background(), Options{
		AuthKey:        "direct",
		AuthKeyRef:     "env:TS_TEST_AUTHKEY",
		AuthKeyCommand: []string{"op", "read", "op://Private/key"},
	})
	if err != nil {
		t.Fatalf("ResolveAuthKey: %v", err)
	}
	if result.Value != "direct" {
		t.Fatalf("Value = %q, want direct", result.Value)
	}
	if result.Source != "auth_key" {
		t.Fatalf("Source = %q, want auth_key", result.Source)
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("Warnings = %#v, want 2 warnings", result.Warnings)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "direct") || strings.Contains(warning, "TS_TEST_AUTHKEY") {
			t.Fatalf("warning leaks secret detail: %q", warning)
		}
	}
}

func TestResolveAuthKeyEnvRef(t *testing.T) {
	t.Setenv("TS_TEST_AUTHKEY", "secret")

	result, err := ResolveAuthKey(context.Background(), Options{AuthKeyRef: "env:TS_TEST_AUTHKEY"})
	if err != nil {
		t.Fatalf("ResolveAuthKey: %v", err)
	}
	if result.Value != "secret" || result.Source != "auth_key_ref" {
		t.Fatalf("result = %#v", result)
	}
}

func TestResolveAuthKeyCommandRequiresOptIn(t *testing.T) {
	t.Parallel()

	_, err := ResolveAuthKey(context.Background(), Options{AuthKeyCommand: []string{"echo", "secret"}})
	if err == nil || !strings.Contains(err.Error(), "allow_secret_command") {
		t.Fatalf("err = %v, want allow_secret_command error", err)
	}
}

func TestResolveAuthKeyCommand(t *testing.T) {
	t.Parallel()

	command := []string{"printf", "secret"}
	if runtime.GOOS == "windows" {
		command = []string{"cmd", "/c", "echo secret"}
	}
	result, err := ResolveAuthKey(context.Background(), Options{
		AuthKeyCommand:     command,
		AllowSecretCommand: true,
	})
	if err != nil {
		t.Fatalf("ResolveAuthKey: %v", err)
	}
	if result.Value != "secret" || result.Source != "auth_key_command" {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseKeychainRef(t *testing.T) {
	t.Parallel()

	service, account, err := parseKeychainRef("keychain://dbrain/tsnet-auth-key")
	if err != nil {
		t.Fatalf("parseKeychainRef: %v", err)
	}
	if service != "dbrain" || account != "tsnet-auth-key" {
		t.Fatalf("service/account = %q/%q", service, account)
	}
	if _, _, err := parseKeychainRef("keychain://missing-account"); err == nil {
		t.Fatalf("expected invalid keychain ref error")
	}
}
