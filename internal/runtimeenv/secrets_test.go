package runtimeenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstNonEmptySecretResolvesConfigEnvRef(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
openrouter:
  api_key: env:TEST_DBRAIN_SECRET_REF
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DBRAIN_SECRET_REF", "resolved-secret")
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	got, err := FirstNonEmptySecret(context.Background(), root, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
	if err != nil {
		t.Fatalf("FirstNonEmptySecret: %v", err)
	}
	if got != "resolved-secret" {
		t.Fatalf("FirstNonEmptySecret = %q, want resolved-secret", got)
	}
}

func TestFirstNonEmptySecretReportsBrokenRef(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
github:
  token: env:TEST_DBRAIN_MISSING_SECRET_REF
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("TEST_DBRAIN_MISSING_SECRET_REF", "")

	_, err := FirstNonEmptySecret(context.Background(), root, "GITHUB_TOKEN")
	if err == nil || !strings.Contains(err.Error(), "TEST_DBRAIN_MISSING_SECRET_REF") {
		t.Fatalf("err = %v, want missing env ref error", err)
	}
}

func TestResolveSecretRefLeavesPlainValuesAlone(t *testing.T) {
	got, err := ResolveSecretRef(context.Background(), "plain-secret")
	if err != nil {
		t.Fatalf("ResolveSecretRef: %v", err)
	}
	if got != "plain-secret" {
		t.Fatalf("ResolveSecretRef = %q, want plain-secret", got)
	}
}

func TestParseKeychainRef(t *testing.T) {
	service, account, err := parseKeychainRef("keychain://dbrain/openrouter-api-key")
	if err != nil {
		t.Fatalf("parseKeychainRef: %v", err)
	}
	if service != "dbrain" || account != "openrouter-api-key" {
		t.Fatalf("service/account = %q/%q", service, account)
	}
	if _, _, err := parseKeychainRef("keychain://missing-account"); err == nil {
		t.Fatalf("expected invalid keychain ref error")
	}
}
